package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// EngagementPruneCounts reports rows deleted by the engagement orphan sweep.
type EngagementPruneCounts struct {
	QueueRows    int
	FavoriteRows int
	ProgressRows int
	// EventRows is engagement_event rows erased because their Media target no
	// longer exists. Only Media-targeted rows (media_id NOT NULL) are pruned by
	// Media existence; Entity-only events carry no Media reference and are never
	// orphaned by a Media purge — they clear only via the My-Data hard-delete.
	EventRows int
	// ItemActionRows is watchlist_item and item_rating rows erased because
	// their Media or Entity target no longer exists.
	ItemActionRows int
}

// PruneOrphanedEngagementRows reconciles events rows against raw catalog Media
// existence. It deliberately does not apply reveal or Library filtering:
// hidden-but-alive Media must keep queue, favorite, and playback-progress rows.
// Conversely, when Media is purged (PurgeTrashedBefore), this is what erases its
// playback_progress — watch position/watched/play-count — so a purge is durable
// and a later re-import of the same content fingerprint starts fresh rather than
// resurrecting the prior per-Profile watch state (ADR-0036 hard-DELETE).
func (s *Store) PruneOrphanedEngagementRows(ctx context.Context) (EngagementPruneCounts, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return EngagementPruneCounts{}, err
	}
	return s.pruneOrphanedEngagementRowsLocked(ctx)
}

func (s *Store) pruneOrphanedEngagementRowsLocked(ctx context.Context) (EngagementPruneCounts, error) {
	readers, allReadable, err := s.catalogReadersLocked(ctx)
	if err != nil {
		return EngagementPruneCounts{}, err
	}
	if !allReadable {
		// A sealed Encrypted Library cannot show which of its targets still
		// exist, so nothing is provably orphaned until it is open again.
		return EngagementPruneCounts{}, nil
	}
	ids, err := s.engagementMediaIDs(ctx)
	if err != nil {
		return EngagementPruneCounts{}, err
	}
	orphanEntities, err := s.orphanedItemActionEntities(ctx, readers)
	if err != nil {
		return EngagementPruneCounts{}, err
	}
	if len(ids) == 0 && len(orphanEntities) == 0 {
		return EngagementPruneCounts{}, nil
	}
	existing, err := s.existingMediaIDs(ctx, readers, ids)
	if err != nil {
		return EngagementPruneCounts{}, err
	}
	orphans := make([]MediaID, 0, len(ids)-len(existing))
	for _, id := range ids {
		if !existing[id] {
			orphans = append(orphans, id)
		}
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return EngagementPruneCounts{}, err
	}
	defer tx.Rollback()
	counts := EngagementPruneCounts{}
	for _, batch := range mediaIDBatches(orphans, 500) {
		placeholders, args := mediaIDPlaceholders(batch)
		result, err := tx.ExecContext(ctx, `DELETE FROM play_queue WHERE media_id IN (`+placeholders+`)`, args...)
		if err != nil {
			return EngagementPruneCounts{}, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return EngagementPruneCounts{}, err
		}
		counts.QueueRows += int(affected)
		result, err = tx.ExecContext(ctx, `DELETE FROM media_favorite WHERE media_id IN (`+placeholders+`)`, args...)
		if err != nil {
			return EngagementPruneCounts{}, err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return EngagementPruneCounts{}, err
		}
		counts.FavoriteRows += int(affected)
		result, err = tx.ExecContext(ctx, `DELETE FROM playback_progress WHERE media_id IN (`+placeholders+`)`, args...)
		if err != nil {
			return EngagementPruneCounts{}, err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return EngagementPruneCounts{}, err
		}
		counts.ProgressRows += int(affected)
		// The raw engagement event log. Only Media-targeted
		// rows are pruned by Media existence; Entity-only rows have a NULL media_id
		// and won't match the id list, so the `media_id IN (...)` clause already
		// excludes them — a Media purge never erases an entity-aggregate event.
		result, err = tx.ExecContext(ctx, `DELETE FROM engagement_event WHERE media_id IN (`+placeholders+`)`, args...)
		if err != nil {
			return EngagementPruneCounts{}, err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return EngagementPruneCounts{}, err
		}
		counts.EventRows += int(affected)
		for _, table := range []string{"watchlist_item", "item_rating"} {
			// #nosec G202 -- table is drawn from the closed literal list above.
			result, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE target_kind = 'media' AND target_id IN (`+placeholders+`)`, args...)
			if err != nil {
				return EngagementPruneCounts{}, err
			}
			affected, err = result.RowsAffected()
			if err != nil {
				return EngagementPruneCounts{}, err
			}
			counts.ItemActionRows += int(affected)
		}
	}
	for _, id := range orphanEntities {
		for _, table := range []string{"watchlist_item", "item_rating"} {
			// #nosec G202 -- table is drawn from the closed literal list above.
			result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE target_kind = 'entity' AND target_id = ?`, id)
			if err != nil {
				return EngagementPruneCounts{}, err
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return EngagementPruneCounts{}, err
			}
			counts.ItemActionRows += int(affected)
		}
	}
	if err := tx.Commit(); err != nil {
		return EngagementPruneCounts{}, err
	}
	return counts, nil
}

// catalogReadersLocked returns the reader of every catalog plane that can be
// read now: the shared catalog and each open Encrypted Library. allReadable is
// false when an Encrypted Library is sealed or quarantined.
func (s *Store) catalogReadersLocked(ctx context.Context) (readers []*sql.DB, allReadable bool, err error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, false, err
	}
	readers = []*sql.DB{s.catalogR}
	allReadable = true
	for _, library := range libraries {
		if !library.Encrypted {
			continue
		}
		db := s.encryptedDBs[library.ID]
		if db == nil || db.state != "unsealed-registered" || db.reader == nil {
			allReadable = false
			continue
		}
		readers = append(readers, db.reader)
	}
	return readers, allReadable, nil
}

// orphanedItemActionEntities returns the Entity targets of watchlist and rating
// rows that exist in none of the catalog planes. Like the Media sweep it tests
// raw existence, so a hidden or trashed Entity keeps its rows.
func (s *Store) orphanedItemActionEntities(ctx context.Context, readers []*sql.DB) ([]string, error) {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT target_id FROM watchlist_item WHERE target_kind = 'entity'
		UNION
		SELECT target_id FROM item_rating WHERE target_kind = 'entity'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var orphans []string
	for _, id := range ids {
		found := false
		for _, reader := range readers {
			var one int
			err := reader.QueryRowContext(ctx, `SELECT 1 FROM entity WHERE id = ?`, id).Scan(&one)
			if err == nil {
				found = true
				break
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		}
		if !found {
			orphans = append(orphans, id)
		}
	}
	return orphans, nil
}

func (s *Store) engagementMediaIDs(ctx context.Context) ([]MediaID, error) {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id FROM play_queue
		UNION
		SELECT media_id FROM media_favorite
		UNION
		SELECT media_id FROM playback_progress
		UNION
		SELECT media_id FROM engagement_event WHERE media_id IS NOT NULL
		UNION
		SELECT target_id FROM watchlist_item WHERE target_kind = 'media'
		UNION
		SELECT target_id FROM item_rating WHERE target_kind = 'media'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []MediaID
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) existingMediaIDs(ctx context.Context, readers []*sql.DB, ids []MediaID) (map[MediaID]bool, error) {
	existing := make(map[MediaID]bool, len(ids))
	for _, reader := range readers {
		for _, batch := range mediaIDBatches(ids, 500) {
			if err := markExistingMediaIDs(ctx, reader, batch, existing); err != nil {
				return nil, err
			}
		}
	}
	return existing, nil
}

func markExistingMediaIDs(ctx context.Context, reader *sql.DB, batch []MediaID, existing map[MediaID]bool) error {
	placeholders, args := mediaIDPlaceholders(batch)
	rows, err := reader.QueryContext(ctx, `SELECT id FROM media WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		existing[id] = true
	}
	// A mid-iteration error must fail the prune: a truncated result would
	// misclassify live media as orphans and hard-delete their engagement.
	return rows.Err()
}

func mediaIDBatches(ids []MediaID, size int) [][]MediaID {
	if len(ids) == 0 {
		return nil
	}
	batches := make([][]MediaID, 0, (len(ids)+size-1)/size)
	for len(ids) > 0 {
		n := size
		if len(ids) < n {
			n = len(ids)
		}
		batches = append(batches, ids[:n])
		ids = ids[n:]
	}
	return batches
}

func mediaIDPlaceholders(ids []MediaID) (string, []any) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	return strings.Join(placeholders, ","), args
}

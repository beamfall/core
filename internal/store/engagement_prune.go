package store

import (
	"context"
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
	ids, err := s.engagementMediaIDs(ctx)
	if err != nil {
		return EngagementPruneCounts{}, err
	}
	if len(ids) == 0 {
		return EngagementPruneCounts{}, nil
	}
	existing, err := s.existingMediaIDs(ctx, ids)
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
	}
	if err := tx.Commit(); err != nil {
		return EngagementPruneCounts{}, err
	}
	return counts, nil
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

func (s *Store) existingMediaIDs(ctx context.Context, ids []MediaID) (map[MediaID]bool, error) {
	existing := make(map[MediaID]bool, len(ids))
	for _, batch := range mediaIDBatches(ids, 500) {
		placeholders, args := mediaIDPlaceholders(batch)
		rows, err := s.catalogR.QueryContext(ctx, `SELECT id FROM media WHERE id IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id MediaID
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			existing[id] = true
		}
		// A mid-iteration error must fail the prune: a truncated result would
		// misclassify live media as orphans and hard-delete their engagement.
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return existing, nil
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

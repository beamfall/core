package store

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

// ArtworkAction is one pending post-commit artwork fetch for an identification
// revision: fetch SourceURL into Slot of MediaID.
type ArtworkAction struct {
	MediaID   MediaID
	Revision  int64
	Slot      string
	SourceURL string
	PluginID  string
	ScraperID string
	Attempts  int
}

// enqueueArtworkActionsTx records the identification's artwork fetches as
// pending actions of its revision, superseding the Media's older pending
// actions, inside the identification transaction.
func enqueueArtworkActionsTx(ctx context.Context, tx *sql.Tx, id MediaID, revision int64, commit IdentificationCommit, now time.Time) error {
	if len(commit.Metadata.Artwork) == 0 {
		return nil
	}
	ms := now.UnixMilli()
	if _, err := tx.ExecContext(ctx, `
		UPDATE media_artwork_action SET state = 'superseded', completed_at_ms = ?
		WHERE media_id = ? AND state = 'pending'`, ms, id); err != nil {
		return err
	}
	slots := make([]string, 0, len(commit.Metadata.Artwork))
	for slot, url := range commit.Metadata.Artwork {
		if slot != "" && url != "" {
			slots = append(slots, slot)
		}
	}
	sort.Strings(slots)
	for _, slot := range slots {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO media_artwork_action(media_id, revision, slot, source_url, plugin_id, scraper_id, created_at_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, revision, slot, commit.Metadata.Artwork[slot], commit.Provenance.PluginID, commit.Provenance.ScraperID, ms); err != nil {
			return err
		}
	}
	return nil
}

// PendingArtworkActions lists up to limit pending artwork actions, oldest
// first, from the shared catalog and every unsealed Encrypted Library plane.
// A sealed plane's actions wait until it is unsealed.
func (s *Store) PendingArtworkActions(ctx context.Context, limit int) ([]ArtworkAction, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	readers := []*sql.DB{s.catalogR}
	for _, db := range s.encryptedDBs {
		if db != nil && db.state == "unsealed-registered" && db.reader != nil {
			readers = append(readers, db.reader)
		}
	}
	nowMS := s.serverNow().UTC().UnixMilli()
	var out []ArtworkAction
	for _, reader := range readers {
		rows, err := reader.QueryContext(ctx, `
			SELECT media_id, revision, slot, source_url, plugin_id, scraper_id, attempts
			FROM media_artwork_action
			WHERE state = 'pending' AND next_attempt_at_ms <= ?
			ORDER BY next_attempt_at_ms, created_at_ms, media_id, slot LIMIT ?`, nowMS, limit-len(out))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var action ArtworkAction
			if err := rows.Scan(&action.MediaID, &action.Revision, &action.Slot, &action.SourceURL, &action.PluginID, &action.ScraperID, &action.Attempts); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out = append(out, action)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// CompleteArtworkAction moves one pending action to done with outcome. It
// reports false when the action was already completed or superseded, so of
// any number of workers or replays exactly one completion is recorded.
func (s *Store) CompleteArtworkAction(ctx context.Context, action ArtworkAction, outcome string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, action.MediaID)
	if err != nil {
		return false, err
	}
	res, err := plane.writer.ExecContext(ctx, `
		UPDATE media_artwork_action
		SET state = 'done', outcome = ?, attempts = attempts + 1, completed_at_ms = ?
		WHERE media_id = ? AND revision = ? AND slot = ? AND state = 'pending'`,
		outcome, s.serverNow().UTC().UnixMilli(), action.MediaID, action.Revision, action.Slot)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RecordArtworkActionRetry records a failed artwork attempt while keeping the
// action pending with backoff until maxAttempts is reached, at which point it
// marks the action done with the failure outcome so permanent errors stop.
func (s *Store) RecordArtworkActionRetry(ctx context.Context, action ArtworkAction, outcome string, retryAfter time.Duration, maxAttempts int) (requeued bool, err error) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, action.MediaID)
	if err != nil {
		return false, err
	}
	now := s.serverNow().UTC()
	nextMS := now.Add(retryAfter).UnixMilli()
	if maxAttempts > 1 {
		res, err := plane.writer.ExecContext(ctx, `
			UPDATE media_artwork_action
			SET outcome = ?, attempts = attempts + 1, next_attempt_at_ms = ?
			WHERE media_id = ? AND revision = ? AND slot = ? AND state = 'pending' AND attempts + 1 < ?`,
			outcome, nextMS, action.MediaID, action.Revision, action.Slot, maxAttempts)
		if err != nil {
			return false, err
		}
		if n, err := res.RowsAffected(); err != nil {
			return false, err
		} else if n == 1 {
			return true, nil
		}
	}
	_, err = plane.writer.ExecContext(ctx, `
		UPDATE media_artwork_action
		SET state = 'done', outcome = ?, attempts = attempts + 1, completed_at_ms = ?
		WHERE media_id = ? AND revision = ? AND slot = ? AND state = 'pending' AND attempts + 1 >= ?`,
		outcome, now.UnixMilli(), action.MediaID, action.Revision, action.Slot, maxAttempts)
	return false, err
}

// PendingArtworkActionsForMedia lists the Media's pending artwork actions on
// its own plane, slot order.
func (s *Store) PendingArtworkActionsForMedia(ctx context.Context, id MediaID) ([]ArtworkAction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	nowMS := s.serverNow().UTC().UnixMilli()
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT media_id, revision, slot, source_url, plugin_id, scraper_id, attempts
		FROM media_artwork_action
		WHERE media_id = ? AND state = 'pending' AND next_attempt_at_ms <= ?
		ORDER BY next_attempt_at_ms, slot`, id, nowMS)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ArtworkAction
	for rows.Next() {
		var action ArtworkAction
		if err := rows.Scan(&action.MediaID, &action.Revision, &action.Slot, &action.SourceURL, &action.PluginID, &action.ScraperID, &action.Attempts); err != nil {
			return nil, err
		}
		out = append(out, action)
	}
	return out, rows.Err()
}

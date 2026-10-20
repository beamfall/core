package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SetMediaFavorite records or clears a Profile's favorite bit for one
// visible Media item. The visible-media check runs before both insert and
// delete so hidden and absent targets are indistinguishable and hidden rows
// cannot be deleted by a relocked caller.
func (s *Store) SetMediaFavorite(ctx context.Context, profileID ProfileID, mediaID MediaID, favorite bool, filter LibraryFilter) error {
	if profileID == "" || mediaID == "" {
		return ValidationError("profile ID and media ID are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if _, err := s.visibleMediaSQL(ctx, mediaID, filter); err != nil {
		return err
	}
	if favorite {
		_, err := s.eventsW.ExecContext(ctx, `
			INSERT INTO media_favorite(profile_id, media_id, favorited_at_ms)
			VALUES (?, ?, ?)
			ON CONFLICT(profile_id, media_id) DO UPDATE SET
			  favorited_at_ms = excluded.favorited_at_ms
		`, profileID, mediaID, timeToMillis(time.Now().UTC()))
		return err
	}
	_, err := s.eventsW.ExecContext(ctx, `
		DELETE FROM media_favorite
		WHERE profile_id = ? AND media_id = ?
	`, profileID, mediaID)
	return err
}

// SetMediaKeep is the ADR-0051 retention-facing name for the same viewer-
// writable rescue bit as Favorite. Retention only consumes the aggregate
// "any Profile kept/favorited this" boolean, never which Profile set it.
func (s *Store) SetMediaKeep(ctx context.Context, profileID ProfileID, mediaID MediaID, keep bool, filter LibraryFilter) error {
	return s.SetMediaFavorite(ctx, profileID, mediaID, keep, filter)
}

// MediaFavorited reports whether profileID has marked mediaID as favorite.
func (s *Store) MediaFavorited(ctx context.Context, profileID ProfileID, mediaID MediaID) (bool, error) {
	if profileID == "" || mediaID == "" {
		return false, ValidationError("profile ID and media ID are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var one int
	err := s.eventsR.QueryRowContext(ctx, `
		SELECT 1 FROM media_favorite
		WHERE profile_id = ? AND media_id = ?
	`, profileID, mediaID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// MediaFavoritedSet reports the favorited IDs from ids in batches of at most 500.
func (s *Store) MediaFavoritedSet(ctx context.Context, profileID ProfileID, ids []MediaID) (map[MediaID]bool, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	unique := make([]MediaID, 0, len(ids))
	seen := make(map[MediaID]bool, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	return s.MediaFavorites(ctx, profileID, unique)
}

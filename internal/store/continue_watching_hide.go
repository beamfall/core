package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// SetContinueWatchingHidden removes one visible Media from profileID's
// Continue Watching shelf, or puts it back. The resume position, watched state
// and play count are untouched, and the next playback report that moves the
// position shows the item again (applyProgressProjectionEvent).
//
// The change is one engagement event applied to the playback projection in the
// same transaction, so a projection rebuild reproduces it. A Media the Profile
// has no progress row for has nothing to hide, and a call that would not change
// the stored flag writes nothing; both succeed. The visibility check runs first,
// so a hidden target is indistinguishable from a missing one.
//
// It returns the Media's item state, read inside the write transaction under
// the same lock, so a caller never reports failure for a committed change.
func (s *Store) SetContinueWatchingHidden(ctx context.Context, profileID ProfileID, mediaID MediaID, hidden bool, filter LibraryFilter) (ItemState, error) {
	// Authority face: Continue Watching state is engagement, which is
	// main-authored. An Attached node refuses.
	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return ItemState{}, err
	}
	if profileID == "" {
		return ItemState{}, ValidationError("profile ID is required")
	}
	if mediaID == "" {
		return ItemState{}, ValidationError("media ID is required")
	}
	target := ItemTarget{MediaID: mediaID}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ItemState{}, err
	}
	if err := s.ensureNotPlayerProfileLocked(ctx, profileID); err != nil {
		return ItemState{}, err
	}
	if err := s.itemTargetVisibleLocked(ctx, target, filter); err != nil {
		return ItemState{}, err
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return ItemState{}, err
	}
	defer tx.Rollback()
	var current int
	err = tx.QueryRowContext(ctx, `
		SELECT continue_hidden FROM playback_progress WHERE profile_id = ? AND media_id = ?
	`, profileID, mediaID).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ItemState{}, err
	}
	if err == nil && (current != 0) != hidden {
		event := playbackProjectionEvent{
			Operation:      playbackProjectionOperationHideContinue,
			ProfileID:      string(profileID),
			MediaID:        string(mediaID),
			UpdatedAtMS:    timeToMillis(time.Now().UTC()),
			ContinueHidden: hidden,
		}
		if err := appendPlaybackProjectionEvent(ctx, tx, event); err != nil {
			return ItemState{}, err
		}
		if err := applyPlaybackProjectionEvent(ctx, tx, event); err != nil {
			return ItemState{}, err
		}
	}
	state, err := s.itemStateLocked(ctx, tx, profileID, target, ItemTargetMedia, string(mediaID), filter)
	if err != nil {
		return ItemState{}, err
	}
	return state, tx.Commit()
}

// applyHideContinueProjectionEvent sets or clears continue_hidden on an
// existing progress row. It never creates a row and leaves updated_at_ms
// alone, so putting an item back restores its place on the shelf.
func applyHideContinueProjectionEvent(ctx context.Context, tx *sql.Tx, event playbackProjectionEvent) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE playback_progress SET continue_hidden = ?
		WHERE profile_id = ? AND media_id = ?
	`, boolToInt(event.ContinueHidden), event.ProfileID, event.MediaID)
	return err
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// hydrationBackfillPrefix prefixes the server_config rows recording that a
// Library was checked once for Media a hydration marker field is missing on,
// and a backfill scan queued if any were. An ordinary scan never hydrates an
// unchanged file, so Media imported before a mapper (or its marker field)
// existed are reached only by that one backfill.
const hydrationBackfillPrefix = "hydration-backfill."

func hydrationBackfillKey(libraryID LibraryID, markerField string) string {
	return hydrationBackfillPrefix + string(libraryID) + "." + markerField
}

// HydrationBackfillChecked reports whether a Library was already checked for
// Media lacking one hydration marker field.
func (s *Store) HydrationBackfillChecked(ctx context.Context, libraryID LibraryID, markerField string) (bool, error) {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return false, err
	}
	if libraryID == "" || markerField == "" {
		return false, errors.New("library ID and marker field are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var key string
	err := s.catalogR.QueryRowContext(ctx, `SELECT key FROM server_config WHERE key = ?`, hydrationBackfillKey(libraryID, markerField)).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// MarkHydrationBackfillChecked records that a Library was checked for Media
// lacking one hydration marker field. The caller marks only after any
// backfill scan is queued, so a crash in between checks again on the next
// start (scan dedupe absorbs a repeat).
func (s *Store) MarkHydrationBackfillChecked(ctx context.Context, libraryID LibraryID, markerField string, now time.Time) error {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if libraryID == "" || markerField == "" {
		return errors.New("library ID and marker field are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO server_config(key, value, updated_at_ms) VALUES (?, '', ?)
		ON CONFLICT(key) DO NOTHING
	`, hydrationBackfillKey(libraryID, markerField), timeToMillis(now))
	return err
}

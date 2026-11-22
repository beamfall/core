package store

import (
	"context"
	"database/sql"
	"encoding/json"
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

// HydrationTagCandidate is a live Media row missing one text tag.
type HydrationTagCandidate struct {
	MediaID        MediaID
	Path           string
	PersistedValue string
}

// HydrationTagCandidates selects each available Media's primary path and
// any old per-Media tag stored with a hydration relation. A shared Entity's
// fields cannot establish the tag for any individual Media.
func (s *Store) HydrationTagCandidates(ctx context.Context, libraryID LibraryID, classes []MediaClass, field string) ([]HydrationTagCandidate, error) {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(classes)
	if err != nil {
		return nil, err
	}
	rows, err := plane.reader.QueryContext(ctx, `
		WITH candidates AS (
			SELECT m.id, (
				SELECT f.path FROM media_file f
				WHERE f.media_id = m.id AND f.missing_at_ms IS NULL
				ORDER BY f.version_label, f.part_ordinal, f.path LIMIT 1
			) AS path
			FROM media m
			WHERE m.library_id = ? AND m.trashed_at_ms IS NULL
			  AND m.media_class IN (SELECT value FROM json_each(?))
			  AND NOT EXISTS (SELECT 1 FROM media_field_text f WHERE f.media_id = m.id AND f.field = ?)
		)
		SELECT c.id, c.path, COALESCE((
			SELECT MIN(NULLIF(json_extract(r.attributes, '$."@hydration".Value'), '') COLLATE BINARY)
			FROM entity_relation r
			WHERE r.to_media_id = c.id AND json_extract(r.attributes, '$."@hydration".Field') = ?
		), '')
		FROM candidates c WHERE c.path IS NOT NULL ORDER BY c.id
	`, libraryID, string(encoded), field, field)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []HydrationTagCandidate
	for rows.Next() {
		var candidate HydrationTagCandidate
		if err := rows.Scan(&candidate.MediaID, &candidate.Path, &candidate.PersistedValue); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

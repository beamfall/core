package store

import (
	"context"
	"database/sql"
	"errors"
)

// Per-field source-precedence locks. Each lockable
// Media metadata field carries a boolean "identified" flag: when set, a re-scan
// or scraper pass must not clobber the field — the operator's (or an
// identification pass's) value wins over the filename-derived one. The lock
// columns are media.<field>_identified, set implicitly by UpdateMediaMetadata
// and read/toggled explicitly here so the Console can read and override the
// per-field precedence. The fields are named by the generic metadata shape
// (title / summary / year), never by domain (invariant #1).

// lockableFields is the fixed set of Media metadata fields that carry a
// source-precedence lock. The order is stable so the wire list is deterministic.
// Each entry maps the wire field name to its media.<col>_identified column.
var lockableFields = []struct {
	Field  string
	Column string
}{
	{Field: "title", Column: "title_identified"},
	{Field: "summary", Column: "summary_identified"},
	{Field: "year", Column: "year_identified"},
}

func lockColumn(field string) (string, bool) {
	for _, f := range lockableFields {
		if f.Field == field {
			return f.Column, true
		}
	}
	return "", false
}

// MediaFieldLock is one field's source-precedence lock state on the wire.
type MediaFieldLock struct {
	Field  string
	Locked bool
}

// MediaFieldLocks returns the per-field source-precedence lock state for one
// visible Media, in the stable lockable-field order. A hidden or unknown Media
// answers "media not found" — the filter gates visibility exactly like Media()
// so this is never an existence oracle.
func (s *Store) MediaFieldLocks(ctx context.Context, id MediaID, filter LibraryFilter) ([]MediaFieldLock, error) {
	if id == "" {
		return nil, ValidationError("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	// Visibility first: reuse the reveal/Library-filtered read so a restricted
	// Media never reveals its lock state to an unrevealed caller.
	// The lock columns live on the catalog plane that owns the Media's
	// Library — an encrypted Library's Media are not in the default plane.
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.visibleMediaSQLFrom(ctx, plane.reader, id, filter); err != nil {
		return nil, err
	}
	locks, err := mediaFieldLocksFrom(ctx, plane.reader, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ValidationError("media not found")
	}
	return locks, err
}

// SetMediaFieldLock sets or clears one field's source-precedence lock on a
// visible Media, returning the full refreshed lock set. Setting a lock pins the
// current value against re-scans/scrapes; clearing it lets the
// next scan/scrape fill the field again. An unknown field or a hidden/unknown
// Media is rejected before any write.
func (s *Store) SetMediaFieldLock(ctx context.Context, id MediaID, field string, locked bool, filter LibraryFilter) ([]MediaFieldLock, error) {
	if id == "" {
		return nil, ValidationError("media ID is required")
	}
	column, ok := lockColumn(field)
	if !ok {
		return nil, ValidationError("unknown lockable field")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.visibleMediaSQLFrom(ctx, plane.reader, id, filter); err != nil {
		return nil, err
	}
	if _, err := plane.writer.ExecContext(ctx,
		"UPDATE media SET "+column+" = ? WHERE id = ?", boolInt(locked), id,
	); err != nil {
		return nil, err
	}
	return mediaFieldLocksFrom(ctx, plane.reader, id)
}

// mediaFieldLocksFrom reads the lock set from one catalog plane without
// re-checking visibility — the caller already gated. Held under s.mu.RLock.
func mediaFieldLocksFrom(ctx context.Context, db *sql.DB, id MediaID) ([]MediaFieldLock, error) {
	var title, summary, year int
	if err := db.QueryRowContext(ctx,
		"SELECT title_identified, summary_identified, year_identified FROM media WHERE id = ?",
		id,
	).Scan(&title, &summary, &year); err != nil {
		return nil, err
	}
	values := map[string]int{"title": title, "summary": summary, "year": year}
	locks := make([]MediaFieldLock, 0, len(lockableFields))
	for _, f := range lockableFields {
		locks = append(locks, MediaFieldLock{Field: f.Field, Locked: values[f.Field] != 0})
	}
	return locks, nil
}

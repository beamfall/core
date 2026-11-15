package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Per-field source-precedence locks. A field is locked when its recorded
// write source is manual (media_field_provenance.source = 'manual'): nothing
// below a manual edit in the precedence order (an identification, a per-item
// refresh, a re-match, a scan) may overwrite it. An admin edit locks the
// fields it writes; the Console reads and toggles the lock explicitly here.
// Fields with a legacy media.<field>_identified column keep it in step, so a
// scan import also leaves a locked value alone. The fields are named by the
// generic metadata shape (title / sortTitle / summary / year / artwork
// slots), never by domain (invariant #1); a plugin-declared field that holds a
// value on the item is lockable by its declared name.

// lockableFields is the fixed set of Media metadata fields that carry a
// source-precedence lock. The order is stable so the wire list is
// deterministic. Mirror is the constant statement that keeps the legacy
// media.<field>_identified boolean in step, or "" when the field has none.
var lockableFields = []struct {
	Field  string
	Mirror string
}{
	{Field: "title", Mirror: "UPDATE media SET title_identified = ? WHERE id = ?"},
	{Field: "sortTitle"},
	{Field: "summary", Mirror: "UPDATE media SET summary_identified = ? WHERE id = ?"},
	{Field: "year", Mirror: "UPDATE media SET year_identified = ? WHERE id = ?"},
	{Field: ArtworkLockField("poster")},
	{Field: ArtworkLockField("backdrop")},
	{Field: ArtworkLockField("thumb")},
}

// ArtworkLockField names the lock field for one artwork slot: a locked slot is
// not refetched by an identification or a per-item refresh.
func ArtworkLockField(slot string) string {
	if slot == "" {
		return "artwork"
	}
	return "artwork" + strings.ToUpper(slot[:1]) + slot[1:]
}

func lockMirror(field string) (string, bool) {
	for _, f := range lockableFields {
		if f.Field == field {
			return f.Mirror, true
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
// visible Media: the fixed lockable fields in their stable order, then every
// other locked field (plugin-declared fields an admin edited) by name. A
// hidden or unknown Media answers "media not found" — the filter gates
// visibility exactly like Media() so this is never an existence oracle.
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
	if _, err := s.visibleMediaSQL(ctx, id, filter); err != nil {
		return nil, err
	}
	return s.mediaFieldLocksLocked(ctx, id)
}

// SetMediaFieldLock sets or clears one field's source-precedence lock on a
// visible Media, returning the full refreshed lock set. Setting a lock raises
// the field's recorded source to manual, pinning the current value against
// identifications, refreshes, re-matches and scans; clearing it drops the
// record so the next of those fills the field again. An unknown field or a
// hidden/unknown Media is rejected before any write.
func (s *Store) SetMediaFieldLock(ctx context.Context, id MediaID, field string, locked bool, filter LibraryFilter) ([]MediaFieldLock, error) {
	if id == "" {
		return nil, ValidationError("media ID is required")
	}
	if field == "" {
		return nil, ValidationError("unknown lockable field")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	if _, err := s.visibleMediaSQL(ctx, id, filter); err != nil {
		return nil, err
	}
	mirror, fixed := lockMirror(field)
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if !fixed {
		held, err := mediaFieldHeldTx(ctx, tx, id, field)
		if err != nil {
			return nil, err
		}
		if !held {
			return nil, ValidationError("unknown lockable field")
		}
	}
	if locked {
		err = recordFieldProvenanceTx(ctx, tx, id, field, MutationSourceManual)
	} else {
		_, err = tx.ExecContext(ctx,
			"DELETE FROM media_field_provenance WHERE media_id = ? AND field = ?", id, field)
	}
	if err != nil {
		return nil, err
	}
	if mirror != "" {
		if _, err := tx.ExecContext(ctx, mirror, boolInt(locked), id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.mediaFieldLocksLocked(ctx, id)
}

// mediaFieldHeldTx reports whether a plugin-declared field holds a value or a
// provenance record on the Media, which makes it lockable by name.
func mediaFieldHeldTx(ctx context.Context, tx *sql.Tx, id MediaID, field string) (bool, error) {
	tables := append([]string{"media_field_provenance"}, mediaFieldTables...)
	for _, table := range tables {
		var one int
		err := tx.QueryRowContext(ctx,
			"SELECT 1 FROM "+table+" WHERE media_id = ? AND field = ? LIMIT 1", id, field).Scan(&one)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	return false, nil
}

// mediaFieldLocksLocked reads the lock set without re-checking visibility — the
// caller already gated. Held under s.mu.RLock.
func (s *Store) mediaFieldLocksLocked(ctx context.Context, id MediaID) ([]MediaFieldLock, error) {
	rows, err := s.catalogR.QueryContext(ctx,
		"SELECT field FROM media_field_provenance WHERE media_id = ? AND source = ? ORDER BY field",
		id, string(MutationSourceManual))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	manual := map[string]bool{}
	var order []string
	for rows.Next() {
		var field string
		if err := rows.Scan(&field); err != nil {
			return nil, err
		}
		manual[field] = true
		order = append(order, field)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	locks := make([]MediaFieldLock, 0, len(lockableFields)+len(order))
	for _, f := range lockableFields {
		locks = append(locks, MediaFieldLock{Field: f.Field, Locked: manual[f.Field]})
	}
	for _, field := range order {
		if _, fixed := lockMirror(field); !fixed {
			locks = append(locks, MediaFieldLock{Field: field, Locked: true})
		}
	}
	return locks, nil
}

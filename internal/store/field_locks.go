package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Per-field source-precedence locks. A field is locked when its recorded
// write source is manual or local (media_field_provenance.source): nothing
// below it in the precedence order (an identification, a per-item refresh, a
// re-match, a scan) may overwrite it. Local values come from sidecar files the
// operator keeps beside the media and unlock when the sidecar goes away. An admin edit locks the
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
	{Field: ArtworkLockField("logo")},
	{Field: ArtworkLockField("banner")},
	{Field: ArtworkLockField("clearart")},
	{Field: ArtworkLockField("disc")},
	{Field: ArtworkLockField("season-poster")},
}

// ArtworkLockField names the lock field for one artwork slot: a locked slot is
// not refetched by an identification or a per-item refresh. Hyphenated slots
// camel-case ("season-poster" locks as "artworkSeasonPoster"). The slot list
// above mirrors artwork.KnownSlots, which the store cannot import; a graph
// test keeps the two in step.
func ArtworkLockField(slot string) string {
	var b strings.Builder
	b.WriteString("artwork")
	for _, part := range strings.Split(slot, "-") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
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
	// Local reports a lock held by a local sidecar rather than an operator: it
	// lifts only when the sidecar stops stating the field, so it is not one an
	// operator clears.
	Local bool
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
	// The lock records live on the catalog plane that owns the Media's
	// Library — an encrypted Library's Media are not in the default plane.
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.visibleMediaSQLFrom(ctx, plane.reader, id, filter); err != nil {
		return nil, err
	}
	return mediaFieldLocksFrom(ctx, plane.reader, id)
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
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.visibleMediaSQLFrom(ctx, plane.reader, id, filter); err != nil {
		return nil, err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := setMediaFieldLockTx(ctx, tx, id, field, locked, false); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return mediaFieldLocksFrom(ctx, plane.reader, id)
}

func setMediaFieldLockTx(ctx context.Context, tx *sql.Tx, id MediaID, field string, locked, onlyManual bool) error {
	mirror, fixed := lockMirror(field)
	if !fixed {
		held, err := mediaFieldHeldTx(ctx, tx, id, field)
		if err != nil {
			return err
		}
		if !held {
			return ValidationError("unknown lockable field")
		}
	}
	if onlyManual {
		// A stale clearLocks selection must preserve an already unlocked field's provenance.
		source, err := fieldProvenanceTx(ctx, tx, id, field)
		if err != nil || source != MutationSourceManual {
			return err
		}
	}
	var err error
	if locked {
		err = recordFieldProvenanceTx(ctx, tx, id, field, MutationSourceManual)
	} else {
		_, err = tx.ExecContext(ctx,
			"DELETE FROM media_field_provenance WHERE media_id = ? AND field = ?", id, field)
	}
	if err != nil {
		return err
	}
	if mirror != "" {
		_, err = tx.ExecContext(ctx, mirror, boolInt(locked), id)
	}
	return err
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

// mediaFieldLocksFrom reads the lock set from one catalog plane without
// re-checking visibility — the caller already gated. Held under s.mu.RLock.
func mediaFieldLocksFrom(ctx context.Context, db *sql.DB, id MediaID) ([]MediaFieldLock, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT field, source FROM media_field_provenance WHERE media_id = ? AND source IN (?, ?) ORDER BY field",
		id, string(MutationSourceManual), string(MutationSourceLocal))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	locked := map[string]bool{}
	local := map[string]bool{}
	var order []string
	for rows.Next() {
		var field, source string
		if err := rows.Scan(&field, &source); err != nil {
			return nil, err
		}
		locked[field] = true
		local[field] = source == string(MutationSourceLocal)
		order = append(order, field)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	locks := make([]MediaFieldLock, 0, len(lockableFields)+len(order))
	for _, f := range lockableFields {
		locks = append(locks, MediaFieldLock{Field: f.Field, Locked: locked[f.Field], Local: local[f.Field]})
	}
	for _, field := range order {
		if _, fixed := lockMirror(field); !fixed {
			locks = append(locks, MediaFieldLock{Field: field, Locked: true, Local: local[field]})
		}
	}
	return locks, nil
}

package store

import (
	"context"
	"database/sql"
	"errors"
)

// The media_field_provenance.parser_owned markers (catalog 0123, encrypted
// library 0009). Any provenance write other than a parser refresh resets the
// marker to parserOwnedNone, so a field identification, an NFO import, a manual
// edit, hydration or local metadata wrote is never refreshed by a rescan.
const (
	parserOwnedNone = 0
	// parserOwnedStable is the parser's output independent of the media
	// probe: every rescan parse refreshes it.
	parserOwnedStable = 1
	// parserOwnedLegacy is a scan-tier class written before the marker
	// existed: the next authoritative parse replaces it, or removes it when
	// the parse emits no class. The only other scan-tier writer of a class is
	// image local metadata, which every scan re-applies; hydration does not
	// write it, and identification and NFO imports write at the operator tier.
	parserOwnedLegacy = 2
	// parserOwnedProbe is the parser's output that changes with the media
	// probe: only a probed parse refreshes it.
	parserOwnedProbe = 3
	// parserOwnedProbeAbsent is a value-less row: a probed parse emitted no
	// such field where a parse without the probe would. Only a probed parse
	// may add the field back, so a later unprobed parse cannot undo it.
	parserOwnedProbeAbsent = 4
)

// ParserFieldRefresh is the filename parser's whole Media field output for one
// file on a scan (CORE-2554).
type ParserFieldRefresh struct {
	// Fields is every field the parse emitted.
	Fields []MediaField
	// ProbeDependent names the Fields whose value came from the media probe
	// (the parse differs without it).
	ProbeDependent map[string]bool
	// ProbeAbsent names fields a parse without the probe emits that this
	// probed parse does not: the probe decided their absence.
	ProbeAbsent map[string]bool
	// Authoritative is true when the parse saw the probe the full import
	// would see, so probe-dependent and legacy fields may be settled by it.
	Authoritative bool
}

// RefreshParserMediaFields applies one rescan parse to the fields the parser
// owns: a changed value is replaced, a field the parser no longer emits is
// removed, and a new field is added when no other writer holds it. Fields
// another writer last won (identification, NFO, manual, hydration, local
// metadata) and tombstoned values are left alone; a tombstoned value counts as
// not emitted. A non-authoritative parse (no probe) leaves probe-dependent and
// legacy fields as they are; an authoritative one settles both, and records a
// probe-decided absence (ProbeAbsent) so a later unprobed parse cannot add the
// field back.
func (s *Store) RefreshParserMediaFields(ctx context.Context, id MediaID, refresh ParserFieldRefresh) error {
	if id == "" {
		return errors.New("media ID is required")
	}
	if err := validateMediaFields(refresh.Fields); err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	owned, err := parserOwnedFieldsTx(ctx, tx, id)
	if err != nil {
		return err
	}
	// A probe-decided absence holds no value, so a value under one came from
	// another writer (an approved AI tag, an undo, an edit): the parser no
	// longer owns the field, and the stale absence is dropped.
	for name, marker := range owned {
		if marker != parserOwnedProbeAbsent {
			continue
		}
		if _, exists, err := mediaFieldValueTx(ctx, tx, id, name); err != nil {
			return err
		} else if !exists {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM media_field_provenance WHERE media_id = ? AND field = ? AND parser_owned = ?",
			id, name, parserOwnedProbeAbsent); err != nil {
			return err
		}
		delete(owned, name)
	}
	refreshable := func(marker int) bool {
		return marker == parserOwnedStable || (refresh.Authoritative && marker != parserOwnedNone)
	}
	changed := false
	emitted := make(map[string]bool, len(refresh.Fields))
	for _, field := range refresh.Fields {
		tombstoned, err := mediaFieldTombstonedTx(ctx, tx, id, field)
		if err != nil {
			return err
		}
		if tombstoned {
			continue
		}
		marker := parserOwnedStable
		if refresh.ProbeDependent[field.Name] {
			marker = parserOwnedProbe
		}
		current, exists, err := mediaFieldValueTx(ctx, tx, id, field.Name)
		if err != nil {
			return err
		}
		stored, isOwned := owned[field.Name]
		if exists && (!isOwned || !refreshable(stored)) {
			continue
		}
		if stored == parserOwnedProbeAbsent && !refresh.Authoritative {
			continue
		}
		emitted[field.Name] = true
		if exists && mediaFieldValuesEqual(current, field) {
			if stored != marker {
				if _, err := tx.ExecContext(ctx,
					"UPDATE media_field_provenance SET parser_owned = ? WHERE media_id = ? AND field = ?",
					marker, id, field.Name); err != nil {
					return err
				}
			}
			continue
		}
		allowed, err := canWriteFieldTx(ctx, tx, id, field.Name, MutationSourceScan)
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		if err := deleteMediaFieldValueTx(ctx, tx, id, field.Name); err != nil {
			return err
		}
		if err := insertMediaFields(ctx, tx, id, []MediaField{field}); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO media_field_provenance(media_id, field, source, parser_owned) VALUES (?, ?, ?, ?)
			ON CONFLICT(media_id, field) DO UPDATE SET source = excluded.source, parser_owned = excluded.parser_owned
		`, id, field.Name, string(MutationSourceScan), marker); err != nil {
			return err
		}
		changed = true
	}
	for name, marker := range owned {
		if emitted[name] || !refreshable(marker) {
			continue
		}
		if marker != parserOwnedProbeAbsent {
			if err := deleteMediaFieldValueTx(ctx, tx, id, name); err != nil {
				return err
			}
			changed = true
		}
		if refresh.Authoritative && refresh.ProbeAbsent[name] {
			if _, err := tx.ExecContext(ctx,
				"UPDATE media_field_provenance SET parser_owned = ? WHERE media_id = ? AND field = ?",
				parserOwnedProbeAbsent, id, name); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM media_field_provenance WHERE media_id = ? AND field = ?", id, name); err != nil {
			return err
		}
	}
	if refresh.Authoritative {
		// A probe-decided absence of a field no one holds yet is recorded too,
		// so the first unprobed rescan after a probed import cannot add it.
		for name := range refresh.ProbeAbsent {
			if _, isOwned := owned[name]; isOwned || emitted[name] {
				continue
			}
			if _, exists, err := mediaFieldValueTx(ctx, tx, id, name); err != nil {
				return err
			} else if exists {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO media_field_provenance(media_id, field, source, parser_owned)
				SELECT ?, ?, ?, ?
				WHERE NOT EXISTS (SELECT 1 FROM media_field_provenance WHERE media_id = ? AND field = ?)
			`, id, name, string(MutationSourceScan), parserOwnedProbeAbsent, id, name); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if changed && !plane.encrypted {
		s.emitMediaUpdated(ctx, id)
	}
	return nil
}

// LibraryFilePathsWithLegacyParserFields returns the available file paths of
// the Library's Media that still hold a legacy parser field (one written
// before parser ownership was recorded). Scan probes these files once, even on
// its fast path, so their next parse is authoritative and settles them.
func (s *Store) LibraryFilePathsWithLegacyParserFields(ctx context.Context, libraryID LibraryID) (map[string]struct{}, error) {
	if libraryID == "" {
		return nil, errors.New("library ID is required")
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
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT f.path
		FROM media m
		JOIN media_file f ON f.media_id = m.id AND f.missing_at_ms IS NULL
		WHERE m.library_id = ?
		  AND EXISTS (
		    SELECT 1 FROM media_field_provenance p
		    WHERE p.media_id = m.id AND p.parser_owned = ?
		  )
	`, libraryID, parserOwnedLegacy)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		out[path] = struct{}{}
	}
	return out, rows.Err()
}

// parserOwnedFieldsTx returns the Media's parser-owned fields and their
// markers.
func parserOwnedFieldsTx(ctx context.Context, tx *sql.Tx, id MediaID) (map[string]int, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT field, parser_owned FROM media_field_provenance WHERE media_id = ? AND parser_owned != ?",
		id, parserOwnedNone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owned := map[string]int{}
	for rows.Next() {
		var name string
		var marker int
		if err := rows.Scan(&name, &marker); err != nil {
			return nil, err
		}
		owned[name] = marker
	}
	return owned, rows.Err()
}

// mediaFieldValueTx reads one field's current value from whichever typed side
// table holds it.
func mediaFieldValueTx(ctx context.Context, tx *sql.Tx, id MediaID, name string) (MediaField, bool, error) {
	field := MediaField{Name: name}
	for _, table := range mediaFieldTables {
		column := "value"
		if table == "media_field_date" {
			column = "value_ms"
		}
		row := tx.QueryRowContext(ctx,
			`SELECT field_type, `+column+` FROM `+table+` WHERE media_id = ? AND field = ?`, id, name) // #nosec G202 -- table and column come from the fixed mediaFieldTables list
		var err error
		switch table {
		case "media_field_text":
			err = row.Scan(&field.Type, &field.Text)
		case "media_field_real":
			err = row.Scan(&field.Type, &field.Number)
		case "media_field_int":
			var value int64
			if err = row.Scan(&field.Type, &value); err == nil {
				field.Flag = value != 0
			}
		case "media_field_date":
			var millis int64
			if err = row.Scan(&field.Type, &millis); err == nil {
				field.Date = millisToTime(millis)
			}
		}
		if err == nil {
			return field, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return MediaField{}, false, err
		}
	}
	return MediaField{}, false, nil
}

func mediaFieldValuesEqual(a, b MediaField) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case EntityFieldText, EntityFieldURL:
		return a.Text == b.Text
	case EntityFieldNumber:
		return a.Number == b.Number
	case EntityFieldFlag:
		return a.Flag == b.Flag
	case EntityFieldDate:
		return timeToMillis(a.Date.UTC()) == timeToMillis(b.Date.UTC())
	default:
		return false
	}
}

func deleteMediaFieldValueTx(ctx context.Context, tx *sql.Tx, id MediaID, name string) error {
	for _, table := range mediaFieldTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE media_id = ? AND field = ?`, id, name); err != nil { // #nosec G202 -- table comes from the fixed mediaFieldTables list
			return err
		}
	}
	return nil
}

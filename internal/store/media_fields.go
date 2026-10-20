package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"time"
)

// MediaField is one plugin-declared field value on a Media item (the
// Bundle.MediaFields contract). Exactly the member matching Type carries the
// value (Text serves both text and url), like EntityField.
type MediaField struct {
	Name   string
	Type   EntityFieldType
	Text   string
	Number float64
	Flag   bool
	Date   time.Time
}

var mediaFieldTables = []string{
	"media_field_int", "media_field_real", "media_field_text", "media_field_date",
}

// validateMediaFields checks one write's field values: names present and
// unique, types from the fixed vocabulary, dates non-zero.
func validateMediaFields(fields []MediaField) error {
	names := map[string]bool{}
	for _, field := range fields {
		if field.Name == "" {
			return errors.New("media field name is required")
		}
		if names[field.Name] {
			return errors.New("duplicate media field " + field.Name)
		}
		names[field.Name] = true
		switch field.Type {
		case EntityFieldText, EntityFieldNumber, EntityFieldFlag, EntityFieldURL:
		case EntityFieldDate:
			if field.Date.IsZero() {
				return errors.New("media date field " + field.Name + " requires a value")
			}
		default:
			return errors.New("unknown media field type " + string(field.Type))
		}
	}
	return nil
}

// SetMediaFields fully replaces one Media item's declared field values. Like
// UpsertEntity this is a server-internal write path (scan, identification) —
// reads are reveal-filtered, writes are not.
func (s *Store) SetMediaFields(ctx context.Context, id MediaID, fields []MediaField) error {
	if id == "" {
		return errors.New("media ID is required")
	}
	if err := validateMediaFields(fields); err != nil {
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
	current, err := mediaFieldsTx(ctx, tx, id)
	if err != nil {
		return err
	}
	incoming := fieldNameSet(fields)
	for _, field := range current {
		if incoming[field.Name] {
			continue
		}
		if err := rejectMediaFieldValueTx(ctx, tx, id, field); err != nil {
			return err
		}
		if err := recordFieldProvenanceTx(ctx, tx, id, field.Name, MutationSourceManual); err != nil {
			return err
		}
	}
	for _, table := range mediaFieldTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE media_id = ?`, id); err != nil {
			return err
		}
	}
	if err := insertMediaFields(ctx, tx, id, fields); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !plane.encrypted {
		s.emitMediaUpdated(ctx, id)
	}
	return nil
}

// emitMediaUpdated records a media.updated realtime event for a field write that
// already committed (cross-DB ordering: see appendMediaEventLocked). Callers
// must hold s.mu and have passed guardLocked (the surrounding write path does).
// A failed outbox append never fails the write — the values are persisted; the
// worst case is a missed live push the next read reconciles.
func (s *Store) emitMediaUpdated(ctx context.Context, id MediaID) {
	if eventErr := s.appendMediaEventLocked(ctx, MediaEventUpdated, id, nil, time.Now().UTC()); eventErr != nil {
		slog.Warn("media outbox event append failed", "kind", MediaEventUpdated, "media", id, "error", eventErr)
	}
}

// MediaFieldProvenance returns the recorded write source for one Media field.
func (s *Store) MediaFieldProvenance(ctx context.Context, id MediaID, field string) (MutationSource, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return "", err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	source, err := fieldProvenanceTx(ctx, tx, id, field)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return source, nil
}

// fieldProvenanceTx reads one field's recorded write source within the caller's
// transaction. A field with no provenance row reads back as the parsed/scan
// default ("") — metadataSourceRank ranks "" as parsed, so an un-recorded field
// behaves exactly as it did before the provenance record existed.
func fieldProvenanceTx(ctx context.Context, tx *sql.Tx, id MediaID, field string) (MutationSource, error) {
	var source string
	err := tx.QueryRowContext(ctx,
		"SELECT source FROM media_field_provenance WHERE media_id = ? AND field = ?", id, field,
	).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return MutationSource(source), nil
}

// recordFieldProvenanceTx UPSERTs the (media, field, source) provenance record
// within the caller's transaction, but only raises it — a lower-rank source
// never demotes a field's recorded provenance. It is the write half of the
// source-precedence gate: callers first check canWriteFieldTx to decide whether
// the value itself lands, then call this to record the winning source.
func recordFieldProvenanceTx(ctx context.Context, tx *sql.Tx, id MediaID, field string, source MutationSource) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_field_provenance(media_id, field, source) VALUES (?, ?, ?)
		ON CONFLICT(media_id, field) DO UPDATE SET source = excluded.source
		WHERE ? >= (SELECT rank FROM (`+sourceRankValues()+`) WHERE src = media_field_provenance.source)
	`, id, field, string(source), metadataSourceRank(source))
	return err
}

// sourceRankValues renders the metadataSourceRank table as an inline SQL VALUES
// list (src, rank) so the provenance UPSERT can compare the incoming rank to the
// stored source's rank without a Go round-trip. Kept in lockstep with
// metadataSourceRank — the one Go source of truth for the ordering.
func sourceRankValues() string {
	return `SELECT 'manual' AS src, 4 AS rank
	  UNION ALL SELECT 'operator', 3
	  UNION ALL SELECT 'scan', 2
	  UNION ALL SELECT 'hydrated', 1
	  UNION ALL SELECT 'machine-derived', 0`
}

// canWriteFieldTx reports whether a write of the given source may overwrite one
// field's current value, per the total precedence order: a write
// lands only when its source rank >= the field's recorded source rank. An
// un-recorded field (no provenance row) reads as parsed.
func canWriteFieldTx(ctx context.Context, tx *sql.Tx, id MediaID, field string, source MutationSource) (bool, error) {
	stored, err := fieldProvenanceTx(ctx, tx, id, field)
	if err != nil {
		return false, err
	}
	return metadataSourceRank(source) >= metadataSourceRank(stored), nil
}

func mediaFieldTombstonedTx(ctx context.Context, tx *sql.Tx, id MediaID, field MediaField) (bool, error) {
	value := mediaFieldTombstoneValue(field)
	var one int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM media_field_rejection
		WHERE media_id = ? AND field = ? AND field_type IN ('', ?) AND value = ?
		LIMIT 1`, id, field.Name, field.Type, value).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func rejectMediaFieldValueTx(ctx context.Context, tx *sql.Tx, id MediaID, field MediaField) error {
	value := mediaFieldTombstoneValue(field)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_field_rejection(media_id, field, field_type, value, rejected_at_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(media_id, field, field_type, value)
		DO UPDATE SET rejected_at_ms = excluded.rejected_at_ms
	`, id, field.Name, field.Type, value, time.Now().UTC().UnixMilli())
	return err
}

func mediaFieldTombstoneValue(field MediaField) string {
	switch field.Type {
	case EntityFieldText, EntityFieldURL:
		return field.Text
	case EntityFieldNumber:
		return strconv.FormatFloat(field.Number, 'g', -1, 64)
	case EntityFieldFlag:
		return strconv.FormatBool(field.Flag)
	case EntityFieldDate:
		return field.Date.UTC().Format("2006-01-02")
	default:
		return ""
	}
}

// insertMediaFields inserts validated field values into their typed side
// tables within the caller's transaction.
func insertMediaFields(ctx context.Context, tx *sql.Tx, id MediaID, fields []MediaField) error {
	for _, field := range fields {
		var table string
		var value any
		switch field.Type {
		case EntityFieldText, EntityFieldURL:
			table, value = "media_field_text", field.Text
		case EntityFieldNumber:
			table, value = "media_field_real", field.Number
		case EntityFieldFlag:
			table, value = "media_field_int", boolToInt(field.Flag)
		case EntityFieldDate:
			table, value = "media_field_date", timeToMillis(field.Date.UTC())
		}
		column := "value"
		if table == "media_field_date" {
			column = "value_ms"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO `+table+`(media_id, field, field_type, `+column+`) VALUES (?, ?, ?, ?)
		`, id, field.Name, field.Type, value); err != nil {
			return err
		}
	}
	return nil
}

// UpsertMediaFields writes the given field values, replacing only fields with
// the same name — values other writers set (scan vs identification) survive.
// SetMediaFields is the full-replace twin. Server-internal write path.
//
// The bare form writes at the parsed (scan) tier: its production caller is the
// scan-side local-metadata extractor (internal/scan), a filename/sidecar source.
// Source-aware callers (identification) use UpsertMediaFieldsWithSource so a
// higher-tier source is recorded and a manual/operator field is not clobbered.
func (s *Store) UpsertMediaFields(ctx context.Context, id MediaID, fields []MediaField) error {
	return s.UpsertMediaFieldsWithSource(ctx, id, fields, MutationSourceScan)
}

// UpsertMediaFieldsWithSource is the source-aware upsert: each
// field lands only when the write's source rank >= the field's recorded source
// rank, and every landed field's (media, field, source) provenance is recorded.
// A field last written by a higher tier (a manual edit, or an operator
// identification vs a scan re-write) is preserved rather than clobbered, closing
// the re-identify / scheduled-re-match clobber gap for plugin fields.
func (s *Store) UpsertMediaFieldsWithSource(ctx context.Context, id MediaID, fields []MediaField, source MutationSource) error {
	if id == "" {
		return errors.New("media ID is required")
	}
	if err := validateMediaFields(fields); err != nil {
		return err
	}
	if len(fields) == 0 {
		return nil
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
	wrote := 0
	for _, field := range fields {
		if source != MutationSourceManual {
			tombstoned, err := mediaFieldTombstonedTx(ctx, tx, id, field)
			if err != nil {
				return err
			}
			if tombstoned {
				continue
			}
		}
		allowed, err := canWriteFieldTx(ctx, tx, id, field.Name, source)
		if err != nil {
			return err
		}
		if !allowed {
			continue // a higher-precedence source owns this field — preserve it
		}
		// A field's type may change across writes, so the old value is
		// cleared from every side table, not just the new type's.
		for _, table := range mediaFieldTables {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE media_id = ? AND field = ?`, id, field.Name); err != nil {
				return err
			}
		}
		if err := insertMediaFields(ctx, tx, id, []MediaField{field}); err != nil {
			return err
		}
		if err := recordFieldProvenanceTx(ctx, tx, id, field.Name, source); err != nil {
			return err
		}
		wrote++
	}
	if wrote == 0 {
		return nil
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !plane.encrypted {
		s.emitMediaUpdated(ctx, id)
	}
	return nil
}

// InsertMediaFieldsIfAbsent writes only fields with no existing (media_id,
// field) row in any typed side table — identification's UpsertMediaFields always
// wins, so a re-scan parse-hook write never clobbers it. Fields
// that already exist are skipped; an empty effective set is a no-op.
// Server-internal write path.
func (s *Store) InsertMediaFieldsIfAbsent(ctx context.Context, id MediaID, fields []MediaField) error {
	if id == "" {
		return errors.New("media ID is required")
	}
	if err := validateMediaFields(fields); err != nil {
		return err
	}
	if len(fields) == 0 {
		return nil
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

	toInsert := make([]MediaField, 0, len(fields))
	for _, field := range fields {
		tombstoned, err := mediaFieldTombstonedTx(ctx, tx, id, field)
		if err != nil {
			return err
		}
		if tombstoned {
			continue
		}
		exists := false
		for _, table := range mediaFieldTables {
			var one int
			err := tx.QueryRowContext(ctx, `SELECT 1 FROM `+table+` WHERE media_id = ? AND field = ? LIMIT 1`, id, field.Name).Scan(&one)
			if err == nil {
				exists = true
				break
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if !exists {
			allowed, err := canWriteFieldTx(ctx, tx, id, field.Name, MutationSourceScan)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			toInsert = append(toInsert, field)
		}
	}
	if len(toInsert) == 0 {
		return nil
	}
	if err := insertMediaFields(ctx, tx, id, toInsert); err != nil {
		return err
	}
	// Record provenance for the truly-absent fields this inserts. These are
	// scan-side parse-hook / hydration fills; recording the parsed
	// tier keeps the (media, field, source) record complete without demoting
	// anything — an absent field has no prior provenance row.
	for _, field := range toInsert {
		if err := recordFieldProvenanceTx(ctx, tx, id, field.Name, MutationSourceScan); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !plane.encrypted {
		s.emitMediaUpdated(ctx, id)
	}
	return nil
}

// MediaFields returns one Media item's declared field values, only when the
// Media is visible under the provided filter (the Reveal contract).
func (s *Store) MediaFields(ctx context.Context, id MediaID, filter LibraryFilter) ([]MediaField, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	if _, err := s.visibleMediaSQL(ctx, id, filter); err != nil {
		return nil, err
	}
	return s.mediaFieldsSQL(ctx, id)
}

// mediaFieldsSQL loads one Media item's field values from the four typed side
// tables, sorted by field name for a deterministic shape. The side tables live
// in the Media's own catalog plane (the global catalog or the per-Library
// encrypted DB — ADR-0045 §2.3), so the read resolves the plane first; reading
// s.catalogR unconditionally would return no fields for an Encrypted Library.
func (s *Store) mediaFieldsSQL(ctx context.Context, id MediaID) ([]MediaField, error) {
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	fields := []MediaField{}
	for _, table := range mediaFieldTables {
		column := "value"
		if table == "media_field_date" {
			column = "value_ms"
		}
		rows, err := plane.reader.QueryContext(ctx,
			`SELECT field, field_type, `+column+` FROM `+table+` WHERE media_id = ?`, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var field MediaField
			switch table {
			case "media_field_text":
				err = rows.Scan(&field.Name, &field.Type, &field.Text)
			case "media_field_real":
				err = rows.Scan(&field.Name, &field.Type, &field.Number)
			case "media_field_int":
				var value int64
				if err = rows.Scan(&field.Name, &field.Type, &value); err == nil {
					field.Flag = value != 0
				}
			case "media_field_date":
				var millis int64
				if err = rows.Scan(&field.Name, &field.Type, &millis); err == nil {
					field.Date = millisToTime(millis)
				}
			}
			if err != nil {
				rows.Close()
				return nil, err
			}
			fields = append(fields, field)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	return fields, nil
}

// MediaLackingFieldInLibrary returns the set of Media IDs in a Library that are
// of the given class, are live (their primary file is present and the Media is
// not trashed), and have no value for fieldName — the scan backfill stale set.
// The field is a flag in media_field_int, so the NOT EXISTS probes
// that table; a non-flag field would need the matching typed side table. The
// result is only the *unmarked* subset, never the whole Library, so it is
// bounded by the number of un-hydrated files (zero once a Library is fully
// hydrated). fieldName is a parameter, not a hardcoded marker, so the store
// stays free of music vocabulary (invariant 1).
func (s *Store) MediaLackingFieldInLibrary(ctx context.Context, libraryID LibraryID, class MediaClass, fieldName string) (map[MediaID]struct{}, error) {
	if libraryID == "" {
		return nil, errors.New("library ID is required")
	}
	if fieldName == "" {
		return nil, errors.New("field name is required")
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
		SELECT m.id
		FROM media m
		WHERE m.library_id = ?
		  AND m.media_class = ?
		  AND m.trashed_at_ms IS NULL
		  AND EXISTS (
		    SELECT 1 FROM media_file f
		    WHERE f.media_id = m.id AND f.missing_at_ms IS NULL
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM media_field_int i
		    WHERE i.media_id = m.id AND i.field = ?
		  )
	`, libraryID, class, fieldName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[MediaID]struct{}{}
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

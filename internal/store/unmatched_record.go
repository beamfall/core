package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// Unmatched-Record failure-reason taxonomy (ADR-0068 Amendment §A). These are
// shape words the Console renders as "why this candidate" — never domain
// vocabulary (invariant #1).
const (
	UnmatchedReasonNoCandidates       = "no-candidates"
	UnmatchedReasonBelowThreshold     = "below-threshold"
	UnmatchedReasonAmbiguous          = "ambiguous"
	UnmatchedReasonLibraryMismatch    = "library-mismatch"
	UnmatchedReasonExternalIDConflict = "external-id-conflict"
)

// Unmatched-Record origins (ADR-0068: "one review inbox … regardless of whether
// the record came from scan, parser, scraper, migration import, feed, or manual
// upload"). Shape words; the importer/plugin id carries species.
const (
	UnmatchedOriginScan   = "scan"
	UnmatchedOriginImport = "import"
)

var (
	ErrUnmatchedRecordNotFound = errors.New("unmatched record not found")
	ErrUnmatchedCreateConflict = errors.New("unmatched record create conflicts with prior request")
)

// UnmatchedRecord is the Media-INDEPENDENT Unmatched Record ADR-0068 mandates: a
// "stable Unmatched Record identity separate from Media identity." Unlike
// UnmatchedItem (the Media-keyed scan queue), a record may back NO Media at all —
// a migration-import item that resolved to nothing (ADR-0037 §2.3 step 4) is a
// real reviewable record with its own opaque id, no MediaID required. The shape
// is generic (invariant #1): Origin/FailureReason are shape words, and the
// ImporterID carries the species verbatim, never a domain term.
type UnmatchedRecord struct {
	// ID is the opaque Media-independent identity (urec-<hex>). Empty on the way
	// in — QueueUnmatchedRecord mints it and returns it.
	ID     string
	Origin string
	// LibraryID is the tentative owning Library; empty means operator-global (no
	// Library yet), visible only to an all-Libraries (ops/admin) scope.
	LibraryID LibraryID
	// MediaID is the tentatively-attached Media; empty for a pure no-Media record.
	MediaID        MediaID
	ImportRunID    string
	ImporterID     string
	SourceHandle   string
	CandidateID    string
	CandidateTitle string
	Confidence     float64
	FailureReason  string
	QueuedAt       time.Time
	// ResolvedAt / ResolvedMediaID are stamped once when the record is resolved;
	// zero/empty while it is still pending review.
	ResolvedAt      time.Time
	ResolvedMediaID MediaID
}

// Resolved reports whether the record has been resolved (associate-or-create).
func (r UnmatchedRecord) Resolved() bool { return !r.ResolvedAt.IsZero() }

func randomUnmatchedRecordID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "urec-" + hex.EncodeToString(raw[:]), nil
}

// QueueUnmatchedRecord inserts one Media-independent Unmatched Record into the
// review inbox and returns its minted opaque id. It is the persistence the
// migration-import path lacked: an unmatched import item (ADR-0037 §2.3 step 4)
// that was only counted now becomes a reviewable record. Unlike QueueUnmatched
// (Media-keyed current-state UPSERT), each call is a fresh append — a distinct
// source record is a distinct row, so an import run's unmatched items accumulate
// as an audit trail rather than collapsing onto one Media key. Server-internal
// write — visibility gating belongs to the read side.
func (s *Store) QueueUnmatchedRecord(ctx context.Context, rec UnmatchedRecord) (string, error) {
	if rec.Origin == "" {
		return "", errors.New("origin is required")
	}
	id := rec.ID
	if id == "" {
		minted, err := randomUnmatchedRecordID()
		if err != nil {
			return "", err
		}
		id = minted
	}
	queuedAt := rec.QueuedAt
	if queuedAt.IsZero() {
		queuedAt = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO unmatched_record(
			id, origin, library_id, media_id, import_run_id, import_importer_id,
			source_handle, candidate_id, candidate_title, confidence,
			failure_reason, queued_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, rec.Origin, nullString(string(rec.LibraryID)), nullString(string(rec.MediaID)),
		rec.ImportRunID, rec.ImporterID, rec.SourceHandle, rec.CandidateID,
		rec.CandidateTitle, rec.Confidence, rec.FailureReason, timeToMillis(queuedAt.UTC()))
	if err != nil {
		return "", err
	}
	return id, nil
}

// QueueUnmatchedRecordOnce inserts one record for stable scan provenance.
// Repeating the same refusal returns the same opaque ID without appending
// another review item.
func (s *Store) QueueUnmatchedRecordOnce(ctx context.Context, rec UnmatchedRecord) (string, error) {
	key := string(rec.LibraryID) + "\x00" + rec.Origin + "\x00" + rec.ImporterID + "\x00" + rec.SourceHandle + "\x00" + rec.FailureReason
	sum := sha256.Sum256([]byte(key))
	rec.ID = "urec-" + hex.EncodeToString(sum[:16])
	id, err := s.QueueUnmatchedRecord(ctx, rec)
	if err == nil {
		return id, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if guardErr := s.guardLocked(); guardErr != nil {
		return "", err
	}
	var one int
	lookupErr := s.catalogR.QueryRowContext(ctx,
		"SELECT 1 FROM unmatched_record WHERE id = ?", rec.ID).Scan(&one)
	if lookupErr == nil {
		return rec.ID, nil
	}
	return "", err
}

// ResolveUnmatchedRecord marks one Media-independent record resolved, stamping
// the resolution audit (ADR-0068 associate-vs-create: resolvedMediaID is the
// Media the record was associated with, or that resolution created — empty when
// a record is resolved without any Media, e.g. a pure suppress). It is
// idempotent: a record already resolved is left untouched and reports found=true
// so a retry is a safe no-op. Reports whether a pending row was resolved.
func (s *Store) ResolveUnmatchedRecord(ctx context.Context, id string, resolvedMediaID MediaID, now time.Time) (bool, error) {
	if id == "" {
		return false, errors.New("record ID is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	// Only stamp still-pending rows so the audit records the first resolution;
	// a repeated call finds nothing to update yet still reports the record exists.
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE unmatched_record
		SET resolved_at_ms = ?, resolved_media_id = ?
		WHERE id = ? AND resolved_at_ms IS NULL
	`, timeToMillis(now.UTC()), nullString(string(resolvedMediaID)), id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 {
		return true, nil
	}
	// No pending row updated — either the id is unknown or already resolved.
	// A replay is successful only when it names the same resolution; accepting a
	// different Media would report split-brain success to the losing command.
	var (
		resolvedAt      sql.NullInt64
		associatedMedia sql.NullString
	)
	err = s.catalogR.QueryRowContext(ctx,
		"SELECT resolved_at_ms, resolved_media_id FROM unmatched_record WHERE id = ?",
		id,
	).Scan(&resolvedAt, &associatedMedia)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return resolvedAt.Valid && MediaID(associatedMedia.String) == resolvedMediaID, nil
}

// UnmatchedRecordByID returns one visible record regardless of pending/resolved
// state. Command replays need the resolved row so a lost 201 response can return
// the original Media instead of turning a committed success into "unknown".
func (s *Store) UnmatchedRecordByID(ctx context.Context, id string, filter LibraryFilter) (UnmatchedRecord, error) {
	if id == "" {
		return UnmatchedRecord{}, ErrUnmatchedRecordNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return UnmatchedRecord{}, err
	}
	var (
		rec             UnmatchedRecord
		libraryID       sql.NullString
		mediaID         sql.NullString
		resolvedAtMS    sql.NullInt64
		resolvedMediaID sql.NullString
		queuedAtMS      int64
	)
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT id, origin, library_id, media_id, import_run_id, import_importer_id,
		       source_handle, candidate_id, candidate_title, confidence,
		       failure_reason, queued_at_ms, resolved_at_ms, resolved_media_id
		FROM unmatched_record WHERE id = ?
	`, id).Scan(
		&rec.ID, &rec.Origin, &libraryID, &mediaID, &rec.ImportRunID,
		&rec.ImporterID, &rec.SourceHandle, &rec.CandidateID,
		&rec.CandidateTitle, &rec.Confidence, &rec.FailureReason, &queuedAtMS,
		&resolvedAtMS, &resolvedMediaID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return UnmatchedRecord{}, ErrUnmatchedRecordNotFound
	}
	if err != nil {
		return UnmatchedRecord{}, err
	}
	if libraryID.Valid {
		var restricted int
		err = s.catalogR.QueryRowContext(ctx, `
			SELECT is_restricted FROM library
			WHERE id = ? AND trashed_at_ms IS NULL
		`, libraryID.String).Scan(&restricted)
		if errors.Is(err, sql.ErrNoRows) {
			return UnmatchedRecord{}, ErrUnmatchedRecordNotFound
		}
		if err != nil {
			return UnmatchedRecord{}, err
		}
		library := Library{ID: LibraryID(libraryID.String), IsRestricted: restricted != 0}
		if !libraryVisible(library, filter) {
			return UnmatchedRecord{}, ErrUnmatchedRecordNotFound
		}
	} else if !filter.AllLibraries {
		return UnmatchedRecord{}, ErrUnmatchedRecordNotFound
	}
	rec.LibraryID = LibraryID(libraryID.String)
	rec.MediaID = MediaID(mediaID.String)
	rec.QueuedAt = millisToTime(queuedAtMS)
	if resolvedAtMS.Valid {
		rec.ResolvedAt = millisToTime(resolvedAtMS.Int64)
	}
	rec.ResolvedMediaID = MediaID(resolvedMediaID.String)
	return rec, nil
}

// AssociateImportUnmatchedRecord resolves one import record to an existing
// Media, scoped to the run that surfaced it. The run predicate prevents a
// stale status page from associating a record from another import. A retry of
// the same association is idempotent; a different or unknown association is
// rejected.
func (s *Store) AssociateImportUnmatchedRecord(ctx context.Context, id, importRunID string, resolvedMediaID MediaID, filter LibraryFilter, now time.Time) (bool, error) {
	if id == "" || importRunID == "" || resolvedMediaID == "" {
		return false, errors.New("record ID, import run ID, and Media ID are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var (
		libraryID  sql.NullString
		resolved   sql.NullInt64
		associated sql.NullString
	)
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT library_id, resolved_at_ms, resolved_media_id
		FROM unmatched_record
		WHERE id = ? AND origin = ? AND import_run_id = ?
	`, id, UnmatchedOriginImport, importRunID).Scan(&libraryID, &resolved, &associated)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if libraryID.Valid {
		libraries, err := loadLibraries(ctx, s.catalogR)
		if err != nil {
			return false, err
		}
		visible := false
		for _, library := range libraries {
			if library.ID == LibraryID(libraryID.String) {
				visible = libraryVisible(library, filter)
				break
			}
		}
		if !visible {
			return false, nil
		}
	} else if !filter.AllLibraries {
		return false, nil
	}
	if resolved.Valid {
		return associated.Valid && MediaID(associated.String) == resolvedMediaID, nil
	}
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE unmatched_record
		SET resolved_at_ms = ?, resolved_media_id = ?
		WHERE id = ? AND origin = ? AND import_run_id = ? AND resolved_at_ms IS NULL
	`, timeToMillis(now.UTC()), string(resolvedMediaID), id, UnmatchedOriginImport, importRunID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 {
		return true, nil
	}
	err = s.catalogR.QueryRowContext(ctx, `
		SELECT resolved_media_id
		FROM unmatched_record
		WHERE id = ? AND origin = ? AND import_run_id = ?
	`, id, UnmatchedOriginImport, importRunID).Scan(&associated)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return associated.Valid && MediaID(associated.String) == resolvedMediaID, nil
}

// UnmatchedRecordsForImportRun returns the visible pending import records for
// exactly one run. Callers still receive the internal record shape; the graph
// projection decides which path-free fields are safe for the wire.
func (s *Store) UnmatchedRecordsForImportRun(ctx context.Context, importRunID string, filter LibraryFilter) ([]UnmatchedRecord, error) {
	if importRunID == "" {
		return nil, errors.New("import run ID is required")
	}
	records, err := s.UnmatchedRecords(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := make([]UnmatchedRecord, 0, len(records))
	for _, record := range records {
		if record.Origin == UnmatchedOriginImport && record.ImportRunID == importRunID {
			out = append(out, record)
		}
	}
	return out, nil
}

// UnmatchedRecords returns the pending Media-independent Unmatched Records the
// session may see, newest-queued first. A record carrying a Library is dropped
// when the filter hides it (the same reveal-and-Library posture UnmatchedItems
// takes). A Library-less (operator-global) record — an import item that resolved
// to no Library yet — carries source handles and provenance that are not scoped
// to any grant, so it is fail-closed: shown ONLY to an all-Libraries (ops/admin)
// scope, never to a partial-grant Manager session. Resolved records are excluded
// — this is the pending review inbox.
func (s *Store) UnmatchedRecords(ctx context.Context, filter LibraryFilter) ([]UnmatchedRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	visible := map[LibraryID]bool{}
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visible[library.ID] = true
		}
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, origin, library_id, media_id, import_run_id, import_importer_id,
		       source_handle, candidate_id, candidate_title, confidence,
		       failure_reason, queued_at_ms
		FROM unmatched_record
		WHERE resolved_at_ms IS NULL
		ORDER BY queued_at_ms DESC, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UnmatchedRecord{}
	for rows.Next() {
		var (
			rec       UnmatchedRecord
			libraryID sql.NullString
			mediaID   sql.NullString
			queuedMS  int64
		)
		if err := rows.Scan(&rec.ID, &rec.Origin, &libraryID, &mediaID,
			&rec.ImportRunID, &rec.ImporterID, &rec.SourceHandle, &rec.CandidateID,
			&rec.CandidateTitle, &rec.Confidence, &rec.FailureReason, &queuedMS); err != nil {
			return nil, err
		}
		// A Library-scoped record must clear the reveal/Library filter. A
		// Library-less (operator-global) record is fail-closed to all-Libraries
		// scope only — a partial-grant session never sees global source handles.
		if libraryID.Valid {
			if !visible[LibraryID(libraryID.String)] {
				continue
			}
		} else if !filter.AllLibraries {
			continue
		}
		rec.LibraryID = LibraryID(libraryID.String)
		rec.MediaID = MediaID(mediaID.String)
		rec.QueuedAt = millisToTime(queuedMS)
		out = append(out, rec)
	}
	return out, rows.Err()
}

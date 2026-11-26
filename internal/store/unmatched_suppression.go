package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"
)

// The three INDEPENDENT suppression scopes ADR-0068 §A requires. They are shape
// words (invariant #1) and the caller always names one — scope is never inferred
// from a generic "Ignore".
const (
	// SuppressionScopeSourceRecord skips one Unmatched Record on re-import. An
	// independent scan may still surface its file; that is a different identity.
	SuppressionScopeSourceRecord = "source-record"
	// SuppressionScopeFile skips one stable file identity on re-scan. Other
	// files and other source records stay eligible.
	SuppressionScopeFile = "file"
	// SuppressionScopeCandidate excludes ONE candidate for ONE Unmatched Record.
	// Ignoring a wrong suggestion never suppresses the whole record.
	SuppressionScopeCandidate = "candidate"
)

// UnmatchedSuppression is one scoped suppression with the provenance the
// operator surface renders beside its own reset control.
type UnmatchedSuppression struct {
	Scope string
	// Key is the opaque identity within the scope: the Unmatched Record id, the
	// stable content fingerprint, or the record+candidate pair. Core compares it
	// and never parses it for meaning.
	Key string
	// RecordID names the Unmatched Record a suppression belongs to (including
	// file-scoped suppressions created from an inbox record); empty only for a
	// standalone file-scoped suppression.
	RecordID string
	// LibraryID optionally pins a file-scoped suppression without RecordID to a
	// Library's own plane. Empty on legacy global catalog rows; populated on read
	// from Encrypted Library planes.
	LibraryID LibraryID
	// SourceHandle is the display handle captured at suppression time (a path or
	// an external-id summary). Context only — never the durable identity.
	SourceHandle   string
	ActorProfileID ProfileID
	CreatedAt      time.Time
}

// CandidateSuppressionKey pairs a candidate with the record it is suppressed
// FOR, so the same candidate stays eligible for every other record. It is the
// single place that composition is defined — callers never hand-build the key.
func CandidateSuppressionKey(recordID, candidateID string) string {
	return recordID + "\x00" + candidateID
}

// validSuppressionScope reports whether the scope is one of the three ADR-0068
// scopes. An unknown scope is rejected rather than stored, so a typo can never
// create a suppression nothing knows how to reset.
func validSuppressionScope(scope string) bool {
	switch scope {
	case SuppressionScopeSourceRecord, SuppressionScopeFile, SuppressionScopeCandidate:
		return true
	}
	return false
}

// SuppressUnmatched records one scoped suppression. It is idempotent on
// (scope, key): re-suppressing the same identity refreshes the provenance rather
// than creating a second row, so a retry after a transport error can never leave
// a partial or duplicated suppression. Suppressing one scope leaves both sibling
// scopes untouched — they are separate rows by construction. Server-internal
// write; the calling surface resolves visibility and authorization. A
// suppression naming a record is stored on that record's plane, so the
// provenance of an Encrypted Library's record never reaches catalog.db; one
// naming no record, or a record no unsealed plane holds, stays in catalog.db.
func (s *Store) SuppressUnmatched(ctx context.Context, suppression UnmatchedSuppression) error {
	if !validSuppressionScope(suppression.Scope) {
		return ValidationError("scope must be source-record, file, or candidate")
	}
	if strings.TrimSpace(suppression.Key) == "" {
		return ValidationError("suppression key is required")
	}
	createdAt := suppression.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	// A suppression lives beside its Unmatched Record or named Library. A record
	// or Library no readable plane holds is refused rather than written to the
	// shared catalog. For a file-scoped suppression without RecordID/LibraryID,
	// resolve any matching open Encrypted Library plane before falling back to the
	// shared catalog so private files never write plaintext keys globally.
	plane := catalogPlane{writer: s.catalogW, reader: s.catalogR}
	switch {
	case suppression.RecordID != "":
		recordPlane, err := s.unmatchedRecordPlaneLocked(ctx, suppression.RecordID)
		if err != nil {
			return err
		}
		plane = recordPlane
	case suppression.LibraryID != "":
		libPlane, err := s.catalogPlaneForLibraryLocked(ctx, suppression.LibraryID)
		if err != nil {
			return err
		}
		plane = libPlane
	case suppression.Scope == SuppressionScopeFile:
		encPlane, found, err := s.fileSuppressionPlaneLocked(ctx, suppression.Key, suppression.SourceHandle)
		if err != nil {
			return err
		}
		if found {
			plane = encPlane
			break
		}
		sharedOwned, err := s.fileSuppressionOwnedBySharedCatalogLocked(ctx, suppression.Key, suppression.SourceHandle)
		if err != nil {
			return err
		}
		if !sharedOwned {
			sealed, err := s.hasSealedEncryptedLibraryLocked(ctx)
			if err != nil {
				return err
			}
			if sealed {
				return ErrEncryptedLibraryDBDisabled
			}
		}
	}
	_, err := plane.writer.ExecContext(ctx, `
		INSERT INTO unmatched_suppression(scope, scope_key, record_id, source_handle, actor_profile_id, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(scope, scope_key) DO UPDATE SET
			record_id = excluded.record_id,
			source_handle = excluded.source_handle,
			actor_profile_id = excluded.actor_profile_id,
			created_at_ms = excluded.created_at_ms
	`, suppression.Scope, suppression.Key, nullString(suppression.RecordID),
		suppression.SourceHandle, nullString(string(suppression.ActorProfileID)),
		timeToMillis(createdAt.UTC()))
	return err
}

// ResetUnmatchedSuppression lifts exactly ONE scoped suppression and reports
// whether a row was actually removed. Resetting one scope cannot touch a sibling
// scope: the DELETE is keyed by (scope, key), and the three scopes never share a
// key. Historical provenance survives in Activity — this only restores
// eligibility.
func (s *Store) ResetUnmatchedSuppression(ctx context.Context, scope, key string) (bool, error) {
	if !validSuppressionScope(scope) {
		return false, ValidationError("scope must be source-record, file, or candidate")
	}
	if strings.TrimSpace(key) == "" {
		return false, ValidationError("suppression key is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	removed := false
	for _, plane := range s.expressionReadPlanesLocked() {
		result, err := plane.writer.ExecContext(ctx,
			"DELETE FROM unmatched_suppression WHERE scope = ? AND scope_key = ?", scope, key)
		if err != nil {
			return false, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		removed = removed || affected > 0
	}
	return removed, nil
}

// UnmatchedSuppressed is the eligibility predicate the re-import and re-scan
// paths consult before re-surfacing an identity.
func (s *Store) UnmatchedSuppressed(ctx context.Context, scope, key string) (bool, error) {
	if !validSuppressionScope(scope) {
		return false, ValidationError("scope must be source-record, file, or candidate")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	for _, plane := range s.expressionReadPlanesLocked() {
		var one int
		err := plane.reader.QueryRowContext(ctx,
			"SELECT 1 FROM unmatched_suppression WHERE scope = ? AND scope_key = ?", scope, key).Scan(&one)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	return false, nil
}

// UnmatchedSuppressions lists the active suppressions, newest first. With a
// recordID it returns that record's own suppressions (its record scope plus any
// candidate scopes); with "" it returns every active suppression — the operator
// surface that shows what is currently being skipped and offers each reset.
// It spans catalog.db and every unsealed Encrypted Library plane.
func (s *Store) UnmatchedSuppressions(ctx context.Context, recordID string) ([]UnmatchedSuppression, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	out := []UnmatchedSuppression{}
	for _, plane := range s.expressionReadPlanesLocked() {
		suppressions, err := planeUnmatchedSuppressions(ctx, plane, recordID)
		if err != nil {
			return nil, err
		}
		out = append(out, suppressions...)
	}
	slices.SortStableFunc(out, func(a, b UnmatchedSuppression) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Scope, b.Scope); c != 0 {
			return c
		}
		return cmp.Compare(a.Key, b.Key)
	})
	return out, nil
}

// UnmatchedSuppressionsFiltered lists active suppressions visible under filter,
// newest first, skipping any suppression bound to an invisible Library.
func (s *Store) UnmatchedSuppressionsFiltered(ctx context.Context, recordID string, filter LibraryFilter) ([]UnmatchedSuppression, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	visibleLibs := make(map[LibraryID]bool, len(libraries))
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visibleLibs[library.ID] = true
		}
	}
	out := []UnmatchedSuppression{}
	for _, plane := range s.expressionReadPlanesLocked() {
		if plane.encrypted && !visibleLibs[plane.libraryID] {
			continue
		}
		suppressions, err := planeUnmatchedSuppressions(ctx, plane, recordID)
		if err != nil {
			return nil, err
		}
		for _, suppression := range suppressions {
			if !plane.encrypted && suppression.RecordID != "" {
				var recLib sql.NullString
				if err := plane.reader.QueryRowContext(ctx,
					`SELECT library_id FROM unmatched_record WHERE id = ?`, suppression.RecordID).
					Scan(&recLib); err == nil && recLib.Valid && !visibleLibs[LibraryID(recLib.String)] {
					continue
				}
			}
			out = append(out, suppression)
		}
	}
	slices.SortStableFunc(out, func(a, b UnmatchedSuppression) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Scope, b.Scope); c != 0 {
			return c
		}
		return cmp.Compare(a.Key, b.Key)
	})
	return out, nil
}

func (s *Store) fileSuppressionPlaneLocked(ctx context.Context, key, sourceHandle string) (catalogPlane, bool, error) {
	for _, plane := range s.unsealedEncryptedPlanesLocked() {
		var hit int
		err := plane.reader.QueryRowContext(ctx, `
			SELECT 1 FROM media WHERE id = ?
			UNION ALL
			SELECT 1 FROM media_file WHERE content_fingerprint = ?
			UNION ALL
			SELECT 1 FROM unmatched_record WHERE media_id = ? OR (? <> '' AND source_handle = ?)
			LIMIT 1`, key, key, key, sourceHandle, sourceHandle).Scan(&hit)
		if err == nil {
			return plane, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return catalogPlane{}, false, err
		}
	}
	return catalogPlane{}, false, nil
}

func (s *Store) fileSuppressionOwnedBySharedCatalogLocked(ctx context.Context, key, sourceHandle string) (bool, error) {
	// When a caller provides a SourceHandle, require that handle itself to be
	// owned by a plaintext Library so a shared content fingerprint can never
	// smuggle a sealed Encrypted Library's private path into catalog.db.
	if sourceHandle != "" {
		var hit int
		err := s.catalogR.QueryRowContext(ctx, `
			SELECT 1 FROM media_file mf
			JOIN media m ON m.id = mf.media_id
			JOIN library l ON l.id = m.library_id
			WHERE l.encrypted = 0 AND mf.path = ? AND (mf.content_fingerprint = ? OR m.id = ?)
			UNION ALL
			SELECT 1 FROM unmatched_record ur
			JOIN library l ON l.id = ur.library_id
			WHERE l.encrypted = 0 AND ur.source_handle = ?
			LIMIT 1`, sourceHandle, key, key, sourceHandle).Scan(&hit)
		if err == nil {
			return true, nil
		}
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	var hit int
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT 1 FROM media m
		JOIN library l ON l.id = m.library_id
		WHERE l.encrypted = 0 AND m.id = ?
		UNION ALL
		SELECT 1 FROM media_file mf
		JOIN media m ON m.id = mf.media_id
		JOIN library l ON l.id = m.library_id
		WHERE l.encrypted = 0 AND mf.content_fingerprint = ?
		UNION ALL
		SELECT 1 FROM unmatched_record ur
		JOIN library l ON l.id = ur.library_id
		WHERE l.encrypted = 0 AND ur.media_id = ?
		LIMIT 1`, key, key, key).Scan(&hit)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func (s *Store) hasSealedEncryptedLibraryLocked(ctx context.Context) (bool, error) {
	rows, err := s.catalogR.QueryContext(ctx, `SELECT id FROM library WHERE encrypted != 0`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id LibraryID
		if err := rows.Scan(&id); err != nil {
			return false, err
		}
		db := s.encryptedDBs[id]
		if db == nil || db.state != "unsealed-registered" || db.reader == nil || db.writer == nil {
			return true, nil
		}
	}
	return false, rows.Err()
}

func planeUnmatchedSuppressions(ctx context.Context, plane catalogPlane, recordID string) ([]UnmatchedSuppression, error) {
	query := `
		SELECT scope, scope_key, record_id, source_handle, actor_profile_id, created_at_ms
		FROM unmatched_suppression
	`
	args := []any{}
	if recordID != "" {
		query += " WHERE record_id = ?"
		args = append(args, recordID)
	}
	query += " ORDER BY created_at_ms DESC, scope, scope_key"
	rows, err := plane.reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnmatchedSuppression
	for rows.Next() {
		var (
			suppression UnmatchedSuppression
			record      sql.NullString
			actor       sql.NullString
			createdMS   int64
		)
		if err := rows.Scan(&suppression.Scope, &suppression.Key, &record,
			&suppression.SourceHandle, &actor, &createdMS); err != nil {
			return nil, err
		}
		suppression.RecordID = record.String
		suppression.LibraryID = plane.libraryID
		suppression.ActorProfileID = ProfileID(actor.String)
		suppression.CreatedAt = millisToTime(createdMS)
		out = append(out, suppression)
	}
	return out, rows.Err()
}

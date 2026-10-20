package store

import (
	"context"
	"database/sql"
	"errors"
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
	// RecordID names the Unmatched Record a candidate- or record-scoped
	// suppression belongs to; empty for a pure file-scoped one.
	RecordID string
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
// write; the calling surface resolves visibility and authorization.
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
	_, err := s.catalogW.ExecContext(ctx, `
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
	result, err := s.catalogW.ExecContext(ctx,
		"DELETE FROM unmatched_suppression WHERE scope = ? AND scope_key = ?", scope, key)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
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
	var one int
	err := s.catalogR.QueryRowContext(ctx,
		"SELECT 1 FROM unmatched_suppression WHERE scope = ? AND scope_key = ?", scope, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// UnmatchedSuppressions lists the active suppressions, newest first. With a
// recordID it returns that record's own suppressions (its record scope plus any
// candidate scopes); with "" it returns every active suppression — the operator
// surface that shows what is currently being skipped and offers each reset.
func (s *Store) UnmatchedSuppressions(ctx context.Context, recordID string) ([]UnmatchedSuppression, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
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
	rows, err := s.catalogR.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UnmatchedSuppression{}
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
		suppression.ActorProfileID = ProfileID(actor.String)
		suppression.CreatedAt = millisToTime(createdMS)
		out = append(out, suppression)
	}
	return out, rows.Err()
}

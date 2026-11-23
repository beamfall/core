package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// RendererSessionCommandIDMinLen and RendererSessionCommandIDMaxLen bound
	// caller-supplied idempotency keys on controlRendererSession (CORE-3309).
	RendererSessionCommandIDMinLen = 16
	RendererSessionCommandIDMaxLen = 128
	// RendererSessionCommandTTL bounds how long applied commandId rows stay in
	// the idempotency ledger before write-time pruning.
	RendererSessionCommandTTL = 24 * time.Hour
)

// RendererSessionControlStatus is the outcome status of a revision-checked
// Renderer Session control request.
type RendererSessionControlStatus string

const (
	RendererSessionControlApplied  RendererSessionControlStatus = "APPLIED"
	RendererSessionControlConflict RendererSessionControlStatus = "CONFLICT"
)

// ErrRendererSessionCommandReuse marks reusing an existing commandId for a
// different target session or canonical command body.
var ErrRendererSessionCommandReuse = errors.New("commandID already used for a different command")

// RendererSessionCommandReuseError is returned when commandId is already in
// the ledger with a different target session or canonical command body hash.
type RendererSessionCommandReuseError struct {
	CommandID string
}

func (e RendererSessionCommandReuseError) Error() string {
	return ErrRendererSessionCommandReuse.Error()
}

func (e RendererSessionCommandReuseError) Unwrap() error {
	return ErrRendererSessionCommandReuse
}

// ErrRendererSessionRevisionConflict marks a stale expectedRevision.
var ErrRendererSessionRevisionConflict = errors.New("renderer session revision conflict")

// RendererSessionRevisionConflictError is returned when expectedRevision does
// not match the target Renderer Session's current monotonic revision.
type RendererSessionRevisionConflictError struct {
	SessionID        SessionID
	ExpectedRevision int64
	CurrentRevision  int64
}

func (e RendererSessionRevisionConflictError) Error() string {
	return fmt.Sprintf("renderer session %s revision conflict: expected %d, current %d",
		e.SessionID, e.ExpectedRevision, e.CurrentRevision)
}

func (e RendererSessionRevisionConflictError) Unwrap() error {
	return ErrRendererSessionRevisionConflict
}

// RendererSessionControlResult is the store-persisted outcome of an applied
// Renderer Session command.
type RendererSessionControlResult struct {
	Status    RendererSessionControlStatus `json:"status"`
	SessionID SessionID                    `json:"sessionID"`
	Command   string                       `json:"command"`
	Revision  int64                        `json:"revision"`
	Replayed  bool                         `json:"-"`
}

// ValidateRendererSessionCommandID enforces the 16-128 character
// [A-Za-z0-9_-] format for idempotency keys.
func ValidateRendererSessionCommandID(commandID string) error {
	if commandID == "" {
		return ValidationError("commandID is required")
	}
	if len(commandID) < RendererSessionCommandIDMinLen || len(commandID) > RendererSessionCommandIDMaxLen {
		return ValidationError("commandID must be 16 to 128 characters of A-Z, a-z, 0-9, _ or -")
	}
	for i := 0; i < len(commandID); i++ {
		c := commandID[i]
		isAlpha := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		isDigit := c >= '0' && c <= '9'
		if !isAlpha && !isDigit && c != '_' && c != '-' {
			return ValidationError("commandID must be 16 to 128 characters of A-Z, a-z, 0-9, _ or -")
		}
	}
	return nil
}

type canonicalSessionControlBody struct {
	Command    string  `json:"command"`
	Direction  string  `json:"direction,omitempty"`
	PositionMs *int64  `json:"positionMs,omitempty"`
	Volume     *int    `json:"volume,omitempty"`
	TrackID    *string `json:"trackID,omitempty"`
	MediaID    MediaID `json:"mediaID,omitempty"`
	Text       string  `json:"text,omitempty"`
}

func sessionControlBodySHA256(payload SessionControlPayload) (string, error) {
	normalized, err := ValidateSessionControlArgs(payload.Command, payload.args())
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(canonicalSessionControlBody{
		Command:    payload.Command,
		Direction:  normalized.Direction,
		PositionMs: normalized.PositionMs,
		Volume:     normalized.Volume,
		TrackID:    normalized.TrackID,
		MediaID:    normalized.MediaID,
		Text:       normalized.Text,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// RendererSessionRevision returns the monotonic revision of a Renderer Session
// (0 when no command has incremented it yet).
func (s *Store) RendererSessionRevision(ctx context.Context, id SessionID) (int64, error) {
	if id == "" {
		return 0, ValidationError("session ID is required")
	}
	revs, err := s.RendererSessionRevisions(ctx, []SessionID{id})
	if err != nil {
		return 0, err
	}
	return revs[id], nil
}

// RendererSessionRevisions returns the current monotonic revision for each
// requested SessionID (missing rows default to 0).
func (s *Store) RendererSessionRevisions(ctx context.Context, ids []SessionID) (map[SessionID]int64, error) {
	out := make(map[SessionID]int64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	seen := make(map[SessionID]bool, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		args = append(args, string(id))
	}
	if len(args) == 0 {
		return out, nil
	}
	placeholders := strings.Repeat("?,", len(args))
	placeholders = placeholders[:len(placeholders)-1]
	rows, err := s.eventsR.QueryContext(ctx,
		`SELECT session_id, revision FROM renderer_session_revision WHERE session_id IN (`+placeholders+`)`, // #nosec G202 -- placeholders holds only "?" marks; all values bound via args
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id SessionID
		var rev int64
		if err := rows.Scan(&id, &rev); err != nil {
			return nil, err
		}
		out[id] = rev
	}
	return out, rows.Err()
}

// ControlRendererSession executes a revision-checked, idempotent Renderer
// Session command in a single serialized events.db writer transaction:
//
//  1. Prunes command ledger entries older than 24 hours.
//  2. Looks up commandID: matching target + body hash returns the stored result
//     without emitting another event or incrementing the revision; mismatching
//     target or body hash returns RendererSessionCommandReuseError.
//  3. Compares expectedRevision against the target session's revision (missing
//     row = 0): mismatch returns RendererSessionRevisionConflictError with the
//     current revision.
//  4. On match, appends the session.control outbox event (and activity row if
//     provided), increments the session revision once, and records commandID.
func (s *Store) ControlRendererSession(
	ctx context.Context,
	target SessionID,
	expectedRevision int64,
	commandID string,
	payload SessionControlPayload,
	activity *ActivityEntry,
	now time.Time,
) (RendererSessionControlResult, error) {
	if expectedRevision < 0 {
		return RendererSessionControlResult{}, ValidationError("expectedRevision must be a non-negative integer")
	}
	if err := ValidateRendererSessionCommandID(commandID); err != nil {
		return RendererSessionControlResult{}, err
	}
	rawPayload, err := sessionControlPayloadJSON(target, payload)
	if err != nil {
		return RendererSessionControlResult{}, err
	}
	bodyHash, err := sessionControlBodySHA256(payload)
	if err != nil {
		return RendererSessionControlResult{}, err
	}
	now = normalizeTime(now)
	if activity != nil {
		if activity.OccurredAt.IsZero() {
			activity.OccurredAt = now
		}
		if err := validateActivityEntry(activity); err != nil {
			return RendererSessionControlResult{}, err
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return RendererSessionControlResult{}, err
	}

	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return RendererSessionControlResult{}, err
	}
	defer tx.Rollback()

	nowMS := now.UTC().UnixMilli()
	cutoffMS := now.UTC().Add(-RendererSessionCommandTTL).UnixMilli()
	if _, err := tx.ExecContext(ctx, `DELETE FROM renderer_session_command WHERE created_at_ms < ?`, cutoffMS); err != nil {
		return RendererSessionControlResult{}, err
	}

	var existingSession SessionID
	var existingHash, existingResultJSON string
	err = tx.QueryRowContext(ctx, `
		SELECT session_id, body_sha256, result_json
		FROM renderer_session_command
		WHERE command_id = ?
	`, commandID).Scan(&existingSession, &existingHash, &existingResultJSON)
	switch {
	case err == nil:
		if existingSession != target || existingHash != bodyHash {
			return RendererSessionControlResult{}, RendererSessionCommandReuseError{CommandID: commandID}
		}
		var stored RendererSessionControlResult
		if err := json.Unmarshal([]byte(existingResultJSON), &stored); err != nil {
			return RendererSessionControlResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return RendererSessionControlResult{}, err
		}
		stored.Replayed = true
		return stored, nil
	case !errors.Is(err, sql.ErrNoRows):
		return RendererSessionControlResult{}, err
	}

	var currentRevision int64
	err = tx.QueryRowContext(ctx, `SELECT revision FROM renderer_session_revision WHERE session_id = ?`, target).Scan(&currentRevision)
	if errors.Is(err, sql.ErrNoRows) {
		currentRevision = 0
	} else if err != nil {
		return RendererSessionControlResult{}, err
	}
	if expectedRevision != currentRevision {
		return RendererSessionControlResult{}, RendererSessionRevisionConflictError{
			SessionID:        target,
			ExpectedRevision: expectedRevision,
			CurrentRevision:  currentRevision,
		}
	}

	if err := appendSessionControlEventSQL(ctx, tx, target, rawPayload, now); err != nil {
		return RendererSessionControlResult{}, err
	}
	if activity != nil {
		if err := appendActivitySQL(ctx, tx, *activity); err != nil {
			return RendererSessionControlResult{}, err
		}
	}
	newRevision, err := incrementRendererSessionRevisionSQL(ctx, tx, target, now)
	if err != nil {
		return RendererSessionControlResult{}, err
	}
	result := RendererSessionControlResult{
		Status:    RendererSessionControlApplied,
		SessionID: target,
		Command:   payload.Command,
		Revision:  newRevision,
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return RendererSessionControlResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO renderer_session_command(command_id, session_id, body_sha256, status, command, revision, result_json, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, commandID, target, bodyHash, string(result.Status), result.Command, result.Revision, string(resultJSON), nowMS); err != nil {
		return RendererSessionControlResult{}, err
	}
	if err := s.commitOutboxTx(tx); err != nil {
		return RendererSessionControlResult{}, err
	}
	return result, nil
}

func incrementRendererSessionRevisionSQL(ctx context.Context, q queryRower, target SessionID, now time.Time) (int64, error) {
	now = normalizeTime(now)
	var rev int64
	err := q.QueryRowContext(ctx, `
		INSERT INTO renderer_session_revision (session_id, revision, updated_at_ms)
		VALUES (?, 1, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			revision = renderer_session_revision.revision + 1,
			updated_at_ms = excluded.updated_at_ms
		RETURNING revision
	`, target, now.UTC().UnixMilli()).Scan(&rev)
	return rev, err
}

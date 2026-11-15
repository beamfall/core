package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"
)

// ErrOfflinePackBuildNotFound is returned when no pending build row exists for
// a pack ID (or it is not the caller's, when scoped by profile).
var ErrOfflinePackBuildNotFound = errors.New("offline pack build not found")

// OfflinePackBuildState is the closed vocabulary of a pending build row. A
// build that succeeds leaves this table: its sealed offline_pack row carries
// the ready state from then on.
type OfflinePackBuildState string

const (
	// OfflinePackBuildBuilding marks a queued or running background build.
	OfflinePackBuildBuilding OfflinePackBuildState = "building"
	// OfflinePackBuildFailed marks a build whose job ended without sealing.
	OfflinePackBuildFailed OfflinePackBuildState = "failed"
)

// OfflinePackBuild is the pending row for a pack that is building in a
// background job under a pre-assigned pack ID (CORE-1743). FailureReason is a
// closed, client-safe code, never a path or a raw error. Payload is the opaque
// build request the retry path re-queues.
type OfflinePackBuild struct {
	PackID        OfflinePackID
	ProfileID     ProfileID
	DeviceID      DeviceID
	TargetKind    string
	TargetRef     string
	Quality       string
	State         OfflinePackBuildState
	Progress      float64
	FailureReason string
	Payload       []byte
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TrackOfflinePackBuild records a building row for build.PackID unless one
// already exists or the pack has already sealed. It is idempotent: the
// existing row is returned, and a sealed pack reads as
// ErrOfflinePackBuildNotFound.
func (s *Store) TrackOfflinePackBuild(ctx context.Context, build OfflinePackBuild, now time.Time) (OfflinePackBuild, error) {
	build.TargetKind = strings.TrimSpace(build.TargetKind)
	switch {
	case build.PackID == "":
		return OfflinePackBuild{}, ValidationError("pack ID is required")
	case build.ProfileID == "":
		return OfflinePackBuild{}, ValidationError("profile ID is required")
	case build.TargetKind == "":
		return OfflinePackBuild{}, ValidationError("target kind is required")
	case len(build.Payload) == 0:
		return OfflinePackBuild{}, ValidationError("build payload is required")
	}
	nowMS := now.UTC().UnixMilli()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return OfflinePackBuild{}, err
	}
	if _, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO offline_pack_build (pack_id, profile_id, device_id, target_kind, target_ref,
		    quality, state, progress, failure_reason, job_payload, created_at_ms, updated_at_ms)
		SELECT ?, ?, ?, ?, ?, ?, ?, 0, '', ?, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM offline_pack WHERE id = ?)
		ON CONFLICT(pack_id) DO NOTHING
	`, build.PackID, build.ProfileID, build.DeviceID, build.TargetKind, build.TargetRef,
		build.Quality, OfflinePackBuildBuilding, build.Payload, nowMS, nowMS, build.PackID); err != nil {
		return OfflinePackBuild{}, err
	}
	got, err := s.offlinePackBuildLocked(ctx, build.PackID)
	if err != nil {
		return OfflinePackBuild{}, err
	}
	if got.ProfileID != build.ProfileID {
		return OfflinePackBuild{}, ErrOfflinePackBuildNotFound
	}
	return got, nil
}

// SetOfflinePackBuildProgress updates a building row's progress (clamped to
// [0, 1]). It never moves progress backwards and is a no-op for a failed or
// absent row.
func (s *Store) SetOfflinePackBuildProgress(ctx context.Context, id OfflinePackID, progress float64, now time.Time) error {
	if id == "" {
		return ValidationError("pack ID is required")
	}
	if math.IsNaN(progress) {
		return ValidationError("progress is not a number")
	}
	progress = math.Max(0, math.Min(1, progress))
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		UPDATE offline_pack_build SET progress = MAX(progress, ?), updated_at_ms = MAX(updated_at_ms, ?)
		WHERE pack_id = ? AND state = ?
	`, progress, now.UTC().UnixMilli(), id, OfflinePackBuildBuilding)
	return err
}

// FailOfflinePackBuild moves a building row to failed with a closed reason
// code. Progress is kept so a client can report how far the build got.
func (s *Store) FailOfflinePackBuild(ctx context.Context, id OfflinePackID, reason string, now time.Time) error {
	reason = strings.TrimSpace(reason)
	if id == "" {
		return ValidationError("pack ID is required")
	}
	if reason == "" {
		return ValidationError("failure reason is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		UPDATE offline_pack_build SET state = ?, failure_reason = ?, updated_at_ms = MAX(updated_at_ms, ?)
		WHERE pack_id = ? AND state = ?
	`, OfflinePackBuildFailed, reason, now.UTC().UnixMilli(), id, OfflinePackBuildBuilding)
	return err
}

// RestartOfflinePackBuild moves the caller's failed row back to building with
// progress and reason cleared, replacing its payload. It reports whether this
// call made the transition: false means the row was not failed (already
// building again), which keeps a repeated retry idempotent.
func (s *Store) RestartOfflinePackBuild(ctx context.Context, id OfflinePackID, profileID ProfileID, payload []byte, now time.Time) (bool, error) {
	if id == "" || profileID == "" {
		return false, ValidationError("pack ID and profile ID are required")
	}
	if len(payload) == 0 {
		return false, ValidationError("build payload is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	res, err := s.catalogW.ExecContext(ctx, `
		UPDATE offline_pack_build
		SET state = ?, progress = 0, failure_reason = '', job_payload = ?, updated_at_ms = MAX(updated_at_ms, ?)
		WHERE pack_id = ? AND profile_id = ? AND state = ?
	`, OfflinePackBuildBuilding, payload, now.UTC().UnixMilli(), id, profileID, OfflinePackBuildFailed)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// DeleteFailedOfflinePackBuild dismisses the caller's failed build row. A
// building row is left alone and reads as not found here.
func (s *Store) DeleteFailedOfflinePackBuild(ctx context.Context, id OfflinePackID, profileID ProfileID) error {
	if id == "" || profileID == "" {
		return ValidationError("pack ID and profile ID are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	res, err := s.catalogW.ExecContext(ctx, `
		DELETE FROM offline_pack_build WHERE pack_id = ? AND profile_id = ? AND state = ?
	`, id, profileID, OfflinePackBuildFailed)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrOfflinePackBuildNotFound
	}
	return nil
}

// OfflinePackBuildByID returns the caller's pending build row. Another
// Profile's row, or a row whose pack has sealed, reads as not found.
func (s *Store) OfflinePackBuildByID(ctx context.Context, id OfflinePackID, profileID ProfileID) (OfflinePackBuild, error) {
	if id == "" || profileID == "" {
		return OfflinePackBuild{}, ValidationError("pack ID and profile ID are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return OfflinePackBuild{}, err
	}
	build, err := s.offlinePackBuildLocked(ctx, id)
	if err != nil {
		return OfflinePackBuild{}, err
	}
	if build.ProfileID != profileID {
		return OfflinePackBuild{}, ErrOfflinePackBuildNotFound
	}
	return build, nil
}

// OfflinePackBuildsForProfile lists the caller's pending build rows,
// newest-updated first. Rows whose pack has sealed are never returned.
func (s *Store) OfflinePackBuildsForProfile(ctx context.Context, profileID ProfileID) ([]OfflinePackBuild, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, offlinePackBuildSelect+`
		WHERE b.profile_id = ? AND NOT EXISTS (SELECT 1 FROM offline_pack p WHERE p.id = b.pack_id)
		ORDER BY b.updated_at_ms DESC, b.pack_id
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OfflinePackBuild{}
	for rows.Next() {
		build, err := scanOfflinePackBuild(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, build)
	}
	return out, rows.Err()
}

const offlinePackBuildSelect = `
		SELECT b.pack_id, b.profile_id, b.device_id, b.target_kind, b.target_ref, b.quality,
		       b.state, b.progress, b.failure_reason, b.job_payload, b.created_at_ms, b.updated_at_ms
		FROM offline_pack_build b`

func (s *Store) offlinePackBuildLocked(ctx context.Context, id OfflinePackID) (OfflinePackBuild, error) {
	build, err := scanOfflinePackBuild(s.catalogR.QueryRowContext(ctx, offlinePackBuildSelect+`
		WHERE b.pack_id = ? AND NOT EXISTS (SELECT 1 FROM offline_pack p WHERE p.id = b.pack_id)
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return OfflinePackBuild{}, ErrOfflinePackBuildNotFound
	}
	return build, err
}

func scanOfflinePackBuild(scanner offlinePackScanner) (OfflinePackBuild, error) {
	var (
		build                    OfflinePackBuild
		createdAtMS, updatedAtMS int64
	)
	if err := scanner.Scan(&build.PackID, &build.ProfileID, &build.DeviceID, &build.TargetKind,
		&build.TargetRef, &build.Quality, &build.State, &build.Progress, &build.FailureReason,
		&build.Payload, &createdAtMS, &updatedAtMS); err != nil {
		return OfflinePackBuild{}, err
	}
	build.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	build.UpdatedAt = time.UnixMilli(updatedAtMS).UTC()
	return build, nil
}

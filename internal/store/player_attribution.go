package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// PlayerCommandedAttribution records the server-bound originator of a
// play-media session-control command addressed to a Player display Session
// (ADR-0240 D5).
type PlayerCommandedAttribution struct {
	SessionID               SessionID
	MediaID                 MediaID
	OriginatorProfileID     ProfileID
	OriginatorSessionID     SessionID
	PlayerBindingGeneration int
	CommandedAt             time.Time
}

// PlaybackGrant binds a display playback session's issued playback grant to
// the server-resolved originator Profile and player binding generation
// (ADR-0240 D5).
type PlaybackGrant struct {
	GrantID                 string
	SessionID               SessionID
	DeviceID                DeviceID
	ProfileID               ProfileID
	MediaID                 MediaID
	OriginatorProfileID     ProfileID
	OriginatorSessionID     SessionID
	PlayerBindingGeneration int
	WalkUp                  bool
	Ambient                 bool
	LastPositionSeconds     int
	LastDurationSeconds     int
	CreatedAt               time.Time
	ExpiresAt               time.Time
	EndedAt                 time.Time
	CancelledAt             time.Time
}

// PlayerReportResolution describes the outcome of resolving and fencing a
// Player display's progress/completion report against its playback grant.
type PlayerReportResolution struct {
	Accepted            bool
	Duplicate           bool
	DropReason          string
	GrantID             string
	OriginatorProfileID ProfileID
	Ended               bool
	PriorProgress       PlaybackProgress
	Progress            PlaybackProgress
}

// NewPlaybackGrantID returns a random opaque server-issued playback grant ID.
func NewPlaybackGrantID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "pg-" + hex.EncodeToString(raw[:]), nil
}

// ActivePlayerCommandedAttribution returns the active server-recorded
// originator for (sessionID, mediaID) if a play-media command is still active
// within PlayerCommandedWindow before now.
func (s *Store) ActivePlayerCommandedAttribution(ctx context.Context, sessionID SessionID, mediaID MediaID, now time.Time) (PlayerCommandedAttribution, bool, error) {
	if sessionID == "" || mediaID == "" {
		return PlayerCommandedAttribution{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PlayerCommandedAttribution{}, false, err
	}
	return activePlayerCommandedAttributionSQL(ctx, s.eventsR, sessionID, mediaID, now)
}

func activePlayerCommandedAttributionSQL(ctx context.Context, q queryRower, sessionID SessionID, mediaID MediaID, now time.Time) (PlayerCommandedAttribution, bool, error) {
	cutoffMS := timeToMillis(normalizeTime(now).Add(-PlayerCommandedWindow))
	var out PlayerCommandedAttribution
	var commandedAtMS int64
	err := q.QueryRowContext(ctx, `
		SELECT session_id, media_id, originator_profile_id, originator_session_id,
		       player_binding_generation, commanded_at_ms
		FROM player_commanded_media
		WHERE session_id = ? AND media_id = ?
		  AND active_for_attribution = 1
		  AND commanded_at_ms >= ?
		LIMIT 1
	`, sessionID, mediaID, cutoffMS).Scan(
		&out.SessionID,
		&out.MediaID,
		&out.OriginatorProfileID,
		&out.OriginatorSessionID,
		&out.PlayerBindingGeneration,
		&commandedAtMS,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PlayerCommandedAttribution{}, false, nil
	}
	if err != nil {
		return PlayerCommandedAttribution{}, false, err
	}
	out.CommandedAt = millisToTime(commandedAtMS)
	return out, true, nil
}

// SkipPlayerCommandedAttribution marks a pending commanded entry inactive for
// attribution so it cannot become current again after failing becoming-current
// checks (e.g. deleted originator Profile).
func (s *Store) SkipPlayerCommandedAttribution(ctx context.Context, sessionID SessionID, mediaID MediaID, revision int64) error {
	if sessionID == "" || mediaID == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.eventsW.ExecContext(ctx, `
		UPDATE player_commanded_media
		SET active_for_attribution = 0
		WHERE session_id = ? AND media_id = ?
		  AND COALESCE((SELECT revision FROM renderer_session_revision WHERE session_id = ?), 0) = ?
	`, sessionID, mediaID, sessionID, revision)
	return err
}

// CreatePlaybackGrant persists a newly issued playback grant and cancels any
// earlier active grant on the same display Session. It returns false if
// playback authorization changed while the stream decision was in flight.
func (s *Store) CreatePlaybackGrant(ctx context.Context, g PlaybackGrant, expectedRevision int64) (bool, error) {
	if g.GrantID == "" || g.SessionID == "" || g.ProfileID == "" || g.MediaID == "" {
		return false, ValidationError("playback grant is incomplete")
	}
	if g.ExpiresAt.IsZero() {
		return false, ValidationError("playback grant expiry is required")
	}
	createdAt := normalizeTime(g.CreatedAt)
	expiresAt := g.ExpiresAt.UTC()
	createdMS := timeToMillis(createdAt)
	expiresMS := timeToMillis(expiresAt)
	pruneBeforeMS := timeToMillis(createdAt.Add(-24 * time.Hour))

	s.sessionBindMu.RLock()
	defer s.sessionBindMu.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var revision int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE((SELECT revision FROM renderer_session_revision WHERE session_id = ?), 0)
	`, g.SessionID).Scan(&revision); err != nil {
		return false, err
	}
	if revision != expectedRevision {
		return false, nil
	}
	live, walkUp, err := s.livePlayerPlaybackScopeLocked(ctx, g.SessionID, g.DeviceID, g.ProfileID, g.PlayerBindingGeneration)
	if err != nil || !live {
		return false, err
	}
	party, err := s.isPlayerPartyCurrentMediaLocked(ctx, tx, g.SessionID, g.MediaID)
	if err != nil {
		return false, err
	}
	switch {
	case g.OriginatorProfileID != "":
		if party {
			return false, nil
		}
		attr, active, err := activePlayerCommandedAttributionSQL(ctx, tx, g.SessionID, g.MediaID, createdAt)
		if err != nil || !active {
			return false, err
		}
		if attr.OriginatorProfileID != g.OriginatorProfileID || attr.OriginatorSessionID != g.OriginatorSessionID ||
			attr.PlayerBindingGeneration != g.PlayerBindingGeneration {
			return false, nil
		}
		if allowed, err := isPlayerControllerLocked(ctx, s.catalogR, g.ProfileID, g.OriginatorProfileID); err != nil || !allowed {
			return false, err
		}
	case g.WalkUp:
		if !walkUp {
			return false, nil
		}
	default:
		if !party {
			return false, nil
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM playback_grant WHERE expires_at_ms <= ?`, pruneBeforeMS); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE playback_grant
		SET cancelled_at_ms = ?
		WHERE session_id = ? AND grant_id <> ?
		  AND ended_at_ms IS NULL AND cancelled_at_ms IS NULL
	`, createdMS, g.SessionID, g.GrantID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO playback_grant(
			grant_id, session_id, device_id, profile_id, media_id,
			originator_profile_id, originator_session_id, player_binding_generation,
			walk_up, ambient, last_position_seconds, last_duration_seconds,
			created_at_ms, expires_at_ms
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		g.GrantID, g.SessionID, g.DeviceID, g.ProfileID, g.MediaID,
		g.OriginatorProfileID, g.OriginatorSessionID, g.PlayerBindingGeneration,
		boolToInt(g.WalkUp), boolToInt(g.Ambient), g.LastPositionSeconds, g.LastDurationSeconds,
		createdMS, expiresMS,
	); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// PlaybackGrant loads a persisted playback grant by ID.
func (s *Store) PlaybackGrant(ctx context.Context, grantID string) (PlaybackGrant, bool, error) {
	if grantID == "" {
		return PlaybackGrant{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PlaybackGrant{}, false, err
	}
	row := s.eventsR.QueryRowContext(ctx, `
		SELECT grant_id, session_id, device_id, profile_id, media_id,
		       originator_profile_id, originator_session_id, player_binding_generation,
		       walk_up, ambient, last_position_seconds, last_duration_seconds,
		       created_at_ms, expires_at_ms, ended_at_ms, cancelled_at_ms
		FROM playback_grant
		WHERE grant_id = ?
	`, grantID)
	g, err := scanPlaybackGrant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PlaybackGrant{}, false, nil
	}
	if err != nil {
		return PlaybackGrant{}, false, err
	}
	return g, true, nil
}

// ActivePlaybackGrantForSession returns the latest active (not ended,
// cancelled, or expired) playback grant for (sessionID, mediaID).
func (s *Store) ActivePlaybackGrantForSession(ctx context.Context, sessionID SessionID, mediaID MediaID, now time.Time) (PlaybackGrant, bool, error) {
	if sessionID == "" || mediaID == "" {
		return PlaybackGrant{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PlaybackGrant{}, false, err
	}
	nowMS := timeToMillis(normalizeTime(now))
	row := s.eventsR.QueryRowContext(ctx, `
		SELECT grant_id, session_id, device_id, profile_id, media_id,
		       originator_profile_id, originator_session_id, player_binding_generation,
		       walk_up, ambient, last_position_seconds, last_duration_seconds,
		       created_at_ms, expires_at_ms, ended_at_ms, cancelled_at_ms
		FROM playback_grant
		WHERE session_id = ? AND media_id = ?
		  AND ended_at_ms IS NULL AND cancelled_at_ms IS NULL AND expires_at_ms > ?
		ORDER BY created_at_ms DESC, rowid DESC
		LIMIT 1
	`, sessionID, mediaID, nowMS)
	g, err := scanPlaybackGrant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PlaybackGrant{}, false, nil
	}
	if err != nil {
		return PlaybackGrant{}, false, err
	}
	return g, true, nil
}

func scanPlaybackGrant(row queryScanner) (PlaybackGrant, error) {
	var g PlaybackGrant
	var walkUp, ambient int
	var createdMS, expiresMS int64
	var endedMS, cancelledMS sql.NullInt64
	err := row.Scan(
		&g.GrantID, &g.SessionID, &g.DeviceID, &g.ProfileID, &g.MediaID,
		&g.OriginatorProfileID, &g.OriginatorSessionID, &g.PlayerBindingGeneration,
		&walkUp, &ambient, &g.LastPositionSeconds, &g.LastDurationSeconds,
		&createdMS, &expiresMS, &endedMS, &cancelledMS,
	)
	if err != nil {
		return PlaybackGrant{}, err
	}
	g.WalkUp = walkUp != 0
	g.Ambient = ambient != 0
	g.CreatedAt = millisToTime(createdMS)
	g.ExpiresAt = millisToTime(expiresMS)
	if endedMS.Valid {
		g.EndedAt = millisToTime(endedMS.Int64)
	}
	if cancelledMS.Valid {
		g.CancelledAt = millisToTime(cancelledMS.Int64)
	}
	return g, nil
}

type queryScanner interface {
	Scan(dest ...any) error
}

// livePlayerPlaybackScopeLocked checks the catalog binding while the caller
// holds sessionBindMu and mu for the life of the events transaction.
func (s *Store) livePlayerPlaybackScopeLocked(ctx context.Context, sessionID SessionID, deviceID DeviceID, profileID ProfileID, generation int) (live, walkUp bool, err error) {
	var walkUpEnabled int
	err = s.catalogR.QueryRowContext(ctx, `
		SELECT p.walk_up_enabled
		FROM auth_session a
		JOIN device d ON d.id = a.device_id
		JOIN profile p ON p.id = d.player_profile_id
		WHERE a.id = ? AND a.profile_id = p.id AND p.id = ? AND p.disabled = 0
		  AND d.id = ? AND d.revoked = 0 AND d.player_binding_generation = ?
	`, sessionID, profileID, deviceID, generation).Scan(&walkUpEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	return err == nil, walkUpEnabled != 0, err
}

// RecordPlayerPlaybackReport fences the report against its explicitly named
// grant and commits the originator's engagement with the grant and sequence
// updates. The caller's report never determines the originator Profile.
func (s *Store) RecordPlayerPlaybackReport(
	ctx context.Context,
	sessionID SessionID,
	deviceID DeviceID,
	playerProfileID ProfileID,
	bindingGeneration int,
	report PlaybackProgress,
	finished bool,
	policy ResumePolicy,
	now time.Time,
) (PlayerReportResolution, error) {
	seq := report.ReportSequence
	if seq != nil && *seq < 0 {
		return PlayerReportResolution{}, ValidationError("sequence must be a non-negative integer")
	}
	if sessionID == "" || report.MediaID == "" {
		return PlayerReportResolution{DropReason: "missing-session-or-media"}, nil
	}
	if report.GrantID == "" {
		return PlayerReportResolution{DropReason: "missing-grant"}, nil
	}
	now = normalizeTime(now)
	nowMS := timeToMillis(now)

	s.sessionBindMu.RLock()
	defer s.sessionBindMu.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PlayerReportResolution{}, err
	}
	live, _, err := s.livePlayerPlaybackScopeLocked(ctx, sessionID, deviceID, playerProfileID, bindingGeneration)
	if err != nil {
		return PlayerReportResolution{}, err
	}
	if !live {
		return PlayerReportResolution{DropReason: "binding-generation-mismatch"}, nil
	}

	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return PlayerReportResolution{}, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		SELECT grant_id, session_id, device_id, profile_id, media_id,
		       originator_profile_id, originator_session_id, player_binding_generation,
		       walk_up, ambient, last_position_seconds, last_duration_seconds,
		       created_at_ms, expires_at_ms, ended_at_ms, cancelled_at_ms
		FROM playback_grant
		WHERE grant_id = ?
	`, report.GrantID)

	g, err := scanPlaybackGrant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PlayerReportResolution{DropReason: "unknown-grant"}, nil
	}
	if err != nil {
		return PlayerReportResolution{}, err
	}

	if g.SessionID != sessionID || (g.DeviceID != "" && deviceID != "" && g.DeviceID != deviceID) || g.MediaID != report.MediaID {
		return PlayerReportResolution{DropReason: "grant-scope-mismatch", GrantID: g.GrantID}, nil
	}
	if !g.EndedAt.IsZero() {
		return PlayerReportResolution{DropReason: "grant-ended", GrantID: g.GrantID}, nil
	}
	if !g.CancelledAt.IsZero() {
		return PlayerReportResolution{DropReason: "grant-cancelled", GrantID: g.GrantID}, nil
	}
	if !g.ExpiresAt.After(now) {
		return PlayerReportResolution{DropReason: "grant-expired", GrantID: g.GrantID}, nil
	}
	if g.PlayerBindingGeneration != bindingGeneration || g.ProfileID != playerProfileID {
		return PlayerReportResolution{DropReason: "binding-generation-mismatch", GrantID: g.GrantID}, nil
	}

	if seq != nil {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO playback_grant_report(grant_id, seq, received_at_ms)
			VALUES (?, ?, ?)
			ON CONFLICT(grant_id, seq) DO NOTHING
		`, g.GrantID, *seq, nowMS)
		if err != nil {
			return PlayerReportResolution{}, err
		}
		inserted, err := res.RowsAffected()
		if err != nil {
			return PlayerReportResolution{}, err
		}
		if inserted == 0 {
			return PlayerReportResolution{
				Duplicate:  true,
				DropReason: "duplicate-sequence",
				GrantID:    g.GrantID,
			}, nil
		}
	}

	pos := max(report.PositionSeconds, 0)
	dur := max(report.DurationSeconds, 0)
	if finished {
		if _, err := tx.ExecContext(ctx, `
			UPDATE playback_grant
			SET ended_at_ms = ?, last_position_seconds = ?, last_duration_seconds = ?
			WHERE grant_id = ?
		`, nowMS, pos, dur, g.GrantID); err != nil {
			return PlayerReportResolution{}, err
		}
		if g.OriginatorProfileID != "" {
			if _, err := tx.ExecContext(ctx, `
				UPDATE player_commanded_media
				SET active_for_attribution = 0
				WHERE session_id = ? AND media_id = ?
			`, sessionID, g.MediaID); err != nil {
				return PlayerReportResolution{}, err
			}
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE playback_grant
			SET last_position_seconds = ?, last_duration_seconds = ?
			WHERE grant_id = ?
		`, pos, dur, g.GrantID); err != nil {
			return PlayerReportResolution{}, err
		}
	}

	if g.WalkUp || g.Ambient || g.OriginatorProfileID == "" {
		if err := tx.Commit(); err != nil {
			return PlayerReportResolution{}, err
		}
		return PlayerReportResolution{
			DropReason: "engagement-silent",
			GrantID:    g.GrantID,
			Ended:      finished,
		}, nil
	}

	// Check that the originator Profile still exists, is enabled, and is not a
	// Player Profile. If deleted, the playing item finishes on its grant and
	// records nothing (ADR-0240 D5).
	var origKind ProfileKind
	var origDisabled int
	err = s.catalogR.QueryRowContext(ctx, `
		SELECT kind, disabled FROM profile WHERE id = ?
	`, g.OriginatorProfileID).Scan(&origKind, &origDisabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PlayerReportResolution{}, err
	}
	if errors.Is(err, sql.ErrNoRows) || origDisabled != 0 || origKind == ProfilePlayer {
		if err := tx.Commit(); err != nil {
			return PlayerReportResolution{}, err
		}
		return PlayerReportResolution{
			DropReason: "originator-unavailable",
			GrantID:    g.GrantID,
			Ended:      finished,
		}, nil
	}

	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return PlayerReportResolution{}, err
	}
	report.ProfileID = g.OriginatorProfileID
	report.DeviceID = deviceID
	event, err := newPlaybackProgressEvent(report, finished, policy, EngagementProvenance{}, nowMS)
	if err != nil {
		return PlayerReportResolution{}, err
	}
	prior, _, err := playbackProgressForSQL(ctx, tx, report.ProfileID, report.MediaID)
	if err != nil {
		return PlayerReportResolution{}, err
	}
	if err := appendPlaybackProjectionEvent(ctx, tx, event); err != nil {
		return PlayerReportResolution{}, err
	}
	if err := applyPlaybackProjectionEvent(ctx, tx, event); err != nil {
		return PlayerReportResolution{}, err
	}
	progress, _, err := playbackProgressForSQL(ctx, tx, report.ProfileID, report.MediaID)
	if err != nil {
		return PlayerReportResolution{}, err
	}

	if err := tx.Commit(); err != nil {
		return PlayerReportResolution{}, err
	}
	return PlayerReportResolution{
		Accepted:            true,
		GrantID:             g.GrantID,
		OriginatorProfileID: g.OriginatorProfileID,
		Ended:               finished,
		PriorProgress:       prior,
		Progress:            progress,
	}, nil
}

// ActiveGrantOriginatorForSession returns the originator Profile ID of the
// currently active playback grant for (sessionID, mediaID), if any.
func (s *Store) ActiveGrantOriginatorForSession(ctx context.Context, sessionID SessionID, mediaID MediaID, now time.Time) (ProfileID, bool) {
	g, ok, err := s.ActivePlaybackGrantForSession(ctx, sessionID, mediaID, now)
	if err != nil || !ok || g.OriginatorProfileID == "" || g.WalkUp || g.Ambient {
		return "", false
	}
	return g.OriginatorProfileID, true
}

// ensureNotPlayerProfileLocked refuses any engagement write targeting a
// Player Profile (ADR-0240 D1, D5).
func (s *Store) ensureNotPlayerProfileLocked(ctx context.Context, profileID ProfileID) error {
	if profileID == "" {
		return nil
	}
	var kind ProfileKind
	err := s.catalogR.QueryRowContext(ctx, `SELECT kind FROM profile WHERE id = ?`, profileID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if kind == ProfilePlayer {
		return ValidationError("player profiles do not record engagement")
	}
	return nil
}

func recordPlayerCommandedMediaWithOriginatorSQL(
	ctx context.Context,
	exec sqlExecer,
	sessionID SessionID,
	mediaID MediaID,
	originatorProfileID ProfileID,
	originatorSessionID SessionID,
	bindingGeneration int,
	now time.Time,
) error {
	nowMS := timeToMillis(normalizeTime(now))
	// Only one commanded item is active for attribution on a display session at
	// a time; older rows stay for the 12h walk-up-off media read window.
	if _, err := exec.ExecContext(ctx, `
		UPDATE player_commanded_media
		SET active_for_attribution = 0
		WHERE session_id = ? AND media_id <> ? AND active_for_attribution <> 0
	`, sessionID, mediaID); err != nil {
		return err
	}
	// A completion for an earlier play of this same Media must not clear
	// the replacement command before the display has requested its grant.
	if _, err := exec.ExecContext(ctx, `
		UPDATE playback_grant
		SET cancelled_at_ms = ?
		WHERE session_id = ? AND media_id = ?
		  AND ended_at_ms IS NULL AND cancelled_at_ms IS NULL
	`, nowMS, sessionID, mediaID); err != nil {
		return err
	}
	_, err := exec.ExecContext(ctx, `
		INSERT INTO player_commanded_media(
			session_id, media_id, commanded_at_ms,
			originator_profile_id, originator_session_id, player_binding_generation, active_for_attribution
		)
		VALUES (?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT(session_id, media_id) DO UPDATE SET
			commanded_at_ms = MAX(player_commanded_media.commanded_at_ms, excluded.commanded_at_ms),
			originator_profile_id = excluded.originator_profile_id,
			originator_session_id = excluded.originator_session_id,
			player_binding_generation = excluded.player_binding_generation,
			active_for_attribution = 1
	`, sessionID, mediaID, nowMS, originatorProfileID, originatorSessionID, bindingGeneration)
	return err
}

func stopPlayerSessionPlaybackSQL(ctx context.Context, exec sqlExecer, sessionID SessionID, now time.Time) error {
	if _, err := exec.ExecContext(ctx, `
		UPDATE player_commanded_media
		SET active_for_attribution = 0
		WHERE session_id = ? AND active_for_attribution <> 0
	`, sessionID); err != nil {
		return err
	}
	_, err := exec.ExecContext(ctx, `
		UPDATE playback_grant
		SET cancelled_at_ms = ?
		WHERE session_id = ? AND ended_at_ms IS NULL AND cancelled_at_ms IS NULL
	`, timeToMillis(now), sessionID)
	return err
}

func playbackPositionFromGrantOrOriginatorSQL(ctx context.Context, eventsR sqlReader, sessionID SessionID, profileID ProfileID, mediaID MediaID, now time.Time) (position, duration int, ok bool, err error) {
	nowMS := timeToMillis(normalizeTime(now))
	var grantPos, grantDur int
	var origProfile ProfileID
	err = eventsR.QueryRowContext(ctx, `
		SELECT last_position_seconds, last_duration_seconds, originator_profile_id
		FROM playback_grant
		WHERE session_id = ? AND media_id = ?
		  AND ended_at_ms IS NULL AND cancelled_at_ms IS NULL AND expires_at_ms > ?
		ORDER BY created_at_ms DESC, rowid DESC
		LIMIT 1
	`, sessionID, mediaID, nowMS).Scan(&grantPos, &grantDur, &origProfile)
	if err == nil {
		if grantPos > 0 || grantDur > 0 {
			return grantPos, grantDur, true, nil
		}
		if origProfile != "" {
			err = eventsR.QueryRowContext(ctx, `
				SELECT position_seconds, duration_seconds
				FROM playback_progress
				WHERE profile_id = ? AND media_id = ?
			`, origProfile, mediaID).Scan(&position, &duration)
			if err == nil {
				return position, duration, true, nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return 0, 0, false, err
			}
		}
		return 0, 0, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, err
	}

	err = eventsR.QueryRowContext(ctx, `
		SELECT position_seconds, duration_seconds
		FROM playback_progress
		WHERE profile_id = ? AND media_id = ?
	`, profileID, mediaID).Scan(&position, &duration)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	return position, duration, true, nil
}

func playbackResumePositionSQL(ctx context.Context, q queryRower, profileID ProfileID, mediaID MediaID) (int, bool, error) {
	var pos, watched int
	err := q.QueryRowContext(ctx, `
		SELECT position_seconds, watched
		FROM playback_progress
		WHERE profile_id = ? AND media_id = ?
	`, profileID, mediaID).Scan(&pos, &watched)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if watched != 0 || pos <= 0 {
		return 0, false, nil
	}
	return pos, true, nil
}

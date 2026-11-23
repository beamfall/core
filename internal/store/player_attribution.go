package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
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
	cutoffMS := timeToMillis(normalizeTime(now).Add(-PlayerCommandedWindow))
	var out PlayerCommandedAttribution
	var commandedAtMS int64
	err := s.eventsR.QueryRowContext(ctx, `
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
func (s *Store) SkipPlayerCommandedAttribution(ctx context.Context, sessionID SessionID, mediaID MediaID) error {
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
	`, sessionID, mediaID)
	return err
}

// CreatePlaybackGrant persists a newly issued playback grant and cancels any
// earlier active grant on the same display Session.
func (s *Store) CreatePlaybackGrant(ctx context.Context, g PlaybackGrant) error {
	if g.GrantID == "" || g.SessionID == "" || g.ProfileID == "" || g.MediaID == "" {
		return ValidationError("playback grant is incomplete")
	}
	if g.ExpiresAt.IsZero() {
		return ValidationError("playback grant expiry is required")
	}
	createdAt := normalizeTime(g.CreatedAt)
	expiresAt := g.ExpiresAt.UTC()
	createdMS := timeToMillis(createdAt)
	expiresMS := timeToMillis(expiresAt)
	pruneBeforeMS := timeToMillis(createdAt.Add(-24 * time.Hour))

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM playback_grant WHERE expires_at_ms <= ?`, pruneBeforeMS); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE playback_grant
		SET cancelled_at_ms = ?
		WHERE session_id = ? AND grant_id <> ?
		  AND ended_at_ms IS NULL AND cancelled_at_ms IS NULL
	`, createdMS, g.SessionID, g.GrantID); err != nil {
		return err
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
		return err
	}
	return tx.Commit()
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

// ResolvePlayerPlaybackReport verifies a Player display's progress/completion
// report against its playback grant, binding generation, and (grant, seq)
// idempotency ledger. It resolves the originator Profile exclusively from the
// server-side grant (never from client input). Reports that are duplicates, on
// ended/cancelled/expired grants, on older binding generations, on walk-up or
// ambient grants, or for a deleted/disabled originator are dropped cleanly.
func (s *Store) ResolvePlayerPlaybackReport(
	ctx context.Context,
	sessionID SessionID,
	deviceID DeviceID,
	playerProfileID ProfileID,
	bindingGeneration int,
	mediaID MediaID,
	grantID string,
	seq *int64,
	positionSeconds int,
	durationSeconds int,
	finished bool,
	now time.Time,
) (PlayerReportResolution, error) {
	if seq != nil && *seq < 0 {
		return PlayerReportResolution{}, ValidationError("sequence must be a non-negative integer")
	}
	if sessionID == "" || mediaID == "" {
		return PlayerReportResolution{DropReason: "missing-session-or-media"}, nil
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

	// Re-check the device's live binding generation in catalog.db while holding
	// sessionBindMu.RLock() so a concurrent rebind cannot race with this report.
	var liveRevoked, liveGen int
	var livePlayerProfile string
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT d.revoked, COALESCE(d.player_profile_id, ''), d.player_binding_generation
		FROM auth_session a
		JOIN device d ON d.id = a.device_id
		WHERE a.id = ?
	`, sessionID).Scan(&liveRevoked, &livePlayerProfile, &liveGen)
	if errors.Is(err, sql.ErrNoRows) || liveRevoked != 0 ||
		ProfileID(livePlayerProfile) != playerProfileID || liveGen != bindingGeneration {
		return PlayerReportResolution{DropReason: "binding-generation-mismatch"}, nil
	}
	if err != nil {
		return PlayerReportResolution{}, err
	}

	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return PlayerReportResolution{}, err
	}
	defer tx.Rollback()

	var row *sql.Row
	if grantID != "" {
		row = tx.QueryRowContext(ctx, `
			SELECT grant_id, session_id, device_id, profile_id, media_id,
			       originator_profile_id, originator_session_id, player_binding_generation,
			       walk_up, ambient, last_position_seconds, last_duration_seconds,
			       created_at_ms, expires_at_ms, ended_at_ms, cancelled_at_ms
			FROM playback_grant
			WHERE grant_id = ?
		`, grantID)
	} else {
		row = tx.QueryRowContext(ctx, `
			SELECT grant_id, session_id, device_id, profile_id, media_id,
			       originator_profile_id, originator_session_id, player_binding_generation,
			       walk_up, ambient, last_position_seconds, last_duration_seconds,
			       created_at_ms, expires_at_ms, ended_at_ms, cancelled_at_ms
			FROM playback_grant
			WHERE session_id = ? AND media_id = ?
			ORDER BY created_at_ms DESC, rowid DESC
			LIMIT 1
		`, sessionID, mediaID)
	}
	g, err := scanPlaybackGrant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PlayerReportResolution{DropReason: "unknown-grant"}, nil
	}
	if err != nil {
		return PlayerReportResolution{}, err
	}

	if g.SessionID != sessionID || (g.DeviceID != "" && deviceID != "" && g.DeviceID != deviceID) || g.MediaID != mediaID {
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
	if g.PlayerBindingGeneration != liveGen || g.ProfileID != ProfileID(livePlayerProfile) {
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

	pos := max(positionSeconds, 0)
	dur := max(durationSeconds, 0)
	if finished {
		if _, err := tx.ExecContext(ctx, `
			UPDATE playback_grant
			SET ended_at_ms = ?, last_position_seconds = ?, last_duration_seconds = ?
			WHERE grant_id = ?
		`, nowMS, pos, dur, g.GrantID); err != nil {
			return PlayerReportResolution{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE player_commanded_media
			SET active_for_attribution = 0
			WHERE session_id = ? AND media_id = ?
		`, sessionID, mediaID); err != nil {
			return PlayerReportResolution{}, err
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
	if errors.Is(err, sql.ErrNoRows) || origDisabled != 0 || origKind == ProfilePlayer {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return PlayerReportResolution{}, err
		}
		if err := tx.Commit(); err != nil {
			return PlayerReportResolution{}, err
		}
		return PlayerReportResolution{
			DropReason: "originator-unavailable",
			GrantID:    g.GrantID,
			Ended:      finished,
		}, nil
	}

	if err := tx.Commit(); err != nil {
		return PlayerReportResolution{}, err
	}
	return PlayerReportResolution{
		Accepted:            true,
		GrantID:             g.GrantID,
		OriginatorProfileID: g.OriginatorProfileID,
		Ended:               finished,
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
	_, err := exec.ExecContext(ctx, `
		UPDATE player_commanded_media
		SET active_for_attribution = 0
		WHERE session_id = ? AND active_for_attribution <> 0
	`, sessionID)
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

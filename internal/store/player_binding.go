package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// PlayerCommandedWindow is how long a play-media session-control command
// authorizes a walk-up-off bound display Session to read the named Media
// (ADR-0240 D7).
const PlayerCommandedWindow = 12 * time.Hour

// SetDevicePlayerBinding binds deviceID to a Player Profile and sets its
// display name. Setting or changing a binding (including the first binding of
// an already-enrolled device) increments player_binding_generation and ends
// every Session of that device in one catalog transaction, then cancels any
// outstanding stream-token grants, commanded-media grants, and pending
// session-control commands for those sessions.
func (s *Store) SetDevicePlayerBinding(ctx context.Context, deviceID DeviceID, profileID ProfileID, displayName string) (Device, error) {
	if deviceID == "" {
		return Device{}, ValidationError("deviceID is required")
	}
	if profileID == "" {
		return Device{}, ValidationError("profileID is required")
	}
	displayName = strings.TrimSpace(displayName)

	s.sessionBindMu.Lock()
	defer s.sessionBindMu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Device{}, err
	}

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Device{}, err
	}
	defer tx.Rollback()

	var deviceName string
	var revoked int
	err = tx.QueryRowContext(ctx, `SELECT name, revoked FROM device WHERE id = ?`, deviceID).Scan(&deviceName, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ValidationError("device not found")
	}
	if err != nil {
		return Device{}, err
	}
	if revoked != 0 {
		return Device{}, ValidationError("device is revoked")
	}
	if displayName == "" {
		displayName = deviceName
	}

	var kind ProfileKind
	var disabled int
	err = tx.QueryRowContext(ctx, `SELECT kind, disabled FROM profile WHERE id = ?`, profileID).Scan(&kind, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ValidationError("profile not found")
	}
	if err != nil {
		return Device{}, err
	}
	if kind != ProfilePlayer {
		return Device{}, ValidationError("target profile must be kind player")
	}
	if disabled != 0 {
		return Device{}, ErrProfileDisabled
	}

	sessionIDs, err := deviceSessionIDsTx(ctx, tx, deviceID)
	if err != nil {
		return Device{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE device
		SET player_profile_id = ?,
		    player_display_name = ?,
		    player_binding_generation = player_binding_generation + 1
		WHERE id = ? AND revoked = 0
	`, profileID, displayName, deviceID); err != nil {
		return Device{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_session WHERE device_id = ?`, deviceID); err != nil {
		return Device{}, err
	}
	if err := tx.Commit(); err != nil {
		return Device{}, err
	}

	s.cleanupEndedDeviceSessions(ctx, deviceID, sessionIDs)
	return s.deviceSQL(ctx, deviceID)
}

// ClearDevicePlayerBinding removes deviceID's player binding, increments its
// player_binding_generation, ends every Session of that device in the same
// transaction, and cancels its outstanding stream-token grants and pending
// session-control commands.
func (s *Store) ClearDevicePlayerBinding(ctx context.Context, deviceID DeviceID) (Device, error) {
	if deviceID == "" {
		return Device{}, ValidationError("deviceID is required")
	}

	s.sessionBindMu.Lock()
	defer s.sessionBindMu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Device{}, err
	}

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Device{}, err
	}
	defer tx.Rollback()

	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM device WHERE id = ?`, deviceID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ValidationError("device not found")
	}
	if err != nil {
		return Device{}, err
	}

	sessionIDs, err := deviceSessionIDsTx(ctx, tx, deviceID)
	if err != nil {
		return Device{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE device
		SET player_profile_id = NULL,
		    player_display_name = '',
		    player_binding_generation = player_binding_generation + 1
		WHERE id = ?
	`, deviceID); err != nil {
		return Device{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_session WHERE device_id = ?`, deviceID); err != nil {
		return Device{}, err
	}
	if err := tx.Commit(); err != nil {
		return Device{}, err
	}

	s.cleanupEndedDeviceSessions(ctx, deviceID, sessionIDs)
	return s.deviceSQL(ctx, deviceID)
}

// SetPlayerWalkUp sets walk_up_enabled on a Player Profile (ADR-0240 D7).
func (s *Store) SetPlayerWalkUp(ctx context.Context, profileID ProfileID, enabled bool) (Profile, error) {
	if profileID == "" {
		return Profile{}, ValidationError("profileID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Profile{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback()

	var kind ProfileKind
	err = tx.QueryRowContext(ctx, `SELECT kind FROM profile WHERE id = ?`, profileID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ValidationError("profile not found")
	}
	if err != nil {
		return Profile{}, err
	}
	if kind != ProfilePlayer {
		return Profile{}, ValidationError("walk-up applies to player profiles only")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE profile SET walk_up_enabled = ? WHERE id = ?`, boolToInt(enabled), profileID); err != nil {
		return Profile{}, err
	}
	if err := tx.Commit(); err != nil {
		return Profile{}, err
	}
	return s.profileSQL(ctx, profileID)
}

// DeleteProfile deletes a non-owner Profile. Deleting a Player Profile
// revokes every Device bound to it and ends their Sessions in the same
// transaction (ADR-0240 D1).
func (s *Store) DeleteProfile(ctx context.Context, id ProfileID) error {
	if id == "" {
		return ValidationError("profile ID is required")
	}
	s.sessionBindMu.Lock()
	defer s.sessionBindMu.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var isOwner int
	err = tx.QueryRowContext(ctx, `SELECT is_owner FROM profile WHERE id = ?`, id).Scan(&isOwner)
	if errors.Is(err, sql.ErrNoRows) {
		return ValidationError("profile not found")
	}
	if err != nil {
		return err
	}
	if isOwner != 0 {
		return ValidationError("cannot delete the owner profile")
	}

	boundDevices, err := playerBoundDeviceIDsTx(ctx, tx, id)
	if err != nil {
		return err
	}
	sessionIDs, err := profileAndDeviceSessionIDsTx(ctx, tx, id, boundDevices)
	if err != nil {
		return err
	}

	if len(boundDevices) > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE device
			SET revoked = 1,
			    player_profile_id = NULL,
			    player_display_name = '',
			    player_binding_generation = player_binding_generation + 1
			WHERE player_profile_id = ?
		`, id); err != nil {
			return err
		}
		for _, devID := range boundDevices {
			if _, err := tx.ExecContext(ctx, `DELETE FROM auth_session WHERE device_id = ?`, devID); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_session WHERE profile_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pairing_code WHERE profile_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scoped_api_key WHERE profile_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM circle_shadow_principal WHERE policy_profile_id = ? OR created_by_profile_id = ?`, id, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM server_config WHERE key = ?`, profileEntryPINConfigKey(id)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM profile WHERE id = ?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	for _, devID := range boundDevices {
		s.cleanupEndedDeviceSessions(ctx, devID, nil)
	}
	s.cleanupEndedSessionRows(ctx, sessionIDs)
	return nil
}

// IsPlayerCommandedMedia reports whether a play-media session-control command
// addressed to sessionID named mediaID within PlayerCommandedWindow before now.
func (s *Store) IsPlayerCommandedMedia(ctx context.Context, sessionID SessionID, mediaID MediaID, now time.Time) (bool, error) {
	if sessionID == "" || mediaID == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	cutoffMS := timeToMillis(normalizeTime(now).Add(-PlayerCommandedWindow))
	var found int
	err := s.eventsR.QueryRowContext(ctx, `
		SELECT 1
		FROM player_commanded_media
		WHERE session_id = ? AND media_id = ? AND commanded_at_ms >= ?
		LIMIT 1
	`, sessionID, mediaID, cutoffMS).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return found != 0, nil
}

func (s *Store) sessionDevicePlayerBoundLocked(ctx context.Context, sessionID SessionID) (bool, error) {
	var bound int
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT 1
		FROM auth_session s
		JOIN device d ON d.id = s.device_id
		WHERE s.id = ? AND d.player_profile_id IS NOT NULL AND d.player_profile_id <> ''
		LIMIT 1
	`, sessionID).Scan(&bound)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bound != 0, nil
}

func recordPlayerCommandedMediaSQL(ctx context.Context, exec sqlExecer, sessionID SessionID, mediaID MediaID, now time.Time) error {
	nowMS := timeToMillis(normalizeTime(now))
	_, err := exec.ExecContext(ctx, `
		INSERT INTO player_commanded_media(session_id, media_id, commanded_at_ms)
		VALUES (?, ?, ?)
		ON CONFLICT(session_id, media_id) DO UPDATE SET
			commanded_at_ms = MAX(player_commanded_media.commanded_at_ms, excluded.commanded_at_ms)
	`, sessionID, mediaID, nowMS)
	return err
}

func deviceSessionIDsTx(ctx context.Context, tx *sql.Tx, deviceID DeviceID) ([]SessionID, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM auth_session WHERE device_id = ?`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []SessionID
	for rows.Next() {
		var id SessionID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func playerBoundDeviceIDsTx(ctx context.Context, tx *sql.Tx, profileID ProfileID) ([]DeviceID, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM device WHERE player_profile_id = ?`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []DeviceID
	for rows.Next() {
		var id DeviceID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func profileAndDeviceSessionIDsTx(ctx context.Context, tx *sql.Tx, profileID ProfileID, devices []DeviceID) ([]SessionID, error) {
	seen := map[SessionID]bool{}
	var out []SessionID
	add := func(ids []SessionID) {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM auth_session WHERE profile_id = ?`, profileID)
	if err != nil {
		return nil, err
	}
	var profSessions []SessionID
	for rows.Next() {
		var id SessionID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		profSessions = append(profSessions, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	add(profSessions)
	for _, devID := range devices {
		devSessions, err := deviceSessionIDsTx(ctx, tx, devID)
		if err != nil {
			return nil, err
		}
		add(devSessions)
	}
	return out, nil
}

func (s *Store) cleanupEndedDeviceSessions(ctx context.Context, deviceID DeviceID, sessionIDs []SessionID) {
	nowMS := timeToMillis(time.Now().UTC())
	if deviceID != "" {
		_, _ = s.eventsW.ExecContext(ctx, `DELETE FROM stream_token_ledger WHERE device_id = ?`, deviceID)
		_, _ = s.eventsW.ExecContext(ctx, `
			UPDATE playback_grant
			SET cancelled_at_ms = ?
			WHERE device_id = ? AND ended_at_ms IS NULL AND cancelled_at_ms IS NULL
		`, nowMS, deviceID)
	}
	s.cleanupEndedSessionRows(ctx, sessionIDs)
}

func (s *Store) cleanupEndedSessionRows(ctx context.Context, sessionIDs []SessionID) {
	nowMS := timeToMillis(time.Now().UTC())
	for _, id := range sessionIDs {
		_, _ = s.eventsW.ExecContext(ctx, `DELETE FROM stream_token_ledger WHERE session_id = ?`, id)
		_, _ = s.eventsW.ExecContext(ctx, `DELETE FROM player_commanded_media WHERE session_id = ?`, id)
		_, _ = s.eventsW.ExecContext(ctx, `
			UPDATE playback_grant
			SET cancelled_at_ms = ?
			WHERE session_id = ? AND ended_at_ms IS NULL AND cancelled_at_ms IS NULL
		`, nowMS, id)
		_, _ = s.eventsW.ExecContext(ctx,
			`DELETE FROM event_outbox WHERE kind = ? AND aggregate_type = ? AND aggregate_id = ?`,
			SessionControlKind, OutboxAggregateSession, id,
		)
	}
}

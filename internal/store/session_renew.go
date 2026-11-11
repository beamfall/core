package store

import (
	"context"
	"database/sql"
	"errors"
)

// ErrNoDeviceSession means the device has no enrolled session row to renew
// from, so its Profile cannot be resolved.
var ErrNoDeviceSession = errors.New("device has no session")

// ErrDeviceRevoked means the device is revoked and may not hold a session.
var ErrDeviceRevoked = errors.New("device is revoked")

// LatestDeviceSession returns the device's enrolled session with the latest
// expiry. auth_session has no creation time; sessions are minted with a fixed
// lifetime, so the latest expiry is the most recently minted one. Session
// renewal uses it to resolve the Profile when the browser's old session row is
// gone (CORE-1106).
func (s *Store) LatestDeviceSession(ctx context.Context, deviceID DeviceID) (Session, error) {
	if deviceID == "" {
		return Session{}, ValidationError("device ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Session{}, err
	}
	var id SessionID
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT id FROM auth_session
		WHERE device_id = ? AND (kind = ? OR kind IS NULL OR kind = '')
		ORDER BY expires_at_ms DESC LIMIT 1`,
		deviceID, SessionKindEnrolled,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNoDeviceSession
	}
	if err != nil {
		return Session{}, err
	}
	return s.sessionSQL(ctx, id)
}

// RenewDeviceSession replaces a device's session in one transaction: it
// deletes oldSessionID when that row belongs to the device (an absent row is
// fine, it may have been swept) and inserts newSession as an enrolled session.
// It refuses a revoked or unknown device and a disabled Profile. The new
// session starts without Reveal or an admin window. No device secret is
// checked here; the caller verifies it first (CORE-1106).
func (s *Store) RenewDeviceSession(ctx context.Context, oldSessionID SessionID, newSession Session) error {
	if newSession.ID == "" || newSession.DeviceID == "" || newSession.ProfileID == "" {
		return ValidationError("session identity is required")
	}
	if newSession.ExpiresAt.IsZero() {
		return ValidationError("session expiry is required")
	}
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
	var revoked int
	err = tx.QueryRowContext(ctx, `SELECT revoked FROM device WHERE id = ?`, newSession.DeviceID).Scan(&revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("device not found")
	}
	if err != nil {
		return err
	}
	if revoked != 0 {
		return ErrDeviceRevoked
	}
	disabled, err := profileDisabled(ctx, tx, newSession.ProfileID)
	if err != nil {
		return err
	}
	if disabled {
		return ErrProfileDisabled
	}
	if oldSessionID != "" {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM auth_session WHERE id = ? AND device_id = ?`,
			oldSessionID, newSession.DeviceID,
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO auth_session(id, device_id, profile_id, revealed, expires_at_ms, kind) VALUES (?, ?, ?, 0, ?, ?)",
		newSession.ID, newSession.DeviceID, newSession.ProfileID, nullableMillis(newSession.ExpiresAt), SessionKindEnrolled,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if oldSessionID != "" {
		// Best-effort, as in TerminateSession: the lazy sweep removes the
		// rows at expiry regardless.
		_, _ = s.eventsW.ExecContext(ctx, `DELETE FROM stream_token_ledger WHERE session_id = ?`, oldSessionID)
	}
	return nil
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNoDeviceSession means the device has no enrolled session row to renew,
// so it must enroll again.
var ErrNoDeviceSession = errors.New("device has no session")

// ErrDeviceRevoked means the device is revoked and may not hold a session.
var ErrDeviceRevoked = errors.New("device is revoked")

// RenewDeviceSession replaces the device's enrolled session with a new one
// in one transaction and returns it (CORE-1106). The device must still hold a
// session row, expired or not: the new session keeps that row's Profile, and
// every enrolled row of the device is deleted, so a device holds at most one
// session after renewal and a device secret cannot stockpile spare sessions.
// A device with no row (after TerminateSession, say) gets
// ErrNoDeviceSession and must enroll again. It refuses a revoked or unknown
// device and a disabled Profile. The new session starts without Reveal or an
// admin window. No device secret is checked here; the caller verifies it
// first.
func (s *Store) RenewDeviceSession(ctx context.Context, deviceID DeviceID, newID SessionID, expiresAt time.Time) (Session, error) {
	if deviceID == "" || newID == "" {
		return Session{}, ValidationError("session identity is required")
	}
	if expiresAt.IsZero() {
		return Session{}, ValidationError("session expiry is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Session{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	var revoked int
	err = tx.QueryRowContext(ctx, `SELECT revoked FROM device WHERE id = ?`, deviceID).Scan(&revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, errors.New("device not found")
	}
	if err != nil {
		return Session{}, err
	}
	if revoked != 0 {
		return Session{}, ErrDeviceRevoked
	}
	replaced, profileID, err := deviceEnrolledSessions(ctx, tx, deviceID)
	if err != nil {
		return Session{}, err
	}
	if len(replaced) == 0 {
		return Session{}, ErrNoDeviceSession
	}
	disabled, err := profileDisabled(ctx, tx, profileID)
	if err != nil {
		return Session{}, err
	}
	if disabled {
		return Session{}, ErrProfileDisabled
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM auth_session WHERE device_id = ? AND (kind = ? OR kind IS NULL OR kind = '')`,
		deviceID, SessionKindEnrolled,
	); err != nil {
		return Session{}, err
	}
	session := Session{ID: newID, DeviceID: deviceID, ProfileID: profileID, ExpiresAt: expiresAt, Kind: SessionKindEnrolled}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO auth_session(id, device_id, profile_id, revealed, expires_at_ms, kind) VALUES (?, ?, ?, 0, ?, ?)",
		session.ID, session.DeviceID, session.ProfileID, nullableMillis(session.ExpiresAt), SessionKindEnrolled,
	); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	for _, id := range replaced {
		// Best-effort, as in TerminateSession: the lazy sweep removes the
		// rows at expiry regardless.
		_, _ = s.eventsW.ExecContext(ctx, `DELETE FROM stream_token_ledger WHERE session_id = ?`, id)
	}
	return session, nil
}

// deviceEnrolledSessions lists the device's enrolled session IDs, latest
// expiry first, and the Profile of the latest. auth_session has no creation
// time; sessions are minted with a fixed lifetime, so the latest expiry is
// the most recently minted one.
func deviceEnrolledSessions(ctx context.Context, tx *sql.Tx, deviceID DeviceID) ([]SessionID, ProfileID, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, profile_id FROM auth_session
		WHERE device_id = ? AND (kind = ? OR kind IS NULL OR kind = '')
		ORDER BY expires_at_ms DESC`,
		deviceID, SessionKindEnrolled,
	)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var (
		ids     []SessionID
		profile ProfileID
	)
	for rows.Next() {
		var id SessionID
		var rowProfile ProfileID
		if err := rows.Scan(&id, &rowProfile); err != nil {
			return nil, "", err
		}
		if len(ids) == 0 {
			profile = rowProfile
		}
		ids = append(ids, id)
	}
	return ids, profile, rows.Err()
}

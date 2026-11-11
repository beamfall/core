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
// every other enrolled row of the device is deleted, so a device holds at most
// one session after renewal and a device secret cannot stockpile spare
// sessions. When the device's session was minted within renewConvergeWindow
// (an overlapping renewal from another tab), that session is returned rather
// than replaced, so concurrent renewals converge on one live session.
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
	rows, err := deviceEnrolledSessions(ctx, tx, deviceID)
	if err != nil {
		return Session{}, err
	}
	if len(rows) == 0 {
		return Session{}, ErrNoDeviceSession
	}
	latest := rows[0]
	disabled, err := profileDisabled(ctx, tx, latest.ProfileID)
	if err != nil {
		return Session{}, err
	}
	if disabled {
		return Session{}, ErrProfileDisabled
	}
	session := Session{ID: newID, DeviceID: deviceID, ProfileID: latest.ProfileID, ExpiresAt: expiresAt, Kind: SessionKindEnrolled}
	keep := SessionID("")
	if latest.ExpiresAtMs.Valid && !millisToTime(latest.ExpiresAtMs.Int64).Before(expiresAt.Add(-renewConvergeWindow)) {
		// An overlapping renewal from this device (another tab) committed a
		// moment ago: hand back its session instead of replacing it, so both
		// callers converge on one live session.
		keep = latest.ID
		session.ID = latest.ID
		session.ExpiresAt = millisToTime(latest.ExpiresAtMs.Int64)
	}
	var replaced []SessionID
	for _, row := range rows {
		if row.ID == keep {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_session WHERE id = ?`, row.ID); err != nil {
			return Session{}, err
		}
		replaced = append(replaced, row.ID)
	}
	if keep == "" {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO auth_session(id, device_id, profile_id, revealed, expires_at_ms, kind) VALUES (?, ?, ?, 0, ?, ?)",
			session.ID, session.DeviceID, session.ProfileID, nullableMillis(session.ExpiresAt), SessionKindEnrolled,
		); err != nil {
			return Session{}, err
		}
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

// renewConvergeWindow is how recently a device's session must have been
// minted for a renewal to return it instead of replacing it. auth_session has
// no creation time; renewals mint sessions with one fixed lifetime, so a
// session whose expiry is within this window of the requested expiry was
// minted within this window of now.
const renewConvergeWindow = time.Minute

type deviceSessionRow struct {
	ID          SessionID
	ProfileID   ProfileID
	ExpiresAtMs sql.NullInt64
}

// deviceEnrolledSessions lists the device's enrolled sessions, latest expiry
// first. auth_session has no creation time; sessions are minted with a fixed
// lifetime, so the latest expiry is the most recently minted one.
func deviceEnrolledSessions(ctx context.Context, tx *sql.Tx, deviceID DeviceID) ([]deviceSessionRow, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, profile_id, expires_at_ms FROM auth_session
		WHERE device_id = ? AND (kind = ? OR kind IS NULL OR kind = '')
		ORDER BY expires_at_ms DESC`,
		deviceID, SessionKindEnrolled,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []deviceSessionRow
	for rows.Next() {
		var row deviceSessionRow
		if err := rows.Scan(&row.ID, &row.ProfileID, &row.ExpiresAtMs); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

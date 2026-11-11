package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SignInSecurityMode is the owner's sign-in security choice (ADR-0007,
// CORE-1110). Home lets a browser on the server's network sign in with the
// admin password; Strict refuses that path, so only an enrolled device, a
// Manager's pairing code or `beamfall pairing-code` on the host can sign in.
type SignInSecurityMode string

const (
	// SignInSecurityHome is the default.
	SignInSecurityHome SignInSecurityMode = "home"
	// SignInSecurityStrict turns off admin-password sign-in.
	SignInSecurityStrict SignInSecurityMode = "strict"
)

// configSignInSecurityMode stores the mode in server_config; an absent row is
// Home, so servers set up before CORE-1110 keep their behaviour.
const configSignInSecurityMode = "auth.signin_security_mode"

// ParseSignInSecurityMode maps a wire value to a mode. An empty value is Home;
// anything else unknown is a ValidationError.
func ParseSignInSecurityMode(raw string) (SignInSecurityMode, error) {
	switch SignInSecurityMode(raw) {
	case "", SignInSecurityHome:
		return SignInSecurityHome, nil
	case SignInSecurityStrict:
		return SignInSecurityStrict, nil
	}
	return "", ValidationError("securityMode must be home or strict")
}

// storedSignInSecurityMode reads a stored row leniently: anything but
// "strict" is Home.
func storedSignInSecurityMode(raw string) SignInSecurityMode {
	if SignInSecurityMode(raw) == SignInSecurityStrict {
		return SignInSecurityStrict
	}
	return SignInSecurityHome
}

// SignInSecurityMode returns the stored sign-in security mode.
func (s *Store) SignInSecurityMode(ctx context.Context) (SignInSecurityMode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	var raw string
	err := s.catalogR.QueryRowContext(ctx, `SELECT value FROM server_config WHERE key = ?`, configSignInSecurityMode).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return SignInSecurityHome, nil
	}
	if err != nil {
		return "", err
	}
	return storedSignInSecurityMode(raw), nil
}

// ErrAdminSignInOff means admin-password sign-in was refused because the
// sign-in security mode is Strict.
var ErrAdminSignInOff = errors.New("admin-password sign-in is off in strict mode")

// EnrollAdminSignInDevice enrolls device with an enrolled session on the
// owner Profile for admin-password sign-in (CORE-1106, CORE-1110). The mode,
// the owner and its disabled flag are read in the same transaction that
// inserts the rows, so a switch to Strict or an ownership change that commits
// while the caller waited for the password check cannot be overtaken. It
// returns ErrAdminSignInOff in Strict mode, ErrNoOwnerProfile with no owner
// and ErrProfileDisabled for a disabled owner, inserting nothing. The
// returned session carries the owner Profile.
func (s *Store) EnrollAdminSignInDevice(ctx context.Context, device Device, sessionID SessionID, expiresAt time.Time) (Session, error) {
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
	var raw string
	switch err := tx.QueryRowContext(ctx, `SELECT value FROM server_config WHERE key = ?`, configSignInSecurityMode).Scan(&raw); {
	case err == nil:
		if storedSignInSecurityMode(raw) == SignInSecurityStrict {
			return Session{}, ErrAdminSignInOff
		}
	case !errors.Is(err, sql.ErrNoRows):
		return Session{}, err
	}
	var (
		owner    ProfileID
		disabled int
	)
	err = tx.QueryRowContext(ctx, `SELECT id, disabled FROM profile WHERE is_owner = 1 LIMIT 1`).Scan(&owner, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNoOwnerProfile
	}
	if err != nil {
		return Session{}, err
	}
	if disabled != 0 {
		return Session{}, ErrProfileDisabled
	}
	device, session, err := normalizeEnrollment(device, Session{ID: sessionID, DeviceID: device.ID, ProfileID: owner, ExpiresAt: expiresAt})
	if err != nil {
		return Session{}, err
	}
	if err := insertDeviceSessionTx(ctx, tx, device, session); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return session, nil
}

// SetSignInSecurityMode stores mode and returns the mode it replaced, read in
// the same transaction so the caller's audit entry names the true old value.
// It checks no caller; a signed-in switch uses SetSignInSecurityModeAsOwner.
func (s *Store) SetSignInSecurityMode(ctx context.Context, mode SignInSecurityMode, now time.Time) (SignInSecurityMode, error) {
	old, _, err := s.setSignInSecurityMode(ctx, mode, now, nil)
	return old, err
}

// SetSignInSecurityModeAsOwner stores mode only while the caller still
// qualifies, checked in the same transaction as the write: session is an
// unexpired enrolled session bound to profile on a non-revoked Manager
// Device, and profile is the designated owner or, with no owner designated,
// an adult (the rule GrantAdminWindow applies). An ownership transfer or
// device revocation that commits during the caller's slow password check
// therefore cannot be overtaken. It reports false, writing nothing, when the
// caller no longer qualifies, and otherwise returns the mode it replaced.
func (s *Store) SetSignInSecurityModeAsOwner(ctx context.Context, session SessionID, profile ProfileID, mode SignInSecurityMode, now time.Time) (SignInSecurityMode, bool, error) {
	if session == "" || profile == "" {
		return "", false, ValidationError("session and profile are required")
	}
	return s.setSignInSecurityMode(ctx, mode, now, func(tx *sql.Tx) (bool, error) {
		var qualifies int
		err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM auth_session AS s
				JOIN device AS d ON d.id = s.device_id
				JOIN profile AS p ON p.id = s.profile_id
				WHERE s.id = ? AND s.profile_id = ?
					AND (s.kind = ? OR s.kind IS NULL OR s.kind = '')
					AND (s.expires_at_ms IS NULL OR s.expires_at_ms > ?)
					AND d.revoked = 0 AND d.role = ?
					AND p.kind = ?
					AND (p.is_owner = 1 OR NOT EXISTS (SELECT 1 FROM profile WHERE is_owner = 1)))`,
			session, profile, SessionKindEnrolled, timeToMillis(now), DeviceRoleManager, ProfileAdult,
		).Scan(&qualifies)
		return qualifies != 0, err
	})
}

// setSignInSecurityMode writes mode in one transaction, after guard (when
// set) confirms the caller still qualifies in that transaction.
func (s *Store) setSignInSecurityMode(ctx context.Context, mode SignInSecurityMode, now time.Time, guard func(*sql.Tx) (bool, error)) (SignInSecurityMode, bool, error) {
	mode, err := ParseSignInSecurityMode(string(mode))
	if err != nil {
		return "", false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", false, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	if guard != nil {
		ok, err := guard(tx)
		if err != nil || !ok {
			return "", false, err
		}
	}
	old := SignInSecurityHome
	var raw string
	switch err := tx.QueryRowContext(ctx, `SELECT value FROM server_config WHERE key = ?`, configSignInSecurityMode).Scan(&raw); {
	case err == nil:
		old = storedSignInSecurityMode(raw)
	case !errors.Is(err, sql.ErrNoRows):
		return "", false, err
	}
	if err := upsertSignInSecurityModeTx(ctx, tx, mode, now); err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return old, true, nil
}

func upsertSignInSecurityModeTx(ctx context.Context, tx *sql.Tx, mode SignInSecurityMode, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO server_config(key, value, updated_at_ms)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at_ms = excluded.updated_at_ms`,
		configSignInSecurityMode, string(mode), timeToMillis(now))
	return err
}

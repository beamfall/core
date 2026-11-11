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

// SetSignInSecurityMode stores mode and returns the mode it replaced, read in
// the same transaction so the caller's audit entry names the true old value.
func (s *Store) SetSignInSecurityMode(ctx context.Context, mode SignInSecurityMode, now time.Time) (SignInSecurityMode, error) {
	mode, err := ParseSignInSecurityMode(string(mode))
	if err != nil {
		return "", err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	old := SignInSecurityHome
	var raw string
	switch err := tx.QueryRowContext(ctx, `SELECT value FROM server_config WHERE key = ?`, configSignInSecurityMode).Scan(&raw); {
	case err == nil:
		old = storedSignInSecurityMode(raw)
	case !errors.Is(err, sql.ErrNoRows):
		return "", err
	}
	if err := upsertSignInSecurityModeTx(ctx, tx, mode, now); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return old, nil
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

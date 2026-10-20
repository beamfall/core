package store

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// ErrNoAdminPassword means no server admin password has been set yet (first
// run). Distinct from a mismatch so wiring can offer the first-run set path
// without leaking, to a network caller, whether a password exists.
var ErrNoAdminPassword = errors.New("no admin password set")

// ErrAdminPasswordMismatch means the supplied admin password did not verify.
var ErrAdminPasswordMismatch = errors.New("admin password mismatch")

// adminCredentialID is the single-row key: one server, one admin password
// (ADR-0007 §4 — household appliance, not enterprise).
const adminCredentialID = "server"

// The admin password is a human-chosen secret (low entropy relative to a
// random token), so a leaked catalog.db must not expose it to an offline
// brute force. It is salted with a random value and stretched with argon2id,
// reusing the reveal-PIN verifier parameters (internal/store/reveal.go). The
// scheme tag lets a future KDF migration verify-and-upgrade in place.
const adminArgon2Prefix = revealArgon2Prefix

// hashAdminPassword derives the scheme-tagged argon2id verifier for password
// under salt. The salt is stored alongside in admin_credential.password_salt.
func hashAdminPassword(password string, salt []byte) string {
	key := argon2.IDKey([]byte(password), salt, revealArgon2Time, revealArgon2Memory, revealArgon2Threads, revealArgon2KeyLen)
	return adminArgon2Prefix + hex.EncodeToString(key)
}

// seedAdminCredentialTx writes the server admin-credential verifier inside an
// open seed transaction with a caller-supplied (fixed) salt, so the seeded
// verifier is reproducible across re-seeds. It is used only by the seed path
// (acceptance profile) to plant a known admin password on a non-production
// host — never the random-salt SetAdminPassword. The plaintext password never
// leaves this function; only the salted argon2id verifier is stored.
func seedAdminCredentialTx(ctx context.Context, tx *sql.Tx, password string, salt []byte, now time.Time) error {
	if strings.TrimSpace(password) == "" {
		return ValidationError("admin password is required")
	}
	if len(salt) == 0 {
		return ValidationError("admin salt is required")
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO admin_credential(id, password_hash, password_salt, updated_at_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			password_hash = excluded.password_hash,
			password_salt = excluded.password_salt,
			updated_at_ms = excluded.updated_at_ms`,
		adminCredentialID, hashAdminPassword(password, salt), salt, timeToMillis(now))
	return err
}

// SetAdminPassword stores the server admin password verifier. Each call mints
// a fresh random salt and clears every open Admin Session window: password
// rotation/reset must force owner re-auth before any stale Console authority
// can continue. Reset is the same write from a
// filesystem-local caller; setting at first run is this call
// against an empty table. The plaintext password never leaves this function.
func (s *Store) SetAdminPassword(ctx context.Context, password string, now time.Time) error {
	if strings.TrimSpace(password) == "" {
		return ValidationError("admin password is required")
	}
	salt := make([]byte, revealSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
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
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO admin_credential(id, password_hash, password_salt, updated_at_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			password_hash = excluded.password_hash,
			password_salt = excluded.password_salt,
			updated_at_ms = excluded.updated_at_ms`,
		adminCredentialID, hashAdminPassword(password, salt), salt, timeToMillis(now)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_session SET admin_until_ms = NULL`); err != nil {
		return err
	}
	return tx.Commit()
}

// AdminPasswordSet reports whether a server admin password exists. Used by
// first-run wiring (do we bootstrap from env / present the setup wizard?) — never
// exposed to a network caller as a presence oracle.
func (s *Store) AdminPasswordSet(ctx context.Context) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var present int
	if err := s.catalogR.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM admin_credential WHERE id = ?`, adminCredentialID).Scan(&present); err != nil {
		return false, err
	}
	return present > 0, nil
}

// VerifyAdminPassword checks a presented admin password against the stored
// verifier in constant time. ErrNoAdminPassword (none set) and
// ErrAdminPasswordMismatch (wrong) are distinct so first-run wiring can branch;
// the network re-auth path must collapse both into one generic failure so a
// caller learns nothing about whether a password exists. Throttling and audit
// are the caller's responsibility, as with reveal/pairing.
func (s *Store) VerifyAdminPassword(ctx context.Context, password string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	var hash string
	var salt []byte
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT password_hash, password_salt FROM admin_credential WHERE id = ?`, adminCredentialID).Scan(&hash, &salt)
	if errors.Is(err, sql.ErrNoRows) {
		// Spend the KDF anyway so an unset password costs the same as a wrong
		// one — no timing oracle distinguishing "not set" from "wrong".
		hashAdminPassword(password, salt)
		return ErrNoAdminPassword
	}
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(hashAdminPassword(password, salt))) != 1 {
		return ErrAdminPasswordMismatch
	}
	return nil
}

// SetSessionAdminUntil stamps (or clears, with the zero time) the admin
// authorization window on a session row — the one server-side switch the
// Admin Session reads from, mirroring SetSessionRevealed for the Reveal
// contract. Every Console-op read re-derives admin authorization from this
// timestamp, so expiry and revocation are immediate.
func (s *Store) SetSessionAdminUntil(ctx context.Context, id SessionID, until time.Time) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	var arg any
	if !until.IsZero() {
		arg = timeToMillis(until)
	}
	result, err := s.catalogW.ExecContext(ctx,
		`UPDATE auth_session SET admin_until_ms = ? WHERE id = ?`, arg, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("session not found")
	}
	return nil
}

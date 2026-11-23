package store

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrNoRevealPath means the Profile cannot reveal: managed kind or no PIN set.
var ErrNoRevealPath = errors.New("profile has no reveal path")

// ErrRevealPINMismatch means the supplied PIN did not verify.
var ErrRevealPINMismatch = errors.New("reveal PIN mismatch")

// Reveal PINs are low-entropy (often a 4-digit code), so a leaked catalog.db
// must not expose them to an offline brute force. They are salted with a
// per-Profile random value and stretched with argon2id; the stored value is
// scheme-tagged so legacy bare-sha256 verifiers (no per-Profile salt) still
// verify and are upgraded in place on the next successful check.
const (
	revealArgon2Prefix = "argon2:"
	// argon2id parameters: one pass over 64 MiB with 4 lanes. Tuned to be
	// expensive against an offline guesser for a 4-digit PIN while staying
	// well under a second on a server-class core.
	revealArgon2Time    = 1
	revealArgon2Memory  = 64 * 1024
	revealArgon2Threads = 4
	revealArgon2KeyLen  = 32
	revealSaltLen       = 16
)

// hashRevealPINArgon2 derives the scheme-tagged argon2id verifier for pin under
// salt. The salt is stored alongside in profile.reveal_pin_salt.
func hashRevealPINArgon2(pin string, salt []byte) string {
	key := argon2.IDKey([]byte(pin), salt, revealArgon2Time, revealArgon2Memory, revealArgon2Threads, revealArgon2KeyLen)
	return revealArgon2Prefix + hex.EncodeToString(key)
}

// verifyRevealPINHash reports whether pin matches stored under salt. A stored
// value tagged argon2: is checked with argon2id (salt required); any other
// shape is treated as the legacy bare-sha256 verifier (HashDeviceSecret),
// which carried no per-Profile salt.
func verifyRevealPINHash(stored string, salt []byte, pin string) bool {
	if strings.HasPrefix(stored, revealArgon2Prefix) {
		if len(salt) == 0 {
			return false
		}
		want := hashRevealPINArgon2(pin, salt)
		return subtle.ConstantTimeCompare([]byte(stored), []byte(want)) == 1
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(HashDeviceSecret(pin))) == 1
}

// SetProfileRevealPIN stores the reveal PIN verifier for an adult Profile.
// Managed Profiles have no reveal path by construction. Each call
// mints a fresh per-Profile salt, so two Profiles with the same PIN never
// share a stored verifier.
func (s *Store) SetProfileRevealPIN(ctx context.Context, id ProfileID, pin string) error {
	if pin == "" {
		return ValidationError("reveal PIN is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	profile, err := s.profileSQL(ctx, id)
	if err != nil {
		return err
	}
	if profile.Kind != ProfileAdult {
		return ErrNoRevealPath
	}
	salt := make([]byte, revealSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	_, err = s.catalogW.ExecContext(ctx,
		`UPDATE profile SET reveal_pin_hash = ?, reveal_pin_salt = ? WHERE id = ?`,
		hashRevealPINArgon2(pin, salt), salt, id)
	return err
}

// ProfileRevealPINSet reports whether a Profile can reveal: an adult Profile
// with a stored reveal PIN verifier. It reads only the verifier's presence, so
// it says nothing about restricted Libraries (ADR-0004) and lets a client hide a
// PIN prompt that could never succeed (CORE-2462). Managed Profiles report false.
func (s *Store) ProfileRevealPINSet(ctx context.Context, id ProfileID) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var kind ProfileKind
	var hash sql.NullString
	if err := s.catalogR.QueryRowContext(ctx,
		`SELECT kind, reveal_pin_hash FROM profile WHERE id = ?`, id).Scan(&kind, &hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return kind == ProfileAdult && hash.Valid && hash.String != "", nil
}

// VerifyRevealPIN checks a reveal PIN for an adult Profile. A correct PIN whose
// stored verifier is still the legacy bare-sha256 form is re-stored as a salted
// argon2id verifier (upgrade-on-verify), so leaked-database brute-force
// resistance improves the first time the PIN is used after the upgrade.
func (s *Store) VerifyRevealPIN(ctx context.Context, id ProfileID, pin string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	profile, err := s.profileSQL(ctx, id)
	if err != nil {
		return err
	}
	if profile.Kind != ProfileAdult {
		return ErrNoRevealPath
	}
	var hash sql.NullString
	var salt []byte
	if err := s.catalogR.QueryRowContext(ctx,
		`SELECT reveal_pin_hash, reveal_pin_salt FROM profile WHERE id = ?`, id).Scan(&hash, &salt); err != nil {
		return err
	}
	if !hash.Valid || hash.String == "" {
		return ErrNoRevealPath
	}
	if !verifyRevealPINHash(hash.String, salt, pin) {
		return ErrRevealPINMismatch
	}
	if !strings.HasPrefix(hash.String, revealArgon2Prefix) {
		// Legacy bare-sha256 verifier just verified: upgrade it in place to a
		// salted argon2id verifier. A failed upgrade write does not fail the
		// verify — the PIN was correct.
		newSalt := make([]byte, revealSaltLen)
		if _, err := rand.Read(newSalt); err == nil {
			_, _ = s.catalogW.ExecContext(ctx,
				`UPDATE profile SET reveal_pin_hash = ?, reveal_pin_salt = ? WHERE id = ?`,
				hashRevealPINArgon2(pin, newSalt), newSalt, id)
		}
	}
	return nil
}

// SetSessionRevealed flips a session's reveal state (the Reveal contract's
// one server-side switch — every read re-derives scope from this row).
func (s *Store) SetSessionRevealed(ctx context.Context, id SessionID, revealed bool) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if revealed {
		var kind ProfileKind
		err := s.catalogR.QueryRowContext(ctx, `
			SELECT p.kind
			FROM auth_session s
			JOIN profile p ON p.id = s.profile_id
			WHERE s.id = ?`, id).Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("session not found")
		}
		if err != nil {
			return err
		}
		if kind != ProfileAdult {
			return ErrNoRevealPath
		}
	}
	result, err := s.catalogW.ExecContext(ctx,
		`UPDATE auth_session SET revealed = ? WHERE id = ?`,
		boolToInt(revealed), id)
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
	if !revealed {
		_, _ = s.eventsW.ExecContext(ctx, `DELETE FROM stream_token_ledger WHERE session_id = ? AND restricted = 1`, id)
	}
	return nil
}

// SessionRestrictedStreamMediaIDs returns the Media IDs of active restricted
// playback grants held by session id, used to detach in-flight transcodes on
// relock (ADR-0004 §4, ADR-0240 D10).
func (s *Store) SessionRestrictedStreamMediaIDs(ctx context.Context, id SessionID) ([]MediaID, error) {
	if id == "" {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.eventsR.QueryContext(ctx,
		`SELECT DISTINCT media_id FROM stream_token_ledger WHERE session_id = ? AND restricted = 1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []MediaID
	for rows.Next() {
		var mediaID MediaID
		if err := rows.Scan(&mediaID); err != nil {
			return nil, err
		}
		ids = append(ids, mediaID)
	}
	return ids, rows.Err()
}

package store

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

var (
	seedProfileEntryPINOnce     sync.Once
	seedProfileEntryPINVerifier string
	seedProfileEntryPINError    error
)

var (
	// ErrNoProfileEntryPIN means an adult Profile has no independent entry
	// credential, or that entry PINs do not apply to the target Profile kind.
	// Integration sign-in refuses it; a profile switch does not (CORE-2581).
	ErrNoProfileEntryPIN = errors.New("profile has no entry PIN")
	// ErrProfileEntryPINMismatch means the supplied independent entry PIN did
	// not match. Verifier presence is not secret: the owner chose on
	// 2026-11-18 that an adult Profile without an entry PIN opens freely, so
	// the picker reports which Profiles have one (ProfileEntryPINsSet).
	ErrProfileEntryPINMismatch = errors.New("profile entry PIN mismatch")
)

const (
	profileEntryPINConfigPrefix = "profile.entry_pin:"
	developmentProfileEntryPIN  = "2468"

	// Entry PINs deliberately have their own scheme tag and implementation,
	// separate from Reveal verifiers. Equal PIN text never couples the two
	// authorization boundaries.
	profileEntryPINArgon2Prefix  = "argon2id$v=19$m=65536,t=1,p=4$"
	profileEntryPINArgon2Time    = 1
	profileEntryPINArgon2Memory  = 64 * 1024
	profileEntryPINArgon2Threads = 4
	profileEntryPINArgon2KeyLen  = 32
	profileEntryPINSaltLen       = 16
)

func (s *Store) seedProfileEntryPINs(ctx context.Context) error {
	// Test/demo seeds use a public credential and create hundreds of ephemeral
	// stores in the suite. Derive its intentionally expensive verifier once per
	// process; production SetProfileEntryPIN always derives a fresh salted hash.
	seedProfileEntryPINOnce.Do(func() {
		seedProfileEntryPINVerifier, seedProfileEntryPINError = newProfileEntryPINVerifier(developmentProfileEntryPIN)
	})
	if seedProfileEntryPINError != nil {
		return seedProfileEntryPINError
	}
	rows, err := s.catalogW.QueryContext(ctx, `SELECT id FROM profile WHERE kind = ? ORDER BY id`, ProfileAdult)
	if err != nil {
		return err
	}
	var ids []ProfileID
	for rows.Next() {
		var id ProfileID
		if err := rows.Scan(&id); err != nil {
			return errors.Join(err, rows.Close())
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := s.catalogW.ExecContext(ctx, `
			INSERT INTO server_config(key, value, updated_at_ms)
			VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET
				value = excluded.value,
				updated_at_ms = excluded.updated_at_ms
		`, profileEntryPINConfigKey(id), seedProfileEntryPINVerifier, timeToMillis(s.serverNow().UTC())); err != nil {
			return err
		}
	}
	return nil
}

func profileEntryPINConfigKey(profileID ProfileID) string {
	return profileEntryPINConfigPrefix + string(profileID)
}

func newProfileEntryPINVerifier(pin string) (string, error) {
	salt := make([]byte, profileEntryPINSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(pin), salt,
		profileEntryPINArgon2Time,
		profileEntryPINArgon2Memory,
		profileEntryPINArgon2Threads,
		profileEntryPINArgon2KeyLen,
	)
	return profileEntryPINArgon2Prefix + hex.EncodeToString(salt) + "$" + hex.EncodeToString(hash), nil
}

func verifyProfileEntryPINVerifier(verifier, pin string) bool {
	encoded, tagged := strings.CutPrefix(verifier, profileEntryPINArgon2Prefix)
	saltHex, hashHex, paired := strings.Cut(encoded, "$")
	salt, saltErr := hex.DecodeString(saltHex)
	want, hashErr := hex.DecodeString(hashHex)
	if !tagged || !paired || saltErr != nil || hashErr != nil ||
		len(salt) != profileEntryPINSaltLen || len(want) != profileEntryPINArgon2KeyLen {
		CheckCredentialDecoy(pin)
		return false
	}
	got := argon2.IDKey([]byte(pin), salt,
		profileEntryPINArgon2Time,
		profileEntryPINArgon2Memory,
		profileEntryPINArgon2Threads,
		profileEntryPINArgon2KeyLen,
	)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// SetProfileEntryPIN establishes or rotates an adult Profile's independent
// entry credential. It is stored in server_config instead of the Reveal
// columns so neither verifier can become an implicit fallback for the other.
func (s *Store) SetProfileEntryPIN(ctx context.Context, profileID ProfileID, pin string) error {
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	if pin == "" {
		return ValidationError("profile entry PIN is required")
	}
	verifier, err := newProfileEntryPINVerifier(pin)
	if err != nil {
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

	var kind ProfileKind
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM profile WHERE id = ?`, profileID).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ValidationError("profile not found")
		}
		return err
	}
	if kind != ProfileAdult {
		return ErrNoProfileEntryPIN
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO server_config(key, value, updated_at_ms)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at_ms = excluded.updated_at_ms
	`, profileEntryPINConfigKey(profileID), verifier, timeToMillis(s.serverNow().UTC())); err != nil {
		return err
	}
	return tx.Commit()
}

// VerifyProfileEntryPIN checks only the independent Profile-entry credential.
// Reveal hashes are never consulted and there is no legacy fallback.
func (s *Store) VerifyProfileEntryPIN(ctx context.Context, profileID ProfileID, pin string) error {
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}

	var (
		kind     ProfileKind
		disabled int
		verifier sql.NullString
	)
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT p.kind, p.disabled, c.value
		FROM profile p
		LEFT JOIN server_config c ON c.key = ?
		WHERE p.id = ?
	`, profileEntryPINConfigKey(profileID), profileID).Scan(&kind, &disabled, &verifier)
	if errors.Is(err, sql.ErrNoRows) {
		return ValidationError("profile not found")
	}
	if err != nil {
		return err
	}
	if disabled != 0 {
		return ErrProfileDisabled
	}
	if kind == ProfileManaged {
		return ErrNoProfileEntryPIN
	}
	return verifyProfileEntryPIN(kind, verifier, pin)
}

// SetSessionProfileWithEntryPIN verifies the target Profile's independent
// entry credential and rebinds the session in one catalog transaction. An
// adult target with an entry PIN needs it; an adult target without one opens
// without a PIN, as managed targets always do (owner decision 2026-11-18,
// CORE-2581). Every successful switch clears Reveal and Admin Session
// authority.
func (s *Store) SetSessionProfileWithEntryPIN(ctx context.Context, sessionID SessionID, profileID ProfileID, pin string) error {
	if sessionID == "" {
		return ValidationError("session ID is required")
	}
	if profileID == "" {
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

	var sessionExists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM auth_session WHERE id = ?`, sessionID).Scan(&sessionExists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("session not found")
		}
		return err
	}
	var (
		kind     ProfileKind
		disabled int
		verifier sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
		SELECT p.kind, p.disabled, c.value
		FROM profile p
		LEFT JOIN server_config c ON c.key = ?
		WHERE p.id = ?
	`, profileEntryPINConfigKey(profileID), profileID).Scan(&kind, &disabled, &verifier)
	if errors.Is(err, sql.ErrNoRows) {
		return ValidationError("profile not found")
	}
	if err != nil {
		return err
	}
	if disabled != 0 {
		return ErrProfileDisabled
	}
	if kind == ProfileAdult && verifier.Valid && verifier.String != "" {
		if err := verifyProfileEntryPIN(kind, verifier, pin); err != nil {
			return err
		}
	}

	result, err := tx.ExecContext(ctx,
		`UPDATE auth_session SET profile_id = ?, revealed = 0, admin_until_ms = NULL WHERE id = ?`,
		profileID, sessionID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("session not found")
	}
	return tx.Commit()
}

// ProfileEntryPINsSet reports which adult Profiles have an entry PIN, so a
// picker asks for one only where a switch needs it (CORE-2581, CORE-1242).
// Profiles absent from the map have none; managed Profiles never do.
func (s *Store) ProfileEntryPINsSet(ctx context.Context) (map[ProfileID]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT p.id
		FROM profile p
		JOIN server_config c ON c.key = ? || p.id
		WHERE p.kind = ? AND c.value <> ''
	`, profileEntryPINConfigPrefix, ProfileAdult)
	if err != nil {
		return nil, err
	}
	set := map[ProfileID]bool{}
	for rows.Next() {
		var id ProfileID
		if err := rows.Scan(&id); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		set[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(err, rows.Close())
	}
	return set, rows.Close()
}

func verifyProfileEntryPIN(kind ProfileKind, verifier sql.NullString, pin string) error {
	if kind != ProfileAdult || !verifier.Valid || verifier.String == "" {
		return ErrNoProfileEntryPIN
	}
	if !verifyProfileEntryPINVerifier(verifier.String, pin) {
		return ErrProfileEntryPINMismatch
	}
	return nil
}

package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"
)

// CompatKeyPrefix marks a compatibility integration key (ADR-0171 §2.4), so a
// leaked key is greppable and the auth seam routes it without a Session
// lookup. Format: bfj_<64 hex secret>.
const CompatKeyPrefix = "bfj_"

// MaxActiveCompatKeys bounds the unrevoked keys one Profile may hold.
const MaxActiveCompatKeys = 16

// compatKeyLastUseInterval is how stale last_used_at_ms may get before a
// verified request writes it again, so a polling tool costs one write a minute.
const compatKeyLastUseInterval = time.Minute

// ErrCompatKeyInvalid is the single failure for an unknown, malformed or
// revoked key, so callers cannot tell them apart.
var ErrCompatKeyInvalid = errors.New("invalid integration key")

// CompatIntegrationKey is one durable, revocable compatibility credential bound
// to a Profile. Only the SHA-256 digest of its secret is stored; the token is
// returned once, by CreateCompatIntegrationKey.
type CompatIntegrationKey struct {
	ID         string
	ProfileID  ProfileID
	Name       string
	Scopes     []IntegrationScope
	CreatedAt  time.Time
	LastUsedAt time.Time
	RevokedAt  time.Time
}

// Revoked reports whether the key was revoked.
func (k CompatIntegrationKey) Revoked() bool { return !k.RevokedAt.IsZero() }

// CreateCompatIntegrationKey mints a key for an adult, enabled, opted-in
// Profile. A key may hold only the read and refresh scopes: the setup scope
// stays with the bounded admin setup Session. The returned token is the only
// time the secret leaves the store.
func (s *Store) CreateCompatIntegrationKey(ctx context.Context, profileID ProfileID, name string, scopes []IntegrationScope, now time.Time) (CompatIntegrationKey, string, error) {
	name = strings.TrimSpace(name)
	if profileID == "" {
		return CompatIntegrationKey{}, "", ValidationError("profile ID is required")
	}
	if name == "" || len(name) > 128 {
		return CompatIntegrationKey{}, "", ValidationError("key name must be 1 to 128 characters")
	}
	if slices.Contains(scopes, IntegrationScopeSetup) {
		return CompatIntegrationKey{}, "", ValidationError("an integration key cannot hold the setup scope")
	}
	encoded, err := encodeIntegrationScopes(scopes)
	if err != nil {
		return CompatIntegrationKey{}, "", err
	}
	decoded, err := decodeIntegrationScopes(encoded)
	if err != nil {
		return CompatIntegrationKey{}, "", err
	}
	var idBytes [8]byte
	var secretBytes [32]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return CompatIntegrationKey{}, "", err
	}
	if _, err := rand.Read(secretBytes[:]); err != nil {
		return CompatIntegrationKey{}, "", err
	}
	token := CompatKeyPrefix + hex.EncodeToString(secretBytes[:])
	key := CompatIntegrationKey{
		ID: hex.EncodeToString(idBytes[:]), ProfileID: profileID, Name: name,
		Scopes: decoded, CreatedAt: millisToTime(timeToMillis(now)),
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return CompatIntegrationKey{}, "", err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return CompatIntegrationKey{}, "", err
	}
	defer tx.Rollback()
	var kind ProfileKind
	var disabled, optIn int
	err = tx.QueryRowContext(ctx, "SELECT kind, disabled, integration_sign_in FROM profile WHERE id = ?", profileID).
		Scan(&kind, &disabled, &optIn)
	if errors.Is(err, sql.ErrNoRows) {
		return CompatIntegrationKey{}, "", ValidationError("profile not found")
	}
	if err != nil {
		return CompatIntegrationKey{}, "", err
	}
	if kind != ProfileAdult || disabled != 0 {
		return CompatIntegrationKey{}, "", ErrProfileDisabled
	}
	if optIn == 0 {
		return CompatIntegrationKey{}, "", ValidationError("integration sign-in is disabled for this profile")
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM compat_integration_key WHERE profile_id = ? AND revoked_at_ms = 0`, profileID).Scan(&active); err != nil {
		return CompatIntegrationKey{}, "", err
	}
	if active >= MaxActiveCompatKeys {
		return CompatIntegrationKey{}, "", ValidationError("this profile already holds the most integration keys allowed")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO compat_integration_key(id, profile_id, name, digest, scopes, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)`,
		key.ID, profileID, name, hashAPITokenSecret(token), encoded, timeToMillis(now)); err != nil {
		return CompatIntegrationKey{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return CompatIntegrationKey{}, "", err
	}
	return key, token, nil
}

// CompatIntegrationKeys lists every key row, revoked ones included, newest
// first. It carries no secret material.
func (s *Store) CompatIntegrationKeys(ctx context.Context) ([]CompatIntegrationKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, profile_id, name, scopes, created_at_ms, last_used_at_ms, revoked_at_ms
		FROM compat_integration_key ORDER BY created_at_ms DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []CompatIntegrationKey{}
	for rows.Next() {
		key, err := scanCompatIntegrationKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// RevokeCompatIntegrationKey revokes a key at once. The row is kept so the
// Console and the activity log can still name it. Revoking a revoked key keeps
// its first revocation time and succeeds.
func (s *Store) RevokeCompatIntegrationKey(ctx context.Context, id string, now time.Time) (CompatIntegrationKey, error) {
	if strings.TrimSpace(id) == "" {
		return CompatIntegrationKey{}, ValidationError("key ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return CompatIntegrationKey{}, err
	}
	key, err := scanCompatIntegrationKey(s.catalogW.QueryRowContext(ctx, `
		UPDATE compat_integration_key
		SET revoked_at_ms = CASE WHEN revoked_at_ms = 0 THEN ? ELSE revoked_at_ms END
		WHERE id = ?
		RETURNING id, profile_id, name, scopes, created_at_ms, last_used_at_ms, revoked_at_ms`,
		timeToMillis(now), id))
	if errors.Is(err, sql.ErrNoRows) {
		return CompatIntegrationKey{}, ValidationError("key not found")
	}
	return key, err
}

// CompatIntegrationKeyByToken resolves an unrevoked key by the digest of the
// presented token and records its use at most once a minute. Every miss
// returns ErrCompatKeyInvalid. Profile eligibility is the caller's check.
func (s *Store) CompatIntegrationKeyByToken(ctx context.Context, token string, now time.Time) (CompatIntegrationKey, error) {
	secret, ok := strings.CutPrefix(token, CompatKeyPrefix)
	if !ok || len(secret) != 64 || !isLowerHex(secret) {
		return CompatIntegrationKey{}, ErrCompatKeyInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return CompatIntegrationKey{}, err
	}
	key, err := scanCompatIntegrationKey(s.catalogR.QueryRowContext(ctx, `
		SELECT id, profile_id, name, scopes, created_at_ms, last_used_at_ms, revoked_at_ms
		FROM compat_integration_key WHERE digest = ? AND revoked_at_ms = 0`, hashAPITokenSecret(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return CompatIntegrationKey{}, ErrCompatKeyInvalid
	}
	if err != nil {
		return CompatIntegrationKey{}, err
	}
	nowMS := timeToMillis(now)
	if nowMS-timeToMillis(key.LastUsedAt) >= compatKeyLastUseInterval.Milliseconds() {
		// The revoked check rides the write too: a revoke that landed after the
		// read wins and the request fails.
		result, err := s.catalogW.ExecContext(ctx, `
			UPDATE compat_integration_key SET last_used_at_ms = ?
			WHERE id = ? AND revoked_at_ms = 0 AND last_used_at_ms < ?`,
			nowMS, key.ID, nowMS)
		if err != nil {
			return CompatIntegrationKey{}, err
		}
		if changed, err := result.RowsAffected(); err != nil {
			return CompatIntegrationKey{}, err
		} else if changed == 1 {
			key.LastUsedAt = millisToTime(nowMS)
		}
	}
	return key, nil
}

func scanCompatIntegrationKey(scanner interface{ Scan(dest ...any) error }) (CompatIntegrationKey, error) {
	var key CompatIntegrationKey
	var scopes string
	var createdAt, lastUsedAt, revokedAt int64
	if err := scanner.Scan(&key.ID, &key.ProfileID, &key.Name, &scopes, &createdAt, &lastUsedAt, &revokedAt); err != nil {
		return CompatIntegrationKey{}, err
	}
	decoded, err := decodeIntegrationScopes(scopes)
	if err != nil {
		return CompatIntegrationKey{}, err
	}
	key.Scopes = decoded
	key.CreatedAt = millisToTime(createdAt)
	if lastUsedAt > 0 {
		key.LastUsedAt = millisToTime(lastUsedAt)
	}
	if revokedAt > 0 {
		key.RevokedAt = millisToTime(revokedAt)
	}
	return key, nil
}

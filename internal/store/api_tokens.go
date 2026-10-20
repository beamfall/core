package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// APIToken is one long-lived ops credential. The secret never
// appears on this struct — only its hash is at rest, and the full token
// string is returned exactly once, from CreateAPIToken.
type APIToken struct {
	ID         string
	Name       string
	CreatedAt  time.Time
	LastUsedAt time.Time
	Revoked    bool
}

// apiTokenPrefix makes a leaked token greppable by secret scanners. Format:
// bft_<16 hex id>_<64 hex secret>.
const apiTokenPrefix = "bft_"

var errInvalidAPIToken = errors.New("invalid API token")

// CreateAPIToken mints a token: 8 random bytes of identifier, 32 random
// bytes of secret. The returned string is the only time the secret leaves
// the store; callers must never log it.
func (s *Store) CreateAPIToken(ctx context.Context, name string, now time.Time) (APIToken, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return APIToken{}, "", ValidationError("token name is required")
	}
	var idBytes [8]byte
	var secretBytes [32]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return APIToken{}, "", err
	}
	if _, err := rand.Read(secretBytes[:]); err != nil {
		return APIToken{}, "", err
	}
	id := hex.EncodeToString(idBytes[:])
	secret := hex.EncodeToString(secretBytes[:])
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return APIToken{}, "", err
	}
	if _, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO api_token(id, name, secret_hash, created_at_ms)
		VALUES (?, ?, ?, ?)`,
		id, name, hashAPITokenSecret(secret), timeToMillis(now)); err != nil {
		return APIToken{}, "", err
	}
	return APIToken{ID: id, Name: name, CreatedAt: now}, apiTokenPrefix + id + "_" + secret, nil
}

// APITokens lists every token row, newest first — list metadata only, no
// secret material in any form.
func (s *Store) APITokens(ctx context.Context) ([]APIToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, name, created_at_ms, last_used_at_ms, revoked
		FROM api_token ORDER BY created_at_ms DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := []APIToken{}
	for rows.Next() {
		token, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

// RevokeAPIToken revokes immediately: the next VerifyAPIToken fails. Rows
// are never deleted — the id stays resolvable for activity-log attribution.
func (s *Store) RevokeAPIToken(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return ValidationError("token ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `UPDATE api_token SET revoked = 1 WHERE id = ?`, id)
	return jobMutationResult(result, err, ValidationError("token not found"))
}

// VerifyAPIToken resolves a presented token string. Every failure returns
// the same generic error — malformed, unknown, wrong secret, and revoked are
// indistinguishable to the caller (no oracle). A successful verify stamps
// last_used_at.
func (s *Store) VerifyAPIToken(ctx context.Context, token string, now time.Time) (APIToken, error) {
	id, secret, ok := parseAPIToken(token)
	if !ok {
		return APIToken{}, errInvalidAPIToken
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return APIToken{}, err
	}
	var storedHash string
	var revoked int
	row := s.catalogR.QueryRowContext(ctx, `SELECT secret_hash, revoked FROM api_token WHERE id = ?`, id)
	if err := row.Scan(&storedHash, &revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Compare against a dummy anyway so unknown ids cost the same
			// as wrong secrets.
			subtle.ConstantTimeCompare([]byte(hashAPITokenSecret("")), []byte(hashAPITokenSecret(secret)))
			return APIToken{}, errInvalidAPIToken
		}
		return APIToken{}, err
	}
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(hashAPITokenSecret(secret))) != 1 {
		return APIToken{}, errInvalidAPIToken
	}
	if revoked != 0 {
		return APIToken{}, errInvalidAPIToken
	}
	if _, err := s.catalogW.ExecContext(ctx, `UPDATE api_token SET last_used_at_ms = ? WHERE id = ?`, timeToMillis(now), id); err != nil {
		return APIToken{}, err
	}
	token2, err := scanAPIToken(s.catalogR.QueryRowContext(ctx, `
		SELECT id, name, created_at_ms, last_used_at_ms, revoked FROM api_token WHERE id = ?`, id))
	if err != nil {
		return APIToken{}, err
	}
	return token2, nil
}

// parseAPIToken splits bft_<id>_<secret> without allocating surprises:
// fixed prefix, exact hex field widths, nothing else accepted.
func parseAPIToken(token string) (id, secret string, ok bool) {
	rest, found := strings.CutPrefix(token, apiTokenPrefix)
	if !found {
		return "", "", false
	}
	id, secret, found = strings.Cut(rest, "_")
	if !found || len(id) != 16 || len(secret) != 64 {
		return "", "", false
	}
	if !isLowerHex(id) || !isLowerHex(secret) {
		return "", "", false
	}
	return id, secret, true
}

func isLowerHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func hashAPITokenSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func scanAPIToken(scanner interface{ Scan(dest ...any) error }) (APIToken, error) {
	var token APIToken
	var createdAt, lastUsedAt int64
	var revoked int
	if err := scanner.Scan(&token.ID, &token.Name, &createdAt, &lastUsedAt, &revoked); err != nil {
		return APIToken{}, err
	}
	token.CreatedAt = millisToTime(createdAt)
	if lastUsedAt > 0 {
		token.LastUsedAt = millisToTime(lastUsedAt)
	}
	token.Revoked = revoked != 0
	return token, nil
}

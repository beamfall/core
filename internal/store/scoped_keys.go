package store

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
)

// APIScope is one grant in the closed API-scope vocabulary.
// The vocabulary is closed on the server — plugins cannot declare new scopes.
type APIScope string

const (
	ScopeReadCatalog     APIScope = "read-catalog"
	ScopeReadSessions    APIScope = "read-sessions"
	ScopeIngest          APIScope = "ingest"
	ScopeBackup          APIScope = "backup"
	ScopeEventsSubscribe APIScope = "events-subscribe"
	ScopePlaybackControl APIScope = "playback-control"
)

// apiScopeVocabulary is the closed set; membership is validated on every write.
var apiScopeVocabulary = map[APIScope]bool{
	ScopeReadCatalog:     true,
	ScopeReadSessions:    true,
	ScopeIngest:          true,
	ScopeBackup:          true,
	ScopeEventsSubscribe: true,
	ScopePlaybackControl: true,
}

// ScopedKeyPrefix makes a leaked key greppable by secret scanners and keeps it
// distinct from the ADR-0013 admin api_token prefix (bft_). Format:
// bfk_<16 hex id>_<64 hex secret>. Exported so the HTTP auth seam can route a
// Bearer credential to the scoped-key path on prefix alone, before any parse.
const ScopedKeyPrefix = "bfk_"

var errInvalidScopedKey = errors.New("invalid API key")

// ScopedAPIKey is one scoped developer credential. The secret
// never appears on this struct — only its hash is at rest, and the full key
// string is returned exactly once, from CreateScopedAPIKey.
type ScopedAPIKey struct {
	ID         string
	Name       string
	Scopes     []APIScope
	ProfileID  ProfileID // empty = admin-scoped (no Profile binding)
	CreatedAt  time.Time
	LastUsedAt time.Time
	Revoked    bool
}

// CreateScopedAPIKey mints a key with the given name, scope set, and optional
// Profile binding. Scopes must be a non-empty subset of the closed vocabulary;
// an unknown scope is rejected so a row can never carry one. The returned
// string is the only time the secret leaves the store; callers must never log
// it.
func (s *Store) CreateScopedAPIKey(ctx context.Context, name string, scopes []APIScope, profileID ProfileID, now time.Time) (ScopedAPIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ScopedAPIKey{}, "", ValidationError("key name is required")
	}
	normalized, err := normalizeScopes(scopes)
	if err != nil {
		return ScopedAPIKey{}, "", err
	}
	var idBytes [8]byte
	var secretBytes [32]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return ScopedAPIKey{}, "", err
	}
	if _, err := rand.Read(secretBytes[:]); err != nil {
		return ScopedAPIKey{}, "", err
	}
	id := hex.EncodeToString(idBytes[:])
	secret := hex.EncodeToString(secretBytes[:])
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ScopedAPIKey{}, "", err
	}
	var profileArg any
	if profileID != "" {
		profileArg = string(profileID)
	}
	if _, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO scoped_api_key(id, name, secret_hash, scopes, profile_id, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, name, hashAPITokenSecret(secret), joinScopes(normalized), profileArg, timeToMillis(now)); err != nil {
		return ScopedAPIKey{}, "", err
	}
	return ScopedAPIKey{ID: id, Name: name, Scopes: normalized, ProfileID: profileID, CreatedAt: now},
		ScopedKeyPrefix + id + "_" + secret, nil
}

// ScopedAPIKeys lists every key row, newest first — list metadata only
// (scopes + Profile binding visible; no secret material).
func (s *Store) ScopedAPIKeys(ctx context.Context) ([]ScopedAPIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, name, scopes, profile_id, created_at_ms, last_used_at_ms, revoked
		FROM scoped_api_key ORDER BY created_at_ms DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []ScopedAPIKey{}
	for rows.Next() {
		key, err := scanScopedAPIKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// RevokeScopedAPIKey revokes immediately: the next VerifyScopedAPIKey fails.
// Rows are never deleted — the id stays resolvable for activity-log attribution.
func (s *Store) RevokeScopedAPIKey(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return ValidationError("key ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `UPDATE scoped_api_key SET revoked = 1 WHERE id = ?`, id)
	return jobMutationResult(result, err, ValidationError("key not found"))
}

// VerifyScopedAPIKey resolves a presented key string into its scopes and
// Profile binding. Every failure returns the same generic error — malformed,
// unknown, wrong secret, and revoked are indistinguishable (no oracle). A
// successful verify stamps last_used_at.
func (s *Store) VerifyScopedAPIKey(ctx context.Context, key string, now time.Time) (ScopedAPIKey, error) {
	id, secret, ok := parseScopedKey(key)
	if !ok {
		return ScopedAPIKey{}, errInvalidScopedKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ScopedAPIKey{}, err
	}
	var storedHash string
	var revoked int
	row := s.catalogR.QueryRowContext(ctx, `SELECT secret_hash, revoked FROM scoped_api_key WHERE id = ?`, id)
	if err := row.Scan(&storedHash, &revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Compare against a dummy anyway so unknown ids cost the same as
			// wrong secrets.
			subtle.ConstantTimeCompare([]byte(hashAPITokenSecret("")), []byte(hashAPITokenSecret(secret)))
			return ScopedAPIKey{}, errInvalidScopedKey
		}
		return ScopedAPIKey{}, err
	}
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(hashAPITokenSecret(secret))) != 1 {
		return ScopedAPIKey{}, errInvalidScopedKey
	}
	if revoked != 0 {
		return ScopedAPIKey{}, errInvalidScopedKey
	}
	if _, err := s.catalogW.ExecContext(ctx, `UPDATE scoped_api_key SET last_used_at_ms = ? WHERE id = ?`, timeToMillis(now), id); err != nil {
		return ScopedAPIKey{}, err
	}
	key2, err := scanScopedAPIKey(s.catalogR.QueryRowContext(ctx, `
		SELECT id, name, scopes, profile_id, created_at_ms, last_used_at_ms, revoked FROM scoped_api_key WHERE id = ?`, id))
	if err != nil {
		return ScopedAPIKey{}, err
	}
	return key2, nil
}

// normalizeScopes validates against the closed vocabulary and returns the set
// deduplicated and sorted (stable storage). An empty set or an unknown scope
// is rejected.
func normalizeScopes(scopes []APIScope) ([]APIScope, error) {
	seen := map[APIScope]bool{}
	for _, scope := range scopes {
		scope = APIScope(strings.TrimSpace(string(scope)))
		if scope == "" {
			continue
		}
		if !apiScopeVocabulary[scope] {
			return nil, ValidationError("unknown API scope: " + string(scope))
		}
		seen[scope] = true
	}
	if len(seen) == 0 {
		return nil, ValidationError("at least one scope is required")
	}
	out := make([]APIScope, 0, len(seen))
	for scope := range seen {
		out = append(out, scope)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func joinScopes(scopes []APIScope) string {
	parts := make([]string, len(scopes))
	for i, scope := range scopes {
		parts[i] = string(scope)
	}
	return strings.Join(parts, ",")
}

func splitScopes(stored string) []APIScope {
	if stored == "" {
		return nil
	}
	parts := strings.Split(stored, ",")
	scopes := make([]APIScope, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			scopes = append(scopes, APIScope(part))
		}
	}
	return scopes
}

// parseScopedKey splits bfk_<id>_<secret> with exact field widths — the same
// shape as the admin token but a distinct prefix so the two credential
// families never collide.
func parseScopedKey(key string) (id, secret string, ok bool) {
	rest, found := strings.CutPrefix(key, ScopedKeyPrefix)
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

func scanScopedAPIKey(scanner interface{ Scan(dest ...any) error }) (ScopedAPIKey, error) {
	var key ScopedAPIKey
	var scopes string
	var profileID sql.NullString
	var createdAt, lastUsedAt int64
	var revoked int
	if err := scanner.Scan(&key.ID, &key.Name, &scopes, &profileID, &createdAt, &lastUsedAt, &revoked); err != nil {
		return ScopedAPIKey{}, err
	}
	key.Scopes = splitScopes(scopes)
	if profileID.Valid {
		key.ProfileID = ProfileID(profileID.String)
	}
	key.CreatedAt = millisToTime(createdAt)
	if lastUsedAt > 0 {
		key.LastUsedAt = millisToTime(lastUsedAt)
	}
	key.Revoked = revoked != 0
	return key, nil
}

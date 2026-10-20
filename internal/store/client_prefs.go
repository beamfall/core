package store

import (
	"context"
	"strings"
	"time"
)

// ClientPref is one per-Profile client display preference (pre-freeze ask,
// competitor-gap A23): a client-namespaced key with an opaque value the
// server stores but never interprets. By convention values carry no content
// references, so reveal rules do not apply to these reads.
type ClientPref struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

const (
	maxClientPrefKeyLen   = 200
	maxClientPrefValueLen = 4096
)

// SetClientPref upserts one preference for the Profile. Keys must be
// client-namespaced ("web.subtitle-style"); an empty value deletes the
// preference so clients can reset to their own defaults.
func (s *Store) SetClientPref(ctx context.Context, profileID ProfileID, key, value string, now time.Time) error {
	key = strings.TrimSpace(key)
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	if key == "" {
		return ValidationError("preference key is required")
	}
	if len(key) > maxClientPrefKeyLen {
		return ValidationError("preference key is too long")
	}
	namespace, _, found := strings.Cut(key, ".")
	if !found || namespace == "" {
		return ValidationError("preference key must be namespaced (\"<client>.<name>\")")
	}
	if len(value) > maxClientPrefValueLen {
		return ValidationError("preference value is too long")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if exists, err := s.profileExistsSQL(ctx, profileID); err != nil {
		return err
	} else if !exists {
		return ValidationError("unknown profile")
	}
	if value == "" {
		_, err := s.catalogW.ExecContext(ctx, `
			DELETE FROM profile_client_prefs WHERE profile_id = ? AND key = ?
		`, profileID, key)
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO profile_client_prefs(profile_id, key, value, updated_at_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(profile_id, key) DO UPDATE SET
			value = excluded.value,
			updated_at_ms = excluded.updated_at_ms
	`, profileID, key, value, now.UTC().UnixMilli())
	return err
}

// ClientPrefs returns every preference stored for the Profile, ordered by key.
func (s *Store) ClientPrefs(ctx context.Context, profileID ProfileID) ([]ClientPref, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT key, value, updated_at_ms
		FROM profile_client_prefs
		WHERE profile_id = ?
		ORDER BY key
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	prefs := []ClientPref{}
	for rows.Next() {
		var pref ClientPref
		var updatedMS int64
		if err := rows.Scan(&pref.Key, &pref.Value, &updatedMS); err != nil {
			return nil, err
		}
		pref.UpdatedAt = time.UnixMilli(updatedMS).UTC()
		prefs = append(prefs, pref)
	}
	return prefs, rows.Err()
}

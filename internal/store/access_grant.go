package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// Per-title access grant. An AccessGrant row is created
// atomically when an adult resolves an AccessRequest with verdict="granted"
// (ResolveAccessRequest). A row's presence is the grant predicate: no status
// column is needed. An adult can revoke a grant (RevokeAccessGrant), which
// deletes the row; the next "Ask a parent" tap starts a new AccessRequest.
//
// The unique index on (profile_id, media_id) allows at most one active grant
// per pair. A re-grant (e.g. after a revoke+new-request cycle) refreshes the
// existing row via ON CONFLICT DO UPDATE.
//
// Schema: 0086_access_grant.sql.

// AccessGrant is one active per-title grant. ProfileID is the managed Profile
// who may now access MediaID. RequestID traces the originating AccessRequest;
// ActorProfileID is the adult who granted it. CreatedAt is wall-clock grant
// time; UpdatedAt is set on refresh.
type AccessGrant struct {
	ID             string
	ProfileID      ProfileID
	MediaID        MediaID
	RequestID      string
	ActorProfileID ProfileID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ErrAccessGrantNotFound is returned when no grant exists for the given pair.
var ErrAccessGrantNotFound = errors.New("access grant not found")

func randomAccessGrantID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "agrant-" + hex.EncodeToString(raw[:]), nil
}

// upsertAccessGrantTx inserts or refreshes an access_grant row within the
// caller's transaction. It is called from ResolveAccessRequest to make the
// access_request update and the grant upsert atomic.
func upsertAccessGrantTx(tx *sql.Tx, id string, profileID ProfileID, mediaID MediaID, requestID string, actorProfileID ProfileID, now time.Time) error {
	nowMS := timeToMillis(now)
	_, err := tx.Exec(`
		INSERT INTO access_grant
			(id, profile_id, media_id, request_id, actor_profile_id, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, media_id) DO UPDATE SET
			request_id       = excluded.request_id,
			actor_profile_id = excluded.actor_profile_id,
			updated_at_ms    = excluded.updated_at_ms
	`, id, string(profileID), string(mediaID), requestID, string(actorProfileID), nowMS, nowMS)
	return err
}

// RevokeAccessGrant removes the active grant for a (profileID, mediaID) pair.
// Revoking a non-existent grant is a no-op (idempotent). Returns the caller's
// profile ID for activity-log use.
func (s *Store) RevokeAccessGrant(ctx context.Context, profileID ProfileID, mediaID MediaID) error {
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	if mediaID == "" {
		return ValidationError("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx,
		`DELETE FROM access_grant WHERE profile_id = ? AND media_id = ?`,
		string(profileID), string(mediaID))
	return err
}

// GrantedMediaIDsForProfile returns the media IDs the managed Profile has
// active grants for. This list is stamped into LibraryFilter.GrantedMediaIDs
// at scope resolution time so the enforcement layer can bypass cert constraints
// for these items. Empty slice (not nil) on no grants.
func (s *Store) GrantedMediaIDsForProfile(ctx context.Context, profileID ProfileID) ([]MediaID, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx,
		`SELECT media_id FROM access_grant WHERE profile_id = ? ORDER BY updated_at_ms DESC`,
		string(profileID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaID{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, MediaID(id))
	}
	return out, rows.Err()
}

// AccessGrantsForProfile returns full AccessGrant objects for a Profile. Used
// by the Console UI to render the exception list. Empty slice (not nil) when
// no grants exist.
func (s *Store) AccessGrantsForProfile(ctx context.Context, profileID ProfileID) ([]AccessGrant, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, profile_id, media_id, request_id, actor_profile_id, created_at_ms, updated_at_ms
		FROM access_grant WHERE profile_id = ? ORDER BY updated_at_ms DESC
	`, string(profileID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccessGrant{}
	for rows.Next() {
		g, err := scanAccessGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func scanAccessGrant(rows *sql.Rows) (AccessGrant, error) {
	var (
		g                         AccessGrant
		profID, mID, reqID, actor string
		createdMS, updatedMS      int64
	)
	if err := rows.Scan(&g.ID, &profID, &mID, &reqID, &actor, &createdMS, &updatedMS); err != nil {
		return AccessGrant{}, err
	}
	g.ProfileID = ProfileID(profID)
	g.MediaID = MediaID(mID)
	g.RequestID = reqID
	g.ActorProfileID = ProfileID(actor)
	g.CreatedAt = millisToTime(createdMS)
	g.UpdatedAt = millisToTime(updatedMS)
	return g, nil
}

package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// Per-title access request. When a managed Profile views a
// Locked item and taps "Ask a parent", a pending AccessRequest is created for
// the (profile, media) pair. The request fans out to every adult Profile's
// devices via the PUSH-1 transport (type-only payload — invariant #5); the
// first adult to respond wins (same first-responder-wins contract as the
// extension request, ADR-0039 §2.3); an unanswered request auto-expires after
// a configurable TTL (default 7 days). Schema: 0085_access_request.sql.
//
// DEDUP: the unique partial index on (profile_id, media_id) WHERE status =
// 'pending' ensures at most one pending request per pair. A second tap while a
// request is pending returns the existing row (idempotent). Once resolved, a
// new pending request for the same pair is permitted.
//
// SUPERVISOR MODEL (same as ADR-0039 §2.3): no stored supervisor link. Any
// adult Profile may respond; a managed Profile may not. responder_profile_id
// records which adult resolved it.

// AccessStatus is the closed two-value lifecycle vocabulary of an
// AccessRequest. A request is born pending and becomes resolved on the first
// adult response or TTL expiry. The resolved sub-state (granted/denied/suppressed)
// rides Verdict.
type AccessStatus string

const (
	AccessPending  AccessStatus = "pending"
	AccessResolved AccessStatus = "resolved"
)

// AccessVerdict is the closed three-value outcome vocabulary once resolved.
// Granted means an adult approved (an AccessGrant follows — ACCESS-3 scope);
// Denied means the adult refused; Suppressed means the TTL expired before any
// response.
type AccessVerdict string

const (
	AccessVerdictGranted    AccessVerdict = "granted"
	AccessVerdictDenied     AccessVerdict = "denied"
	AccessVerdictSuppressed AccessVerdict = "suppressed"
	// AccessVerdictNone is the zero value for a still-pending request (no verdict yet).
	AccessVerdictNone AccessVerdict = ""
)

// DefaultAccessRequestTTL is the ADR-0113 §2.2 "configurable TTL" default:
// 7 days. The server operator may override this; the store uses the value
// provided at CreateAccessRequest time (no global config coupling).
const DefaultAccessRequestTTL = 7 * 24 * time.Hour

// AccessRequest is one per-title access request through its lifecycle.
// ProfileID is the managed requester; MediaID is the Locked item. Status is
// pending until resolved by an adult or TTL expiry. Verdict is the sub-state
// once resolved (empty while pending). Responder is the adult who resolved it
// (empty while pending or suppressed). TTLExpiresAt is when an unanswered
// pending request becomes suppressed.
type AccessRequest struct {
	ID           string
	ProfileID    ProfileID
	MediaID      MediaID
	Status       AccessStatus
	Verdict      AccessVerdict
	CreatedAt    time.Time
	TTLExpiresAt time.Time
	Responder    ProfileID
}

// AccessRequestInput is a CreateAccessRequest argument. ProfileID and MediaID
// are required. TTL defaults to DefaultAccessRequestTTL when zero. Now
// defaults to time.Now when zero.
type AccessRequestInput struct {
	ProfileID ProfileID
	MediaID   MediaID
	TTL       time.Duration
	Now       time.Time
}

// ErrAccessRequestAlreadyResolved is returned by ResolveAccessRequest when the
// request is no longer pending (a prior responder won, or it was suppressed by
// TTL). The first-responder-wins sentinel (same shape as ErrExtensionAlreadyResolved).
var ErrAccessRequestAlreadyResolved = errors.New("access request already resolved")

// ErrAccessRequestNotFound is returned when an access request id is unknown.
var ErrAccessRequestNotFound = errors.New("access request not found")

func randomAccessRequestID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "acreq-" + hex.EncodeToString(raw[:]), nil
}

// CreateAccessRequest records a new pending access request and returns it. If
// a pending request already exists for the (profile, media) pair, the existing
// row is returned (idempotent dedup — the unique partial index surfaces the
// conflict; the caller treats both cases as "request is pending"). TTL defaults
// to DefaultAccessRequestTTL when zero.
func (s *Store) CreateAccessRequest(ctx context.Context, in AccessRequestInput) (AccessRequest, error) {
	if in.ProfileID == "" {
		return AccessRequest{}, ValidationError("profile ID is required")
	}
	if in.MediaID == "" {
		return AccessRequest{}, ValidationError("media ID is required")
	}
	ttl := in.TTL
	if ttl <= 0 {
		ttl = DefaultAccessRequestTTL
	}
	now := normalizeTime(in.Now)
	if in.Now.IsZero() {
		now = time.Now().UTC()
	}
	id, err := randomAccessRequestID()
	if err != nil {
		return AccessRequest{}, err
	}
	expires := now.Add(ttl)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return AccessRequest{}, err
	}
	_, err = s.catalogW.ExecContext(ctx, `
		INSERT INTO access_request
			(id, profile_id, media_id, status, verdict, created_at_ms,
			 ttl_expires_at_ms, responder_profile_id)
		VALUES (?, ?, ?, ?, '', ?, ?, '')
		ON CONFLICT DO NOTHING
	`, id, string(in.ProfileID), string(in.MediaID), string(AccessPending),
		timeToMillis(now), timeToMillis(expires))
	if err != nil {
		return AccessRequest{}, err
	}
	// If the INSERT was a no-op (conflict), return the existing pending row.
	return s.accessRequestByProfileMediaLocked(ctx, in.ProfileID, in.MediaID)
}

func (s *Store) accessRequestByProfileMediaLocked(ctx context.Context, profileID ProfileID, mediaID MediaID) (AccessRequest, error) {
	var (
		req                  AccessRequest
		profID, mID, status  string
		verdict, responder   string
		createdMS, expiresMS int64
	)
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT id, profile_id, media_id, status, verdict,
		       created_at_ms, ttl_expires_at_ms, responder_profile_id
		FROM access_request
		WHERE profile_id = ? AND media_id = ? AND status = ?
	`, string(profileID), string(mediaID), string(AccessPending)).Scan(
		&req.ID, &profID, &mID, &status, &verdict, &createdMS, &expiresMS, &responder)
	if errors.Is(err, sql.ErrNoRows) {
		return AccessRequest{}, ErrAccessRequestNotFound
	}
	if err != nil {
		return AccessRequest{}, err
	}
	req.ProfileID = ProfileID(profID)
	req.MediaID = MediaID(mID)
	req.Status = AccessStatus(status)
	req.Verdict = AccessVerdict(verdict)
	req.CreatedAt = millisToTime(createdMS)
	req.TTLExpiresAt = millisToTime(expiresMS)
	req.Responder = ProfileID(responder)
	return req, nil
}

// AccessRequestByID reads one request by id. A pending request whose TTL has
// passed is reported with Status=resolved, Verdict=suppressed WITHOUT a write —
// lazy suppression mirrors the extension_request lazy-expiry pattern. The store
// row stays pending until a resolve attempt or GC run updates it.
func (s *Store) AccessRequestByID(ctx context.Context, id string, now time.Time) (AccessRequest, error) {
	if id == "" {
		return AccessRequest{}, ValidationError("access request ID is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return AccessRequest{}, err
	}
	req, err := s.accessRequestByIDLocked(ctx, id)
	if err != nil {
		return AccessRequest{}, err
	}
	if req.Status == AccessPending && !now.Before(req.TTLExpiresAt) {
		req.Status = AccessResolved
		req.Verdict = AccessVerdictSuppressed
	}
	return req, nil
}

func (s *Store) accessRequestByIDLocked(ctx context.Context, id string) (AccessRequest, error) {
	var (
		req                  AccessRequest
		profID, mID, status  string
		verdict, responder   string
		createdMS, expiresMS int64
	)
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT id, profile_id, media_id, status, verdict,
		       created_at_ms, ttl_expires_at_ms, responder_profile_id
		FROM access_request WHERE id = ?
	`, id).Scan(&req.ID, &profID, &mID, &status, &verdict, &createdMS, &expiresMS, &responder)
	if errors.Is(err, sql.ErrNoRows) {
		return AccessRequest{}, ErrAccessRequestNotFound
	}
	if err != nil {
		return AccessRequest{}, err
	}
	req.ProfileID = ProfileID(profID)
	req.MediaID = MediaID(mID)
	req.Status = AccessStatus(status)
	req.Verdict = AccessVerdict(verdict)
	req.CreatedAt = millisToTime(createdMS)
	req.TTLExpiresAt = millisToTime(expiresMS)
	req.Responder = ProfileID(responder)
	return req, nil
}

// ResolveAccessRequest resolves a pending request with the given verdict,
// first-responder-wins. The conditional UPDATE asserts
// status='pending' so two concurrent responders cannot both win. responder is
// the adult Profile id (the caller has already verified kind). verdict must be
// granted or denied; suppressed is reserved for TTL GC. When verdict=granted,
// an access_grant row is upserted atomically in the same transaction.
// Returns ErrAccessRequestAlreadyResolved when the row is no
// longer pending.
func (s *Store) ResolveAccessRequest(ctx context.Context, id string, responder ProfileID, verdict AccessVerdict, now time.Time) (AccessRequest, error) {
	if id == "" {
		return AccessRequest{}, ValidationError("access request ID is required")
	}
	if responder == "" {
		return AccessRequest{}, ValidationError("responder profile ID is required")
	}
	if verdict != AccessVerdictGranted && verdict != AccessVerdictDenied && verdict != AccessVerdictSuppressed {
		return AccessRequest{}, ValidationError("verdict must be granted, denied, or suppressed")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return AccessRequest{}, err
	}
	req, err := s.accessRequestByIDLocked(ctx, id)
	if err != nil {
		return AccessRequest{}, err
	}
	if req.Status != AccessPending || !now.Before(req.TTLExpiresAt) {
		return AccessRequest{}, ErrAccessRequestAlreadyResolved
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return AccessRequest{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE access_request
		SET status = ?, verdict = ?, responder_profile_id = ?
		WHERE id = ? AND status = ? AND ttl_expires_at_ms > ?
	`, string(AccessResolved), string(verdict), string(responder),
		id, string(AccessPending), timeToMillis(now))
	if err != nil {
		return AccessRequest{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return AccessRequest{}, err
	}
	if affected == 0 {
		return AccessRequest{}, ErrAccessRequestAlreadyResolved
	}
	if verdict == AccessVerdictGranted {
		grantID, err := randomAccessGrantID()
		if err != nil {
			return AccessRequest{}, err
		}
		if err := upsertAccessGrantTx(tx, grantID, req.ProfileID, req.MediaID, id, responder, now); err != nil {
			return AccessRequest{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AccessRequest{}, err
	}
	req.Status = AccessResolved
	req.Verdict = verdict
	req.Responder = responder
	return req, nil
}

// PendingAccessRequestsForProfile returns the pending access requests a Profile
// can see. For an adult caller (any adult = supervisor), this is ALL pending
// requests across managed Profiles (the adult inbox). For a managed caller,
// this is the caller's own pending requests only. Lazy suppression is applied:
// rows whose TTL has passed are reported as resolved/suppressed without a write.
func (s *Store) PendingAccessRequestsForProfile(ctx context.Context, profileID ProfileID, isAdult bool, now time.Time) ([]AccessRequest, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	var rows *sql.Rows
	var err error
	if isAdult {
		rows, err = s.catalogR.QueryContext(ctx, `
			SELECT id, profile_id, media_id, status, verdict,
			       created_at_ms, ttl_expires_at_ms, responder_profile_id
			FROM access_request
			WHERE status = ?
			ORDER BY created_at_ms DESC
		`, string(AccessPending))
	} else {
		rows, err = s.catalogR.QueryContext(ctx, `
			SELECT id, profile_id, media_id, status, verdict,
			       created_at_ms, ttl_expires_at_ms, responder_profile_id
			FROM access_request
			WHERE profile_id = ? AND status = ?
			ORDER BY created_at_ms DESC
		`, string(profileID), string(AccessPending))
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccessRequest{}
	for rows.Next() {
		var (
			req                  AccessRequest
			profID, mID, status  string
			verdict, responder   string
			createdMS, expiresMS int64
		)
		if err := rows.Scan(&req.ID, &profID, &mID, &status, &verdict, &createdMS, &expiresMS, &responder); err != nil {
			return nil, err
		}
		req.ProfileID = ProfileID(profID)
		req.MediaID = MediaID(mID)
		req.Status = AccessStatus(status)
		req.Verdict = AccessVerdict(verdict)
		req.CreatedAt = millisToTime(createdMS)
		req.TTLExpiresAt = millisToTime(expiresMS)
		req.Responder = ProfileID(responder)
		// Lazy suppression: report TTL-expired pending rows as suppressed.
		if req.Status == AccessPending && !now.Before(req.TTLExpiresAt) {
			req.Status = AccessResolved
			req.Verdict = AccessVerdictSuppressed
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

// GCExpiredAccessRequests marks pending access requests whose TTL has passed as
// resolved/suppressed. This is the write-side complement to the lazy-suppression
// reads. It is safe to run periodically; affected rows are those still pending
// whose ttl_expires_at_ms < now.
func (s *Store) GCExpiredAccessRequests(ctx context.Context, now time.Time) (int64, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	res, err := s.catalogW.ExecContext(ctx, `
		UPDATE access_request
		SET status = ?, verdict = ?
		WHERE status = ? AND ttl_expires_at_ms < ?
	`, string(AccessResolved), string(AccessVerdictSuppressed),
		string(AccessPending), timeToMillis(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

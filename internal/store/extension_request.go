package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

// Parental extension request (ADR-0039 §2.1–2.4 / PUSH-2). When a managed
// Profile is blocked mid-playback, it raises a pending ExtensionRequest; an adult
// Profile approves or denies it; the first responder wins; an unanswered
// request auto-expires after 5 minutes; an approval lifts the managed Profile's
// block for the granted window, scoped to the requesting Session only. The
// schema lives in 0062_extension_request.sql.
//
// SUPERVISOR MODEL (ADR-0039 §2.3 + rejected-alternatives): there is no stored
// supervisor→managed link. The request fans out to "every adult Profile on the
// server" and "the first available parent answers, not a designated primary".
// So the store never records a supervisor; authorization (any adult may
// respond, a managed Profile may not) is the caller's gate — the store only
// records WHICH adult resolved a request (responder_profile_id).

// ExtensionStatus is the closed lifecycle vocabulary of an ExtensionRequest. A
// request is born pending, becomes approved or denied on the first response, or
// expired once its 5-minute window passes unanswered. Core never branches on an
// unknown value; the four states are exhaustive.
type ExtensionStatus string

const (
	// ExtensionPending is the initial state: awaiting an adult response, still
	// inside its 5-minute window.
	ExtensionPending ExtensionStatus = "pending"
	// ExtensionApproved means an adult granted the extension; grant_expires_at_ms
	// marks when the lifted block closes.
	ExtensionApproved ExtensionStatus = "approved"
	// ExtensionDenied means an adult refused the extension; the managed Profile
	// stays blocked (ADR-0039 §2.3 step 5).
	ExtensionDenied ExtensionStatus = "denied"
	// ExtensionExpired means no adult responded within 5 minutes; the request can
	// no longer be approved (ADR-0039 §2.3 step 5).
	ExtensionExpired ExtensionStatus = "expired"
)

// ExtensionRequestTTL is the §2.3-step-5 window: a pending request unanswered
// for this long auto-expires and can no longer be approved. ADR-0039 pins five
// minutes ("configurable per-server"); the value is chosen here, not derived.
const ExtensionRequestTTL = 5 * time.Minute

// ExtensionRequest is one parental extension request through its lifecycle.
// ProfileID is the managed Profile; RequestingSessionID is the §2.2 "current
// Session" the grant is scoped to. TriggerSource is the §2.1 opaque label
// ("schedule" / "cap" / "budget") for the notification text — Core never
// interprets it (invariant #1). RemainingMinutes is the finish-item default
// captured at request time; GrantMinutes is the resolved length once approved.
// GrantExpiresAt is when an approved grant's lifted block closes. Responder is
// the adult Profile that resolved it (empty while pending/expired).
type ExtensionRequest struct {
	ID                  string
	ProfileID           ProfileID
	RequestingSessionID SessionID
	TriggerSource       string
	Status              ExtensionStatus
	RemainingMinutes    int
	GrantMinutes        int
	CreatedAt           time.Time
	ExpiresAt           time.Time
	GrantExpiresAt      time.Time
	Responder           ProfileID
}

func (r ExtensionRequest) ApprovalNonce() string {
	sum := sha256.Sum256([]byte(r.ID + "|" + string(r.ProfileID) + "|" + string(r.RequestingSessionID) + "|" + strconv.FormatInt(r.CreatedAt.UnixMilli(), 10)))
	return hex.EncodeToString(sum[:16])
}

func (r ExtensionRequest) ApprovalToken() string {
	return r.ID + ":" + r.ApprovalNonce()
}

// ExtensionRequestInput is a CreateExtensionRequest argument. ProfileID and
// RequestingSessionID are required; the managed-kind check is the caller's gate
// (the store does not read the Profile here). TriggerSource and RemainingMinutes
// are the §2.1/§2.2 request context. Now defaults to time.Now when zero.
type ExtensionRequestInput struct {
	ProfileID           ProfileID
	RequestingSessionID SessionID
	TriggerSource       string
	RemainingMinutes    int
	Now                 time.Time
}

// ErrExtensionAlreadyResolved is returned by RespondExtensionRequest when the
// request is no longer pending (a prior responder won, or it expired). It is a
// stable sentinel so the graph layer can surface "already resolved" distinctly
// from a not-found or store error — the first-responder-wins contract (ADR-0039
// §2.3 step 4 / rejected-alternatives "subsequent approvals are silently
// ignored", surfaced here as an explicit signal so the second responder learns
// it lost rather than seeing a false success).
var ErrExtensionAlreadyResolved = errors.New("extension request already resolved")

// ErrExtensionNotFound is returned when an extension request id is unknown.
var ErrExtensionNotFound = errors.New("extension request not found")

func randomExtensionRequestID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "extreq-" + hex.EncodeToString(raw[:]), nil
}

// CreateExtensionRequest records a new pending request and returns it. The
// 5-minute expiry (ExtensionRequestTTL) is stamped from Now. The caller has
// already verified the Profile is managed and blocked; the store records the
// request verbatim. RemainingMinutes is clamped to non-negative.
func (s *Store) CreateExtensionRequest(ctx context.Context, in ExtensionRequestInput) (ExtensionRequest, error) {
	if in.ProfileID == "" {
		return ExtensionRequest{}, ValidationError("profile ID is required")
	}
	if in.RequestingSessionID == "" {
		return ExtensionRequest{}, ValidationError("requesting session ID is required")
	}
	now := normalizeTime(in.Now)
	if in.Now.IsZero() {
		now = time.Now().UTC()
	}
	if in.RemainingMinutes < 0 {
		in.RemainingMinutes = 0
	}
	id, err := randomExtensionRequestID()
	if err != nil {
		return ExtensionRequest{}, err
	}
	expires := now.Add(ExtensionRequestTTL)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ExtensionRequest{}, err
	}
	_, err = s.catalogW.ExecContext(ctx, `
		INSERT INTO extension_request
			(id, profile_id, requesting_session_id, trigger_source, status,
			 remaining_minutes, grant_minutes, created_at_ms, expires_at_ms,
			 grant_expires_at_ms, responder_profile_id)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, 0, '')
	`, id, string(in.ProfileID), string(in.RequestingSessionID), in.TriggerSource,
		string(ExtensionPending), in.RemainingMinutes, timeToMillis(now), timeToMillis(expires))
	if err != nil {
		return ExtensionRequest{}, err
	}
	return ExtensionRequest{
		ID:                  id,
		ProfileID:           in.ProfileID,
		RequestingSessionID: in.RequestingSessionID,
		TriggerSource:       in.TriggerSource,
		Status:              ExtensionPending,
		RemainingMinutes:    in.RemainingMinutes,
		CreatedAt:           now,
		ExpiresAt:           expires,
	}, nil
}

// ExtensionRequestByID reads one request by id. A pending request whose expiry
// has passed is reported with Status=expired WITHOUT a write — the lazy expiry
// the respond path also enforces — so a reader never sees a "pending" request
// that can no longer be approved. The persisted row stays pending until a
// respond attempt or a sweep flips it; this read-time view is purely a
// presentation of the same rule auth.Resolve and RespondExtensionRequest apply.
func (s *Store) ExtensionRequestByID(ctx context.Context, id string, now time.Time) (ExtensionRequest, error) {
	if id == "" {
		return ExtensionRequest{}, ValidationError("extension request ID is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ExtensionRequest{}, err
	}
	req, err := s.extensionRequestLocked(ctx, id)
	if err != nil {
		return ExtensionRequest{}, err
	}
	if req.Status == ExtensionPending && !now.Before(req.ExpiresAt) {
		req.Status = ExtensionExpired
	}
	return req, nil
}

func (s *Store) extensionRequestLocked(ctx context.Context, id string) (ExtensionRequest, error) {
	var (
		req                            ExtensionRequest
		profileID, sessionID, status   string
		responder                      string
		createdMS, expiresMS, grantExp int64
	)
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT id, profile_id, requesting_session_id, trigger_source, status,
		       remaining_minutes, grant_minutes, created_at_ms, expires_at_ms,
		       grant_expires_at_ms, responder_profile_id
		FROM extension_request WHERE id = ?
	`, id).Scan(&req.ID, &profileID, &sessionID, &req.TriggerSource, &status,
		&req.RemainingMinutes, &req.GrantMinutes, &createdMS, &expiresMS, &grantExp, &responder)
	if errors.Is(err, sql.ErrNoRows) {
		return ExtensionRequest{}, ErrExtensionNotFound
	}
	if err != nil {
		return ExtensionRequest{}, err
	}
	req.ProfileID = ProfileID(profileID)
	req.RequestingSessionID = SessionID(sessionID)
	req.Status = ExtensionStatus(status)
	req.CreatedAt = millisToTime(createdMS)
	req.ExpiresAt = millisToTime(expiresMS)
	if grantExp != 0 {
		req.GrantExpiresAt = millisToTime(grantExp)
	}
	req.Responder = ProfileID(responder)
	return req, nil
}

// RespondExtensionRequest approves or denies a pending request, first-responder
// wins (ADR-0039 §2.3 step 4). It is a conditional write: the row is updated only
// while it is still pending AND not past its 5-minute expiry, so a second
// responder — or a responder racing the expiry — gets ErrExtensionAlreadyResolved
// rather than overwriting the first decision. responder is the adult Profile id
// (the caller has already enforced that it IS an adult and not the managed
// requester — the store records it, it does not authorize). On approve,
// grantMinutes is the resolved length and grant_expires_at_ms = now + grantMinutes,
// the window auth.Resolve lifts the block for; on deny grantMinutes is ignored.
// status must be approved or denied. Returns the resolved request.
func (s *Store) RespondExtensionRequest(ctx context.Context, id string, approvalNonce string, responder ProfileID, status ExtensionStatus, grantMinutes int, now time.Time) (ExtensionRequest, error) {
	if id == "" {
		return ExtensionRequest{}, ValidationError("extension request ID is required")
	}
	if responder == "" {
		return ExtensionRequest{}, ValidationError("responder profile ID is required")
	}
	if status != ExtensionApproved && status != ExtensionDenied {
		return ExtensionRequest{}, ValidationError("extension response must be approved or denied")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if grantMinutes < 0 {
		grantMinutes = 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ExtensionRequest{}, err
	}
	req, err := s.extensionRequestLocked(ctx, id)
	if err != nil {
		return ExtensionRequest{}, err
	}
	// Already resolved (a prior responder won) or expired — first-responder-wins
	// and the 5-minute window both collapse to "already resolved" so the loser
	// learns it lost rather than seeing a false success.
	if req.Status != ExtensionPending || !now.Before(req.ExpiresAt) {
		return ExtensionRequest{}, ErrExtensionAlreadyResolved
	}
	if responder != req.ProfileID && approvalNonce != req.ApprovalNonce() {
		return ExtensionRequest{}, ErrExtensionNotFound
	}
	var grantExp int64
	if status == ExtensionApproved {
		grantExp = timeToMillis(now.Add(time.Duration(grantMinutes) * time.Minute))
	} else {
		grantMinutes = 0
	}
	// Conditional update: the WHERE re-asserts pending AND unexpired inside the
	// write so two concurrent responders cannot both win (the second affects zero
	// rows and is reported already-resolved).
	res, err := s.catalogW.ExecContext(ctx, `
		UPDATE extension_request
		SET status = ?, grant_minutes = ?, grant_expires_at_ms = ?, responder_profile_id = ?
		WHERE id = ? AND status = ? AND expires_at_ms > ?
	`, string(status), grantMinutes, grantExp, string(responder), id,
		string(ExtensionPending), timeToMillis(now))
	if err != nil {
		return ExtensionRequest{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return ExtensionRequest{}, err
	}
	if affected == 0 {
		return ExtensionRequest{}, ErrExtensionAlreadyResolved
	}
	req.Status = status
	req.GrantMinutes = grantMinutes
	req.Responder = responder
	if status == ExtensionApproved {
		req.GrantExpiresAt = millisToTime(grantExp)
	}
	return req, nil
}

// ActiveExtensionGrant reports whether the managed Profile has an APPROVED,
// unexpired extension grant for the given Session at instant now — the block
// hook (auth.Resolve) consults it before refusing a managed Profile outside its
// schedule. The grant is Session-scoped: an approval for one
// Session never lifts another Session's block. A grant whose grant_expires_at_ms
// has passed no longer lifts the block (normal enforcement resumes), so this is
// the live, time-bounded check, not a standing flag.
func (s *Store) ActiveExtensionGrant(ctx context.Context, profileID ProfileID, sessionID SessionID, now time.Time) (bool, error) {
	if profileID == "" || sessionID == "" {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var count int
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM extension_request
		WHERE profile_id = ? AND requesting_session_id = ? AND status = ?
		  AND grant_expires_at_ms > ?
	`, string(profileID), string(sessionID), string(ExtensionApproved), timeToMillis(now)).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// SessionHasApprovedExtension reports whether the Session already has ANY
// approved extension request (regardless of whether its grant window is still
// open). It is the once-per-session guard the §2.4 pre-authorization auto-approve
// uses: pre-auth auto-approves only the FIRST request in a Session, so a second
// request still requires a real adult response.
func (s *Store) SessionHasApprovedExtension(ctx context.Context, profileID ProfileID, sessionID SessionID) (bool, error) {
	if profileID == "" || sessionID == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var count int
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM extension_request
		WHERE profile_id = ? AND requesting_session_id = ? AND status = ?
	`, string(profileID), string(sessionID), string(ExtensionApproved)).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// SetExtensionPreauthorization toggles the §2.4 per-managed-Profile
// pre-authorization ("auto-approve one extension per session"). It upserts one
// row per Profile; the caller has already verified the Profile is managed (an
// adult Profile has no schedule to extend). Disabling leaves the row present
// with enabled=0 rather than deleting it, mirroring the toggle's idempotency.
func (s *Store) SetExtensionPreauthorization(ctx context.Context, profileID ProfileID, enabled bool, now time.Time) error {
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	flag := 0
	if enabled {
		flag = 1
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO extension_preauthorization (profile_id, enabled, updated_at_ms)
		VALUES (?, ?, ?)
		ON CONFLICT(profile_id) DO UPDATE SET
			enabled       = excluded.enabled,
			updated_at_ms = excluded.updated_at_ms
	`, string(profileID), flag, timeToMillis(now))
	return err
}

// ExtensionPreauthorizationEnabled reports whether the managed Profile has
// pre-authorization enabled. A Profile with no row defaults to
// false — pre-authorization is off unless an adult explicitly enabled it.
func (s *Store) ExtensionPreauthorizationEnabled(ctx context.Context, profileID ProfileID) (bool, error) {
	if profileID == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var flag int
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT enabled FROM extension_preauthorization WHERE profile_id = ?
	`, string(profileID)).Scan(&flag)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return flag == 1, nil
}

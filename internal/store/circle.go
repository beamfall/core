package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

var (
	ErrCircleNotFound          = errors.New("circle not found")
	ErrCircleRevoked           = errors.New("circle revoked")
	ErrShadowPrincipalNotFound = errors.New("circle shadow principal not found")
	ErrRemoteProfileReassigned = errors.New("remote profile id reassigned")
	ErrCircleSessionNotFound   = errors.New("circle session not found")
	ErrCircleNoticeNotFound    = errors.New("circle revoke notice not found")
	ErrCircleOwnerRequired     = errors.New("circle owner authorization required")
)

type CircleMembership struct {
	ID                   string
	CredentialGeneration int64
	RevokedAt            *time.Time
}

type ShadowPrincipal struct {
	ID                   string
	CircleID             string
	RemoteProfileID      string
	RemoteProfileBinding string
	PolicyProfileID      ProfileID
	Revealed             bool
	CreatedByProfileID   ProfileID
}

type CircleSessionCache struct {
	CircleID           string
	SessionID          string
	ShadowPrincipalID  string
	NegotiatedRevision int
	GuestSPKIPin       string
}

type CircleRevokeNotice struct {
	ID             string
	CircleID       string
	CreatedAt      time.Time
	AttemptCount   int
	AcknowledgedAt *time.Time
}

type BindShadowPrincipalInput struct {
	CircleID             string
	RemoteProfileID      string
	RemoteProfileBinding string
	PolicyProfileID      ProfileID
	Revealed             bool
	ActorProfileID       ProfileID
}

func randomCircleID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

func (s *Store) CreateCircle(ctx context.Context, id string) (CircleMembership, error) {
	if id == "" {
		return CircleMembership{}, ValidationError("circle ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return CircleMembership{}, err
	}
	if _, err := s.catalogW.ExecContext(ctx, `INSERT INTO circle_membership(id) VALUES (?)`, id); err != nil {
		return CircleMembership{}, err
	}
	return CircleMembership{ID: id, CredentialGeneration: 1}, nil
}

func (s *Store) Circle(ctx context.Context, id string) (CircleMembership, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return CircleMembership{}, err
	}
	return s.circleLocked(ctx, s.catalogR, id)
}

type circleQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) circleLocked(ctx context.Context, q circleQueryer, id string) (CircleMembership, error) {
	var membership CircleMembership
	var revokedAt sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT id, credential_generation, revoked_at_ms
		FROM circle_membership WHERE id = ?
	`, id).Scan(&membership.ID, &membership.CredentialGeneration, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CircleMembership{}, ErrCircleNotFound
	}
	if err != nil {
		return CircleMembership{}, err
	}
	if revokedAt.Valid {
		value := millisToTime(revokedAt.Int64)
		membership.RevokedAt = &value
	}
	return membership, nil
}

func (s *Store) BindShadowPrincipal(ctx context.Context, in BindShadowPrincipalInput) (ShadowPrincipal, error) {
	return s.bindShadowPrincipal(ctx, in, false)
}

func (s *Store) RebindShadowPrincipal(ctx context.Context, in BindShadowPrincipalInput) (ShadowPrincipal, error) {
	return s.bindShadowPrincipal(ctx, in, true)
}

func (s *Store) bindShadowPrincipal(ctx context.Context, in BindShadowPrincipalInput, replace bool) (ShadowPrincipal, error) {
	if in.CircleID == "" || in.RemoteProfileID == "" || in.RemoteProfileBinding == "" || in.PolicyProfileID == "" || in.ActorProfileID == "" {
		return ShadowPrincipal{}, ValidationError("circle shadow principal binding is incomplete")
	}
	id, err := randomCircleID("circle-principal-")
	if err != nil {
		return ShadowPrincipal{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ShadowPrincipal{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return ShadowPrincipal{}, err
	}
	defer tx.Rollback()
	if err := requireCircleOwner(ctx, tx, in.ActorProfileID); err != nil {
		return ShadowPrincipal{}, err
	}
	membership, err := s.circleLocked(ctx, tx, in.CircleID)
	if err != nil {
		return ShadowPrincipal{}, err
	}
	if membership.RevokedAt != nil {
		return ShadowPrincipal{}, ErrCircleRevoked
	}
	existing, err := shadowPrincipalLocked(ctx, tx, in.CircleID, in.RemoteProfileID)
	if err == nil {
		if existing.RemoteProfileBinding == in.RemoteProfileBinding && existing.PolicyProfileID == in.PolicyProfileID && existing.Revealed == in.Revealed {
			return existing, tx.Commit()
		}
		if !replace {
			return ShadowPrincipal{}, ErrRemoteProfileReassigned
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM circle_shadow_principal WHERE id = ?`, existing.ID); err != nil {
			return ShadowPrincipal{}, err
		}
	} else if !errors.Is(err, ErrShadowPrincipalNotFound) {
		return ShadowPrincipal{}, err
	}
	principal := ShadowPrincipal{
		ID: id, CircleID: in.CircleID, RemoteProfileID: in.RemoteProfileID,
		RemoteProfileBinding: in.RemoteProfileBinding, PolicyProfileID: in.PolicyProfileID,
		Revealed: in.Revealed, CreatedByProfileID: in.ActorProfileID,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO circle_shadow_principal
			(id, circle_id, remote_profile_id, remote_profile_binding, policy_profile_id, revealed, created_by_profile_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, principal.ID, principal.CircleID, principal.RemoteProfileID, principal.RemoteProfileBinding,
		principal.PolicyProfileID, principal.Revealed, principal.CreatedByProfileID); err != nil {
		return ShadowPrincipal{}, err
	}
	if err := tx.Commit(); err != nil {
		return ShadowPrincipal{}, err
	}
	return principal, nil
}

func requireCircleOwner(ctx context.Context, tx *sql.Tx, actor ProfileID) error {
	var owner int
	err := tx.QueryRowContext(ctx, `SELECT is_owner FROM profile WHERE id = ?`, actor).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || owner == 0 {
		return ErrCircleOwnerRequired
	}
	return err
}

func (s *Store) ShadowPrincipal(ctx context.Context, circleID, remoteProfileID string) (ShadowPrincipal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ShadowPrincipal{}, err
	}
	return shadowPrincipalLocked(ctx, s.catalogR, circleID, remoteProfileID)
}

func shadowPrincipalLocked(ctx context.Context, q circleQueryer, circleID, remoteProfileID string) (ShadowPrincipal, error) {
	var principal ShadowPrincipal
	var revealed int
	err := q.QueryRowContext(ctx, `
		SELECT p.id, p.circle_id, p.remote_profile_id, p.remote_profile_binding,
		       p.policy_profile_id, p.revealed, p.created_by_profile_id
		FROM circle_shadow_principal p
		JOIN circle_membership c ON c.id = p.circle_id
		WHERE p.circle_id = ? AND p.remote_profile_id = ? AND c.revoked_at_ms IS NULL
	`, circleID, remoteProfileID).Scan(&principal.ID, &principal.CircleID, &principal.RemoteProfileID,
		&principal.RemoteProfileBinding, &principal.PolicyProfileID, &revealed, &principal.CreatedByProfileID)
	if errors.Is(err, sql.ErrNoRows) {
		return ShadowPrincipal{}, ErrShadowPrincipalNotFound
	}
	if err != nil {
		return ShadowPrincipal{}, err
	}
	principal.Revealed = revealed != 0
	return principal, nil
}

func (s *Store) CacheCircleSession(ctx context.Context, cache CircleSessionCache) error {
	if cache.CircleID == "" || cache.SessionID == "" || cache.ShadowPrincipalID == "" || cache.NegotiatedRevision < 0 || cache.GuestSPKIPin == "" {
		return ValidationError("circle session cache is incomplete")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO circle_session_cache(circle_id, session_id, shadow_principal_id, negotiated_revision, guest_spki_pin)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(circle_id, session_id) DO UPDATE SET
			shadow_principal_id = excluded.shadow_principal_id,
			negotiated_revision = excluded.negotiated_revision,
			guest_spki_pin = excluded.guest_spki_pin
	`, cache.CircleID, cache.SessionID, cache.ShadowPrincipalID, cache.NegotiatedRevision, cache.GuestSPKIPin)
	return err
}

func (s *Store) CircleSession(ctx context.Context, circleID, sessionID string) (CircleSessionCache, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return CircleSessionCache{}, err
	}
	var cache CircleSessionCache
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT s.circle_id, s.session_id, s.shadow_principal_id, s.negotiated_revision, s.guest_spki_pin
		FROM circle_session_cache s
		JOIN circle_membership c ON c.id = s.circle_id
		WHERE s.circle_id = ? AND s.session_id = ? AND c.revoked_at_ms IS NULL
	`, circleID, sessionID).Scan(&cache.CircleID, &cache.SessionID, &cache.ShadowPrincipalID,
		&cache.NegotiatedRevision, &cache.GuestSPKIPin)
	if errors.Is(err, sql.ErrNoRows) {
		return CircleSessionCache{}, ErrCircleSessionNotFound
	}
	return cache, err
}

func (s *Store) RevokeCircle(ctx context.Context, circleID string, actor ProfileID, now time.Time) (CircleRevokeNotice, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	noticeID, err := randomCircleID("circle-revoke-")
	if err != nil {
		return CircleRevokeNotice{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return CircleRevokeNotice{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return CircleRevokeNotice{}, err
	}
	defer tx.Rollback()
	if err := requireCircleOwner(ctx, tx, actor); err != nil {
		return CircleRevokeNotice{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE circle_membership
		SET revoked_at_ms = COALESCE(revoked_at_ms, ?), credential_generation = credential_generation + 1
		WHERE id = ? AND revoked_at_ms IS NULL
	`, timeToMillis(now), circleID)
	if err != nil {
		return CircleRevokeNotice{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return CircleRevokeNotice{}, err
	}
	if changed == 0 {
		if _, err := s.circleLocked(ctx, tx, circleID); err != nil {
			return CircleRevokeNotice{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM circle_shadow_principal WHERE circle_id = ?`, circleID); err != nil {
		return CircleRevokeNotice{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO circle_revoke_notice(id, circle_id, created_at_ms)
		VALUES (?, ?, ?)
		ON CONFLICT(circle_id) DO NOTHING
	`, noticeID, circleID, timeToMillis(now)); err != nil {
		return CircleRevokeNotice{}, err
	}
	notice, err := circleNoticeLocked(ctx, tx, circleID)
	if err != nil {
		return CircleRevokeNotice{}, err
	}
	if err := tx.Commit(); err != nil {
		return CircleRevokeNotice{}, err
	}
	return notice, nil
}

func circleNoticeLocked(ctx context.Context, q circleQueryer, circleID string) (CircleRevokeNotice, error) {
	var notice CircleRevokeNotice
	var created int64
	var acknowledged sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT id, circle_id, created_at_ms, attempt_count, acknowledged_at_ms
		FROM circle_revoke_notice WHERE circle_id = ?
	`, circleID).Scan(&notice.ID, &notice.CircleID, &created, &notice.AttemptCount, &acknowledged)
	if errors.Is(err, sql.ErrNoRows) {
		return CircleRevokeNotice{}, ErrCircleNoticeNotFound
	}
	if err != nil {
		return CircleRevokeNotice{}, err
	}
	notice.CreatedAt = millisToTime(created)
	if acknowledged.Valid {
		value := millisToTime(acknowledged.Int64)
		notice.AcknowledgedAt = &value
	}
	return notice, nil
}

func (s *Store) PendingCircleRevokeNotices(ctx context.Context) ([]CircleRevokeNotice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, circle_id, created_at_ms, attempt_count
		FROM circle_revoke_notice WHERE acknowledged_at_ms IS NULL
		ORDER BY created_at_ms, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notices []CircleRevokeNotice
	for rows.Next() {
		var notice CircleRevokeNotice
		var created int64
		if err := rows.Scan(&notice.ID, &notice.CircleID, &created, &notice.AttemptCount); err != nil {
			return nil, err
		}
		notice.CreatedAt = millisToTime(created)
		notices = append(notices, notice)
	}
	return notices, rows.Err()
}

func (s *Store) RecordCircleRevokeNoticeAttempt(ctx context.Context, id string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE circle_revoke_notice SET attempt_count = attempt_count + 1
		WHERE id = ? AND acknowledged_at_ms IS NULL
	`, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrCircleNoticeNotFound
	}
	return nil
}

func (s *Store) AcknowledgeCircleRevokeNotice(ctx context.Context, id string, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE circle_revoke_notice SET acknowledged_at_ms = COALESCE(acknowledged_at_ms, ?)
		WHERE id = ?
	`, timeToMillis(now), id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrCircleNoticeNotFound
	}
	return nil
}

// CircleLeaseActive is the local-only Stream Token revocation resolver. It
// deliberately performs no peer I/O: revoked credentials, destroyed session
// cache, a rebind, or any context mismatch refuses immediately.
func (s *Store) CircleLeaseActive(ctx context.Context, circleID, sessionID, shadowPrincipalID, guestSPKIPin string, revision int, credentialGeneration int64) error {
	membership, err := s.Circle(ctx, circleID)
	if err != nil {
		return err
	}
	if membership.RevokedAt != nil || membership.CredentialGeneration != credentialGeneration {
		return ErrCircleRevoked
	}
	cache, err := s.CircleSession(ctx, circleID, sessionID)
	if err != nil {
		return err
	}
	if cache.ShadowPrincipalID != shadowPrincipalID || cache.GuestSPKIPin != guestSPKIPin || cache.NegotiatedRevision != revision {
		return ErrCircleSessionNotFound
	}
	return nil
}

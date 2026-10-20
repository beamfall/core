package streamtoken

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/beamfall/core/internal/serverrole"
	"github.com/beamfall/core/internal/store"
)

const (
	CircleLeaseTTL         = 120 * time.Second
	CircleLeaseRenewalLead = 60 * time.Second
	MaxCircleLeaseTTL      = 300 * time.Second
)

type CircleLeaseScope string

const (
	CircleScopeCatalogRead CircleLeaseScope = "catalog-read"
	CircleScopeMediaStream CircleLeaseScope = "media-stream"
)

type CircleGrant struct {
	CircleID             string           `json:"circleID"`
	CircleSessionID      string           `json:"circleSessionID"`
	ShadowPrincipalID    string           `json:"shadowPrincipalID"`
	LeaseScope           CircleLeaseScope `json:"leaseScope"`
	NegotiatedRevision   int              `json:"negotiatedRevision"`
	GuestServerSPKIPin   string           `json:"guestServerSPKIPin"`
	CredentialGeneration int64            `json:"credentialGeneration"`
	MediaID              store.MediaID    `json:"mediaID,omitempty"`
	PlaybackDecision     string           `json:"playbackDecision,omitempty"`
	IssuedAt             time.Time        `json:"issuedAt"`
	ExpiresAt            time.Time        `json:"expiresAt"`
}

type CircleLeaseContext struct {
	CircleID           string
	CircleSessionID    string
	ShadowPrincipalID  string
	LeaseScope         CircleLeaseScope
	NegotiatedRevision int
	GuestServerSPKIPin string
	MediaID            store.MediaID
}

type CircleLeaseResolver interface {
	CircleLeaseActive(ctx context.Context, circleID, sessionID, shadowPrincipalID, guestSPKIPin string, revision int, credentialGeneration int64) error
}

func CircleLeaseNeedsRenewal(grant CircleGrant, now time.Time) bool {
	return !now.Before(grant.ExpiresAt.Add(-CircleLeaseRenewalLead))
}

func (s *Service) SetCircleLeaseResolver(resolver CircleLeaseResolver) {
	s.circleLeaseResolver = resolver
}

func (s *Service) IssueCircle(ctx context.Context, grant CircleGrant) (string, error) {
	if err := s.role.GuardAuthority(serverrole.FaceStreamToken); err != nil {
		return "", err
	}
	if len(s.key) == 0 {
		return "", errors.New("stream token key is required")
	}
	now := s.now().UTC()
	if grant.IssuedAt.IsZero() {
		grant.IssuedAt = now
	}
	if grant.ExpiresAt.IsZero() {
		grant.ExpiresAt = grant.IssuedAt.Add(CircleLeaseTTL)
	}
	if err := validCircleGrant(grant, now); err != nil {
		return "", err
	}
	if s.circleLeaseResolver == nil {
		return "", errors.New("circle lease resolver is required")
	}
	if err := s.circleLeaseResolver.CircleLeaseActive(ctx, grant.CircleID, grant.CircleSessionID,
		grant.ShadowPrincipalID, grant.GuestServerSPKIPin, grant.NegotiatedRevision, grant.CredentialGeneration); err != nil {
		return "", errors.New("circle lease is not locally active")
	}
	payload, err := json.Marshal(grant)
	if err != nil {
		return "", err
	}
	return encode(payload) + "." + encode(s.sign(payload)), nil
}

func validCircleGrant(grant CircleGrant, now time.Time) error {
	if grant.CircleID == "" || grant.CircleSessionID == "" || grant.ShadowPrincipalID == "" || grant.GuestServerSPKIPin == "" || grant.CredentialGeneration <= 0 {
		return errors.New("circle lease binding is incomplete")
	}
	if grant.NegotiatedRevision < 0 {
		return errors.New("circle lease revision is invalid")
	}
	if grant.LeaseScope != CircleScopeCatalogRead && grant.LeaseScope != CircleScopeMediaStream {
		return errors.New("circle lease scope is invalid")
	}
	if grant.LeaseScope == CircleScopeMediaStream && (grant.MediaID == "" || grant.PlaybackDecision == "") {
		return errors.New("circle media lease subject is incomplete")
	}
	if grant.LeaseScope == CircleScopeCatalogRead && (grant.MediaID != "" || grant.PlaybackDecision != "") {
		return errors.New("circle catalog lease cannot carry a media subject")
	}
	if grant.IssuedAt.After(now) || !grant.ExpiresAt.After(now) || !grant.ExpiresAt.After(grant.IssuedAt) {
		return errors.New("circle lease lifetime is invalid")
	}
	if grant.ExpiresAt.Sub(grant.IssuedAt) > MaxCircleLeaseTTL {
		return errors.New("circle lease exceeds maximum TTL")
	}
	return nil
}

func (s *Service) VerifyCircle(ctx context.Context, token string, expected CircleLeaseContext) (CircleGrant, error) {
	grant, err := s.authenticateCircle(token)
	if err != nil {
		return CircleGrant{}, err
	}
	if !circleContextMatches(grant, expected) {
		return CircleGrant{}, errors.New("circle lease context mismatch")
	}
	if s.circleLeaseResolver == nil {
		return CircleGrant{}, errors.New("circle lease resolver is required")
	}
	if err := s.circleLeaseResolver.CircleLeaseActive(ctx, grant.CircleID, grant.CircleSessionID,
		grant.ShadowPrincipalID, grant.GuestServerSPKIPin, grant.NegotiatedRevision, grant.CredentialGeneration); err != nil {
		return CircleGrant{}, errors.New("circle lease is not locally active")
	}
	return grant, nil
}

func (s *Service) authenticateCircle(token string) (CircleGrant, error) {
	if len(s.key) == 0 {
		return CircleGrant{}, errors.New("stream token key is required")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return CircleGrant{}, errors.New("invalid circle lease")
	}
	payload, err := decode(parts[0])
	if err != nil {
		return CircleGrant{}, errors.New("invalid circle lease payload")
	}
	signature, err := decode(parts[1])
	if err != nil || !validSignature(signature, s.sign(payload)) {
		return CircleGrant{}, errors.New("invalid circle lease signature")
	}
	var grant CircleGrant
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&grant); err != nil {
		return CircleGrant{}, errors.New("invalid circle lease payload")
	}
	if err := validCircleGrant(grant, s.now().UTC()); err != nil {
		return CircleGrant{}, err
	}
	return grant, nil
}

// GuardCircleMediaLease authenticates a Media-bound Circle lease and guards
// its signed session context for the lifetime of the byte stream.
func (s *Service) GuardCircleMediaLease(parent context.Context, token string, pollInterval time.Duration) (context.Context, context.CancelCauseFunc, CircleGrant, error) {
	grant, err := s.authenticateCircle(token)
	if err != nil {
		return nil, nil, CircleGrant{}, err
	}
	if grant.LeaseScope != CircleScopeMediaStream {
		return nil, nil, CircleGrant{}, errors.New("circle lease does not authorize media streaming")
	}
	expected := CircleLeaseContext{
		CircleID: grant.CircleID, CircleSessionID: grant.CircleSessionID,
		ShadowPrincipalID: grant.ShadowPrincipalID, LeaseScope: grant.LeaseScope,
		NegotiatedRevision: grant.NegotiatedRevision, GuestServerSPKIPin: grant.GuestServerSPKIPin,
		MediaID: grant.MediaID,
	}
	ctx, cancel, err := s.GuardCircleLease(parent, token, expected, pollInterval)
	if err != nil {
		return nil, nil, CircleGrant{}, err
	}
	return ctx, cancel, grant, nil
}

// GuardCircleLease returns a child context for one active stream. It checks
// local revocation without peer I/O and cancels no later than signed expiry.
// The caller must invoke cancel when the stream ends.
func (s *Service) GuardCircleLease(parent context.Context, token string, expected CircleLeaseContext, pollInterval time.Duration) (context.Context, context.CancelCauseFunc, error) {
	grant, err := s.VerifyCircle(parent, token, expected)
	if err != nil {
		return nil, nil, err
	}
	if pollInterval <= 0 || pollInterval > CircleLeaseTTL {
		pollInterval = time.Second
	}
	ctx, cancel := context.WithCancelCause(parent)
	expires := time.NewTimer(grant.ExpiresAt.Sub(s.now().UTC()))
	ticker := time.NewTicker(pollInterval)
	go func() {
		defer expires.Stop()
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-expires.C:
				cancel(errors.New("circle lease expired"))
				return
			case <-ticker.C:
				if _, err := s.VerifyCircle(ctx, token, expected); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	return ctx, cancel, nil
}

func circleContextMatches(grant CircleGrant, expected CircleLeaseContext) bool {
	return grant.CircleID == expected.CircleID &&
		grant.CircleSessionID == expected.CircleSessionID &&
		grant.ShadowPrincipalID == expected.ShadowPrincipalID &&
		grant.LeaseScope == expected.LeaseScope &&
		grant.NegotiatedRevision == expected.NegotiatedRevision &&
		grant.GuestServerSPKIPin == expected.GuestServerSPKIPin &&
		grant.MediaID == expected.MediaID
}

func validSignature(got, want []byte) bool {
	if len(got) != len(want) {
		return false
	}
	var different byte
	for i := range got {
		different |= got[i] ^ want[i]
	}
	return different == 0
}

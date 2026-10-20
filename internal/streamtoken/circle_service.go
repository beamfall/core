package streamtoken

import (
	"context"
	"errors"
	"time"

	"github.com/beamfall/core/internal/store"
)

var (
	ErrPrincipalUnresolved = errors.New("circle principal unresolved")
	ErrPolicyDenied        = errors.New("circle policy denied")
	ErrMediaIneligible     = errors.New("circle media ineligible")
)

type StateStore interface {
	Circle(context.Context, string) (store.CircleMembership, error)
	ShadowPrincipal(context.Context, string, string) (store.ShadowPrincipal, error)
	CacheCircleSession(context.Context, store.CircleSessionCache) error
	CircleSession(context.Context, string, string) (store.CircleSessionCache, error)
	Profile(context.Context, store.ProfileID) (store.Profile, error)
	ProfileAccessWindows(context.Context, store.ProfileID) ([]store.ProfileAccessWindow, error)
	Media(context.Context, store.MediaID, store.LibraryFilter) (store.Media, error)
	CreateAccessRequest(context.Context, store.AccessRequestInput) (store.AccessRequest, error)
	AccessRequestByID(context.Context, string, time.Time) (store.AccessRequest, error)
}

type CertificationFilterResolver func(context.Context, store.Profile) ([]store.CertificationConstraint, error)

type StorePolicy struct {
	Store                 StateStore
	CertificationResolver CertificationFilterResolver
}

type PolicyAction struct {
	Operation       Operation
	Principal       store.ShadowPrincipal
	MediaID         store.MediaID
	AccessRequestID string
	OwnerNow        time.Time
}

type PolicyDecision struct {
	PlaybackDecision string
	AccessRequest    *store.AccessRequest
}

func (p StorePolicy) Evaluate(ctx context.Context, action PolicyAction) (PolicyDecision, error) {
	profile, err := p.Store.Profile(ctx, action.Principal.PolicyProfileID)
	if err != nil || profile.Disabled {
		return PolicyDecision{}, ErrPolicyDenied
	}
	windows, err := p.Store.ProfileAccessWindows(ctx, profile.ID)
	if err != nil || !store.AllowedAt(windows, action.OwnerNow) {
		return PolicyDecision{}, ErrPolicyDenied
	}
	switch action.Operation {
	case OperationCatalogRead:
		return PolicyDecision{}, nil
	case OperationPlaybackAuthorize:
		filter, err := p.filter(ctx, profile, action.Principal.Revealed)
		if err != nil {
			return PolicyDecision{}, ErrPolicyDenied
		}
		if _, err := p.Store.Media(ctx, action.MediaID, filter); err != nil {
			return PolicyDecision{}, ErrMediaIneligible
		}
		return PolicyDecision{PlaybackDecision: "owner-authorized"}, nil
	case OperationAccessRequestCreate:
		request, err := p.Store.CreateAccessRequest(ctx, store.AccessRequestInput{
			ProfileID: profile.ID, MediaID: action.MediaID, Now: action.OwnerNow,
		})
		if err != nil {
			return PolicyDecision{}, ErrPolicyDenied
		}
		return PolicyDecision{AccessRequest: &request}, nil
	case OperationAccessRequestStatus:
		request, err := p.Store.AccessRequestByID(ctx, action.AccessRequestID, action.OwnerNow)
		if err != nil || request.ProfileID != profile.ID {
			return PolicyDecision{}, ErrPolicyDenied
		}
		return PolicyDecision{AccessRequest: &request}, nil
	default:
		return PolicyDecision{}, ErrPolicyDenied
	}
}

func (p StorePolicy) filter(ctx context.Context, profile store.Profile, revealed bool) (store.LibraryFilter, error) {
	filter := store.LibraryFilter{
		AllowedIDs: profile.Library, Revealed: revealed, PlaybackGate: true,
	}
	if len(profile.CertificationCeilings) == 0 {
		return filter, nil
	}
	if p.CertificationResolver == nil {
		return store.LibraryFilter{}, ErrPolicyDenied
	}
	constraints, err := p.CertificationResolver(ctx, profile)
	if err != nil {
		return store.LibraryFilter{}, err
	}
	filter.Certification = constraints
	return filter, nil
}

type CircleService struct {
	state  StateStore
	policy StorePolicy
	tokens *Service
	now    func() time.Time
}

func NewCircleService(state StateStore, tokens *Service, now func() time.Time, certificationResolver CertificationFilterResolver) *CircleService {
	if now == nil {
		now = time.Now
	}
	return &CircleService{
		state: state, tokens: tokens, now: now,
		policy: StorePolicy{Store: state, CertificationResolver: certificationResolver},
	}
}

func (s *CircleService) OpenSession(ctx context.Context, circleID, remoteProfileID, sessionID string, revision int, guestSPKIPin string) (store.ShadowPrincipal, error) {
	principal, err := s.state.ShadowPrincipal(ctx, circleID, remoteProfileID)
	if err != nil {
		return store.ShadowPrincipal{}, ErrPrincipalUnresolved
	}
	cache := store.CircleSessionCache{
		CircleID: circleID, SessionID: sessionID, ShadowPrincipalID: principal.ID,
		NegotiatedRevision: revision, GuestSPKIPin: guestSPKIPin,
	}
	if err := s.state.CacheCircleSession(ctx, cache); err != nil {
		return store.ShadowPrincipal{}, err
	}
	return principal, nil
}

type AuthorizationRequest struct {
	CircleID           string
	CircleSessionID    string
	RemoteProfileID    string
	Operation          Operation
	LeaseScope         CircleLeaseScope
	NegotiatedRevision int
	GuestServerSPKIPin string
	MediaID            store.MediaID
	AccessRequestID    string
}

type Authorization struct {
	Token          string
	Grant          CircleGrant
	PolicyDecision PolicyDecision
}

func (s *CircleService) Authorize(ctx context.Context, request AuthorizationRequest) (Authorization, error) {
	principal, err := s.state.ShadowPrincipal(ctx, request.CircleID, request.RemoteProfileID)
	if err != nil {
		return Authorization{}, ErrPrincipalUnresolved
	}
	cache, err := s.state.CircleSession(ctx, request.CircleID, request.CircleSessionID)
	if err != nil || cache.ShadowPrincipalID != principal.ID || cache.NegotiatedRevision != request.NegotiatedRevision || cache.GuestSPKIPin != request.GuestServerSPKIPin {
		return Authorization{}, streamtokenContextError()
	}
	ownerNow := s.now().UTC()
	policyOperation, err := leasePolicyOperation(request.Operation, request.LeaseScope)
	if err != nil {
		return Authorization{}, err
	}
	decision, err := s.policy.Evaluate(ctx, PolicyAction{
		Operation: policyOperation, Principal: principal, MediaID: request.MediaID,
		AccessRequestID: request.AccessRequestID, OwnerNow: ownerNow,
	})
	if err != nil {
		return Authorization{}, err
	}
	membership, err := s.state.Circle(ctx, request.CircleID)
	if err != nil {
		return Authorization{}, err
	}
	grant := CircleGrant{
		CircleID: request.CircleID, CircleSessionID: request.CircleSessionID,
		ShadowPrincipalID: principal.ID, LeaseScope: request.LeaseScope,
		NegotiatedRevision: request.NegotiatedRevision, GuestServerSPKIPin: request.GuestServerSPKIPin,
		CredentialGeneration: membership.CredentialGeneration, MediaID: request.MediaID,
		PlaybackDecision: decision.PlaybackDecision, IssuedAt: ownerNow,
		ExpiresAt: ownerNow.Add(CircleLeaseTTL),
	}
	token, err := s.tokens.IssueCircle(ctx, grant)
	if err != nil {
		return Authorization{}, err
	}
	return Authorization{Token: token, Grant: grant, PolicyDecision: decision}, nil
}

func leasePolicyOperation(operation Operation, scope CircleLeaseScope) (Operation, error) {
	switch operation {
	case OperationCatalogRead:
		if scope == CircleScopeCatalogRead {
			return operation, nil
		}
	case OperationPlaybackAuthorize:
		if scope == CircleScopeMediaStream {
			return operation, nil
		}
	case OperationLeaseRenew:
		if scope == CircleScopeCatalogRead {
			return OperationCatalogRead, nil
		}
		if scope == CircleScopeMediaStream {
			return OperationPlaybackAuthorize, nil
		}
	}
	return "", ErrPolicyDenied
}

func (s *CircleService) CreateAccessRequest(ctx context.Context, circleID, remoteProfileID string, mediaID store.MediaID) (store.AccessRequest, error) {
	principal, err := s.state.ShadowPrincipal(ctx, circleID, remoteProfileID)
	if err != nil {
		return store.AccessRequest{}, ErrPrincipalUnresolved
	}
	decision, err := s.policy.Evaluate(ctx, PolicyAction{
		Operation: OperationAccessRequestCreate, Principal: principal,
		MediaID: mediaID, OwnerNow: s.now().UTC(),
	})
	if err != nil || decision.AccessRequest == nil {
		return store.AccessRequest{}, ErrPolicyDenied
	}
	return *decision.AccessRequest, nil
}

func (s *CircleService) AccessRequestStatus(ctx context.Context, circleID, remoteProfileID, requestID string) (store.AccessRequest, error) {
	principal, err := s.state.ShadowPrincipal(ctx, circleID, remoteProfileID)
	if err != nil {
		return store.AccessRequest{}, ErrPrincipalUnresolved
	}
	decision, err := s.policy.Evaluate(ctx, PolicyAction{
		Operation: OperationAccessRequestStatus, Principal: principal,
		AccessRequestID: requestID, OwnerNow: s.now().UTC(),
	})
	if err != nil || decision.AccessRequest == nil {
		return store.AccessRequest{}, ErrPolicyDenied
	}
	return *decision.AccessRequest, nil
}

func streamtokenContextError() error {
	return errors.New("circle lease context mismatch")
}

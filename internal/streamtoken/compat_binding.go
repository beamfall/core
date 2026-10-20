package streamtoken

import (
	"context"
	"errors"
	"fmt"

	"github.com/beamfall/core/internal/store"
)

// ErrCompatDecisionInvalid means a Stream Token's compatibility decision is
// no longer the subject's live decision.
var ErrCompatDecisionInvalid = errors.New("stream token compatibility decision is invalid")

// DecisionWant identifies the live authorization context a resolver must use.
// The concrete server adapter resolves the Session again so authorization and
// compatibility changes made after mint are observed on every byte fetch.
type DecisionWant struct {
	SessionID store.SessionID
}

// DecisionResolver resolves the subject's current server-held compatibility
// decision. ok=false with no error means the subject has no decision and keeps
// the established unbound behavior; a stale, expired, or refused decision is
// an error so mint and verify fail closed.
type DecisionResolver interface {
	Resolve(ctx context.Context, subject string, want DecisionWant) (decisionID string, ok bool, err error)
}

type compatDecisionStampKey struct{}

type compatDecisionStamp struct {
	subject    string
	decisionID string
	err        error
}

// StampCompatDecision binds the current decision to a grant before signing.
func StampCompatDecision(ctx context.Context, grant Grant, resolver DecisionResolver) (Grant, error) {
	if resolver == nil {
		if grant.CompatSubject == "" && grant.CompatDecisionID == "" {
			return grant, nil
		}
		return Grant{}, ErrCompatDecisionInvalid
	}
	subject := decisionSubject(grant.SessionID)
	decisionID, ok, err := resolver.Resolve(ctx, subject, DecisionWant{SessionID: grant.SessionID})
	if err != nil {
		return Grant{}, fmt.Errorf("%w: %v", ErrCompatDecisionInvalid, err)
	}
	if !ok {
		if grant.CompatSubject != "" || grant.CompatDecisionID != "" {
			return Grant{}, ErrCompatDecisionInvalid
		}
		return grant, nil
	}
	if subject == "" || decisionID == "" {
		return Grant{}, ErrCompatDecisionInvalid
	}
	grant.CompatSubject = subject
	grant.CompatDecisionID = decisionID
	return grant, nil
}

// VerifyCompatDecision rejects a bound grant unless its decision is still the
// subject's exact live decision.
//
// An UNSTAMPED grant is still checked against the resolver. Accepting it
// unconditionally would defeat this slice's entire purpose: every token issued
// before this binding existed is unstamped, and those already-issued tokens are
// exactly the ones an invalidation has to stop serving. Skipping the resolver
// here left a signed pre-change token usable after its subject was invalidated
// for as long as the signing key stayed active.
//
// The check is narrow on purpose. Only a resolver ERROR — stale, expired or
// refused, per DecisionResolver's contract — rejects. ok=false (the subject
// simply has no decision) keeps the established unbound behavior, and so does a
// subject with a live decision: an unstamped token is not bound to a decision,
// so a VALID decision gives nothing to contradict. That breaks precisely the
// tokens whose subject was invalidated and no others.
func VerifyCompatDecision(ctx context.Context, grant Grant, resolver DecisionResolver) error {
	if grant.CompatSubject == "" && grant.CompatDecisionID == "" {
		if resolver == nil {
			return nil
		}
		subject := decisionSubject(grant.SessionID)
		if subject == "" {
			return nil
		}
		if _, _, err := resolver.Resolve(ctx, subject, DecisionWant{SessionID: grant.SessionID}); err != nil {
			return fmt.Errorf("%w: %v", ErrCompatDecisionInvalid, err)
		}
		return nil
	}
	if grant.CompatSubject == "" || grant.CompatDecisionID == "" || resolver == nil {
		return ErrCompatDecisionInvalid
	}
	subject := decisionSubject(grant.SessionID)
	if subject == "" || grant.CompatSubject != subject {
		return ErrCompatDecisionInvalid
	}
	decisionID, ok, err := resolver.Resolve(ctx, subject, DecisionWant{SessionID: grant.SessionID})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCompatDecisionInvalid, err)
	}
	if !ok || decisionID == "" || decisionID != grant.CompatDecisionID {
		return ErrCompatDecisionInvalid
	}
	return nil
}

func decisionSubject(sessionID store.SessionID) string {
	if sessionID == "" {
		return ""
	}
	return "session:" + string(sessionID)
}

// ContextWithCompatDecisionStamp carries one already-resolved decision across
// every token minted for a single playback response. The Issue boundary still
// validates the stamp against each grant's Session before signing.
func ContextWithCompatDecisionStamp(ctx context.Context, grant Grant, err error) context.Context {
	if err == nil && grant.CompatSubject == "" && grant.CompatDecisionID == "" {
		return ctx
	}
	return context.WithValue(ctx, compatDecisionStampKey{}, compatDecisionStamp{
		subject:    grant.CompatSubject,
		decisionID: grant.CompatDecisionID,
		err:        err,
	})
}

func (s *Service) stampCompatDecisionForIssue(ctx context.Context, grant Grant) (Grant, error) {
	stamp, ok := ctx.Value(compatDecisionStampKey{}).(compatDecisionStamp)
	if !ok {
		return StampCompatDecision(ctx, grant, s.compatResolver)
	}
	if stamp.err != nil {
		return Grant{}, stamp.err
	}
	if stamp.subject == "" && stamp.decisionID == "" {
		return grant, nil
	}
	if stamp.subject == "" || stamp.decisionID == "" || stamp.subject != decisionSubject(grant.SessionID) {
		return Grant{}, ErrCompatDecisionInvalid
	}
	grant.CompatSubject = stamp.subject
	grant.CompatDecisionID = stamp.decisionID
	return grant, nil
}

// SetCompatDecisionResolver wires the composition-root compatibility adapter.
// It is a boot-time setter; callers must not replace it while requests run.
func (s *Service) SetCompatDecisionResolver(resolver DecisionResolver) {
	s.compatResolver = resolver
}

// CompatDecisionResolver returns the boot-time resolver for playback's
// request-wide stamp. Callers must not mutate the resolver after startup.
func (s *Service) CompatDecisionResolver() DecisionResolver {
	return s.compatResolver
}

// VerifyCompatDecision verifies a grant against this service's resolver.
func (s *Service) VerifyCompatDecision(ctx context.Context, grant Grant) error {
	return VerifyCompatDecision(ctx, grant, s.compatResolver)
}

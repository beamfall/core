package streamtoken

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

type compatResolverStub struct {
	decisionID string
	ok         bool
	err        error
	subject    string
	want       DecisionWant
	calls      int
}

func (r *compatResolverStub) Resolve(_ context.Context, subject string, want DecisionWant) (string, bool, error) {
	r.calls++
	r.subject = subject
	r.want = want
	return r.decisionID, r.ok, r.err
}

func TestCompatDecisionBindingRejectsSupersededToken(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	resolver := &compatResolverStub{decisionID: "decision-1", ok: true}
	service := NewService([]byte("test-key"), func() time.Time { return now })
	service.SetCompatDecisionResolver(resolver)
	scope := auth.Scope{DeviceID: "device-tv", SessionID: "session-default", ProfileID: "profile-default"}

	token, err := service.Issue(context.Background(), Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}
	grant, err := service.Verify(token, TargetMediaDirect, scope)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if grant.CompatSubject != "session:session-default" || grant.CompatDecisionID != "decision-1" {
		t.Fatalf("compat stamp = %q/%q", grant.CompatSubject, grant.CompatDecisionID)
	}
	if resolver.subject != "session:session-default" || resolver.want.SessionID != scope.SessionID {
		t.Fatalf("resolver input = %q/%q", resolver.subject, resolver.want.SessionID)
	}
	if err := service.VerifyCompatDecision(context.Background(), grant); err != nil {
		t.Fatalf("unchanged decision rejected: %v", err)
	}

	resolver.decisionID = "decision-2"
	if err := service.VerifyCompatDecision(context.Background(), grant); !errors.Is(err, ErrCompatDecisionInvalid) {
		t.Fatalf("superseded decision error = %v, want %v", err, ErrCompatDecisionInvalid)
	}
}

func TestCompatDecisionBindingRejectsInvalidatedToken(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	resolver := &compatResolverStub{decisionID: "decision-1", ok: true}
	service := NewService([]byte("test-key"), func() time.Time { return now })
	service.SetCompatDecisionResolver(resolver)
	token, err := service.Issue(context.Background(), Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  "device-tv",
		SessionID: "session-default",
		ProfileID: "profile-default",
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := service.UnverifiedGrant(token, TargetMediaDirect)
	if err != nil {
		t.Fatal(err)
	}

	resolver.ok = false
	resolver.decisionID = ""
	if err := service.VerifyCompatDecision(context.Background(), grant); !errors.Is(err, ErrCompatDecisionInvalid) {
		t.Fatalf("invalidated decision error = %v, want %v", err, ErrCompatDecisionInvalid)
	}

	resolver.err = errors.New("decision expired")
	if err := service.VerifyCompatDecision(context.Background(), grant); !errors.Is(err, ErrCompatDecisionInvalid) {
		t.Fatalf("expired decision error = %v, want %v", err, ErrCompatDecisionInvalid)
	}
}

func TestCompatDecisionBindingLeavesNoDecisionTokenUnchanged(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })
	service.SetCompatDecisionResolver(&compatResolverStub{})
	token, err := service.Issue(context.Background(), Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  "device-tv",
		SessionID: "session-default",
		ProfileID: "profile-default",
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := service.UnverifiedGrant(token, TargetMediaDirect)
	if err != nil {
		t.Fatal(err)
	}
	if grant.CompatSubject != "" || grant.CompatDecisionID != "" {
		t.Fatalf("unbound grant gained compat stamp %q/%q", grant.CompatSubject, grant.CompatDecisionID)
	}
	if err := service.VerifyCompatDecision(context.Background(), grant); err != nil {
		t.Fatalf("unbound grant rejected: %v", err)
	}
}

func TestCompatDecisionBindingFailsMintOnResolverError(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })
	service.SetCompatDecisionResolver(&compatResolverStub{err: errors.New("stale generation")})

	token, err := service.Issue(context.Background(), Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  "device-tv",
		SessionID: "session-default",
		ProfileID: "profile-default",
		ExpiresAt: now.Add(time.Minute),
	})
	if token != "" || !errors.Is(err, ErrCompatDecisionInvalid) {
		t.Fatalf("Issue = %q, %v; want empty token and %v", token, err, ErrCompatDecisionInvalid)
	}
}

func TestCompatDecisionContextReusesOneResolvedStamp(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	resolver := &compatResolverStub{decisionID: "decision-1", ok: true}
	service := NewService([]byte("test-key"), func() time.Time { return now })
	service.SetCompatDecisionResolver(resolver)
	stamp, err := StampCompatDecision(context.Background(), Grant{SessionID: "session-default"}, resolver)
	ctx := ContextWithCompatDecisionStamp(context.Background(), stamp, err)
	resolver.decisionID = "decision-2"

	for _, mediaID := range []string{"media-alpha", "media-beta"} {
		token, err := service.Issue(ctx, Grant{
			Target:    TargetMediaDirect,
			MediaID:   store.MediaID(mediaID),
			DeviceID:  "device-tv",
			SessionID: "session-default",
			ProfileID: "profile-default",
			ExpiresAt: now.Add(time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		grant, err := service.UnverifiedGrant(token, TargetMediaDirect)
		if err != nil {
			t.Fatal(err)
		}
		if grant.CompatDecisionID != "decision-1" {
			t.Fatalf("%s decision = %q, want decision-1", mediaID, grant.CompatDecisionID)
		}
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want one per playback response", resolver.calls)
	}
}

// An UNSTAMPED grant — one issued before this binding existed — must still be
// rejected once its subject's decision is invalidated. Those already-issued
// tokens are exactly what this slice exists to stop serving, and verification
// previously returned early for them without ever consulting the resolver, so a
// signed pre-change token stayed usable after invalidation for as long as the
// signing key was active.
func TestCompatDecisionBindingRejectsUnstampedTokenAfterInvalidation(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })
	unstamped := Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  "device-tv",
		SessionID: "session-legacy",
	}

	// Subject invalidated: the resolver reports an error, per DecisionResolver's
	// contract that stale/expired/refused decisions fail closed.
	invalidated := &compatResolverStub{err: errors.New("decision invalidated")}
	service.SetCompatDecisionResolver(invalidated)
	if err := service.VerifyCompatDecision(context.Background(), unstamped); !errors.Is(err, ErrCompatDecisionInvalid) {
		t.Fatalf("unstamped token after invalidation = %v, want %v", err, ErrCompatDecisionInvalid)
	}
	if invalidated.calls == 0 {
		t.Fatal("resolver was never consulted for an unstamped grant — the early return is back")
	}
	if invalidated.subject != "session:session-legacy" {
		t.Fatalf("resolver subject = %q, want %q", invalidated.subject, "session:session-legacy")
	}

	// Control: the same unstamped token stays valid while the subject holds a
	// live decision. The fix must break only invalidated subjects.
	live := &compatResolverStub{decisionID: "decision-1", ok: true}
	service.SetCompatDecisionResolver(live)
	if err := service.VerifyCompatDecision(context.Background(), unstamped); err != nil {
		t.Fatalf("unstamped token with a live decision = %v, want nil", err)
	}

	// Control: a subject with no decision at all keeps the established unbound
	// behavior (ok=false, no error).
	none := &compatResolverStub{}
	service.SetCompatDecisionResolver(none)
	if err := service.VerifyCompatDecision(context.Background(), unstamped); err != nil {
		t.Fatalf("unstamped token with no decision = %v, want nil", err)
	}
}

package streamtoken

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// circleResolverStub is read by GuardCircleLease's poll goroutine while tests mutate it, so
// the error is mutex-guarded (race-short gate, 2026-08-19).
type circleResolverStub struct {
	mu  sync.Mutex
	err error
}

func (r *circleResolverStub) CircleLeaseActive(context.Context, string, string, string, string, int, int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *circleResolverStub) setErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func validCircleTestGrant(now time.Time) CircleGrant {
	return CircleGrant{
		CircleID: "circle-1", CircleSessionID: "session-1", ShadowPrincipalID: "principal-1",
		LeaseScope: CircleScopeCatalogRead, NegotiatedRevision: CurrentRevision, GuestServerSPKIPin: "spki-1",
		CredentialGeneration: 1, IssuedAt: now, ExpiresAt: now.Add(CircleLeaseTTL),
	}
}

func TestIssueCircleRejectsInvalidGrantsAndInactiveLease(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		edit func(*CircleGrant)
		want string
	}{
		{"incomplete binding", func(g *CircleGrant) { g.CircleID = "" }, "circle lease binding is incomplete"},
		{"negative revision", func(g *CircleGrant) { g.NegotiatedRevision = -1 }, "circle lease revision is invalid"},
		{"unknown scope", func(g *CircleGrant) { g.LeaseScope = "unknown" }, "circle lease scope is invalid"},
		{"media subject missing", func(g *CircleGrant) { g.LeaseScope = CircleScopeMediaStream }, "circle media lease subject is incomplete"},
		{"catalog carries media", func(g *CircleGrant) { g.MediaID = "media-1" }, "circle catalog lease cannot carry a media subject"},
		{"issued in future", func(g *CircleGrant) { g.IssuedAt = now.Add(time.Second); g.ExpiresAt = now.Add(time.Minute) }, "circle lease lifetime is invalid"},
		{"expired", func(g *CircleGrant) { g.IssuedAt = now.Add(-time.Minute); g.ExpiresAt = now }, "circle lease lifetime is invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			grant := validCircleTestGrant(now)
			test.edit(&grant)
			service := NewService([]byte("circle-test-key"), func() time.Time { return now })
			service.SetCircleLeaseResolver(&circleResolverStub{})
			if _, err := service.IssueCircle(context.Background(), grant); err == nil || err.Error() != test.want {
				t.Fatalf("IssueCircle() error = %v; want %q", err, test.want)
			}
		})
	}

	if _, err := NewService(nil, func() time.Time { return now }).IssueCircle(context.Background(), validCircleTestGrant(now)); err == nil || err.Error() != "stream token key is required" {
		t.Fatalf("IssueCircle without key = %v", err)
	}
	if _, err := NewService([]byte("circle-test-key"), func() time.Time { return now }).IssueCircle(context.Background(), validCircleTestGrant(now)); err == nil || err.Error() != "circle lease resolver is required" {
		t.Fatalf("IssueCircle without resolver = %v", err)
	}
	service := NewService([]byte("circle-test-key"), func() time.Time { return now })
	service.SetCircleLeaseResolver(&circleResolverStub{err: errors.New("inactive")})
	if _, err := service.IssueCircle(context.Background(), validCircleTestGrant(now)); err == nil || err.Error() != "circle lease is not locally active" {
		t.Fatalf("IssueCircle inactive lease = %v", err)
	}
}

func TestIssueCircleDefaultsLifetimeAndVerifyRejectsMalformedOrForeignTokens(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	service := NewService([]byte("circle-test-key"), func() time.Time { return now })
	service.SetCircleLeaseResolver(&circleResolverStub{})
	grant := validCircleTestGrant(now)
	grant.IssuedAt = time.Time{}
	grant.ExpiresAt = time.Time{}
	token, err := service.IssueCircle(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	expected := CircleLeaseContext{
		CircleID: grant.CircleID, CircleSessionID: grant.CircleSessionID, ShadowPrincipalID: grant.ShadowPrincipalID,
		LeaseScope: grant.LeaseScope, NegotiatedRevision: grant.NegotiatedRevision, GuestServerSPKIPin: grant.GuestServerSPKIPin,
	}
	verified, err := service.VerifyCircle(context.Background(), token, expected)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.IssuedAt.Equal(now) || !verified.ExpiresAt.Equal(now.Add(CircleLeaseTTL)) {
		t.Fatalf("default lifetime = %v to %v", verified.IssuedAt, verified.ExpiresAt)
	}

	foreign := NewService([]byte("other-server-key"), func() time.Time { return now })
	foreign.SetCircleLeaseResolver(&circleResolverStub{})
	cases := []struct {
		name, token, want string
		service           *Service
	}{
		{"missing key", token, "stream token key is required", NewService(nil, func() time.Time { return now })},
		{"bad segments", "one.two.three", "invalid circle lease", service},
		{"bad payload encoding", "%.AA", "invalid circle lease payload", service},
		{"bad signature encoding", "AA.%", "invalid circle lease signature", service},
		{"foreign server", token, "invalid circle lease signature", foreign},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.service.VerifyCircle(context.Background(), test.token, expected); err == nil || err.Error() != test.want {
				t.Fatalf("VerifyCircle() error = %v; want %q", err, test.want)
			}
		})
	}

	payload := []byte(`{"circleID":"circle-1","unexpected":true}`)
	malformedSigned := encode(payload) + "." + encode(service.sign(payload))
	if _, err := service.VerifyCircle(context.Background(), malformedSigned, expected); err == nil || err.Error() != "invalid circle lease payload" {
		t.Fatalf("VerifyCircle unknown field = %v", err)
	}
}

func TestCircleLeaseBindingExpiryAndLocalRevocation(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	service := NewService([]byte("circle-test-key"), func() time.Time { return now })
	resolver := &circleResolverStub{}
	service.SetCircleLeaseResolver(resolver)
	grant := CircleGrant{
		CircleID: "circle-1", CircleSessionID: "session-1", ShadowPrincipalID: "principal-1",
		LeaseScope: CircleScopeMediaStream, NegotiatedRevision: 2, GuestServerSPKIPin: "spki-1",
		CredentialGeneration: 1, MediaID: "media-1", PlaybackDecision: "owner-authorized",
		IssuedAt: now, ExpiresAt: now.Add(CircleLeaseTTL),
	}
	token, err := service.IssueCircle(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	expected := CircleLeaseContext{
		CircleID: grant.CircleID, CircleSessionID: grant.CircleSessionID,
		ShadowPrincipalID: grant.ShadowPrincipalID, LeaseScope: grant.LeaseScope,
		NegotiatedRevision: grant.NegotiatedRevision, GuestServerSPKIPin: grant.GuestServerSPKIPin,
		MediaID: grant.MediaID,
	}
	if _, err := service.VerifyCircle(context.Background(), token, expected); err != nil {
		t.Fatal(err)
	}
	if CircleLeaseNeedsRenewal(grant, grant.ExpiresAt.Add(-CircleLeaseRenewalLead-time.Second)) {
		t.Fatal("lease renewed before the fixed lead window")
	}
	if !CircleLeaseNeedsRenewal(grant, grant.ExpiresAt.Add(-CircleLeaseRenewalLead)) {
		t.Fatal("lease did not renew at the fixed lead window")
	}
	streamContext, cancel, err := service.GuardCircleLease(context.Background(), token, expected, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel(context.Canceled)
	mutations := []func(*CircleLeaseContext){
		func(v *CircleLeaseContext) { v.CircleID = "other" },
		func(v *CircleLeaseContext) { v.CircleSessionID = "other" },
		func(v *CircleLeaseContext) { v.ShadowPrincipalID = "other" },
		func(v *CircleLeaseContext) { v.LeaseScope = CircleScopeCatalogRead },
		func(v *CircleLeaseContext) { v.NegotiatedRevision++ },
		func(v *CircleLeaseContext) { v.GuestServerSPKIPin = "other" },
		func(v *CircleLeaseContext) { v.MediaID = store.MediaID("other") },
	}
	for _, mutate := range mutations {
		changed := expected
		mutate(&changed)
		if _, err := service.VerifyCircle(context.Background(), token, changed); err == nil {
			t.Fatalf("context mutation accepted: %+v", changed)
		}
	}
	resolver.setErr(errors.New("revoked locally"))
	if _, err := service.VerifyCircle(context.Background(), token, expected); err == nil {
		t.Fatal("locally revoked lease verified while peer unreachable")
	}
	select {
	case <-streamContext.Done():
		if context.Cause(streamContext) == nil {
			t.Fatal("active stream ended without revocation cause")
		}
	case <-time.After(time.Second):
		t.Fatal("active stream survived local revocation")
	}
	resolver.setErr(nil)
	now = now.Add(CircleLeaseTTL)
	if _, err := service.VerifyCircle(context.Background(), token, expected); err == nil {
		t.Fatal("expired lease verified")
	}
}

func TestCircleLeaseRejectsTTLAboveCeiling(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	service := NewService([]byte("circle-test-key"), func() time.Time { return now })
	service.SetCircleLeaseResolver(&circleResolverStub{})
	_, err := service.IssueCircle(context.Background(), CircleGrant{
		CircleID: "circle-1", CircleSessionID: "session-1", ShadowPrincipalID: "principal-1",
		LeaseScope: CircleScopeCatalogRead, NegotiatedRevision: 2, GuestServerSPKIPin: "spki-1",
		CredentialGeneration: 1, IssuedAt: now, ExpiresAt: now.Add(MaxCircleLeaseTTL + time.Second),
	})
	if err == nil {
		t.Fatal("lease above hard ceiling was clamped or accepted")
	}
}

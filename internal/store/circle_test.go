package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCircleShadowPrincipalLifecycleAndDurableRevocation(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := Open(ctx, dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCircle(ctx, "circle-1"); err != nil {
		t.Fatal(err)
	}
	input := BindShadowPrincipalInput{
		CircleID: "circle-1", RemoteProfileID: "remote-profile-1", RemoteProfileBinding: "binding-1",
		PolicyProfileID: "profile-managed", ActorProfileID: "profile-default",
	}
	if _, err := st.BindShadowPrincipal(ctx, BindShadowPrincipalInput{
		CircleID: input.CircleID, RemoteProfileID: input.RemoteProfileID, RemoteProfileBinding: input.RemoteProfileBinding,
		PolicyProfileID: input.PolicyProfileID, ActorProfileID: "profile-managed",
	}); !errors.Is(err, ErrCircleOwnerRequired) {
		t.Fatalf("non-owner bind = %v", err)
	}
	principal, err := st.BindShadowPrincipal(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CacheCircleSession(ctx, CircleSessionCache{
		CircleID: "circle-1", SessionID: "session-1", ShadowPrincipalID: principal.ID,
		NegotiatedRevision: 2, GuestSPKIPin: "spki-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	reopened, err := st.ShadowPrincipal(ctx, "circle-1", "remote-profile-1")
	if err != nil || reopened.ID != principal.ID {
		t.Fatalf("reopened principal = %+v, %v", reopened, err)
	}
	conflict := input
	conflict.RemoteProfileBinding = "binding-reassigned"
	if _, err := st.BindShadowPrincipal(ctx, conflict); !errors.Is(err, ErrRemoteProfileReassigned) {
		t.Fatalf("silent reassignment = %v", err)
	}
	rebound, err := st.RebindShadowPrincipal(ctx, conflict)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.ID == principal.ID {
		t.Fatal("destructive rebind retained shadow principal ID")
	}
	if _, err := st.CircleSession(ctx, "circle-1", "session-1"); !errors.Is(err, ErrCircleSessionNotFound) {
		t.Fatalf("rebind retained cached session: %v", err)
	}
	if err := st.CacheCircleSession(ctx, CircleSessionCache{
		CircleID: "circle-1", SessionID: "session-2", ShadowPrincipalID: rebound.ID,
		NegotiatedRevision: 2, GuestSPKIPin: "spki-1",
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	notice, err := st.RevokeCircle(ctx, "circle-1", "profile-default", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ShadowPrincipal(ctx, "circle-1", "remote-profile-1"); !errors.Is(err, ErrShadowPrincipalNotFound) {
		t.Fatalf("revocation retained principal: %v", err)
	}
	if _, err := st.CircleSession(ctx, "circle-1", "session-2"); !errors.Is(err, ErrCircleSessionNotFound) {
		t.Fatalf("revocation retained session: %v", err)
	}
	if err := st.RecordCircleRevokeNoticeAttempt(ctx, notice.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pending, err := st.PendingCircleRevokeNotices(ctx)
	if err != nil || len(pending) != 1 || pending[0].AttemptCount != 1 {
		t.Fatalf("pending after restart = %+v, %v", pending, err)
	}
	if err := st.AcknowledgeCircleRevokeNotice(ctx, notice.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	pending, err = st.PendingCircleRevokeNotices(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after ack = %+v, %v", pending, err)
	}
}

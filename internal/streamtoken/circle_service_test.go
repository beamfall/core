package streamtoken

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

func TestShadowPrincipalUsesLiveOwnerPolicyAndOwnerClock(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCircle(ctx, "circle-1"); err != nil {
		t.Fatal(err)
	}
	principal, err := st.BindShadowPrincipal(ctx, store.BindShadowPrincipalInput{
		CircleID: "circle-1", RemoteProfileID: "remote-managed", RemoteProfileBinding: "binding-managed",
		PolicyProfileID: "profile-managed", ActorProfileID: "profile-default",
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerNow := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC) // Monday.
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", []store.ProfileAccessWindow{{
		Weekday: int(ownerNow.Weekday()), StartMinute: 9 * 60, EndMinute: 11 * 60,
	}}); err != nil {
		t.Fatal(err)
	}
	tokens := NewService([]byte("circle-service-test-key"), func() time.Time { return ownerNow })
	tokens.SetCircleLeaseResolver(st)
	service := NewCircleService(st, tokens, func() time.Time { return ownerNow }, nil)
	if _, err := service.OpenSession(ctx, "circle-1", "remote-managed", "session-1", CurrentRevision, "spki-1"); err != nil {
		t.Fatal(err)
	}
	authorization, err := service.Authorize(ctx, AuthorizationRequest{
		CircleID: "circle-1", CircleSessionID: "session-1", RemoteProfileID: "remote-managed",
		Operation: OperationCatalogRead, LeaseScope: CircleScopeCatalogRead,
		NegotiatedRevision: CurrentRevision, GuestServerSPKIPin: "spki-1",
	})
	if err != nil || authorization.Grant.ShadowPrincipalID != principal.ID {
		t.Fatalf("catalog authorization = %+v, %v", authorization, err)
	}
	request, err := service.CreateAccessRequest(ctx, "circle-1", "remote-managed", "media-alpha")
	if err != nil || request.ProfileID != "profile-managed" {
		t.Fatalf("access request = %+v, %v", request, err)
	}
	status, err := service.AccessRequestStatus(ctx, "circle-1", "remote-managed", request.ID)
	if err != nil || status.ID != request.ID {
		t.Fatalf("access request status = %+v, %v", status, err)
	}
	ownerNow = ownerNow.Add(2 * time.Hour)
	if _, err := service.Authorize(ctx, AuthorizationRequest{
		CircleID: "circle-1", CircleSessionID: "session-1", RemoteProfileID: "remote-managed",
		Operation: OperationCatalogRead, LeaseScope: CircleScopeCatalogRead,
		NegotiatedRevision: CurrentRevision, GuestServerSPKIPin: "spki-1",
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("owner-clock schedule denial = %v", err)
	}
}

// TestCircleOwnerClockIgnoresSkewedGuestClock supplies guest endpoints whose
// clocks are three days ahead or behind the owner. The guest clock is consumed
// only on the guest side because Circle requests carry no time input. A
// deliberate mutation wiring guest.now into NewCircleService failed with:
// "circle_service_test.go:123: owner authorization with guest clock +72h = circle policy denied".
func TestCircleOwnerClockIgnoresSkewedGuestClock(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCircle(ctx, "circle-owner-clock"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BindShadowPrincipal(ctx, store.BindShadowPrincipalInput{
		CircleID: "circle-owner-clock", RemoteProfileID: "remote-managed", RemoteProfileBinding: "binding-managed",
		PolicyProfileID: "profile-managed", ActorProfileID: "profile-default", Revealed: false,
	}); err != nil {
		t.Fatal(err)
	}
	ownerNow := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC) // Monday.
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", []store.ProfileAccessWindow{{
		Weekday: int(ownerNow.Weekday()), StartMinute: 9 * 60, EndMinute: 11 * 60,
	}}); err != nil {
		t.Fatal(err)
	}
	tokens := NewService([]byte("circle-owner-clock-test-key"), func() time.Time { return ownerNow })
	tokens.SetCircleLeaseResolver(st)
	owner := NewCircleService(st, tokens, func() time.Time { return ownerNow }, nil)

	for _, skew := range []time.Duration{-72 * time.Hour, 72 * time.Hour} {
		t.Run(skew.String(), func(t *testing.T) {
			guest := struct {
				now func() time.Time
			}{now: func() time.Time { return ownerNow.Add(skew) }}
			guestNow := guest.now()
			if guestNow.Weekday() == ownerNow.Weekday() {
				t.Fatalf("guest clock did not cross the owner schedule boundary: owner=%v guest=%v", ownerNow, guestNow)
			}
			sessionID := "session-" + skew.String()
			if _, err := owner.OpenSession(ctx, "circle-owner-clock", "remote-managed", sessionID, CurrentRevision, "spki-1"); err != nil {
				t.Fatal(err)
			}
			authorization, err := owner.Authorize(ctx, AuthorizationRequest{
				CircleID: "circle-owner-clock", CircleSessionID: sessionID, RemoteProfileID: "remote-managed",
				Operation: OperationCatalogRead, LeaseScope: CircleScopeCatalogRead,
				NegotiatedRevision: CurrentRevision, GuestServerSPKIPin: "spki-1",
			})
			if err != nil {
				t.Fatalf("owner authorization with guest clock %+dh = %v", int(skew.Hours()), err)
			}
			if !authorization.Grant.IssuedAt.Equal(ownerNow) || !authorization.Grant.ExpiresAt.Equal(ownerNow.Add(CircleLeaseTTL)) {
				t.Fatalf("lease times used guest clock %v: issued=%v expires=%v", guestNow, authorization.Grant.IssuedAt, authorization.Grant.ExpiresAt)
			}
			if _, err := owner.Authorize(ctx, AuthorizationRequest{
				CircleID: "circle-owner-clock", CircleSessionID: sessionID, RemoteProfileID: "remote-managed",
				Operation: OperationPlaybackAuthorize, LeaseScope: CircleScopeMediaStream,
				NegotiatedRevision: CurrentRevision, GuestServerSPKIPin: "spki-1", MediaID: "media-archive",
			}); !errors.Is(err, ErrMediaIneligible) {
				t.Fatalf("unrevealed playback with guest clock %+dh = %v", int(skew.Hours()), err)
			}
		})
	}
}

func TestShadowPrincipalRevealAndRevocationDestroyAuthorization(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCircle(ctx, "circle-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BindShadowPrincipal(ctx, store.BindShadowPrincipalInput{
		CircleID: "circle-1", RemoteProfileID: "remote-adult", RemoteProfileBinding: "binding-adult",
		PolicyProfileID: "profile-default", ActorProfileID: "profile-default", Revealed: false,
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	tokens := NewService([]byte("circle-service-test-key"), func() time.Time { return now })
	tokens.SetCircleLeaseResolver(st)
	service := NewCircleService(st, tokens, func() time.Time { return now }, nil)
	principal, err := service.OpenSession(ctx, "circle-1", "remote-adult", "session-1", CurrentRevision, "spki-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authorize(ctx, AuthorizationRequest{
		CircleID: "circle-1", CircleSessionID: "session-1", RemoteProfileID: "remote-adult",
		Operation: OperationPlaybackAuthorize, LeaseScope: CircleScopeMediaStream,
		NegotiatedRevision: CurrentRevision, GuestServerSPKIPin: "spki-1", MediaID: "media-archive",
	}); !errors.Is(err, ErrMediaIneligible) {
		t.Fatalf("unrevealed restricted playback = %v", err)
	}
	if _, err := st.RebindShadowPrincipal(ctx, store.BindShadowPrincipalInput{
		CircleID: "circle-1", RemoteProfileID: "remote-adult", RemoteProfileBinding: "binding-adult",
		PolicyProfileID: "profile-default", ActorProfileID: "profile-default", Revealed: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CircleSession(ctx, "circle-1", "session-1"); !errors.Is(err, store.ErrCircleSessionNotFound) {
		t.Fatalf("rebind retained old principal session %q: %v", principal.ID, err)
	}
	if _, err := st.RevokeCircle(ctx, "circle-1", "profile-default", now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.OpenSession(ctx, "circle-1", "remote-adult", "session-2", CurrentRevision, "spki-1"); !errors.Is(err, ErrPrincipalUnresolved) {
		t.Fatalf("revoked circle reopened = %v", err)
	}
}

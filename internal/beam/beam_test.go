package beam

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestOfferClaimConsumesOnceForSameProfile(t *testing.T) {
	st, authz, service, now := newTestService(t)
	ctx := context.Background()
	source := mustScope(t, authz, "session-default", "device-tv", "dev-tv-secret")
	target := mustScope(t, authz, "session-manager", "device-mac", "dev-manager-secret")

	offer, err := service.CreateOffer(ctx, source, OfferRequest{MediaID: "media-alpha", PositionSeconds: 42, TargetDeviceID: "device-mac"})
	if err != nil {
		t.Fatalf("CreateOffer returned error: %v", err)
	}
	claim, err := service.ClaimOffer(ctx, target, ClaimRequest{HandoffToken: offer.HandoffToken})
	if err != nil {
		t.Fatalf("ClaimOffer returned error: %v", err)
	}
	if claim.MediaID != "media-alpha" || claim.PositionSeconds != 42 || claim.SourceSessionID != "session-default" {
		t.Fatalf("claim = %#v, want media-alpha at 42 from session-default", claim)
	}
	if _, err := service.ClaimOffer(ctx, target, ClaimRequest{HandoffToken: offer.HandoffToken}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("replay error = %v, want ErrInvalidToken", err)
	}
	if _, err := st.ConsumeBeamHandoffOffer(ctx, tokenHash(offer.HandoffToken), now().Add(time.Second)); !errors.Is(err, store.ErrBeamHandoffInvalid) {
		t.Fatalf("store replay error = %v, want ErrBeamHandoffInvalid", err)
	}
}

func TestOfferClaimCarriesMediaClassAndSenderReveal(t *testing.T) {
	_, authz, service, _ := newTestService(t)
	ctx := context.Background()

	// Unrevealed offer of a non-restricted Media: senderRevealed is false and the
	// resolved Media class is echoed back on both offer and claim.
	source := mustScope(t, authz, "session-default", "device-tv", "dev-tv-secret")
	target := mustScope(t, authz, "session-manager", "device-mac", "dev-manager-secret")
	// Client-supplied hints are ignored; the server derives both authoritatively.
	offer, err := service.CreateOffer(ctx, source, OfferRequest{
		MediaID: "media-alpha", PositionSeconds: 7, TargetDeviceID: "device-mac",
		MediaClass: "spoofed", SenderRevealed: true,
	})
	if err != nil {
		t.Fatalf("CreateOffer returned error: %v", err)
	}
	if offer.MediaClass == "spoofed" {
		t.Fatalf("offer.MediaClass = %q, want server-derived class not client spoof", offer.MediaClass)
	}
	if offer.SenderRevealed {
		t.Fatalf("offer.SenderRevealed = true for unrevealed session, want false")
	}
	claim, err := service.ClaimOffer(ctx, target, ClaimRequest{HandoffToken: offer.HandoffToken})
	if err != nil {
		t.Fatalf("ClaimOffer returned error: %v", err)
	}
	if claim.MediaClass != offer.MediaClass {
		t.Fatalf("claim.MediaClass = %q, want %q (offer→claim fidelity)", claim.MediaClass, offer.MediaClass)
	}
	if claim.SenderRevealed {
		t.Fatalf("claim.SenderRevealed = true, want false")
	}

	// A revealed session's offer derives senderRevealed=true from scope.Revealed
	// (the offer→claim persistence of this flag is covered by the store test).
	revealed := mustScope(t, authz, "session-revealed", "device-tv", "dev-tv-secret")
	rOffer, err := service.CreateOffer(ctx, revealed, OfferRequest{MediaID: "media-archive", PositionSeconds: 3, TargetDeviceID: "device-mac"})
	if err != nil {
		t.Fatalf("revealed CreateOffer returned error: %v", err)
	}
	if !rOffer.SenderRevealed {
		t.Fatalf("revealed offer.SenderRevealed = false, want true")
	}
}

func TestClaimRequiresSameProfileAndTargetDevice(t *testing.T) {
	_, authz, service, _ := newTestService(t)
	ctx := context.Background()
	source := mustScope(t, authz, "session-default", "device-tv", "dev-tv-secret")
	target := mustScope(t, authz, "session-managed", "device-tv", "dev-tv-secret")

	offer, err := service.CreateOffer(ctx, source, OfferRequest{MediaID: "media-alpha", PositionSeconds: 12, TargetDeviceID: "device-mac"})
	if err != nil {
		t.Fatalf("CreateOffer returned error: %v", err)
	}
	if _, err := service.ClaimOffer(ctx, target, ClaimRequest{HandoffToken: offer.HandoffToken}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("cross-profile/wrong-target error = %v, want ErrInvalidToken", err)
	}
}

func TestRestrictedOfferRequiresRevealAndSourceRevealAtClaim(t *testing.T) {
	_, authz, service, _ := newTestService(t)
	ctx := context.Background()
	unrevealed := mustScope(t, authz, "session-default", "device-tv", "dev-tv-secret")
	if _, err := service.CreateOffer(ctx, unrevealed, OfferRequest{MediaID: "media-archive", PositionSeconds: 9}); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("unrevealed restricted offer error = %v, want ErrMediaUnavailable", err)
	}

	revealed := mustScope(t, authz, "session-revealed", "device-tv", "dev-tv-secret")
	offer, err := service.CreateOffer(ctx, revealed, OfferRequest{MediaID: "media-archive", PositionSeconds: 9, TargetDeviceID: "device-mac"})
	if err != nil {
		t.Fatalf("revealed CreateOffer returned error: %v", err)
	}
	if err := service.store.SetSessionRevealed(ctx, "session-revealed", false); err != nil {
		t.Fatalf("SetSessionRevealed returned error: %v", err)
	}
	target := mustScope(t, authz, "session-manager", "device-mac", "dev-manager-secret")
	if _, err := service.ClaimOffer(ctx, target, ClaimRequest{HandoffToken: offer.HandoffToken}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("source relock claim error = %v, want ErrInvalidToken", err)
	}
}

func newTestService(t *testing.T) (*store.Store, *auth.Service, *Service, func() time.Time) {
	t.Helper()
	st := storetest.Open(t)
	nowValue := time.Unix(3000, 0).UTC()
	now := func() time.Time { return nowValue }
	authz := auth.NewServiceWithClock(st, now)
	return st, authz, NewService(st, authz, now), now
}

func mustScope(t *testing.T, authz *auth.Service, sessionID store.SessionID, deviceID store.DeviceID, secret string) auth.Scope {
	t.Helper()
	scope, err := authz.ScopeWithDeviceCredential(context.Background(), sessionID, deviceID, secret)
	if err != nil {
		t.Fatalf("resolve scope %s: %v", sessionID, err)
	}
	return scope
}

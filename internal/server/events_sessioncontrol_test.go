package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// authorizeManaged drives a managed-Profile (profile-managed) session over the
// SSE stream — a DIFFERENT Profile from the profile-default viewer — so the
// cross-Profile delivery boundary can be exercised. session-managed is seeded
// on device-tv (sqlite.go).
func authorizeManaged(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-managed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

// TestEventStreamDeliversSessionControlSameProfileOnly is the AV-3 subscribe
// acceptance: a cross-device transport command published
// against device A's session reaches a SAME-Profile subscriber over the existing
// /api/events SSE, but never a DIFFERENT-Profile subscriber. This is the
// load-bearing same-Profile boundary on the delivery channel.
//
// feature:cross-device-session-control
func TestEventStreamDeliversSessionControlSameProfileOnly(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	now := time.Unix(0, 0).UTC()

	// A transport command targeting session-default (profile-default), playing
	// the open-library media-alpha — published exactly as the controlSession
	// mutation publishes it.
	if err := st.AppendSessionControlEvent(t.Context(), "session-default", store.SessionControlPayload{
		Command:    "pause",
		FromDevice: "device-mac",
		ProfileID:  "profile-default",
		MediaID:    "media-alpha",
	}, now); err != nil {
		t.Fatalf("AppendSessionControlEvent: %v", err)
	}

	// A same-Profile subscriber (profile-default) receives the command.
	sameProfile := collectSSE(t, srv, authorizeViewer)
	if !strings.Contains(sameProfile, "event: "+store.SessionControlKind) {
		t.Fatalf("same-Profile subscriber missing the session-control event:\n%s", sameProfile)
	}
	if !strings.Contains(sameProfile, "session-default") || !strings.Contains(sameProfile, "pause") {
		t.Fatalf("same-Profile subscriber missing target/command:\n%s", sameProfile)
	}

	// A different-Profile subscriber (profile-managed) never receives it — the
	// command never crosses the Profile boundary.
	crossProfile := collectSSE(t, srv, authorizeManaged)
	if strings.Contains(crossProfile, store.SessionControlKind) {
		t.Fatalf("cross-Profile subscriber leaked a session-control event:\n%s", crossProfile)
	}
}

// TestEventStreamSessionControlRevealFiltered is the AV-3 subscribe Reveal
// acceptance (ADR-0035 §"Security surface"): a transport command for a session
// playing restricted Media is reveal-filtered on the delivery channel exactly
// like a media event — an unrevealed same-Profile subscriber never sees it, a
// revealed one does. So the transport channel is not a reveal side-channel.
//
// feature:cross-device-session-control
func TestEventStreamSessionControlRevealFiltered(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	now := time.Unix(0, 0).UTC()

	// A command naming restricted media-archive (library-archive), same Profile.
	if err := st.AppendSessionControlEvent(t.Context(), "session-default", store.SessionControlPayload{
		Command:   "skip-media",
		ProfileID: "profile-default",
		MediaID:   "media-archive",
	}, now); err != nil {
		t.Fatalf("AppendSessionControlEvent: %v", err)
	}

	// Unrevealed same-Profile subscriber: filtered out (no reveal side-channel).
	unrevealed := collectSSE(t, srv, authorizeViewer)
	if strings.Contains(unrevealed, store.SessionControlKind) {
		t.Fatalf("unrevealed subscriber leaked a restricted-media session-control event:\n%s", unrevealed)
	}

	// Revealed same-Profile subscriber: receives it (proves the absence above is a
	// real reveal filter, not a dropped-everywhere event).
	revealed := collectSSE(t, srv, authorizeRevealed)
	if !strings.Contains(revealed, "event: "+store.SessionControlKind) {
		t.Fatalf("revealed subscriber missing the restricted-media session-control event:\n%s", revealed)
	}
}

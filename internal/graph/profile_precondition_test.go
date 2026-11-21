package graph

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// The handler resolves the session again after the route's precondition
// passed. A Profile switch that lands in between must still refuse the write
// the request made for the old Profile, and write to neither (CORE-2923).
func TestStaleProfileHeaderRefusedAtGraphScopeResolution(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	if err := st.SetSessionProfile(t.Context(), "session-default", "profile-managed"); err != nil {
		t.Fatalf("SetSessionProfile: %v", err)
	}
	stale := func(req *http.Request) {
		authorizeViewer(req)
		req.Header.Set(auth.ProfileHeader, "profile-default")
	}
	rec := postGraphQL(t, handler, stale,
		`mutation { setClientPref(key: "web.poster-size", value: "small") { key } }`, nil)
	if rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), `"code":"CONFLICT"`) ||
		!strings.Contains(rec.Body.String(), `"reason":"PROFILE_MISMATCH"`) {
		t.Fatalf("stale write = %d %s, want 409 CONFLICT / PROFILE_MISMATCH", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	for _, profile := range []store.ProfileID{"profile-default", "profile-managed"} {
		prefs, err := st.ClientPrefs(t.Context(), profile)
		if err != nil || len(prefs) != 0 {
			t.Fatalf("ClientPrefs(%s) = %+v, %v after a refused write, want none", profile, prefs, err)
		}
	}
}

// A Reveal whose PIN was verified for one Profile, or a player's cleanup for
// one, must not apply after another tab switched the session to another
// Profile between the request's scope and its write (CORE-2923).
func TestScopeResolvedBeforeSwitchCannotRevealOrEndPlaybackAfterIt(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	ctx := t.Context()
	if err := st.SetProfileRevealPIN(ctx, "profile-default", "2468"); err != nil {
		t.Fatalf("SetProfileRevealPIN: %v", err)
	}
	scope := auth.Scope{SessionID: "session-default", ProfileID: "profile-default"}
	if err := st.SetSessionProfile(ctx, "session-default", "profile-managed"); err != nil {
		t.Fatalf("SetSessionProfile: %v", err)
	}

	_, err := h.revealOps(ctx, scope, map[string]bool{"reveal": true},
		request{Variables: map[string]any{"pin": "2468"}})
	if !errors.Is(err, errProfileMismatch) {
		t.Fatalf("reveal err = %v, want the Profile mismatch", err)
	}
	session, err := st.Session(ctx, "session-default")
	if err != nil || session.Revealed {
		t.Fatalf("session after a refused reveal = %+v, %v; want it still locked", session, err)
	}

	err = h.endPlayback(ctx, scope, request{Variables: map[string]any{"mediaId": "media-alpha"}})
	if !errors.Is(err, errProfileMismatch) {
		t.Fatalf("endPlayback err = %v, want the Profile mismatch", err)
	}
}

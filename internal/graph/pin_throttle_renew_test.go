package graph

import (
	"net/http"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// renewTVSession replaces device-tv's session the way session renewal does and
// returns a request authorizer for the new session.
func renewTVSession(t *testing.T, st *store.Store, newID store.SessionID) func(*http.Request) {
	t.Helper()
	session, err := st.RenewDeviceSession(t.Context(), "device-tv", newID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != newID {
		t.Fatalf("renewal returned session %q, want a new session %q", session.ID, newID)
	}
	return func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", string(newID))
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}
}

// A device that spent its free entry-PIN guesses must stay throttled after it
// renews into a new session (ADR-0012 §3, CORE-2108).
func TestProfileEntryThrottleSurvivesSessionRenewal(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	setBody := `{"query":"mutation U($profileID: String!, $entryPIN: String!){ updateProfile(profileID:$profileID, entryPIN:$entryPIN){ id } }","variables":{"profileID":"profile-default","entryPIN":"2468"}}`
	if _, errs := post(t, h, setBody, authorizeManager); len(errs) != 0 {
		t.Fatalf("set entry PIN errors = %#v", errs)
	}
	switchBody := func(pin string) string {
		return `{"query":"mutation S($profileID: String!, $pin: String){ switchProfile(profileID:$profileID, pin:$pin){ profileID } }","variables":{"profileID":"profile-default","pin":"` + pin + `"}}`
	}
	for i := 0; i < profileEntryBackoff.FreeAttempts; i++ {
		if _, errs := post(t, h, switchBody("0000"), authorizeManaged); len(errs) != 1 || errs[0].Message != "profile PIN required" {
			t.Fatalf("wrong PIN %d errors = %#v", i, errs)
		}
	}
	if _, errs := post(t, h, switchBody("2468"), authorizeManaged); len(errs) != 1 || errs[0].Message != "profile PIN attempts throttled" {
		t.Fatalf("same-session errors = %#v, want throttled", errs)
	}
	renewed := renewTVSession(t, st, "session-renewed")
	if _, errs := post(t, h, switchBody("2468"), renewed); len(errs) != 1 || errs[0].Message != "profile PIN attempts throttled" {
		t.Fatalf("after renewal errors = %#v, want throttled", errs)
	}
}

// The Reveal PIN lane must likewise stay throttled across a session renewal.
func TestRevealThrottleSurvivesSessionRenewal(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	revealBody := func(pin string) string {
		return `{"query":"mutation R($pin: String!){ reveal(pin:$pin){ revealed } }","variables":{"pin":"` + pin + `"}}`
	}
	for i := 0; i < revealBackoff.FreeAttempts; i++ {
		if _, errs := post(t, h, revealBody("0000"), authorizeViewer); len(errs) != 1 || errs[0].Message != "reveal PIN mismatch" {
			t.Fatalf("wrong PIN %d errors = %#v", i, errs)
		}
	}
	renewed := renewTVSession(t, st, "session-renewed")
	session, err := st.Session(t.Context(), "session-renewed")
	if err != nil {
		t.Fatal(err)
	}
	if session.ProfileID != "profile-default" {
		t.Fatalf("renewed session profile = %q, want profile-default", session.ProfileID)
	}
	if _, errs := post(t, h, revealBody("1234"), renewed); len(errs) != 1 || errs[0].Message != "reveal attempts throttled" {
		t.Fatalf("after renewal errors = %#v, want throttled", errs)
	}
}

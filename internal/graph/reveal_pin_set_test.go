package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestRevealPinSetTracksTheSessionProfilesRevealPIN covers CORE-2462: clients
// read revealPinSet to hide a Restrictions PIN prompt that cannot succeed.
func TestRevealPinSetTracksTheSessionProfilesRevealPIN(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	post := func(body string, authorize func(*http.Request)) []byte {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Body.Bytes()
	}
	currentPinSet := func(authorize func(*http.Request)) bool {
		t.Helper()
		var resp struct {
			Data struct {
				CurrentSession map[string]any `json:"currentSession"`
			} `json:"data"`
			Errors []struct{ Message string } `json:"errors"`
		}
		body := post(`{"query":"{ currentSession { revealed revealPinSet } }"}`, authorize)
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Errors) != 0 {
			t.Fatalf("currentSession errors = %#v", resp.Errors)
		}
		value, ok := resp.Data.CurrentSession["revealPinSet"].(bool)
		if !ok {
			t.Fatalf("currentSession = %s, want a boolean revealPinSet", body)
		}
		return value
	}
	managed := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-managed")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}

	// The seeded adult Profile carries the development reveal PIN.
	if !currentPinSet(authorizeViewer) {
		t.Fatal("seeded adult Profile with a reveal PIN reports revealPinSet=false")
	}
	// Managed Profiles never have a reveal path.
	if currentPinSet(managed) {
		t.Fatal("managed Profile reports revealPinSet=true")
	}

	// An adult Profile without a PIN reports false, so no client prompts for one.
	fresh, err := st.CreateProfile(ctx, "No PIN Adult", store.ProfileAdult, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionProfile(ctx, "session-default", fresh.ID); err != nil {
		t.Fatal(err)
	}
	if currentPinSet(authorizeViewer) {
		t.Fatal("adult Profile without a reveal PIN reports revealPinSet=true")
	}
	var relock struct {
		Data struct {
			Relock RevealState `json:"relock"`
		} `json:"data"`
	}
	if err := json.Unmarshal(post(`{"query":"mutation { relock { revealed revealPinSet } }"}`, authorizeViewer), &relock); err != nil {
		t.Fatal(err)
	}
	if relock.Data.Relock.RevealPinSet {
		t.Fatalf("relock without a PIN = %#v, want revealPinSet=false", relock.Data.Relock)
	}

	// Setting a PIN (Manage > Restrictions) flips it on, and a reveal agrees.
	if err := st.SetProfileRevealPIN(ctx, fresh.ID, "4321"); err != nil {
		t.Fatal(err)
	}
	if !currentPinSet(authorizeViewer) {
		t.Fatal("after setting a reveal PIN, currentSession reports revealPinSet=false")
	}
	var reveal struct {
		Data struct {
			Reveal RevealState `json:"reveal"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(post(`{"query":"mutation R($pin: String!) { reveal(pin: $pin) { revealed revealPinSet } }","variables":{"pin":"4321"}}`, authorizeViewer), &reveal); err != nil {
		t.Fatal(err)
	}
	if len(reveal.Errors) != 0 || !reveal.Data.Reveal.Revealed || !reveal.Data.Reveal.RevealPinSet {
		t.Fatalf("reveal = %#v errors = %#v", reveal.Data.Reveal, reveal.Errors)
	}
}

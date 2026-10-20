package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// feature:reveal-contract
//
// setRevealPin is the manager-only Reveal-PIN setter: it writes the
// per-Profile verifier every surface enforces. These wire tests prove the set
// PERSISTS (a later reveal with the new PIN succeeds; the old PIN no longer
// does), that only a MANAGER role may set it, and that the change is an
// idempotent overwrite of security-sensitive state.

// revealPinResp is a file-local decode envelope (uniquely prefixed to avoid
// collisions with sibling test files in this package).
type revealPinResp struct {
	Data struct {
		SetRevealPin *RevealState `json:"setRevealPin"`
		Reveal       *RevealState `json:"reveal"`
		Relock       *RevealState `json:"relock"`
	} `json:"data"`
	Errors []struct{ Message string } `json:"errors"`
}

// TestSetRevealPinPersistsAndGatesByRole drives setRevealPin end-to-end: a
// manager rotates the seeded dev PIN, the new verifier is proven stored by
// exercising reveal across a separate viewer session bound to the same Profile
// (old PIN rejected, new PIN reveals restricted content, wrong PIN throttles),
// and non-manager callers are refused with the real role error.
func TestSetRevealPinPersistsAndGatesByRole(t *testing.T) {
	handler := newTestHandler(t)

	post := func(body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	decode := func(rec *httptest.ResponseRecorder) revealPinResp {
		var resp revealPinResp
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
		}
		return resp
	}
	// authorizeManaged mirrors the file-local managed closure used elsewhere in
	// this package (admin_test.go) — session-managed → device-tv (VIEWER role)
	// → profile-managed (no reveal path). Kept inside this test so it cannot
	// collide with any sibling package-level helper.
	authorizeManaged := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-managed")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}

	setPin := func(pin string) string {
		return `{"query":"mutation SetPin($pin: String!) { setRevealPin(pin: $pin) { revealed } }","variables":{"pin":"` + pin + `"}}`
	}
	revealPin := func(pin string) string {
		return `{"query":"mutation Reveal($pin: String!) { reveal(pin: $pin) { revealed } }","variables":{"pin":"` + pin + `"}}`
	}

	const newPin = "7788"

	// (2) NEGATIVE AUTH: a VIEWER role cannot set the PIN. session-default and
	// session-managed both ride device-tv (VIEWER), so both hit the role gate.
	for name, authorize := range map[string]func(*http.Request){
		"viewer":  authorizeViewer,
		"managed": authorizeManaged,
	} {
		resp := decode(post(setPin(newPin), authorize))
		if len(resp.Errors) == 0 || resp.Errors[0].Message != "manager role required" {
			t.Fatalf("%s setRevealPin errors = %#v, want \"manager role required\"", name, resp.Errors)
		}
		if resp.Data.SetRevealPin != nil {
			t.Fatalf("%s setRevealPin returned data %#v despite role rejection", name, resp.Data.SetRevealPin)
		}
	}

	// Guard: the rejected sets must NOT have rotated the PIN. The seeded dev PIN
	// ("1234") still reveals on the viewer session, proving the verifier was
	// untouched by the denied calls above.
	resp := decode(post(revealPin("1234"), authorizeViewer))
	if len(resp.Errors) != 0 || resp.Data.Reveal == nil || !resp.Data.Reveal.Revealed {
		t.Fatalf("seeded-PIN reveal after denied sets = %#v errors = %#v, want reveal still works on old PIN", resp.Data.Reveal, resp.Errors)
	}
	// Relock so the persistence checks below start from a locked viewer session.
	if r := decode(post(`{"query":"mutation { relock { revealed } }"}`, authorizeViewer)); r.Data.Relock == nil || r.Data.Relock.Revealed {
		t.Fatalf("relock = %#v, want revealed=false", r.Data.Relock)
	}

	// (1) MANAGER sets a NEW PIN. session-manager → device-mac (MANAGER role) →
	// profile-default, the same Profile the viewer session is bound to.
	resp = decode(post(setPin(newPin), authorizeManager))
	if len(resp.Errors) != 0 || resp.Data.SetRevealPin == nil {
		t.Fatalf("manager setRevealPin = %#v errors = %#v, want success", resp.Data.SetRevealPin, resp.Errors)
	}

	// PERSISTENCE, proven by exercising reveal end-to-end on a DIFFERENT session
	// (viewer) bound to the same Profile:
	//   a) the OLD dev PIN is now REJECTED — the verifier was actually replaced,
	resp = decode(post(revealPin("1234"), authorizeViewer))
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "reveal PIN mismatch" {
		t.Fatalf("old-PIN reveal after rotation = %#v, want \"reveal PIN mismatch\"", resp.Errors)
	}
	//   b) the NEW PIN REVEALS and restricted media becomes searchable.
	resp = decode(post(revealPin(newPin), authorizeViewer))
	if len(resp.Errors) != 0 || resp.Data.Reveal == nil || !resp.Data.Reveal.Revealed {
		t.Fatalf("new-PIN reveal = %#v errors = %#v, want revealed", resp.Data.Reveal, resp.Errors)
	}
	searchBody := `{"query":"query Search($query: String!) { search(query: $query) { id } }","variables":{"query":"archive"}}`
	var searchResp struct {
		Data struct {
			Search []SourceItem `json:"search"`
		} `json:"data"`
	}
	rec := post(searchBody, authorizeViewer)
	if err := json.Unmarshal(rec.Body.Bytes(), &searchResp); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	if len(searchResp.Data.Search) != 1 {
		t.Fatalf("post-reveal search = %#v, want restricted media visible after rotated-PIN reveal", searchResp.Data.Search)
	}

	// REVEAL CONTRACT (locked vs revealed): relock hides the restricted media
	// again under the same rotated verifier.
	if r := decode(post(`{"query":"mutation { relock { revealed } }"}`, authorizeViewer)); r.Data.Relock == nil || r.Data.Relock.Revealed {
		t.Fatalf("relock = %#v, want revealed=false", r.Data.Relock)
	}
	rec = post(searchBody, authorizeViewer)
	if err := json.Unmarshal(rec.Body.Bytes(), &searchResp); err != nil {
		t.Fatalf("decode search after relock: %v", err)
	}
	if len(searchResp.Data.Search) != 0 {
		t.Fatalf("post-relock search = %#v, want restricted media hidden", searchResp.Data.Search)
	}

	// The rotated verifier also drives throttling: the free-attempt budget of
	// wrong attempts spends the lane so the next is throttled even with the
	// correct rotated PIN — proves the new verifier is the one being checked
	// (a stale/empty verifier could not produce the mismatch that spends budget).
	for i := 0; i < revealBackoff.FreeAttempts; i++ {
		post(revealPin("0000"), authorizeViewer)
	}
	resp = decode(post(revealPin(newPin), authorizeViewer))
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "reveal attempts throttled" {
		t.Fatalf("throttle after rotation = %#v, want \"reveal attempts throttled\"", resp.Errors)
	}
}

// TestSetRevealPinIsIdempotentOverwrite asserts the security-sensitive overwrite
// is idempotent: setting the same PIN twice succeeds both times, and a reveal
// with that PIN still works after the re-send (no lockout, no double-write
// corruption of the verifier).
func TestSetRevealPinIsIdempotentOverwrite(t *testing.T) {
	// feature:reveal-contract
	handler := newTestHandler(t)

	post := func(body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	decode := func(rec *httptest.ResponseRecorder) revealPinResp {
		var resp revealPinResp
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
		}
		return resp
	}
	setPin := func(pin string) string {
		return `{"query":"mutation SetPin($pin: String!) { setRevealPin(pin: $pin) { revealed } }","variables":{"pin":"` + pin + `"}}`
	}

	const pin = "5150"

	// First set and re-send both succeed (overwrite, not append).
	for attempt := 1; attempt <= 2; attempt++ {
		resp := decode(post(setPin(pin), authorizeManager))
		if len(resp.Errors) != 0 || resp.Data.SetRevealPin == nil {
			t.Fatalf("setRevealPin attempt %d = %#v errors = %#v, want success", attempt, resp.Data.SetRevealPin, resp.Errors)
		}
	}

	// After the idempotent re-send the PIN still reveals on the viewer session
	// bound to the same Profile — the second write left a valid verifier.
	resp := decode(post(`{"query":"mutation Reveal($pin: String!) { reveal(pin: $pin) { revealed } }","variables":{"pin":"`+pin+`"}}`, authorizeViewer))
	if len(resp.Errors) != 0 || resp.Data.Reveal == nil || !resp.Data.Reveal.Revealed {
		t.Fatalf("reveal after idempotent re-send = %#v errors = %#v, want revealed", resp.Data.Reveal, resp.Errors)
	}
}

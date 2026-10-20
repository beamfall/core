package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// ENR-6 / ADR-0048 Decision 2: three independent Profile-global auto-skip
// booleans (intro / recap / credits), default off, stored in the generic
// per-Profile preference store and exposed via the same clientPrefs /
// setClientPref graph operations every other client pref roams through (so they
// land in the per-Profile Snapshot). The server never acts on them — these
// tests assert storage + exposure only, never a playhead move.

// (a) The three prefs default off for a Profile that has set nothing.
func TestAutoSkipPrefsDefaultOff(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)

	prefs := h.autoSkipPrefs(context.Background(), "profile-default")
	if prefs.Intro || prefs.Recap || prefs.Credits {
		t.Fatalf("default auto-skip prefs = %+v, want all off", prefs)
	}
}

// (b) Set + read round-trips per kind, independently, through the store and the
// default-off reader. Any value other than "on" reads as off.
func TestAutoSkipPrefsRoundTripPerKind(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	ctx := context.Background()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

	if err := st.SetClientPref(ctx, "profile-default", autoSkipIntroKey, "on", now); err != nil {
		t.Fatalf("set intro: %v", err)
	}
	if err := st.SetClientPref(ctx, "profile-default", autoSkipCreditsKey, "on", now); err != nil {
		t.Fatalf("set credits: %v", err)
	}
	// recap left absent → off; an explicit non-"on" value also reads off.
	if err := st.SetClientPref(ctx, "profile-default", autoSkipRecapKey, "off", now); err != nil {
		t.Fatalf("set recap: %v", err)
	}

	prefs := h.autoSkipPrefs(ctx, "profile-default")
	if !prefs.Intro {
		t.Errorf("intro = false, want true")
	}
	if !prefs.Credits {
		t.Errorf("credits = false, want true")
	}
	if prefs.Recap {
		t.Errorf("recap = true, want false (non-\"on\" value reads off)")
	}

	// Clearing a key (empty value) returns the kind to its default-off.
	if err := st.SetClientPref(ctx, "profile-default", autoSkipIntroKey, "", now); err != nil {
		t.Fatalf("clear intro: %v", err)
	}
	if h.autoSkipPrefs(ctx, "profile-default").Intro {
		t.Errorf("after clear, intro = true, want false")
	}
}

// (d) A Profile only sets its own prefs: enabling auto-skip on one Profile does
// not leak into another.
func TestAutoSkipPrefsScopedPerProfile(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	ctx := context.Background()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

	if err := st.SetClientPref(ctx, "profile-default", autoSkipIntroKey, "on", now); err != nil {
		t.Fatalf("set on default: %v", err)
	}

	if other := h.autoSkipPrefs(ctx, "profile-managed"); other.Intro || other.Recap || other.Credits {
		t.Fatalf("profile-managed sees auto-skip prefs = %+v, want all off", other)
	}
}

// (c) The three prefs appear in the session Profile's clientPrefs query — the
// generic feed the tvOS/mobile Snapshot folds client-owned prefs out of.
// Exercised over the real GraphQL handler, set and read back via
// the same operations any other client pref uses; no dedicated field is added.
func TestAutoSkipPrefsSurfacedInClientPrefs(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	set := func(key, value string) {
		body := `{"query":"mutation S($key:String!,$value:String!){ setClientPref(key:$key,value:$value){ key } }","variables":{"key":"` + key + `","value":"` + value + `"}}`
		if rec := post(body); bytes.Contains(rec.Body.Bytes(), []byte("\"errors\"")) {
			t.Fatalf("setClientPref(%q) failed: %s", key, rec.Body.String())
		}
	}

	// Self-authored by the viewer session's own Profile (no reveal, no manager).
	set(autoSkipIntroKey, "on")
	set(autoSkipRecapKey, "on")
	set(autoSkipCreditsKey, "on")

	var listResp struct {
		Data struct {
			ClientPrefs []ClientPrefInfo `json:"clientPrefs"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	list := `{"query":"query { clientPrefs { key value } }"}`
	if err := json.Unmarshal(post(list).Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Errors) != 0 {
		t.Fatalf("clientPrefs errors = %#v", listResp.Errors)
	}

	got := map[string]string{}
	for _, p := range listResp.Data.ClientPrefs {
		got[p.Key] = p.Value
	}
	for _, key := range []string{autoSkipIntroKey, autoSkipRecapKey, autoSkipCreditsKey} {
		if got[key] != "on" {
			t.Errorf("clientPrefs missing %q=on (Snapshot feed); got value %q", key, got[key])
		}
	}

	// And the typed reader agrees with what the wire surfaced.
	prefs := handler.autoSkipPrefs(context.Background(), store.ProfileID("profile-default"))
	if !prefs.Intro || !prefs.Recap || !prefs.Credits {
		t.Fatalf("reader after wire set = %+v, want all on", prefs)
	}
}

// (e) ADR-0193 Decision 2/5 adds the FOURTH kind. autoskip.ad resolves exactly
// like the other three — default off, "on" means on, anything else off, keyed on
// the segment KIND alone (detection flavor never reaches a client and never
// gates auto-skip) — and it is independent of them.
func TestAutoSkipAdPrefResolvesLikeTheOthers(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	if h.autoSkipPrefs(ctx, "profile-default").Ad {
		t.Fatal("autoskip.ad defaults on, want off")
	}
	if err := st.SetClientPref(ctx, "profile-default", autoSkipAdKey, "on", now); err != nil {
		t.Fatalf("set ad: %v", err)
	}
	prefs := h.autoSkipPrefs(ctx, "profile-default")
	if !prefs.Ad {
		t.Fatal("autoskip.ad = false after setting \"on\"")
	}
	if prefs.Intro || prefs.Recap || prefs.Credits {
		t.Fatalf("enabling ad leaked into the other kinds: %+v", prefs)
	}
	if err := st.SetClientPref(ctx, "profile-default", autoSkipAdKey, "yes", now); err != nil {
		t.Fatalf("set ad to a non-\"on\" value: %v", err)
	}
	if h.autoSkipPrefs(ctx, "profile-default").Ad {
		t.Fatal("a non-\"on\" value read as enabled")
	}
	if err := st.SetClientPref(ctx, "profile-default", autoSkipAdKey, "", now); err != nil {
		t.Fatalf("clear ad: %v", err)
	}
	if h.autoSkipPrefs(ctx, "profile-default").Ad {
		t.Fatal("after clear, autoskip.ad = true, want off")
	}
}

// (f) `sponsor` is a NON-BINDING RESERVED PLACEHOLDER: the
// autoskip.sponsor <- autoskip.ad read-time fallback is a recorded CONTRACT, not
// code. The generic pref store accepts any key, so the existence of a stored
// autoskip.sponsor proves nothing; what is pinned here is that no resolution
// branch reads it, in either direction.
func TestSponsorAutoSkipKeyIsNotImplemented(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	ctx := context.Background()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	if err := st.SetClientPref(ctx, "profile-default", "autoskip.sponsor", "on", now); err != nil {
		t.Fatalf("set sponsor: %v", err)
	}
	prefs := h.autoSkipPrefs(ctx, "profile-default")
	if prefs.Intro || prefs.Recap || prefs.Credits || prefs.Ad {
		t.Fatalf("a stored autoskip.sponsor changed resolution: %+v — sponsor is reserved, not live", prefs)
	}

	// And the reserved fallback is not implemented in the other direction either:
	// enabling ad must not synthesize any sponsor behavior.
	if err := st.SetClientPref(ctx, "profile-default", autoSkipAdKey, "on", now); err != nil {
		t.Fatalf("set ad: %v", err)
	}
	if enabled := h.autoSkipPrefs(ctx, "profile-default"); !enabled.Ad || enabled.Intro || enabled.Recap || enabled.Credits {
		t.Fatalf("autoskip.ad resolution = %+v, want ad only", enabled)
	}
}

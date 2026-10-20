package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/config"
)

// TestServeActionLoopOverHTTP is the Phase-3 keystone: it proves the
// out-of-process action+hydration loop end to end. It boots a real
// conformance-seeded HTTP listener on an ephemeral port (the same boot path as
// the conformance golden test — app.New + go beamfall.Run + beamfall.Addr for
// readiness, authenticated with the seeded session/device headers), drives a
// MUTATING action (setClientPref) over real HTTP, and asserts the EFFECT by
// reading it back with a clientPrefs query over the wire — not in process. It
// then proves hydrate-per-scenario isolation: a SECOND boot on a fresh
// t.TempDir starts from clean seeded state, with scenario 1's mutation absent.
//
// setClientPref is chosen as the simplest mutating action the seeded
// session-revealed (profile-default) can perform without enrollment: it is
// viewer-allowed by design (the prefs belong to the session's own Profile), it
// requires no reveal PIN, and clientPrefs reads it straight back. The job loop
// runs on the wall clock here (a real serve), so this asserts only on
// state/IDs/values — never on timestamps.
func TestServeActionLoopOverHTTP(t *testing.T) {
	// Scenario 1: boot, mutate over HTTP, read the effect back over HTTP.
	addr1 := serveActionBootConformance(t)

	const (
		prefKey   = "web.subtitle-style"
		prefValue = `{"size":"large"}`
	)
	// The pref value is an opaque string the server never parses; escape it for
	// embedding in the GraphQL JSON variables.
	valueJSON, err := json.Marshal(prefValue)
	if err != nil {
		t.Fatal(err)
	}
	setBody := `{"query":"mutation SetClientPref($key: String!, $value: String!) { setClientPref(key: $key, value: $value) { key value updatedAt } }","variables":{"key":"` + prefKey + `","value":` + string(valueJSON) + `}}`

	var setResp struct {
		Data struct {
			SetClientPref struct {
				Key       string `json:"key"`
				Value     string `json:"value"`
				UpdatedAt string `json:"updatedAt"`
			} `json:"setClientPref"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	serveActionGraphQLJSON(t, addr1, setBody, &setResp)
	if len(setResp.Errors) != 0 {
		t.Fatalf("setClientPref over HTTP returned errors: %#v", setResp.Errors)
	}
	if setResp.Data.SetClientPref.Key != prefKey || setResp.Data.SetClientPref.Value != prefValue {
		t.Fatalf("setClientPref over HTTP = %#v, want key=%q value=%q", setResp.Data.SetClientPref, prefKey, prefValue)
	}
	// The mutation runs against the wall clock; assert it is non-empty, never a
	// specific timestamp.
	if setResp.Data.SetClientPref.UpdatedAt == "" {
		t.Fatalf("setClientPref over HTTP returned empty updatedAt")
	}

	// Assert the EFFECT by reading state back over the wire with a fresh query.
	prefs1 := serveActionListClientPrefs(t, addr1)
	if len(prefs1) != 1 {
		t.Fatalf("after mutation, clientPrefs over HTTP = %#v, want exactly 1 entry", prefs1)
	}
	if prefs1[0].Key != prefKey || prefs1[0].Value != prefValue {
		t.Fatalf("after mutation, clientPrefs[0] = %#v, want key=%q value=%q", prefs1[0], prefKey, prefValue)
	}

	// Scenario 2: hydrate-per-scenario isolation. A second server on a fresh
	// t.TempDir must boot from the clean conformance seed — scenario 1's
	// mutation lives only in scenario 1's data-dir and must be ABSENT here. This
	// is the leak/negative case for the hydration boundary.
	addr2 := serveActionBootConformance(t)
	if addr2 == addr1 {
		t.Fatalf("second server bound the same address %q as the first; the boots are not isolated", addr2)
	}
	prefs2 := serveActionListClientPrefs(t, addr2)
	if len(prefs2) != 0 {
		t.Fatalf("fresh boot leaked scenario-1 state: clientPrefs over HTTP = %#v, want empty (clean seed)", prefs2)
	}

	// Both servers tear down cleanly via their t.Cleanup (cancel + Run returns),
	// registered in serveActionBootConformance.
}

func TestServeRunStopsCleanlyOnContextCancel(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.ServerName = "CancelE2E"
	cfg.Discovery.Enabled = false
	cfg.SeedProfile = "conformance"
	cfg.AllowConformanceSeed = true

	beamfall, err := app.New(context.Background(), cfg, discardLogger(), firstPartyBundles(), firstPartyHooks())
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- beamfall.Run(runCtx) }()

	addrCtx, addrCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer addrCancel()
	if addr := beamfall.Addr(addrCtx); addr == "" {
		t.Fatal("server did not bind a listener within 5s")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop within 10s")
	}
}

// serveActionClientPref is the read-back shape for one stored client pref;
// file-local and uniquely prefixed so it cannot collide with sibling files in
// package main.
type serveActionClientPref struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updatedAt"`
}

// serveActionBootConformance boots a real conformance-seeded HTTP listener on
// an ephemeral port over a fresh t.TempDir data-dir and returns its bound
// address. It mirrors the conformance golden test's boot exactly (app.New + go
// beamfall.Run + beamfall.Addr readiness) and registers the cancel+Run-returns
// teardown via t.Cleanup. Each call is a fully isolated scenario.
func serveActionBootConformance(t *testing.T) string {
	t.Helper()
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0" // ephemeral port
	cfg.DataDir = t.TempDir()
	cfg.ServerName = "ActionE2E"
	cfg.Discovery.Enabled = false
	cfg.SeedProfile = "conformance"
	cfg.AllowConformanceSeed = true

	beamfall, err := app.New(context.Background(), cfg, discardLogger(), firstPartyBundles(), firstPartyHooks())
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- beamfall.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-runErr:
			if err != nil {
				t.Errorf("Run returned error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Run did not stop within 10s")
		}
	})

	addrCtx, addrCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer addrCancel()
	addr := beamfall.Addr(addrCtx)
	if addr == "" {
		t.Fatal("server did not bind a listener within 5s")
	}
	return addr
}

// serveActionGraphQLJSON posts a GraphQL document to /api/graphql over real
// HTTP, authenticating with the conformance seed's revealed session/device
// headers (the same credentials the conformance golden test uses for
// playback-info), and decodes the response into out. profile-default's
// session-revealed can perform the viewer-allowed setClientPref mutation and
// the clientPrefs query.
func serveActionGraphQLJSON(t *testing.T, addr, body string, out any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/graphql", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Beamfall-Session", "session-revealed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("graphql request: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("graphql status %d: %s", resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode graphql response: %v\n%s", err, raw)
	}
}

// serveActionListClientPrefs reads the session Profile's stored client prefs
// back over real HTTP via the clientPrefs query, asserting no GraphQL errors,
// and returns the list. Used both to confirm scenario 1's mutation effect and
// to confirm scenario 2 boots from a clean seed.
func serveActionListClientPrefs(t *testing.T, addr string) []serveActionClientPref {
	t.Helper()
	const list = `{"query":"query ClientPrefs { clientPrefs { key value updatedAt } }"}`
	var resp struct {
		Data struct {
			ClientPrefs []serveActionClientPref `json:"clientPrefs"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	serveActionGraphQLJSON(t, addr, list, &resp)
	if len(resp.Errors) != 0 {
		t.Fatalf("clientPrefs over HTTP returned errors: %#v", resp.Errors)
	}
	return resp.Data.ClientPrefs
}

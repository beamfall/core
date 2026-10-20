package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/config"
)

// TestServeConformanceServesCorpusGoldenOverHTTP boots a real HTTP listener on
// an ephemeral port over the conformance-seeded corpus world (fixed clock +
// fixed Stream-Token key) and asserts the playback-info response matches the
// committed corpus golden byte-identically. This is the e2e enabler: it proves
// `beamfall serve --seed=conformance` serves the deterministic corpus world
// over the wire, not just in-process. The playback-info surface is chosen
// because it is plugin-independent and fully pinned by the fixed clock + key
// (its golden is captured un-normalized, unlike the manifest's generatedAt).
func TestServeConformanceServesCorpusGoldenOverHTTP(t *testing.T) {
	addr := bootConformanceCorpusServer(t)

	// The revealed-adult context: session-revealed on device-tv, requesting
	// playback-info for media-alpha (mirrors the corpus test's contexts).
	body := `{"mediaID":"media-alpha","capabilityProfile":{"client":"conformance","containers":["mp4"],"directCodecs":["h264"]}}`
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/playback-info", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Beamfall-Session", "session-revealed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("playback-info request: %v", err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("playback-info status %d: %s", resp.StatusCode, got)
	}

	// The committed corpus golden is the contract source of truth; the live HTTP
	// response must match its semantic content. The golden is pretty-printed and
	// key-sorted (json.MarshalIndent); the live handler emits compact,
	// insertion-order JSON. Comparing canonicalized forms proves byte-equal
	// content without coupling to whitespace/key-order encoding differences.
	goldenPath := filepath.Join("..", "..", "internal", "conformance", "testdata", "corpus", "revealed-adult", "playback-info.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read corpus golden: %v", err)
	}
	if gotCanon, wantCanon := canonicalJSON(t, got), canonicalJSON(t, want); gotCanon != wantCanon {
		t.Fatalf("playback-info over HTTP diverged from the corpus golden.\n got: %s\nwant: %s", gotCanon, wantCanon)
	}
}

// TestServeConformanceServesManifestGoldenOverHTTP is the manifest sibling of
// the playback-info e2e: it boots the conformance corpus over a real HTTP
// listener and asserts GET /api/manifest matches the committed revealed-adult
// Navigation Manifest golden. Unlike playback-info the manifest carries a
// volatile generatedAt, so the live response is normalized the same way the
// in-process corpus capture normalizes it (normalizeManifestMap stamps a fixed
// epoch) before comparison; the committed golden is already stored normalized.
// This extends the over-HTTP contract proof from playback-info to the
// Navigation Manifest surface clients decode at launch.
func TestServeConformanceServesManifestGoldenOverHTTP(t *testing.T) {
	addr := bootConformanceCorpusServer(t)

	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/api/manifest", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Beamfall-Session", "session-revealed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("manifest request: %v", err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("manifest status %d: %s", resp.StatusCode, got)
	}

	goldenPath := filepath.Join("..", "..", "internal", "conformance", "testdata", "corpus", "revealed-adult", "manifest.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read corpus manifest golden: %v", err)
	}
	// Mirror normalizeManifestMap: the only volatile field is generatedAt. The
	// golden is already stored with the fixed epoch, so stamping the live
	// response makes the two canonicalize equal iff their content matches.
	gotCanon := canonicalManifest(t, got, true)
	wantCanon := canonicalManifest(t, want, false)
	if gotCanon != wantCanon {
		t.Fatalf("manifest over HTTP diverged from the corpus golden.\n got: %s\nwant: %s", gotCanon, wantCanon)
	}
}

// serveGraphQuery is the corpus graph-contract query (mirrors
// internal/conformance corpusGraphQuery), exercising the SessionInfo +
// SourceItem resolvers over the GraphQL POST path.
const serveGraphQuery = `{"query":"query Contract($kind: String!) { currentSession { id deviceRole profileID profile revealed } sourceItems(source: { kind: $kind }) { id kind title restricted } }","variables":{"kind":"recently-added"}}`

// TestServeConformanceGraphQLServesRevealedSessionOverHTTP drives POST
// /api/graphql over a real socket in the serve regime — a code path no other
// test covers (the graph goldens run in-process via httptest with a synthetic
// plugin set, and the other over-HTTP checks only hit the REST endpoints). It
// asserts only the plugin- and artwork-INDEPENDENT invariants, so it is robust
// to the serve regime's first-party bundles + content-hash artwork tokens
// (which is exactly why the corpus graph golden cannot be diffed here): the
// revealed-adult session resolves with revealed=true and its Profile binding,
// and reveal-filtering passes the restricted media-archive item alongside the
// unrestricted media-alpha through the GraphQL path over the wire.
func TestServeConformanceGraphQLServesRevealedSessionOverHTTP(t *testing.T) {
	addr := bootConformanceCorpusServer(t)

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/graphql", strings.NewReader(serveGraphQuery))
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("graphql status %d: %s", resp.StatusCode, body)
	}

	var doc struct {
		Data struct {
			CurrentSession struct {
				ProfileID string `json:"profileID"`
				Revealed  bool   `json:"revealed"`
			} `json:"currentSession"`
			SourceItems []struct {
				ID string `json:"id"`
				// *bool distinguishes "restricted":false from an absent key
				// (FIDELITY-IOS-REVEAL-QA: omitempty => absent means false).
				Restricted *bool `json:"restricted"`
			} `json:"sourceItems"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("graphql response is not valid JSON: %v\n%s", err, body)
	}
	if !doc.Data.CurrentSession.Revealed {
		t.Fatal("revealed-adult session resolved revealed=false over HTTP")
	}
	if doc.Data.CurrentSession.ProfileID != "profile-default" {
		t.Fatalf("currentSession.profileID = %q, want profile-default", doc.Data.CurrentSession.ProfileID)
	}
	ids := map[string]bool{}
	restrictedByID := map[string]*bool{}
	for _, item := range doc.Data.SourceItems {
		ids[item.ID] = true
		restrictedByID[item.ID] = item.Restricted
	}
	// A revealed session must see BOTH the restricted (media-archive) and the
	// unrestricted (media-alpha) item — proving reveal-filtering travels the
	// GraphQL path over the wire, not just the in-process resolver tests.
	if !ids["media-archive"] || !ids["media-alpha"] {
		t.Fatalf("revealed sourceItems over HTTP = %v, want both media-archive and media-alpha", doc.Data.SourceItems)
	}
	// FIDELITY-IOS-REVEAL-QA: the per-item restricted flag drives the iOS
	// per-poster tag. To a REVEALED session it IS served — the restricted-Library
	// item carries restricted=true; the unrestricted item omits the key
	// (omitempty => absent means false). Positive half of the privacy contract:
	// the flag reaches only the authorized revealed session.
	if r := restrictedByID["media-archive"]; r == nil {
		t.Fatal("revealed media-archive restricted flag absent, want true")
	} else if !*r {
		t.Fatal("revealed media-archive restricted flag = false, want true")
	}
	if r := restrictedByID["media-alpha"]; r != nil && *r {
		t.Fatalf("revealed media-alpha restricted flag = true, want absent/false")
	}
}

// TestServeConformanceGraphQLHidesRestrictedFromUnrevealedOverHTTP is the
// negative reveal case of the test above: the locked-adult context
// (session-default, unrevealed) drives POST /api/graphql over a real socket and
// must NOT receive the restricted media-archive item, while still seeing the
// unrestricted media-alpha. Together the two pin the reveal boundary over the
// GraphQL path in both directions; this one proves restricted content never
// leaks to an unrevealed session over the wire. Like its sibling it asserts
// only plugin/artwork-independent fields, so it is robust to the serve regime.
func TestServeConformanceGraphQLHidesRestrictedFromUnrevealedOverHTTP(t *testing.T) {
	addr := bootConformanceCorpusServer(t)

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/graphql", strings.NewReader(serveGraphQuery))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Beamfall-Session", "session-default")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("graphql request: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("graphql status %d: %s", resp.StatusCode, body)
	}

	var doc struct {
		Data struct {
			CurrentSession struct {
				Revealed bool `json:"revealed"`
			} `json:"currentSession"`
			// Raw items so we can assert the "restricted" KEY is absent, not
			// merely false — the locked wire must carry no restricted signal.
			SourceItems []json.RawMessage `json:"sourceItems"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("graphql response is not valid JSON: %v\n%s", err, body)
	}
	if doc.Data.CurrentSession.Revealed {
		t.Fatal("locked-adult session resolved revealed=true over HTTP")
	}
	ids := map[string]bool{}
	for _, raw := range doc.Data.SourceItems {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("sourceItems element is not an object: %v\n%s", err, raw)
		}
		// FIDELITY-IOS-REVEAL-QA hard privacy invariant (ADR-0004 #1/#7): an
		// unrevealed session must NOT receive the per-item restricted flag on ANY
		// item — even though the query selects it. Its presence (even false) would
		// leak count/placement of restricted titles to an unauthorized viewer. The
		// key must be entirely absent on the locked wire.
		if _, ok := item["restricted"]; ok {
			t.Fatalf("restricted flag leaked to an unrevealed session over HTTP on item %s: %s", item["id"], raw)
		}
		var id string
		if err := json.Unmarshal(item["id"], &id); err == nil {
			ids[id] = true
		}
	}
	if ids["media-archive"] {
		t.Fatalf("restricted media-archive leaked to an unrevealed session over HTTP: %v", ids)
	}
	if !ids["media-alpha"] {
		t.Fatalf("unrevealed sourceItems over HTTP = %v, want the unrestricted media-alpha", ids)
	}
}

// bootConformanceCorpusServer starts `beamfall serve --seed=conformance` on an
// ephemeral port over the deterministic corpus world (fixed clock + fixed
// Stream-Token key) and returns its bound address. The server is stopped on
// test cleanup.
func bootConformanceCorpusServer(t *testing.T) string {
	t.Helper()
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0" // ephemeral port
	cfg.DataDir = t.TempDir()
	cfg.ServerName = "Conformance"
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

// canonicalManifest unmarshals a Navigation Manifest, optionally stamps the
// volatile generatedAt with the fixed epoch (matching normalizeManifestMap),
// and re-marshals into a stable key-sorted form for content comparison.
func canonicalManifest(t *testing.T, data []byte, stampGeneratedAt bool) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid manifest JSON: %v\n%s", err, data)
	}
	if stampGeneratedAt {
		doc["generatedAt"] = "1970-01-01T00:00:00Z"
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// canonicalJSON re-marshals JSON into a stable, key-sorted form so two
// encodings of the same content (compact vs. pretty-printed) compare equal.
func canonicalJSON(t *testing.T, data []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

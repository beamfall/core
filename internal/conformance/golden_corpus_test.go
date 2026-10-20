package conformance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/beam"
	"github.com/beamfall/core/internal/genassets"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/server"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// The contract corpus is the versioned, multi-context superset of the three
// standalone goldens (manifest / graph / playback-info). One seeded world,
// composed on a fixed clock and a fixed token key so every byte is
// deterministic, is exercised across every meaningful seeded session context
// and the JSON wire surfaces a client renders. The result pins the
// reveal-filtered shape per (context × surface), and the whole tree is hashed
// into corpus.version, the core-side source of truth. Client repos do not copy
// this corpus tree (the never-functional `publish-corpus` propagation was
// removed 2026-06-16); instead each client vendors byte-identical copies of the
// three standalone goldens and gates them against internal/conformance/testdata/
// goldens.sha256 (emitted by `go run ./tools/golden-hashes`, pinned by
// TestGoldenHashes) — so server↔client contract drift cannot merge undetected.
//
// This is the contract-as-single-source-of-truth layer. The standalone goldens remain for
// now because the client repos still reference them directly; they will be
// subsumed by the corpus in Phase 2.
//
// Regenerate: BEAMFALL_UPDATE_GOLDEN=1 go test ./internal/conformance -run TestGoldenCorpus

const (
	corpusDir          = "testdata/corpus"
	corpusVersionPath  = "testdata/corpus/corpus.version"
	corpusManifestPath = "testdata/corpus/corpus.manifest.json"
)

// corpusContext is one seeded authorization context. The four chosen contexts
// span the reveal contract's core states (locked adult, revealed adult, managed
// child) plus the manager Device Role. The revoked-device session is omitted —
// it is rejected at auth, so it carries no renderable surface (its negative
// behavior belongs in an auth test, not the corpus).
type corpusContext struct {
	name         string // directory under corpus/
	session      string
	device       string
	deviceSecret string
}

var corpusContexts = []corpusContext{
	{"locked-adult", "session-default", "device-tv", "dev-tv-secret"},
	{"revealed-adult", "session-revealed", "device-tv", "dev-tv-secret"},
	{"managed-child", "session-managed", "device-tv", "dev-tv-secret"},
	{"manager", "session-manager", "device-mac", "dev-manager-secret"},
}

// corpusSurface is one JSON wire surface captured per context.
type corpusSurface struct {
	file      string // file under corpus/<context>/
	method    string
	path      string
	body      string                              // empty for GET
	normalize func(map[string]any) map[string]any // pin volatile fields; nil = none
}

// The graph query mirrors TestGoldenGraphResponse verbatim so the corpus pins
// the same SessionInfo / PluginInfo / SourceItem shapes clients decode.
const corpusGraphQuery = `{"query":"query Contract($kind: String!) { currentSession { id deviceRole profileID profile revealed } plugins { id displayName mediaLabel mediaClassLabels } sourceItems(source: { kind: $kind }) { id kind title summary artwork { poster } artworkSlots } }","variables":{"kind":"recently-added"}}`

// media-alpha lives in library-local, which every context (including the
// managed child) can access, so playback-info resolves in all four — the
// difference across contexts is the session-bound Stream Token, which the
// fixed clock + fixed key make deterministic.
const corpusPlaybackBody = `{"mediaID":"media-alpha","capabilityProfile":{"client":"conformance","containers":["mp4"],"directCodecs":["h264"]}}`

func corpusSurfaces() []corpusSurface {
	return []corpusSurface{
		{"manifest.json", http.MethodGet, "/api/manifest", "", normalizeManifestMap},
		{"graph-contract.json", http.MethodPost, "/api/graphql", corpusGraphQuery, normalizeGraphMap},
		{"playback-info.json", http.MethodPost, "/api/playback-info", corpusPlaybackBody, nil},
	}
}

func TestGoldenCorpus(t *testing.T) {
	// Proves the server emits the correct, reveal-filtered wire contract for:
	//   feature:navigation-manifest feature:section-sources feature:artwork-slots
	//   feature:reveal-unlock feature:playback-info feature:direct-play
	//   feature:stream-token feature:canonical-timeline feature:locale-l10n
	srv := newCorpusServer(t)

	// Capture every (context × surface) into a deterministic path→bytes map.
	files := map[string][]byte{}
	for _, ctx := range corpusContexts {
		for _, surf := range corpusSurfaces() {
			rel := filepath.ToSlash(filepath.Join(ctx.name, surf.file))
			req := httptest.NewRequest(surf.method, surf.path, strings.NewReader(surf.body))
			req.Header.Set("X-Beamfall-Session", ctx.session)
			req.Header.Set("X-Beamfall-Device", ctx.device)
			req.Header.Set("X-Beamfall-Device-Secret", ctx.deviceSecret)
			if surf.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("corpus %s: status %d: %s", rel, rec.Code, rec.Body.String())
			}
			var doc map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
				t.Fatalf("corpus %s: response is not valid JSON: %v", rel, err)
			}
			if surf.normalize != nil {
				doc = surf.normalize(doc)
			}
			pretty, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			files[rel] = append(pretty, '\n')
		}
	}

	version := corpusHash(files)

	if os.Getenv("BEAMFALL_UPDATE_GOLDEN") == "1" {
		// Rewrite the whole tree so removed contexts/surfaces leave no orphans.
		if err := os.RemoveAll(corpusDir); err != nil {
			t.Fatal(err)
		}
		for rel, content := range files {
			abs := filepath.FromSlash(filepath.Join(corpusDir, rel))
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(abs, content, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(corpusVersionPath, []byte(version+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(corpusManifestPath, corpusManifestBytes(files), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("contract corpus updated (%d files, version %s)", len(files), version)
		return
	}

	// Drift gate: every live file must match its committed copy byte-for-byte,
	// the committed version hash must match a fresh hash, and no orphan corpus
	// files may exist.
	for rel, want := range files {
		abs := filepath.FromSlash(filepath.Join(corpusDir, rel))
		got, err := os.ReadFile(abs)
		if err != nil {
			t.Fatalf("corpus file %s missing (%v) — regenerate with BEAMFALL_UPDATE_GOLDEN=1 go test ./internal/conformance -run TestGoldenCorpus", rel, err)
		}
		if string(got) != string(want) {
			t.Fatalf("corpus %s drifted from the committed copy.\n"+
				"If intentional: BEAMFALL_UPDATE_GOLDEN=1 go test ./internal/conformance -run TestGoldenCorpus.", rel)
		}
	}
	committed, err := os.ReadFile(corpusVersionPath)
	if err != nil {
		t.Fatalf("corpus.version missing (%v) — regenerate with BEAMFALL_UPDATE_GOLDEN=1", err)
	}
	if strings.TrimSpace(string(committed)) != version {
		t.Fatalf("corpus.version (%s) != freshly computed (%s) — regenerate with BEAMFALL_UPDATE_GOLDEN=1",
			strings.TrimSpace(string(committed)), version)
	}
	committedManifest, err := os.ReadFile(corpusManifestPath)
	if err != nil {
		t.Fatalf("corpus.manifest.json missing (%v) — regenerate with BEAMFALL_UPDATE_GOLDEN=1", err)
	}
	if string(committedManifest) != string(corpusManifestBytes(files)) {
		t.Fatalf("corpus.manifest.json drifted from the live corpus — regenerate with BEAMFALL_UPDATE_GOLDEN=1")
	}
	assertNoCorpusOrphans(t, files)
}

// assertNoCorpusOrphans fails if a *.json file exists under corpusDir that the
// live capture did not produce — a stale fixture is drift too.
func assertNoCorpusOrphans(t *testing.T, live map[string][]byte) {
	t.Helper()
	orphans, err := findCorpusOrphans(corpusDir, live, filepath.WalkDir, filepath.Rel)
	if err != nil {
		t.Fatalf("inspect corpus files: %v", err)
	}
	for _, rel := range orphans {
		t.Errorf("orphan corpus file %s — not produced by the live capture; regenerate with BEAMFALL_UPDATE_GOLDEN=1", rel)
	}
}

type corpusWalkFunc func(string, fs.WalkDirFunc) error
type corpusRelFunc func(string, string) (string, error)

func findCorpusOrphans(root string, live map[string][]byte, walk corpusWalkFunc, relative corpusRelFunc) ([]string, error) {
	var orphans []string
	err := walk(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		rel, relErr := relative(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == filepath.Base(corpusManifestPath) {
			return nil // the manifest itself is not a captured surface
		}
		if _, ok := live[filepath.ToSlash(rel)]; !ok {
			orphans = append(orphans, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(orphans)
	return orphans, nil
}

// newCorpusServer composes the real HTTP stack on a fixed clock and a fixed
// token key, both seeded Media backed by real files, a demo plugin registry
// (so the manifest carries a Layout and the graph pins plugin/mediaClassLabels
// shape), and a seeded poster (so SourceItem artwork is exercised). It mirrors
// the proven compositions in TestGoldenPlaybackInfo and TestGoldenGraphResponse.
func newCorpusServer(t *testing.T) *server.Server {
	t.Helper()
	st := storetest.Open(t)
	for i, mediaID := range []store.MediaID{"media-alpha", "media-archive"} {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("media-%d.bin", i))
		if err := os.WriteFile(path, []byte{byte(i)}, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := st.SetPrimaryMediaFile(context.Background(), mediaID, path); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "demo",
		Version:     "0.1.0",
		DisplayName: plugin.L("Demo"),
		MediaLabel:  plugin.L("Item"),
		MediaClassLabels: map[string]plugin.LocaleString{
			"feature": {"en": "Feature", "fr": "Long métrage"},
			"chapter": plugin.L("Chapter"),
		},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	authService := auth.NewServiceWithClock(st, clock)
	manifests := manifest.NewService(st, clock, registry)
	tokens := streamtoken.NewService([]byte("12345678901234567890123456789012"), clock)
	assetService := genassets.NewService(st, nil, t.TempDir())
	playbackInfo := playback.NewService(st, tokens, clock)
	playbackInfo.SetAssets(playbackLoudnessLocator{assetService})
	artworkService := artwork.NewService(st, nil, t.TempDir())
	graphHandler := graph.NewHandler(manifests, authService, st, registry)
	// A fixed artwork locator pins the ?v=<token> in artwork URLs. The real
	// artwork.Service stamps a non-deterministic mtime-derived token, which would
	// make the corpus flaky run-to-run (reuses fixedArtworkLocator from
	// golden_graph_test.go, same package).
	graphHandler.SetArtwork(fixedArtworkLocator{slots: map[string]bool{
		"media-alpha:" + artwork.SlotPoster:   true,
		"media-alpha:" + artwork.SlotBackdrop: true,
	}})
	return server.New(
		manifests,
		playbackInfo,
		beam.NewService(st, authService, clock),
		pairing.NewService(st, clock),
		graphHandler,
		mediahttp.NewHandler(st, authService, tokens, artworkService, playback.NewSessions(nil, t.TempDir(), nil)),
		authService,
		st,
	)
}

func normalizeManifestMap(doc map[string]any) map[string]any {
	doc["generatedAt"] = "1970-01-01T00:00:00Z"
	return doc
}

func normalizeGraphMap(doc map[string]any) map[string]any {
	if data, ok := doc["data"].(map[string]any); ok {
		if session, ok := data["currentSession"].(map[string]any); ok {
			if _, ok := session["expiresAt"]; ok {
				session["expiresAt"] = "1970-01-01T00:00:00Z"
			}
		}
	}
	return doc
}

// corpusHash is the order-independent content hash over the corpus files — the
// contract version clients pin against. Paths are slash-normalized and sorted
// so the hash is identical on every OS.
func corpusHash(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		fmt.Fprintf(h, "%s\n", p)
		h.Write(files[p])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// corpusManifestBytes is the per-file content manifest — a JSON object mapping
// each corpus file's slash-relative path to the lowercase hex sha256 of its
// bytes. Client repos verify each copied file against it with a plain per-file
// sha256 (no ordering or concatenation subtleties), so the check is trivially
// identical in Swift, Kotlin, and JS. json.MarshalIndent sorts the keys.
func corpusManifestBytes(files map[string][]byte) []byte {
	manifest := make(map[string]string, len(files))
	for rel, content := range files {
		sum := sha256.Sum256(content)
		manifest[rel] = hex.EncodeToString(sum[:])
	}
	out, _ := json.MarshalIndent(manifest, "", "  ")
	return append(out, '\n')
}

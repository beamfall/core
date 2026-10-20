package conformance

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/config"
)

// allowlistedCommandRoutes maps a "METHOD /pattern" route id to the reason it
// is genuinely exercised but cannot be detected by the path-literal scan below
// (e.g. a behavior test that drives the handler through a helper that builds
// the URL from a variable, so the path never appears as a literal). Add an
// entry ONLY after confirming a real behavior test exists; the stale-allowlist
// check fails the build if an entry stops being necessary.
//
// Currently empty: every command route is invoked with its path as a string
// literal in internal/server/*_test.go or internal/mediahttp/*_test.go.
var allowlistedCommandRoutes = map[string]string{}

// routeActionStatefulGETs is the curated set of token/session-bearing GET
// routes that are stateful "command-adjacent" surfaces (they mint/consume
// Stream Tokens or open a long-lived session stream) and therefore owe a
// behavior test on equal footing with the POST write routes.
//
// "Derive don't hand-curate" (the route-conformance review intent, mirrored
// from action_test.go's MutationFields() derivation) is satisfied for the
// write routes below by enumerating live POST routes from the route registry.
// It cannot be satisfied for these GETs from the registry alone: server.Route
// carries only Method / Pattern / RevealClass (internal/server/server.go ~32),
// and none of those fields distinguishes a stateful GET (HLS/direct stream,
// SSE events) from a purely read-only GET (/api/manifest, /api/media/artwork,
// /web/, /metrics, /health). reveal.EventFanout uniquely flags /api/events,
// but the two media-stream GETs share reveal.ContentBytes with the read-only
// artwork/asset/web GETs, so no field derives them. A curated list with this
// justification is the available option until the registry grows a "mutating"
// or "session-bearing" attribute. These three are exactly the GET routes named
// by the action-conformance plan as carrying a token/session.
var routeActionStatefulGETs = []string{
	"GET /api/media/hls/",    // Stream-Token-gated HLS delivery (ADR-0010)
	"GET /api/media/direct/", // Stream-Token-gated direct-play delivery (ADR-0010)
	"GET /api/media/proxy/",  // Stream-Token-gated proxy-tee stream + promote (ADR-0052)
	"GET /api/events",        // Reveal-filtered long-lived SSE session (ADR-0040)
}

// TestEveryCommandRouteHasBehaviorTest is the route-completeness gate and the
// symmetric partner to TestEveryActionHasBehaviorTest (which covers GraphQL
// mutations): every WRITE/COMMAND/stateful HTTP route must have at least one
// behavior test that drives it.
//
// The command set is derived from the live route registry where possible —
// every POST route reported by app.Routes() is a write/command route — and
// augmented with the curated routeActionStatefulGETs (token/session-bearing
// GETs that the registry's Method/Pattern/RevealClass fields cannot single out;
// see that var's comment).
//
// Detection mirrors action_test.go: deliberately tolerant. A route is "tested"
// when its Pattern appears as a string literal anywhere in
// internal/server/*_test.go or internal/mediahttp/*_test.go — the packages that
// own the handlers. A false PASS (counting a route as tested on a weak test) is
// preferable to a false FAIL (blocking the build over a path the scanner can't
// match); the allowlist covers anything genuinely tested only via a
// helper-built URL.
func TestEveryCommandRouteHasBehaviorTest(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.StreamTokenKey = []byte("12345678901234567890123456789012")
	beamfall, err := app.New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("app.New returned error: %v", err)
	}
	defer beamfall.Close()

	// Derive the write/command routes from the registry: every POST is a
	// state-changing command. Curated stateful GETs are added on top.
	commandRoutes := map[string]bool{}
	postRoutes := 0
	for _, route := range beamfall.Routes() {
		if route.Method == http.MethodPost {
			commandRoutes[route.Method+" "+route.Pattern] = true
			postRoutes++
		}
	}
	if postRoutes == 0 {
		t.Fatal("no POST routes derived from app.Routes(); the registry derivation is vacuous")
	}
	// Guard the curated GETs against drift: each must still be a live route, or
	// the curated list is stale and silently inventing coverage demands.
	liveRoutes := map[string]bool{}
	for _, route := range beamfall.Routes() {
		liveRoutes[route.Method+" "+route.Pattern] = true
	}
	for _, id := range routeActionStatefulGETs {
		if !liveRoutes[id] {
			t.Fatalf("curated stateful GET %q is not a live route; remove or fix the routeActionStatefulGETs entry", id)
		}
		commandRoutes[id] = true
	}

	// Scan the handler-owning test packages for behavior references.
	root := repoRoot(t)
	blob := routeActionScanBlob(t, root,
		filepath.Join("internal", "server"),
		filepath.Join("internal", "mediahttp"),
	)

	covered := map[string]bool{}
	for id := range commandRoutes {
		// id is "METHOD /pattern"; behavior tests reference the pattern as a
		// string literal opening with a double quote. An exact-path POST
		// (httptest.NewRequest(..., "/api/enroll", ...)) matches the closing
		// quote; a trailing-slash stream GET reference like
		// "/api/media/hls/"+token contains the opening `"/api/media/hls/` even
		// though the literal continues, so matching from the opening quote
		// covers both without an extra disjunct.
		pattern := id[strings.IndexByte(id, ' ')+1:]
		if strings.Contains(blob, `"`+pattern) {
			covered[id] = true
		}
	}

	var uncovered []string
	for id := range commandRoutes {
		if covered[id] {
			continue
		}
		if _, ok := allowlistedCommandRoutes[id]; ok {
			continue
		}
		uncovered = append(uncovered, id)
	}
	sort.Strings(uncovered)
	if len(uncovered) > 0 {
		t.Errorf("%d command route(s) have no detected behavior test in internal/server/*_test.go or internal/mediahttp/*_test.go (add a test, or allowlist with a reason): %v",
			len(uncovered), uncovered)
	}

	// Stale-allowlist check (mirrors action_test.go): an allowlist entry the
	// scanner DID detect, or that names a route no longer in the command set,
	// is dead and must be removed so the allowlist never hides a real gap.
	for id := range allowlistedCommandRoutes {
		if !commandRoutes[id] {
			t.Errorf("allowlistedCommandRoutes has %q which is not a current command route; remove the stale entry", id)
			continue
		}
		if covered[id] {
			t.Errorf("allowlistedCommandRoutes has %q but the scan DID detect a test for it; remove the unnecessary allowlist entry", id)
		}
	}
}

// routeActionScanBlob reads every *_test.go file under the given dirs (relative
// to root) into a single string for literal scanning. A dir with no test files
// is a test failure, never a skip — an empty scan would make the gate vacuous.
func routeActionScanBlob(t *testing.T, root string, dirs ...string) string {
	t.Helper()
	var b strings.Builder
	scanned := 0
	for _, dir := range dirs {
		abs := filepath.Join(root, dir)
		entries, err := os.ReadDir(abs)
		if err != nil {
			t.Fatalf("read %s: %v", abs, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(abs, e.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			text := string(data)
			if dir == filepath.Join("internal", "server") && e.Name() == "server_test.go" {
				text = stripGoTestFunc(text, "TestRoutesAreRecorded")
				text = stripGoTestFunc(text, "TestRouteSurfacesAreDerivedFromMountedRoutes")
			}
			b.WriteString(text)
			b.WriteByte('\n')
			scanned++
		}
	}
	if scanned == 0 {
		t.Fatalf("no *_test.go files found under %v to scan for route coverage", dirs)
	}
	return b.String()
}

func stripGoTestFunc(src, name string) string {
	start := strings.Index(src, "func "+name+"(")
	if start < 0 {
		return src
	}
	open := strings.Index(src[start:], "{")
	if open < 0 {
		return src
	}
	open += start
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[:start] + src[i+1:]
			}
		}
	}
	return src
}

package conformance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/config"
)

func TestEveryRouteRegistered(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.StreamTokenKey = []byte("12345678901234567890123456789012")
	beamfall, err := app.New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("app.New returned error: %v", err)
	}
	defer beamfall.Close()

	allowlist := map[string]bool{
		"GET /health": false,
		// Public AGPL §13 corresponding-source offer. It redirects to the
		// release-injected source archive and exposes no library/Reveal data.
		"GET /source": false,
		// Exact-root convenience redirect to the Core Web UI shell.
		// It serves no Media, Entities, bytes, search results, counts, stats, or
		// presence, so it is route-tracked but not a Reveal content surface.
		"GET /{$}": false,
		// Unauthenticated first-run probe: a pre-enrollment GET that
		// leaks only the single boolean "is setup still required", so it carries no
		// Reveal surface — like /health.
		"GET /api/setup": false,
		// Public moderation/contact path (NATIVEMGR-4, apple ADR-0004 §4 Guideline
		// 1.2): returns only the published moderation contact, no content/Reveal
		// data, so it carries no Reveal surface — like /api/setup and /health.
		"GET /api/moderation/contact": false,
		// Authenticated compatibility negotiation returns only a client/server
		// contract decision; it exposes no catalog or Reveal content.
		"POST /api/compat/handshake": false,
	}
	surfaces := map[string]bool{}
	for _, surface := range beamfall.Surfaces().All() {
		surfaces[surface.ID] = true
	}

	for _, route := range beamfall.Routes() {
		id := route.Method + " " + route.Pattern
		if _, ok := allowlist[id]; ok {
			allowlist[id] = true
			continue
		}
		if !routeRegistered(surfaces, id) {
			t.Fatalf("route %q is not registered as a reveal surface or allowlisted", id)
		}
	}
	for id, used := range allowlist {
		if !used {
			t.Fatalf("allowlist entry %q was not used", id)
		}
	}
}

func TestCompatRouteInventoryAndV1Exemption(t *testing.T) {
	serverSource, err := os.ReadFile(filepath.Join(repoRoot(t), "internal/server/server.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(serverSource)
	for _, seam := range []string{
		"if compatRouteExempt(method, pattern) {",
		"s.requireCompatDecision(next)(w, r)",
	} {
		if !strings.Contains(source, seam) {
			t.Fatalf("central route compatibility wrapper missing %q", seam)
		}
	}

	enforcementSource, err := os.ReadFile(filepath.Join(repoRoot(t), "internal/server/compat_enforcement.go"))
	if err != nil {
		t.Fatal(err)
	}
	enforcement := string(enforcementSource)
	if !strings.Contains(enforcement, `strings.HasPrefix(pattern, "/v1/")`) {
		t.Fatal("/v1 compatibility exemption is not parameterized in the production route guard")
	}
	for _, requiredBound := range []string{
		`"/api/graphql"`,
		`"/api/playback-info"`,
		`"/api/events"`,
	} {
		if strings.Contains(enforcement, requiredBound) {
			t.Errorf("mandatory embedded-web route %s appears in the compatibility exemption table", requiredBound)
		}
	}
}

func routeRegistered(surfaces map[string]bool, id string) bool {
	if surfaces[id] {
		return true
	}
	for surface := range surfaces {
		if strings.HasSuffix(surface, "/") && strings.HasPrefix(id, surface) {
			return true
		}
	}
	return false
}

package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/plugin"
)

const goldenManifestPath = "testdata/manifest.golden.json"

// TestGoldenManifest pins the Navigation Manifest wire shape — the most
// drift-prone client surface. The committed golden is also decoded by the
// client repos' contract tests (beamfall-apple ContractDecodingTests), so a
// shape change here must be propagated by copying the regenerated golden.
// Regenerate with BEAMFALL_UPDATE_GOLDEN=1 go test ./internal/conformance -run TestGoldenManifest.
func TestGoldenManifest(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.StreamTokenKey = []byte("12345678901234567890123456789012")
	cfg.SeedDevelopmentData = true
	t.Setenv("BEAMFALL_ADMIN_PASSWORD", "correct horse battery")
	beamfall, err := app.New(context.Background(), cfg, nil, []plugin.Bundle{directoryDiscoverBundle()})
	if err != nil {
		t.Fatalf("app.New returned error: %v", err)
	}
	defer beamfall.Close()
	createPluginLibrary(t, beamfall)

	req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	authorizeManager(req)
	rec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d: %s", rec.Code, rec.Body.String())
	}

	live := normalizeManifest(t, rec.Body.Bytes())

	if os.Getenv("BEAMFALL_UPDATE_GOLDEN") == "1" {
		pretty, err := json.MarshalIndent(live, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(goldenManifestPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenManifestPath, append(pretty, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden manifest updated: %s — copy it to the client repos' contract fixtures", goldenManifestPath)
		return
	}

	goldenBytes, err := os.ReadFile(goldenManifestPath)
	if err != nil {
		t.Fatalf("golden manifest missing (%v) — generate with BEAMFALL_UPDATE_GOLDEN=1", err)
	}
	golden := normalizeManifest(t, goldenBytes)
	if !reflect.DeepEqual(live, golden) {
		liveJSON, _ := json.MarshalIndent(live, "", "  ")
		t.Fatalf("manifest shape drifted from the committed golden.\n"+
			"If intentional: BEAMFALL_UPDATE_GOLDEN=1 go test ./internal/conformance -run TestGoldenManifest, "+
			"then copy the golden to the client repos.\nlive:\n%s", liveJSON)
	}
}

func directoryDiscoverBundle() plugin.Bundle {
	return compatibleTestBundle(plugin.Bundle{
		ID:           "starlog",
		DisplayName:  plugin.L("Starlog"),
		MediaLabel:   plugin.L("Entry"),
		MediaClasses: []string{"video"},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
			{ID: "discover", Kind: "shelf", Title: plugin.L("Directory Discover"), Source: plugin.SourceSpec{Kind: "directory", Value: "trending"}},
		}}},
	})
}

func createPluginLibrary(t *testing.T, beamfall *app.App) {
	t.Helper()
	verify := []byte(`{"query":"mutation Verify($password:String!){ verifyAdminSession(password:$password){ active } }","variables":{"password":"correct horse battery"}}`)
	postGraphQL(t, beamfall, verify)
	body := []byte(`{"query":"mutation CreateLibrary($title:String!,$pluginID:String!){ createLibrary(title:$title,pluginID:$pluginID){ id } }","variables":{"title":"Directory Discover","pluginID":"starlog"}}`)
	postGraphQL(t, beamfall, body)
}

func postGraphQL(t *testing.T, beamfall *app.App, body []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	beamfall.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || bytes.Contains(rec.Body.Bytes(), []byte(`"errors"`)) {
		t.Fatalf("graphql status = %d: %s", rec.Code, rec.Body.String())
	}
}

func authorizeManager(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Beamfall-Session", "session-manager")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
}

// normalizeManifest parses manifest JSON and pins volatile fields so the
// golden stays diffable yet decodes with every required key present.
func normalizeManifest(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	doc["generatedAt"] = "1970-01-01T00:00:00Z"
	return doc
}

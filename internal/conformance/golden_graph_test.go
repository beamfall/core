package conformance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const goldenGraphPath = "testdata/graph-response.golden.json"

// TestGoldenGraphResponse pins the GraphQL response envelope and the wire
// shapes clients decode from it — SessionInfo, PluginInfo, SourceItem — the
// same way the manifest and playback-info goldens pin theirs. Volatile
// values (session expiry) are normalized.
// Regenerate with BEAMFALL_UPDATE_GOLDEN=1 go test ./internal/conformance -run TestGoldenGraphResponse.
func TestGoldenGraphResponse(t *testing.T) {
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "demo",
		Version:     "0.1.0",
		DisplayName: plugin.L("Demo"),
		MediaLabel:  plugin.L("Item"),
		// Pins the mediaClassLabels wire shape: one full locale map (object
		// form) and one en-only shorthand (plain string).
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
	handler := graph.NewHandler(
		manifest.NewService(st, clock, registry),
		auth.NewService(st),
		st,
		registry,
	)
	// Artwork presence joins the SourceItem contract. A fake locator with a
	// FIXED version token pins the artwork shape deterministically: a real
	// artwork.Service would stamp each URL with the seed file's non-deterministic
	// mtime (?v=<mtime>), making this golden flaky. media-alpha has a poster +
	// backdrop; everything else omits the field.
	handler.SetArtwork(fixedArtworkLocator{slots: map[string]bool{
		"media-alpha:" + artwork.SlotPoster:   true,
		"media-alpha:" + artwork.SlotBackdrop: true,
	}})

	body := `{"query":"query Contract($kind: String!) { currentSession { id deviceRole profileID profile revealed } plugins { id displayName mediaLabel mediaClassLabels } sourceItems(source: { kind: $kind }) { id kind title summary artwork { poster } artworkSlots } }","variables":{"kind":"recently-added"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(body))
	req.Header.Set("X-Beamfall-Session", "session-default")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("graph status = %d: %s", rec.Code, rec.Body.String())
	}

	live := normalizeGraphResponse(t, rec.Body.Bytes())

	if os.Getenv("BEAMFALL_UPDATE_GOLDEN") == "1" {
		pretty, err := json.MarshalIndent(live, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(goldenGraphPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenGraphPath, append(pretty, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden graph response updated: %s — copy it to the client repos' contract fixtures", goldenGraphPath)
		return
	}

	goldenBytes, err := os.ReadFile(goldenGraphPath)
	if err != nil {
		t.Fatalf("golden graph response missing (%v) — generate with BEAMFALL_UPDATE_GOLDEN=1", err)
	}
	golden := normalizeGraphResponse(t, goldenBytes)
	if !jsonEqual(live, golden) {
		liveJSON, _ := json.MarshalIndent(live, "", "  ")
		t.Fatalf("graph response shape drifted from the committed golden.\n"+
			"If intentional: BEAMFALL_UPDATE_GOLDEN=1 go test ./internal/conformance -run TestGoldenGraphResponse, "+
			"then copy the golden to the client repos.\nlive:\n%s", liveJSON)
	}
}

// fixedArtworkLocator satisfies graph.ArtworkLocator with a deterministic
// version token ("av1") for the seeded slots, keyed "<id>:<slot>".
type fixedArtworkLocator struct {
	slots map[string]bool
}

const fixedArtworkVersion = "av1"

func (f fixedArtworkLocator) ArtworkVersion(id store.MediaID, slot string) (string, bool) {
	if f.slots[string(id)+":"+slot] {
		return fixedArtworkVersion, true
	}
	return "", false
}

func (f fixedArtworkLocator) EntityArtworkVersion(id store.EntityID, slot string) (string, bool) {
	if f.slots[string(id)+":"+slot] {
		return fixedArtworkVersion, true
	}
	return "", false
}

// normalizeGraphResponse parses a GraphQL response and pins volatile values
// (session expiry) while keeping every key present.
func normalizeGraphResponse(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	doc := decodeJSON(t, raw)
	if data, ok := doc["data"].(map[string]any); ok {
		if session, ok := data["currentSession"].(map[string]any); ok {
			if _, ok := session["expiresAt"]; ok {
				session["expiresAt"] = "1970-01-01T00:00:00Z"
			}
		}
	}
	return doc
}

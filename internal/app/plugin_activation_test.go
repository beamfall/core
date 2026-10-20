package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// bundleHasKind reports whether the registry's bundle declares an Entity Kind by
// name — the observable proof that a bundle's Kinds/Sources actually loaded.
func bundleHasKind(reg *plugin.Registry, id plugin.ID, kind string) bool {
	b, ok := reg.Bundle(id)
	if !ok {
		return false
	}
	for _, k := range b.EntityKinds {
		if k.Name == kind {
			return true
		}
	}
	return false
}

// TestPluginDeactivationEnforcedAtBoot proves the activation override (migration
// 0035) is enforced at registry construction: a bundle the Console deactivated is
// absent from the live registry (its Entity Kinds never load), while a bundle
// with no override stays active.
func TestPluginDeactivationEnforcedAtBoot(t *testing.T) {
	ctx := context.Background()
	bundles := []plugin.Bundle{
		testBundleWithKind(testPathPluginID, "navigator"),
		testBundleWithKind(testSecondaryPluginID, "waypoint"),
	}

	// Persist a deactivation into the data dir app.New will open,
	// then close so app.New owns the single-writer store.
	cfg := testConfig(t)
	st, err := store.Open(ctx, cfg.DataDir, store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.SetPluginActivation(ctx, testSecondaryPluginID, false, time.Now().UTC()); err != nil {
		t.Fatalf("SetPluginActivation: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	beamfall, err := New(ctx, cfg, nil, bundles)
	if err != nil {
		t.Fatalf("New with a deactivation override: %v", err)
	}
	defer beamfall.Close()

	// Deactivated bundle absent from the live registry; its Entity Kinds gone.
	if _, ok := beamfall.plugins.Bundle(testSecondaryPluginID); ok {
		t.Fatal("deactivated bundle present in the live registry")
	}
	if bundleHasKind(beamfall.plugins, testSecondaryPluginID, "waypoint") {
		t.Fatal("deactivated bundle Entity Kind loaded")
	}
	// A bundle with no override stays active and contributes its Kinds.
	if _, ok := beamfall.plugins.Bundle(testPathPluginID); !ok {
		t.Fatal("active bundle missing from the live registry")
	}
	if !bundleHasKind(beamfall.plugins, testPathPluginID, "navigator") {
		t.Fatal("active bundle Entity Kind did not load")
	}
}

// TestPluginActivationDefaultsActiveAtBoot proves the first-boot default: with no
// override row (empty plugin_activation table), every declared bundle loads.
func TestPluginActivationDefaultsActiveAtBoot(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	bundles := []plugin.Bundle{
		testBundleWithKind(testPathPluginID, "navigator"),
		testBundleWithKind(testSecondaryPluginID, "waypoint"),
	}

	beamfall, err := New(ctx, cfg, nil, bundles)
	if err != nil {
		t.Fatalf("New on a fresh data dir: %v", err)
	}
	defer beamfall.Close()

	for _, b := range bundles {
		if _, ok := beamfall.plugins.Bundle(b.ID); !ok {
			t.Fatalf("default-active bundle %q missing with no override", b.ID)
		}
	}
}

func TestPluginStandingDerivedAtBoot(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	bundle := testBundleWithKind(testPathPluginID, "navigator")
	bundle.DisplayName = plugin.L("Beamfall Official ✓")

	beamfall, err := New(ctx, cfg, nil, []plugin.Bundle{bundle})
	if err != nil {
		t.Fatalf("New on a fresh data dir: %v", err)
	}
	defer beamfall.Close()

	standing, ok := beamfall.plugins.Standing(bundle.ID)
	if !ok {
		t.Fatalf("standing for loaded plugin %q is missing", bundle.ID)
	}
	want := plugin.PluginStanding{
		Provenance: plugin.ProvenanceFirstParty,
		TrustState: plugin.PluginUnsigned,
	}
	if standing != want {
		t.Fatalf("standing = %#v, want %#v", standing, want)
	}
}

func TestStagedPluginInventoryAppliesAtBoot(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	base := testBundle(testPathPluginID)
	dynamic := testBundle("dynamic")
	stageTestBundle(t, cfg.DataDir, []plugin.Bundle{base}, dynamic)

	beamfall, err := New(ctx, cfg, nil, []plugin.Bundle{base})
	if err != nil {
		t.Fatalf("New with staged inventory: %v", err)
	}
	defer beamfall.Close()
	if _, ok := beamfall.plugins.Bundle(dynamic.ID); !ok {
		t.Fatal("staged bundle did not apply at restart")
	}
	standing, ok := beamfall.plugins.Standing(dynamic.ID)
	wantStanding := plugin.PluginStanding{Provenance: plugin.ProvenanceSideloaded, TrustState: plugin.PluginUnsigned}
	if !ok || standing != wantStanding {
		t.Fatalf("staged bundle standing = %#v, %v; want %#v", standing, ok, wantStanding)
	}
}

func TestCorruptStagedPluginInventoryRetainsLastGoodAtBoot(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	base := testBundle(testPathPluginID)
	dynamic := testBundle("dynamic")
	stageTestBundle(t, cfg.DataDir, []plugin.Bundle{base}, dynamic)
	staged := filepath.Join(cfg.DataDir, "plugins", "inventory", "staged.json")
	if err := os.WriteFile(staged, []byte(`{"schema_version":1,"entries":[`), 0o600); err != nil {
		t.Fatal(err)
	}

	beamfall, err := New(ctx, cfg, nil, []plugin.Bundle{base})
	if err != nil {
		t.Fatalf("New with corrupt staged inventory: %v", err)
	}
	defer beamfall.Close()
	if _, ok := beamfall.plugins.Bundle(base.ID); !ok {
		t.Fatal("last-good executable bundle missing")
	}
	if _, ok := beamfall.plugins.Bundle(dynamic.ID); ok {
		t.Fatal("corrupt staged bundle applied")
	}
}

func TestStagedBundleCannotActivateCompiledFeedHook(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	base := testBundle(testPathPluginID)
	dynamic := testBundle("dynamic")
	dynamic.Scrapers = []plugin.ScraperSpec{{ID: "local", DisplayName: plugin.L("Local"), Kind: "exif"}}
	stageTestBundle(t, cfg.DataDir, []plugin.Bundle{base}, dynamic)
	called := false
	hooks := Hooks{FeedSearcher: &FeedSearcherHook{
		PluginID:          string(dynamic.ID),
		RequiredScraperID: "local",
		New: func(*scrape.Engine, SecretSource) graph.FeedSearcher {
			called = true
			return inertFeedSearcher{}
		},
	}}

	beamfall, err := New(ctx, cfg, nil, []plugin.Bundle{base}, hooks)
	if err != nil {
		t.Fatalf("New with dynamic declaration and compiled hook: %v", err)
	}
	defer beamfall.Close()
	if called {
		t.Fatal("declarative staged bundle activated compiled code")
	}
}

func TestPluginInventoryCannotReachExecutableOrLegacyAuthorizationPaths(t *testing.T) {
	appSource, err := os.ReadFile("app.go") // #nosec G304 -- fixed package-relative source path.
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{
		[]byte("scanIdentifier{registry: executablePlugins}"),
		[]byte("identify.NewService(scrapeEngine, executablePlugins, st)"),
	} {
		if !bytes.Contains(appSource, required) {
			t.Fatalf("staged inventory reached an executable identify path; missing %q", required)
		}
	}
	if bytes.Contains(appSource, []byte("IsFirstParty")) {
		t.Fatal("app.go uses legacy first-party authorization")
	}
	pluginSource, err := os.ReadFile("../plugin/inventory.go") // #nosec G304 -- fixed package-relative source path.
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(pluginSource, []byte("IsFirstParty")) {
		t.Fatal("../plugin/inventory.go uses legacy first-party authorization")
	}
}

type inertFeedSearcher struct{}

func (inertFeedSearcher) SearchFeeds(context.Context, string) ([]graph.FeedSearchResult, error) {
	return nil, nil
}

func stageTestBundle(t *testing.T, dataDir string, executable []plugin.Bundle, bundle plugin.Bundle) {
	t.Helper()
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	inventory, err := plugin.OpenInventory(filepath.Join(dataDir, "plugins", "inventory"), executable)
	if err != nil {
		t.Fatal(err)
	}
	_, err = inventory.Stage(plugin.StageBundleRequest{
		Artifact: raw,
		Origin:   plugin.BundleOriginSideloaded,
		Confirmation: &plugin.StageConfirmation{
			Origin:         plugin.BundleOriginSideloaded,
			PluginID:       bundle.ID,
			Version:        bundle.Version,
			ArtifactSHA256: hex.EncodeToString(sum[:]),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

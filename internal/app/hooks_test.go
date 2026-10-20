package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/store"
	scrobble "github.com/beamfall/plugin-sdk/scrobble"
)

func TestNewRejectsMultipleHooksBeforeStoreInitialization(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "not-created")
	cfg := config.Default()
	cfg.DataDir = dataDir
	if _, err := New(t.Context(), cfg, nil, nil, Hooks{}, Hooks{}); err == nil || !strings.Contains(err.Error(), "at most one Hooks") {
		t.Fatalf("New error = %v, want Hooks cardinality error", err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("invalid Hooks initialized data dir: %v", err)
	}
}

type countingEnrichmentProvider struct{}

func (countingEnrichmentProvider) Available() bool           { return false }
func (countingEnrichmentProvider) Kind() store.EmbeddingKind { return "test" }
func (countingEnrichmentProvider) EmbedQuery(context.Context, string) ([]float32, error) {
	return nil, nil
}
func (countingEnrichmentProvider) EmbedDocument(context.Context, string) ([]float32, error) {
	return nil, nil
}

func TestNewConstructsEnrichmentProviderOnce(t *testing.T) {
	calls := 0
	hooks := Hooks{EnrichmentProvider: func(bool) EnrichmentProvider {
		calls++
		return countingEnrichmentProvider{}
	}}
	app, err := New(t.Context(), testConfig(t), nil, nil, hooks)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if calls != 1 {
		t.Fatalf("enrichment provider constructions = %d, want 1", calls)
	}
}

type hookScrobbleAdapter struct{ service scrobble.Service }

func (a hookScrobbleAdapter) Service() scrobble.Service                     { return a.service }
func (hookScrobbleAdapter) Class() scrobble.MediaClass                      { return "video" }
func (hookScrobbleAdapter) IdentitySchemes() []string                       { return []string{"test"} }
func (hookScrobbleAdapter) DeclaredEndpoints() []string                     { return nil }
func (hookScrobbleAdapter) Submit(context.Context, scrobble.Scrobble) error { return nil }

func TestKnownInactiveScrobbleServiceIsIgnoredWithoutMaskingUnknown(t *testing.T) {
	cfg := testConfig(t)
	st, err := store.Open(t.Context(), cfg.DataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	registry := map[scrobble.Service]scrobble.AdapterFactory{
		"active": func(scrobble.Doer, scrobble.AdapterConfig, scrobble.SecretSource) (scrobble.Adapter, error) {
			return hookScrobbleAdapter{service: "active"}, nil
		},
		"inactive": func(scrobble.Doer, scrobble.AdapterConfig, scrobble.SecretSource) (scrobble.Adapter, error) {
			return inactiveScrobbleAdapter{service: "inactive"}, nil
		},
	}
	configPath := filepath.Join(cfg.DataDir, "scrobble-adapters.json")
	if err := os.WriteFile(configPath, []byte(`{"adapters":[{"service":"active"},{"service":"inactive"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := buildScrobbleDispatcherWithRegistryAndInactive(cfg, st, nil, newScrobbleHTTPClient(), registry, map[scrobble.Service]bool{"inactive": true})
	if err != nil {
		t.Fatal(err)
	}
	descriptors := dispatcher.AdapterDescriptors()
	if len(descriptors) != 1 || descriptors[0].Service != "active" {
		t.Fatalf("adapter descriptors = %+v, want only active", descriptors)
	}
	if err := os.WriteFile(configPath, []byte(`{"adapters":[{"service":"unknown"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildScrobbleDispatcherWithRegistryAndInactive(cfg, st, nil, newScrobbleHTTPClient(), registry, map[scrobble.Service]bool{"inactive": true}); err == nil || !strings.Contains(err.Error(), "unknown service") {
		t.Fatalf("unknown service error = %v", err)
	}
}

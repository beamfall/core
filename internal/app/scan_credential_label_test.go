package app

import (
	"strings"
	"testing"

	"github.com/beamfall/core/internal/plugin"
	scanpkg "github.com/beamfall/core/internal/scan"
)

// TestScanIdentifierLabelsMissingCredentialByPluginName (CORE-2187): scan
// status names a missing provider credential by the plugin's display name
// and the credential's label, never by the internal scoped ref.
func TestScanIdentifierLabelsMissingCredentialByPluginName(t *testing.T) {
	const pluginID = "label-fixture"
	bundle := testBundle(pluginID)
	bundle.DisplayName = plugin.L("Synthetic Catalog")
	bundle.Scrapers = []plugin.ScraperSpec{
		{
			ID: "helped", DisplayName: plugin.L("Helped"), Kind: "http-json", Endpoint: "https://metadata.example.test",
			LookupTemplate: "/{id}", AuthRef: "catalog-key", AuthStyle: "query:key", FieldMap: map[string]string{"title": "name"},
			CredentialHelp: &plugin.CredentialHelp{Label: plugin.L("Catalog API key")},
		},
		{
			ID: "bare", DisplayName: plugin.L("Bare"), Kind: "http-json", Endpoint: "https://metadata.example.test",
			LookupTemplate: "/{id}", AuthRef: "bare-key", AuthStyle: "query:key", FieldMap: map[string]string{"title": "name"},
		},
	}
	registry, err := plugin.NewRegistry([]plugin.Bundle{bundle})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	adapter := &scanIdentifier{registry: registry}
	var _ scanpkg.CredentialLabeler = adapter

	helped := plugin.ScopedCredentialRef(pluginID, "catalog-key")
	if got, want := adapter.CredentialLabel(helped), "Synthetic Catalog: Catalog API key"; got != want {
		t.Fatalf("CredentialLabel(helped) = %q, want %q", got, want)
	}
	if got, want := adapter.CredentialLabel(plugin.ScopedCredentialRef(pluginID, "bare-key")), "Synthetic Catalog: provider key"; got != want {
		t.Fatalf("CredentialLabel(bare) = %q, want %q", got, want)
	}
	for _, ref := range []string{
		plugin.ScopedCredentialRef(pluginID, "undeclared-key"),
		plugin.ScopedCredentialRef("absent-plugin", "catalog-key"),
		"not-a-scoped-ref",
	} {
		if got := adapter.CredentialLabel(ref); got != "" {
			t.Fatalf("CredentialLabel(%q) = %q, want empty", ref, got)
		}
	}
	if got := (&scanIdentifier{}).CredentialLabel(helped); got != "" {
		t.Fatalf("CredentialLabel(no registry) = %q, want empty", got)
	}
	if strings.Contains(adapter.CredentialLabel(helped), "bfplugin-") {
		t.Fatal("label carries the internal ref")
	}
}

package app

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/plugin"
)

type fixedPluginConfig map[string]map[string]string

func (f fixedPluginConfig) PluginConfigValues(_ context.Context, pluginID string) (map[string]string, error) {
	values := map[string]string{}
	for name, value := range f[pluginID] {
		values[name] = value
	}
	return values, nil
}

type fixedBundles map[plugin.ID]plugin.Bundle

func (f fixedBundles) Bundle(id plugin.ID) (plugin.Bundle, bool) {
	bundle, ok := f[id]
	return bundle, ok
}

// TestScrapeConfigFallsBackToDeclaredDefaults pins CORE-1128: a scraper's
// {config.<name>} reads the saved value, else the field's declared default, the
// same effective value the Console shows.
func TestScrapeConfigFallsBackToDeclaredDefaults(t *testing.T) {
	bundles := fixedBundles{"fixture-plugin": {ConfigFields: []plugin.ConfigFieldSpec{
		{Name: "metadata-language", Type: "text", Default: "eng"},
		{Name: "region", Type: "text", Default: "north"},
		{Name: "no-default", Type: "text"},
	}}}
	source := pluginConfigWithDefaults{
		saved:   fixedPluginConfig{"fixture-plugin": {"region": "south"}},
		bundles: bundles,
	}
	values, err := source.PluginConfigValues(context.Background(), "fixture-plugin")
	if err != nil {
		t.Fatal(err)
	}
	if values["metadata-language"] != "eng" || values["region"] != "south" {
		t.Fatalf("values = %#v, want the default language and the saved region", values)
	}
	if _, ok := values["no-default"]; ok {
		t.Fatalf("a field with no default and no saved value resolved to %q", values["no-default"])
	}
	other, err := source.PluginConfigValues(context.Background(), "unknown-plugin")
	if err != nil || len(other) != 0 {
		t.Fatalf("unknown plugin values = %#v, %v", other, err)
	}
}

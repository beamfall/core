package app

import (
	"context"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
)

// pluginConfigWithDefaults resolves {config.<name>} scraper substitutions from
// the owner's saved plugin settings, falling back to each declared field's
// default, so a scraper reads the same effective value the Console shows
// (CORE-1128: a metadata language the owner never changed still applies).
type pluginConfigWithDefaults struct {
	saved   scrape.ConfigSource
	bundles interface {
		Bundle(plugin.ID) (plugin.Bundle, bool)
	}
}

func (c pluginConfigWithDefaults) PluginConfigValues(ctx context.Context, pluginID string) (map[string]string, error) {
	values, err := c.saved.PluginConfigValues(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	bundle, ok := c.bundles.Bundle(plugin.ID(pluginID))
	if !ok {
		return values, nil
	}
	for _, field := range bundle.ConfigFields {
		if _, saved := values[field.Name]; saved || field.Default == "" {
			continue
		}
		if values == nil {
			values = map[string]string{}
		}
		values[field.Name] = field.Default
	}
	return values, nil
}

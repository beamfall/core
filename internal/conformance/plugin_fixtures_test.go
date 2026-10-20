package conformance

import (
	"github.com/beamfall/core/internal/apptest/plugintest"
	"github.com/beamfall/core/internal/plugin"
)

func compatibleTestBundle(bundle plugin.Bundle) plugin.Bundle {
	return plugintest.Compatible(bundle)
}

func newTestPluginRegistry(bundles []plugin.Bundle) (*plugin.Registry, error) {
	return plugintest.Registry(bundles)
}

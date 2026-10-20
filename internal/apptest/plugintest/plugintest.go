// Package plugintest provides shared plugin fixtures for Core tests.
package plugintest

import (
	"github.com/beamfall/core/internal/plugin"
	sdk "github.com/beamfall/plugin-sdk"
)

// Compatible declares compatibility with the SDK embedded by this Core build.
func Compatible(bundle plugin.Bundle) plugin.Bundle {
	if bundle.SDKFloor == "" {
		bundle.SDKFloor = sdk.Version
	}
	if bundle.SDKCeiling == "" {
		bundle.SDKCeiling = sdk.Version
	}
	return bundle
}

// Registry builds a production registry from SDK-compatible test bundles.
func Registry(bundles []plugin.Bundle) (*plugin.Registry, error) {
	for i := range bundles {
		bundles[i] = Compatible(bundles[i])
	}
	return plugin.NewRegistry(bundles)
}

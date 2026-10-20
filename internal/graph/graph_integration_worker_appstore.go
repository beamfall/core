//go:build beamfall_appstore

package graph

import (
	"context"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/integrationhost"
)

// ADR-0058 / ADR-0133 Integration Worker Console surface — App Store half.
// App Store binaries must not expose worker state, grant approval, update-source
// configuration, or lifecycle-action endpoints. The dispatcher literals remain
// in graph.go for conformance visibility, but this file removes the fields from
// the scoped registries before graph.go assembles scopedFields, so callers see
// them as unknown top-level fields.

var _ = suppressIntegrationWorkerFields()

func suppressIntegrationWorkerFields() bool {
	for _, name := range []string{
		"pluginworker",
		"approvepluginpermission",
		"setpluginupdatesource",
		"runpluginlifecycleaction",
	} {
		delete(queryFields, name)
		delete(mutationFields, name)
		delete(adminFields, name)
		delete(resultTypes, name)
	}
	return true
}

type IntegrationLifecycle interface{}

func (h *Handler) SetIntegrationActionRunner(_ integrationhost.ActionRunner) {}

type PluginWorkerInfo struct{}

func (h *Handler) pluginWorkerField(_ context.Context, _ request) (PluginWorkerInfo, error) {
	return PluginWorkerInfo{}, clientError("unsupported query")
}

func (h *Handler) approvePluginPermission(_ context.Context, _ auth.Scope, _ request) (PluginWorkerInfo, error) {
	return PluginWorkerInfo{}, clientError("unsupported query")
}

func (h *Handler) setPluginUpdateSource(_ context.Context, _ auth.Scope, _ request) (PluginWorkerInfo, error) {
	return PluginWorkerInfo{}, clientError("unsupported query")
}

func (h *Handler) runPluginLifecycleAction(_ context.Context, _ auth.Scope, _ request) (PluginWorkerInfo, error) {
	return PluginWorkerInfo{}, clientError("unsupported query")
}

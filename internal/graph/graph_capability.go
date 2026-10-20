//go:build !beamfall_appstore

package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/store"
)

// Capability runtime-gating control surface. This file is the
// NON-App-Store half: the web Console and the direct-distribution macOS Manager
// (neither is an App Store binary) expose the toggle freely. The App Store half
// (graph_capability_appstore.go) compiles all of this OUT so the API is not
// advertised and not reachable (invariant 8) — Workflow Plugins are compiled in,
// so suppressing only the UI would leave a reachable endpoint.

// CapabilityStateInfo is the wire shape for one capability's runtime state: its
// id, kind (workflow / probe), runtime-enabled flag, whether the operator may
// flip it (only Workflow capabilities), and the degraded reason a probe-absent
// capability reports.
type CapabilityStateInfo struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Enabled   bool   `json:"enabled"`
	Flippable bool   `json:"flippable"`
	Reason    string `json:"reason,omitempty"`
}

// registerCapabilityControlFields adds the capability-control fields to the
// dispatcher registries so they participate in the auth gate and schema manifest.
// On an App Store binary the build-tag sibling makes this a no-op, so the fields
// are never advertised or reachable.
func registerCapabilityControlFields() {
	queryFields["capabilitystates"] = true
	mutationFields["setcapabilityenabled"] = true
	adminFields["capabilitystates"] = true
	adminFields["setcapabilityenabled"] = true
	// Both fields resolve to the runtime capability-state list, so the nested
	// schema can project them. Registered here (not as a literal) so the App Store
	// sibling leaves resultTypes free of fields scopedFields does not carry.
	resultTypes["capabilitystates"] = []CapabilityStateInfo{}
	resultTypes["setcapabilityenabled"] = []CapabilityStateInfo{}
}

// dispatchCapabilityControl resolves the capability-control fields. It returns
// handled=true when it ran (so execute() routes errors), and a response carrying
// any error. Admin-gating is enforced by the caller.
func (h *Handler) dispatchCapabilityControl(dispatch *fieldDispatchContext) (response, bool) {
	handled := false
	ctx := dispatch.ctx
	scope := dispatch.scope
	req := dispatch.req
	fields := dispatch.fields
	if fields["setcapabilityenabled"] {
		handled = true
		result, err := h.setCapabilityEnabled(ctx, scope, req)
		if resp := resolveField(dispatch, "setcapabilityenabled", result, err); len(resp.Errors) > 0 {
			return resp, true
		}
	}
	if fields["capabilitystates"] {
		handled = true
		states, err := h.capabilityStates(ctx)
		if resp := resolveField(dispatch, "capabilitystates", states, err); len(resp.Errors) > 0 {
			return resp, true
		}
	}
	return response{}, handled
}

// capabilityStates reads the runtime state of every compiled-in capability,
// applying the operator's persisted Workflow toggles. Probe
// capabilities are auto-detected and report `enabled:false, reason:probe-absent`
// when their host probe fails; the Console omits a toggle for them (flippable
// false).
func (h *Handler) capabilityStates(ctx context.Context) ([]CapabilityStateInfo, error) {
	overrides, err := h.store.CapabilityStates(ctx)
	if err != nil {
		return nil, err
	}
	resolved := capabilities.Resolve(overrides)
	out := make([]CapabilityStateInfo, 0, len(resolved))
	for _, s := range resolved {
		out = append(out, CapabilityStateInfo{
			ID:        s.ID,
			Kind:      s.Kind,
			Enabled:   s.Enabled,
			Flippable: s.Kind == capabilities.KindWorkflow,
			Reason:    s.Reason,
		})
	}
	return out, nil
}

// setCapabilityEnabled flips one Workflow capability's runtime enabled state and
// returns the refreshed capability states. It is an audited, attributed mutation
// — Admin-gated in execute(). Only operator-flippable Workflow
// capabilities are valid targets: a probe-derived or unknown id is a client
// error, so the override table can never carry a phantom or auto-detected
// capability.
func (h *Handler) setCapabilityEnabled(ctx context.Context, scope auth.Scope, req request) ([]CapabilityStateInfo, error) {
	id := trimmedVariable(req, "capability")
	if id == "" {
		return nil, clientError("capability is required")
	}
	v := newVars(req)
	enabled, ok := v.Bool("enabled")
	if err := v.Err(); err != nil {
		return nil, err
	}
	if !ok {
		return nil, clientError("enabled is required")
	}
	if !isFlippableWorkflowCapability(id) {
		return nil, clientError("unknown or non-flippable capability")
	}
	if err := h.store.SetCapabilityEnabled(ctx, id, enabled, time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityCapabilityToggle, "capability", id, ""); err != nil {
		return nil, err
	}
	return h.capabilityStates(ctx)
}

// isFlippableWorkflowCapability reports whether id names a compiled-in,
// operator-flippable Workflow capability. Probe capabilities and unknown ids are
// rejected — probe caps are auto-detected, never operator-flipped.
func isFlippableWorkflowCapability(id string) bool {
	for _, wid := range capabilities.WorkflowIDs() {
		if wid == id {
			return true
		}
	}
	return false
}

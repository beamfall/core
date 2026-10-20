//go:build beamfall_appstore

package graph

// Capability runtime-gating control surface — the App Store half.
// On an App Store binary the capability-control surface is suppressed BOTH UI and
// API (invariant 8): because Workflow Plugins are compiled INTO the binary
//, suppressing only the toggle UI would leave a reachable
// runtime endpoint. So here the control fields are never registered (not
// advertised in the schema manifest, rejected as unknown top-level fields) and
// the dispatcher never resolves them (not reachable). A Viewer never sees, and an
// API caller never reaches, a capability toggle.

// registerCapabilityControlFields is a no-op on an App Store binary: the
// capability-control fields are deliberately absent from the dispatcher
// registries, so they are not advertised and any request naming them is rejected
// as an unknown top-level field.
func registerCapabilityControlFields() {}

// dispatchCapabilityControl is a no-op on an App Store binary: the
// capability-control API is not reachable. (On an App Store build the control
// fields are never registered, so execute()'s fields[...] guard is never true;
// this stub exists only to satisfy the call site that is compiled in.)
func (h *Handler) dispatchCapabilityControl(_ *fieldDispatchContext) (response, bool) {
	return response{}, false
}

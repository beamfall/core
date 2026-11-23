package graph

import (
	"testing"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/store"
)

// A Device that held the manager role before it was bound keeps that stored
// role, but a bound display is a receiver: manager- and admin-gated fields are
// refused (ADR-0240 D1, D2).
func TestPlayerBoundManagerDeviceCannotRunManagerOrAdminFields(t *testing.T) {
	for _, field := range []string{"trashmedia", "setdevicerole"} {
		handler := fieldHandlerRegistry[field]
		if len(handler.run) == 0 {
			t.Fatalf("%s has no dispatch order", field)
		}
		dispatch := &fieldDispatchContext{
			scope: auth.Scope{
				ProfileID:          "profile-player",
				Profile:            store.ProfilePlayer,
				PlayerProfileID:    "profile-player",
				DeviceRole:         store.DeviceRoleManager,
				Admin:              true,
				ClientCapabilities: []string{string(capabilities.CapabilityPlayerDisplay)},
			},
			fields: map[string]bool{field: true},
		}
		resp := authorizeDispatchOrder(dispatch, handler.run[0])
		if !resp.authorizationRefused || len(resp.Errors) == 0 || resp.Errors[0].Extensions.Code != ErrorCodeForbidden {
			t.Fatalf("%s from bound manager device = %#v, want FORBIDDEN", field, resp)
		}
	}
}

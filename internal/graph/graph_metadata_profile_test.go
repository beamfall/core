package graph

import (
	"testing"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// A managed profile keeps its manager device role, so the item-edit mutations
// refuse it by profile kind, not just by device role (CORE-2905).
func TestItemEditMutationsRefuseManagedProfile(t *testing.T) {
	for _, field := range []string{"pinartworkcandidate", "setmediafieldlock", "editmediametadata", "refreshmediametadata"} {
		handler := fieldHandlerRegistry[field]
		order := handler.run[0]
		for _, tc := range []struct {
			profile store.ProfileKind
			refused bool
		}{
			{store.ProfileManaged, true},
			{store.ProfileAdult, false},
		} {
			dispatch := &fieldDispatchContext{
				scope:  auth.Scope{SessionID: "s", DeviceRole: store.DeviceRoleManager, Profile: tc.profile},
				fields: map[string]bool{field: true},
			}
			resp := authorizeDispatchOrder(dispatch, order)
			if refused := resp.authorizationRefused; refused != tc.refused {
				t.Fatalf("%s with %s profile: refused = %v, want %v (%#v)", field, tc.profile, refused, tc.refused, resp.Errors)
			}
			if tc.refused && resp.Errors[0].Message != "adult profile required" {
				t.Fatalf("%s: error = %q", field, resp.Errors[0].Message)
			}
		}
	}
}

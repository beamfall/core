package store_test

import (
	"testing"
	"time"

	"github.com/beamfall/core/internal/store/storetest"
)

// TestCapabilityStatesPersistAndReadBack proves the Workflow-capability enabled
// override persists under capability.<id>.enabled and reads back,
// with absent capabilities carrying no override (POWERTOOLS-9, store half).
func TestCapabilityStatesPersistAndReadBack(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	now := time.Unix(1700, 0).UTC()

	states, err := st.CapabilityStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("fresh store capability overrides = %#v, want none", states)
	}

	if err := st.SetCapabilityEnabled(ctx, "conversion", false, now); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCapabilityEnabled(ctx, "rename", true, now); err != nil {
		t.Fatal(err)
	}

	states, err = st.CapabilityStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := states["conversion"]; !ok || got {
		t.Fatalf("conversion override = (%v,%v), want (false,true)", got, ok)
	}
	if got, ok := states["rename"]; !ok || !got {
		t.Fatalf("rename override = (%v,%v), want (true,true)", got, ok)
	}

	// Re-flipping conversion back on is an upsert, not a duplicate row.
	if err := st.SetCapabilityEnabled(ctx, "conversion", true, now); err != nil {
		t.Fatal(err)
	}
	states, _ = st.CapabilityStates(ctx)
	if !states["conversion"] {
		t.Fatal("conversion override = false after re-enable, want true")
	}
}

// TestSetCapabilityEnabledRejectsMalformedID proves the store validates the
// capability id slug shape so a malformed id can never reach the config table.
func TestSetCapabilityEnabledRejectsMalformedID(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	if err := st.SetCapabilityEnabled(ctx, "Bad ID", true, time.Unix(1700, 0).UTC()); err == nil {
		t.Fatal("SetCapabilityEnabled(\"Bad ID\") error = nil, want validation error")
	}
}

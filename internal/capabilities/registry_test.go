//go:build !beamfall_no_powertools && !beamfall_appstore

package capabilities

import "testing"

// stateByID indexes resolved states for assertion.
func stateByID(states []State) map[string]State {
	out := map[string]State{}
	for _, s := range states {
		out[s.ID] = s
	}
	return out
}

func TestResolveDefaultsEnableWorkflowCaps(t *testing.T) {
	states := stateByID(Resolve(nil))
	for _, id := range []string{"conversion", "rename"} {
		s, ok := states[id]
		if !ok {
			t.Fatalf("missing workflow capability %q", id)
		}
		if s.Kind != KindWorkflow {
			t.Fatalf("%s kind = %q, want %q", id, s.Kind, KindWorkflow)
		}
		if !s.Enabled {
			t.Fatalf("%s enabled = false, want true (default)", id)
		}
		if s.Reason != "" {
			t.Fatalf("%s reason = %q, want empty", id, s.Reason)
		}
	}
}

func TestResolveWorkflowOverrideDisables(t *testing.T) {
	states := stateByID(Resolve(map[string]bool{"conversion": false}))
	if states["conversion"].Enabled {
		t.Fatal("conversion enabled = true after override false, want false")
	}
	// The override is reflected in the convenience enabled-ID list.
	for _, id := range EnabledIDs(map[string]bool{"conversion": false}) {
		if id == "conversion" {
			t.Fatal("EnabledIDs still lists conversion after disable override")
		}
	}
	if !states["rename"].Enabled {
		t.Fatal("rename should remain enabled — override targeted only conversion")
	}
}

func TestProbeCapabilityReportsProbeAbsent(t *testing.T) {
	t.Setenv("BEAMFALL_PROBE_SHARE_MOUNT", "")
	s, ok := stateByID(Resolve(nil))["share-mount"]
	if !ok {
		t.Fatal("missing probe capability share-mount")
	}
	if s.Kind != KindProbe {
		t.Fatalf("share-mount kind = %q, want %q", s.Kind, KindProbe)
	}
	if s.Enabled {
		t.Fatal("share-mount enabled = true with probe absent, want false")
	}
	if s.Reason != ReasonProbeAbsent {
		t.Fatalf("share-mount reason = %q, want %q", s.Reason, ReasonProbeAbsent)
	}
	// A probe capability is auto-detected, never operator-flipped: an override is
	// ignored.
	if stateByID(Resolve(map[string]bool{"share-mount": true}))["share-mount"].Enabled {
		t.Fatal("share-mount enabled by operator override — probe caps must ignore overrides")
	}
}

func TestProbeCapabilityPresentWhenProbed(t *testing.T) {
	t.Setenv("BEAMFALL_PROBE_SHARE_MOUNT", "1")
	s := stateByID(Resolve(nil))["share-mount"]
	if !s.Enabled {
		t.Fatal("share-mount enabled = false with probe present, want true")
	}
	if s.Reason != "" {
		t.Fatalf("share-mount reason = %q, want empty when present", s.Reason)
	}
}

func TestWorkflowIDsExcludesProbeCaps(t *testing.T) {
	ids := WorkflowIDs()
	for _, id := range ids {
		if id == "share-mount" {
			t.Fatal("WorkflowIDs lists probe capability share-mount — must be flippable only")
		}
	}
	if len(ids) != 2 {
		t.Fatalf("WorkflowIDs = %v, want exactly conversion+rename", ids)
	}
}

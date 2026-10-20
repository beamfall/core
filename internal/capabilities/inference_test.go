//go:build !beamfall_no_powertools && !beamfall_appstore

package capabilities

import "testing"

// TestInferenceModelAbsentIsObservable is ADR-0127 Acceptance 9: with no model
// present the inference capability reports `enabled:false, reason:model-absent`
// — a discoverable disabled state, never a silently omitted capability
// indistinguishable from a broken install.
func TestInferenceModelAbsentIsObservable(t *testing.T) {
	t.Setenv("BEAMFALL_INFERENCE_MODEL_PRESENT", "")
	s, ok := stateByID(Resolve(nil))["inference"]
	if !ok {
		t.Fatal("inference capability missing from resolved states — must be discoverable")
	}
	if s.Kind != KindInference {
		t.Fatalf("inference kind = %q, want %q", s.Kind, KindInference)
	}
	if s.Enabled {
		t.Fatal("inference enabled = true with no model present, want false")
	}
	if s.Reason != ReasonModelAbsent {
		t.Fatalf("inference reason = %q, want %q", s.Reason, ReasonModelAbsent)
	}
	// The manifest entry carries a machine-comparable version even while disabled
	//: a client distinguishes "absent because too old"
	// from "present but model-absent".
	if s.Version != InferenceCapabilityVersion {
		t.Fatalf("inference version = %d, want %d", s.Version, InferenceCapabilityVersion)
	}
}

// TestInferenceModelPresentEnables proves a present model flips the capability
// on with no degraded reason.
func TestInferenceModelPresentEnables(t *testing.T) {
	t.Setenv("BEAMFALL_INFERENCE_MODEL_PRESENT", "1")
	s := stateByID(Resolve(nil))["inference"]
	if !s.Enabled {
		t.Fatal("inference enabled = false with a model present, want true")
	}
	if s.Reason != "" {
		t.Fatalf("inference reason = %q, want empty when the model is present", s.Reason)
	}
	// The enabled inference capability appears in the flat runtime enabled-ID list.
	found := false
	for _, id := range EnabledIDs(nil) {
		if id == "inference" {
			found = true
		}
	}
	if !found {
		t.Fatal("EnabledIDs omits inference when its model is present")
	}
}

// TestInferenceIgnoresOperatorOverride proves local model presence is
// auto-detected, never operator-flipped: an override cannot force it on, and it
// is excluded from the operator-flippable Workflow toggle set.
func TestInferenceIgnoresOperatorOverride(t *testing.T) {
	t.Setenv("BEAMFALL_INFERENCE_MODEL_PRESENT", "")
	if stateByID(Resolve(map[string]bool{"inference": true}))["inference"].Enabled {
		t.Fatal("inference enabled by operator override — model presence must not be operator-flipped")
	}
	for _, id := range WorkflowIDs() {
		if id == "inference" {
			t.Fatal("inference listed in WorkflowIDs — it is not an operator-flippable Workflow capability")
		}
	}
}

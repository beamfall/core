//go:build !beamfall_appstore

package graph

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestSetCapabilityEnabledTogglesAndAudits is the POWERTOOLS-9 vertical slice:
// the Workflow-capability toggle is Admin-Session-gated, audited
// + attributed, and the flip is reflected in the next capabilityStates read.
func TestSetCapabilityEnabledTogglesAndAudits(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	ctx := t.Context()

	// Without the Admin Session window open, the toggle is refused.
	_, errs := post(t, handler,
		`{"query":"mutation T($capability: String!, $enabled: Boolean!){ setCapabilityEnabled(capability:$capability, enabled:$enabled){ id enabled } }","variables":{"capability":"conversion","enabled":false}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("toggle without admin session errors = %#v, want admin-session gate", errs)
	}

	openManagerAdminSession(t, st)

	// conversion defaults enabled — flip it off.
	data, errs := post(t, handler,
		`{"query":"mutation T($capability: String!, $enabled: Boolean!){ setCapabilityEnabled(capability:$capability, enabled:$enabled){ id kind enabled flippable reason } }","variables":{"capability":"conversion","enabled":false}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setCapabilityEnabled errors = %#v", errs)
	}
	var states []CapabilityStateInfo
	mustUnmarshal(t, data["setCapabilityEnabled"], &states)
	if enabledOf(states, "conversion") {
		t.Fatal("conversion enabled = true after disable, want false (flip not reflected)")
	}

	// The flip is reflected in a fresh capabilityStates read.
	data, errs = post(t, handler,
		`{"query":"query { capabilityStates { id kind enabled flippable reason } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("capabilityStates errors = %#v", errs)
	}
	mustUnmarshal(t, data["capabilityStates"], &states)
	if enabledOf(states, "conversion") {
		t.Fatal("capabilityStates conversion enabled = true, want false after flip")
	}

	// The flip is audited + attributed: an activity entry records the toggle with
	// the capability id and an attributed session.
	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var audited *store.ActivityEntry
	for i := range entries {
		if entries[i].Action == store.ActivityCapabilityToggle {
			audited = &entries[i]
			break
		}
	}
	if audited == nil {
		t.Fatal("no capability.toggle activity entry — the flip must be audited")
	}
	if audited.TargetID != "conversion" || audited.TargetType != "capability" {
		t.Fatalf("audit entry target = (%q,%q), want (capability, conversion)", audited.TargetType, audited.TargetID)
	}
	if audited.SessionID == "" {
		t.Fatal("audit entry has no attribution (SessionID empty)")
	}
}

// TestSetCapabilityEnabledRejectsProbeAndUnknown proves a probe-derived or
// unknown capability id is a client error — probe caps are auto-detected, never
// operator-flipped, so the override table stays clean.
func TestSetCapabilityEnabledRejectsProbeAndUnknown(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)

	for _, id := range []string{"share-mount", "ghost"} {
		_, errs := post(t, handler,
			`{"query":"mutation T($capability: String!, $enabled: Boolean!){ setCapabilityEnabled(capability:$capability, enabled:$enabled){ id } }","variables":{"capability":"`+id+`","enabled":true}}`,
			authorizeManager)
		if len(errs) == 0 || !strings.Contains(errs[0].Message, "non-flippable") {
			t.Fatalf("toggle %q errors = %#v, want non-flippable client error", id, errs)
		}
	}
}

// TestCapabilityStatesReportsProbeAbsent proves the probe-derived capability is
// observable as enabled:false, reason:probe-absent and is not flippable
// (ADR-0124 §5 degraded observable).
func TestCapabilityStatesReportsProbeAbsent(t *testing.T) {
	t.Setenv("BEAMFALL_PROBE_SHARE_MOUNT", "")
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)

	data, errs := post(t, handler,
		`{"query":"query { capabilityStates { id kind enabled flippable reason } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("capabilityStates errors = %#v", errs)
	}
	var states []CapabilityStateInfo
	mustUnmarshal(t, data["capabilityStates"], &states)
	var share *CapabilityStateInfo
	for i := range states {
		if states[i].ID == "share-mount" {
			share = &states[i]
		}
	}
	if share == nil {
		t.Fatal("share-mount probe capability not reported")
	}
	if share.Enabled {
		t.Fatal("share-mount enabled = true, want false (probe absent)")
	}
	if share.Reason != "probe-absent" {
		t.Fatalf("share-mount reason = %q, want probe-absent", share.Reason)
	}
	if share.Flippable {
		t.Fatal("share-mount flippable = true, want false (probe caps never operator-flipped)")
	}
}

func enabledOf(states []CapabilityStateInfo, id string) bool {
	for _, s := range states {
		if s.ID == id {
			return s.Enabled
		}
	}
	return false
}

func TestClientContractRenderingProfileWireProjection(t *testing.T) {
	got, err := json.Marshal(ClientContractWireProjection{RenderingProfile: RenderingCapabilityProfile{
		VocabularyVersion: "1.0.0",
		Capabilities:      []string{"layout-browse", "section-grid"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"renderingProfile":{"vocabularyVersion":"1.0.0","capabilities":["layout-browse","section-grid"]}}`
	if string(got) != want {
		t.Fatalf("Client Contract wire projection = %s, want %s", got, want)
	}
}

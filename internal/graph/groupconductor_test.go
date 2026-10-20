package graph

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/conductor"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// newConductorHandler wires a test graph Handler with an Ambient Group Conductor
// driven by a fixed clock, so present_at and re-sync timestamps are deterministic.
// The conductor is the in-memory ephemeral Registry — nothing it holds is
// persisted, mirroring production wiring (app.SetConductor).
func newConductorHandler(st *store.Store, now time.Time) *Handler {
	h := newTestHandlerWithStore(st)
	h.SetConductor(conductor.NewRegistryWithClock(func() time.Time { return now }))
	return h
}

// formGroupID forms a group as the given authorizer and returns its id.
func formGroupID(t *testing.T, h *Handler, authorize func(*http.Request)) string {
	t.Helper()
	data, errs := post(t, h, `{"query":"mutation { formGroup { groupID members { sessionID } } }"}`, authorize)
	if len(errs) != 0 {
		t.Fatalf("formGroup errors = %#v", errs)
	}
	var info GroupInfo
	if err := json.Unmarshal(data["formGroup"], &info); err != nil {
		t.Fatalf("decode formGroup: %v", err)
	}
	if info.GroupID == "" {
		t.Fatal("formGroup returned an empty group id")
	}
	return info.GroupID
}

func joinGroupBody(groupID string, clockOffsetMS, renderLatencyMS int64) string {
	return `{"query":"mutation J($groupID: String!, $clockOffsetMS: Int!, $renderLatencyMS: Int!){ joinGroup(groupID:$groupID, clockOffsetMS:$clockOffsetMS, renderLatencyMS:$renderLatencyMS){ groupID members { sessionID clockOffsetMS renderLatencyMS } } }",` +
		`"variables":{"groupID":"` + groupID + `","clockOffsetMS":` + jsonNumber(clockOffsetMS) + `,"renderLatencyMS":` + jsonNumber(renderLatencyMS) + `}}`
}

func jsonNumber(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestGroupFormJoinSameProfile is the AV-4 membership + same-Profile acceptance
// (d): a device forms a group in its OWN Profile and joins it with
// its calibrated clock offset + render latency; the membership records the
// calibration. Group state is in-memory only.
//
// feature:ambient-group-conductor
func TestGroupFormJoinSameProfile(t *testing.T) {
	st := storetest.Open(t)
	h := newConductorHandler(st, time.Unix(1_700_000_000, 0).UTC())

	groupID := formGroupID(t, h, authorizeViewer) // session-default / profile-default

	data, errs := post(t, h, joinGroupBody(groupID, 200, 50), authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("joinGroup errors = %#v", errs)
	}
	var info GroupInfo
	if err := json.Unmarshal(data["joinGroup"], &info); err != nil {
		t.Fatalf("decode joinGroup: %v", err)
	}
	if len(info.Members) != 1 {
		t.Fatalf("group members = %d, want 1", len(info.Members))
	}
	m := info.Members[0]
	if m.SessionID != "session-default" {
		t.Errorf("member session = %q, want session-default", m.SessionID)
	}
	if m.ClockOffsetMS != 200 || m.RenderLatencyMS != 50 {
		t.Errorf("member calibration = %d/%d ms, want 200/50", m.ClockOffsetMS, m.RenderLatencyMS)
	}
}

// TestGroupCrossProfileRejected is the AV-4 same-Profile hard gate (d)
// (ADR-0035 §8, inheriting §7): a DIFFERENT-Profile device can neither join nor
// learn of another Profile's group — the group is reported "unknown group" with
// no existence leak.
//
// feature:ambient-group-conductor
func TestGroupCrossProfileRejected(t *testing.T) {
	st := storetest.Open(t)
	h := newConductorHandler(st, time.Unix(1_700_000_000, 0).UTC())

	groupID := formGroupID(t, h, authorizeViewer) // profile-default

	// The managed-Profile caller (session-managed / profile-managed) cannot join.
	_, errs := post(t, h, joinGroupBody(groupID, 0, 0), authorizeManaged)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown group") {
		t.Fatalf("cross-Profile join errors = %#v, want unknown group", errs)
	}
	// And cannot transition it.
	_, errs = post(t, h, transitionBody(groupID, "media-alpha", 0), authorizeManaged)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown group") {
		t.Fatalf("cross-Profile transition errors = %#v, want unknown group", errs)
	}
}

// TestGroupLeaveDissolves is the AV-4 membership-dynamics acceptance
// (ADR-0035 §8 "Leave"): a member leaves and the group dissolves when the last
// member is gone — a subsequent join sees an unknown group (ephemeral, nothing
// persisted).
//
// feature:ambient-group-conductor
func TestGroupLeaveDissolves(t *testing.T) {
	st := storetest.Open(t)
	h := newConductorHandler(st, time.Unix(1_700_000_000, 0).UTC())

	groupID := formGroupID(t, h, authorizeViewer)
	if _, errs := post(t, h, joinGroupBody(groupID, 0, 0), authorizeViewer); len(errs) != 0 {
		t.Fatalf("join errors = %#v", errs)
	}

	leaveBody := `{"query":"mutation L($groupID: String!){ leaveGroup(groupID:$groupID){ groupID } }","variables":{"groupID":"` + groupID + `"}}`
	data, errs := post(t, h, leaveBody, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("leaveGroup errors = %#v", errs)
	}
	var res LeaveGroupResult
	if err := json.Unmarshal(data["leaveGroup"], &res); err != nil {
		t.Fatalf("decode leaveGroup: %v", err)
	}
	if res.GroupID != groupID {
		t.Errorf("leaveGroup echo = %q, want %q", res.GroupID, groupID)
	}

	// The group dissolved: a re-join is now an unknown group.
	_, errs = post(t, h, joinGroupBody(groupID, 0, 0), authorizeViewer)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown group") {
		t.Fatalf("re-join of dissolved group errors = %#v, want unknown group", errs)
	}
}

func transitionBody(groupID, item string, positionMS int64) string {
	return `{"query":"mutation T($groupID: String!, $item: String!, $positionMS: Int!){ transitionGroup(groupID:$groupID, item:$item, positionMS:$positionMS){ groupID item positionMS presentAtMS } }",` +
		`"variables":{"groupID":"` + groupID + `","item":"` + item + `","positionMS":` + jsonNumber(positionMS) + `}}`
}

// TestGroupTransitionBroadcastReachesMembers is the AV-4 transition acceptance
// (b) (ADR-0035 §8 "Transition protocol"): a transition broadcasts
// {item, position, present_at} on the existing SSE outbox, Profile-gated; the
// payload carries a server-clock present_at each member converts via its own
// offset. The test forms+joins a group, transitions it, reads the REAL outbox,
// and converts present_at for a calibrated member exactly as the device would.
//
// feature:ambient-group-conductor
func TestGroupTransitionBroadcastReachesMembers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	groupID := formGroupID(t, h, authorizeViewer)
	if _, errs := post(t, h, joinGroupBody(groupID, 200, 50), authorizeViewer); len(errs) != 0 {
		t.Fatalf("join errors = %#v", errs)
	}

	data, errs := post(t, h, transitionBody(groupID, "media-alpha", 12000), authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("transitionGroup errors = %#v", errs)
	}
	var res GroupTransitionResult
	if err := json.Unmarshal(data["transitionGroup"], &res); err != nil {
		t.Fatalf("decode transitionGroup: %v", err)
	}
	wantPresent := now.Add(conductor.PreBufferWindow).UnixMilli()
	if res.Item != "media-alpha" || res.PositionMS != 12000 || res.PresentAtMS != wantPresent {
		t.Fatalf("transition result = %#v, want item media-alpha pos 12000 present %d", res, wantPresent)
	}

	// The transition landed on the REAL outbox as a group.sync transition event,
	// Profile-stamped for delivery.
	events, err := st.ReadEventOutbox(t.Context(), 0, 256)
	if err != nil {
		t.Fatalf("ReadEventOutbox: %v", err)
	}
	var payload store.GroupSyncPayload
	found := false
	for _, e := range events {
		if e.AggregateType != store.OutboxAggregateGroup {
			continue
		}
		found = true
		if e.Kind != store.GroupSyncKind {
			t.Errorf("group event kind = %q, want %q", e.Kind, store.GroupSyncKind)
		}
		if e.AggregateID != groupID {
			t.Errorf("group event aggregate = %q, want %q", e.AggregateID, groupID)
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
	}
	if !found {
		t.Fatal("no group.sync event reached the outbox")
	}
	if payload.Signal != store.GroupSignalTransition {
		t.Errorf("payload signal = %q, want %q", payload.Signal, store.GroupSignalTransition)
	}
	if payload.ProfileID != "profile-default" {
		t.Errorf("payload profile = %q, want profile-default (delivery gate)", payload.ProfileID)
	}
	if payload.Item != "media-alpha" || payload.PositionMS != 12000 || payload.PresentAtMS != wantPresent {
		t.Errorf("payload = %#v, want media-alpha/12000/%d", payload, wantPresent)
	}

	// Per-member conversion (the load-bearing acceptance): the member with the
	// 200 ms clock offset + 50 ms render latency converts the server present_at to
	// its own clock and backs off by its latency — exactly what the device does.
	member := conductor.Member{ClockOffset: 200 * time.Millisecond, RenderLatency: 50 * time.Millisecond}
	serverPresent := time.UnixMilli(payload.PresentAtMS).UTC()
	wantRelease := serverPresent.Add(200 * time.Millisecond).Add(-50 * time.Millisecond)
	if got := member.ReleaseAt(serverPresent); !got.Equal(wantRelease) {
		t.Errorf("member local release = %v, want %v", got, wantRelease)
	}
}

func reportDriftBody(groupID string, driftMS int64) string {
	return `{"query":"mutation D($groupID: String!, $driftMS: Int!){ reportDrift(groupID:$groupID, driftMS:$driftMS){ groupID action rate } }",` +
		`"variables":{"groupID":"` + groupID + `","driftMS":` + jsonNumber(driftMS) + `}}`
}

// TestGroupReportDriftNudgeAndResync is the AV-4 drift-correction acceptance (c)
// (ADR-0035 §8 "Drift correction"): a small drift produces a ±0.1 % rate nudge
// published to the reporting member on the SSE channel; a drift > 500 ms produces
// a full re-sync — a fresh transition broadcast. The test drives both through the
// mutation and confirms the real outbox signals.
//
// feature:ambient-group-conductor
func TestGroupReportDriftNudgeAndResync(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	groupID := formGroupID(t, h, authorizeViewer)
	if _, errs := post(t, h, joinGroupBody(groupID, 0, 0), authorizeViewer); len(errs) != 0 {
		t.Fatalf("join errors = %#v", errs)
	}
	if _, errs := post(t, h, transitionBody(groupID, "media-alpha", 0), authorizeViewer); len(errs) != 0 {
		t.Fatalf("transition errors = %#v", errs)
	}
	baseEvents, _ := st.ReadEventOutbox(t.Context(), 0, 256)
	cursor := int64(0)
	if n := len(baseEvents); n > 0 {
		cursor = baseEvents[n-1].Seq
	}

	// Small drift (the member is 80 ms AHEAD) → a slow-down nudge.
	data, errs := post(t, h, reportDriftBody(groupID, 80), authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("reportDrift(nudge) errors = %#v", errs)
	}
	var res ReportDriftResult
	if err := json.Unmarshal(data["reportDrift"], &res); err != nil {
		t.Fatalf("decode reportDrift: %v", err)
	}
	if res.Action != "nudge" || res.Rate != -conductor.NudgeRate {
		t.Fatalf("nudge result = %#v, want action nudge rate %v", res, -conductor.NudgeRate)
	}

	// The nudge landed on the outbox addressed to the reporting session.
	nudgeEvents, _ := st.ReadEventOutbox(t.Context(), cursor, 256)
	var nudgePayload store.GroupSyncPayload
	nudges := 0
	for _, e := range nudgeEvents {
		if e.AggregateType != store.OutboxAggregateGroup {
			continue
		}
		var p store.GroupSyncPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("decode nudge payload: %v", err)
		}
		if p.Signal == store.GroupSignalNudge {
			nudges++
			nudgePayload = p
		}
	}
	if nudges != 1 {
		t.Fatalf("nudge events = %d, want 1", nudges)
	}
	if nudgePayload.Session != "session-default" || nudgePayload.Rate != -conductor.NudgeRate {
		t.Errorf("nudge payload = %#v, want session-default / rate %v", nudgePayload, -conductor.NudgeRate)
	}

	cursor = lastSeq(t, st)

	// Unrecoverable drift (700 ms) → a full re-sync (a fresh transition).
	data, errs = post(t, h, reportDriftBody(groupID, 700), authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("reportDrift(resync) errors = %#v", errs)
	}
	if err := json.Unmarshal(data["reportDrift"], &res); err != nil {
		t.Fatalf("decode resync reportDrift: %v", err)
	}
	if res.Action != "resync" {
		t.Fatalf("resync result = %#v, want action resync", res)
	}
	resyncEvents, _ := st.ReadEventOutbox(t.Context(), cursor, 256)
	transitions := 0
	for _, e := range resyncEvents {
		if e.AggregateType != store.OutboxAggregateGroup {
			continue
		}
		var p store.GroupSyncPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("decode resync payload: %v", err)
		}
		if p.Signal == store.GroupSignalTransition {
			transitions++
		}
	}
	if transitions != 1 {
		t.Fatalf("resync transition events = %d, want 1 (full re-sync broadcast)", transitions)
	}
}

func lastSeq(t *testing.T, st *store.Store) int64 {
	t.Helper()
	events, err := st.ReadEventOutbox(t.Context(), 0, 1024)
	if err != nil {
		t.Fatalf("ReadEventOutbox: %v", err)
	}
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Seq
}

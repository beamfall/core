package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestAppendGroupSyncEventRoundTrip is the AV-4 store-side acceptance:
// a Group Conductor sync signal is recorded on the SAME outbox the
// SSE surface reads, as a "group"-aggregate row carrying the group id and a
// Profile-stamped, signal-typed payload. No new transport, no persisted group
// state — just one outbox row, exactly like a session-control event.
func TestAppendGroupSyncEventRoundTrip(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()

	if err := st.AppendGroupSyncEvent(ctx, GroupSyncPayload{
		GroupID:     "group-1",
		ProfileID:   "profile-default",
		Signal:      GroupSignalTransition,
		Item:        "media-alpha",
		PositionMS:  12000,
		PresentAtMS: now.Add(2 * time.Second).UnixMilli(),
	}, now); err != nil {
		t.Fatalf("AppendGroupSyncEvent: %v", err)
	}

	all, err := st.ReadEventOutbox(ctx, 0, 1000)
	if err != nil {
		t.Fatalf("ReadEventOutbox: %v", err)
	}
	var group []OutboxEvent
	for _, e := range all {
		if e.AggregateType == OutboxAggregateGroup {
			group = append(group, e)
		}
	}
	if len(group) != 1 {
		t.Fatalf("group outbox rows = %d, want 1", len(group))
	}
	e := group[0]
	if e.Kind != GroupSyncKind || e.AggregateID != "group-1" {
		t.Errorf("event kind/id = %q/%q, want %q/group-1", e.Kind, e.AggregateID, GroupSyncKind)
	}
	var p GroupSyncPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.ProfileID != "profile-default" || p.Signal != GroupSignalTransition || p.Item != "media-alpha" || p.PositionMS != 12000 {
		t.Errorf("payload = %#v, want profile-default/transition/media-alpha/12000", p)
	}
}

// TestAppendGroupSyncEventValidation confirms the write rejects a row missing its
// group id or Profile (the SSE delivery gate), or carrying an out-of-vocabulary
// signal — fail-closed, never a malformed outbox row.
func TestAppendGroupSyncEventValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()

	cases := []struct {
		name    string
		payload GroupSyncPayload
	}{
		{"missing group", GroupSyncPayload{ProfileID: "profile-default", Signal: GroupSignalTransition}},
		{"missing profile", GroupSyncPayload{GroupID: "group-1", Signal: GroupSignalTransition}},
		{"unknown signal", GroupSyncPayload{GroupID: "group-1", ProfileID: "profile-default", Signal: "warp"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := st.AppendGroupSyncEvent(ctx, tc.payload, now)
			var verr ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
		})
	}
}

package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// TestGroupSyncEventProfileGated is the AV-4 SSE-delivery acceptance (b)+(d) at
// the gate: a Group Conductor sync signal reaches ONLY a
// same-Profile subscriber, never a different-Profile one and never a non-Profile
// (admin/unbound) context. The group is same-Profile by construction, so the
// signal is Profile-gated and NOT reveal-filtered on a Media id — the position
// /anchor it carries is not catalog content. This is the same eventPermitted
// path the SSE stream delivers through, isolated to the decision function.
//
// feature:ambient-group-conductor
func TestGroupSyncEventProfileGated(t *testing.T) {
	srv, _ := newTestServerWithStore(t)
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()

	payload, err := json.Marshal(store.GroupSyncPayload{
		GroupID:    "group-1",
		ProfileID:  "profile-default",
		Signal:     store.GroupSignalTransition,
		Item:       "media-alpha",
		PositionMS: 0,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	event := store.OutboxEvent{
		Kind:          store.GroupSyncKind,
		AggregateType: store.OutboxAggregateGroup,
		AggregateID:   "group-1",
		Payload:       payload,
		OccurredAt:    now,
	}

	// The owning Profile receives it — under any filter, since a group signal is
	// Profile-gated, not reveal-gated. Use the narrowest (unrevealed) filter to
	// prove reveal is not consulted.
	if !srv.eventPermitted(ctx, event, "profile-default", eventsActionUnrevealedFilter()) {
		t.Fatal("eventPermitted denied a group.sync signal to its OWN Profile")
	}
	// A different Profile never observes it (the hard same-Profile gate).
	if srv.eventPermitted(ctx, event, "profile-managed", eventsActionRevealedFilter()) {
		t.Fatal("eventPermitted leaked a group.sync signal to a DIFFERENT Profile")
	}
	// A non-Profile context (admin token / unbound scoped key) never observes it.
	if srv.eventPermitted(ctx, event, "", eventsActionRevealedFilter()) {
		t.Fatal("eventPermitted leaked a group.sync signal to a non-Profile context")
	}

	// A malformed payload fails closed (never over-delivers).
	bad := event
	bad.Payload = []byte("{not json")
	if srv.eventPermitted(ctx, bad, "profile-default", eventsActionUnrevealedFilter()) {
		t.Fatal("eventPermitted allowed a group.sync signal with a malformed payload")
	}
}

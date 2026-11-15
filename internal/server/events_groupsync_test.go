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
// (admin/unbound) context. This is the same eventPermitted path the SSE stream
// delivers through, isolated to the decision function.
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

	// The owning Profile receives it, even unrevealed, when its item is not
	// restricted.
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

// TestGroupSyncEventRevealFiltered: a Watch Party writes one group.sync row per
// member Profile (CORE-1677), and transition and roster signals name the anchor's
// Media item. A signal naming restricted Media (media-archive, library-archive)
// never reaches an unrevealed same-Profile session, so party sync is not a reveal
// side-channel. A revealed session still receives it.
//
// feature:ambient-group-conductor
func TestGroupSyncEventRevealFiltered(t *testing.T) {
	srv, _ := newTestServerWithStore(t)
	ctx := context.Background()

	for _, signal := range []string{store.GroupSignalTransition, store.GroupSignalRoster} {
		payload, err := json.Marshal(store.GroupSyncPayload{
			GroupID:   "party-1",
			ProfileID: "profile-default",
			Signal:    signal,
			Item:      "media-archive",
			Members:   []string{"Alex"},
		})
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		event := store.OutboxEvent{
			Kind:          store.GroupSyncKind,
			AggregateType: store.OutboxAggregateGroup,
			AggregateID:   "party-1",
			Payload:       payload,
			OccurredAt:    time.Unix(0, 0).UTC(),
		}
		if srv.eventPermitted(ctx, event, "profile-default", eventsActionUnrevealedFilter()) {
			t.Fatalf("%s: eventPermitted leaked a restricted-media group.sync signal to an unrevealed session", signal)
		}
		if !srv.eventPermitted(ctx, event, "profile-default", eventsActionRevealedFilter()) {
			t.Fatalf("%s: eventPermitted denied a restricted-media group.sync signal to a revealed session", signal)
		}
	}
}

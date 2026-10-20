package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// TestInvalidationEventProfileGated is the PANEL-17 SSE-delivery gate: a
// server-initiated client-cache invalidation reaches ONLY the affected Profile,
// never a different Profile and never a non-Profile (admin/unbound) context. The
// signal names no Media (invariant #5: the payload carries only the affected
// Profile and a content-free Reason), so it is Profile-gated and NOT
// reveal-filtered. This is load-bearing: without the explicit branch the
// eventPermitted catch-all would deliver every non-Media aggregate to everyone.
func TestInvalidationEventProfileGated(t *testing.T) {
	srv, _ := newTestServerWithStore(t)
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()

	payload, err := json.Marshal(store.InvalidationPayload{
		ProfileID: "profile-managed",
		Reason:    store.InvalidationReasonAccessChanged,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	event := store.OutboxEvent{
		Kind:          store.InvalidationKind,
		AggregateType: store.OutboxAggregateInvalidation,
		AggregateID:   "profile-managed",
		Payload:       payload,
		OccurredAt:    now,
	}

	// The affected Profile receives it — under the narrowest (unrevealed) filter,
	// proving reveal is not consulted.
	if !srv.eventPermitted(ctx, event, "profile-managed", eventsActionUnrevealedFilter()) {
		t.Fatal("eventPermitted denied an invalidation to its OWN Profile")
	}
	// A different Profile never observes it (cross-Profile gate, fail-closed).
	if srv.eventPermitted(ctx, event, "profile-default", eventsActionRevealedFilter()) {
		t.Fatal("eventPermitted leaked an invalidation to a DIFFERENT Profile")
	}
	// A non-Profile context (admin token / unbound scoped key) never observes it.
	if srv.eventPermitted(ctx, event, "", eventsActionRevealedFilter()) {
		t.Fatal("eventPermitted leaked an invalidation to a non-Profile context")
	}
	// A malformed payload fails closed.
	bad := event
	bad.Payload = []byte("{not json")
	if srv.eventPermitted(ctx, bad, "profile-managed", eventsActionUnrevealedFilter()) {
		t.Fatal("eventPermitted allowed an invalidation with a malformed payload")
	}
}

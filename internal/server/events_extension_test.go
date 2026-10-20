package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// TestExtensionEventProfileGated is the PUSH-2 SSE-delivery acceptance (ADR-0039
// §2.3 step 6): an extension-request OUTCOME reaches ONLY the managed Profile it
// belongs to, never a different Profile and never a non-Profile (admin/unbound)
// context. The outcome names no Media (invariant #5: the payload carries only
// the request id, the managed Profile, and the resolved status), so it is
// Profile-gated and NOT reveal-filtered — the same eventPermitted path the SSE
// stream delivers through, isolated to the decision function.
func TestExtensionEventProfileGated(t *testing.T) {
	srv, _ := newTestServerWithStore(t)
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()

	payload, err := json.Marshal(store.ExtensionEventPayload{
		RequestID:    "extreq-1",
		ProfileID:    "profile-managed",
		Status:       string(store.ExtensionApproved),
		GrantMinutes: 30,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	event := store.OutboxEvent{
		Kind:          store.ExtensionEventKind,
		AggregateType: store.OutboxAggregateExtension,
		AggregateID:   "extreq-1",
		Payload:       payload,
		OccurredAt:    now,
	}

	// The managed Profile the outcome belongs to receives it — under the narrowest
	// (unrevealed) filter, proving reveal is not consulted.
	if !srv.eventPermitted(ctx, event, "profile-managed", eventsActionUnrevealedFilter()) {
		t.Fatal("eventPermitted denied an extension outcome to its OWN managed Profile")
	}
	// A different Profile never observes it (cross-Profile gate, fail-closed).
	if srv.eventPermitted(ctx, event, "profile-default", eventsActionRevealedFilter()) {
		t.Fatal("eventPermitted leaked an extension outcome to a DIFFERENT Profile")
	}
	// A non-Profile context (admin token / unbound scoped key) never observes it.
	if srv.eventPermitted(ctx, event, "", eventsActionRevealedFilter()) {
		t.Fatal("eventPermitted leaked an extension outcome to a non-Profile context")
	}
	// A malformed payload fails closed.
	bad := event
	bad.Payload = []byte("{not json")
	if srv.eventPermitted(ctx, bad, "profile-managed", eventsActionUnrevealedFilter()) {
		t.Fatal("eventPermitted allowed an extension outcome with a malformed payload")
	}
}

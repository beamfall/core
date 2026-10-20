package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestAppendInvalidationEvent covers PANEL-17: the server-initiated cache
// invalidation primitive writes a content-free, Profile-scoped outbox row and
// validates its inputs.
func TestAppendInvalidationEvent(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	if err := st.AppendInvalidationEvent(ctx, InvalidationPayload{
		ProfileID: "profile-default",
		Reason:    InvalidationReasonAccessChanged,
	}, time.Unix(0, 0).UTC()); err != nil {
		t.Fatalf("append: %v", err)
	}

	events, err := st.ReadEventOutbox(ctx, 0, 1000)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var found *OutboxEvent
	for i := range events {
		if events[i].Kind == InvalidationKind {
			found = &events[i]
			break
		}
	}
	if found == nil {
		t.Fatal("invalidation event not found in the outbox")
	}
	if found.AggregateType != OutboxAggregateInvalidation || found.AggregateID != "profile-default" {
		t.Fatalf("wrong aggregate: type=%q id=%q", found.AggregateType, found.AggregateID)
	}
	var payload InvalidationPayload
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload.ProfileID != "profile-default" || payload.Reason != InvalidationReasonAccessChanged {
		t.Fatalf("payload mismatch: %+v", payload)
	}
}

// TestAppendInvalidationEventValidation asserts the required-field + closed-vocab
// guards: a missing Profile and an unknown Reason both fail rather than writing a
// malformed (over-deliverable / off-vocabulary) outbox row.
func TestAppendInvalidationEventValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.AppendInvalidationEvent(ctx, InvalidationPayload{Reason: InvalidationReasonAccessChanged}, time.Unix(0, 0).UTC()); err == nil {
		t.Fatal("expected error for missing Profile")
	}
	if err := st.AppendInvalidationEvent(ctx, InvalidationPayload{ProfileID: "profile-default", Reason: "bogus"}, time.Unix(0, 0).UTC()); err == nil {
		t.Fatal("expected error for unknown reason")
	}
}

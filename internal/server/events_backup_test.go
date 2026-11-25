package server

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// TestBackupEventNeverReachesSSE pins that scheduled-backup outcome events
// (ADR-0192 §6) are webhook-only: the SSE catch-all would otherwise deliver a
// non-Media aggregate to every session.
func TestBackupEventNeverReachesSSE(t *testing.T) {
	srv, _ := newTestServerWithStore(t)
	event := store.OutboxEvent{
		Kind:          store.BackupEventFailed,
		AggregateType: store.OutboxAggregateBackup,
		Payload:       []byte(`{"failureClass":"destination-full"}`),
		OccurredAt:    time.Unix(0, 0).UTC(),
	}
	for _, profile := range []store.ProfileID{"", "profile-default", "profile-managed"} {
		if srv.eventPermitted(context.Background(), event, profile, eventsActionRevealedFilter()) {
			t.Fatalf("backup event delivered on SSE to profile %q", profile)
		}
	}
}

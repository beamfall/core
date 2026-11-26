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
	for _, kind := range []string{
		store.BackupEventCompleted,
		store.BackupEventFailed,
		store.BackupEventIntegrityFailed,
		store.BackupEventUpgradeBypassed,
		store.BackupEventUpgradeWithoutBackup,
		store.BackupEventUpdateStaged,
	} {
		event := store.OutboxEvent{
			Kind:          kind,
			AggregateType: store.OutboxAggregateBackup,
			Payload:       []byte(`{"failureClass":"destination-full","toVersion":"v1.1.0","policy":"soft"}`),
			OccurredAt:    time.Unix(0, 0).UTC(),
		}
		for _, profile := range []store.ProfileID{"", "profile-default", "profile-managed"} {
			if srv.eventPermitted(context.Background(), event, profile, eventsActionRevealedFilter()) {
				t.Fatalf("backup event %q delivered on SSE to profile %q", kind, profile)
			}
		}
	}
}

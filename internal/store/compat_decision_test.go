package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func compatInvalidation(subject string, now time.Time, ttl time.Duration) store.CompatDecisionInvalidation {
	return store.CompatDecisionInvalidation{
		Subject:    subject,
		Decision:   "client-too-old",
		ReasonCode: "revoked-build",
		RecordedAt: now,
		ExpiresAt:  now.Add(ttl),
	}
}

// A tombstone written by one process is readable by the next one over the same
// data dir. Without that, "invalidated" and "never handshook" stay the same
// observable across a restart.
func TestCompatDecisionInvalidationSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	now := time.Unix(1_700_000_000, 0).UTC()

	first := storetest.OpenAt(t, dataDir)
	if err := first.PutCompatDecisionInvalidation(ctx, compatInvalidation("session:doomed", now, time.Hour), now); err != nil {
		t.Fatalf("PutCompatDecisionInvalidation: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	second := storetest.OpenAt(t, dataDir)
	entries, err := second.CompatDecisionInvalidations(ctx, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CompatDecisionInvalidations: %v", err)
	}
	if len(entries) != 1 || entries[0].Subject != "session:doomed" {
		t.Fatalf("reopened invalidations = %+v, want one tombstone for session:doomed", entries)
	}
	if entries[0].Decision != "client-too-old" || entries[0].ReasonCode != "revoked-build" {
		t.Fatalf("reopened tombstone lost its refusal vocabulary: %+v", entries[0])
	}
}

// The persisted set has a bounded lifetime: a tombstone past its expiry is gone
// from the read AND deleted from the table, so the set cannot grow forever.
func TestCompatDecisionInvalidationExpires(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_700_000_000, 0).UTC()

	if err := st.PutCompatDecisionInvalidation(ctx, compatInvalidation("session:short", now, time.Minute), now); err != nil {
		t.Fatalf("PutCompatDecisionInvalidation: %v", err)
	}
	// Control: still in force one second before expiry.
	live, err := st.CompatDecisionInvalidations(ctx, now.Add(59*time.Second))
	if err != nil {
		t.Fatalf("CompatDecisionInvalidations: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("tombstone before expiry = %+v, want it still in force", live)
	}

	expired, err := st.CompatDecisionInvalidations(ctx, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("CompatDecisionInvalidations: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("tombstone after expiry = %+v, want it pruned", expired)
	}
	// The prune deleted the row rather than filtering it out of one read: a
	// later read at a time BEFORE the expiry must not resurrect it.
	resurrected, err := st.CompatDecisionInvalidations(ctx, now)
	if err != nil {
		t.Fatalf("CompatDecisionInvalidations: %v", err)
	}
	if len(resurrected) != 0 {
		t.Fatalf("pruned tombstone came back = %+v", resurrected)
	}
}

func TestCompatDecisionInvalidationClearAndReplace(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_700_000_000, 0).UTC()

	if err := st.PutCompatDecisionInvalidation(ctx, compatInvalidation("session:one", now, time.Hour), now); err != nil {
		t.Fatalf("PutCompatDecisionInvalidation: %v", err)
	}
	refreshed := compatInvalidation("session:one", now.Add(time.Minute), 2*time.Hour)
	refreshed.ReasonCode = "unknown-surface"
	if err := st.PutCompatDecisionInvalidation(ctx, refreshed, now.Add(time.Minute)); err != nil {
		t.Fatalf("refresh PutCompatDecisionInvalidation: %v", err)
	}
	entries, err := st.CompatDecisionInvalidations(ctx, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CompatDecisionInvalidations: %v", err)
	}
	if len(entries) != 1 || entries[0].ReasonCode != "unknown-surface" {
		t.Fatalf("refresh left %+v, want a single updated row per subject", entries)
	}

	if err := st.DeleteCompatDecisionInvalidation(ctx, "session:one"); err != nil {
		t.Fatalf("DeleteCompatDecisionInvalidation: %v", err)
	}
	entries, err = st.CompatDecisionInvalidations(ctx, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CompatDecisionInvalidations: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("cleared invalidations = %+v, want none", entries)
	}
}

func TestCompatDecisionInvalidationRejectsUnusableInput(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_700_000_000, 0).UTC()

	if err := st.PutCompatDecisionInvalidation(ctx, compatInvalidation("", now, time.Hour), now); err == nil {
		t.Fatal("PutCompatDecisionInvalidation accepted an empty subject")
	}
	// A tombstone that is already expired at write time would be pruned by its
	// own write and silently record nothing.
	if err := st.PutCompatDecisionInvalidation(ctx, compatInvalidation("session:stale", now, -time.Hour), now); err == nil {
		t.Fatal("PutCompatDecisionInvalidation accepted an already-expired tombstone")
	}
	if err := st.DeleteCompatDecisionInvalidation(ctx, ""); err == nil {
		t.Fatal("DeleteCompatDecisionInvalidation accepted an empty subject")
	}
}

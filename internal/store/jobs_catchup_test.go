package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestCatchUpDedupeKeepsPendingGenerationThroughCompletion(t *testing.T) {
	for _, floor := range []int64{100, 101, 102} {
		t.Run(fmt.Sprintf("new-floor-%d", floor), func(t *testing.T) {
			ctx := context.Background()
			st, err := Open(ctx, migratedDataDir(t), Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			now := time.Now().UTC()
			enqueue := func(floor int64) Job {
				t.Helper()
				job, err := st.EnqueueJob(ctx, Job{
					Kind: "asset-catch-up", DedupeKey: "asset-catch-up", CreatedAt: now,
					Payload: fmt.Appendf(nil, `{"version":1,"after_rowid":%d}`, floor),
				})
				if err != nil {
					t.Fatal(err)
				}
				return job
			}
			created := enqueue(101)
			claimed, err := st.ClaimNextJob(ctx, "worker", nil, now)
			if err != nil {
				t.Fatal(err)
			}
			// The worker has consumed its snapshot, but has not completed its claim.
			checkpoint := []byte(`{"version":1,"after_rowid":101,"last_rowid":102}`)
			if err := st.UpdateJobCheckpoint(ctx, claimed.ID, claimed.LeaseOwner, checkpoint, JobProgress{}, now); err != nil {
				t.Fatal(err)
			}
			coalesced := enqueue(floor)
			if coalesced.ID != created.ID {
				t.Fatal("dedupe created a second active job")
			}
			var payload struct {
				AfterRowID int64 `json:"after_rowid"`
				Generation int64 `json:"generation"`
			}
			if err := json.Unmarshal(coalesced.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.AfterRowID != min(floor, 101) || payload.Generation != 1 {
				t.Fatalf("coalesced payload = %+v", payload)
			}
			// A stale checkpoint write must not acknowledge the new request.
			if err := st.UpdateJobCheckpoint(ctx, claimed.ID, claimed.LeaseOwner, checkpoint, JobProgress{}, now); err != nil {
				t.Fatal(err)
			}
			if err := st.CompleteJob(ctx, claimed.ID, claimed.LeaseOwner, now); err != nil {
				t.Fatal(err)
			}
			pending, err := st.Job(ctx, claimed.ID)
			if err != nil || pending.Status != JobStatusYielded || !pending.FinishedAt.IsZero() {
				t.Fatalf("pending = %+v, %v", pending, err)
			}
			resumed, err := st.ClaimNextJob(ctx, "worker", nil, now)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.CompleteJob(ctx, claimed.ID, claimed.LeaseOwner, now); !errors.Is(err, ErrJobLeaseLost) {
				t.Fatalf("stale completion = %v", err)
			}
			checkpoint = fmt.Appendf(nil, `{"version":1,"after_rowid":%d,"generation":1,"last_rowid":103}`, payload.AfterRowID)
			if err := st.UpdateJobCheckpoint(ctx, resumed.ID, resumed.LeaseOwner, checkpoint, JobProgress{}, now); err != nil {
				t.Fatal(err)
			}
			if err := st.CompleteJob(ctx, resumed.ID, resumed.LeaseOwner, now); err != nil {
				t.Fatal(err)
			}
			completed, err := st.Job(ctx, resumed.ID)
			if err != nil || completed.Status != JobStatusCompleted {
				t.Fatalf("completed = %+v, %v", completed, err)
			}
		})
	}
}

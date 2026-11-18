package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestRetryOfCancelledJobResumesFromItsCheckpointAndProgress covers CORE-2395:
// retrying a scan cancelled mid-run starts at the cursor and progress it had
// reached, not at 0, so the Console does not show it starting over.
func TestRetryOfCancelledJobResumesFromItsCheckpointAndProgress(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Now().UTC()
	if _, err := st.EnqueueJob(ctx, store.Job{ID: "scan-1", Kind: "scan", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	job, err := st.ClaimNextJob(ctx, "worker-a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	progress := store.JobProgress{Current: 7, Total: 10, Unit: "files", Message: "scanning"}
	if err := st.UpdateJobCheckpoint(ctx, job.ID, job.LeaseOwner, []byte(`{"cursor":"f7"}`), progress, now); err != nil {
		t.Fatal(err)
	}
	if err := st.CancelJob(ctx, job.ID, now); err != nil {
		t.Fatal(err)
	}

	retry, err := st.RetryJob(ctx, job.ID, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := st.Job(ctx, retry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.Checkpoint) != `{"cursor":"f7"}` {
		t.Fatalf("retry checkpoint = %s, want the cancelled job's cursor", stored.Checkpoint)
	}
	if stored.Progress != progress {
		t.Fatalf("retry progress = %+v, want the cancelled job's %+v", stored.Progress, progress)
	}
	if stored.Status != store.JobStatusQueued {
		t.Fatalf("retry status = %s, want queued", stored.Status)
	}
}

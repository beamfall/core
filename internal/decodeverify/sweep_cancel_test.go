package decodeverify

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type cancelVerifier struct {
	calls  int
	cancel context.CancelFunc
}

func (v *cancelVerifier) DecodeVerify(ctx context.Context, _ string, _ int) (bool, error) {
	v.calls++
	if v.calls == 2 {
		v.cancel()
		return false, ctx.Err()
	}
	return false, nil
}

func TestSweepCancellationPreservesCompletedCheckpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(ctx, storetest.MigratedDataDir(t), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateUngrantedLibraryWithRoots(ctx, "Local", false, "", nil, ""); err != nil {
		t.Fatal(err)
	}
	seedVideo(t, st, "first", "/m/first.mp4")
	seedVideo(t, st, "second", "/m/second.mp4")
	ids, err := st.MediaIDsAfter(ctx, "", 10)
	if err != nil || len(ids) != 2 {
		t.Fatalf("fixture IDs = %v, %v", ids, err)
	}
	job, err := NewSweepJob(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	job, err = st.ClaimNextJob(ctx, "worker", nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	verifier := &cancelVerifier{cancel: cancel}
	result, err := SweepHandler(NewService(st, verifier))(ctx, job)
	if !errors.Is(err, jobs.ErrYielded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("handler error = %v", err)
	}
	if verifier.calls != 2 || ctx.Err() == nil {
		t.Fatal("cancellation did not interrupt the second item")
	}
	var checkpoint sweepCheckpoint
	if err := json.Unmarshal(result.Checkpoint, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.LastID != string(ids[0]) || checkpoint.Sound != 1 || checkpoint.Failed != 0 {
		t.Fatalf("completed checkpoint = %+v, want first ID %s and one sound item", checkpoint, ids[0])
	}
}

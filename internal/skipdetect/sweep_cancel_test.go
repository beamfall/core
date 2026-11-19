package skipdetect

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type cancelDetector struct {
	fakeDetector
	calls  int
	cancel context.CancelFunc
}

func (d *cancelDetector) Chapters(ctx context.Context, _ string) ([]ffmpeg.Chapter, error) {
	d.calls++
	if d.calls == 2 {
		d.cancel()
		return nil, ctx.Err()
	}
	return nil, nil
}

func TestSweepCancellationPreservesCompletedCheckpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(ctx, storetest.MigratedDataDir(t), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	lib, err := st.CreateUngrantedLibraryWithRoots(ctx, "Local", false, "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	enable(t, st, lib.ID)
	seedVideo(t, st, lib.ID, "first", 100)
	seedVideo(t, st, lib.ID, "second", 100)
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
	detector := &cancelDetector{cancel: cancel}
	result, err := SweepHandler(NewService(st, detector))(ctx, job)
	if !errors.Is(err, jobs.ErrYielded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("handler error = %v", err)
	}
	if detector.calls != 2 || ctx.Err() == nil {
		t.Fatal("cancellation did not interrupt the second item")
	}
	var checkpoint sweepCheckpoint
	if err := json.Unmarshal(result.Checkpoint, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.LastID != string(ids[0]) || checkpoint.Detected != 1 || checkpoint.Failed != 0 {
		t.Fatalf("completed checkpoint = %+v, want first ID %s and one detected item", checkpoint, ids[0])
	}
}

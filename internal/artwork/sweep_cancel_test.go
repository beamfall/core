package artwork

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

type cancelGrabber struct {
	fakeGrabber
	cancel context.CancelFunc
}

func (g *cancelGrabber) FrameGrab(ctx context.Context, src string, at float64, width int, dst string) error {
	if len(g.calls) == 1 {
		g.calls = append(g.calls, src)
		g.cancel()
		return ctx.Err()
	}
	return g.fakeGrabber.FrameGrab(ctx, src, at, width, dst)
}

func TestSweepCancellationPreservesCompletedCheckpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := storetest.Open(t)
	setPrimaryFile(t, st, "media-alpha")
	setPrimaryFile(t, st, "media-archive")
	job, err := NewSelectedSweepJob([]store.MediaID{"media-alpha", "media-archive"}, time.Now().UTC())
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
	grabber := &cancelGrabber{cancel: cancel}
	svc := NewService(st, grabber, t.TempDir())
	result, err := SweepHandler(svc)(ctx, job)
	if !errors.Is(err, jobs.ErrYielded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("handler error = %v", err)
	}
	if len(grabber.calls) != 2 || ctx.Err() == nil {
		t.Fatal("cancellation did not interrupt the second item")
	}
	if !svc.HasPoster("media-alpha") {
		t.Fatal("first item did not complete")
	}
	var checkpoint sweepCheckpoint
	if err := json.Unmarshal(result.Checkpoint, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.LastID != "media-alpha" || checkpoint.Generated != 1 || checkpoint.Failed != 0 {
		t.Fatalf("completed checkpoint = %+v, want media-alpha and one generated item", checkpoint)
	}
}

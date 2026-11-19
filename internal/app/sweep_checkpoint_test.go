package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/beamfall/core/internal/decodeverify"
	"github.com/beamfall/core/internal/governor"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type sweepPreemptVerifier struct {
	calls   int
	preempt func()
}

func (v *sweepPreemptVerifier) DecodeVerify(ctx context.Context, _ string, _ int) (bool, error) {
	v.calls++
	if v.calls == 2 {
		v.preempt()
		return false, ctx.Err()
	}
	return false, nil
}

func TestGovernedSweepPreservesActualHandlerProgress(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprintf("after-completion-%v", late), func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(ctx, storetest.MigratedDataDir(t), store.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			lib, err := st.CreateUngrantedLibraryWithRoots(ctx, "Local", false, "", nil, "")
			if err != nil {
				t.Fatal(err)
			}
			for i := range 2 {
				if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
					LibraryID: lib.ID, Path: fmt.Sprintf("/m/item-%d.mp4", i), Title: fmt.Sprintf("Item %d", i),
					Container: "mp4", Codec: "h264", Class: store.MediaClassVideo,
					ContentFingerprint: fmt.Sprintf("sha256:item-%d", i), DurationSecondsHint: 40,
				}); err != nil {
					t.Fatal(err)
				}
			}
			ids, err := st.MediaIDsAfter(ctx, "", 10)
			if err != nil || len(ids) != 2 {
				t.Fatalf("fixture IDs = %v, %v", ids, err)
			}
			job, err := decodeverify.NewSweepJob(time.Now().UTC())
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
			g := governor.New()
			preempt := func() {
				lease, ok := g.BeginScan(ctx)
				if !ok {
					t.Fatal("scan admission refused")
				}
				t.Cleanup(lease.Release)
			}
			verifier := &sweepPreemptVerifier{preempt: func() {
				if !late {
					preempt()
				}
			}}
			actual := decodeverify.SweepHandler(decodeverify.NewService(st, verifier))
			handler := governedSweep(g, nil, func(ctx context.Context, job store.Job) (jobs.Result, error) {
				result, err := actual(ctx, job)
				if late {
					preempt()
				}
				return result, err
			})
			result, err := handler(ctx, job)
			if verifier.calls != 2 || !errors.Is(err, jobs.ErrYielded) || result.RunAfter.IsZero() {
				t.Fatalf("preemption = %+v, %v, calls %d", result, err, verifier.calls)
			}
			var checkpoint struct {
				LastID string `json:"last_id"`
				Sound  int    `json:"sound"`
			}
			if err := json.Unmarshal(result.Checkpoint, &checkpoint); err != nil {
				t.Fatal(err)
			}
			wantCount := 1
			if late {
				wantCount = 2
			}
			if checkpoint.LastID != string(ids[wantCount-1]) || checkpoint.Sound != wantCount {
				t.Fatalf("yielded checkpoint = %+v, want completed item %s", checkpoint, ids[wantCount-1])
			}
		})
	}
}

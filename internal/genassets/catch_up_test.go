package genassets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

type catchUpGenerator struct {
	fakeGen
	paths      []string
	cancel     context.CancelFunc
	onWaveform func()
}

func (g *catchUpGenerator) WaveformAmplitudes(ctx context.Context, src string) ([]float64, error) {
	g.paths = append(g.paths, src)
	if g.onWaveform != nil {
		g.onWaveform()
	}
	if g.cancel != nil {
		g.cancel()
		return nil, ctx.Err()
	}
	return []float64{0.2, 0.8}, nil
}

func TestCatchUpOnlyVisitsNewMediaAndResumesCheckpoint(t *testing.T) {
	ctx := context.Background()
	gen := &catchUpGenerator{}
	svc, st, oldFingerprint := newTestService(t, gen)
	floor, err := st.MaxMediaRowID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var wantPaths []string
	for i := range sweepJobBatchSize + 2 {
		path := filepath.Join(t.TempDir(), "track.mp3")
		if err := os.WriteFile(path, fmt.Appendf(nil, "catch-up audio %d", i), 0o600); err != nil {
			t.Fatal(err)
		}
		seedAudioMedia(t, st, path, fmt.Sprintf("New %d", i))
		wantPaths = append(wantPaths, path)
	}
	now := time.Now().UTC()
	job, err := NewCatchUpJob(now, floor)
	if err != nil {
		t.Fatal(err)
	}
	if job.Kind != CatchUpJobKind || job.Class != store.JobClassBackground || job.DedupeKey != string(CatchUpJobKind) || !job.CreatedAt.Equal(now) {
		t.Fatalf("catch-up job = %#v", job)
	}
	if string(job.Payload) != fmt.Sprintf(`{"version":1,"after_rowid":%d}`, floor) {
		t.Fatalf("payload = %s", job.Payload)
	}
	created, err := st.EnqueueJob(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := st.EnqueueJob(ctx, job)
	if err != nil || duplicate.ID != created.ID {
		t.Fatalf("dedupe = %s, %v, want %s", duplicate.ID, err, created.ID)
	}
	registry := jobs.NewRegistry(st, "catch-up-worker")
	registry.Register(CatchUpJobKind, CatchUpHandler(svc))
	if _, err := registry.RunOne(ctx, nil, now); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if !reflect.DeepEqual(gen.paths, wantPaths[:sweepJobBatchSize]) {
		t.Fatalf("first batch = %v, want %v", gen.paths, wantPaths[:sweepJobBatchSize])
	}
	persisted, err := st.Job(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != store.JobStatusYielded {
		t.Fatalf("first batch status = %s, want yielded", persisted.Status)
	}
	var checkpoint catchUpCheckpoint
	if err := json.Unmarshal(persisted.Checkpoint, &checkpoint); err != nil {
		t.Fatal(err)
	}
	refs, err := st.MediaIDsAfterRowID(ctx, floor, sweepJobBatchSize)
	if err != nil || len(refs) != sweepJobBatchSize {
		t.Fatalf("first rows = %v, %v", refs, err)
	}
	if checkpoint.LastRowID != refs[len(refs)-1].RowID || checkpoint.Generated != sweepJobBatchSize {
		t.Fatalf("checkpoint = %+v", checkpoint)
	}
	// Recreate the registry/handler so resumption depends on the durable cursor.
	registry = jobs.NewRegistry(st, "resumed-worker")
	registry.Register(CatchUpJobKind, CatchUpHandler(svc))
	if _, err := registry.RunOne(ctx, nil, now.Add(time.Hour)); err != nil {
		t.Fatalf("resumed batch: %v", err)
	}
	if !reflect.DeepEqual(gen.paths, wantPaths) {
		t.Fatalf("visited = %v, want %v", gen.paths, wantPaths)
	}
	if gen.spriteCalls != 0 {
		t.Fatal("catch-up visited pre-floor video")
	}
	if _, ok := svc.TrickplayAsset(oldFingerprint); ok {
		t.Fatal("catch-up generated assets below the floor")
	}
}

func TestCatchUpRetriesInterruptedItem(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gen := &catchUpGenerator{cancel: cancel}
	svc, st, _ := newTestService(t, gen)
	floor, err := st.MaxMediaRowID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "interrupted.mp3")
	if err := os.WriteFile(path, []byte("interrupt catch-up"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedAudioMedia(t, st, path, "Interrupted")
	job, err := NewCatchUpJob(time.Now().UTC(), floor)
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
	handler := CatchUpHandler(svc)
	result, err := handler(ctx, job)
	if !errors.Is(err, jobs.ErrYielded) || len(gen.paths) != 1 {
		t.Fatalf("interrupted = %v, calls %v", err, gen.paths)
	}
	var checkpoint catchUpCheckpoint
	if err := json.Unmarshal(result.Checkpoint, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.LastRowID != floor || checkpoint.Generated != 0 || checkpoint.Failed != 0 {
		t.Fatalf("interrupted item advanced checkpoint: %+v", checkpoint)
	}
	gen.cancel = nil
	job.Checkpoint = result.Checkpoint
	if _, err := handler(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gen.paths, []string{path, path}) {
		t.Fatalf("interrupted item was not retried: %v", gen.paths)
	}
}

// A scan with the higher watermark can finish first. Exercise both stale
// checkpoint writes and the gap between the last checkpoint and completion.
func TestCatchUpCoalescesDescendingFloors(t *testing.T) {
	for _, phase := range []string{"queued", "running-before-batch", "running-during-batch", "running-after-checkpoint", "yielded"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			gen := &catchUpGenerator{}
			svc, st, _ := newTestService(t, gen)
			floor, err := st.MaxMediaRowID(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for floor < 100 {
				if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
					LibraryID: "library-local", Path: fmt.Sprintf("/m/prefix-%d", floor), Title: fmt.Sprintf("Prefix %d", floor),
					Container: "mp3", Codec: "mp3", Class: store.MediaClassAudio, ContentFingerprint: fmt.Sprintf("prefix-%d", floor),
				}); err != nil {
					t.Fatal(err)
				}
				floor++
			}
			var paths []string
			for i := range 2 {
				path := filepath.Join(t.TempDir(), "track.mp3")
				if err := os.WriteFile(path, fmt.Appendf(nil, "descending %d", i), 0o600); err != nil {
					t.Fatal(err)
				}
				seedAudioMedia(t, st, path, fmt.Sprintf("Descending %d", i))
				paths = append(paths, path)
			}
			refs, err := st.MediaIDsAfterRowID(ctx, 100, 3)
			if err != nil || len(refs) != 2 || refs[0].RowID != 101 || refs[1].RowID != 102 {
				t.Fatalf("fixture refs = %+v, %v", refs, err)
			}
			now := time.Now().UTC()
			high, err := NewCatchUpJob(now, 101)
			if err != nil {
				t.Fatal(err)
			}
			created, err := st.EnqueueJob(ctx, high)
			if err != nil {
				t.Fatal(err)
			}
			enqueueLower := func() {
				low, err := NewCatchUpJob(now, 100)
				if err != nil {
					t.Fatal(err)
				}
				got, err := st.EnqueueJob(ctx, low)
				if err != nil || got.ID != created.ID {
					t.Fatalf("coalesced = %+v, %v", got, err)
				}
				var payload catchUpPayload
				if err := json.Unmarshal(got.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.AfterRowID != 100 {
					t.Fatalf("floor = %d, want 100", payload.AfterRowID)
				}
			}
			if phase == "queued" {
				enqueueLower()
			} else {
				claimed, err := st.ClaimNextJob(ctx, "first-worker", nil, now)
				if err != nil {
					t.Fatal(err)
				}
				if phase == "running-before-batch" {
					enqueueLower()
				}
				if phase == "running-during-batch" {
					gen.onWaveform = enqueueLower
				}
				if _, err := CatchUpHandler(svc)(ctx, claimed); err != nil {
					t.Fatal(err)
				}
				gen.onWaveform = nil
				persisted, err := st.Job(ctx, claimed.ID)
				if err != nil {
					t.Fatal(err)
				}
				var checkpoint catchUpCheckpoint
				if err := json.Unmarshal(persisted.Checkpoint, &checkpoint); err != nil {
					t.Fatal(err)
				}
				if checkpoint.LastRowID != 102 {
					t.Fatalf("first claim did not checkpoint past 101: %+v", checkpoint)
				}
				if phase == "yielded" {
					if err := st.YieldJob(ctx, claimed.ID, claimed.LeaseOwner, persisted.Checkpoint, now, now); err != nil {
						t.Fatal(err)
					}
					enqueueLower()
				} else {
					if phase == "running-after-checkpoint" {
						enqueueLower()
					}
					if err := st.CompleteJob(ctx, claimed.ID, claimed.LeaseOwner, now); err != nil {
						t.Fatal(err)
					}
					pending, err := st.Job(ctx, claimed.ID)
					if err != nil || pending.Status != store.JobStatusYielded {
						t.Fatalf("pending lower floor lost: %+v, %v", pending, err)
					}
				}
			}
			registry := jobs.NewRegistry(st, "resumed-worker")
			registry.Register(CatchUpJobKind, CatchUpHandler(svc))
			if _, err := registry.RunOne(ctx, nil, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, path := range gen.paths {
				found = found || path == paths[0]
			}
			if !found {
				t.Fatalf("Media 101 was skipped: visited %v", gen.paths)
			}
			finished, err := st.Job(ctx, created.ID)
			if err != nil || finished.Status != store.JobStatusCompleted {
				t.Fatalf("final job = %+v, %v", finished, err)
			}
		})
	}
}

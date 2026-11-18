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
	floor, err := st.MaxMediaInsertSeq(ctx)
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
	if string(job.Payload) != fmt.Sprintf(`{"version":2,"after_rowid":%d}`, floor) {
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
	refs, err := st.MediaIDsAfterInsertSeq(ctx, floor, sweepJobBatchSize)
	if err != nil || len(refs) != sweepJobBatchSize {
		t.Fatalf("first rows = %v, %v", refs, err)
	}
	if checkpoint.LastInsertSeq != refs[len(refs)-1].InsertSeq || checkpoint.Generated != sweepJobBatchSize {
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
	floor, err := st.MaxMediaInsertSeq(ctx)
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
	if checkpoint.LastInsertSeq != floor || checkpoint.Generated != 0 || checkpoint.Failed != 0 {
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
			floor, err := st.MaxMediaInsertSeq(ctx)
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
			refs, err := st.MediaIDsAfterInsertSeq(ctx, 100, 3)
			if err != nil || len(refs) != 2 || refs[0].InsertSeq != 101 || refs[1].InsertSeq != 102 {
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
				if payload.AfterInsertSeq != 100 {
					t.Fatalf("floor = %d, want 100", payload.AfterInsertSeq)
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
				if checkpoint.LastInsertSeq != 102 {
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

func TestCatchUpVisitsReplacementAfterSavedCursor(t *testing.T) {
	for _, phase := range []string{"yielded", "running"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			gen := &catchUpGenerator{}
			svc, st, _ := newTestService(t, gen)
			floor, err := st.MaxMediaInsertSeq(ctx)
			if err != nil {
				t.Fatal(err)
			}
			importAudio := func(name string) (store.Media, string) {
				t.Helper()
				path := filepath.Join(t.TempDir(), name+".mp3")
				if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
					t.Fatal(err)
				}
				media, _ := seedAudioMedia(t, st, path, name)
				return media, path
			}
			old, oldPath := importAudio("purged")
			now := time.Now().UTC()
			job, err := NewCatchUpJob(now, floor)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.EnqueueJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			claimed, err := st.ClaimNextJob(ctx, "first-worker", nil, now)
			if err != nil {
				t.Fatal(err)
			}
			result, err := CatchUpHandler(svc)(ctx, claimed)
			if err != nil {
				t.Fatal(err)
			}
			var saved catchUpCheckpoint
			if err := json.Unmarshal(result.Checkpoint, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.LastInsertSeq <= floor || saved.Generated != 1 || !reflect.DeepEqual(gen.paths, []string{oldPath}) {
				t.Fatalf("fixture did not save completed cursor: %+v, paths %v", saved, gen.paths)
			}
			if phase == "yielded" {
				if err := st.YieldJob(ctx, claimed.ID, claimed.LeaseOwner, result.Checkpoint, now, now); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.TrashMedia(ctx, old.ID, store.LibraryFilter{AllLibraries: true, Revealed: true}); err != nil {
				t.Fatal(err)
			}
			if count, err := st.PurgeTrashedBefore(ctx, now.Add(time.Hour)); err != nil || count != 1 {
				t.Fatalf("purge = %d, %v", count, err)
			}
			fresh, freshPath := importAudio("replacement")
			refs, err := st.MediaIDsAfterInsertSeq(ctx, saved.LastInsertSeq, 10)
			if err != nil || len(refs) != 1 || refs[0].ID != fresh.ID {
				t.Fatalf("replacement beyond saved cursor = %+v, %v", refs, err)
			}
			if phase == "running" {
				// A post-scan enqueue arriving after the checkpoint must keep the
				// active job runnable through completion of its stale claim.
				next, err := NewCatchUpJob(now, saved.LastInsertSeq)
				if err != nil {
					t.Fatal(err)
				}
				coalesced, err := st.EnqueueJob(ctx, next)
				if err != nil || coalesced.ID != claimed.ID {
					t.Fatalf("coalesce = %+v, %v", coalesced, err)
				}
				if err := st.CompleteJob(ctx, claimed.ID, claimed.LeaseOwner, now); err != nil {
					t.Fatal(err)
				}
			}
			registry := jobs.NewRegistry(st, "resumed-worker")
			registry.Register(CatchUpJobKind, CatchUpHandler(svc))
			if _, err := registry.RunOne(ctx, nil, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gen.paths, []string{oldPath, freshPath}) {
				t.Fatalf("visited %v, want replacement", gen.paths)
			}
			finished, err := st.Job(ctx, claimed.ID)
			if err != nil || finished.Status != store.JobStatusCompleted {
				t.Fatalf("final job = %+v, %v", finished, err)
			}
		})
	}
}

func TestCatchUpLegacyCursorRestartsAndResumes(t *testing.T) {
	for _, phase := range []string{"queued", "yielded", "coalesced"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			gen := &catchUpGenerator{}
			svc, st, _ := newTestService(t, gen)
			var wantPaths []string
			for i := range sweepJobBatchSize + 2 {
				path := filepath.Join(t.TempDir(), "track.mp3")
				if err := os.WriteFile(path, fmt.Appendf(nil, "legacy %d", i), 0o600); err != nil {
					t.Fatal(err)
				}
				seedAudioMedia(t, st, path, fmt.Sprintf("Legacy %d", i))
				wantPaths = append(wantPaths, path)
			}
			now := time.Now().UTC()
			created, err := st.EnqueueJob(ctx, store.Job{
				Kind: CatchUpJobKind, DedupeKey: string(CatchUpJobKind), CreatedAt: now,
				Payload: []byte(`{"version":1,"after_rowid":9000}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			if phase != "queued" {
				claimed, err := st.ClaimNextJob(ctx, "legacy-worker", nil, now)
				if err != nil {
					t.Fatal(err)
				}
				if err := st.YieldJob(ctx, claimed.ID, claimed.LeaseOwner, []byte(`{"version":1,"after_rowid":9000,"last_rowid":9999,"generated":17}`), now, now); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "coalesced" {
				seq, err := st.MaxMediaInsertSeq(ctx)
				if err != nil {
					t.Fatal(err)
				}
				job, err := NewCatchUpJob(now, seq)
				if err != nil {
					t.Fatal(err)
				}
				coalesced, err := st.EnqueueJob(ctx, job)
				if err != nil || coalesced.ID != created.ID {
					t.Fatalf("coalesce = %+v, %v", coalesced, err)
				}
			}
			registry := jobs.NewRegistry(st, "new-worker")
			registry.Register(CatchUpJobKind, CatchUpHandler(svc))
			var previous int64
			for batch := range 3 {
				if _, err := registry.RunOne(ctx, nil, now.Add(time.Duration(batch)*time.Second)); err != nil {
					t.Fatal(err)
				}
				persisted, err := st.Job(ctx, created.ID)
				if err != nil {
					t.Fatal(err)
				}
				var checkpoint catchUpCheckpoint
				if err := json.Unmarshal(persisted.Checkpoint, &checkpoint); err != nil {
					t.Fatal(err)
				}
				if checkpoint.Version != 2 || checkpoint.AfterInsertSeq == nil || *checkpoint.AfterInsertSeq != 0 || checkpoint.LastInsertSeq <= previous {
					t.Fatalf("legacy resume failed to advance: %+v (previous %d)", checkpoint, previous)
				}
				previous = checkpoint.LastInsertSeq
				if persisted.Status == store.JobStatusCompleted {
					break
				}
			}
			finished, err := st.Job(ctx, created.ID)
			if err != nil || finished.Status != store.JobStatusCompleted || !reflect.DeepEqual(gen.paths, wantPaths) {
				t.Fatalf("legacy job = %+v, %v; paths %v, want %v", finished, err, gen.paths, wantPaths)
			}
		})
	}
}

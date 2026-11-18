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
	paths  []string
	cancel context.CancelFunc
}

func (g *catchUpGenerator) WaveformAmplitudes(ctx context.Context, src string) ([]float64, error) {
	g.paths = append(g.paths, src)
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

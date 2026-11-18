package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/beamfall/core/internal/genassets"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Only audio generators are used by this fixture; embedding the interface makes
// unexpected video generation fail rather than silently counting as coverage.
type catchUpAudioGenerator struct {
	genassets.Generator
	paths []string
}

func (g *catchUpAudioGenerator) WaveformAmplitudes(_ context.Context, path string) ([]float64, error) {
	g.paths = append(g.paths, path)
	return []float64{0.2, 0.8}, nil
}
func (*catchUpAudioGenerator) SilenceSpans(context.Context, string) ([][2]float64, error) {
	return nil, nil
}
func (*catchUpAudioGenerator) LoudnessMeasure(context.Context, string) (float64, float64, float64, bool, error) {
	return -18, 4, -1, false, nil
}

func TestAssetCatchUpEnqueuesAndVisitsMediaAfterPurge(t *testing.T) {
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
	importAudio := func(name string) (store.Media, string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), name+".mp3")
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: lib.ID, Path: path, Title: name, Container: "mp3", Codec: "mp3",
			Class: store.MediaClassAudio, ContentFingerprint: "sha256:" + name, DurationSecondsHint: 200,
		})
		if err != nil {
			t.Fatal(err)
		}
		return media, path
	}
	old, _ := importAudio("purged")
	floor, err := st.MaxMediaInsertSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.TrashMedia(ctx, old.ID, store.LibraryFilter{AllLibraries: true, Revealed: true}); err != nil {
		t.Fatal(err)
	}
	if count, err := st.PurgeTrashedBefore(ctx, time.Now().Add(time.Hour)); err != nil || count != 1 {
		t.Fatalf("purge = %d, %v", count, err)
	}
	fresh, path := importAudio("replacement")
	enqueueAssetCatchUp(ctx, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, floor)
	queued, err := st.ListJobs(ctx)
	if err != nil || len(queued) != 1 || queued[0].Kind != genassets.CatchUpJobKind {
		t.Fatalf("catch-up not enqueued: %+v, %v", queued, err)
	}
	gen := &catchUpAudioGenerator{}
	svc := genassets.NewService(st, gen, t.TempDir())
	registry := jobs.NewRegistry(st, "catch-up-worker")
	registry.Register(genassets.CatchUpJobKind, genassets.CatchUpHandler(svc))
	if _, err := registry.RunOne(ctx, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gen.paths, []string{path}) {
		t.Fatalf("visits = %v, want fresh Media %s at %s", gen.paths, fresh.ID, path)
	}
	finished, err := st.Job(ctx, queued[0].ID)
	if err != nil || finished.Status != store.JobStatusCompleted {
		t.Fatalf("catch-up = %+v, %v", finished, err)
	}
	var checkpoint struct {
		Generated int `json:"generated"`
	}
	if err := json.Unmarshal(finished.Checkpoint, &checkpoint); err != nil || checkpoint.Generated != 1 {
		t.Fatalf("checkpoint = %+v, %v", checkpoint, err)
	}
}

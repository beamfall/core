package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback"
	scanpkg "github.com/beamfall/core/internal/scan"
	schedulepkg "github.com/beamfall/core/internal/schedule"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// composingParser is a fixture parser that also implements the optional
// ComposedPicture method, the way a plugin's compiled parser would.
type composingParser struct {
	graphics, text string
	ok             bool
	gotPath        *string
}

func (composingParser) Parse(string, scanpkg.ParseProbe) (scanpkg.ParsedMedia, bool) {
	return scanpkg.ParsedMedia{}, false
}

func (p composingParser) ComposedPicture(path string) (string, string, bool) {
	*p.gotPath = path
	return p.graphics, p.text, p.ok
}

type plainParser struct{}

func (plainParser) Parse(string, scanpkg.ParseProbe) (scanpkg.ParsedMedia, bool) {
	return scanpkg.ParsedMedia{}, false
}

func TestNewPictureComposersOnlyWhenAParserComposes(t *testing.T) {
	st := storetest.Open(t)
	if newPictureComposers(st, map[string]scanpkg.FilenameParser{"plain": plainParser{}}) != nil {
		t.Fatal("composers built with no composing parser")
	}
	if newPictureComposers(st, nil) != nil {
		t.Fatal("composers built with no parsers")
	}
	var nilSet *pictureComposers
	if nilSet.playbackComposer() != nil || nilSet.generationComposer() != nil {
		t.Fatal("a nil set must disable both seams")
	}
}

func TestPictureComposersResolveThroughLibraryBinding(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	var gotPath string
	composers := newPictureComposers(st, map[string]scanpkg.FilenameParser{
		"composing": composingParser{text: "[Script Info]", ok: true, gotPath: &gotPath},
		"plain":     plainParser{},
	})
	if composers == nil {
		t.Fatal("composing parser not discovered")
	}
	scope, err := auth.NewService(st).Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	bound, err := st.CreateLibrary(ctx, "Bound", false, "composing", scope.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateLibrary(ctx, "Other", false, "plain", scope.ProfileID)
	if err != nil {
		t.Fatal(err)
	}

	picture, ok, err := composers.forMedia(ctx, store.Media{LibraryID: bound.ID}, "/lib/song.mp3")
	if err != nil || !ok || picture.TimedText != "[Script Info]" || picture.GraphicsPath != "" || gotPath != "/lib/song.mp3" {
		t.Fatalf("bound library: picture = %+v ok %v err %v path %q", picture, ok, err, gotPath)
	}
	if _, ok, err := composers.forMedia(ctx, store.Media{LibraryID: other.ID}, "/lib/x.mp3"); ok || err != nil {
		t.Fatalf("a non-composing plugin's Library composed: ok %v err %v", ok, err)
	}
	if _, ok, err := composers.forMedia(ctx, store.Media{LibraryID: "library-local"}, "/lib/x.mp3"); ok || err != nil {
		t.Fatalf("an unbound Library composed: ok %v err %v", ok, err)
	}
}

func TestPictureComposersSweepEnqueuesMissingRenditions(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	var gotPath string
	composers := newPictureComposers(st, map[string]scanpkg.FilenameParser{
		"composing": composingParser{text: "[Script Info]", ok: true, gotPath: &gotPath},
		"plain":     plainParser{},
	})
	scope, err := auth.NewService(st).Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	bound, err := st.CreateLibrary(ctx, "Bound", false, "composing", scope.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateLibrary(ctx, "Other", false, "plain", scope.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	importAudio := func(library store.LibraryID, name string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library, Path: path, Title: name, Container: "mp3", Codec: "mp3",
			Class: store.MediaClassAudio, ContentFingerprint: "fp-" + name, DurationSecondsHint: 3,
		}); err != nil {
			t.Fatalf("ImportMediaFile(%s): %v", name, err)
		}
	}
	importAudio(bound.ID, "one.mp3")
	importAudio(bound.ID, "two.mp3")
	importAudio(other.ID, "plain.mp3")

	now := time.Unix(700, 0).UTC()
	enqueued, err := composers.sweep(ctx, t.TempDir(), now, nil)
	if err != nil || enqueued != 2 {
		t.Fatalf("sweep = %d err %v, want 2 (only the composing Library)", enqueued, err)
	}
	all, err := st.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	precompute := 0
	for _, job := range all {
		if job.Kind == playback.PrecomputeJobKind {
			precompute++
		}
	}
	if precompute != 2 {
		t.Fatalf("precompute jobs = %d, want 2", precompute)
	}

	// With the queue at the limit, a sweep adds nothing more.
	for i := precompute; i < composedSweepLimit; i++ {
		if _, err := st.EnqueueJob(ctx, store.Job{
			Kind: playback.PrecomputeJobKind, Class: store.JobClassBackground,
			DedupeKey: fmt.Sprintf("filler-%d", i), Payload: []byte(`{}`), CreatedAt: now, RunAfter: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	importAudio(bound.ID, "three.mp3")
	if enqueued, err := composers.sweep(ctx, t.TempDir(), now, nil); err != nil || enqueued != 0 {
		t.Fatalf("sweep at queue limit = %d err %v, want 0", enqueued, err)
	}

	var nilSet *pictureComposers
	if n, err := nilSet.sweep(ctx, t.TempDir(), now, nil); n != 0 || err != nil {
		t.Fatalf("nil set sweep = %d err %v", n, err)
	}
}

func TestComposedSweepScheduleSeededOnlyWithBudgetAndComposer(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		budget   int64
		composes bool
		want     bool
	}{
		{"budget and composer", 1 << 30, true, true},
		{"no budget", 0, true, false},
		{"no composer", 1 << 30, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := storetest.Open(t)
			service := schedulepkg.NewService(st)
			registerBuiltInSchedules(ctx, st, service, t.TempDir(), tc.budget, tc.composes)
			if err := service.Reconcile(ctx, time.Unix(800, 0).UTC()); err != nil {
				t.Fatal(err)
			}
			_, err := st.Schedule(ctx, store.ScheduleID(playback.ComposedSweepJobKind))
			if got := err == nil; got != tc.want {
				t.Fatalf("composed sweep schedule seeded = %v (err %v), want %v", got, err, tc.want)
			}
		})
	}
}

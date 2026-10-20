package migrate_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// enqueueAndRunMapped enqueues + runs an import with an explicit target Library
// (the Profile/Library mapping, ADR-0037 §2.2). An empty libraryID exercises the
// default cross-Library match. It returns the completed Job so the test can read
// the unified match-rate report off the progress row.
func enqueueAndRunMapped(t *testing.T, st *store.Store, source migrate.Source, profileID, libraryID string, categories []string, now time.Time) store.Job {
	t.Helper()
	registry := jobs.NewRegistry(st, "test-worker")
	registry.Register(migrate.JobKind, migrate.Handler(st, fakeResolver{source: source}))

	payload, err := json.Marshal(migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "plex",
		Categories: categories,
		ProfileID:  profileID,
		LibraryID:  libraryID,
	})
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := st.EnqueueJob(context.Background(), store.Job{
		Kind:      migrate.JobKind,
		Class:     store.JobClassBackground,
		DedupeKey: "filmtv:plex:" + libraryID,
		Payload:   payload,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("enqueue migration job: %v", err)
	}
	if _, err := registry.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	done, err := st.Job(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatalf("read job back: %v", err)
	}
	return done
}

func seedMediaInLibrary(t *testing.T, st *store.Store, libraryID, path, fingerprint, title string) store.MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          store.LibraryID(libraryID),
		Path:               path,
		Title:              title,
		Container:          "mkv",
		Codec:              "h264",
		Class:              store.MediaClassVideo,
		ContentFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatalf("seed media in %s at %q: %v", libraryID, path, err)
	}
	return media.ID
}

// TestMigrationImportLibraryMappingScopesMatches proves the Profile/Library
// mapping: when the operator scopes the run to a target Library,
// a path that matches a Media in a *different* Library is counted unmatched and
// never applied, while the in-scope match applies normally. An unscoped run
// (empty LibraryID) matches both — the default cross-Library behavior.
func TestMigrationImportLibraryMappingScopesMatches(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"

	localID := seedMediaInLibrary(t, st, "library-local", "/media/local/In.mkv", "fp-local-0001", "InScope")
	otherID := seedMediaInLibrary(t, st, "library-archive", "/media/archive/Out.mkv", "fp-arch-0001", "OutOfScope")

	historical := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{
			{Path: "/media/local/In.mkv", WatchState: migrate.WatchState{Present: true, Watched: true, DurationSeconds: 600, OccurredAt: historical}},
			{Path: "/media/archive/Out.mkv", WatchState: migrate.WatchState{Present: true, Watched: true, DurationSeconds: 600, OccurredAt: historical}},
		},
	}
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	// Scoped to library-local: the archive match is out of scope → unmatched.
	scoped := enqueueAndRunMapped(t, st, source, profileID, "library-local", []string{migrate.CategoryWatchState}, now)
	if scoped.Progress.Current != 1 || scoped.Progress.Total != 2 {
		t.Fatalf("scoped match-rate = %d of %d, want 1 of 2", scoped.Progress.Current, scoped.Progress.Total)
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, profileID, localID); !ok {
		t.Fatal("in-scope match was not applied")
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, profileID, otherID); ok {
		t.Fatal("out-of-scope (different Library) match WAS applied — mapping not enforced")
	}

	// Unscoped (empty LibraryID): both match — the default cross-Library behavior.
	unscoped := enqueueAndRunMapped(t, st, source, profileID, "", []string{migrate.CategoryWatchState}, now.Add(time.Hour))
	if unscoped.Progress.Current != 2 || unscoped.Progress.Total != 2 {
		t.Fatalf("unscoped match-rate = %d of %d, want 2 of 2", unscoped.Progress.Current, unscoped.Progress.Total)
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, profileID, otherID); !ok {
		t.Fatal("unscoped run did not apply the archive match")
	}
}

// TestMigrationImportUnifiedReportPerCategoryBreakdown proves the unified
// match-rate report: the progress message carries the overall
// "matched of total" headline AND a per-category breakdown so the operator sees
// which kinds of state landed, not just an aggregate rate.
func TestMigrationImportUnifiedReportPerCategoryBreakdown(t *testing.T) {
	st := storetest.Open(t)
	const profileID = "profile-default"

	seedMediaInLibrary(t, st, "library-local", "/media/local/A.mkv", "fp-a-0001", "A")
	seedMediaInLibrary(t, st, "library-local", "/media/local/B.mkv", "fp-b-0001", "B")
	cID := seedMediaInLibrary(t, st, "library-local", "/media/local/C.mkv", "fp-c-0001", "C")
	addExternalID(t, st, cID, "tmdb", "300")

	historical := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{
			{Path: "/media/local/A.mkv", WatchState: migrate.WatchState{Present: true, Watched: true, DurationSeconds: 600, OccurredAt: historical}},
			{Path: "/media/local/B.mkv", WatchState: migrate.WatchState{Present: true, PositionSeconds: 100, DurationSeconds: 600, OccurredAt: historical}},
			{ExternalIDs: []migrate.ExternalID{{Scheme: "tmdb", Value: "300"}}, WatchState: migrate.WatchState{Present: true, Watched: true, DurationSeconds: 600, OccurredAt: historical}},
			{Path: "/media/local/Gone.mkv", WatchState: migrate.WatchState{Present: true, Watched: true, DurationSeconds: 600}},
		},
	}
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	done := enqueueAndRunMapped(t, st, source, profileID, "", []string{migrate.CategoryWatchState}, now)

	if done.Progress.Current != 3 || done.Progress.Total != 4 {
		t.Fatalf("match-rate = %d of %d, want 3 of 4", done.Progress.Current, done.Progress.Total)
	}
	// Three matched items carried watch-state → "(watch-state=3)" suffix.
	wantHead := "3 of 4 items matched"
	if !strings.HasPrefix(done.Progress.Message, wantHead) {
		t.Fatalf("message = %q, want prefix %q", done.Progress.Message, wantHead)
	}
	if !strings.Contains(done.Progress.Message, "watch-state=3") {
		t.Fatalf("message = %q, want per-category breakdown watch-state=3", done.Progress.Message)
	}
	for _, want := range []string{"external-id=1", "path=2", "filename=0", "unmatched=1"} {
		if !strings.Contains(done.Progress.Message, want) {
			t.Fatalf("message = %q, want strategy breakdown %q", done.Progress.Message, want)
		}
	}
}

// TestMigrationFilenameHeuristicAmbiguityIsUnmatched proves ADR-0037 §2.3
// step 3 never guesses: two Media with the same filename-derived title make the
// heuristic ambiguous, so the item reaches the distinct unmatched bucket.
func TestMigrationFilenameHeuristicAmbiguityIsUnmatched(t *testing.T) {
	st := storetest.Open(t)
	const profileID = "profile-default"

	seedMediaInLibrary(t, st, "library-local", "/media/local/Shared.mkv", "fp-shared-local", "Shared")
	seedMediaInLibrary(t, st, "library-local", "/media/local/Shared-copy.mkv", "fp-shared-copy", "shared")
	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{{
			Path:       "/source/relocated/Shared.mkv",
			WatchState: migrate.WatchState{Present: true, Watched: true},
		}},
	}

	done := enqueueAndRunMapped(
		t,
		st,
		source,
		profileID,
		"",
		[]string{migrate.CategoryWatchState},
		time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
	)
	if done.Progress.Current != 0 || done.Progress.Total != 1 {
		t.Fatalf("ambiguous filename match-rate = %d of %d, want 0 of 1", done.Progress.Current, done.Progress.Total)
	}
	for _, want := range []string{"filename=0", "unmatched=1"} {
		if !strings.Contains(done.Progress.Message, want) {
			t.Fatalf("message = %q, want %q", done.Progress.Message, want)
		}
	}
}

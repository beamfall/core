package migrate_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestMigrationDryRunItemizedReportGolden(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"

	externalID := seedMediaInLibrary(t, st, "library-local", "/media/local/External.mkv", "fp-dry-external", "External")
	addExternalID(t, st, externalID, "tmdb", "100")
	pathID := seedMediaInLibrary(t, st, "library-local", "/media/local/Path.mkv", "fp-dry-path", "Path")
	filenameID := seedMediaInLibrary(t, st, "library-local", "/media/local/Filename.mkv", "fp-dry-filename", "Filename Match")
	otherID := seedMediaInLibrary(t, st, "library-archive", "/media/archive/Other.mkv", "fp-dry-other", "Other")
	firstConflictID := seedMediaInLibrary(t, st, "library-local", "/media/local/ConflictA.mkv", "fp-dry-conflict-a", "Conflict A")
	secondConflictID := seedMediaInLibrary(t, st, "library-local", "/media/local/ConflictB.mkv", "fp-dry-conflict-b", "Conflict B")
	addExternalID(t, st, firstConflictID, "imdb", "tt-dry-conflict")
	addExternalID(t, st, secondConflictID, "imdb", "tt-dry-conflict")

	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{
			{ExternalIDs: []migrate.ExternalID{{Scheme: "tmdb", Value: "100"}}, WatchState: migrate.WatchState{Present: true, Watched: true}},
			{Path: "/media/local/Path.mkv", WatchState: migrate.WatchState{Present: true, Watched: true}},
			{Path: `C:\source\relocated\Filename Match.mkv`, WatchState: migrate.WatchState{Present: true, Watched: true}},
			{Path: "/media/local/Missing.mkv", WatchState: migrate.WatchState{Present: true, Watched: true}},
			{ExternalIDs: []migrate.ExternalID{{Scheme: "imdb", Value: "tt-dry-conflict"}}, WatchState: migrate.WatchState{Present: true, Watched: true}},
			{Path: "/media/archive/Other.mkv", WatchState: migrate.WatchState{Present: true, Watched: true}},
		},
	}
	payload := migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "fixture",
		Categories: []string{migrate.CategoryWatchState},
		DryRun:     true,
		ProfileID:  profileID,
		LibraryID:  "library-local",
	}

	done := enqueuePayloadAndRun(t, st, source, payload, "dry-run-itemized", time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC))
	golden, err := os.ReadFile("testdata/dry-run-itemized-report.golden")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := done.Progress.Message+"\n", string(golden); got != want {
		t.Fatalf("itemized dry-run report:\n got: %s\nwant: %s", got, want)
	}
	for _, mediaID := range []store.MediaID{externalID, pathID, filenameID, otherID, firstConflictID, secondConflictID} {
		if progress, ok, err := st.PlaybackProgressFor(ctx, profileID, mediaID); err != nil || ok {
			t.Fatalf("dry-run wrote progress for %s: progress=%#v ok=%v err=%v", mediaID, progress, ok, err)
		}
	}
	records, err := st.UnmatchedRecords(ctx, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("dry-run persisted %d unmatched records, want 0", len(records))
	}
	storedReport := done.Progress.Message + string(done.Checkpoint)
	for _, private := range []string{
		"/media/local/External.mkv",
		"/media/local/Path.mkv",
		`C:\source\relocated\Filename Match.mkv`,
		"/media/local/Missing.mkv",
		"/media/archive/Other.mkv",
		"tmdb:100",
		"tt-dry-conflict",
		"library-archive",
	} {
		if strings.Contains(storedReport, private) {
			t.Fatalf("privacy-filtered field %q reached stored report %q", private, storedReport)
		}
	}
}

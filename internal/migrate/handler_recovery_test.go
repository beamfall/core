package migrate_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type interruptAfterFirstUnmatchedStore struct {
	*store.Store
	cancel      context.CancelFunc
	interrupted bool
}

func (s *interruptAfterFirstUnmatchedStore) QueueUnmatchedRecordOnce(ctx context.Context, rec store.UnmatchedRecord) (string, error) {
	id, err := s.Store.QueueUnmatchedRecordOnce(ctx, rec)
	if err == nil && !s.interrupted {
		s.interrupted = true
		s.cancel()
	}
	return id, err
}

// LCRES-2 regression proof (2026-08-21 UTC): replacing the provenance-derived
// collection ID with a per-run timestamp made the focused test fail with
// "handler_recovery_test.go:177: collections after resume = 3, want stable count 2".
func TestMigrationInterruptedRunResumesWithoutDuplicatingItemsOrUnmatchedRecords(t *testing.T) {
	st := storetest.Open(t)
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	historical := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)
	authoredCollection, err := st.CreateCollection(context.Background(), store.Collection{
		LibraryID: "library-local",
		Name:      "Interrupted Baseline",
	}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	firstID, _ := seedMediaAtPath(t, st, "/media/films/First.mkv", "fp-interrupted-first", "First")
	lastID, _ := seedMediaAtPath(t, st, "/media/films/Last.mkv", "fp-interrupted-last", "Last")
	source := fakeSource{
		categories: []string{migrate.CategoryWatchState, migrate.CategoryCollections},
		items: []migrate.ForeignItem{
			{
				SmartCollection: migrate.SmartCollection{
					Present:               true,
					Identity:              "source-interrupted-baseline",
					Name:                  authoredCollection.Name,
					CurrentMembersPresent: true,
					CurrentMembers:        []migrate.ForeignItem{},
				},
			},
			{
				Path: "/media/films/First.mkv",
				WatchState: migrate.WatchState{
					Present: true, Watched: true, DurationSeconds: 600, OccurredAt: historical,
				},
			},
			{
				Path: "/media/films/Missing.mkv",
				WatchState: migrate.WatchState{
					Present: true, Watched: true, DurationSeconds: 600, OccurredAt: historical,
				},
			},
			{
				Path: "/media/films/Last.mkv",
				WatchState: migrate.WatchState{
					Present: true, Watched: true, DurationSeconds: 600, OccurredAt: historical,
				},
			},
		},
	}
	payload, err := json.Marshal(migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "plex",
		Categories: []string{migrate.CategoryWatchState, migrate.CategoryCollections},
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
	})
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := st.EnqueueJob(context.Background(), store.Job{
		Kind:      migrate.JobKind,
		Class:     store.JobClassBackground,
		DedupeKey: "filmtv:plex:interrupted",
		Payload:   payload,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	interruptingStore := &interruptAfterFirstUnmatchedStore{Store: st, cancel: cancel}
	registry := jobs.NewRegistry(interruptingStore, "test-worker-interrupted")
	registry.Register(migrate.JobKind, migrate.Handler(interruptingStore, fakeResolver{source: source}))
	if _, err := registry.RunOne(runCtx, nil, now); err != nil {
		t.Fatalf("interrupted RunOne: %v", err)
	}
	yielded, err := st.Job(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if yielded.Status != store.JobStatusYielded {
		t.Fatalf("interrupted job status = %q, want yielded", yielded.Status)
	}
	firstBefore, ok, err := st.PlaybackProgressFor(context.Background(), "profile-default", firstID)
	if err != nil || !ok {
		t.Fatalf("first item after interruption: ok=%v err=%v", ok, err)
	}
	if _, ok, err := st.PlaybackProgressFor(context.Background(), "profile-default", lastID); err != nil || ok {
		t.Fatalf("last item after interruption: ok=%v err=%v, want not yet applied", ok, err)
	}
	recordsBefore, err := st.UnmatchedRecords(context.Background(), store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(recordsBefore) != 1 {
		t.Fatalf("unmatched records after interruption = %d, want 1", len(recordsBefore))
	}
	collectionsBefore, err := st.Collections(context.Background(), "library-local", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(collectionsBefore) != 2 {
		t.Fatalf("collections after interruption = %#v, want authored and imported same-name collections", collectionsBefore)
	}
	var importedBefore store.Collection
	for _, collection := range collectionsBefore {
		if collection.ID != authoredCollection.ID {
			importedBefore = collection
		}
	}
	if importedBefore.ID == "" || importedBefore.Name != authoredCollection.Name {
		t.Fatalf("imported collection after interruption = %#v, want distinct same-name baseline", importedBefore)
	}

	if _, err := registry.RunOne(context.Background(), nil, now.Add(time.Second)); err != nil {
		t.Fatalf("resumed RunOne: %v", err)
	}
	done, err := st.Job(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != store.JobStatusCompleted {
		t.Fatalf("resumed job status = %q, want completed", done.Status)
	}
	if done.Progress.Current != 2 || done.Progress.Total != 3 {
		t.Fatalf("resumed match-rate = %d of %d, want 2 of 3", done.Progress.Current, done.Progress.Total)
	}
	firstAfter, ok, err := st.PlaybackProgressFor(context.Background(), "profile-default", firstID)
	if err != nil || !ok {
		t.Fatalf("first item after resume: ok=%v err=%v", ok, err)
	}
	if firstAfter.PlayCount != firstBefore.PlayCount {
		t.Fatalf("resume duplicated first item: play count %d -> %d", firstBefore.PlayCount, firstAfter.PlayCount)
	}
	if _, ok, err := st.PlaybackProgressFor(context.Background(), "profile-default", lastID); err != nil || !ok {
		t.Fatalf("last item after resume: ok=%v err=%v, want applied", ok, err)
	}
	recordsAfter, err := st.UnmatchedRecords(context.Background(), store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(recordsAfter) != 1 || recordsAfter[0].ID != recordsBefore[0].ID {
		t.Fatalf("unmatched records after resume = %#v, want original record %q only", recordsAfter, recordsBefore[0].ID)
	}
	collectionsAfter, err := st.Collections(context.Background(), "library-local", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(collectionsAfter) != len(collectionsBefore) {
		t.Fatalf("collections after resume = %d, want stable count %d", len(collectionsAfter), len(collectionsBefore))
	}
	var importedAfter store.Collection
	for _, collection := range collectionsAfter {
		if collection.ID == importedBefore.ID {
			importedAfter = collection
		}
	}
	if importedAfter != importedBefore {
		t.Fatalf("imported collection after resume = %#v, want stable %#v", importedAfter, importedBefore)
	}
}

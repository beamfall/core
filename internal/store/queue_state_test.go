package store

import (
	"log/slog"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
)

func TestPlayQueueMigrationPreservesExistingRows(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "events.db")
	backupDir := t.TempDir()
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const dir = "migrate/events"
	before := migrationsBefore(t, eventsMigrations, dir, "0025")
	if err := migrate(ctx, slog.Default(), db, dbPath, "events", before, dir, backupDir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO play_queue(profile_id, position, media_id, added_at_ms)
		VALUES ('profile-a', 42, 'media-a', 100), ('profile-a', 2, 'media-z', 200), ('profile-b', 0, 'media-z', 300)`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, slog.Default(), db, dbPath, "events", eventsMigrations, dir, backupDir); err != nil {
		t.Fatal(err)
	}

	read := func(profile ProfileID) queueSnapshot {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		state, err := loadQueue(ctx, tx, profile)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	a, b := read("profile-a"), read("profile-b")
	if len(a.entries) != 2 || len(b.entries) != 1 || a.entries[0].mediaID != "media-z" ||
		a.entries[0].addedAtMS != 200 || a.entries[1].mediaID != "media-a" || a.entries[1].addedAtMS != 100 ||
		b.entries[0].mediaID != "media-z" || b.entries[0].addedAtMS != 300 {
		t.Fatalf("migrated queue entries = %+v and %+v", a.entries, b.entries)
	}
	ids := []string{a.entries[0].id, a.entries[1].id, b.entries[0].id}
	if slices.Contains(ids, "") || ids[0] == ids[1] || ids[0] == ids[2] || ids[1] == ids[2] {
		t.Fatalf("missing or duplicate migrated IDs: %v", ids)
	}
	if a.currentID != "" || a.positionMS != 0 || a.shuffle || a.repeat != QueueRepeatOff {
		t.Fatalf("migrated queue default state = %+v", a)
	}
	if err := migrate(ctx, slog.Default(), db, dbPath, "events", eventsMigrations, dir, backupDir); err != nil {
		t.Fatal(err)
	}
	if reloaded := read("profile-a"); !slices.Equal(reloaded.entries, a.entries) {
		t.Fatalf("migrate changed queue identities: %+v, want %+v", reloaded.entries, a.entries)
	}
}

func TestPlayQueueStateSurvivesStoreRestartAndPrivacyDeletion(t *testing.T) {
	ctx := t.Context()
	dataDir := migratedDataDir(t)
	opts := Options{SeedDevelopmentData: true}
	st, err := Open(ctx, dataDir, opts)
	if err != nil {
		t.Fatal(err)
	}
	initial := st
	t.Cleanup(func() { _ = initial.Close() })
	filter := LibraryFilter{AllLibraries: true, Revealed: true}
	profile := ProfileID("profile-default")
	read := func() PlayQueue {
		t.Helper()
		state, err := st.PlayQueue(ctx, profile, filter)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}

	if err := st.QueueAddMany(ctx, profile, []MediaID{"media-alpha", "media-archive", "media-alpha"}, QueueEnd, filter); err != nil {
		t.Fatal(err)
	}
	entry := read().Entries[1]
	if err := st.QueueSetCurrent(ctx, profile, entry.ID, 42300, filter); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueSetShuffle(ctx, profile, true, filter); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueSetRepeat(ctx, profile, QueueRepeatOne, filter); err != nil {
		t.Fatal(err)
	}
	before := read()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dataDir, opts)
	if err != nil {
		t.Fatal(err)
	}
	st = reopened
	t.Cleanup(func() { _ = reopened.Close() })
	if after := read(); !reflect.DeepEqual(after, before) {
		t.Fatalf("restarted queue = %+v, want %+v", after, before)
	}

	tx, err := st.eventsW.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := deleteMyQueueMedia(ctx, tx, profile, entry.Item.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after := read()
	if after.CurrentEntryID != nil || after.PositionMS != 0 || len(after.Entries) != 2 ||
		!after.Shuffle || after.RepeatMode != QueueRepeatOne {
		t.Fatalf("queue after privacy deletion = %+v", after)
	}
}

func TestPlayQueueConcurrentBatchesStayIntact(t *testing.T) {
	ctx := t.Context()
	st := openSeeded(t)
	filter := LibraryFilter{AllLibraries: true, Revealed: true}
	const profile = "profile-default"
	const batches = 8
	var writers sync.WaitGroup
	for range batches {
		writers.Go(func() {
			if err := st.QueueAddMany(ctx, profile, []MediaID{"media-alpha", "media-archive"}, QueueEnd, filter); err != nil {
				t.Error(err)
			}
		})
	}
	writers.Wait()

	state, err := st.PlayQueue(ctx, profile, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Entries) != batches*2 {
		t.Fatalf("queue contains %d entries, want %d", len(state.Entries), batches*2)
	}
	seen := make(map[string]bool, len(state.Entries))
	for i, entry := range state.Entries {
		want := []MediaID{"media-alpha", "media-archive"}[i%2]
		if entry.Item.ID != want || entry.ID == "" || seen[entry.ID] {
			t.Fatalf("wrong or duplicate entry at %d: %+v", i, entry)
		}
		seen[entry.ID] = true
	}
}

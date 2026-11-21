package store

import (
	"context"
	"testing"
	"time"
)

// TestTrashedMediaListExplainsAVersionMerge: a Media the Version merge folded
// lists its batch and survivor; an undo clears it; an ordinary trash has none
// (CORE-2916).
func TestTrashedMediaListExplainsAVersionMerge(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	keep, drop, _, _ := splitTitle(t, st)
	now := time.Now().UTC()
	result, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{keep, drop}, now)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := st.TrashedMediaList(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != drop {
		t.Fatalf("trash = %+v, want only %s", entries, drop)
	}
	merge := entries[0].Merge
	if merge == nil || merge.BatchID != result.BatchID || merge.Survivor != keep || merge.SurvivorTitle == "" {
		t.Fatalf("merge = %+v, want batch %s into %s with its title", merge, result.BatchID, keep)
	}
	if merge.MergedAt.IsZero() {
		t.Fatal("merge time is zero")
	}

	if _, err := st.UndoBatch(ctx, result.BatchID, filter, now.Add(time.Minute)); err != nil {
		t.Fatalf("UndoBatch: %v", err)
	}
	if err := st.TrashMedia(ctx, drop, filter); err != nil {
		t.Fatal(err)
	}
	entries, err = st.TrashedMediaList(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Merge != nil {
		t.Fatalf("trash after undo and a plain trash = %+v, want no merge", entries)
	}
}

// TestPurgeOfAMergedMediaKeepsTheSurvivorFiles: purging the empty Media a
// merge left behind removes only that row; the moved files stay on the
// survivor (CORE-2916).
func TestPurgeOfAMergedMediaKeepsTheSurvivorFiles(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	keep, drop, _, _ := splitTitle(t, st)
	now := time.Now().UTC()
	if _, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{keep, drop}, now); err != nil {
		t.Fatal(err)
	}
	purged, err := st.PurgeTrashedBefore(ctx, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if purged != 1 {
		t.Fatalf("purged = %d, want 1", purged)
	}
	var survivorFiles, droppedRows int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_file WHERE media_id = ? AND missing_at_ms IS NULL`, keep).Scan(&survivorFiles); err != nil {
		t.Fatal(err)
	}
	if survivorFiles != 2 {
		t.Fatalf("survivor files = %d, want both copies", survivorFiles)
	}
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM media WHERE id = ?`, drop).Scan(&droppedRows); err != nil {
		t.Fatal(err)
	}
	if droppedRows != 0 {
		t.Fatalf("merged Media row survived the purge")
	}
}

// TestMergeSplitVersionsLogsTheScanAsActor: the merge's activity entry reads
// as the scan's, while a merge a session made does not (CORE-2916).
func TestMergeSplitVersionsLogsTheScanAsActor(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	keep, drop, _, _ := splitTitle(t, st)
	if _, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{keep, drop}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	entries, err := st.ActivityEntries(ctx, ActivityQuery{Filter: filter, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Action == ActivityMediaMerge {
			found = true
			if got := entry.SystemActor(); got != ActivitySystemActorScan {
				t.Fatalf("merge entry actor = %q, want scan", got)
			}
		}
	}
	if !found {
		t.Fatal("no merge activity entry")
	}
	bySession := ActivityEntry{Action: ActivityMediaMerge, SessionID: "session-1", ProfileID: "profile-default"}
	if got := bySession.SystemActor(); got != "" {
		t.Fatalf("session merge actor = %q, want none", got)
	}
	byToken := ActivityEntry{Action: ActivityMediaMerge, TokenID: "token-1"}
	if got := byToken.SystemActor(); got != "" {
		t.Fatalf("token merge actor = %q, want none", got)
	}
	other := ActivityEntry{Action: ActivityMediaTrash}
	if got := other.SystemActor(); got != "" {
		t.Fatalf("trash actor = %q, want none", got)
	}
}

// TestDuplicateCopiesListsByteIdenticalVersions: two live part-0 Versions with
// the same OSHash and size form one set; a missing copy, a different size or
// an empty hash does not (CORE-2916).
func TestDuplicateCopiesListsByteIdenticalVersions(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	keep, drop, _, _ := splitTitle(t, st)
	if _, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{keep, drop}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	setFiles := func(query string, args ...any) {
		t.Helper()
		if _, err := st.catalogW.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	sets, err := st.DuplicateCopies(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 0 {
		t.Fatalf("sets without hashes = %+v, want none", sets)
	}

	setFiles(`UPDATE media_file SET oshash = 'abc', size_bytes = 42 WHERE media_id = ?`, keep)
	sets, err = st.DuplicateCopies(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 1 || sets[0].MediaID != keep || len(sets[0].Copies) != 2 {
		t.Fatalf("sets = %+v, want one set of two on %s", sets, keep)
	}
	if sets[0].Copies[0].Path == sets[0].Copies[1].Path || sets[0].Copies[0].SizeBytes != 42 {
		t.Fatalf("copies = %+v", sets[0].Copies)
	}

	setFiles(`UPDATE media_file SET size_bytes = 43 WHERE rowid = (SELECT MIN(rowid) FROM media_file WHERE media_id = ?)`, keep)
	if sets, err = st.DuplicateCopies(ctx, filter); err != nil || len(sets) != 0 {
		t.Fatalf("sets with differing sizes = %+v, %v; want none", sets, err)
	}
	setFiles(`UPDATE media_file SET size_bytes = 42, missing_at_ms = 1 WHERE rowid = (SELECT MIN(rowid) FROM media_file WHERE media_id = ?)`, keep)
	if sets, err = st.DuplicateCopies(ctx, filter); err != nil || len(sets) != 0 {
		t.Fatalf("sets with a missing copy = %+v, %v; want none", sets, err)
	}
}

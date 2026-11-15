package store

import (
	"fmt"
	"testing"
	"time"
)

// TestCollectionSourceItemsFollowCuratedOrder pins the "collection" Source kind
// (CORE-1684): a manual Collection's visible members in their stored order,
// paged over visible items, and re-read in the new order after a reorder.
func TestCollectionSourceItemsFollowCuratedOrder(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	ids := []MediaID{"media-alpha"}
	for i := 1; i <= 2; i++ {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          "library-local",
			Path:               fmt.Sprintf("/media/local/collection-source-%d.mkv", i),
			Title:              fmt.Sprintf("Collection Source Member %d", i),
			Container:          "mkv",
			Codec:              "h264",
			ContentFingerprint: fmt.Sprintf("collection-source-%d-fingerprint", i),
			OSHash:             fmt.Sprintf("%016x", i+900),
		})
		if err != nil {
			t.Fatalf("seed member %d: %v", i, err)
		}
		ids = append(ids, media.ID)
	}
	col, err := st.CreateCollection(ctx, Collection{LibraryID: "library-local", Name: "Ordered Set"}, now)
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if _, err := st.AddCollectionMedia(ctx, col.ID, ids, filter, now); err != nil {
		t.Fatalf("AddCollectionMedia: %v", err)
	}

	source := func(offset, limit int) []MediaID {
		t.Helper()
		items, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "collection", Value: string(col.ID), Offset: offset, Limit: limit})
		if err != nil {
			t.Fatalf("SourceItems: %v", err)
		}
		out := make([]MediaID, len(items))
		for i, item := range items {
			if item.Kind != ItemTargetMedia || item.Title == "" {
				t.Fatalf("item %d = %#v, want a titled media item", i, item)
			}
			out[i] = item.ID
		}
		return out
	}
	assertMediaOrder(t, "added order", source(0, 0), ids)
	assertMediaOrder(t, "second page", source(1, 1), ids[1:2])

	reordered := []MediaID{ids[2], ids[0], ids[1]}
	if _, err := st.ReorderCollectionMedia(ctx, col.ID, reordered, filter, now); err != nil {
		t.Fatalf("ReorderCollectionMedia: %v", err)
	}
	assertMediaOrder(t, "after reorder", source(0, 0), reordered)
}

// TestCollectionSourceItemsHideWhatTheSessionCannotSee keeps the Source on the
// same visibility chokepoints as every other feed: a Collection in a Library
// outside the allow-list, a trashed Collection, an unknown ID, and a member the
// session may not see all stay out without an error that would leak existence.
func TestCollectionSourceItemsHideWhatTheSessionCannotSee(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-archive"}, Revealed: true}
	unrevealed := LibraryFilter{AllowedIDs: []LibraryID{"library-archive"}}

	col, err := st.CreateCollection(ctx, Collection{LibraryID: "library-archive", Name: "Sealed Set"}, now)
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if _, err := st.AddCollectionMedia(ctx, col.ID, []MediaID{"media-archive"}, revealed, now); err != nil {
		t.Fatalf("AddCollectionMedia: %v", err)
	}
	count := func(filter LibraryFilter, value string) int {
		t.Helper()
		items, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "collection", Value: value})
		if err != nil {
			t.Fatalf("SourceItems(%q): %v", value, err)
		}
		return len(items)
	}
	if got := count(revealed, string(col.ID)); got != 1 {
		t.Fatalf("revealed count = %d, want 1", got)
	}
	if got := count(unrevealed, string(col.ID)); got != 0 {
		t.Fatalf("unrevealed count = %d, want 0 (restricted member leaked)", got)
	}
	if got := count(LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true}, string(col.ID)); got != 0 {
		t.Fatalf("other-library count = %d, want 0", got)
	}
	if got := count(revealed, "collection-unknown"); got != 0 {
		t.Fatalf("unknown collection count = %d, want 0", got)
	}
	if got := count(revealed, ""); got != 0 {
		t.Fatalf("empty value count = %d, want 0", got)
	}
	if _, err := st.TrashCollection(ctx, col.ID, now); err != nil {
		t.Fatalf("TrashCollection: %v", err)
	}
	if got := count(revealed, string(col.ID)); got != 0 {
		t.Fatalf("trashed collection count = %d, want 0", got)
	}
}

func assertMediaOrder(t *testing.T, label string, got, want []MediaID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
}

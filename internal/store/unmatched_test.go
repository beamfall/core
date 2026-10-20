package store

import (
	"context"
	"testing"
	"time"
)

// TestUnmatchedQueueRoundTrip covers the PIPE-1 Unmatched-queue
// store table: QueueUnmatched writes/replaces a row, IsUnmatched reads it,
// UnmatchedItems lists it under a Library filter, and ClearUnmatched removes it.
func TestUnmatchedQueueRoundTrip(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// A real Media row to satisfy the unmatched_queue → media(id) FK.
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/fixtures/unmatched.mkv",
		Title:              "unmatched",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "sha256:unmatched",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}

	// Not queued initially.
	if queued, err := st.IsUnmatched(ctx, media.ID); err != nil || queued {
		t.Fatalf("IsUnmatched before queue: queued=%v err=%v", queued, err)
	}

	// Queue it (the below-threshold branch).
	item := UnmatchedItem{
		MediaID:        media.ID,
		PluginID:       "filmtv",
		ScraperID:      "tmdb-movie",
		Confidence:     0.62,
		Threshold:      0.75,
		CandidateID:    "1234",
		CandidateTitle: "Best Guess",
	}
	if err := st.QueueUnmatched(ctx, item); err != nil {
		t.Fatalf("QueueUnmatched: %v", err)
	}
	if queued, err := st.IsUnmatched(ctx, media.ID); err != nil || !queued {
		t.Fatalf("IsUnmatched after queue: queued=%v err=%v", queued, err)
	}

	// Visible under a Library filter that includes the item's Library.
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true}
	items, err := st.UnmatchedItems(ctx, filter, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("UnmatchedItems = %d rows, want 1", len(items))
	}
	got := items[0]
	if got.MediaID != media.ID || got.PluginID != "filmtv" || got.ScraperID != "tmdb-movie" {
		t.Fatalf("queued row = %#v", got)
	}
	if got.Title != "unmatched" {
		t.Fatalf("queued title = %q, want unmatched", got.Title)
	}
	if got.Confidence != 0.62 || got.Threshold != 0.75 || got.CandidateTitle != "Best Guess" {
		t.Fatalf("queued row lost its score/guess: %#v", got)
	}

	// Hidden under a filter that excludes the Library.
	hidden, err := st.UnmatchedItems(ctx, LibraryFilter{AllowedIDs: []LibraryID{"library-nonesuch"}, Revealed: true}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden) != 0 {
		t.Fatalf("UnmatchedItems leaked a hidden-Library row: %#v", hidden)
	}

	// Re-queue replaces (one row per Media, not a log).
	item.Confidence = 0.70
	if err := st.QueueUnmatched(ctx, item); err != nil {
		t.Fatalf("re-QueueUnmatched: %v", err)
	}
	items, err = st.UnmatchedItems(ctx, filter, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Confidence != 0.70 {
		t.Fatalf("re-queue did not replace in place: %#v", items)
	}

	// Clear removes it.
	if ok, err := st.ClearUnmatched(ctx, media.ID); err != nil || !ok {
		t.Fatalf("ClearUnmatched: ok=%v err=%v", ok, err)
	}
	if queued, err := st.IsUnmatched(ctx, media.ID); err != nil || queued {
		t.Fatalf("IsUnmatched after clear: queued=%v err=%v", queued, err)
	}
	// Clearing an absent row is a no-op (ok=false), not an error.
	if ok, err := st.ClearUnmatched(ctx, media.ID); err != nil || ok {
		t.Fatalf("ClearUnmatched(absent): ok=%v err=%v", ok, err)
	}
}

func TestUnmatchedItemsFiltersBeforePagination(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	queuedAt := time.Unix(1_700_000_000, 0).UTC()
	seed := func(libraryID LibraryID, title, fingerprint string, age int) MediaID {
		t.Helper()
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          libraryID,
			Path:               "/unmatched/" + fingerprint + ".mp4",
			Title:              title,
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: fingerprint,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.QueueUnmatched(ctx, UnmatchedItem{
			MediaID:   media.ID,
			PluginID:  "test",
			ScraperID: "test",
			QueuedAt:  queuedAt.Add(-time.Duration(age) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
		return media.ID
	}
	seed("library-archive", "Hidden newest", "unmatched-hidden-new", 0)
	visibleA := seed("library-local", "Visible first", "unmatched-visible-a", 1)
	seed("library-archive", "Hidden middle", "unmatched-hidden-middle", 2)
	visibleB := seed("library-local", "Visible second", "unmatched-visible-b", 3)

	filter := LibraryFilter{AllLibraries: true}
	first, err := st.UnmatchedItems(ctx, filter, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.UnmatchedItems(ctx, filter, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].MediaID != visibleA || first[0].Title != "Visible first" {
		t.Fatalf("first visible page = %#v", first)
	}
	if len(second) != 1 || second[0].MediaID != visibleB || second[0].Title != "Visible second" {
		t.Fatalf("second visible page = %#v", second)
	}
}

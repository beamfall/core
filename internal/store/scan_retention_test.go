package store

import (
	"context"
	"testing"
)

func TestLibraryHasUndeclaredMediaIgnoresTrash(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	library := createRetentionLibrary(t, st, "Scan retention")
	other := createRetentionLibrary(t, st, "Other library")
	importScanRetireMedia(t, st, other.ID, "foreign.jpg", MediaClassImage)
	importScanRetireMedia(t, st, library.ID, "clip.mp4", MediaClassVideo)
	classes := []MediaClass{MediaClassVideo}
	check := func(want bool) {
		t.Helper()
		got, err := st.LibraryHasUndeclaredMedia(ctx, library.ID, classes)
		if err != nil || got != want {
			t.Fatalf("LibraryHasUndeclaredMedia = %v, %v; want %v", got, err, want)
		}
	}
	check(false)
	image := importScanRetireMedia(t, st, library.ID, "existing.jpg", MediaClassImage)
	check(true)
	filter := LibraryFilter{AllowedIDs: []LibraryID{library.ID}, Revealed: true}
	if err := st.TrashMedia(ctx, image.ID, filter); err != nil {
		t.Fatal(err)
	}
	check(false)
	if err := st.RestoreMedia(ctx, image.ID, filter); err != nil {
		t.Fatal(err)
	}
	check(true)
	classes = nil
	check(false)
}

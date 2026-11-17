package store

import (
	"context"
	"errors"
	"testing"
)

func importScanRetireMedia(t *testing.T, st *Store, libraryID LibraryID, name string, class MediaClass) Media {
	t.Helper()
	file := ImportMediaFile{
		LibraryID:          libraryID,
		Path:               "/media/local/" + name,
		Title:              name,
		Container:          "jpg",
		ContentFingerprint: "sha256:" + name + "-scan-retire",
		Class:              class,
	}
	if class == MediaClassVideo {
		file.Container, file.Codec, file.DurationSecondsHint = "mp4", "h264", 120
	}
	media, _, err := st.ImportMediaFile(context.Background(), file)
	if err != nil {
		t.Fatalf("ImportMediaFile %s: %v", name, err)
	}
	return media
}

// TestTrashMediaRetiredByScanTrashesOnlyMatchingMedia covers CORE-2361's store
// half: only live Media of the named class in the named Library move to the
// trash, each with a media.trash activity entry, and a repeat call is a no-op.
func TestTrashMediaRetiredByScanTrashesOnlyMatchingMedia(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	library := createRetentionLibrary(t, st, "Scan Retire")
	other := createRetentionLibrary(t, st, "Scan Retire Other")
	image := importScanRetireMedia(t, st, library.ID, "banner.jpg", MediaClassImage)
	video := importScanRetireMedia(t, st, library.ID, "clip.mp4", MediaClassVideo)
	foreign := importScanRetireMedia(t, st, other.ID, "foreign.jpg", MediaClassImage)

	trashed, skipped, err := st.TrashMediaRetiredByScan(ctx, library.ID, MediaClassImage, []MediaID{image.ID, video.ID, foreign.ID, "media-missing"})
	if err != nil || skipped || trashed != 1 {
		t.Fatalf("TrashMediaRetiredByScan = %d, %v, %v; want 1 trashed", trashed, skipped, err)
	}
	filter := LibraryFilter{AllowedIDs: []LibraryID{library.ID, other.ID}, Revealed: true}
	if _, err := st.Media(ctx, image.ID, filter); err == nil {
		t.Fatal("image Media is still live")
	}
	for _, kept := range []Media{video, foreign} {
		if _, err := st.Media(ctx, kept.ID, filter); err != nil {
			t.Fatalf("%s was trashed: %v", kept.Title, err)
		}
	}
	entries, err := st.ActivityEntries(ctx, ActivityQuery{Filter: filter, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	audits := 0
	for _, entry := range entries {
		if entry.Action == ActivityMediaTrash && entry.TargetID == string(image.ID) && entry.LibraryID == library.ID {
			audits++
		}
	}
	if audits != 1 {
		t.Fatalf("media.trash entries for the image = %d, want 1 (entries %v)", audits, entries)
	}
	again, _, err := st.TrashMediaRetiredByScan(ctx, library.ID, MediaClassImage, []MediaID{image.ID})
	if err != nil || again != 0 {
		t.Fatalf("repeat TrashMediaRetiredByScan = %d, %v; want 0", again, err)
	}
}

// TestTrashMediaRetiredByScanRefusesWhileTrashDisabled pins hard invariant 6
// for the scan's autonomous retirement.
func TestTrashMediaRetiredByScanRefusesWhileTrashDisabled(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true, TrashDisabled: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	library := createRetentionLibrary(t, st, "Scan Retire Disabled")
	image := importScanRetireMedia(t, st, library.ID, "banner.jpg", MediaClassImage)
	trashed, skipped, err := st.TrashMediaRetiredByScan(ctx, library.ID, MediaClassImage, []MediaID{image.ID})
	if !errors.Is(err, ErrTrashDisabledConfirmationRequired) || !skipped || trashed != 0 {
		t.Fatalf("TrashMediaRetiredByScan trash disabled = %d, %v, %v", trashed, skipped, err)
	}
	if got := st.TrashDisabledSweepSkips(); got != 1 {
		t.Fatalf("TrashDisabledSweepSkips = %d, want 1", got)
	}
	if _, err := st.Media(ctx, image.ID, LibraryFilter{AllowedIDs: []LibraryID{library.ID}, Revealed: true}); err != nil {
		t.Fatalf("media hidden despite disabled trash: %v", err)
	}
}

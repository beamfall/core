package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func framedImport(libraryID LibraryID, path string, width, height int) ImportMediaFile {
	imported := versionImport(libraryID, path, "Disclosure Day")
	imported.VideoWidthHint, imported.VideoHeightHint = width, height
	return imported
}

// TestImportMediaFileVersionDerivesLabelsForUnlabelledCopies: two copies of
// one title without parsed labels are labelled by resolution (CORE-2473); the
// anchor leaves the default slot; a re-import keeps both labels; and a third
// copy joining labelled siblings takes the first of its own labels that is
// free.
func TestImportMediaFileVersionDerivesLabelsForUnlabelledCopies(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	remux := filepath.Join(root, "Disclosure.Day.2026.REPACK.1080p.Remux.mkv")
	media, anchor, err := st.ImportMediaFile(ctx, framedImport("library-local", remux, 1920, 800))
	if err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(root, "Disclosure.Day.2026.2160p.WEB-DL.mkv")
	req := MediaVersionImport{
		MediaID:           media.ID,
		File:              framedImport("library-local", web, 3840, 1600),
		AnchorFingerprint: anchor.ContentFingerprint,
		DeriveLabels:      true,
	}
	file, err := st.ImportMediaFileVersion(ctx, req)
	if err != nil || file.VersionLabel != "2160p" {
		t.Fatalf("version file = %+v, %v; want 2160p", file, err)
	}
	want := map[string]string{filepath.Base(remux): "1080p", filepath.Base(web): "2160p"}
	if got := versionLabels(t, st, media.ID); !equalLabels(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}

	// The same bytes re-imported, now unprobed, keep their slot.
	req.File.VideoWidthHint, req.File.VideoHeightHint = 0, 0
	if again, err := st.ImportMediaFileVersion(ctx, req); err != nil || again.VersionLabel != "2160p" {
		t.Fatalf("re-import = %+v, %v; want 2160p kept", again, err)
	}

	// A third 2160p copy: its resolution slot is taken, so it takes its
	// container's, and the labelled anchor stays.
	third := filepath.Join(root, "Disclosure.Day.2026.2160p.HDR.mp4")
	req.File = framedImport("library-local", third, 3840, 2160)
	if got, err := st.ImportMediaFileVersion(ctx, req); err != nil || got.VersionLabel != "MP4" {
		t.Fatalf("third copy = %+v, %v; want MP4", got, err)
	}
	want[filepath.Base(third)] = "MP4"
	if got := versionLabels(t, st, media.ID); !equalLabels(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
}

// TestImportMediaFileVersionDerivedLabelsNeedADifference: copies of one frame
// class are told apart by container, and copies that differ only by name may
// be different items, so the store refuses them with ErrVersionSlotTaken and
// leaves the anchor in the default slot.
func TestImportMediaFileVersionDerivedLabelsNeedADifference(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	first := filepath.Join(root, "Night.Harbor.2004.mkv")
	media, anchor, err := st.ImportMediaFile(ctx, framedImport("library-local", first, 1920, 1080))
	if err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(root, "Night.Harbor.2004.mp4")
	file, err := st.ImportMediaFileVersion(ctx, MediaVersionImport{
		MediaID:           media.ID,
		File:              framedImport("library-local", second, 1920, 1080),
		AnchorFingerprint: anchor.ContentFingerprint,
		DeriveLabels:      true,
	})
	if err != nil || file.VersionLabel != "MP4" {
		t.Fatalf("version file = %+v, %v; want MP4", file, err)
	}
	if got := versionLabels(t, st, media.ID)[filepath.Base(first)]; got != "MKV" {
		t.Fatalf("anchor label = %q, want MKV", got)
	}

	other := filepath.Join(root, "b", "Night.Harbor.2004.Theatrical.mkv")
	lone, loneAnchor, err := st.ImportMediaFile(ctx, framedImport("library-local", other, 1920, 1080))
	if err != nil {
		t.Fatal(err)
	}
	twin := filepath.Join(root, "b", "Night.Harbor.2004.Directors.Cut.mkv")
	_, err = st.ImportMediaFileVersion(ctx, MediaVersionImport{
		MediaID:           lone.ID,
		File:              framedImport("library-local", twin, 1920, 1080),
		AnchorFingerprint: loneAnchor.ContentFingerprint,
		DeriveLabels:      true,
	})
	if !errors.Is(err, ErrVersionSlotTaken) || !errors.Is(err, ErrVersionNotAttached) {
		t.Fatalf("twin attach err = %v, want ErrVersionSlotTaken", err)
	}
	if got := versionLabels(t, st, lone.ID); got[filepath.Base(other)] != "" || len(got) != 1 {
		t.Fatalf("lone labels = %v, want the anchor alone in the default slot", got)
	}
}

func TestDerivedVersionLabels(t *testing.T) {
	cases := []struct {
		width, height int
		path          string
		want          [2]string
	}{
		{1920, 800, "/m/A.2026.mkv", [2]string{"1080p", "MKV"}},
		{3840, 2160, "/m/A.mp4", [2]string{"2160p", "MP4"}},
		{2560, 1440, "/m/A.mkv", [2]string{"1440p", "MKV"}},
		{1280, 536, "/m/A.m4v", [2]string{"720p", "M4V"}},
		{720, 576, "/m/A.avi", [2]string{"SD", "AVI"}},
		{7680, 4320, "/m/A.ts", [2]string{"4320p", "TS"}},
		{0, 1080, "/m/A", [2]string{"", ""}},
	}
	for _, c := range cases {
		if got := derivedVersionLabels(c.width, c.height, c.path); got != c.want {
			t.Errorf("derivedVersionLabels(%d, %d, %q) = %q, want %q", c.width, c.height, c.path, got, c.want)
		}
	}
}

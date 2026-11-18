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
// copy joins labelled siblings only by a free label of their kind.
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

	// Versions labelled by resolution admit a copy only by a free resolution
	// class: a second 2160p copy stays its own Media, a 720p one joins.
	req.File = framedImport("library-local", filepath.Join(root, "Disclosure.Day.2026.2160p.HDR.mp4"), 3840, 2160)
	if _, err := st.ImportMediaFileVersion(ctx, req); !errors.Is(err, ErrVersionSlotTaken) {
		t.Fatalf("second 2160p copy err = %v, want ErrVersionSlotTaken", err)
	}
	third := filepath.Join(root, "Disclosure.Day.2026.720p.mp4")
	req.File = framedImport("library-local", third, 1280, 720)
	if got, err := st.ImportMediaFileVersion(ctx, req); err != nil || got.VersionLabel != "720p" {
		t.Fatalf("720p copy = %+v, %v; want 720p", got, err)
	}
	want[filepath.Base(third)] = "720p"
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

// TestImportMediaFileVersionDerivedAnchorReplacedInPlace: new bytes at a
// derived anchor's path take its slot on the same Media (Codex review of
// CORE-2473).
func TestImportMediaFileVersionDerivedAnchorReplacedInPlace(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	remux := filepath.Join(root, "Disclosure.Day.2026.Remux.mkv")
	media, anchor, err := st.ImportMediaFile(ctx, framedImport("library-local", remux, 1920, 1080))
	if err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(root, "Disclosure.Day.2026.WEB.mkv")
	req := MediaVersionImport{MediaID: media.ID, File: framedImport("library-local", web, 3840, 2160), AnchorFingerprint: anchor.ContentFingerprint, DeriveLabels: true}
	if _, err := st.ImportMediaFileVersion(ctx, req); err != nil {
		t.Fatal(err)
	}
	replaced := framedImport("library-local", remux, 1280, 720)
	replaced.ContentFingerprint = HashMediaFilePath(remux + "#new bytes")
	req.File = replaced
	file, err := st.ImportMediaFileVersion(ctx, req)
	if err != nil || file.VersionLabel != "1080p" || file.MediaID != media.ID {
		t.Fatalf("replaced anchor = %+v, %v; want slot 1080p on %s", file, err, media.ID)
	}
	want := map[string]string{filepath.Base(remux): "1080p", filepath.Base(web): "2160p"}
	if got := versionLabels(t, st, media.ID); !equalLabels(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
}

// TestImportMediaFileVersionDerivedJoinNeedsTheVersionsKind: Versions told
// apart by container admit a new file only by a container they lack, so a
// third copy that matches one of them in resolution and container stays its
// own Media (Codex review of CORE-2473).
func TestImportMediaFileVersionDerivedJoinNeedsTheVersionsKind(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	first := filepath.Join(root, "Night.Harbor.2004.mkv")
	media, anchor, err := st.ImportMediaFile(ctx, framedImport("library-local", first, 1920, 1080))
	if err != nil {
		t.Fatal(err)
	}
	attach := func(name string, width, height int) (MediaFile, error) {
		return st.ImportMediaFileVersion(ctx, MediaVersionImport{
			MediaID:           media.ID,
			File:              framedImport("library-local", filepath.Join(root, name), width, height),
			AnchorFingerprint: anchor.ContentFingerprint,
			DeriveLabels:      true,
		})
	}
	if file, err := attach("Night.Harbor.2004.mp4", 1920, 1080); err != nil || file.VersionLabel != "MP4" {
		t.Fatalf("mp4 = %+v, %v", file, err)
	}
	if _, err := attach("Night.Harbor.2004.Copy.mkv", 1920, 1080); !errors.Is(err, ErrVersionSlotTaken) {
		t.Fatalf("third mkv err = %v, want ErrVersionSlotTaken", err)
	}
	if file, err := attach("Night.Harbor.2004.m4v", 3840, 2160); err != nil || file.VersionLabel != "M4V" {
		t.Fatalf("m4v = %+v, %v; want M4V", file, err)
	}
}

// TestImportMediaFileVersionRelabelledAnchorDropsItsFrame: relabelling the
// anchor can leave another existing file first; the Media row then stops
// claiming the anchor's frame size (Codex review of CORE-2473).
func TestImportMediaFileVersionRelabelledAnchorDropsItsFrame(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	media, anchor, err := st.ImportMediaFile(ctx, framedImport("library-local", filepath.Join(root, "a.mkv"), 3840, 2160))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportMediaFileVersion(ctx, MediaVersionImport{MediaID: media.ID, File: framedImport("library-local", filepath.Join(root, "b.mkv"), 1920, 1080), Label: "1080p"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportMediaFileVersion(ctx, MediaVersionImport{
		MediaID:           media.ID,
		File:              framedImport("library-local", filepath.Join(root, "c.mkv"), 1280, 720),
		Label:             "720p",
		AnchorFingerprint: anchor.ContentFingerprint,
		AnchorLabel:       "Zeta",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := st.Media(ctx, media.ID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.VideoWidth != 0 || got.VideoHeight != 0 {
		t.Fatalf("frame = %dx%d, want unknown once 1080p b.mkv is first", got.VideoWidth, got.VideoHeight)
	}
}

package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func versionImport(libraryID LibraryID, path, title string) ImportMediaFile {
	return ImportMediaFile{
		LibraryID:          libraryID,
		Path:               path,
		Title:              title,
		Container:          "mkv",
		Codec:              "hevc",
		Class:              MediaClassVideo,
		ContentFingerprint: HashMediaFilePath(path),
		Streams:            []MediaFileStream{{Index: 0, Type: "video", Codec: "hevc"}},
	}
}

func versionLabels(t *testing.T, st *Store, mediaID MediaID) map[string]string {
	t.Helper()
	files, err := st.MediaFilesForMedia(context.Background(), mediaID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, f := range files {
		labels[filepath.Base(f.Path)] = f.VersionLabel
	}
	return labels
}

// TestImportMediaFileVersionGroupsFilesOfOneTitle: a 4K file joins the 1080p
// file's Media as a labelled Version, and a re-scan keeps both slots.
func TestImportMediaFileVersionGroupsFilesOfOneTitle(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	anchorPath := filepath.Join(root, "Night Harbor (2004) - 1080p.mkv")
	media, anchor, err := st.ImportMediaFile(ctx, versionImport("library-local", anchorPath, "Night Harbor"))
	if err != nil {
		t.Fatal(err)
	}
	uhdPath := filepath.Join(root, "Night Harbor (2004) - 4K.mkv")
	req := MediaVersionImport{
		MediaID:           media.ID,
		File:              versionImport("library-local", uhdPath, "Night Harbor"),
		Label:             "4K",
		AnchorFingerprint: anchor.ContentFingerprint,
		AnchorLabel:       "1080p",
	}
	file, err := st.ImportMediaFileVersion(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if file.MediaID != media.ID || file.VersionLabel != "4K" {
		t.Fatalf("version file = %+v", file)
	}
	want := map[string]string{filepath.Base(anchorPath): "1080p", filepath.Base(uhdPath): "4K"}
	if got := versionLabels(t, st, media.ID); !equalLabels(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	streams, err := st.MediaFileStreams(ctx, file.ContentFingerprint)
	if err != nil || len(streams) != 1 {
		t.Fatalf("version streams = %v, %v", streams, err)
	}

	// A re-scan of the same bytes keeps the slot even under another label.
	req.Label = "UHD"
	if again, err := st.ImportMediaFileVersion(ctx, req); err != nil || again.VersionLabel != "4K" {
		t.Fatalf("re-scan = %+v, %v; want slot 4K kept", again, err)
	}
	if got := versionLabels(t, st, media.ID); !equalLabels(got, want) {
		t.Fatalf("labels after re-scan = %v, want %v", got, want)
	}
}

// TestImportMediaFileVersionReplacedInPlace: new bytes at a Version's path
// retire the old bytes first, so they take the slot the old bytes held.
func TestImportMediaFileVersionReplacedInPlace(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	media, anchor, err := st.ImportMediaFile(ctx, versionImport("library-local", filepath.Join(root, "Night Harbor (2004) - 1080p.mkv"), "Night Harbor"))
	if err != nil {
		t.Fatal(err)
	}
	uhdPath := filepath.Join(root, "Night Harbor (2004) - 4K.mkv")
	req := MediaVersionImport{
		MediaID:           media.ID,
		File:              versionImport("library-local", uhdPath, "Night Harbor"),
		Label:             "4K",
		AnchorFingerprint: anchor.ContentFingerprint,
		AnchorLabel:       "1080p",
	}
	if _, err := st.ImportMediaFileVersion(ctx, req); err != nil {
		t.Fatal(err)
	}
	req.File.ContentFingerprint = HashMediaFilePath(uhdPath + "#remux")
	replaced, err := st.ImportMediaFileVersion(ctx, req)
	if err != nil || replaced.VersionLabel != "4K" {
		t.Fatalf("replaced bytes = %+v, %v; want the 4K slot", replaced, err)
	}
	files, err := st.MediaFilesForMedia(ctx, media.ID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil || len(files) != 2 {
		t.Fatalf("live files = %+v, %v; want the 1080p file and the new 4K bytes", files, err)
	}
	for _, f := range files {
		if f.Path == uhdPath && f.ContentFingerprint != req.File.ContentFingerprint {
			t.Fatalf("4K slot still serves the old bytes %q", f.ContentFingerprint)
		}
	}
}

// TestImportMediaFileDetachesVersionCopiedToAnotherLibrary: a Version's bytes
// scanned in another Library become a Media of their own there; the source
// Media and its other Version stay in their Library.
func TestImportMediaFileDetachesVersionCopiedToAnotherLibrary(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	media, anchor, err := st.ImportMediaFile(ctx, versionImport("library-local", filepath.Join(root, "Night Harbor (2004) - 1080p.mkv"), "Night Harbor"))
	if err != nil {
		t.Fatal(err)
	}
	uhd := versionImport("library-local", filepath.Join(root, "Night Harbor (2004) - 4K.mkv"), "Night Harbor")
	if _, err := st.ImportMediaFileVersion(ctx, MediaVersionImport{
		MediaID: media.ID, File: uhd, Label: "4K",
		AnchorFingerprint: anchor.ContentFingerprint, AnchorLabel: "1080p",
	}); err != nil {
		t.Fatal(err)
	}

	copied := versionImport("library-archive", filepath.Join(root, "archive", "Night Harbor (2004) - 1080p.mkv"), "Night Harbor")
	// The anchor's own fingerprint derives the source Media's ID, so the
	// detached Media needs another.
	copied.ContentFingerprint = anchor.ContentFingerprint
	moved, file, err := st.ImportMediaFile(ctx, copied)
	if err != nil {
		t.Fatal(err)
	}
	if moved.ID == media.ID || moved.LibraryID != "library-archive" || file.VersionLabel != "" {
		t.Fatalf("copied Version = %+v / %+v, want a new archive Media in the default slot", moved, file)
	}
	archived, err := st.MediaFilesForMedia(ctx, moved.ID, LibraryFilter{AllowedIDs: []LibraryID{"library-archive"}, Revealed: true})
	if err != nil || len(archived) != 1 {
		t.Fatalf("archive Media files = %+v, %v; want only the copied file", archived, err)
	}
	source, err := st.MediaFilesForMedia(ctx, media.ID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil || len(source) != 1 || source[0].ContentFingerprint != uhd.ContentFingerprint {
		t.Fatalf("source Media files = %+v, %v; want the 4K file left behind", source, err)
	}
	// A re-scan of the archive copy keeps its new Media.
	if again, _, err := st.ImportMediaFile(ctx, copied); err != nil || again.ID != moved.ID {
		t.Fatalf("archive re-scan = %+v, %v; want Media %s", again, err, moved.ID)
	}
}

// TestImportMediaFileVersionUnlabelledTakesDefaultSlot: a labelled file
// imported first moves out of the default slot when an unlabelled sibling
// arrives, so the unlabelled file becomes the primary.
func TestImportMediaFileVersionUnlabelledTakesDefaultSlot(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	extendedPath := filepath.Join(root, "Coastline (1998) - Extended.mkv")
	extended := versionImport("library-local", extendedPath, "Coastline")
	extended.VideoWidthHint, extended.VideoHeightHint, extended.DurationSecondsHint = 3840, 2160, 140
	media, anchor, err := st.ImportMediaFile(ctx, extended)
	if err != nil {
		t.Fatal(err)
	}
	plainPath := filepath.Join(root, "Coastline (1998).mkv")
	plain := versionImport("library-local", plainPath, "Coastline")
	plain.Codec, plain.VideoWidthHint, plain.VideoHeightHint, plain.DurationSecondsHint = "h264", 1920, 1080, 120
	if _, err := st.ImportMediaFileVersion(ctx, MediaVersionImport{
		MediaID:           media.ID,
		File:              plain,
		AnchorFingerprint: anchor.ContentFingerprint,
		AnchorLabel:       "Extended",
	}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{filepath.Base(extendedPath): "Extended", filepath.Base(plainPath): ""}
	if got := versionLabels(t, st, media.ID); !equalLabels(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	primary, err := st.PrimaryMediaFile(ctx, media.ID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil || filepath.Base(primary.Path) != filepath.Base(plainPath) {
		t.Fatalf("primary = %+v, %v; want the unlabelled file", primary, err)
	}
	// The row now describes the first Version, the unlabelled file, which
	// playback judges by the row.
	row, err := st.Media(ctx, media.ID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if row.Codec != "h264" || row.VideoWidth != 1920 || row.VideoHeight != 1080 || row.DurationSeconds != 120 {
		t.Fatalf("row facts = %s %dx%d %vs, want the unlabelled file's h264 1920x1080 120s", row.Codec, row.VideoWidth, row.VideoHeight, row.DurationSeconds)
	}
}

// TestMediaRowKeepsFirstVersionFacts: the row holds the first Version's probe
// facts. A plain re-import of a later Version, as a rescan's seeded anchor
// does, leaves them alone; a first Version attached without a probe keeps
// the known duration; and the first Version still names the row's facts
// after its file goes missing.
func TestMediaRowKeepsFirstVersionFacts(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	root := t.TempDir()
	extendedPath := filepath.Join(root, "Coastline (1998) - Extended.mkv")
	extended := versionImport("library-local", extendedPath, "Coastline")
	extended.VideoWidthHint, extended.VideoHeightHint, extended.DurationSecondsHint = 3840, 2160, 140
	media, anchor, err := st.ImportMediaFile(ctx, extended)
	if err != nil {
		t.Fatal(err)
	}
	plainPath := filepath.Join(root, "Coastline (1998).mkv")
	plain := versionImport("library-local", plainPath, "Coastline")
	plain.Codec, plain.VideoWidthHint, plain.VideoHeightHint, plain.DurationSecondsHint = "h264", 1920, 1080, 120
	if _, err := st.ImportMediaFileVersion(ctx, MediaVersionImport{
		MediaID: media.ID, File: plain, AnchorFingerprint: anchor.ContentFingerprint, AnchorLabel: "Extended",
	}); err != nil {
		t.Fatal(err)
	}
	wantRow := func(when string) {
		t.Helper()
		row, err := st.Media(ctx, media.ID, filter)
		if err != nil {
			t.Fatal(err)
		}
		if row.Codec != "h264" || row.VideoWidth != 1920 || row.VideoHeight != 1080 || row.DurationSeconds != 120 {
			t.Fatalf("%s: row facts = %s %dx%d %vs, want the first Version's h264 1920x1080 120s", when, row.Codec, row.VideoWidth, row.VideoHeight, row.DurationSeconds)
		}
	}
	reimported, _, err := st.ImportMediaFile(ctx, extended)
	if err != nil {
		t.Fatal(err)
	}
	if reimported.ID != media.ID {
		t.Fatalf("re-import landed on %s, want %s", reimported.ID, media.ID)
	}
	if reimported.Codec != "h264" || reimported.VideoHeight != 1080 || reimported.DurationSeconds != 120 {
		t.Fatalf("re-import returned %s %dp %vs, want the stored first Version's facts", reimported.Codec, reimported.VideoHeight, reimported.DurationSeconds)
	}
	wantRow("after a plain re-import of the Extended Version")

	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media_file SET missing_at_ms = 1 WHERE content_fingerprint = ?`, plain.ContentFingerprint); err != nil {
		t.Fatal(err)
	}
	if fp, err := st.MediaFactsFingerprint(ctx, media.ID); err != nil || fp != plain.ContentFingerprint {
		t.Fatalf("MediaFactsFingerprint with the first file missing = %q, %v; want the missing first file %q", fp, err, plain.ContentFingerprint)
	}
	wantRow("with the first Version's file missing")
}

// TestImportMediaFileVersionWithoutProbeKeepsDuration: a first Version
// attached with no probe facts takes the slot's container and codec but does
// not erase the duration already known for the title.
func TestImportMediaFileVersionWithoutProbeKeepsDuration(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	extendedPath := filepath.Join(root, "Night Harbor (2001) - Extended.mkv")
	extended := versionImport("library-local", extendedPath, "Night Harbor")
	extended.VideoWidthHint, extended.VideoHeightHint, extended.DurationSecondsHint = 1920, 1080, 140
	media, anchor, err := st.ImportMediaFile(ctx, extended)
	if err != nil {
		t.Fatal(err)
	}
	uhdPath := filepath.Join(root, "Night Harbor (2001) - 4K.mkv")
	uhd := versionImport("library-local", uhdPath, "Night Harbor")
	if _, err := st.ImportMediaFileVersion(ctx, MediaVersionImport{
		MediaID: media.ID, File: uhd, Label: "4K", AnchorFingerprint: anchor.ContentFingerprint, AnchorLabel: "Extended",
	}); err != nil {
		t.Fatal(err)
	}
	if fp, err := st.MediaFactsFingerprint(ctx, media.ID); err != nil || fp != uhd.ContentFingerprint {
		t.Fatalf("MediaFactsFingerprint = %q, %v; want the 4K file, first in label order", fp, err)
	}
	row, err := st.Media(ctx, media.ID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if row.DurationSeconds != 140 || row.VideoWidth != 0 || row.VideoHeight != 0 {
		t.Fatalf("row after a probe-less first Version = %dx%d %vs, want unknown frame size and the known 140s", row.VideoWidth, row.VideoHeight, row.DurationSeconds)
	}
}

// TestImportMediaFileVersionRefusesAndRollsBack: a taken slot, bytes on
// another Media, a Media in another Library or a bad label all refuse, and a
// refusal undoes the anchor relabel.
func TestImportMediaFileVersionRefusesAndRollsBack(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	firstPath := filepath.Join(root, "Night Harbor (2004).mkv")
	media, anchor, err := st.ImportMediaFile(ctx, versionImport("library-local", firstPath, "Night Harbor"))
	if err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(root, "Coastline (1998).mkv")
	if _, _, err := st.ImportMediaFile(ctx, versionImport("library-local", otherPath, "Coastline")); err != nil {
		t.Fatal(err)
	}

	cases := map[string]MediaVersionImport{
		// Two unlabelled files of one title: the default slot is taken. The
		// anchor relabel to "" is a no-op, so this checks the slot guard.
		"default slot taken": {MediaID: media.ID, File: versionImport("library-local", filepath.Join(root, "Night Harbor (2004) copy.mkv"), "Night Harbor")},
		// Coastline's bytes already belong to Coastline's Media.
		"bytes on another media": {MediaID: media.ID, File: versionImport("library-local", otherPath, "Coastline"), Label: "Alt",
			AnchorFingerprint: anchor.ContentFingerprint, AnchorLabel: "Theatrical"},
		"media in another library": {MediaID: media.ID, File: versionImport("library-archive", filepath.Join(root, "Night Harbor (2004) - 4K.mkv"), "Night Harbor"), Label: "4K"},
		"unknown media":            {MediaID: "media-absent", File: versionImport("library-local", filepath.Join(root, "x.mkv"), "x"), Label: "4K"},
	}
	for name, req := range cases {
		if _, err := st.ImportMediaFileVersion(ctx, req); !errors.Is(err, ErrVersionNotAttached) {
			t.Errorf("%s: err = %v, want ErrVersionNotAttached", name, err)
		}
	}
	if got := versionLabels(t, st, media.ID); !equalLabels(got, map[string]string{filepath.Base(firstPath): ""}) {
		t.Fatalf("refused attaches changed the Media: %v", got)
	}

	for _, label := range []string{" 4K", "a\tb", strings.Repeat("x", MaxMediaVersionLabelLen+1)} {
		_, err := st.ImportMediaFileVersion(ctx, MediaVersionImport{MediaID: media.ID, File: versionImport("library-local", filepath.Join(root, "y.mkv"), "y"), Label: label})
		if err == nil || errors.Is(err, ErrVersionNotAttached) {
			t.Errorf("label %q: err = %v, want a validation error", label, err)
		}
	}
}

func equalLabels(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			return false
		}
	}
	return true
}

// TestMediaFilesInDirectoryListsOnlyDirectChildren: files in a subdirectory or
// in a directory whose name extends this one are left out.
func TestMediaFilesInDirectoryListsOnlyDirectChildren(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "Night Harbor (2004)")
	for _, path := range []string{
		filepath.Join(dir, "Night Harbor (2004) - 4K.mkv"),
		filepath.Join(dir, "extras", "Night Harbor (2004) - Trailer.mkv"),
		filepath.Join(root, "Night Harbor (2004)2", "Night Harbor (2004).mkv"),
		filepath.Join(root, "Night Harbor (2004)-copy", "Night Harbor (2004).mkv"),
	} {
		if _, _, err := st.ImportMediaFile(ctx, versionImport("library-local", path, "Night Harbor")); err != nil {
			t.Fatal(err)
		}
	}
	files, err := st.MediaFilesInDirectory(ctx, "library-local", dir)
	if err != nil || len(files) != 1 || filepath.Base(files[0].Path) != "Night Harbor (2004) - 4K.mkv" {
		t.Fatalf("files = %+v, %v; want only the 4K file", files, err)
	}
}

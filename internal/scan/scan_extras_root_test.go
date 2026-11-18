package scan

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestIsExtraPathCountsOnlyFoldersBelowRoot (CORE-2189): the Extras folder
// conventions apply only between the Library root and the file. The root
// itself and every folder above it are ignored.
func TestIsExtraPathCountsOnlyFoldersBelowRoot(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "mnt", "Trailers", "Library")
	tvRoot := filepath.Join(string(filepath.Separator), "mnt", "user", "tv")
	cases := []struct {
		name string
		root string
		path string
		want bool
	}{
		{"ancestor above root", root, filepath.Join(root, "Synthetic Title", "Synthetic Title.mkv"), false},
		{"file directly in root", root, filepath.Join(root, "Synthetic Title.mkv"), false},
		{"root itself named Extras", filepath.Join(string(filepath.Separator), "data", "Extras"), filepath.Join(string(filepath.Separator), "data", "Extras", "Synthetic Title.mkv"), false},
		{"Extras folder below root", root, filepath.Join(root, "Synthetic Title", "Extras", "Making Of.mkv"), true},
		{"nested below an Extras folder", root, filepath.Join(root, "Synthetic Title", "featurettes", "Disc 1", "Clip.mkv"), true},
		{"suffix convention", root, filepath.Join(root, "Synthetic Title-trailer.mkv"), true},
		{"path outside root", root, filepath.Join(string(filepath.Separator), "other", "Extras", "Clip.mkv"), false},
		{"no root", "", filepath.Join(string(filepath.Separator), "x", "Extras", "Clip.mkv"), false},
		// CORE-2424: a season-numbered bonus label alone is not a static Extra;
		// it is one only when its folder has a sole parent (see
		// TestExtraOrdinalStemMatches and TestScanOrdinalStemNeedsSoleParent).
		{"season extra outtakes", tvRoot, filepath.Join(tvRoot, "The IT Crowd", "The IT Crowd  Series 3", "3.x Out Takes.mkv"), false},
		{"season extra deleted scene", tvRoot, filepath.Join(tvRoot, "The IT Crowd", "The IT Crowd  Series 3", "3.x Deleted Scene.mkv"), false},
		{"season extra set tour", tvRoot, filepath.Join(tvRoot, "The IT Crowd", "The IT Crowd  Series 3", "3.x Set Tour.mkv"), false},
		{"season by episode code", tvRoot, filepath.Join(tvRoot, "The IT Crowd", "3x07 Calamity Jen.mkv"), false},
		{"leading episode pair", tvRoot, filepath.Join(tvRoot, "The IT Crowd", "The IT Crowd  Series 3", "3.01 From Hell.mkv"), false},
		{"plain outtakes title", root, filepath.Join(root, "Outtakes", "Outtakes.mkv"), false},
		{"exact library-tv outtakes name", tvRoot, filepath.Join(tvRoot, "The IT Crowd", "The IT Crowd  Series 3", "3 x Out Takes.mkv"), false},
		// The Plex/Jellyfin extras folders and suffixes. The common-word folders
		// count only inside a title folder, never as a top-level category.
		{"Behind The Scenes in a show", tvRoot, filepath.Join(tvRoot, "The IT Crowd", "Behind The Scenes", "Making Of.mkv"), true},
		{"Deleted Scenes in a season", tvRoot, filepath.Join(tvRoot, "The IT Crowd", "Season 3", "Deleted Scenes", "Cut.mkv"), true},
		{"Other in a title folder", root, filepath.Join(root, "Synthetic Title", "Other", "Clip.mkv"), true},
		{"Other as a top-level category", root, filepath.Join(root, "Other", "Synthetic Title.mkv"), false},
		{"Scenes as a top-level category", root, filepath.Join(root, "Scenes", "Synthetic Title", "Synthetic Title.mkv"), false},
		{"deleted suffix", root, filepath.Join(root, "Synthetic Title", "Synthetic Title-deleted.mkv"), true},
		{"scene suffix", root, filepath.Join(root, "Synthetic Title", "Opening-scene.mkv"), true},
		{"numbered music track", root, filepath.Join(root, "X Ambassadors", "VHS", "01 - X Ambassadors - Renegades.flac"), false},
		{"numbered music track without dash", root, filepath.Join(root, "X Ambassadors", "VHS", "01 X Ambassadors.flac"), false},
		{"music in an artist's Other folder", root, filepath.Join(root, "Artist", "Other", "01 Song.flac"), false},
		{"photo in a Scenes folder", root, filepath.Join(root, "2026", "Scenes", "IMG_001.jpg"), false},
		{"song ending in -other", root, filepath.Join(root, "Artist", "Album", "The Other.flac"), false},
		{"song with a -scene suffix", root, filepath.Join(root, "Artist", "Album", "Opening-scene.flac"), false},
		{"trailer suffix stays for any type", root, filepath.Join(root, "Synthetic Title", "Synthetic Title-trailer.mp3"), true},
		{"dashed number before an x title", tvRoot, filepath.Join(tvRoot, "Synthetic Show", "01 - X Marks the Spot.mkv"), false},
	}
	for _, tc := range cases {
		if got := isExtraPath(tc.root, tc.path); got != tc.want {
			t.Errorf("%s: isExtraPath(%q, %q) = %v, want %v", tc.name, tc.root, tc.path, got, tc.want)
		}
	}
}

// TestScanLibraryUnderTrailersFolderImportsMainMedia (CORE-2189): a Library
// rooted under a folder named Trailers indexes its files as main Media, and
// an Extras folder inside the Library is still an Extra.
func TestScanLibraryUnderTrailersFolderImportsMainMedia(t *testing.T) {
	// feature:scan-pipeline
	root := filepath.Join(t.TempDir(), "Trailers", "Films")
	writeScanFile(t, root, "Synthetic Feature/Synthetic Feature.mp4", []byte("feature content"))
	writeScanFile(t, root, "Other Feature.mp4", []byte("other feature content"))
	writeScanFile(t, root, "Synthetic Feature/Extras/Behind It.mp4", []byte("extra content"))
	st := storetest.Open(t)
	ctx := context.Background()

	result, err := Scan(ctx, st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.FilesSeen != 3 {
		t.Fatalf("FilesSeen = %d, want 3", result.FilesSeen)
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	items, err := st.SourceItems(ctx, filter, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Synthetic Feature", "Other Feature"} {
		if !hasSourceItemWithTitle(items, title) {
			t.Fatalf("browse Source omitted main Media %q under a Trailers ancestor: %#v", title, items)
		}
	}
	if hasSourceItemWithTitle(items, "Behind It") {
		t.Fatalf("browse Source leaked the Extras/ folder file: %#v", items)
	}

	var featureID, extraID store.MediaID
	for _, f := range result.Files {
		switch filepath.Base(f.Path) {
		case "Synthetic Feature.mp4":
			featureID = f.MediaID
		case "Behind It.mp4":
			extraID = f.MediaID
		}
	}
	extras, err := st.MediaRelationsOf(ctx, featureID, store.MediaRelationExtraOf, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(extras) != 1 || extras[0].ID != extraID {
		t.Fatalf("extras of feature = %#v, want only %q", extras, extraID)
	}
}

// TestEstimateCountsLibraryUnderExtrasFolder (CORE-2189): the first-scan
// estimate applies the same root-bounded rule.
func TestEstimateCountsLibraryUnderExtrasFolder(t *testing.T) {
	// feature:first-scan-estimate
	root := filepath.Join(t.TempDir(), "Extras", "Films")
	writeScanFile(t, root, "Synthetic Feature.mkv", []byte("video bytes"))
	writeScanFile(t, root, "Trailers/Clip.mkv", []byte("an extra"))

	est, err := EstimateRoots(context.Background(), []string{root}, ImagesBesideMedia)
	if err != nil {
		t.Fatalf("EstimateRoots: %v", err)
	}
	if est.EstimatedFiles != 1 {
		t.Fatalf("EstimatedFiles = %d, want 1 (the feature; the Trailers/ file below the root is an Extra)", est.EstimatedFiles)
	}
}

// TestSubfolderScanBoundsExtrasByLibraryRoot (CORE-2189): a scan aimed at a
// folder inside the Library still bounds the Extras conventions by the
// configured Library root, so scanning <root>/Feature/Extras imports its new
// file as an Extra rather than as main Media.
func TestSubfolderScanBoundsExtrasByLibraryRoot(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	writeScanFile(t, root, "Synthetic Feature/Synthetic Feature.mp4", []byte("feature content"))
	writeScanFile(t, root, "Synthetic Feature/Extras/Behind It.mp4", []byte("extra content"))
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatalf("SetLibraryRoots: %v", err)
	}

	if _, err := Scan(ctx, st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: filepath.Join(root, "Synthetic Feature")}); err != nil {
		t.Fatalf("Scan feature folder: %v", err)
	}
	writeScanFile(t, root, "Synthetic Feature/Extras/New Clip.mp4", []byte("new extra content"))
	if _, err := Scan(ctx, st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: filepath.Join(root, "Synthetic Feature", "Extras")}); err != nil {
		t.Fatalf("Scan Extras folder: %v", err)
	}

	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	items, err := st.SourceItems(ctx, filter, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSourceItemWithTitle(items, "Synthetic Feature") {
		t.Fatalf("browse Source omitted the main Media: %#v", items)
	}
	for _, title := range []string{"Behind It", "New Clip"} {
		if hasSourceItemWithTitle(items, title) {
			t.Fatalf("subfolder scan imported Extras/ file %q as main Media: %#v", title, items)
		}
	}
}

// TestRescanClearsStaleExtraFlagFromAboveRootFolder (CORE-2189): a file
// stored as an Extra because of a folder that is not below its Library root
// (the pre-fix classification) is main Media again after a rescan.
func TestRescanClearsStaleExtraFlagFromAboveRootFolder(t *testing.T) {
	// feature:scan-pipeline
	parent := t.TempDir()
	root := filepath.Join(parent, "Trailers", "Films")
	writeScanFile(t, root, "Synthetic Feature.mp4", []byte("feature content"))
	st := storetest.Open(t)
	ctx := context.Background()

	// With no configured roots the scan Root bounds the conventions, so a scan
	// of the parent stores the file as an Extra under Trailers/.
	if _, err := Scan(ctx, st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: parent}); err != nil {
		t.Fatalf("Scan parent: %v", err)
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	items, err := st.SourceItems(ctx, filter, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if hasSourceItemWithTitle(items, "Synthetic Feature") {
		t.Fatalf("setup: file below Trailers/ was not stored as an Extra: %#v", items)
	}

	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatalf("SetLibraryRoots: %v", err)
	}
	if _, err := Scan(ctx, st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	items, err = st.SourceItems(ctx, filter, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSourceItemWithTitle(items, "Synthetic Feature") {
		t.Fatalf("rescan kept the stale Extra flag; main Media missing from browse: %#v", items)
	}
}

// TestExtraParentCoversEveryExtraConvention: every path isExtraPath marks as
// an Extra resolves its parent by the same convention, so no classified Extra
// silently loses its extra-of relation. The newly recognised video suffixes,
// the ordinal stem and the title-scoped Extras folders are covered here.
func TestExtraParentCoversEveryExtraConvention(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "mnt", "media")
	titleDir := filepath.Join(root, "Synthetic Title")
	seasonDir := filepath.Join(root, "Synthetic Show", "Season 3")
	soloSeasonDir := filepath.Join(root, "Solo Show", "Series 2")
	byDir := map[string][]store.MediaID{
		titleDir:      {"title"},
		seasonDir:     {"ep1", "ep2"},
		soloSeasonDir: {"solo"},
	}
	byDirTitle := map[extraParentTitleKey]store.MediaID{
		{dir: titleDir, title: "synthetic title"}: "title",
		{dir: seasonDir, title: "pilot"}:          "ep1",
	}
	cases := []struct {
		name string
		path string
		want store.MediaID
	}{
		{"established suffix", filepath.Join(titleDir, "Synthetic Title-trailer.mkv"), "title"},
		{"deleted suffix", filepath.Join(titleDir, "Synthetic Title-deleted.mkv"), "title"},
		{"deletedscene suffix", filepath.Join(titleDir, "Synthetic Title-DeletedScene.mkv"), "title"},
		{"scene suffix", filepath.Join(titleDir, "Synthetic Title-scene.mkv"), "title"},
		{"clip suffix", filepath.Join(titleDir, "Synthetic Title-clip.mp4"), "title"},
		{"other suffix", filepath.Join(titleDir, "Synthetic Title-other.mkv"), "title"},
		{"extra suffix", filepath.Join(titleDir, "Synthetic Title-extra.mkv"), "title"},
		{"episode suffix", filepath.Join(seasonDir, "Pilot-deleted.mkv"), "ep1"},
		{"suffix with no matching title", filepath.Join(titleDir, "Elsewhere-deleted.mkv"), ""},
		{"ordinal stem, sole Media", filepath.Join(soloSeasonDir, "2.x Out Takes.mkv"), "solo"},
		{"ordinal stem, spaced x", filepath.Join(soloSeasonDir, "2 x Set Tour.mkv"), "solo"},
		{"ordinal stem, several Media", filepath.Join(seasonDir, "3.x Out Takes.mkv"), ""},
		{"ordinal stem inside an Extras folder", filepath.Join(titleDir, "Extras", "3.x Out Takes.mkv"), "title"},
		{"suffix inside an Extras folder", filepath.Join(titleDir, "Extras", "Cut-deleted.mkv"), "title"},
		{"established suffix inside an Extras folder", filepath.Join(titleDir, "Trailers", "Teaser-trailer.mkv"), "title"},
		{"Behind The Scenes folder", filepath.Join(titleDir, "Behind The Scenes", "Making Of.mkv"), "title"},
		{"Deleted Scenes folder", filepath.Join(titleDir, "Deleted Scenes", "Cut.mkv"), "title"},
		{"Scenes folder", filepath.Join(titleDir, "Scenes", "Opening.mkv"), "title"},
		{"Clips folder", filepath.Join(titleDir, "Clips", "Clip.mkv"), "title"},
		{"Samples folder", filepath.Join(titleDir, "Samples", "Sample.mkv"), "title"},
		{"Other folder", filepath.Join(titleDir, "Other", "Clip.mkv"), "title"},
		{"Extras folder beside several Media", filepath.Join(seasonDir, "Deleted Scenes", "Cut.mkv"), ""},
	}
	for _, tc := range cases {
		if match := matchExtraPath(root, tc.path); match.mainMedia() {
			t.Fatalf("%s: matchExtraPath(%q) matched no Extra convention", tc.name, tc.path)
		}
		if got := extraParentMediaID(root, tc.path, byDir, byDirTitle); got != tc.want {
			t.Errorf("%s: extraParentMediaID(%q) = %q, want %q", tc.name, tc.path, got, tc.want)
		}
	}
}

// TestScanLinksNewExtraConventionsToParent: an end-to-end scan links a
// -deleted suffix file, an ordinal-stem file and a title-scoped Extras folder
// file to the sole main Media of their folder.
func TestScanLinksNewExtraConventionsToParent(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	writeScanFile(t, root, "Synthetic Feature/Synthetic Feature.mp4", []byte("feature content"))
	writeScanFile(t, root, "Synthetic Feature/Synthetic Feature-deleted.mp4", []byte("deleted content"))
	writeScanFile(t, root, "Synthetic Feature/1.x Out Takes.mp4", []byte("outtakes content"))
	writeScanFile(t, root, "Synthetic Feature/Behind The Scenes/Making Of.mp4", []byte("making-of content"))
	st := storetest.Open(t)
	ctx := context.Background()
	result, err := Scan(ctx, st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	ids := map[string]store.MediaID{}
	for _, f := range result.Files {
		ids[filepath.Base(f.Path)] = f.MediaID
	}
	featureID := ids["Synthetic Feature.mp4"]
	if featureID == "" {
		t.Fatalf("main Media not imported: %#v", ids)
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	extras, err := st.MediaRelationsOf(ctx, featureID, store.MediaRelationExtraOf, filter)
	if err != nil {
		t.Fatalf("MediaRelationsOf(extra-of): %v", err)
	}
	got := map[store.MediaID]bool{}
	for _, extra := range extras {
		got[extra.ID] = true
	}
	for _, name := range []string{"Synthetic Feature-deleted.mp4", "1.x Out Takes.mp4", "Making Of.mp4"} {
		if ids[name] == "" || !got[ids[name]] {
			t.Errorf("extra %q (id %q) not linked to main Media; extras = %#v", name, ids[name], extras)
		}
	}
}

// TestExtraOrdinalStemMatches: the ordinal-stem convention recognises a
// group-numbered bonus label and never an ordinal pair or a numbered track.
func TestExtraOrdinalStemMatches(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "mnt", "user", "tv")
	dir := filepath.Join(root, "The IT Crowd", "The IT Crowd  Series 3")
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"dotted x", filepath.Join(dir, "3.x Out Takes.mkv"), true},
		{"spaced x", filepath.Join(dir, "3 x Out Takes.mkv"), true},
		{"bare x", filepath.Join(dir, "3x Set Tour.mkv"), true},
		{"episode code", filepath.Join(dir, "3x07 Calamity Jen.mkv"), false},
		{"episode pair", filepath.Join(dir, "3.01 From Hell.mkv"), false},
		{"dashed number", filepath.Join(dir, "01 - X Marks the Spot.mkv"), false},
		{"audio file", filepath.Join(dir, "3.x Out Takes.flac"), false},
	}
	for _, tc := range cases {
		if got := matchExtraPath(root, tc.path).ordinal; got != tc.want {
			t.Errorf("%s: ordinal(%q) = %v, want %v", tc.name, tc.path, got, tc.want)
		}
	}
}

// TestScanOrdinalStemNeedsSoleParent: an ordinal-stem file beside several
// main Media is ordinary visible Media, not a hidden parentless Extra, and a
// rescan clears the Extra flag it got while its folder had a sole parent.
func TestScanOrdinalStemNeedsSoleParent(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	writeScanFile(t, root, "Synthetic Show/Season 3/Pilot.mp4", []byte("pilot content"))
	writeScanFile(t, root, "Synthetic Show/Season 3/3.x Out Takes.mp4", []byte("outtakes content"))
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatalf("SetLibraryRoots: %v", err)
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	scanVisible := func() []store.SourceItem {
		t.Helper()
		if _, err := Scan(ctx, st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		items, err := st.SourceItems(ctx, filter, store.SourceQuery{Kind: "recently-added"})
		if err != nil {
			t.Fatal(err)
		}
		return items
	}

	if items := scanVisible(); hasSourceItemWithTitle(items, "3.x Out Takes") {
		t.Fatalf("setup: ordinal-stem file beside a sole parent was not an Extra: %#v", items)
	}

	writeScanFile(t, root, "Synthetic Show/Season 3/Second.mp4", []byte("second content"))
	items := scanVisible()
	if !hasSourceItemWithTitle(items, "3.x Out Takes") {
		t.Fatalf("ordinal-stem file beside several main Media is hidden: %#v", items)
	}
	for _, title := range []string{"Pilot", "Second"} {
		if !hasSourceItemWithTitle(items, title) {
			t.Fatalf("main Media %q missing from browse: %#v", title, items)
		}
	}
}

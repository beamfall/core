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
		// CORE-2424: season-numbered bonus labels in a TV library are Extras;
		// an episode code and a plain title are not.
		{"season extra outtakes", tvRoot, filepath.Join(tvRoot, "The IT Crowd", "The IT Crowd  Series 3", "3.x Out Takes.mkv"), true},
		{"season extra deleted scene", tvRoot, filepath.Join(tvRoot, "The IT Crowd", "The IT Crowd  Series 3", "3.x Deleted Scene.mkv"), true},
		{"season extra set tour", tvRoot, filepath.Join(tvRoot, "The IT Crowd", "The IT Crowd  Series 3", "3.x Set Tour.mkv"), true},
		{"season by episode code", tvRoot, filepath.Join(tvRoot, "The IT Crowd", "3x07 Calamity Jen.mkv"), false},
		{"leading episode pair", tvRoot, filepath.Join(tvRoot, "The IT Crowd", "The IT Crowd  Series 3", "3.01 From Hell.mkv"), false},
		{"plain outtakes title", root, filepath.Join(root, "Outtakes", "Outtakes.mkv"), false},
		{"exact library-tv outtakes name", tvRoot, filepath.Join(tvRoot, "The IT Crowd", "The IT Crowd  Series 3", "3 x Out Takes.mkv"), true},
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

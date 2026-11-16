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

	est, err := EstimateRoots(context.Background(), []string{root}, false)
	if err != nil {
		t.Fatalf("EstimateRoots: %v", err)
	}
	if est.EstimatedFiles != 1 {
		t.Fatalf("EstimatedFiles = %d, want 1 (the feature; the Trailers/ file below the root is an Extra)", est.EstimatedFiles)
	}
}

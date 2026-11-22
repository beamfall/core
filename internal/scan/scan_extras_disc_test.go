package scan

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestIsExtraPathDiscStreamBesideTitledFile (CORE-3060): a bare disc stream
// file is an Extra only in a folder below the Library root that also holds a
// titled video file.
func TestIsExtraPathDiscStreamBesideTitledFile(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Titled/Synthetic Feature (2024).mkv", []byte("feature"))
	writeScanFile(t, root, "Titled/00019.m2ts", []byte("stream"))
	writeScanFile(t, root, "Titled/00032.MTS", []byte("stream"))
	writeScanFile(t, root, "Titled/123456.m2ts", []byte("six digits"))
	writeScanFile(t, root, "Untitled/00001.m2ts", []byte("stream"))
	writeScanFile(t, root, "Untitled/00002.m2ts", []byte("stream"))
	writeScanFile(t, root, "TrailerOnly/00003.m2ts", []byte("stream"))
	writeScanFile(t, root, "TrailerOnly/Synthetic Feature-trailer.mkv", []byte("trailer"))
	writeScanFile(t, root, "ArtOnly/00004.m2ts", []byte("stream"))
	writeScanFile(t, root, "ArtOnly/poster.jpg", []byte("art"))
	writeScanFile(t, root, "00005.m2ts", []byte("stream"))
	writeScanFile(t, root, "Loose Feature.mkv", []byte("feature"))
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"disc stream beside a titled file", "Titled/00019.m2ts", true},
		{"mts extension, any case", "Titled/00032.MTS", true},
		{"titled file stays main", "Titled/Synthetic Feature (2024).mkv", false},
		{"six digits is not a disc stream", "Titled/123456.m2ts", false},
		{"folder of disc streams only", "Untitled/00001.m2ts", false},
		{"only a suffixed Extra beside it", "TrailerOnly/00003.m2ts", false},
		{"only artwork beside it", "ArtOnly/00004.m2ts", false},
		{"directly in the Library root", "00005.m2ts", false},
	}
	for _, tc := range cases {
		if got := isExtraPath(root, filepath.Join(root, filepath.FromSlash(tc.path))); got != tc.want {
			t.Errorf("%s: isExtraPath(%q) = %v, want %v", tc.name, tc.path, got, tc.want)
		}
	}
}

// TestRescanTurnsDiscStreamIntoExtraOfFolderMedia (CORE-3060): a disc stream
// file stored as main Media becomes an Extra of the folder's main Media on the
// next scan, keeping its Media ID, and leaves browse.
func TestRescanTurnsDiscStreamIntoExtraOfFolderMedia(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	writeScanFile(t, root, "Synthetic Feature (2024)/00019.m2ts", []byte("featurette content"))
	st := storetest.Open(t)
	ctx := context.Background()
	const libraryID = store.LibraryID("library-local")
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{libraryID}}
	if err := st.SetLibraryRoots(ctx, libraryID, []string{root}); err != nil {
		t.Fatalf("SetLibraryRoots: %v", err)
	}

	// Alone in its folder the stream file is main Media: the stored state a
	// scan before CORE-3060 left beside a titled file.
	first, err := Scan(ctx, st, nil, nil, nil, nil, nil, Request{LibraryID: libraryID, Root: root})
	if err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	if len(first.Files) != 1 {
		t.Fatalf("first scan files = %#v, want one", first.Files)
	}
	streamID := first.Files[0].MediaID
	items, err := st.SourceItems(ctx, filter, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSourceItemWithTitle(items, "00019") {
		t.Fatalf("setup: lone stream file not main Media: %#v", items)
	}

	writeScanFile(t, root, "Synthetic Feature (2024)/Synthetic Feature (2024).mkv", []byte("feature content"))
	second, err := Scan(ctx, st, nil, nil, nil, nil, nil, Request{LibraryID: libraryID, Root: root})
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	ids := map[string]store.MediaID{}
	for _, f := range second.Files {
		ids[filepath.Base(f.Path)] = f.MediaID
	}
	featureID := ids["Synthetic Feature (2024).mkv"]
	if featureID == "" || ids["00019.m2ts"] != streamID {
		t.Fatalf("rescan files = %#v, want the feature and the stream file under Media %q", ids, streamID)
	}
	items, err = st.SourceItems(ctx, filter, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if hasSourceItemWithTitle(items, "00019") || !hasSourceItemWithTitle(items, "Synthetic Feature (2024)") {
		t.Fatalf("browse after rescan = %#v, want the feature and not the stream file", items)
	}
	extras, err := st.MediaRelationsOf(ctx, featureID, store.MediaRelationExtraOf, filter)
	if err != nil {
		t.Fatalf("MediaRelationsOf(extra-of): %v", err)
	}
	if len(extras) != 1 || extras[0].ID != streamID {
		t.Fatalf("extras of feature = %#v, want only %q", extras, streamID)
	}
}

package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"modernc.org/sqlite"
)

func TestSelectedDuplicateClustersQueriesOnlySelectedMembership(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	// Give the same canonical survivor both hashes. The exact MergeApply set is
	// therefore the union of both displayed duplicate groups.
	survivor, _, err := st.ImportMediaFileParts(ctx, []ImportMediaFilePart{
		{SourceOrdinal: 1, File: ImportMediaFile{
			LibraryID: "library-local", Path: "/duplicates/selected-survivor-a.mp4",
			Title: "selected-survivor", Container: "mp4", Codec: "h264",
			ContentFingerprint: "sha256:multihash-a", OSHash: "ffff000000000031",
		}},
		{SourceOrdinal: 2, File: ImportMediaFile{
			LibraryID: "library-local", Path: "/duplicates/selected-survivor-b.mp4",
			Title: "selected-survivor", Container: "mp4", Codec: "h264",
			ContentFingerprint: "sha256:multihash-b", OSHash: "ffff000000000032",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	redundantA := importDuplicateSelectionMedia(t, st, "sha256:multihash-drop-a", "ffff000000000031")
	redundantB := importDuplicateSelectionMedia(t, st, "sha256:multihash-drop-b", "ffff000000000032")

	for i := 0; i < 40; i++ {
		hash := fmt.Sprintf("unselected-hash-%03d", i)
		importDuplicateSelectionMedia(t, st, fmt.Sprintf("unselected-%03d-a", i), hash)
		importDuplicateSelectionMedia(t, st, fmt.Sprintf("unselected-%03d-b", i), hash)
	}

	restrictedCountingDriverOnce.Do(func() {
		sql.Register(restrictedCountingDriverName, &restrictedCountingDriver{
			inner: &sqlite.Driver{}, count: &restrictedQueryCount,
		})
	})
	installCountingCatalogReader(t, st)

	restrictedQueryCount.Store(0)
	clusters, err := st.SelectedDuplicateClusters(ctx, "library-local", []MediaID{redundantA, redundantB}, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := restrictedQueryCount.Load(); got > 3 {
		t.Fatalf("selected duplicate snapshot executed %d queries, want at most 3", got)
	}
	if len(clusters) != 1 {
		t.Fatalf("clusters = %#v, want one selected survivor", clusters)
	}
	members := map[MediaID]bool{}
	for _, id := range clusters[0].Members {
		members[id] = true
	}
	for _, id := range []MediaID{survivor.ID, redundantA, redundantB} {
		if !members[id] {
			t.Fatalf("members = %#v, missing %s", clusters[0].Members, id)
		}
	}
}

func installCountingCatalogReader(t *testing.T, st *Store) {
	t.Helper()
	restrictedCountingDriverOnce.Do(func() {
		sql.Register(restrictedCountingDriverName, &restrictedCountingDriver{
			inner: &sqlite.Driver{}, count: &restrictedQueryCount,
		})
	})
	if err := st.catalogR.Close(); err != nil {
		t.Fatal(err)
	}
	countingReader, err := sql.Open(restrictedCountingDriverName, st.catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	st.catalogR = countingReader
}

func TestSelectedDuplicateClustersBoundsInput(t *testing.T) {
	st := openSeeded(t)
	ids := make([]MediaID, maxSelectedDuplicateIDs+1)
	for i := range ids {
		ids[i] = MediaID(fmt.Sprintf("media-%03d", i))
	}
	if _, err := st.SelectedDuplicateClusters(context.Background(), "library-local", ids, LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("oversized selected duplicate input accepted")
	}
}

func importDuplicateSelectionMedia(t *testing.T, st *Store, title, hash string) MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID: "library-local", Path: "/duplicates/" + title + ".mp4", Title: title,
		Container: "mp4", Codec: "h264", ContentFingerprint: title, OSHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	return media.ID
}

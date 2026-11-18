package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

// folderFixture files the open demo Library's Media into two roots:
// a/top, a/Nested/mid, a/Nested/Deeper/low and b/other, with every other Media
// outside both roots.
func folderFixture(t *testing.T) (st *Store, rootA, rootB string, ids []MediaID) {
	t.Helper()
	st = openDemo(t)
	ctx := context.Background()
	all := mustLibraryBrowse(t, st, LibraryBrowseQuery{Limit: 200})
	if len(all.Items) < 5 {
		t.Fatalf("demo library holds %d items, want at least 5", len(all.Items))
	}
	for _, item := range all.Items {
		ids = append(ids, item.ID)
	}
	sep := string(filepath.Separator)
	rootA = filepath.Join(sep, "srv", "lib-a")
	rootB = filepath.Join(sep, "srv", "lib-b")
	if err := st.SetLibraryRoots(ctx, "library-demo-open", []string{rootA, rootB}); err != nil {
		t.Fatal(err)
	}
	paths := map[MediaID]string{
		ids[0]: filepath.Join(rootA, "top.mkv"),
		ids[1]: filepath.Join(rootA, "Nested", "mid.mkv"),
		ids[2]: filepath.Join(rootA, "Nested", "Deeper", "low.mkv"),
		ids[3]: filepath.Join(rootB, "other.mkv"),
	}
	for i, id := range ids {
		path, ok := paths[id]
		if !ok {
			path = filepath.Join(sep, "elsewhere", fmt.Sprintf("item-%d.mkv", i))
		}
		// A Media with several files keeps them apart by a part suffix.
		if _, err := st.catalogW.ExecContext(ctx,
			`UPDATE media_file SET path = ? || CASE WHEN part_ordinal = 0 AND version_label = '' THEN '' ELSE '.' || rowid END WHERE media_id = ?`,
			path, id); err != nil {
			t.Fatal(err)
		}
	}
	return st, rootA, rootB, ids
}

func mustLibraryFolder(t *testing.T, st *Store, query LibraryFolderQuery) LibraryFolderResult {
	t.Helper()
	query.LibraryID = "library-demo-open"
	result, err := st.LibraryFolder(context.Background(), LibraryFilter{AllowedIDs: []LibraryID{"library-demo-open"}}, query)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func folderSummary(folders []LibraryFolder) []string {
	out := make([]string, len(folders))
	for i, f := range folders {
		out[i] = fmt.Sprintf("%s=%d", f.Name, f.MediaCount)
	}
	return out
}

func itemIDs(items []SourceItem) []MediaID {
	out := make([]MediaID, len(items))
	for i, item := range items {
		out[i] = item.ID
	}
	return out
}

func TestLibraryFolderWalksRootsSubfoldersAndMedia(t *testing.T) {
	st, rootA, rootB, ids := folderFixture(t)

	top := mustLibraryFolder(t, st, LibraryFolderQuery{})
	if top.Path != "" || top.RootCount != 2 || len(top.Items) != 0 || len(top.Crumbs) != 0 {
		t.Fatalf("root list = %+v, want two roots and no items", top)
	}
	if got := folderSummary(top.Folders); !slices.Equal(got, []string{"lib-a=3", "lib-b=1"}) {
		t.Fatalf("root folders = %v", got)
	}
	if top.Folders[0].Path != rootA || top.Folders[1].Path != rootB {
		t.Fatalf("root paths = %+v", top.Folders)
	}

	a := mustLibraryFolder(t, st, LibraryFolderQuery{Path: rootA})
	if got := folderSummary(a.Folders); !slices.Equal(got, []string{"Nested=2"}) {
		t.Fatalf("lib-a folders = %v", got)
	}
	if a.Total != 1 || !slices.Equal(itemIDs(a.Items), []MediaID{ids[0]}) || a.Items[0].Kind != "media" {
		t.Fatalf("lib-a items = %+v", a.Items)
	}
	if len(a.Crumbs) != 1 || a.Crumbs[0].Path != rootA || a.Crumbs[0].Name != "lib-a" || a.RootCount != 2 {
		t.Fatalf("lib-a crumbs = %+v", a.Crumbs)
	}

	nested := mustLibraryFolder(t, st, LibraryFolderQuery{Path: a.Folders[0].Path})
	if got := folderSummary(nested.Folders); !slices.Equal(got, []string{"Deeper=1"}) {
		t.Fatalf("Nested folders = %v", got)
	}
	if !slices.Equal(itemIDs(nested.Items), []MediaID{ids[1]}) {
		t.Fatalf("Nested items = %v", itemIDs(nested.Items))
	}
	if len(nested.Crumbs) != 2 || nested.Crumbs[1].Name != "Nested" || nested.Crumbs[1].Path != filepath.Join(rootA, "Nested") {
		t.Fatalf("Nested crumbs = %+v", nested.Crumbs)
	}
	deeper := mustLibraryFolder(t, st, LibraryFolderQuery{Path: nested.Folders[0].Path})
	if len(deeper.Folders) != 0 || !slices.Equal(itemIDs(deeper.Items), []MediaID{ids[2]}) || len(deeper.Crumbs) != 3 {
		t.Fatalf("Deeper = %+v", deeper)
	}

	// A folder inside a root that holds nothing visible is simply empty.
	if empty := mustLibraryFolder(t, st, LibraryFolderQuery{Path: filepath.Join(rootA, "absent")}); len(empty.Folders)+len(empty.Items) != 0 {
		t.Fatalf("absent folder = %+v", empty)
	}
}

func TestLibraryFolderStaysInsideTheLibraryRoots(t *testing.T) {
	st, rootA, _, _ := folderFixture(t)
	ctx := context.Background()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-demo-open"}}
	for _, path := range []string{
		filepath.Join(string(filepath.Separator), "elsewhere"),
		filepath.Join(rootA, "..", "..", "elsewhere"),
		"relative/folder",
	} {
		_, err := st.LibraryFolder(ctx, filter, LibraryFolderQuery{LibraryID: "library-demo-open", Path: path})
		var verr ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("folder %q: err = %v, want a validation error", path, err)
		}
	}
	// A Library the caller may not see is unknown.
	if _, err := st.LibraryFolder(ctx, LibraryFilter{AllowedIDs: []LibraryID{"library-other"}}, LibraryFolderQuery{LibraryID: "library-demo-open"}); err == nil {
		t.Fatal("hidden Library answered a folder read")
	}
}

func TestLibraryFolderHidesWhatTheBrowseHides(t *testing.T) {
	st, rootA, _, ids := folderFixture(t)
	if _, err := st.catalogW.ExecContext(context.Background(), `UPDATE media SET trashed_at_ms = 1 WHERE id = ?`, ids[1]); err != nil {
		t.Fatal(err)
	}
	a := mustLibraryFolder(t, st, LibraryFolderQuery{Path: rootA})
	if got := folderSummary(a.Folders); !slices.Equal(got, []string{"Nested=1"}) {
		t.Fatalf("lib-a folders after trashing one = %v", got)
	}
	nested := mustLibraryFolder(t, st, LibraryFolderQuery{Path: filepath.Join(rootA, "Nested")})
	if len(nested.Items) != 0 || nested.Total != 0 {
		t.Fatalf("trashed Media still listed: %v", itemIDs(nested.Items))
	}
}

func TestLibraryFolderOpensASingleRootAndPages(t *testing.T) {
	st, rootA, _, ids := folderFixture(t)
	ctx := context.Background()
	if err := st.SetLibraryRoots(ctx, "library-demo-open", []string{rootA}); err != nil {
		t.Fatal(err)
	}
	// Put a second Media beside the first so the root folder has two to page.
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media_file SET path = ? WHERE media_id = ?`, filepath.Join(rootA, "another.mkv"), ids[3]); err != nil {
		t.Fatal(err)
	}
	opened := mustLibraryFolder(t, st, LibraryFolderQuery{})
	if opened.Path != rootA || opened.RootCount != 1 || len(opened.Crumbs) != 1 {
		t.Fatalf("single root did not open: %+v", opened)
	}
	if !slices.Equal(itemIDs(opened.Items), []MediaID{ids[3], ids[0]}) {
		t.Fatalf("root items = %v, want file-name order", itemIDs(opened.Items))
	}
	page := mustLibraryFolder(t, st, LibraryFolderQuery{Limit: 1, Offset: 1})
	if page.Total != 2 || !slices.Equal(itemIDs(page.Items), []MediaID{ids[0]}) {
		t.Fatalf("second page = %v (total %d)", itemIDs(page.Items), page.Total)
	}
}

// TestLibraryFolderSortsByOrdinalThenFileName pins CORE-2386: Media carrying
// stored ordinals list by sequence then index, ahead of Media carrying none,
// which keep file-name order.
func TestLibraryFolderSortsByOrdinalThenFileName(t *testing.T) {
	st, rootA, _, ids := folderFixture(t)
	ctx := context.Background()
	if err := st.SetLibraryRoots(ctx, "library-demo-open", []string{rootA}); err != nil {
		t.Fatal(err)
	}
	// Four Media directly in the root. File names sort a, b, c, d; the
	// ordinals say d (2.1), b (2.10) and c (3.1), with a carrying none.
	names := []string{"d.mkv", "b.mkv", "c.mkv", "a.mkv"}
	ordinals := [][2]float64{{2, 1}, {2, 10}, {3, 1}, {0, 0}}
	for i, name := range names {
		if _, err := st.catalogW.ExecContext(ctx, `UPDATE media_file SET path = ? || CASE WHEN part_ordinal = 0 AND version_label = '' THEN '' ELSE '.' || rowid END WHERE media_id = ?`, filepath.Join(rootA, name), ids[i]); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMediaFields(ctx, ids[i], nil); err != nil {
			t.Fatal(err)
		}
		var fields []MediaField
		if ordinals[i][0] > 0 {
			fields = append(fields, MediaField{Name: "sequence", Type: EntityFieldNumber, Number: ordinals[i][0]})
		}
		if ordinals[i][1] > 0 {
			fields = append(fields, MediaField{Name: "index", Type: EntityFieldNumber, Number: ordinals[i][1]})
		}
		if err := st.SetMediaFields(ctx, ids[i], fields); err != nil {
			t.Fatal(err)
		}
	}
	opened := mustLibraryFolder(t, st, LibraryFolderQuery{})
	want := []MediaID{ids[0], ids[1], ids[2], ids[3]}
	if !slices.Equal(itemIDs(opened.Items), want) {
		t.Fatalf("folder items = %v, want ordinal order then file name %v", itemIDs(opened.Items), want)
	}
}

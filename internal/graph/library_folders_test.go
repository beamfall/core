package graph

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

const libraryFolderTestQuery = `query($libraryID: String!, $path: String, $limit: Int, $offset: Int) {
	libraryFolder(libraryID: $libraryID, path: $path, limit: $limit, offset: $offset) {
		path
		rootCount
		crumbs { path name }
		folders { path name mediaCount }
		total
		items { id kind title }
	}
}`

// newFolderLibraryHandler serves the demo seed with the open demo Library
// rooted at the folder its seed files sit under (<data>/seed-media/demo).
func newFolderLibraryHandler(t *testing.T) (handler *Handler, root string) {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(context.Background(), dataDir, store.Options{SeedProfile: store.SeedProfileDemo})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root = filepath.Join(dataDir, "seed-media")
	if err := st.SetLibraryRoots(context.Background(), "library-demo-open", []string{root}); err != nil {
		t.Fatal(err)
	}
	return newTestHandlerWithStore(st), root
}

func TestLibraryFolderOpensTheRootAndListsMediaByFolder(t *testing.T) {
	handler, root := newFolderLibraryHandler(t)

	var top LibraryFolderResponse
	execGraph(t, handler, authorizeDemoViewer, libraryFolderTestQuery,
		map[string]any{"libraryID": "library-demo-open"}, "libraryFolder", &top)
	if top.Path != root || top.RootCount != 1 || len(top.Crumbs) != 1 || top.Crumbs[0].Name != "seed-media" {
		t.Fatalf("single root did not open: %+v", top)
	}
	if len(top.Folders) != 1 || top.Folders[0].Name != "demo" || top.Folders[0].MediaCount == 0 || len(top.Items) != 0 {
		t.Fatalf("root folder = %+v, want the demo folder and no Media", top)
	}

	var demo LibraryFolderResponse
	execGraph(t, handler, authorizeDemoViewer, libraryFolderTestQuery,
		map[string]any{"libraryID": "library-demo-open", "path": top.Folders[0].Path, "limit": 2}, "libraryFolder", &demo)
	if demo.Total != top.Folders[0].MediaCount || len(demo.Items) != 2 {
		t.Fatalf("demo folder total=%d items=%d, want %d and a page of 2", demo.Total, len(demo.Items), top.Folders[0].MediaCount)
	}
	if demo.Items[0].Kind != "media" || len(demo.Crumbs) != 2 || demo.Crumbs[1].Path != top.Folders[0].Path {
		t.Fatalf("demo folder = %+v", demo)
	}
}

func TestLibraryFolderRefusesPathsOutsideTheLibrary(t *testing.T) {
	handler, root := newFolderLibraryHandler(t)
	for name, variables := range map[string]map[string]any{
		"outside":  {"libraryID": "library-demo-open", "path": filepath.Dir(root)},
		"escape":   {"libraryID": "library-demo-open", "path": filepath.Join(root, "..", "..")},
		"relative": {"libraryID": "library-demo-open", "path": "seed-media/demo"},
		"missing":  {"path": root},
	} {
		resp := postGraphJSON(t, handler, authorizeDemoViewer, libraryFolderTestQuery, variables)
		if len(resp.Errors) == 0 {
			t.Fatalf("%s: libraryFolder answered %s", name, resp.Data["libraryFolder"])
		}
		if strings.Contains(resp.Errors[0].Message, "unavailable") {
			t.Fatalf("%s: error %q is not a client error", name, resp.Errors[0].Message)
		}
	}
}

func TestLibraryFolderHidesLibrariesOutsideTheSessionScope(t *testing.T) {
	handler := newTestHandler(t)
	resp := postGraphJSON(t, handler, authorizeViewer, libraryFolderTestQuery, map[string]any{"libraryID": "library-archive"})
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "unknown library" {
		t.Fatalf("unrevealed library answered %s (errors %+v)", resp.Data["libraryFolder"], resp.Errors)
	}
	var revealed LibraryFolderResponse
	execGraph(t, handler, authorizeRevealed, libraryFolderTestQuery,
		map[string]any{"libraryID": "library-archive"}, "libraryFolder", &revealed)
}

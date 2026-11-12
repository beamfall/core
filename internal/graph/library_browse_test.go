package graph

import (
	"context"
	"net/http"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// newDemoLibraryHandler serves the demo seed, whose open library holds enough
// items to page and sort.
func newDemoLibraryHandler(t *testing.T) *Handler {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir(), store.Options{SeedProfile: store.SeedProfileDemo})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return newTestHandlerWithStore(st)
}

func authorizeDemoViewer(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-demo-viewer")
	req.Header.Set("X-Beamfall-Device", "device-demo-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "demo-viewer-secret")
}

const libraryItemsTestQuery = `query($libraryID: String!, $sort: String, $direction: String, $watch: String, $quality: String, $limit: Int, $offset: Int) {
	libraryItems(libraryID: $libraryID, sort: $sort, direction: $direction, watch: $watch, quality: $quality, limit: $limit, offset: $offset) {
		total
		items { id title }
		facets { entities decades qualities }
	}
}`

func TestLibraryItemsCountsEveryVisibleItemAndPages(t *testing.T) {
	handler := newDemoLibraryHandler(t)
	var all []SourceItem
	execGraph(t, handler, authorizeDemoViewer,
		`query($kind: String!, $libraryID: String, $limit: Int) { sourceItems(kind: $kind, libraryID: $libraryID, limit: $limit) { id } }`,
		map[string]any{"kind": "all-items", "libraryID": "library-demo-open", "limit": 200}, "sourceItems", &all)
	if len(all) < 2 {
		t.Fatalf("fixture library has %d items, need at least 2", len(all))
	}

	var page LibraryItemsResponse
	execGraph(t, handler, authorizeDemoViewer, libraryItemsTestQuery,
		map[string]any{"libraryID": "library-demo-open", "limit": 1, "offset": 1}, "libraryItems", &page)
	if page.Total != len(all) {
		t.Fatalf("total = %d, want the %d items the all-items Source shows", page.Total, len(all))
	}
	if len(page.Items) != 1 {
		t.Fatalf("page holds %d items, want 1", len(page.Items))
	}

	var asc, desc LibraryItemsResponse
	execGraph(t, handler, authorizeDemoViewer, libraryItemsTestQuery,
		map[string]any{"libraryID": "library-demo-open", "sort": "title"}, "libraryItems", &asc)
	execGraph(t, handler, authorizeDemoViewer, libraryItemsTestQuery,
		map[string]any{"libraryID": "library-demo-open", "sort": "title", "direction": "desc"}, "libraryItems", &desc)
	if asc.Items[0].ID == desc.Items[0].ID {
		t.Fatalf("ascending and descending both open on %s", asc.Items[0].ID)
	}
	if asc.Items[1].ID != page.Items[0].ID {
		t.Fatalf("offset 1 returned %s, want the second title %s", page.Items[0].ID, asc.Items[1].ID)
	}
}

func TestLibraryItemsHidesLibrariesOutsideTheSessionScope(t *testing.T) {
	handler := newTestHandler(t)
	var hidden LibraryItemsResponse
	execGraph(t, handler, authorizeViewer, libraryItemsTestQuery,
		map[string]any{"libraryID": "library-archive"}, "libraryItems", &hidden)
	if hidden.Total != 0 || len(hidden.Items) != 0 || len(hidden.Facets.Decades) != 0 {
		t.Fatalf("unrevealed library leaked total=%d items=%d facets=%+v", hidden.Total, len(hidden.Items), hidden.Facets)
	}
	var revealed LibraryItemsResponse
	execGraph(t, handler, authorizeRevealed, libraryItemsTestQuery,
		map[string]any{"libraryID": "library-archive"}, "libraryItems", &revealed)
	if revealed.Total == 0 {
		t.Fatal("revealed session sees no items in the restricted library")
	}
}

func TestLibraryItemsRejectsUnknownSortAndFilters(t *testing.T) {
	handler := newTestHandler(t)
	for name, variables := range map[string]map[string]any{
		"missing library": {"libraryID": " "},
		"unknown sort":    {"libraryID": "library-local", "sort": "rating"},
		"bad direction":   {"libraryID": "library-local", "direction": "sideways"},
		"unknown watch":   {"libraryID": "library-local", "watch": "abandoned"},
		"unknown quality": {"libraryID": "library-local", "quality": "8k"},
	} {
		resp := postGraphJSON(t, handler, authorizeViewer, libraryItemsTestQuery, variables)
		if len(resp.Errors) == 0 {
			t.Errorf("%s: accepted, want a client error", name)
		}
	}
}

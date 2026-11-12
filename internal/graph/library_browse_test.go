package graph

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
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
		facets { groupings { kind title values } decades qualities }
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
		"unknown sort":    {"libraryID": "library-local", "sort": "popularity"},
		"rating too high": {"libraryID": "library-local", "minRating": 11},
		"negative rating": {"libraryID": "library-local", "minRating": -1},
		"bad direction":   {"libraryID": "library-local", "direction": "sideways"},
		"unknown watch":   {"libraryID": "library-local", "watch": "abandoned"},
		"unknown quality": {"libraryID": "library-local", "quality": "8k"},
		"blank grouping":  {"libraryID": "library-local", "groupFilters": map[string]any{"sector": " "}},
	} {
		resp := postGraphJSON(t, handler, authorizeViewer, libraryItemsTestQuery, variables)
		if len(resp.Errors) == 0 {
			t.Errorf("%s: accepted, want a client error", name)
		}
	}
}

// The grouping filters come from the Library's plugin: every Entity Kind it
// declares with the grouping archetype, labelled by the plugin, and nothing
// core names itself.
func TestLibraryItemsGroupsByThePluginsDeclaredKinds(t *testing.T) {
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:          "starlog",
		Version:     "0.1.0",
		DisplayName: plugin.L("Starlog"),
		MediaLabel:  plugin.L("Entry"),
		EntityKinds: []plugin.EntityKind{
			{Name: "sector", Archetype: "category", DisplayName: plugin.LocaleString{"en": "Sector", "fr": "Secteur"}},
			{Name: "navigator", Archetype: "person", DisplayName: plugin.L("Navigator")},
			{Name: "era", Archetype: "category", DisplayName: plugin.L("Era")},
			{Name: "fleet", Archetype: "category", DisplayName: plugin.L("Fleet")},
		},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	ctx := context.Background()
	library, err := st.CreateLibrary(ctx, "Star Library", false, "starlog", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	media := map[string]store.MediaID{}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/media/" + name + ".mp4", Title: name,
			Container: "mp4", Codec: "h264", ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		media[name] = item.ID
	}
	relate := func(id store.EntityID, kind, title string, members ...string) {
		t.Helper()
		if err := st.UpsertEntity(ctx, store.Entity{ID: id, LibraryID: library.ID, Kind: kind, Title: title}); err != nil {
			t.Fatal(err)
		}
		for _, member := range members {
			if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: id, Name: "contains", ToMediaID: media[member]}); err != nil {
				t.Fatal(err)
			}
		}
	}
	relate("entity-inner", "sector", "Inner", "alpha", "beta")
	relate("entity-outer", "sector", "Outer", "gamma")
	relate("entity-early", "era", "Early", "beta", "gamma")
	relate("entity-nav", "navigator", "Nav One", "alpha")
	relate("entity-fleet", "fleet", "Unused")

	const query = `query($libraryID: String!, $groupFilters: Map) {
		libraryItems(libraryID: $libraryID, groupFilters: $groupFilters) {
			total items { id title } facets { groupings { kind title values } decades qualities }
		}
	}`
	var all LibraryItemsResponse
	execGraph(t, handler, authorizeViewer, query, map[string]any{"libraryID": string(library.ID)}, "libraryItems", &all)
	groupings := all.Facets.Groupings
	if all.Total != 3 || len(groupings) != 2 {
		t.Fatalf("total = %d groupings = %+v, want 3 items and the two grouping kinds that have values", all.Total, groupings)
	}
	if groupings[0].Kind != "sector" || groupings[0].Title.Resolve("fr") != "Secteur" || !slices.Equal(groupings[0].Values, []string{"Inner", "Outer"}) {
		t.Fatalf("first grouping = %+v, want sector labelled by the plugin with Inner and Outer", groupings[0])
	}
	if groupings[1].Kind != "era" || !slices.Equal(groupings[1].Values, []string{"Early"}) {
		t.Fatalf("second grouping = %+v, want era with Early", groupings[1])
	}

	var both LibraryItemsResponse
	execGraph(t, handler, authorizeViewer, query, map[string]any{
		"libraryID":    string(library.ID),
		"groupFilters": map[string]any{"sector": "Inner", "era": "Early"},
	}, "libraryItems", &both)
	if both.Total != 1 || both.Items[0].Title != "beta" {
		t.Fatalf("Inner and Early matched %d items (%+v), want only beta", both.Total, both.Items)
	}
	if len(both.Facets.Groupings) != 2 {
		t.Fatalf("filtered groupings = %+v, want the unfiltered choices kept", both.Facets.Groupings)
	}

	var plain LibraryItemsResponse
	execGraph(t, handler, authorizeViewer, query, map[string]any{"libraryID": "library-local"}, "libraryItems", &plain)
	if len(plain.Facets.Groupings) != 0 {
		t.Fatalf("plugin-less library groupings = %+v, want none", plain.Facets.Groupings)
	}
}

package graph

import (
	"context"
	"net/http"
	"slices"
	"strings"
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
			total items { id title } facets { groupings { kind title values counts } decades qualities }
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
	// Each value carries how many Media reach it (CORE-2505).
	if !slices.Equal(groupings[0].Counts, []int{2, 1}) || !slices.Equal(groupings[1].Counts, []int{2}) {
		t.Fatalf("grouping counts = %v and %v, want [2 1] and [2]", groupings[0].Counts, groupings[1].Counts)
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

// The full Library grid carries the same playback state every other card
// surface does, so a grid card shows progress and completion.
func TestLibraryItemsCarryTheProfilesPlaybackState(t *testing.T) {
	handler := newDemoLibraryHandler(t)
	var page LibraryItemsResponse
	execGraph(t, handler, authorizeDemoViewer,
		`query($libraryID: String!, $limit: Int) { libraryItems(libraryID: $libraryID, limit: $limit) { items { id engagement { positionSeconds durationSeconds completed } } } }`,
		map[string]any{"libraryID": "library-demo-open", "limit": 200}, "libraryItems", &page)
	states := map[string]*SourceItemEngagement{}
	for _, item := range page.Items {
		states[item.ID] = item.Engagement
	}
	partial := states["media-demo-sintel"]
	if partial == nil || partial.PositionSeconds == 0 || partial.DurationSeconds == 0 {
		t.Fatalf("in-progress grid item engagement = %+v, want a position and duration", partial)
	}
	done := states["media-demo-bunny"]
	if done == nil || !done.Completed {
		t.Fatalf("finished grid item engagement = %+v, want completed", done)
	}
}

// A title sort reports where each title initial starts across the whole
// filtered set, so a client can jump to a letter without loading every item.
func TestLibraryItemsReportWhereEachTitleInitialStarts(t *testing.T) {
	handler := newDemoLibraryHandler(t)
	const query = `query($libraryID: String!, $sort: String, $limit: Int, $offset: Int) {
		libraryItems(libraryID: $libraryID, sort: $sort, limit: $limit, offset: $offset) {
			total items { id title } letters { letter offset count }
		}
	}`
	var first LibraryItemsResponse
	execGraph(t, handler, authorizeDemoViewer, query,
		map[string]any{"libraryID": "library-demo-open", "sort": "title", "limit": 1}, "libraryItems", &first)
	if len(first.Letters) < 2 {
		t.Fatalf("letters = %+v, want the initials of the whole set on a one-item page", first.Letters)
	}
	counted := 0
	for _, letter := range first.Letters {
		counted += letter.Count
	}
	if counted != first.Total {
		t.Fatalf("letter counts sum to %d, want the %d items", counted, first.Total)
	}
	last := first.Letters[len(first.Letters)-1]
	if last.Offset <= 0 || last.Offset >= first.Total {
		t.Fatalf("last letter %+v lies outside the %d items", last, first.Total)
	}
	var jumped LibraryItemsResponse
	execGraph(t, handler, authorizeDemoViewer, query,
		map[string]any{"libraryID": "library-demo-open", "sort": "title", "limit": 1, "offset": last.Offset}, "libraryItems", &jumped)
	if len(jumped.Items) != 1 || !strings.EqualFold(jumped.Items[0].Title[:len(last.Letter)], last.Letter) {
		t.Fatalf("offset %d opened on %+v, want a title starting with %q", last.Offset, jumped.Items, last.Letter)
	}

	var byYear LibraryItemsResponse
	execGraph(t, handler, authorizeDemoViewer, query,
		map[string]any{"libraryID": "library-demo-open", "sort": "year"}, "libraryItems", &byYear)
	if byYear.Letters == nil || len(byYear.Letters) != 0 {
		t.Fatalf("year sort letters = %+v, want an empty list", byYear.Letters)
	}
}

// CORE-2552: the facet count follows the same outermost containers as cards.
func TestLibraryCategoryCountsBrowseContainers(t *testing.T) {
	h, library, media := newContainerLibrary(t)
	ctx := context.Background()
	// Same-titled containers remain distinct browse units.
	if err := h.store.UpsertEntity(ctx, store.Entity{ID: "entity-vol-b", LibraryID: library, Kind: "volume", Title: "Harbour"}); err != nil {
		t.Fatal(err)
	}
	for title, members := range map[string][]string{
		"Contained": {"a1", "a2", "a3", "b1"},
		"Loose":     {"loose", "mentioned"},
		"Mixed":     {"a1", "a2", "loose"},
	} {
		id := store.EntityID("topic-" + title)
		if err := h.store.UpsertEntity(ctx, store.Entity{ID: id, LibraryID: library, Kind: "topic", Title: title}); err != nil {
			t.Fatal(err)
		}
		for _, member := range members {
			if err := h.store.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: id, Name: "includes", ToMediaID: media[member]}); err != nil {
				t.Fatal(err)
			}
		}
	}
	var response struct {
		Facets struct {
			Groupings []struct {
				Values       []string
				Counts       []int
				BrowseCounts []int
				BrowseLabels []plugin.LocaleString
			}
		}
	}
	execGraph(t, h, authorizeViewer, `query($libraryID: String!, $categoryCounts: Boolean) { libraryItems(libraryID: $libraryID, limit: 1, categoryCounts: $categoryCounts) { facets { groupings { values counts browseCounts browseLabels } } } }`, map[string]any{"libraryID": string(library), "categoryCounts": true}, "libraryItems", &response)
	if len(response.Facets.Groupings) != 1 {
		t.Fatalf("groupings = %+v", response)
	}
	g := response.Facets.Groupings[0]
	if !slices.Equal(g.Values, []string{"Contained", "Loose", "Mixed"}) || !slices.Equal(g.Counts, []int{4, 2, 3}) || !slices.Equal(g.BrowseCounts, []int{2, 2, 3}) {
		t.Fatalf("counts = %+v, want raw [4 2 3] and browse [2 2 3]", g)
	}
	if len(g.BrowseLabels) != 3 || g.BrowseLabels[0].Resolve("en") != "Volumes" || len(g.BrowseLabels[1]) != 0 || len(g.BrowseLabels[2]) != 0 {
		t.Fatalf("browse labels = %+v", g.BrowseLabels)
	}
	// Hiding the only member of the second container must remove its count.
	if err := h.store.SetMute(ctx, "profile-default", store.MuteKindEntity, "entity-part-b1", false, true); err != nil {
		t.Fatal(err)
	}
	execGraph(t, h, authorizeViewer, `query($libraryID: String!, $categoryCounts: Boolean) { libraryItems(libraryID: $libraryID, limit: 1, categoryCounts: $categoryCounts) { facets { groupings { values counts browseCounts browseLabels } } } }`, map[string]any{"libraryID": string(library), "categoryCounts": true}, "libraryItems", &response)
	if response.Facets.Groupings[0].BrowseCounts[0] != 1 {
		t.Fatalf("muted member contributed to count: %+v", response)
	}
}

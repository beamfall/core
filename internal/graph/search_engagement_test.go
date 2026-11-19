package graph

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestSearchContainerEngagementCountsOnlyItsMedia(t *testing.T) {
	t.Run("shared kinds across plugins", func(t *testing.T) {
		testContainerEngagement(t, true, false)
	})
	t.Run("direct child in another visible library", func(t *testing.T) {
		testContainerEngagement(t, false, true)
	})
}

func testContainerEngagement(t *testing.T, sharedKinds, crossLibrary bool) {
	t.Helper()
	ctx := context.Background()
	st := storetest.Open(t)
	bundle := plugin.Bundle{
		ID: "boxes", Version: "0.1.0", DisplayName: plugin.L("Boxes"), MediaLabel: plugin.L("Piece"),
		EntityKinds: []plugin.EntityKind{
			{Name: "box", Archetype: "container", OrderedChildren: true, FullTextSearchable: true, Relations: []plugin.RelationSpec{
				{Name: "credits", ToKind: "maker"}, {Name: "holds", ToKind: plugin.MediaKindName},
			}},
			{Name: "tray", Archetype: "container", OrderedChildren: true, Relations: []plugin.RelationSpec{
				{Name: "tray-of", ToKind: "box"}, {Name: "holds-piece", ToKind: plugin.MediaKindName, Cardinality: "one"},
			}},
			{Name: "maker", Archetype: "person", Relations: []plugin.RelationSpec{{Name: "made", ToKind: plugin.MediaKindName}}},
			{Name: "category", Archetype: "category", FullTextSearchable: true, Relations: []plugin.RelationSpec{{Name: "features", ToKind: "maker"}}},
		},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
			{ID: "boxes", Kind: "grid", Title: plugin.L("Boxes"), ArtworkSlot: "poster", Source: plugin.SourceSpec{Kind: "entity-items", EntityKind: "box"}},
		}}},
	}
	bundles := []plugin.Bundle{bundle}
	if sharedKinds {
		bundles = append(bundles, plugin.Bundle{
			ID: "crates", Version: "0.1.0", DisplayName: plugin.L("Crates"), MediaLabel: plugin.L("Piece"),
			Layouts: bundle.Layouts,
			EntityKinds: []plugin.EntityKind{
				{Name: "box", Archetype: "container", OrderedChildren: true, Relations: []plugin.RelationSpec{
					{Name: "contains", ToKind: plugin.MediaKindName},
				}},
				{Name: "tray", Archetype: "container", OrderedChildren: true, Relations: []plugin.RelationSpec{
					{Name: "part-of", ToKind: "box"}, {Name: "carries", ToKind: plugin.MediaKindName, Cardinality: "one"},
				}},
			},
		})
	}
	registry, err := newTestPluginRegistry(bundles)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry), auth.NewService(st), st, registry)
	library, err := st.CreateLibrary(ctx, "Boxes", false, string(bundle.ID), "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterEntityKindPolicies(ctx, []store.EntityKindPolicy{
		{Kind: "box", FullTextSearchable: true}, {Kind: "category", FullTextSearchable: true},
	}); err != nil {
		t.Fatal(err)
	}
	for _, entity := range []store.Entity{
		{ID: "box-a", Kind: "box", Title: "Atlas A"},
		{ID: "box-b", Kind: "box", Title: "Atlas B"},
		{ID: "box-c", Kind: "box", Title: "Atlas C"},
		{ID: "tray-a", Kind: "tray", Title: "Left"},
		{ID: "tray-b", Kind: "tray", Title: "Right"},
		{ID: "maker", Kind: "maker", Title: "Maker"},
		{ID: "category", Kind: "category", Title: "Atlas Category"},
	} {
		entity.LibraryID = library.ID
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	order := 1
	link := func(from store.EntityID, name string, toEntity store.EntityID, toMedia store.MediaID) {
		t.Helper()
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
			FromEntityID: from, Name: name, ToEntityID: toEntity, ToMediaID: toMedia, SortOrder: &order,
		}); err != nil {
			t.Fatal(err)
		}
	}
	link("tray-a", "tray-of", "box-a", "")
	link("tray-b", "tray-of", "box-a", "")
	link("box-a", "credits", "maker", "")
	link("box-b", "credits", "maker", "")
	link("category", "features", "maker", "")

	filter := store.LibraryFilter{AllLibraries: true}
	other, err := st.CreateProfile(ctx, "Another viewer", store.ProfileAdult, filter)
	if err != nil {
		t.Fatal(err)
	}
	for _, piece := range []struct {
		name, relation string
		holder         store.EntityID
		playedBy       store.ProfileID
	}{
		{"Amber", "holds-piece", "tray-a", "profile-default"},
		{"Blue", "holds-piece", "tray-a", other.ID},
		{"Copper", "holds-piece", "tray-b", ""},
		{"Denim", "holds", "box-b", ""},
		{"Emerald", "holds", "box-c", "profile-default"},
		{"Flint", "made", "maker", ""},
		{"Gold", "made", "maker", ""},
		{"Hazel", "holds-piece", "tray-b", ""},
	} {
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/boxes/" + piece.name + ".mkv", Title: piece.name,
			Container: "mkv", Codec: "h264", ContentFingerprint: "fp-" + piece.name,
		})
		if err != nil {
			t.Fatal(err)
		}
		link(piece.holder, piece.relation, "", item.ID)
		if piece.name == "Blue" {
			link("box-a", "holds", "", item.ID) // Also reached through a tray.
		}
		if piece.name == "Hazel" {
			if err := st.TrashMedia(ctx, item.ID, filter); err != nil {
				t.Fatal(err)
			}
		}
		if piece.playedBy != "" {
			if _, err := st.SetItemWatched(ctx, piece.playedBy, store.ItemTarget{MediaID: item.ID}, true, filter); err != nil {
				t.Fatal(err)
			}
		}
	}

	want := map[string]SourceItemEngagement{
		"box-a":    {TotalCount: 3, RemainingCount: 2},
		"box-b":    {TotalCount: 1, RemainingCount: 1},
		"box-c":    {TotalCount: 1, Completed: true},
		"category": {TotalCount: 2, RemainingCount: 2},
	}
	check := func(surface string, item SourceItem) {
		t.Helper()
		if expected, ok := want[item.ID]; !ok || item.Kind != "entity" || item.Engagement == nil || *item.Engagement != expected {
			t.Errorf("%s entity %q engagement = %+v, want %+v", surface, item.ID, item.Engagement, expected)
		}
	}
	seen := map[string]bool{}
	for offset := 0; offset < len(want); offset += 2 {
		var search SearchResultResponse
		execGraph(t, handler, authorizeViewer,
			`query($offset: Int!) { searchResult(query: "Atlas", mode: keyword, limit: 2, offset: $offset) { items { id kind engagement { totalCount remainingCount completed } } } }`,
			map[string]any{"offset": offset}, "searchResult", &search)
		for _, item := range search.Items {
			check("search", item.SourceItem)
			seen[item.ID] = true
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("entity %q missing from search results", id)
		}
	}

	if crossLibrary {
		otherLibrary, err := st.CreateLibrary(ctx, "Other boxes", false, string(bundle.ID), "profile-default")
		if err != nil {
			t.Fatal(err)
		}
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: otherLibrary.ID, Path: "/other-boxes/indigo.mkv", Title: "Indigo",
			Container: "mkv", Codec: "h264", ContentFingerprint: "fp-Indigo",
		})
		if err != nil {
			t.Fatal(err)
		}
		link("box-b", "holds", "", item.ID)
	}

	var cards []SourceItem
	execGraph(t, handler, authorizeViewer,
		`query($libraryID: String!) { sourceItems(kind: "entity-items", entityKind: "box", libraryID: $libraryID) { id kind engagement { totalCount remainingCount completed } } }`,
		map[string]any{"libraryID": string(library.ID)}, "sourceItems", &cards)
	if len(cards) != 3 {
		t.Fatalf("sourceItems returned %d cards, want 3", len(cards))
	}
	for _, item := range cards {
		check("source", item)
	}
}

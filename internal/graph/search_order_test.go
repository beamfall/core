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

// TestSearchKeepsOrderedItemsTogetherInOrder pins CORE-2471: search rows whose
// ordered containers sit under one container root are listed together at the
// first one's position, by sequence then index, while other rows keep rank.
func TestSearchKeepsOrderedItemsTogetherInOrder(t *testing.T) {
	bundle := plugin.Bundle{
		ID:          "boxkit",
		Version:     "0.1.0",
		DisplayName: plugin.L("Boxkit"),
		MediaLabel:  plugin.L("Piece"),
		EntityKinds: []plugin.EntityKind{
			{Name: "box", Archetype: "container", DisplayName: plugin.L("Box"),
				Relations: []plugin.RelationSpec{{Name: "holds", ToKind: plugin.MediaKindName}}},
			{Name: "lid", Archetype: "container", DisplayName: plugin.L("Lid"), OrderedChildren: true,
				Relations: []plugin.RelationSpec{{Name: "lid-of", ToKind: "box"}, {Name: "lid-holds", ToKind: plugin.MediaKindName}}},
		},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
			{ID: "all", Kind: "grid", Title: plugin.L("All"), ArtworkSlot: "poster", Source: plugin.SourceSpec{Kind: "all-items"}},
		}}},
	}
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{bundle})
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
	library, err := st.CreateLibrary(ctx, "Boxes", false, "boxkit", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(ctx, store.Entity{ID: "box-1", LibraryID: library.ID, Kind: "box", Title: "Saga"}); err != nil {
		t.Fatal(err)
	}
	for _, lid := range []store.EntityID{"lid-1", "lid-2"} {
		if err := st.UpsertEntity(ctx, store.Entity{ID: lid, LibraryID: library.ID, Kind: "lid", Title: string(lid)}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: lid, Name: "lid-of", ToEntityID: "box-1"}); err != nil {
			t.Fatal(err)
		}
	}
	// Imported in an order that scatters the box's pieces through the ranking.
	pieces := []struct {
		name     string
		lid      store.EntityID
		sequence float64
		index    float64
	}{
		{"late", "lid-2", 2, 1},
		{"loose", "", 0, 0},
		{"second", "lid-1", 1, 2},
		{"first", "lid-1", 1, 1},
	}
	ids := map[string]string{}
	for i, piece := range pieces {
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/boxes/" + piece.name + ".mkv", Title: "Saga " + piece.name,
			Container: "mkv", Codec: "h264", ContentFingerprint: "fp-" + piece.name,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[piece.name] = string(item.ID)
		if piece.lid == "" {
			continue
		}
		if err := st.SetMediaFields(ctx, item.ID, []store.MediaField{
			{Name: "sequence", Type: store.EntityFieldNumber, Number: piece.sequence},
			{Name: "index", Type: store.EntityFieldNumber, Number: piece.index},
		}); err != nil {
			t.Fatal(err)
		}
		order := i
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: piece.lid, Name: "lid-holds", ToMediaID: item.ID, SortOrder: &order}); err != nil {
			t.Fatal(err)
		}
	}

	var search SearchResultResponse
	execGraph(t, handler, authorizeViewer,
		`query($query: String!) { searchResult(query: $query, mode: keyword) { items { id kind sequence index } } }`,
		map[string]any{"query": "Saga"}, "searchResult", &search)
	var order []string
	for _, item := range search.Items {
		for name, id := range ids {
			if item.ID == id {
				order = append(order, name)
			}
		}
	}
	at := map[string]int{}
	for i, name := range order {
		at[name] = i
	}
	if len(order) != 4 {
		t.Fatalf("search order = %v, want all four pieces", order)
	}
	if at["second"] != at["first"]+1 || at["late"] != at["first"]+2 {
		t.Fatalf("search order = %v, want first, second and late together in order", order)
	}
}

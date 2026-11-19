package graph

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// newContainerLibrary builds a Library whose plugin declares a two-level
// container chain: a part holds Media and nests into a volume. Two volumes
// hold parts; one Media item has no container, and one is pointed at only
// through a non-holding relation.
func newContainerLibrary(t *testing.T) (*Handler, store.LibraryID, map[string]store.MediaID) {
	t.Helper()
	bundle := plugin.Bundle{
		ID:          "volumekit",
		Version:     "0.1.0",
		DisplayName: plugin.L("Volumekit"),
		MediaLabel:  plugin.L("Piece"),
		EntityKinds: []plugin.EntityKind{
			{Name: "part", Archetype: "container", Relations: []plugin.RelationSpec{
				{Name: "holds", ToKind: plugin.MediaKindName, Cardinality: "one"},
				{Name: "mentions", ToKind: plugin.MediaKindName},
				{Name: "part-of", ToKind: "volume"},
			}},
			{Name: "volume", Archetype: "container"},
			{Name: "topic", Archetype: "category", DisplayName: plugin.L("Topic")},
		},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
			{ID: "volumes", Kind: "shelf", Title: plugin.L("Volumes"), Source: plugin.SourceSpec{Kind: "entity-items", EntityKind: "volume"}},
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
	library, err := st.CreateLibrary(ctx, "Volumes", false, "volumekit", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterEntityKindPolicies(ctx, []store.EntityKindPolicy{{Kind: "volume", FullTextSearchable: true}}); err != nil {
		t.Fatal(err)
	}
	// A searchable field carries the volume's name on its pieces, so a
	// volume search also finds pieces whose own titles do not match.
	if err := st.ReplaceMediaFieldSearchPolicies(ctx, []store.MediaFieldSearchPolicy{
		{PluginID: "volumekit", Field: "volume-name", Type: store.EntityFieldText, Searchable: true},
	}); err != nil {
		t.Fatal(err)
	}
	type piece struct {
		title           string
		sequence, index float64
	}
	pieces := map[string]piece{
		"a2": {"Departure", 1, 2}, "a1": {"Arrival", 1, 1}, "a3": {"Return", 2, 1},
		"b1": {"Harbour Lights", 1, 1}, "loose": {"Harbour Notes", 0, 0}, "mentioned": {"Aside", 0, 0},
	}
	media := map[string]store.MediaID{}
	for name, p := range pieces {
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/volumes/" + name + ".mkv", Title: p.title,
			Container: "mkv", Codec: "h264", ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		if p.sequence > 0 {
			fields := []store.MediaField{
				{Name: "sequence", Type: store.EntityFieldNumber, Number: p.sequence},
				{Name: "index", Type: store.EntityFieldNumber, Number: p.index},
			}
			if name[0] == 'a' {
				fields = append(fields, store.MediaField{Name: "volume-name", Type: store.EntityFieldText, Text: "Harbour"})
			}
			if err := st.SetMediaFields(ctx, item.ID, fields); err != nil {
				t.Fatal(err)
			}
		}
		media[name] = item.ID
	}
	for id, entity := range map[store.EntityID][2]string{
		"entity-vol-a": {"volume", "Harbour"}, "entity-vol-b": {"volume", "Anchorage"},
		"entity-part-a1": {"part", "Part One"}, "entity-part-a2": {"part", "Part Two"},
		"entity-part-b1": {"part", "Part One"},
	} {
		if err := st.UpsertEntity(ctx, store.Entity{ID: id, LibraryID: library.ID, Kind: entity[0], Title: entity[1]}); err != nil {
			t.Fatal(err)
		}
	}
	for _, relation := range []store.EntityRelation{
		{FromEntityID: "entity-part-a1", Name: "holds", ToMediaID: media["a1"]},
		{FromEntityID: "entity-part-a1", Name: "holds", ToMediaID: media["a2"]},
		{FromEntityID: "entity-part-a2", Name: "holds", ToMediaID: media["a3"]},
		{FromEntityID: "entity-part-b1", Name: "holds", ToMediaID: media["b1"]},
		{FromEntityID: "entity-part-a1", Name: "mentions", ToMediaID: media["mentioned"]},
		{FromEntityID: "entity-part-a1", Name: "part-of", ToEntityID: "entity-vol-a"},
		{FromEntityID: "entity-part-a2", Name: "part-of", ToEntityID: "entity-vol-a"},
		{FromEntityID: "entity-part-b1", Name: "part-of", ToEntityID: "entity-vol-b"},
	} {
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatal(err)
		}
	}
	return handler, library.ID, media
}

// TestMediaCardsNameTheirOutermostContainer pins CORE-2524: a Media card names
// the outermost container reached through the plugin's declared container
// relations; a non-holding relation and a container-less item name none.
func TestMediaCardsNameTheirOutermostContainer(t *testing.T) {
	handler, libraryID, media := newContainerLibrary(t)
	var page LibraryItemsResponse
	execGraph(t, handler, authorizeViewer,
		`query($libraryID: String!) { libraryItems(libraryID: $libraryID, limit: 50) { items { id container { id title } } } }`,
		map[string]any{"libraryID": string(libraryID)}, "libraryItems", &page)
	containers := map[string]*SourceItemContainer{}
	for _, item := range page.Items {
		containers[item.ID] = item.Container
	}
	for name, want := range map[string]string{"a1": "entity-vol-a", "a3": "entity-vol-a", "b1": "entity-vol-b"} {
		got := containers[string(media[name])]
		if got == nil || got.ID != want {
			t.Fatalf("%s container = %+v, want %s", name, got, want)
		}
	}
	if got := containers[string(media["a1"])]; got.Title != "Harbour" {
		t.Fatalf("a1 container title = %q, want Harbour", got.Title)
	}
	for _, name := range []string{"loose", "mentioned"} {
		if got := containers[string(media[name])]; got != nil {
			t.Fatalf("%s container = %+v, want none", name, got)
		}
	}
}

// TestKeywordSearchGroupsItemsUnderTheirContainer pins CORE-2525: searching
// a container's title returns the container first, then the items found
// through it in ordinal order; an item matching on its own title ranks by it.
func TestKeywordSearchGroupsItemsUnderTheirContainer(t *testing.T) {
	handler, _, media := newContainerLibrary(t)
	var search SearchResultResponse
	execGraph(t, handler, authorizeViewer,
		`query($query: String!) { searchResult(query: $query, mode: keyword) { items { id kind container { id title } } } }`,
		map[string]any{"query": "Harbour"}, "searchResult", &search)
	var got []string
	for _, item := range search.Items {
		got = append(got, item.ID)
	}
	// Every hit ties on relevance, so hits sort by their group: Harbour
	// Lights under its volume Anchorage, then the volume Harbour followed by
	// the pieces found through it in ordinal order, then the container-less
	// Harbour Notes.
	want := []string{string(media["b1"]), "entity-vol-a", string(media["a1"]), string(media["a2"]), string(media["a3"]), string(media["loose"])}
	if !slices.Equal(got, want) {
		t.Fatalf("search order = %v, want %v", got, want)
	}
	if c := search.Items[2].Container; c == nil || c.ID != "entity-vol-a" || c.Title != "Harbour" {
		t.Fatalf("a1 search container = %+v, want entity-vol-a Harbour", c)
	}
}

// TestKeywordSearchKeepsAContainersFirstItemsPastTheCut pins CORE-2525:
// container ordering runs over every scored hit before the 50-hit cut, so a
// container with more matching items than the cut still lists its first
// items, whatever their titles.
func TestKeywordSearchKeepsAContainersFirstItemsPastTheCut(t *testing.T) {
	handler, libraryID, _ := newContainerLibrary(t)
	ctx := context.Background()
	var first store.MediaID
	for i := 1; i <= 260; i++ {
		title := fmt.Sprintf("Alpha %03d", i)
		if i == 1 {
			title = "Zulu"
		}
		item, _, err := handler.store.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: libraryID, Path: fmt.Sprintf("/volumes/extra-%03d.mkv", i), Title: title,
			Container: "mkv", Codec: "h264", ContentFingerprint: fmt.Sprintf("fp-extra-%03d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := handler.store.SetMediaFields(ctx, item.ID, []store.MediaField{
			{Name: "sequence", Type: store.EntityFieldNumber, Number: 3},
			{Name: "index", Type: store.EntityFieldNumber, Number: float64(i)},
			{Name: "volume-name", Type: store.EntityFieldText, Text: "Harbour"},
		}); err != nil {
			t.Fatal(err)
		}
		if err := handler.store.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: "entity-part-a2", Name: "holds", ToMediaID: item.ID}); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			first = item.ID
		}
	}
	var search SearchResultResponse
	execGraph(t, handler, authorizeViewer,
		`query($query: String!) { searchResult(query: $query, mode: keyword) { items { id } } }`,
		map[string]any{"query": "Harbour"}, "searchResult", &search)
	if len(search.Items) != 50 {
		t.Fatalf("search returned %d hits, want the 50-hit cut", len(search.Items))
	}
	if !slices.ContainsFunc(search.Items, func(item SearchResultItem) bool { return item.ID == string(first) }) {
		t.Fatalf("search dropped the container's first item %s", first)
	}
}

// TestKeywordSearchKeepsAContainerGroupWholeAtItsBestRank pins CORE-2525 with
// CORE-2471's ordered regrouping: an item whose own title outranks its
// container's lifts the whole group to that rank, so the container still leads
// its items in ordinal order rather than trailing the item that matched best.
func TestKeywordSearchKeepsAContainerGroupWholeAtItsBestRank(t *testing.T) {
	handler, libraryID, media := newContainerLibrary(t)
	ctx := context.Background()
	if err := handler.store.UpsertEntity(ctx, store.Entity{ID: "entity-vol-a", LibraryID: libraryID, Kind: "volume", Title: "Harbours"}); err != nil {
		t.Fatal(err)
	}
	exact, _, err := handler.store.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: libraryID, Path: "/volumes/a4.mkv", Title: "Harbour",
		Container: "mkv", Codec: "h264", ContentFingerprint: "fp-a4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.store.SetMediaFields(ctx, exact.ID, []store.MediaField{
		{Name: "sequence", Type: store.EntityFieldNumber, Number: 2},
		{Name: "index", Type: store.EntityFieldNumber, Number: 2},
	}); err != nil {
		t.Fatal(err)
	}
	// Ordered holds give CORE-2471's regrouping a root to group under.
	for i, held := range []struct {
		part  store.EntityID
		media store.MediaID
	}{{"entity-part-a1", media["a1"]}, {"entity-part-a1", media["a2"]}, {"entity-part-a2", media["a3"]}, {"entity-part-a2", exact.ID}} {
		order := i
		if err := handler.store.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: held.part, Name: "holds", ToMediaID: held.media, SortOrder: &order}); err != nil {
			t.Fatal(err)
		}
	}
	var search SearchResultResponse
	execGraph(t, handler, authorizeViewer,
		`query($query: String!) { searchResult(query: $query, mode: keyword) { items { id } } }`,
		map[string]any{"query": "Harbour"}, "searchResult", &search)
	var got []string
	for _, item := range search.Items {
		got = append(got, item.ID)
	}
	want := []string{
		string(media["b1"]), string(media["loose"]),
		"entity-vol-a", string(media["a1"]), string(media["a2"]), string(media["a3"]), string(exact.ID),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("search order = %v, want %v", got, want)
	}
}

// TestKeywordSearchKeepsAnExactTitleMatchPastTheCut pins CORE-2547: a group
// ranks as its best row, but the 50-hit cut still keeps the row whose own
// title matched, even when it sits past the cut in ordinal order.
func TestKeywordSearchKeepsAnExactTitleMatchPastTheCut(t *testing.T) {
	handler, libraryID, _ := newContainerLibrary(t)
	ctx := context.Background()
	if err := handler.store.UpsertEntity(ctx, store.Entity{ID: "entity-vol-a", LibraryID: libraryID, Kind: "volume", Title: "Harbours"}); err != nil {
		t.Fatal(err)
	}
	var exact store.MediaID
	for i := 1; i <= 60; i++ {
		title := fmt.Sprintf("Leg %03d", i)
		if i == 60 {
			title = "Harbour"
		}
		item, _, err := handler.store.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: libraryID, Path: fmt.Sprintf("/volumes/leg-%03d.mkv", i), Title: title,
			Container: "mkv", Codec: "h264", ContentFingerprint: fmt.Sprintf("fp-leg-%03d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := handler.store.SetMediaFields(ctx, item.ID, []store.MediaField{
			{Name: "sequence", Type: store.EntityFieldNumber, Number: 3},
			{Name: "index", Type: store.EntityFieldNumber, Number: float64(i)},
			{Name: "volume-name", Type: store.EntityFieldText, Text: "Harbour"},
		}); err != nil {
			t.Fatal(err)
		}
		if err := handler.store.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: "entity-part-a2", Name: "holds", ToMediaID: item.ID}); err != nil {
			t.Fatal(err)
		}
		if i == 60 {
			exact = item.ID
		}
	}
	var search SearchResultResponse
	execGraph(t, handler, authorizeViewer,
		`query($query: String!) { searchResult(query: $query, mode: keyword) { items { id } } }`,
		map[string]any{"query": "Harbour"}, "searchResult", &search)
	if len(search.Items) != 50 {
		t.Fatalf("search returned %d hits, want the 50-hit cut", len(search.Items))
	}
	at := slices.IndexFunc(search.Items, func(item SearchResultItem) bool { return item.ID == string(exact) })
	if at < 0 {
		t.Fatalf("search dropped the exact title match %s", exact)
	}
	if container := slices.IndexFunc(search.Items, func(item SearchResultItem) bool { return item.ID == "entity-vol-a" }); container < 0 || container > at {
		t.Fatalf("container entity-vol-a at %d, want it kept ahead of its item at %d", container, at)
	}
}

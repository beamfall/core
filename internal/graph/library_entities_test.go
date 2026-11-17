package graph

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const libraryEntitiesTestQuery = `query($libraryID: String!, $kind: String, $limit: Int, $offset: Int) {
	libraryEntities(libraryID: $libraryID, kind: $kind, limit: $limit, offset: $offset) {
		kinds { kind label kindDisplayName archetype total }
		allLabel kind total
		items { id kind title mediaClass childCount mediaCount parents { id title } }
	}
}`

// shelfBundle declares a person kind, a container kind that points at it, a
// child container kind that is not browsable on its own, and a browsable
// grouping kind the Library holds none of. The browse Layout lists the
// container before the person, so parent-first ordering is observable.
func shelfBundle() plugin.Bundle {
	return plugin.Bundle{
		ID:          "shelfkit",
		Version:     "0.1.0",
		DisplayName: plugin.L("Shelfkit"),
		MediaLabel:  plugin.L("Piece"),
		EntityKinds: []plugin.EntityKind{
			{Name: "maker", Archetype: "person", DisplayName: plugin.L("Maker"),
				Relations: []plugin.RelationSpec{{Name: "made", ToKind: plugin.MediaKindName}}},
			{Name: "box", Archetype: "container", DisplayName: plugin.LocaleString{"en": "Box", "fr": "Boîte"},
				Relations: []plugin.RelationSpec{{Name: "holds", ToKind: plugin.MediaKindName}, {Name: "box-maker", ToKind: "maker"}}},
			{Name: "lid", Archetype: "container", DisplayName: plugin.L("Lid"),
				Relations: []plugin.RelationSpec{{Name: "lid-of", ToKind: "box"}, {Name: "lid-holds", ToKind: plugin.MediaKindName}}},
			{Name: "tag", Archetype: "category", DisplayName: plugin.L("Tag"),
				Relations: []plugin.RelationSpec{{Name: "tags", ToKind: plugin.MediaKindName}}},
		},
		Layouts: []plugin.LayoutSpec{{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
			{ID: "boxes", Kind: "shelf", Title: plugin.LocaleString{"en": "Boxes", "fr": "Boîtes"},
				ArtworkSlot: "poster", Source: plugin.SourceSpec{Kind: "entity-items", EntityKind: "box"}},
			{ID: "makers", Kind: "shelf", Title: plugin.L("Makers"),
				ArtworkSlot: "poster", Source: plugin.SourceSpec{Kind: "entity-items", EntityKind: "maker"}},
			{ID: "tags", Kind: "shelf", Title: plugin.L("Tags"),
				ArtworkSlot: "poster", Source: plugin.SourceSpec{Kind: "entity-items", EntityKind: "tag"}},
			{ID: "everything", Kind: "grid", Title: plugin.L("Everything"),
				ArtworkSlot: "poster", Source: plugin.SourceSpec{Kind: "all-items"}},
		}}},
	}
}

// newShelfLibrary seeds a shelfkit Library: box "Alpha Quill" holds three
// audio pieces and, through its two lids, a fourth, and names makers Em and Zed;
// box "beta" names Zed. Piece "Quill Piece" credits Zed then Em; the video
// clip credits Zed.
func newShelfLibrary(t *testing.T, restricted bool) (*Handler, store.LibraryID, map[string]store.MediaID) {
	t.Helper()
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{shelfBundle()})
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
	library, err := st.CreateLibrary(ctx, "Shelf", restricted, "shelfkit", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	media := map[string]store.MediaID{}
	for name, title := range map[string]string{"p1": "Quill Piece", "p2": "Second Piece", "p3": "Third Piece", "p4": "Fourth Piece"} {
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/shelf/" + name + ".flac", Title: title,
			Container: "flac", Codec: "flac", Class: store.MediaClassAudio, ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		media[name] = item.ID
	}
	clip, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: library.ID, Path: "/shelf/clip.mp4", Title: "Clip",
		Container: "mp4", Codec: "h264", ContentFingerprint: "fp-clip",
	})
	if err != nil {
		t.Fatal(err)
	}
	media["clip"] = clip.ID
	// Boxes opt into search so the search card test can find one.
	if err := st.RegisterEntityKindPolicies(ctx, []store.EntityKindPolicy{{Kind: "box", FullTextSearchable: true}}); err != nil {
		t.Fatal(err)
	}
	for id, entity := range map[store.EntityID][2]string{
		"entity-box-a": {"box", "Alpha Quill"}, "entity-box-b": {"box", "beta"},
		"entity-lid-1": {"lid", "Lid 1"}, "entity-lid-2": {"lid", "Lid 2"},
		"entity-maker-z": {"maker", "Zed"}, "entity-maker-m": {"maker", "Em"},
	} {
		if err := st.UpsertEntity(ctx, store.Entity{ID: id, LibraryID: library.ID, Kind: entity[0], Title: entity[1]}); err != nil {
			t.Fatal(err)
		}
	}
	one, two := 1, 2
	for _, relation := range []store.EntityRelation{
		{FromEntityID: "entity-lid-1", Name: "lid-of", ToEntityID: "entity-box-a"},
		{FromEntityID: "entity-lid-2", Name: "lid-of", ToEntityID: "entity-box-a"},
		{FromEntityID: "entity-lid-1", Name: "lid-holds", ToMediaID: media["p1"]},
		{FromEntityID: "entity-lid-2", Name: "lid-holds", ToMediaID: media["p3"]},
		{FromEntityID: "entity-box-a", Name: "holds", ToMediaID: media["p1"]},
		{FromEntityID: "entity-box-a", Name: "holds", ToMediaID: media["p2"]},
		{FromEntityID: "entity-box-a", Name: "holds", ToMediaID: media["p4"]},
		{FromEntityID: "entity-box-a", Name: "box-maker", ToEntityID: "entity-maker-z", SortOrder: &two},
		{FromEntityID: "entity-box-a", Name: "box-maker", ToEntityID: "entity-maker-m", SortOrder: &one},
		{FromEntityID: "entity-box-b", Name: "box-maker", ToEntityID: "entity-maker-z"},
		{FromEntityID: "entity-maker-z", Name: "made", ToMediaID: media["p1"], SortOrder: &one},
		{FromEntityID: "entity-maker-m", Name: "made", ToMediaID: media["p1"], SortOrder: &two},
		{FromEntityID: "entity-maker-z", Name: "made", ToMediaID: media["clip"]},
	} {
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatal(err)
		}
	}
	return handler, library.ID, media
}

func libraryEntityTitles(items []LibraryEntityItem) []string {
	titles := []string{}
	for _, item := range items {
		titles = append(titles, item.Title)
	}
	return titles
}

// The browse kinds, their order and labels come from the plugin: a kind
// another kind points at comes first, a kind with no Entities is left out,
// and the Media view takes the plugin's unfiltered grid title.
func TestLibraryEntitiesListsThePluginsBrowseKinds(t *testing.T) {
	handler, libraryID, _ := newShelfLibrary(t, false)
	var resp LibraryEntitiesResponse
	execGraph(t, handler, authorizeViewer, libraryEntitiesTestQuery,
		map[string]any{"libraryID": string(libraryID)}, "libraryEntities", &resp)
	kinds := []string{}
	for _, kind := range resp.Kinds {
		kinds = append(kinds, kind.Kind)
	}
	if !slices.Equal(kinds, []string{"maker", "box"}) {
		t.Fatalf("kinds = %v, want maker before box and no empty tag kind", kinds)
	}
	box := resp.Kinds[1]
	if box.Label.Resolve("fr") != "Boîtes" || box.KindDisplayName.Resolve("fr") != "Boîte" || box.Archetype != "container" || box.Total != 2 {
		t.Fatalf("box kind = %+v, want the plugin's label, display name, archetype and a total of 2", box)
	}
	if resp.AllLabel.Resolve("en") != "Everything" {
		t.Fatalf("allLabel = %v, want the plugin's all-items title", resp.AllLabel)
	}
	if resp.Kind != "maker" || resp.Total != 2 || !slices.Equal(libraryEntityTitles(resp.Items), []string{"Em", "Zed"}) {
		t.Fatalf("default listing = kind %q total %d %v, want makers Em and Zed", resp.Kind, resp.Total, libraryEntityTitles(resp.Items))
	}
	em, zed := resp.Items[0], resp.Items[1]
	// Em: box Alpha Quill's own p1, p2 and p4 (p1 is also Em's own credit);
	// p3 sits under a lid, a grandchild, and is not counted.
	// Zed: both boxes, plus p1 and the clip it is credited on.
	if em.ChildCount != 1 || em.MediaCount != 3 || zed.ChildCount != 2 || zed.MediaCount != 4 {
		t.Fatalf("maker counts = Em %d/%d Zed %d/%d, want 1/3 and 2/4", em.ChildCount, em.MediaCount, zed.ChildCount, zed.MediaCount)
	}
	if em.Kind != "entity" || len(em.Parents) != 0 {
		t.Fatalf("maker card = %+v, want an entity card with no parents", em)
	}
}

// A kind's page carries child and Media counts, parents in relation order and
// the class its Media share; paging keeps the kind's total.
func TestLibraryEntitiesPagesAKindWithCountsAndParents(t *testing.T) {
	handler, libraryID, _ := newShelfLibrary(t, false)
	var first, second LibraryEntitiesResponse
	execGraph(t, handler, authorizeViewer, libraryEntitiesTestQuery,
		map[string]any{"libraryID": string(libraryID), "kind": "box", "limit": 1}, "libraryEntities", &first)
	execGraph(t, handler, authorizeViewer, libraryEntitiesTestQuery,
		map[string]any{"libraryID": string(libraryID), "kind": "box", "limit": 1, "offset": 1}, "libraryEntities", &second)
	if first.Total != 2 || second.Total != 2 || len(first.Items) != 1 || len(second.Items) != 1 {
		t.Fatalf("pages = %+v / %+v, want one box each and a total of 2", first, second)
	}
	alpha, beta := first.Items[0], second.Items[0]
	if alpha.ID != "entity-box-a" || alpha.ChildCount != 2 || alpha.MediaCount != 4 || alpha.MediaClass != "audio" {
		t.Fatalf("first box = %+v, want Alpha Quill with 2 lids, 4 audio pieces", alpha)
	}
	if got := []LibraryEntityParent{{ID: "entity-maker-m", Title: "Em"}, {ID: "entity-maker-z", Title: "Zed"}}; !slices.Equal(alpha.Parents, got) {
		t.Fatalf("Alpha Quill parents = %+v, want %+v in relation order", alpha.Parents, got)
	}
	if beta.ID != "entity-box-b" || beta.MediaCount != 0 || beta.MediaClass != "" || len(beta.Parents) != 1 || beta.Parents[0].Title != "Zed" {
		t.Fatalf("second box = %+v, want beta with no Media, no class and parent Zed", beta)
	}

	var lid LibraryEntitiesResponse
	execGraph(t, handler, authorizeViewer, libraryEntitiesTestQuery,
		map[string]any{"libraryID": string(libraryID), "kind": "lid"}, "libraryEntities", &lid)
	if len(lid.Kinds) != 2 || lid.Total != 0 || len(lid.Items) != 0 {
		t.Fatalf("non-browse kind = %+v, want the kinds and no items", lid)
	}
}

// A Library the session cannot see, and a plugin-less Library, answer no
// kinds; a missing libraryID is a client error.
func TestLibraryEntitiesHidesWhatTheSessionCannotBrowse(t *testing.T) {
	handler, libraryID, _ := newShelfLibrary(t, true)
	var hidden LibraryEntitiesResponse
	execGraph(t, handler, authorizeViewer, libraryEntitiesTestQuery,
		map[string]any{"libraryID": string(libraryID)}, "libraryEntities", &hidden)
	if len(hidden.Kinds) != 0 || hidden.Total != 0 || len(hidden.Items) != 0 || len(hidden.AllLabel) != 0 {
		t.Fatalf("restricted Library leaked %+v to an unrevealed session", hidden)
	}
	var revealed LibraryEntitiesResponse
	execGraph(t, handler, authorizeRevealed, libraryEntitiesTestQuery,
		map[string]any{"libraryID": string(libraryID)}, "libraryEntities", &revealed)
	if len(revealed.Kinds) != 2 || len(revealed.Items) != 2 {
		t.Fatalf("revealed session sees %+v, want the two kinds and two makers", revealed)
	}

	var plain LibraryEntitiesResponse
	execGraph(t, handler, authorizeViewer, libraryEntitiesTestQuery,
		map[string]any{"libraryID": "library-local"}, "libraryEntities", &plain)
	if len(plain.Kinds) != 0 || len(plain.Items) != 0 {
		t.Fatalf("plugin-less Library = %+v, want no kinds", plain)
	}
	if resp := postGraphJSON(t, handler, authorizeViewer, libraryEntitiesTestQuery, map[string]any{"libraryID": " "}); len(resp.Errors) == 0 {
		t.Fatal("a blank libraryID was accepted, want a client error")
	}
}

// An audio card credits its person-archetype Entities in link order, on the
// Library grid and in search; a video card and an Entity card carry none.
func TestAudioCardsCarryTheirByline(t *testing.T) {
	handler, libraryID, media := newShelfLibrary(t, false)
	var page LibraryItemsResponse
	execGraph(t, handler, authorizeViewer,
		`query($libraryID: String!) { libraryItems(libraryID: $libraryID, limit: 50) { items { id byline mediaClass } } }`,
		map[string]any{"libraryID": string(libraryID)}, "libraryItems", &page)
	bylines := map[string][]string{}
	for _, item := range page.Items {
		bylines[item.ID] = item.Byline
	}
	if got := bylines[string(media["p1"])]; !slices.Equal(got, []string{"Zed", "Em"}) {
		t.Fatalf("Quill Piece byline = %v, want Zed then Em", got)
	}
	if got := bylines[string(media["clip"])]; len(got) != 0 {
		t.Fatalf("video clip byline = %v, want none", got)
	}
	if got := bylines[string(media["p2"])]; len(got) != 0 {
		t.Fatalf("uncredited piece byline = %v, want none", got)
	}

	var search SearchResultResponse
	execGraph(t, handler, authorizeViewer,
		`query($query: String!) { searchResult(query: $query, mode: keyword) { items { id kind mediaClass byline entityKind } } }`,
		map[string]any{"query": "Quill"}, "searchResult", &search)
	rows := map[string]SearchResultItem{}
	for _, item := range search.Items {
		rows[item.ID] = item
	}
	piece, ok := rows[string(media["p1"])]
	if !ok || piece.MediaClass != "audio" || !slices.Equal(piece.Byline, []string{"Zed", "Em"}) {
		t.Fatalf("search piece row = %+v (found %v), want audio with byline Zed, Em", piece, ok)
	}
	if box, ok := rows["entity-box-a"]; !ok || box.EntityKind != "box" || box.MediaClass != "audio" || len(box.Byline) != 0 {
		t.Fatalf("search box row = %+v (found %v), want a box row with the audio class and no byline", box, ok)
	}
}

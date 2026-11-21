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
	music "github.com/beamfall/plugin-music"
)

const libraryEntitiesTestQuery = `query($libraryID: String!, $kind: String, $limit: Int, $offset: Int) {
	libraryEntities(libraryID: $libraryID, kind: $kind, limit: $limit, offset: $offset) {
		kinds { kind label kindDisplayName archetype total }
		childKinds { kind kindDisplayName kindPluralDisplayName }
		allLabel kind total
		items { id kind title mediaClass byline year childCount childCounts { kind count } mediaCount parents { id title } }
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
			{Name: "lid", Archetype: "container", DisplayName: plugin.L("Lid"), PluralDisplayName: plugin.L("Lids"),
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

func newMusicArtistBrowseFixture(t *testing.T) (*Handler, store.LibraryID) {
	t.Helper()
	st := storetest.Open(t)
	registry, err := newTestPluginRegistry([]plugin.Bundle{music.Bundle()})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(manifest.NewService(st, time.Now, registry), auth.NewService(st), st, registry, Options{
		EntityBrowseSources: map[plugin.ID]map[string][]store.EntityBrowseSource{music.PluginID: {"artist": {
			{Relation: "album-artist", FromKind: "album"},
			{Relation: "appears-on", ExcludeMediaRelation: "album-track"},
		}}},
	})
	ctx := context.Background()
	library, err := st.CreateLibrary(ctx, "Music", false, music.PluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range []store.Entity{
		{ID: "album", Kind: "album", Title: "An Album"},
		{ID: "album-artist", Kind: "artist", Title: "Earth, Wind & Fire"},
		{ID: "combined-credit", Kind: "artist", Title: "AURORA, Pomme"},
		{ID: "loose-credit", Kind: "artist", Title: "Tyler, The Creator"},
	} {
		entity.LibraryID = library.ID
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	tracks := map[string]store.MediaID{}
	for _, name := range []string{"grouped", "loose"} {
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/music/" + name + ".flac", Title: name,
			Container: "flac", Codec: "flac", Class: store.MediaClassAudio, ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		tracks[name] = item.ID
	}
	for _, relation := range []store.EntityRelation{
		{FromEntityID: "album", Name: "album-artist", ToEntityID: "album-artist"},
		{FromEntityID: "album", Name: "album-track", ToMediaID: tracks["grouped"]},
		{FromEntityID: "combined-credit", Name: "appears-on", ToMediaID: tracks["grouped"]},
		{FromEntityID: "loose-credit", Name: "appears-on", ToMediaID: tracks["loose"]},
	} {
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatal(err)
		}
	}
	return handler, library.ID
}

func TestLibraryEntitiesMusicArtistBrowse(t *testing.T) {
	handler, libraryID := newMusicArtistBrowseFixture(t)
	var resp LibraryEntitiesResponse
	execGraph(t, handler, authorizeViewer, libraryEntitiesTestQuery,
		map[string]any{"libraryID": string(libraryID), "kind": "artist"}, "libraryEntities", &resp)
	if len(resp.Kinds) != 2 || resp.Kinds[0].Kind != "artist" || resp.Kinds[0].Total != 2 ||
		resp.Kinds[1].Kind != "album" || resp.Kinds[1].Total != 1 || resp.Total != 2 ||
		!slices.Equal(libraryEntityTitles(resp.Items), []string{"Earth, Wind & Fire", "Tyler, The Creator"}) {
		t.Fatalf("Artists browse = %+v", resp)
	}
	// The music plugin, not core, names an artist's child kind.
	if len(resp.ChildKinds) != 1 || resp.ChildKinds[0].Kind != "album" ||
		resp.ChildKinds[0].KindPluralDisplayName.Resolve("en") == "" {
		t.Fatalf("artist child kinds = %+v, want the plugin-named album kind", resp.ChildKinds)
	}
}

func TestSourceItemsMusicArtistsHomeShelf(t *testing.T) {
	handler, _ := newMusicArtistBrowseFixture(t)
	var source plugin.SourceSpec
	for _, layout := range music.Bundle().Layouts {
		if layout.ID != "home" {
			continue
		}
		for _, section := range layout.Sections {
			if section.ID == "artists" {
				source = section.Source
				break
			}
		}
	}
	if source.Kind != "entity-items" {
		t.Fatalf("Artists home shelf source = %+v", source)
	}
	var items []SourceItem
	execGraph(t, handler, authorizeViewer, `query($kind: String!, $entityKind: String!, $home: Boolean) {
		sourceItems(kind: $kind, entityKind: $entityKind, home: $home) { id }
	}`, map[string]any{"kind": source.Kind, "entityKind": source.EntityKind, "home": true}, "sourceItems", &items)
	if got, want := itemIDs(items), []string{"album-artist", "loose-credit"}; !slices.Equal(got, want) {
		t.Fatalf("Artists home shelf = %v, want %v", got, want)
	}
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
	// The lid kind is box's only child kind; maker is a parent, not a child,
	// though it relates to box too. The labels are the plugin's.
	if len(first.ChildKinds) != 1 || first.ChildKinds[0].Kind != "lid" ||
		first.ChildKinds[0].KindDisplayName.Resolve("en") != "Lid" || first.ChildKinds[0].KindPluralDisplayName.Resolve("en") != "Lids" {
		t.Fatalf("box child kinds = %+v, want only lid named Lid/Lids", first.ChildKinds)
	}
	if got := []LibraryEntityChildCount{{Kind: "lid", Count: 2}}; !slices.Equal(alpha.ChildCounts, got) {
		t.Fatalf("Alpha Quill child counts = %+v, want %+v", alpha.ChildCounts, got)
	}
	if beta.ChildCounts == nil || len(beta.ChildCounts) != 0 {
		t.Fatalf("beta child counts = %#v, want an empty list", beta.ChildCounts)
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

// Audio Media and containers credit their person-archetype Entities in link
// order on the Library grids and in search.
func TestAudioCardsCarryTheirByline(t *testing.T) {
	handler, libraryID, media := newShelfLibrary(t, false)
	ctx := context.Background()
	if err := handler.store.UpsertEntity(ctx, store.Entity{
		ID: "entity-box-a", LibraryID: libraryID, Kind: "box", Title: "Alpha Quill",
		Fields: []store.EntityField{{Name: "release-date", Type: store.EntityFieldDate, Date: time.Date(2001, 2, 3, 0, 0, 0, 0, time.UTC)}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := handler.store.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "entity-box-b", Name: "holds", ToMediaID: media["clip"],
	}); err != nil {
		t.Fatal(err)
	}
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

	var boxes LibraryEntitiesResponse
	execGraph(t, handler, authorizeViewer, libraryEntitiesTestQuery,
		map[string]any{"libraryID": string(libraryID), "kind": "box"}, "libraryEntities", &boxes)
	if len(boxes.Items) != 2 {
		t.Fatalf("box cards = %+v, want two", boxes.Items)
	}
	if box := boxes.Items[0]; box.ID != "entity-box-a" || box.MediaClass != "audio" ||
		!slices.Equal(box.Byline, []string{"Em", "Zed"}) || box.Year != 2001 {
		t.Fatalf("audio box card = %+v, want Em, Zed and release year 2001", box)
	}
	if box := boxes.Items[1]; box.ID != "entity-box-b" || box.MediaClass != "video" || len(box.Byline) != 0 {
		t.Fatalf("video box card = %+v, want a video card without a byline", box)
	}

	var search SearchResultResponse
	execGraph(t, handler, authorizeViewer,
		`query($query: String!) { searchResult(query: $query, mode: keyword) { items { id kind mediaClass byline year entityKind } } }`,
		map[string]any{"query": "Quill"}, "searchResult", &search)
	rows := map[string]SearchResultItem{}
	for _, item := range search.Items {
		rows[item.ID] = item
	}
	piece, ok := rows[string(media["p1"])]
	if !ok || piece.MediaClass != "audio" || !slices.Equal(piece.Byline, []string{"Zed", "Em"}) {
		t.Fatalf("search piece row = %+v (found %v), want audio with byline Zed, Em", piece, ok)
	}
	if box, ok := rows["entity-box-a"]; !ok || box.EntityKind != "box" || box.MediaClass != "audio" ||
		!slices.Equal(box.Byline, []string{"Em", "Zed"}) || box.Year != 2001 {
		t.Fatalf("search box row = %+v (found %v), want audio with byline Em, Zed and release year 2001", box, ok)
	}
}

const libraryEntitiesBrowseTestQuery = `query($libraryID: String!, $kind: String, $sort: String, $direction: String, $watch: String, $groupFilters: Map) {
	libraryEntities(libraryID: $libraryID, kind: $kind, sort: $sort, direction: $direction, watch: $watch, groupFilters: $groupFilters) {
		kinds { kind total }
		kind total
		items { id title }
		letters { letter offset count }
	}
}`

// An Entity kind takes libraryItems's sort and filter arguments (CORE-2440):
// a title sort answers the letter index, a filter narrows the total but not
// the kind totals, and an unknown sort is a client error.
func TestLibraryEntitiesSortsAndFiltersAKind(t *testing.T) {
	handler, libraryID, _ := newShelfLibrary(t, false)
	browse := func(vars map[string]any) LibraryEntitiesResponse {
		t.Helper()
		vars["libraryID"] = string(libraryID)
		var resp LibraryEntitiesResponse
		execGraph(t, handler, authorizeViewer, libraryEntitiesBrowseTestQuery, vars, "libraryEntities", &resp)
		return resp
	}

	desc := browse(map[string]any{"kind": "box", "direction": "desc"})
	if got := libraryEntityTitles(desc.Items); !slices.Equal(got, []string{"beta", "Alpha Quill"}) {
		t.Fatalf("title desc = %v, want [beta Alpha Quill]", got)
	}
	if want := []LibraryItemsLetter{{Letter: "B", Offset: 0, Count: 1}, {Letter: "A", Offset: 1, Count: 1}}; !slices.Equal(desc.Letters, want) {
		t.Fatalf("letters = %+v, want %+v", desc.Letters, want)
	}

	// Box beta reaches no Media, so a watch filter drops it; the kind total stays.
	unwatched := browse(map[string]any{"kind": "box", "watch": "unwatched"})
	if got := libraryEntityTitles(unwatched.Items); unwatched.Total != 1 || !slices.Equal(got, []string{"Alpha Quill"}) || unwatched.Kinds[1].Total != 2 {
		t.Fatalf("unwatched boxes = %v (total %d, kinds %+v), want Alpha Quill of 1 with the kind total 2", got, unwatched.Total, unwatched.Kinds)
	}

	// Zed reaches the clip, the last Media added; a non-title sort has no letters.
	added := browse(map[string]any{"kind": "maker", "sort": "added", "direction": "desc"})
	if got := libraryEntityTitles(added.Items); !slices.Equal(got, []string{"Zed", "Em"}) || len(added.Letters) != 0 {
		t.Fatalf("added desc = %v letters %+v, want [Zed Em] and no letters", got, added.Letters)
	}

	none := browse(map[string]any{"kind": "box", "groupFilters": map[string]any{"tag": "Absent"}})
	if none.Total != 0 || len(none.Items) != 0 || len(none.Kinds) != 2 {
		t.Fatalf("unmatched group filter = %+v, want no items and the kinds intact", none)
	}

	if resp := postGraphJSON(t, handler, authorizeViewer, libraryEntitiesBrowseTestQuery,
		map[string]any{"libraryID": string(libraryID), "sort": "loudness"}); len(resp.Errors) == 0 {
		t.Fatal("an unknown sort was accepted, want a client error")
	}
}

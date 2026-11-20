package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// libraryEntitiesFixture seeds two "box" Entities, each holding "lid"
// Entities that carry Media, a "maker" each box points at, and a box in the
// hidden Library. Box Alpha holds two lids with three Media each plus one
// Media of its own.
func libraryEntitiesFixture(t *testing.T) (*Store, Library, Library, map[string]MediaID) {
	t.Helper()
	st, lib, hidden := relatedFixture(t)
	ctx := context.Background()
	upsertRelatedEntity(t, st, "entity-box-b", lib.ID, "box", "beta")
	upsertRelatedEntity(t, st, "entity-box-a", lib.ID, "box", "Alpha")
	upsertRelatedEntity(t, st, "entity-box-c", lib.ID, "box", "Gamma")
	upsertRelatedEntity(t, st, "entity-box-x", hidden.ID, "box", "Locked")
	upsertRelatedEntity(t, st, "entity-lid-1", lib.ID, "lid", "Lid 1")
	upsertRelatedEntity(t, st, "entity-lid-2", lib.ID, "lid", "Lid 2")
	upsertRelatedEntity(t, st, "entity-maker-z", lib.ID, "maker", "Zed")
	upsertRelatedEntity(t, st, "entity-maker-m", lib.ID, "maker", "Em")
	media := map[string]MediaID{}
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "own"} {
		item, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: lib.ID, Path: "/shelf/" + name + ".flac", Title: name, Container: "flac", Codec: "flac",
			Class: MediaClassAudio, ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		media[name] = item.ID
	}
	two := 2
	one := 1
	relateEntities(t, st,
		EntityRelation{FromEntityID: "entity-lid-1", Name: "lid-of", ToEntityID: "entity-box-a"},
		EntityRelation{FromEntityID: "entity-lid-2", Name: "lid-of", ToEntityID: "entity-box-a"},
		EntityRelation{FromEntityID: "entity-lid-1", Name: "holds", ToMediaID: media["a"]},
		EntityRelation{FromEntityID: "entity-lid-1", Name: "holds", ToMediaID: media["b"]},
		EntityRelation{FromEntityID: "entity-lid-1", Name: "holds", ToMediaID: media["c"]},
		EntityRelation{FromEntityID: "entity-lid-2", Name: "holds", ToMediaID: media["d"]},
		EntityRelation{FromEntityID: "entity-lid-2", Name: "holds", ToMediaID: media["e"]},
		EntityRelation{FromEntityID: "entity-lid-2", Name: "holds", ToMediaID: media["f"]},
		// A Media held by the box and by one of its lids counts once.
		EntityRelation{FromEntityID: "entity-box-a", Name: "holds", ToMediaID: media["a"]},
		EntityRelation{FromEntityID: "entity-box-a", Name: "holds", ToMediaID: media["own"]},
		EntityRelation{FromEntityID: "entity-box-a", Name: "made-by", ToEntityID: "entity-maker-z", SortOrder: &two},
		EntityRelation{FromEntityID: "entity-box-a", Name: "made-by", ToEntityID: "entity-maker-m", SortOrder: &one},
		EntityRelation{FromEntityID: "entity-box-b", Name: "made-by", ToEntityID: "entity-maker-z"},
	)
	return st, lib, hidden, media
}

func libraryEntityIDs(items []LibraryEntity) []MediaID {
	out := []MediaID{}
	for _, item := range items {
		out = append(out, item.Item.ID)
	}
	return out
}

// TestLibraryEntitiesListsKindWithCounts pins the CORE-2362 listing: one kind
// in case-insensitive title order, every requested kind totalled, children
// and distinct Media counted through the child kinds, and parents in relation
// order.
func TestLibraryEntitiesListsKindWithCounts(t *testing.T) {
	st, lib, _, _ := libraryEntitiesFixture(t)
	ctx := context.Background()
	query := LibraryEntitiesQuery{
		LibraryID:   lib.ID,
		Kinds:       []string{"maker", "box"},
		Kind:        "box",
		ChildKinds:  []string{"lid"},
		ParentKinds: []string{"maker"},
	}
	got, err := st.LibraryEntities(ctx, LibraryFilter{AllLibraries: true}, query)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"box": 3, "maker": 2}; !reflect.DeepEqual(got.Totals, want) {
		t.Fatalf("Totals = %v, want %v", got.Totals, want)
	}
	if got.Total != 3 {
		t.Fatalf("Total = %d, want 3", got.Total)
	}
	if ids, want := libraryEntityIDs(got.Items), []MediaID{"entity-box-a", "entity-box-b", "entity-box-c"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("items = %v, want %v", ids, want)
	}
	alpha := got.Items[0]
	if alpha.Item.Kind != "entity" || alpha.Item.Title != "Alpha" {
		t.Fatalf("item = %+v, want an entity card titled Alpha", alpha.Item)
	}
	if alpha.ChildCount != 2 || alpha.MediaCount != 7 {
		t.Fatalf("Alpha counts = %d children / %d media, want 2 / 7", alpha.ChildCount, alpha.MediaCount)
	}
	if want := []LibraryEntityRef{{ID: "entity-maker-m", Title: "Em"}, {ID: "entity-maker-z", Title: "Zed"}}; !reflect.DeepEqual(alpha.Parents, want) {
		t.Fatalf("Alpha parents = %+v, want %+v", alpha.Parents, want)
	}
	beta := got.Items[1]
	if beta.ChildCount != 0 || beta.MediaCount != 0 || len(beta.Parents) != 1 || beta.Parents[0].Title != "Zed" {
		t.Fatalf("beta = %+v, want no counts and parent Zed", beta)
	}

	// Paging keeps the total.
	query.Limit, query.Offset = 1, 1
	page, err := st.LibraryEntities(ctx, LibraryFilter{AllLibraries: true}, query)
	if err != nil {
		t.Fatal(err)
	}
	if ids := libraryEntityIDs(page.Items); page.Total != 3 || !reflect.DeepEqual(ids, []MediaID{"entity-box-b"}) {
		t.Fatalf("page = %v (total %d), want [entity-box-b] of 3", ids, page.Total)
	}

	// The child kind lists its parent box through the reverse link.
	lids, err := st.LibraryEntities(ctx, LibraryFilter{AllLibraries: true}, LibraryEntitiesQuery{
		LibraryID: lib.ID, Kinds: []string{"lid"}, Kind: "lid", ParentKinds: []string{"box"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lids.Items) != 2 || lids.Items[0].MediaCount != 3 || len(lids.Items[0].Parents) != 1 || lids.Items[0].Parents[0].ID != "entity-box-a" {
		t.Fatalf("lids = %+v, want two lids of Alpha with three Media each", lids.Items)
	}
}

func TestLibraryEntitiesBrowseSources(t *testing.T) {
	for _, plane := range []string{"catalog", "encrypted"} {
		t.Run(plane, func(t *testing.T) {
			ctx := context.Background()
			var st *Store
			var libraryID LibraryID
			if plane == "encrypted" {
				var media Media
				st, media = seededEncryptedMedia(t, "browse-sources-seed")
				t.Cleanup(func() {
					if err := st.Close(); err != nil {
						t.Error(err)
					}
				})
				libraryID = media.LibraryID
			} else {
				var lib Library
				st, lib, _ = relatedFixture(t)
				libraryID = lib.ID
			}
			for id, title := range map[EntityID]string{
				"maker-a": "Alpha", "maker-b": "Beta, Chorus", "maker-g": "Gamma", "maker-t": "Trashed",
			} {
				upsertRelatedEntity(t, st, id, libraryID, "maker", title)
			}
			upsertRelatedEntity(t, st, "box", libraryID, "box", "Box")
			media := map[string]MediaID{}
			for _, name := range []string{"grouped", "loose", "trashed"} {
				item, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
					LibraryID: libraryID, Path: "/shelf/" + name + ".flac", Title: name,
					Container: "flac", Codec: "flac", Class: MediaClassAudio, ContentFingerprint: "fp-" + name,
				})
				if err != nil {
					t.Fatal(err)
				}
				media[name] = item.ID
			}
			relateEntities(t, st,
				EntityRelation{FromEntityID: "box", Name: "box-maker", ToEntityID: "maker-a"},
				EntityRelation{FromEntityID: "box", Name: "contains", ToMediaID: media["grouped"]},
				EntityRelation{FromEntityID: "maker-b", Name: "credit", ToMediaID: media["grouped"]},
				EntityRelation{FromEntityID: "maker-g", Name: "credit", ToMediaID: media["loose"]},
				EntityRelation{FromEntityID: "maker-t", Name: "credit", ToMediaID: media["trashed"]},
			)
			filter := LibraryFilter{AllLibraries: true}
			if plane == "encrypted" {
				// Seed the encrypted plane's lifecycle marker for this read test.
				if _, err := st.encryptedDBs[libraryID].writer.ExecContext(ctx,
					`UPDATE media SET trashed_at_ms = 1 WHERE id = ?`, media["trashed"]); err != nil {
					t.Fatal(err)
				}
			} else if err := st.TrashMedia(ctx, media["trashed"], filter); err != nil {
				t.Fatal(err)
			}
			query := LibraryEntitiesQuery{LibraryID: libraryID, Kinds: []string{"maker", "box"}, Kind: "maker"}
			list := func() LibraryEntitiesResult {
				t.Helper()
				got, err := st.LibraryEntities(ctx, filter, query)
				if err != nil {
					t.Fatal(err)
				}
				return got
			}
			if got := list(); got.Total != 4 || got.Totals["maker"] != 4 || len(got.Letters) != 4 ||
				!reflect.DeepEqual(libraryEntityIDs(got.Items), []MediaID{"maker-a", "maker-b", "maker-g", "maker-t"}) {
				t.Fatalf("browse without rules = %+v", got)
			}
			query.BrowseSources = map[string][]EntityBrowseSource{"maker": {
				{Relation: "box-maker", FromKind: "box"},
				{Relation: "credit", ExcludeMediaRelation: "contains"},
			}}
			letters := []LibraryBrowseLetter{{Letter: "A", Count: 1}, {Letter: "G", Offset: 1, Count: 1}}
			if got := list(); got.Total != 2 || !reflect.DeepEqual(got.Totals, map[string]int{"maker": 2, "box": 1}) ||
				!reflect.DeepEqual(libraryEntityIDs(got.Items), []MediaID{"maker-a", "maker-g"}) || !reflect.DeepEqual(got.Letters, letters) {
				t.Fatalf("browse with rules = %+v", got)
			}
			query.Offset, query.Limit = 1, 1
			if got := list(); got.Total != 2 || !reflect.DeepEqual(libraryEntityIDs(got.Items), []MediaID{"maker-g"}) || !reflect.DeepEqual(got.Letters, letters) {
				t.Fatalf("page with rules = %+v", got)
			}
			query.Browse.Text = "Chorus"
			if got := list(); got.Total != 0 || got.Totals["maker"] != 2 || len(got.Items) != 0 || len(got.Letters) != 0 {
				t.Fatalf("text filter for hidden Entity = %+v", got)
			}
			query.Browse.Text = ""
			credits, err := st.EntitiesForMedia(ctx, media["grouped"], "credit", filter)
			if err != nil || len(credits) != 1 || credits[0].Entity.Title != "Beta, Chorus" {
				t.Fatalf("original track credit = %+v, err = %v", credits, err)
			}

			if plane == "encrypted" {
				if _, err := st.encryptedDBs[libraryID].writer.ExecContext(ctx,
					`UPDATE entity SET trashed_at_ms = 1 WHERE id = 'box'`); err != nil {
					t.Fatal(err)
				}
			} else if err := st.TrashEntity(ctx, "box", filter, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			query.Offset = 0
			if got := list(); got.Total != 1 || got.Totals["box"] != 0 || !reflect.DeepEqual(libraryEntityIDs(got.Items), []MediaID{"maker-g"}) {
				t.Fatalf("browse after trashing source = %+v", got)
			}
			if plane == "encrypted" {
				if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
					t.Fatal(err)
				}
				if got := list(); got.Total != 0 || len(got.Totals) != 0 || len(got.Items) != 0 || len(got.Letters) != 0 {
					t.Fatalf("sealed browse = %+v", got)
				}
				if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
					t.Fatal(err)
				}
				if got := list(); got.Total != 1 || !reflect.DeepEqual(libraryEntityIDs(got.Items), []MediaID{"maker-g"}) {
					t.Fatalf("unsealed browse = %+v", got)
				}
			}
		})
	}
}

// TestLibraryEntitiesVisibility pins the browse funnel: a Library outside the
// filter lists nothing, trashed Entities and Media drop out of the listing and
// the counts, and the certification ceiling hides Media from the count.
func TestLibraryEntitiesVisibility(t *testing.T) {
	st, lib, hidden, media := libraryEntitiesFixture(t)
	ctx := context.Background()
	query := LibraryEntitiesQuery{LibraryID: lib.ID, Kinds: []string{"box"}, Kind: "box", ChildKinds: []string{"lid"}}

	outside, err := st.LibraryEntities(ctx, LibraryFilter{AllowedIDs: []LibraryID{hidden.ID}}, query)
	if err != nil {
		t.Fatal(err)
	}
	if outside.Total != 0 || len(outside.Items) != 0 || len(outside.Totals) != 0 {
		t.Fatalf("Library outside the filter = %+v, want nothing", outside)
	}
	none, err := st.LibraryEntities(ctx, LibraryFilter{}, query)
	if err != nil {
		t.Fatal(err)
	}
	if none.Total != 0 || len(none.Items) != 0 {
		t.Fatalf("empty filter = %+v, want nothing (fail closed)", none)
	}
	locked, err := st.LibraryEntities(ctx, LibraryFilter{AllLibraries: true}, LibraryEntitiesQuery{LibraryID: hidden.ID, Kinds: []string{"box"}, Kind: "box"})
	if err != nil {
		t.Fatal(err)
	}
	if locked.Total != 0 {
		t.Fatalf("unrevealed hidden Library = %+v, want nothing", locked)
	}
	revealed, err := st.LibraryEntities(ctx, LibraryFilter{AllLibraries: true, Revealed: true}, LibraryEntitiesQuery{LibraryID: hidden.ID, Kinds: []string{"box"}, Kind: "box"})
	if err != nil {
		t.Fatal(err)
	}
	if revealed.Total != 1 || !revealed.Items[0].Item.Restricted {
		t.Fatalf("revealed hidden Library = %+v, want one restricted box", revealed)
	}

	server := LibraryFilter{AllLibraries: true, Revealed: true}
	if err := st.TrashEntity(ctx, "entity-box-c", server, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.TrashEntity(ctx, "entity-lid-2", server, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, media["b"], []MediaField{{Name: "certification", Type: EntityFieldText, Text: "high"}}); err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{
		AllLibraries:  true,
		Certification: []CertificationConstraint{{PluginID: "shelf", Allowed: []string{"low"}, AllowUnrated: true}},
	}
	got, err := st.LibraryEntities(ctx, filter, query)
	if err != nil {
		t.Fatal(err)
	}
	if ids := libraryEntityIDs(got.Items); got.Total != 2 || !reflect.DeepEqual(ids, []MediaID{"entity-box-a", "entity-box-b"}) {
		t.Fatalf("after trash = %v (total %d), want Alpha and beta", ids, got.Total)
	}
	// Alpha keeps one lid (a, b, c) and its own Media; b is above the ceiling.
	if alpha := got.Items[0]; alpha.ChildCount != 1 || alpha.MediaCount != 3 {
		t.Fatalf("Alpha counts = %d / %d, want 1 child and 3 visible Media", alpha.ChildCount, alpha.MediaCount)
	}
}

// TestLibraryEntitiesUnknownKind pins that an unlisted kind lists nothing but
// still reports the requested kinds' totals.
func TestLibraryEntitiesUnknownKind(t *testing.T) {
	st, lib, _, _ := libraryEntitiesFixture(t)
	got, err := st.LibraryEntities(context.Background(), LibraryFilter{AllLibraries: true}, LibraryEntitiesQuery{
		LibraryID: lib.ID, Kinds: []string{"box"}, Kind: "nothing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 0 || len(got.Items) != 0 || got.Totals["box"] != 3 {
		t.Fatalf("unknown kind = %+v, want no items and the box total", got)
	}
}

// TestAnnotateMediaClassEntityTakesSharedClass pins that an entity card takes
// the class its linked Media share (CORE-2362) and stays empty when they mix
// or when it links none.
func TestAnnotateMediaClassEntityTakesSharedClass(t *testing.T) {
	st, lib, _, media := libraryEntitiesFixture(t)
	ctx := context.Background()
	video, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: "/shelf/clip.mkv", Title: "Clip", Container: "matroska", Codec: "h264",
		Class: MediaClassVideo, ContentFingerprint: "fp-clip",
	})
	if err != nil {
		t.Fatal(err)
	}
	relateEntities(t, st,
		EntityRelation{FromEntityID: "entity-lid-2", Name: "holds", ToMediaID: video.ID},
	)
	items := []SourceItem{
		{ID: "entity-lid-1", Kind: "entity"},
		{ID: "entity-lid-2", Kind: "entity"},
		{ID: "entity-box-b", Kind: "entity"},
		{ID: media["a"], Kind: "media"},
	}
	if err := st.AnnotateMediaClass(ctx, items); err != nil {
		t.Fatal(err)
	}
	want := []MediaClass{MediaClassAudio, "", "", MediaClassAudio}
	for i, item := range items {
		if item.MediaClass != want[i] || (item.Kind == "entity" && item.Year != 0) {
			t.Errorf("%s class = %q year = %d, want %q and no year", item.ID, item.MediaClass, item.Year, want[i])
		}
	}
}

// TestMediaEntityLinksOrderAndVisibility pins the byline read: the Entities
// linking to each Media item, in relation sort order then title, without
// trashed Entities.
func TestMediaEntityLinksOrderAndVisibility(t *testing.T) {
	st, lib, _, media := libraryEntitiesFixture(t)
	ctx := context.Background()
	upsertRelatedEntity(t, st, "entity-person-b", lib.ID, "maker", "Bea")
	upsertRelatedEntity(t, st, "entity-person-a", lib.ID, "maker", "Abe")
	upsertRelatedEntity(t, st, "entity-person-gone", lib.ID, "maker", "Gone")
	one := 1
	relateEntities(t, st,
		EntityRelation{FromEntityID: "entity-person-a", Name: "made", ToMediaID: media["a"]},
		EntityRelation{FromEntityID: "entity-person-b", Name: "made", ToMediaID: media["a"], SortOrder: &one},
		EntityRelation{FromEntityID: "entity-person-gone", Name: "made", ToMediaID: media["a"]},
	)
	if err := st.TrashEntity(ctx, "entity-person-gone", LibraryFilter{AllLibraries: true, Revealed: true}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	links, err := st.MediaEntityLinks(ctx, []MediaID{media["a"], media["own"], "media-missing"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, link := range links[media["a"]] {
		got = append(got, string(link.EntityID))
		if link.LibraryID != lib.ID {
			t.Fatalf("link %+v carries Library %q, want %q", link, link.LibraryID, lib.ID)
		}
	}
	// lid-1 and box-a hold the Media without a sort order; Bea leads by hers.
	want := []string{"entity-person-b", "entity-person-a", "entity-box-a", "entity-lid-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("links(a) = %v, want %v", got, want)
	}
	if own := links[media["own"]]; len(own) != 1 || own[0].Kind != "box" || own[0].Title != "Alpha" {
		t.Fatalf("links(own) = %+v, want box Alpha", own)
	}
	if _, ok := links["media-missing"]; ok {
		t.Fatal("missing Media has links")
	}
}

// TestRelatedEntityGroupsNaturalTitleOrder pins that Entities without a
// relation sort order follow natural title order (CORE-2362).
func TestRelatedEntityGroupsNaturalTitleOrder(t *testing.T) {
	st, lib, _ := relatedFixture(t)
	ctx := context.Background()
	upsertRelatedEntity(t, st, "entity-rack", lib.ID, "rack", "Rack")
	for id, title := range map[EntityID]string{"entity-10": "Part 10", "entity-2": "Part 2", "entity-1": "part 1", "entity-x": "Extra"} {
		upsertRelatedEntity(t, st, id, lib.ID, "part", title)
		relateEntities(t, st, EntityRelation{FromEntityID: id, Name: "part-of", ToEntityID: "entity-rack"})
	}
	groups, err := st.RelatedEntityGroups(ctx, "entity-rack", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]MediaID{"part": {"entity-x", "entity-1", "entity-2", "entity-10"}}
	if got := groupIDs(groups); !reflect.DeepEqual(got, want) {
		t.Fatalf("RelatedEntityGroups = %v, want %v", got, want)
	}
}

func TestNaturalCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"Part 2", "Part 10", -1},
		{"Part 10", "Part 2", 1},
		{"part 02", "Part 2", 0},
		{"Alpha", "beta", -1},
		{"Track", "Track 1", -1},
		{"9", "10", -1},
	} {
		if got := naturalCompare(tc.a, tc.b); got != tc.want {
			t.Errorf("naturalCompare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

package store

import (
	"context"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"
)

// relatedFixture seeds a shelf Library and a restricted one with Entities of
// three kinds and returns the open store.
func relatedFixture(t *testing.T) (*Store, Library, Library) {
	t.Helper()
	st := openSeeded(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Shelf", false, "shelf", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := st.CreateLibrary(ctx, "Locked", true, "shelf", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	return st, lib, hidden
}

func upsertRelatedEntity(t *testing.T, st *Store, id EntityID, library LibraryID, kind, title string) {
	t.Helper()
	if err := st.UpsertEntity(context.Background(), Entity{ID: id, LibraryID: library, Kind: kind, Title: title}); err != nil {
		t.Fatal(err)
	}
}

func relateEntities(t *testing.T, st *Store, relations ...EntityRelation) {
	t.Helper()
	for _, relation := range relations {
		if err := st.UpsertEntityRelation(context.Background(), relation); err != nil {
			t.Fatal(err)
		}
	}
}

// groupIDs flattens groups to kind → ordered Entity ids for comparison.
func groupIDs(groups []RelatedEntityGroup) map[string][]MediaID {
	out := map[string][]MediaID{}
	for _, group := range groups {
		for _, item := range group.Items {
			out[group.Kind] = append(out[group.Kind], item.ID)
		}
	}
	return out
}

// TestRelatedEntityGroupsBothDirections pins that an Entity lists the
// Entities it points at and the Entities pointing at it, grouped by kind in
// kind-name order, each Entity once, never itself, and never a Media row.
func TestRelatedEntityGroupsBothDirections(t *testing.T) {
	st, lib, _ := relatedFixture(t)
	ctx := context.Background()
	server := LibraryFilter{Revealed: true, AllLibraries: true}
	upsertRelatedEntity(t, st, "entity-maker", lib.ID, "maker", "Maker")
	upsertRelatedEntity(t, st, "entity-box-a", lib.ID, "box", "Alpha")
	upsertRelatedEntity(t, st, "entity-box-b", lib.ID, "box", "Beta")
	upsertRelatedEntity(t, st, "entity-place", lib.ID, "place", "Place")
	upsertRelatedEntity(t, st, "entity-lone", lib.ID, "maker", "Lone")
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: "/shelf/item.flac", Title: "Item", Container: "flac", Codec: "flac",
		Class: MediaClassAudio, ContentFingerprint: "fp-item",
	})
	if err != nil {
		t.Fatal(err)
	}
	relateEntities(t, st,
		EntityRelation{FromEntityID: "entity-box-a", Name: "made-by", ToEntityID: "entity-maker"},
		EntityRelation{FromEntityID: "entity-box-b", Name: "made-by", ToEntityID: "entity-maker"},
		// A second relation to the same Entity must not duplicate it.
		EntityRelation{FromEntityID: "entity-box-b", Name: "sold-by", ToEntityID: "entity-maker"},
		EntityRelation{FromEntityID: "entity-maker", Name: "based-in", ToEntityID: "entity-place"},
		EntityRelation{FromEntityID: "entity-maker", Name: "made", ToMediaID: media.ID},
	)

	groups, err := st.RelatedEntityGroups(ctx, "entity-maker", server)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]MediaID{
		"box":   {"entity-box-a", "entity-box-b"},
		"place": {"entity-place"},
	}
	if got := groupIDs(groups); !reflect.DeepEqual(got, want) {
		t.Fatalf("RelatedEntityGroups(maker) = %v, want %v", got, want)
	}
	if len(groups) != 2 || groups[0].Kind != "box" || groups[1].Kind != "place" {
		t.Fatalf("groups not in kind order: %+v", groups)
	}
	if item := groups[0].Items[0]; item.Kind != "entity" || item.Title != "Alpha" {
		t.Fatalf("related item = %+v, want an entity item titled Alpha", item)
	}

	groups, err = st.RelatedEntityGroups(ctx, "entity-box-a", server)
	if err != nil {
		t.Fatal(err)
	}
	if got := groupIDs(groups); !reflect.DeepEqual(got, map[string][]MediaID{"maker": {"entity-maker"}}) {
		t.Fatalf("RelatedEntityGroups(box) = %v, want the maker it points at", got)
	}

	groups, err = st.RelatedEntityGroups(ctx, "entity-lone", server)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Fatalf("RelatedEntityGroups(lone) = %+v, want none", groups)
	}
}

// TestRelatedEntityGroupsHonorVisibility pins the Reveal funnel: trashed
// related Entities and Entities in Libraries the filter hides are omitted.
func TestRelatedEntityGroupsHonorVisibility(t *testing.T) {
	st, lib, hidden := relatedFixture(t)
	ctx := context.Background()
	server := LibraryFilter{Revealed: true, AllLibraries: true}
	upsertRelatedEntity(t, st, "entity-maker", lib.ID, "maker", "Maker")
	upsertRelatedEntity(t, st, "entity-box-open", lib.ID, "box", "Open")
	upsertRelatedEntity(t, st, "entity-box-gone", lib.ID, "box", "Gone")
	upsertRelatedEntity(t, st, "entity-box-locked", hidden.ID, "box", "Locked")
	relateEntities(t, st,
		EntityRelation{FromEntityID: "entity-box-open", Name: "made-by", ToEntityID: "entity-maker"},
		EntityRelation{FromEntityID: "entity-box-gone", Name: "made-by", ToEntityID: "entity-maker"},
		EntityRelation{FromEntityID: "entity-box-locked", Name: "made-by", ToEntityID: "entity-maker"},
	)
	if err := st.TrashEntity(ctx, "entity-box-gone", server, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	unrevealed := LibraryFilter{AllLibraries: true}
	groups, err := st.RelatedEntityGroups(ctx, "entity-maker", unrevealed)
	if err != nil {
		t.Fatal(err)
	}
	if got := groupIDs(groups); !reflect.DeepEqual(got, map[string][]MediaID{"box": {"entity-box-open"}}) {
		t.Fatalf("unrevealed RelatedEntityGroups = %v, want only the open box", got)
	}

	groups, err = st.RelatedEntityGroups(ctx, "entity-maker", server)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]MediaID{"box": {"entity-box-locked", "entity-box-open"}}
	if got := groupIDs(groups); !reflect.DeepEqual(got, want) {
		t.Fatalf("revealed RelatedEntityGroups = %v, want %v", got, want)
	}
	for _, item := range groups[0].Items {
		if item.Restricted != (item.ID == "entity-box-locked") {
			t.Fatalf("item %s restricted = %v", item.ID, item.Restricted)
		}
	}
}

// TestRelatedEntityGroupsHiddenContextEntity pins that the context Entity
// must itself be visible: a hidden one errors like a missing one.
func TestRelatedEntityGroupsHiddenContextEntity(t *testing.T) {
	st, lib, hidden := relatedFixture(t)
	ctx := context.Background()
	upsertRelatedEntity(t, st, "entity-locked", hidden.ID, "maker", "Locked")
	upsertRelatedEntity(t, st, "entity-box", lib.ID, "box", "Box")
	relateEntities(t, st, EntityRelation{FromEntityID: "entity-box", Name: "made-by", ToEntityID: "entity-locked"})

	if _, err := st.RelatedEntityGroups(ctx, "entity-locked", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("hidden context Entity returned related groups")
	}
	if _, err := st.RelatedEntityGroups(ctx, "entity-missing", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("missing context Entity returned related groups")
	}
}

// TestRelatedEntityGroupsOrderBySortOrderThenTitle pins in-group ordering:
// relation rows with a sort order first in that order, then by title.
func TestRelatedEntityGroupsOrderBySortOrderThenTitle(t *testing.T) {
	st, lib, _ := relatedFixture(t)
	ctx := context.Background()
	upsertRelatedEntity(t, st, "entity-shelf", lib.ID, "rack", "Rack")
	upsertRelatedEntity(t, st, "entity-a", lib.ID, "box", "Aardvark")
	upsertRelatedEntity(t, st, "entity-b", lib.ID, "box", "Zebra")
	upsertRelatedEntity(t, st, "entity-c", lib.ID, "box", "Mango")
	upsertRelatedEntity(t, st, "entity-d", lib.ID, "box", "Banana")
	one, two := 1, 2
	relateEntities(t, st,
		EntityRelation{FromEntityID: "entity-shelf", Name: "holds", ToEntityID: "entity-a"},
		EntityRelation{FromEntityID: "entity-shelf", Name: "holds", ToEntityID: "entity-b", SortOrder: &two},
		EntityRelation{FromEntityID: "entity-shelf", Name: "holds", ToEntityID: "entity-c", SortOrder: &one},
		EntityRelation{FromEntityID: "entity-d", Name: "sits-on", ToEntityID: "entity-shelf"},
	)

	groups, err := st.RelatedEntityGroups(ctx, "entity-shelf", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]MediaID{"box": {"entity-c", "entity-b", "entity-a", "entity-d"}}
	if got := groupIDs(groups); !reflect.DeepEqual(got, want) {
		t.Fatalf("RelatedEntityGroups order = %v, want %v", got, want)
	}
}

// TestRelatedEntityGroupsReadsTheEncryptedLibraryPlane pins that an encrypted
// Library's related Entities are read from that Library's own database, where
// its relations live, and that a sealed Library fails the read.
func TestRelatedEntityGroupsReadsTheEncryptedLibraryPlane(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "related-plane-target")
	defer st.Close()
	upsertRelatedEntity(t, st, "entity-plane-maker", media.LibraryID, "maker", "Maker")
	upsertRelatedEntity(t, st, "entity-plane-box", media.LibraryID, "box", "Box")
	relateEntities(t, st, EntityRelation{FromEntityID: "entity-plane-box", Name: "made-by", ToEntityID: "entity-plane-maker"})

	server := LibraryFilter{Revealed: true, AllLibraries: true}
	groups, err := st.RelatedEntityGroups(ctx, "entity-plane-maker", server)
	if err != nil {
		t.Fatalf("RelatedEntityGroups unsealed: %v", err)
	}
	if got := groupIDs(groups); !reflect.DeepEqual(got, map[string][]MediaID{"box": {"entity-plane-box"}}) {
		t.Fatalf("encrypted RelatedEntityGroups = %v, want the box from the Library plane", got)
	}

	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	if _, err := st.RelatedEntityGroups(ctx, "entity-plane-maker", server); err == nil {
		t.Fatal("sealed encrypted RelatedEntityGroups succeeded")
	}
}

// TestRelatedEntityGroupsBySplitsAKindByField pins CORE-2769: a kind named in
// groupFields splits by its text field's value — valueless Entities first as
// the kind's own group, then one group per value in item order — while other
// kinds stay whole.
func TestRelatedEntityGroupsBySplitsAKindByField(t *testing.T) {
	st, lib, _ := relatedFixture(t)
	ctx := context.Background()
	server := LibraryFilter{Revealed: true, AllLibraries: true}
	upsertRelatedEntity(t, st, "entity-maker", lib.ID, "maker", "Maker")
	upsertRelatedEntity(t, st, "entity-place", lib.ID, "place", "Place")
	for _, box := range []struct {
		id    EntityID
		title string
		shape string
	}{{"entity-box-a", "Alpha", "round"}, {"entity-box-b", "Beta", ""}, {"entity-box-c", "Gamma", "square"}, {"entity-box-d", "Delta", "round"}} {
		entity := Entity{ID: box.id, LibraryID: lib.ID, Kind: "box", Title: box.title}
		if box.shape != "" {
			entity.Fields = []EntityField{{Name: "shape", Type: EntityFieldText, Text: box.shape}}
		}
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
		relateEntities(t, st, EntityRelation{FromEntityID: box.id, Name: "made-by", ToEntityID: "entity-maker"})
	}
	relateEntities(t, st, EntityRelation{FromEntityID: "entity-maker", Name: "based-in", ToEntityID: "entity-place"})

	groups, err := st.RelatedEntityGroupsBy(ctx, "entity-maker", server, func(_, kind string) string {
		if kind == "box" {
			return "shape"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	type part struct {
		kind, group string
		ids         []MediaID
	}
	var got []part
	for _, g := range groups {
		p := part{kind: g.Kind, group: g.Group}
		for _, item := range g.Items {
			p.ids = append(p.ids, item.ID)
		}
		got = append(got, p)
	}
	want := []part{
		{"box", "", []MediaID{"entity-box-b"}},
		{"box", "round", []MediaID{"entity-box-a", "entity-box-d"}},
		{"box", "square", []MediaID{"entity-box-c"}},
		{"place", "", []MediaID{"entity-place"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RelatedEntityGroupsBy = %+v, want %+v", got, want)
	}
	plain, err := st.RelatedEntityGroups(ctx, "entity-maker", server)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 2 || len(plain[0].Items) != 4 || plain[0].Group != "" {
		t.Fatalf("RelatedEntityGroups = %+v, want box whole then place", plain)
	}
}

func TestRelatedEntityYearsExceedSQLiteVariableLimit(t *testing.T) {
	st, lib, _ := relatedFixture(t)
	ctx := t.Context()
	upsertRelatedEntity(t, st, "dated-entity", lib.ID, "box", "Dated")
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: "/shelf/dated.flac", Title: "Dated", Container: "flac", Codec: "flac",
		Class: MediaClassAudio, ContentFingerprint: "dated", Year: 2005,
	})
	if err != nil {
		t.Fatal(err)
	}
	relateEntities(t, st, EntityRelation{FromEntityID: "dated-entity", Name: "contains", ToMediaID: media.ID})

	// Exercise the same year-annotation path as RelatedEntityGroups without
	// inserting thousands of unrelated fixture rows.
	items := make([]SourceItem, 32_767)
	for i := range items {
		items[i] = SourceItem{ID: MediaID(strconv.Itoa(i)), Kind: "entity"}
	}
	items[len(items)-1].ID = "dated-entity"
	st.mu.RLock()
	defer st.mu.RUnlock()
	if err := st.annotateEntityYearsLocked(ctx, LibraryFilter{AllLibraries: true}, items); err != nil {
		t.Fatal(err)
	}
	if got := items[len(items)-1].Year; got != 2005 || items[0].Year != 0 {
		t.Fatalf("years = %d, %d; want 0, 2005", items[0].Year, got)
	}
}

func TestUpsertEntityMergingTextIsIndependentOfTrackOrder(t *testing.T) {
	st, lib, _ := relatedFixture(t)
	ctx := t.Context()
	for _, tc := range []struct {
		name   string
		values []string
		want   []string
	}{
		{"short first", []string{"short", "", "long", ""}, []string{"short", "short", "long", "long"}},
		{"long first", []string{"long", "short", "", ""}, []string{"long", "long", "long", "long"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entity := Entity{ID: EntityID("box-" + tc.name), LibraryID: lib.ID, Kind: "box", Title: "Box"}
			var linked []MediaID
			for i, value := range tc.values {
				track := tc.name + "-" + strconv.Itoa(i)
				media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
					LibraryID: lib.ID, Path: "/shelf/" + track + ".flac", Title: track, Container: "flac", Codec: "flac",
					Class: MediaClassAudio, ContentFingerprint: track,
				})
				if err != nil {
					t.Fatal(err)
				}
				entity.Fields = []EntityField{{Name: "other", Type: EntityFieldText, Text: "latest write"}}
				if value != "" {
					entity.Fields = append(entity.Fields, EntityField{Name: "shape", Type: EntityFieldText, Text: value})
				}
				if err := st.UpsertEntityMergingText(ctx, entity, "shape", media.ID); err != nil {
					t.Fatal(err)
				}
				linked = append(linked, media.ID)
				attributes := map[string]string{"@hydration": "render attribute"}
				relation := EntityRelation{
					FromEntityID: entity.ID, Name: "contains", ToMediaID: media.ID, Attributes: attributes,
				}
				if value != "" {
					relation.HydrationText = &EntityTextContribution{Field: "shape", Value: value}
				}
				relateEntities(t, st, relation)
				credits, err := st.EntitiesForMedia(ctx, media.ID, "contains", LibraryFilter{AllLibraries: true})
				if err != nil || len(credits) != 1 || !reflect.DeepEqual(credits[0].Attributes, attributes) {
					t.Fatalf("public relation attributes = %+v, err = %v", credits, err)
				}
				read, err := st.Entity(ctx, entity.ID, LibraryFilter{AllLibraries: true})
				if err != nil {
					t.Fatal(err)
				}
				want := []EntityField{
					{Name: "other", Type: EntityFieldText, Text: "latest write"},
					{Name: "shape", Type: EntityFieldText, Text: tc.want[i]},
				}
				if !reflect.DeepEqual(read.Fields, want) {
					t.Fatalf("fields after %q = %+v, want %+v", value, read.Fields, want)
				}
			}

			// Once the winning track is trashed, it no longer votes when
			// another track hydrates, even with several live tracks.
			winner := slices.Index(tc.values, "long")
			filter := LibraryFilter{AllLibraries: true}
			if err := st.TrashMedia(ctx, linked[winner], filter); err != nil {
				t.Fatal(err)
			}
			entity.Fields = nil
			if err := st.UpsertEntityMergingText(ctx, entity, "shape", linked[len(linked)-1]); err != nil {
				t.Fatal(err)
			}
			read, err := st.Entity(ctx, entity.ID, filter)
			if want := []EntityField{{Name: "shape", Type: EntityFieldText, Text: "short"}}; err != nil || !reflect.DeepEqual(read.Fields, want) {
				t.Fatalf("fields without the trashed track = %+v, err = %v; want %+v", read.Fields, err, want)
			}
		})
	}
}

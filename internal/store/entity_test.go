package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func widgetEntity(id EntityID, library LibraryID) Entity {
	return Entity{
		ID:        id,
		LibraryID: library,
		Kind:      "widget",
		Title:     "Widget " + string(id),
	}
}

func TestUpsertEntityRoundTripsAllFieldTypes(t *testing.T) {
	st := openSeeded(t)
	released := time.Date(2020, 3, 14, 15, 9, 26, 0, time.UTC)
	entity := widgetEntity("entity-one", "library-local")
	entity.Fields = []EntityField{
		{Name: "summary", Type: EntityFieldText, Text: "a sample widget"},
		{Name: "weight", Type: EntityFieldNumber, Number: 4.5},
		{Name: "active", Type: EntityFieldFlag, Flag: true},
		{Name: "released", Type: EntityFieldDate, Date: released},
		{Name: "homepage", Type: EntityFieldURL, Text: "https://example.com"},
	}
	if err := st.UpsertEntity(context.Background(), entity); err != nil {
		t.Fatal(err)
	}

	got, err := st.Entity(context.Background(), "entity-one", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "widget" || got.LibraryID != "library-local" || got.Title != "Widget entity-one" {
		t.Fatalf("entity round trip mismatch: %#v", got)
	}
	if len(got.Fields) != 5 {
		t.Fatalf("fields = %d, want 5: %#v", len(got.Fields), got.Fields)
	}
	byName := map[string]EntityField{}
	for _, field := range got.Fields {
		byName[field.Name] = field
	}
	if f := byName["summary"]; f.Type != EntityFieldText || f.Text != "a sample widget" {
		t.Fatalf("summary field = %#v", f)
	}
	if f := byName["weight"]; f.Type != EntityFieldNumber || f.Number != 4.5 {
		t.Fatalf("weight field = %#v", f)
	}
	if f := byName["active"]; f.Type != EntityFieldFlag || !f.Flag {
		t.Fatalf("active field = %#v", f)
	}
	if f := byName["released"]; f.Type != EntityFieldDate || !f.Date.Equal(released) {
		t.Fatalf("released field = %#v", f)
	}
	if f := byName["homepage"]; f.Type != EntityFieldURL || f.Text != "https://example.com" {
		t.Fatalf("homepage field = %#v", f)
	}

	// Re-upserting replaces the title and the full field set.
	entity.Title = "Renamed Widget"
	entity.Fields = []EntityField{{Name: "summary", Type: EntityFieldText, Text: "updated"}}
	if err := st.UpsertEntity(context.Background(), entity); err != nil {
		t.Fatal(err)
	}
	got, err = st.Entity(context.Background(), "entity-one", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Renamed Widget" || len(got.Fields) != 1 || got.Fields[0].Text != "updated" {
		t.Fatalf("entity after replace = %#v", got)
	}
}

func TestUpsertEntityValidation(t *testing.T) {
	st := openSeeded(t)
	cases := []Entity{
		{LibraryID: "library-local", Kind: "widget", Title: "x"},
		{ID: "entity-a", Kind: "widget", Title: "x"},
		{ID: "entity-a", LibraryID: "library-local", Title: "x"},
		{ID: "entity-a", LibraryID: "library-local", Kind: "widget"},
		{ID: "entity-a", LibraryID: "library-missing", Kind: "widget", Title: "x"},
		{ID: "entity-a", LibraryID: "library-local", Kind: "widget", Title: "x",
			Fields: []EntityField{{Name: "v", Type: "mystery"}}},
		{ID: "entity-a", LibraryID: "library-local", Kind: "widget", Title: "x",
			Fields: []EntityField{{Name: "v", Type: EntityFieldText}, {Name: "v", Type: EntityFieldFlag}}},
		{ID: "entity-a", LibraryID: "library-local", Kind: "widget", Title: "x",
			Fields: []EntityField{{Name: "v", Type: EntityFieldDate}}},
	}
	for i, entity := range cases {
		if err := st.UpsertEntity(context.Background(), entity); err == nil {
			t.Fatalf("case %d: invalid entity accepted: %#v", i, entity)
		}
	}
}

func TestEntityRevealFiltering(t *testing.T) {
	st := openSeeded(t)
	if err := st.UpsertEntity(context.Background(), widgetEntity("entity-hidden", "library-archive")); err != nil {
		t.Fatal(err)
	}

	hidden := LibraryFilter{AllLibraries: true}
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	if _, err := st.Entity(context.Background(), "entity-hidden", hidden); err == nil {
		t.Fatal("restricted entity visible without reveal")
	}
	if _, err := st.Entity(context.Background(), "entity-hidden", revealed); err != nil {
		t.Fatal(err)
	}

	// The restricted Library itself reads as not found to an unrevealed scope.
	if _, err := st.EntitiesByKind(context.Background(), "library-archive", "widget", hidden, EntityKindOptions{}, 0, 0); err == nil {
		t.Fatal("restricted library listable without reveal")
	}
	entities, err := st.EntitiesByKind(context.Background(), "library-archive", "widget", revealed, EntityKindOptions{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 1 || entities[0].ID != "entity-hidden" {
		t.Fatalf("entities = %#v, want entity-hidden", entities)
	}

	// AllowedIDs narrows scope the same way it does for Media.
	scoped := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true}
	if _, err := st.Entity(context.Background(), "entity-hidden", scoped); err == nil {
		t.Fatal("entity outside allowed libraries visible")
	}
}

func TestEntitiesByKindOrdersByTitle(t *testing.T) {
	st := openSeeded(t)
	beta := widgetEntity("entity-beta", "library-local")
	beta.Title = "Beta"
	alpha := widgetEntity("entity-alpha", "library-local")
	alpha.Title = "Alpha"
	for _, entity := range []Entity{beta, alpha} {
		if err := st.UpsertEntity(context.Background(), entity); err != nil {
			t.Fatal(err)
		}
	}
	other := widgetEntity("entity-other-kind", "library-local")
	other.Kind = "gadget"
	if err := st.UpsertEntity(context.Background(), other); err != nil {
		t.Fatal(err)
	}

	entities, err := st.EntitiesByKind(context.Background(), "library-local", "widget", LibraryFilter{AllLibraries: true}, EntityKindOptions{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 2 || entities[0].Title != "Alpha" || entities[1].Title != "Beta" {
		t.Fatalf("entities = %#v, want Alpha then Beta", entities)
	}
}

func TestEntityMediaRelations(t *testing.T) {
	st := openSeeded(t)
	if err := st.UpsertEntity(context.Background(), widgetEntity("entity-a", "library-local")); err != nil {
		t.Fatal(err)
	}
	relation := EntityRelation{
		FromEntityID: "entity-a",
		Name:         "linked",
		ToMediaID:    "media-alpha",
		Attributes:   map[string]string{"role": "primary"},
	}
	if err := st.UpsertEntityRelation(context.Background(), relation); err != nil {
		t.Fatal(err)
	}

	related, err := st.EntitiesForMedia(context.Background(), "media-alpha", "linked", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 1 || related[0].Entity.ID != "entity-a" {
		t.Fatalf("related = %#v, want entity-a", related)
	}
	if related[0].Attributes["role"] != "primary" {
		t.Fatalf("attributes = %#v, want role=primary", related[0].Attributes)
	}

	// Re-upserting the same row replaces attributes instead of duplicating.
	relation.Attributes = map[string]string{"role": "secondary"}
	if err := st.UpsertEntityRelation(context.Background(), relation); err != nil {
		t.Fatal(err)
	}
	related, err = st.EntitiesForMedia(context.Background(), "media-alpha", "linked", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 1 || related[0].Attributes["role"] != "secondary" {
		t.Fatalf("related after re-upsert = %#v", related)
	}

	// An unknown relation name returns an empty list, not an error.
	none, err := st.EntitiesForMedia(context.Background(), "media-alpha", "unrelated", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("unexpected related entities: %#v", none)
	}

	items, err := st.MediaForEntity(context.Background(), "entity-a", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "media-alpha" || items[0].Kind != "media" {
		t.Fatalf("items = %#v, want media-alpha", items)
	}
}

func TestEntityRelationRevealFiltering(t *testing.T) {
	st := openSeeded(t)
	// One entity in the open Library, one in the restricted one; both relate
	// to Media on the other side of the reveal boundary.
	if err := st.UpsertEntity(context.Background(), widgetEntity("entity-open", "library-local")); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(context.Background(), widgetEntity("entity-hidden", "library-archive")); err != nil {
		t.Fatal(err)
	}
	relations := []EntityRelation{
		{FromEntityID: "entity-open", Name: "linked", ToMediaID: "media-alpha"},
		{FromEntityID: "entity-open", Name: "linked", ToMediaID: "media-archive"},
		{FromEntityID: "entity-hidden", Name: "linked", ToMediaID: "media-alpha"},
	}
	for _, relation := range relations {
		if err := st.UpsertEntityRelation(context.Background(), relation); err != nil {
			t.Fatal(err)
		}
	}
	hidden := LibraryFilter{AllLibraries: true}
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	// Restricted entity never reaches an unrevealed caller, even via a
	// visible Media item.
	related, err := st.EntitiesForMedia(context.Background(), "media-alpha", "linked", hidden)
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 1 || related[0].Entity.ID != "entity-open" {
		t.Fatalf("related = %#v, want only entity-open", related)
	}
	related, err = st.EntitiesForMedia(context.Background(), "media-alpha", "linked", revealed)
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 2 {
		t.Fatalf("related revealed = %#v, want both entities", related)
	}

	// Restricted Media never reaches an unrevealed caller via its relations.
	items, err := st.MediaForEntity(context.Background(), "entity-open", hidden)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "media-alpha" {
		t.Fatalf("items = %#v, want only media-alpha", items)
	}
	items, err = st.MediaForEntity(context.Background(), "entity-open", revealed)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items revealed = %#v, want both media", items)
	}

	// Relations to restricted Media on a restricted entity stay unreachable.
	if _, err := st.MediaForEntity(context.Background(), "entity-hidden", hidden); err == nil {
		t.Fatal("restricted entity media listable without reveal")
	}
}

func TestEntityRelationRowsFiltersBeforePagination(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	for _, entity := range []Entity{
		{ID: "entity-page-source", LibraryID: "library-local", Kind: "container", Title: "Source"},
		{ID: "entity-page-visible-a", LibraryID: "library-local", Kind: "item", Title: "Visible A"},
		{ID: "entity-page-hidden-a", LibraryID: "library-archive", Kind: "item", Title: "Hidden A"},
		{ID: "entity-page-visible-b", LibraryID: "library-local", Kind: "item", Title: "Visible B"},
		{ID: "entity-page-hidden-b", LibraryID: "library-archive", Kind: "item", Title: "Hidden B"},
	} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range []EntityID{
		"entity-page-hidden-a",
		"entity-page-visible-a",
		"entity-page-hidden-b",
		"entity-page-visible-b",
	} {
		order := i + 1
		if err := st.UpsertEntityRelation(ctx, EntityRelation{
			FromEntityID: "entity-page-source",
			Name:         "children",
			ToEntityID:   id,
			SortOrder:    &order,
		}); err != nil {
			t.Fatal(err)
		}
	}

	filter := LibraryFilter{AllLibraries: true}
	first, err := st.EntityRelationRows(ctx, "entity-page-source", "children", filter, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.EntityRelationRows(ctx, "entity-page-source", "children", filter, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].ToEntity == nil || first[0].ToEntity.ID != "entity-page-visible-a" {
		t.Fatalf("first visible page = %#v, want visible-a", first)
	}
	if len(second) != 1 || second[0].ToEntity == nil || second[0].ToEntity.ID != "entity-page-visible-b" {
		t.Fatalf("second visible page = %#v, want visible-b", second)
	}
	all, err := st.EntityRelationRows(ctx, "entity-page-source", "children", filter, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ToEntity.ID != first[0].ToEntity.ID || all[1].ToEntity.ID != second[0].ToEntity.ID {
		t.Fatalf("paged rows (%#v, %#v) differ from unpaged visible order %#v", first, second, all)
	}
}

func TestMediaRelationsFiltersCertificationBeforePagination(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	libraryID, ids := seedCertifiedLibrary(t, st, "relations", false, "high", "low", "mid")
	parent, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          libraryID,
		Path:               "/relations/parent.mp4",
		Title:              "Parent",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "relations-parent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, parent.ID, []MediaField{{Name: "certification", Type: EntityFieldText, Text: "low"}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []MediaID{ids["high"], ids["low"], ids["mid"]} {
		if err := st.LinkMediaRelation(ctx, id, MediaRelationExtraOf, parent.ID); err != nil {
			t.Fatal(err)
		}
	}
	filter := LibraryFilter{
		AllowedIDs:    []LibraryID{libraryID},
		Certification: []CertificationConstraint{{PluginID: "relations", Allowed: []string{"low", "mid"}}},
	}
	first, err := st.MediaRelationsOf(ctx, parent.ID, MediaRelationExtraOf, filter, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.MediaRelationsOf(ctx, parent.ID, MediaRelationExtraOf, filter, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("pages = %#v / %#v, want one visible relation each", first, second)
	}
	if first[0].ID == ids["high"] || second[0].ID == ids["high"] || first[0].ID == second[0].ID {
		t.Fatalf("certification-filtered pages = %#v / %#v", first, second)
	}
}

func TestEntityToEntityRelations(t *testing.T) {
	st := openSeeded(t)
	parent := widgetEntity("entity-parent", "library-local")
	parent.Kind = "bundle"
	if err := st.UpsertEntity(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(context.Background(), widgetEntity("entity-child", "library-local")); err != nil {
		t.Fatal(err)
	}
	relation := EntityRelation{
		FromEntityID: "entity-parent",
		Name:         "linked",
		ToEntityID:   "entity-child",
		Attributes:   map[string]string{"position": "1"},
	}
	if err := st.UpsertEntityRelation(context.Background(), relation); err != nil {
		t.Fatal(err)
	}
	// Idempotent re-upsert with fresh attributes succeeds (unique per
	// from+name+target).
	relation.Attributes = map[string]string{"position": "2"}
	if err := st.UpsertEntityRelation(context.Background(), relation); err != nil {
		t.Fatal(err)
	}

	// Invalid shapes are rejected.
	bad := []EntityRelation{
		{FromEntityID: "entity-parent", Name: "linked"},
		{FromEntityID: "entity-parent", Name: "linked", ToEntityID: "entity-child", ToMediaID: "media-alpha"},
		{FromEntityID: "entity-parent", ToEntityID: "entity-child"},
		{FromEntityID: "entity-missing", Name: "linked", ToEntityID: "entity-child"},
		{FromEntityID: "entity-parent", Name: "linked", ToEntityID: "entity-missing"},
		{FromEntityID: "entity-parent", Name: "linked", ToMediaID: "media-missing"},
	}
	for i, relation := range bad {
		if err := st.UpsertEntityRelation(context.Background(), relation); err == nil {
			t.Fatalf("case %d: invalid relation accepted: %#v", i, relation)
		}
	}
}

func TestMediaForEntityOmitsTrashedMedia(t *testing.T) {
	st := openSeeded(t)
	if err := st.UpsertEntity(context.Background(), widgetEntity("entity-a", "library-local")); err != nil {
		t.Fatal(err)
	}
	relation := EntityRelation{FromEntityID: "entity-a", Name: "linked", ToMediaID: "media-alpha"}
	if err := st.UpsertEntityRelation(context.Background(), relation); err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	if err := st.TrashMedia(context.Background(), "media-alpha", filter); err != nil {
		t.Fatal(err)
	}
	items, err := st.MediaForEntity(context.Background(), "entity-a", filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("trashed media still related: %#v", items)
	}
	if err := st.RestoreMedia(context.Background(), "media-alpha", filter); err != nil {
		t.Fatal(err)
	}
	items, err = st.MediaForEntity(context.Background(), "entity-a", filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("restored media missing from relations: %#v", items)
	}
}

func TestEntityItemsSourceRevealFiltered(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	visible := Entity{ID: "entity-visible", LibraryID: "library-local", Kind: "widget", Title: "Visible Widget"}
	hidden := Entity{ID: "entity-hidden", LibraryID: "library-archive", Kind: "widget", Title: "Hidden Widget"}
	for _, entity := range []Entity{visible, hidden} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}

	items, err := st.SourceItems(ctx, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "entity-items", EntityKind: "widget"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "entity-visible" || items[0].Kind != "entity" {
		t.Fatalf("unrevealed entity items = %#v, want only the visible widget", items)
	}

	items, err = st.SourceItems(ctx, LibraryFilter{Revealed: true, AllLibraries: true}, SourceQuery{Kind: "entity-items", EntityKind: "widget"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("revealed entity items = %#v, want both widgets", items)
	}

	items, err = st.SourceItems(ctx, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "entity-items"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("entity items without a kind = %#v, want empty", items)
	}
}

func TestRelatedEntitiesTraversal(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	parent := Entity{ID: "entity-parent", LibraryID: "library-local", Kind: "widget", Title: "Parent"}
	child := Entity{ID: "entity-child", LibraryID: "library-local", Kind: "widget", Title: "Child"}
	hidden := Entity{ID: "entity-secret", LibraryID: "library-archive", Kind: "widget", Title: "Secret"}
	for _, entity := range []Entity{parent, child, hidden} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []EntityID{"entity-child", "entity-secret"} {
		if err := st.UpsertEntityRelation(ctx, EntityRelation{
			FromEntityID: "entity-parent",
			Name:         "linked",
			ToEntityID:   target,
			Attributes:   map[string]string{"position": "1"},
		}); err != nil {
			t.Fatal(err)
		}
	}

	related, err := st.RelatedEntities(ctx, "entity-parent", "linked", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 1 || related[0].Entity.ID != "entity-child" {
		t.Fatalf("unrevealed related = %#v, want only the visible child", related)
	}
	if related[0].Attributes["position"] != "1" {
		t.Fatalf("attributes = %#v, want position=1", related[0].Attributes)
	}

	related, err = st.RelatedEntities(ctx, "entity-parent", "linked", LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 2 {
		t.Fatalf("revealed related = %#v, want both targets", related)
	}
}

func TestEntitiesByKindPages(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	for _, title := range []string{"Alpha", "Beta", "Gamma"} {
		if err := st.UpsertEntity(ctx, Entity{
			ID:        EntityID("entity-" + title),
			LibraryID: "library-local",
			Kind:      "widget",
			Title:     title,
		}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := st.EntitiesByKind(ctx, "library-local", "widget", LibraryFilter{AllLibraries: true}, EntityKindOptions{}, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Title != "Alpha" || page[1].Title != "Beta" {
		t.Fatalf("first page = %v", page)
	}
	page, err = st.EntitiesByKind(ctx, "library-local", "widget", LibraryFilter{AllLibraries: true}, EntityKindOptions{}, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Title != "Gamma" {
		t.Fatalf("second page = %v", page)
	}
}

// TestLoadEntityFieldsChunksLargeIDSet exercises loadEntityFields with more
// ids than entityIDBatchSize (600 > 500) to prove the IN-list chunking
// aggregates every field to the right entity with no drops or duplicates. The
// unchunked version built one IN(...) per id, so on large libraries it would
// exceed SQLite's 32766 bind-variable cap; this guards that the batched path
// stays correct across the batch boundary.
func TestLoadEntityFieldsChunksLargeIDSet(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const count = 600
	if count <= entityIDBatchSize {
		t.Fatalf("test must exceed batch size %d, got %d", entityIDBatchSize, count)
	}
	ids := make([]EntityID, count)
	for i := 0; i < count; i++ {
		id := EntityID(fmt.Sprintf("entity-bulk-%04d", i))
		ids[i] = id
		entity := widgetEntity(id, "library-local")
		// Two fields per entity so we can assert both come back, and so the
		// total field count (count*2) crosses the batch boundary too.
		entity.Fields = []EntityField{
			{Name: "summary", Type: EntityFieldText, Text: fmt.Sprintf("summary-%04d", i)},
			{Name: "weight", Type: EntityFieldNumber, Number: float64(i)},
		}
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}

	got := make(map[EntityID][]EntityField, count)
	st.mu.RLock()
	err := st.loadEntityFields(ctx, ids, func(id EntityID, field EntityField) {
		got[id] = append(got[id], field)
	})
	st.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != count {
		t.Fatalf("entities with fields = %d, want %d", len(got), count)
	}
	for i := 0; i < count; i++ {
		id := EntityID(fmt.Sprintf("entity-bulk-%04d", i))
		fields := got[id]
		if len(fields) != 2 {
			t.Fatalf("%s fields = %d, want 2 (drop/duplicate across chunk boundary): %#v", id, len(fields), fields)
		}
		byName := map[string]EntityField{}
		for _, f := range fields {
			byName[f.Name] = f
		}
		if f := byName["summary"]; f.Type != EntityFieldText || f.Text != fmt.Sprintf("summary-%04d", i) {
			t.Fatalf("%s summary = %#v", id, f)
		}
		if f := byName["weight"]; f.Type != EntityFieldNumber || f.Number != float64(i) {
			t.Fatalf("%s weight = %#v", id, f)
		}
	}
}

func TestSourceItemsEntityMediaPagingAndReveal(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.UpsertEntity(ctx, widgetEntity("entity-a", "library-local")); err != nil {
		t.Fatal(err)
	}
	// Two extra local Media so paging has rows beyond the seeded pair.
	for _, spec := range []struct{ path, title, fingerprint string }{
		{"/media/beta.mp4", "Beta", "sha256:beta"},
		{"/media/gamma.mp4", "Gamma", "sha256:gamma"},
	} {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          "library-local",
			Path:               spec.path,
			Title:              spec.title,
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: spec.fingerprint,
		})
		if err != nil {
			t.Fatal(err)
		}
		relation := EntityRelation{FromEntityID: "entity-a", Name: "linked", ToMediaID: media.ID}
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []MediaID{"media-alpha", "media-archive"} {
		relation := EntityRelation{FromEntityID: "entity-a", Name: "linked", ToMediaID: id}
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatal(err)
		}
	}

	// Unrevealed: the restricted archive Media never appears.
	items, err := st.SourceItems(ctx, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "entity-media", EntityID: "entity-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("unrevealed items = %#v, want 3 local media", items)
	}
	for _, item := range items {
		if item.ID == "media-archive" {
			t.Fatalf("restricted media leaked unrevealed: %#v", items)
		}
		if item.Kind != "media" {
			t.Fatalf("item kind = %q, want media", item.Kind)
		}
	}

	// Revealed: all four related Media, title-ordered.
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}
	all, err := st.SourceItems(ctx, revealed, SourceQuery{Kind: "entity-media", EntityID: "entity-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("revealed items = %#v, want 4", all)
	}

	// Paging walks the same ordering with the standard clamps.
	page, err := st.SourceItems(ctx, revealed, SourceQuery{Kind: "entity-media", EntityID: "entity-a", Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID != all[1].ID || page[1].ID != all[2].ID {
		t.Fatalf("page = %#v, want %v then %v", page, all[1].ID, all[2].ID)
	}

	// An empty EntityID yields nothing, like the other under-specified kinds.
	none, err := st.SourceItems(ctx, revealed, SourceQuery{Kind: "entity-media"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("entity-media without entity returned %#v", none)
	}
}

func TestSourceItemsEntityMediaHiddenEntityErrors(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.UpsertEntity(ctx, widgetEntity("entity-hidden", "library-archive")); err != nil {
		t.Fatal(err)
	}
	relation := EntityRelation{FromEntityID: "entity-hidden", Name: "linked", ToMediaID: "media-alpha"}
	if err := st.UpsertEntityRelation(ctx, relation); err != nil {
		t.Fatal(err)
	}
	// A restricted entity errors unrevealed — same contract as MediaForEntity.
	if _, err := st.SourceItems(ctx, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "entity-media", EntityID: "entity-hidden"}); err == nil {
		t.Fatal("restricted entity's media listable without reveal")
	}
	items, err := st.SourceItems(ctx, LibraryFilter{Revealed: true, AllLibraries: true}, SourceQuery{Kind: "entity-media", EntityID: "entity-hidden"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "media-alpha" {
		t.Fatalf("revealed items = %#v, want media-alpha", items)
	}
}

// insertPluginLibrary adds a Library bound to one plugin — the construction
// the kind-collision verdict rests on (a Library binds at most one plugin).
func insertPluginLibrary(t *testing.T, st *Store, id LibraryID, pluginID string) {
	t.Helper()
	if _, err := st.catalogW.ExecContext(context.Background(),
		"INSERT INTO library(id, title, plugin_id, is_restricted) VALUES (?, ?, ?, 0)",
		id, "Library "+string(id), pluginID,
	); err != nil {
		t.Fatal(err)
	}
}

// Pins the kind-collision verdict: entity.kind stores the bare declared kind
// name, and that is safe because Entities are Library-scoped and a Library
// binds at most one plugin — two plugins both declaring "studio" never
// produce ambiguous rows, and Library-scoped reads never mix them.
func TestEntityKindNamesAreLibraryScoped(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	insertPluginLibrary(t, st, "library-films", "filmtv")
	insertPluginLibrary(t, st, "library-tunes", "music")
	for _, entity := range []Entity{
		{ID: "entity-film-studio", LibraryID: "library-films", Kind: "studio", Title: "Mossbank Films"},
		{ID: "entity-tune-studio", LibraryID: "library-tunes", Kind: "studio", Title: "Mossbank Records"},
	} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}

	// Per-Library reads of the same bare kind never mix the two plugins'
	// instances.
	films, err := st.EntitiesByKind(ctx, "library-films", "studio", LibraryFilter{AllLibraries: true}, EntityKindOptions{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(films) != 1 || films[0].ID != "entity-film-studio" {
		t.Fatalf("film studios = %#v, want only entity-film-studio", films)
	}
	tunes, err := st.EntitiesByKind(ctx, "library-tunes", "studio", LibraryFilter{AllLibraries: true}, EntityKindOptions{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tunes) != 1 || tunes[0].ID != "entity-tune-studio" {
		t.Fatalf("tune studios = %#v, want only entity-tune-studio", tunes)
	}

	// The entity-items Source separates plugins through its Library scope.
	items, err := st.SourceItems(ctx, LibraryFilter{AllLibraries: true}, SourceQuery{
		Kind: "entity-items", EntityKind: "studio", LibraryID: "library-films",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "entity-film-studio" {
		t.Fatalf("library-scoped entity-items = %#v, want only entity-film-studio", items)
	}
}

func TestUpsertEntityRelationOneCardinalityReplaces(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	for _, id := range []EntityID{"entity-a", "entity-b"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, "library-local")); err != nil {
			t.Fatal(err)
		}
	}

	// Default (many): both source entities keep their rows.
	for _, from := range []EntityID{"entity-a", "entity-b"} {
		if err := st.UpsertEntityRelation(ctx, EntityRelation{
			FromEntityID: from, Name: "tagged", ToMediaID: "media-alpha",
		}); err != nil {
			t.Fatal(err)
		}
	}
	tagged, err := st.EntitiesForMedia(ctx, "media-alpha", "tagged", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tagged) != 2 {
		t.Fatalf("many-cardinality rows = %#v, want both entities", tagged)
	}

	// One: the second source entity replaces the first on the same Media.
	if err := st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "entity-a", Name: "member-of", ToMediaID: "media-alpha", Cardinality: "one",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "entity-b", Name: "member-of", ToMediaID: "media-alpha", Cardinality: "one",
	}); err != nil {
		t.Fatal(err)
	}
	members, err := st.EntitiesForMedia(ctx, "media-alpha", "member-of", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].Entity.ID != "entity-b" {
		t.Fatalf("one-cardinality rows = %#v, want only entity-b", members)
	}
	// The replacement is per relation name — the "tagged" rows survive.
	tagged, err = st.EntitiesForMedia(ctx, "media-alpha", "tagged", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tagged) != 2 {
		t.Fatalf("other relation names disturbed: %#v", tagged)
	}

	// Re-upserting the same source is idempotent, never an error.
	if err := st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "entity-b", Name: "member-of", ToMediaID: "media-alpha", Cardinality: "one",
	}); err != nil {
		t.Fatal(err)
	}

	// The cardinality vocabulary is closed.
	err = st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "entity-a", Name: "member-of", ToMediaID: "media-alpha", Cardinality: "several",
	})
	if err == nil || !strings.Contains(err.Error(), "cardinality") {
		t.Fatalf("unknown cardinality error = %v", err)
	}
}

func TestMediaForEntityOrdersBySortOrderThenTitle(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.UpsertEntity(ctx, widgetEntity("entity-a", "library-local")); err != nil {
		t.Fatal(err)
	}
	// Three extra Media whose titles invert the intended play order, plus the
	// seeded media-alpha left unordered.
	ids := map[string]MediaID{}
	for _, spec := range []struct{ title, fingerprint string }{
		{"Zeta", "sha256:zeta"}, {"Yankee", "sha256:yankee"}, {"Xray", "sha256:xray"},
	} {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          "library-local",
			Path:               "/media/" + spec.title + ".mp4",
			Title:              spec.title,
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: spec.fingerprint,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[spec.title] = media.ID
	}
	link := func(media MediaID, order *int) {
		t.Helper()
		if err := st.UpsertEntityRelation(ctx, EntityRelation{
			FromEntityID: "entity-a", Name: "linked", ToMediaID: media, SortOrder: order,
		}); err != nil {
			t.Fatal(err)
		}
	}
	one, two := 1, 2
	link(ids["Zeta"], &one)
	link(ids["Yankee"], &two)
	link(ids["Xray"], nil)
	link("media-alpha", nil)

	items, err := st.MediaForEntity(ctx, "entity-a", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("items = %#v, want 4", items)
	}
	// Ordered rows first in sort order, then unordered rows title-ordered.
	want := []MediaID{ids["Zeta"], ids["Yankee"], "media-alpha", ids["Xray"]}
	for i, id := range want {
		if items[i].ID != id {
			t.Fatalf("items[%d] = %q, want %q (full: %#v)", i, items[i].ID, id, items)
		}
	}

	// Re-upserting moves a row's position instead of duplicating it.
	zero := 0
	link(ids["Yankee"], &zero)
	items, err = st.MediaForEntity(ctx, "entity-a", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 || items[0].ID != ids["Yankee"] || items[1].ID != ids["Zeta"] {
		t.Fatalf("items after reorder = %#v", items)
	}
}

func TestPurgeOrphanEntities(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	// The widget kind opts into OrphanPolicy:"gc" so its orphans are collectible.
	registerWidgetKindPolicy(t, st)
	for _, id := range []EntityID{"entity-linked", "entity-orphan", "entity-trash-linked", "entity-parent"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, "library-local")); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "entity-linked", Name: "linked", ToMediaID: "media-alpha",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "entity-trash-linked", Name: "linked", ToMediaID: "media-archive",
	}); err != nil {
		t.Fatal(err)
	}
	// entity-parent only points at entity-linked — an entity→entity relation
	// keeps both alive.
	if err := st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "entity-parent", Name: "contains", ToEntityID: "entity-linked",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.TrashMedia(ctx, "media-archive", LibraryFilter{Revealed: true, AllLibraries: true}); err != nil {
		t.Fatal(err)
	}

	// Only the relation-less entity purges; relations to trashed Media (and
	// entity→entity relations) keep their entities alive.
	purged, err := st.PurgeOrphanEntities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(purged) != 1 || purged[0] != "entity-orphan" {
		t.Fatalf("purged = %#v, want only entity-orphan", purged)
	}
	if _, err := st.Entity(ctx, "entity-orphan", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("purged entity still readable")
	}
	for _, id := range []EntityID{"entity-linked", "entity-trash-linked", "entity-parent"} {
		if _, err := st.Entity(ctx, id, LibraryFilter{AllLibraries: true}); err != nil {
			t.Fatalf("entity %s did not survive: %v", id, err)
		}
	}

	// A second sweep with nothing to do purges nothing.
	purged, err = st.PurgeOrphanEntities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(purged) != 0 {
		t.Fatalf("idle sweep purged %#v", purged)
	}

	// Once the trashed Media purges (cascading its relation rows), the next
	// sweep collects the entity it was holding alive.
	if _, err := st.PurgeTrashedBefore(ctx, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	purged, err = st.PurgeOrphanEntities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(purged) != 1 || purged[0] != "entity-trash-linked" {
		t.Fatalf("post-media-purge sweep = %#v, want entity-trash-linked", purged)
	}
}

// TestDepthAwareMediaCount pins ADR-0027 D2 §1's depth-aware counts: a
// hierarchical Entity's Media count rolls up its descendants' counts through the
// self-referential parent relation. The hierarchy is three levels —
// root → mid → leaf — each linked to its own distinct Media; the generic count
// must sum the subtree, deduplicate Media shared across levels, and stay flat
// (own-Media only) for a leaf. Domain-neutral on purpose: the kind is the test's
// "bundle", the parent edge a plugin-named relation.
func TestDepthAwareMediaCount(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const lib = LibraryID("library-local")
	const parentRel = "parent-bundle"

	bundle := func(id EntityID, parent EntityID) {
		e := widgetEntity(id, lib)
		e.Kind = "bundle"
		if err := st.UpsertEntity(ctx, e); err != nil {
			t.Fatal(err)
		}
		if parent != "" {
			if err := st.UpsertEntityRelation(ctx, EntityRelation{
				FromEntityID: id, Name: parentRel, ToEntityID: parent, Cardinality: "one",
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	importMedia := func(slug string) MediaID {
		m, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          lib,
			Path:               "/media/" + slug + ".mp4",
			Title:              slug,
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: "sha256:" + slug,
		})
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	link := func(entity EntityID, media MediaID) {
		if err := st.UpsertEntityRelation(ctx, EntityRelation{
			FromEntityID: entity, Name: "linked", ToMediaID: media,
		}); err != nil {
			t.Fatal(err)
		}
	}

	bundle("root", "")
	bundle("mid", "root")
	bundle("leaf", "mid")
	rootMedia := importMedia("root-media")
	midMedia := importMedia("mid-media")
	leafMedia := importMedia("leaf-media")
	shared := importMedia("shared-media")
	link("root", rootMedia)
	link("mid", midMedia)
	link("leaf", leafMedia)
	// shared sits on both leaf and root — it must be counted once at the root.
	link("leaf", shared)
	link("root", shared)

	filter := LibraryFilter{AllLibraries: true}

	// Leaf: flat — its own two Media (leaf-media + shared), no descendants.
	if got, err := st.DepthAwareMediaCount(ctx, "leaf", parentRel, filter); err != nil {
		t.Fatal(err)
	} else if got != 2 {
		t.Fatalf("leaf depth-aware count = %d, want 2", got)
	}
	// Mid: own midMedia + leaf's {leafMedia, shared} = 3 distinct.
	if got, err := st.DepthAwareMediaCount(ctx, "mid", parentRel, filter); err != nil {
		t.Fatal(err)
	} else if got != 3 {
		t.Fatalf("mid depth-aware count = %d, want 3", got)
	}
	// Root: {rootMedia, shared} + mid's midMedia + leaf's {leafMedia, shared}
	// = {root, mid, leaf, shared} = 4 distinct (shared dedup'd).
	if got, err := st.DepthAwareMediaCount(ctx, "root", parentRel, filter); err != nil {
		t.Fatal(err)
	} else if got != 4 {
		t.Fatalf("root depth-aware count = %d, want 4", got)
	}
	// An empty parent relation degrades to the flat own-count (no edges climbed).
	if got, err := st.DepthAwareMediaCount(ctx, "root", "", filter); err != nil {
		t.Fatal(err)
	} else if got != 2 {
		t.Fatalf("root flat count = %d, want 2 (rootMedia + shared)", got)
	}
}

// TestDepthAwareMediaCountRespectsReveal pins that the rollup never counts Media
// in a Library the filter hides, and that a hidden context Entity errors like a
// missing one (the Reveal contract). A child in the restricted Library carries
// Media that must be invisible to an unrevealed caller.
func TestDepthAwareMediaCountRespectsReveal(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const parentRel = "parent-bundle"

	root := widgetEntity("rev-root", "library-local")
	root.Kind = "bundle"
	if err := st.UpsertEntity(ctx, root); err != nil {
		t.Fatal(err)
	}
	hiddenChild := widgetEntity("rev-child", "library-archive")
	hiddenChild.Kind = "bundle"
	if err := st.UpsertEntity(ctx, hiddenChild); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "rev-child", Name: parentRel, ToEntityID: "rev-root", Cardinality: "one",
	}); err != nil {
		t.Fatal(err)
	}
	rootMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/rev-root.mp4", Title: "rev-root",
		Container: "mp4", Codec: "h264", ContentFingerprint: "sha256:rev-root",
	})
	if err != nil {
		t.Fatal(err)
	}
	hiddenMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-archive", Path: "/media/rev-hidden.mp4", Title: "rev-hidden",
		Container: "mp4", Codec: "h264", ContentFingerprint: "sha256:rev-hidden",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "rev-root", Name: "linked", ToMediaID: rootMedia.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "rev-child", Name: "linked", ToMediaID: hiddenMedia.ID,
	}); err != nil {
		t.Fatal(err)
	}

	// Unrevealed: the restricted child and its Media are invisible — only the
	// root's own Media counts (the descendant is dropped from the subtree).
	if got, err := st.DepthAwareMediaCount(ctx, "rev-root", parentRel, LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatal(err)
	} else if got != 1 {
		t.Fatalf("unrevealed root count = %d, want 1", got)
	}
	// Revealed: the descendant and its Media join the rollup.
	if got, err := st.DepthAwareMediaCount(ctx, "rev-root", parentRel, LibraryFilter{Revealed: true, AllLibraries: true}); err != nil {
		t.Fatal(err)
	} else if got != 2 {
		t.Fatalf("revealed root count = %d, want 2", got)
	}
	// A hidden context Entity is indistinguishable from a missing one.
	if _, err := st.DepthAwareMediaCount(ctx, "rev-child", parentRel, LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("expected hidden context entity to error like a missing one")
	}
}

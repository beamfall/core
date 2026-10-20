package store

import (
	"context"
	"testing"
	"time"
)

func registerWidgetIndexes(t *testing.T, st *Store) {
	t.Helper()
	err := st.RegisterEntityFieldIndexes(context.Background(), []EntityFieldIndex{
		{Kind: "widget", Field: "color", Type: EntityFieldText, Filterable: true},
		{Kind: "widget", Field: "weight", Type: EntityFieldNumber, Sortable: true},
		{Kind: "widget", Field: "summary", Type: EntityFieldText, Searchable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The widget kind opts into the KIND-level seams the FTS/GC tests exercise:
	// full-text search (so its instances surface in SearchEntities) and orphan
	// GC (so PurgeOrphanEntities collects its orphans).
	registerWidgetKindPolicy(t, st)
}

// registerWidgetKindPolicy opts the "widget" kind into the KIND-level seams
// (FullTextSearchable, OrphanPolicy:"gc", CursorPaginated). Split out so tests
// that need the policy without the per-field indexes can register it alone.
func registerWidgetKindPolicy(t *testing.T, st *Store) {
	t.Helper()
	err := st.RegisterEntityKindPolicies(context.Background(), []EntityKindPolicy{
		{Kind: "widget", FullTextSearchable: true, OrphanGC: true, CursorPaginated: true},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRegisterEntityFieldIndexesValidation(t *testing.T) {
	st := openSeeded(t)
	cases := []EntityFieldIndex{
		{Kind: "Widget", Field: "color", Type: EntityFieldText, Filterable: true},                 // kind not a slug
		{Kind: "widget", Field: "color'; DROP TABLE entity;--", Type: EntityFieldText},            // field not a slug
		{Kind: "widget", Field: "color", Type: EntityFieldType("blob"), Filterable: true},         // unknown type
		{Kind: "widget", Field: "weight", Type: EntityFieldNumber, Searchable: true},              // searchable non-text
		{Kind: "widget", Field: "released", Type: EntityFieldDate, Searchable: true},              // searchable non-text
		{Kind: "wid get", Field: "color", Type: EntityFieldText, Sortable: true},                  // kind with space
		{Kind: "widget", Field: `color" ON entity(id);--`, Type: EntityFieldText, Sortable: true}, // quoted injection
	}
	for i, spec := range cases {
		if err := st.RegisterEntityFieldIndexes(context.Background(), []EntityFieldIndex{spec}); err == nil {
			t.Fatalf("case %d: invalid spec accepted: %#v", i, spec)
		}
	}
	// Conflicting types for the same (kind, field) are rejected.
	err := st.RegisterEntityFieldIndexes(context.Background(), []EntityFieldIndex{
		{Kind: "widget", Field: "color", Type: EntityFieldText, Filterable: true},
		{Kind: "widget", Field: "color", Type: EntityFieldNumber, Sortable: true},
	})
	if err == nil {
		t.Fatal("conflicting field types accepted")
	}
}

func TestRegisterEntityFieldIndexesCreatesIndexesIdempotently(t *testing.T) {
	st := openSeeded(t)
	registerWidgetIndexes(t, st)
	// Second registration (a restart) must not error or duplicate.
	registerWidgetIndexes(t, st)

	for _, name := range []string{
		"idx_entity_field_text_widget_color",
		"idx_entity_field_real_widget_weight",
	} {
		var count int
		err := st.catalogR.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name,
		).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("index %s count = %d, want 1", name, count)
		}
	}
	// Searchable-only fields get no side-table index.
	var count int
	if err := st.catalogR.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_entity_field_text_widget_summary'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("searchable-only field grew an index, count = %d", count)
	}
}

func seedColoredWidgets(t *testing.T, st *Store) {
	t.Helper()
	for _, w := range []struct {
		id     EntityID
		title  string
		color  string
		weight float64
	}{
		{"entity-a", "Anvil", "red", 9},
		{"entity-b", "Bolt", "blue", 1},
		{"entity-c", "Crate", "red", 5},
	} {
		entity := widgetEntity(w.id, "library-local")
		entity.Title = w.title
		entity.Fields = []EntityField{
			{Name: "color", Type: EntityFieldText, Text: w.color},
			{Name: "weight", Type: EntityFieldNumber, Number: w.weight},
		}
		if err := st.UpsertEntity(context.Background(), entity); err != nil {
			t.Fatal(err)
		}
	}
	// One widget with neither field: must sort last, never match filters.
	if err := st.UpsertEntity(context.Background(), widgetEntity("entity-bare", "library-local")); err != nil {
		t.Fatal(err)
	}
}

func TestEntitiesByKindFiltersByRegisteredField(t *testing.T) {
	st := openSeeded(t)
	registerWidgetIndexes(t, st)
	seedColoredWidgets(t, st)
	ctx := context.Background()

	red := EntityKindOptions{Filter: &EntityField{Name: "color", Type: EntityFieldText, Text: "red"}}
	entities, err := st.EntitiesByKind(ctx, "library-local", "widget", LibraryFilter{AllLibraries: true}, red, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 2 || entities[0].Title != "Anvil" || entities[1].Title != "Crate" {
		t.Fatalf("red widgets = %#v, want Anvil then Crate", entities)
	}

	// Unregistered, non-filterable, and mistyped filters error.
	for i, opts := range []EntityKindOptions{
		{Filter: &EntityField{Name: "size", Type: EntityFieldText, Text: "xl"}},    // never registered
		{Filter: &EntityField{Name: "summary", Type: EntityFieldText, Text: "x"}},  // searchable, not filterable
		{Filter: &EntityField{Name: "color", Type: EntityFieldNumber, Number: 3}},  // type mismatch
		{Filter: &EntityField{Name: "weight", Type: EntityFieldNumber, Number: 5}}, // sortable, not filterable
	} {
		if _, err := st.EntitiesByKind(ctx, "library-local", "widget", LibraryFilter{AllLibraries: true}, opts, 0, 0); err == nil {
			t.Fatalf("case %d: invalid filter accepted: %#v", i, opts)
		}
	}
}

// TestEntitiesByKindFiltersComposeMultipleFields covers the ticket's named
// regression: two Filters on one kind, including two backed by the SAME
// typed side table (both Text, so both resolve to entity_field_text), must
// narrow correctly via distinct join aliases rather than colliding on the
// single hard-coded "ff" alias the query used before entityFilterJoins.
func TestEntitiesByKindFiltersComposeMultipleFields(t *testing.T) {
	st := openSeeded(t)
	registerWidgetIndexes(t, st)
	if err := st.RegisterEntityFieldIndexes(context.Background(), []EntityFieldIndex{
		{Kind: "widget", Field: "material", Type: EntityFieldText, Filterable: true},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seed := []struct {
		id       EntityID
		title    string
		color    string
		material string
	}{
		{"entity-a", "Anvil", "red", "steel"},  // matches both
		{"entity-b", "Bolt", "red", "wood"},    // color only
		{"entity-c", "Crate", "blue", "steel"}, // material only
		{"entity-d", "Drum", "red", "steel"},   // matches both
	}
	for _, w := range seed {
		entity := widgetEntity(w.id, "library-local")
		entity.Title = w.title
		entity.Fields = []EntityField{
			{Name: "color", Type: EntityFieldText, Text: w.color},
			{Name: "material", Type: EntityFieldText, Text: w.material},
		}
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}

	opts := EntityKindOptions{Filters: []EntityField{
		{Name: "color", Type: EntityFieldText, Text: "red"},
		{Name: "material", Type: EntityFieldText, Text: "steel"},
	}}
	entities, err := st.EntitiesByKind(ctx, "library-local", "widget", LibraryFilter{AllLibraries: true}, opts, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 2 || entities[0].Title != "Anvil" || entities[1].Title != "Drum" {
		t.Fatalf("red+steel widgets = %#v, want Anvil then Drum (Bolt/Crate excluded)", entities)
	}
}

func TestEntitiesByKindSortsByRegisteredField(t *testing.T) {
	st := openSeeded(t)
	registerWidgetIndexes(t, st)
	seedColoredWidgets(t, st)
	ctx := context.Background()

	asc, err := st.EntitiesByKind(ctx, "library-local", "widget", LibraryFilter{AllLibraries: true}, EntityKindOptions{SortField: "weight"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantAsc := []string{"Bolt", "Crate", "Anvil", "Widget entity-bare"}
	for i, title := range wantAsc {
		if asc[i].Title != title {
			t.Fatalf("asc sort = %#v, want %v (missing-field rows last)", asc, wantAsc)
		}
	}

	desc, err := st.EntitiesByKind(ctx, "library-local", "widget", LibraryFilter{AllLibraries: true}, EntityKindOptions{SortField: "weight", SortDesc: true}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantDesc := []string{"Anvil", "Crate", "Bolt", "Widget entity-bare"}
	for i, title := range wantDesc {
		if desc[i].Title != title {
			t.Fatalf("desc sort = %#v, want %v (missing-field rows still last)", desc, wantDesc)
		}
	}

	// Filter + sort compose.
	redByWeight := EntityKindOptions{
		Filter:    &EntityField{Name: "color", Type: EntityFieldText, Text: "red"},
		SortField: "weight",
	}
	both, err := st.EntitiesByKind(ctx, "library-local", "widget", LibraryFilter{AllLibraries: true}, redByWeight, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(both) != 2 || both[0].Title != "Crate" || both[1].Title != "Anvil" {
		t.Fatalf("red by weight = %#v, want Crate then Anvil", both)
	}

	// A filterable-but-not-sortable field cannot order.
	if _, err := st.EntitiesByKind(ctx, "library-local", "widget", LibraryFilter{AllLibraries: true}, EntityKindOptions{SortField: "color"}, 0, 0); err == nil {
		t.Fatal("non-sortable field accepted as sort key")
	}
}

func TestSearchEntitiesMatchesTitleAndSearchableFields(t *testing.T) {
	st := openSeeded(t)
	registerWidgetIndexes(t, st)
	ctx := context.Background()

	entity := widgetEntity("entity-doc", "library-local")
	entity.Title = "Lighthouse"
	entity.Fields = []EntityField{
		{Name: "summary", Type: EntityFieldText, Text: "a coastal beacon keeper"},
		// color is filterable but NOT searchable — its value must not match.
		{Name: "color", Type: EntityFieldText, Text: "vermilion"},
	}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"light", "Lighthouse", "beacon", "coastal bea"} {
		items, err := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, query, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || items[0].ID != "entity-doc" || items[0].Kind != "entity" {
			t.Fatalf("search %q = %#v, want entity-doc", query, items)
		}
	}
	for _, query := range []string{"vermilion", "nothing-here", ""} {
		items, err := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, query, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 0 {
			t.Fatalf("search %q = %#v, want empty", query, items)
		}
	}
}

func TestSearchEntitiesFoldsDiacriticsTyposAndFiltersBeforeRanking(t *testing.T) {
	st := openSeeded(t)
	registerWidgetIndexes(t, st)
	ctx := context.Background()
	if err := st.UpsertEntity(ctx, Entity{
		ID:        "entity-visible-artist",
		LibraryID: "library-local",
		Kind:      "widget",
		Title:     "Beyoncé",
		Fields:    []EntityField{{Name: "summary", Type: EntityFieldText, Text: "Lemonade"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(ctx, Entity{
		ID:        "entity-hidden-artist",
		LibraryID: "library-archive",
		Kind:      "widget",
		Title:     "Beyonce Exact",
	}); err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}}
	for _, query := range []string{"beyonce", "lemonde"} {
		items, err := st.SearchEntities(ctx, filter, query, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || items[0].ID != "entity-visible-artist" {
			t.Fatalf("search %q = %#v, want only visible artist", query, items)
		}
	}
}

func TestSearchEntitiesSyncsOnUpsertAndDelete(t *testing.T) {
	st := openSeeded(t)
	registerWidgetIndexes(t, st)
	ctx := context.Background()

	entity := widgetEntity("entity-sync", "library-local")
	entity.Title = "Original"
	entity.Fields = []EntityField{{Name: "summary", Type: EntityFieldText, Text: "ephemeral"}}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}

	// Re-upsert replaces the search document: old terms stop matching.
	entity.Title = "Renamed"
	entity.Fields = []EntityField{{Name: "summary", Type: EntityFieldText, Text: "durable"}}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	for query, want := range map[string]int{"Original": 0, "ephemeral": 0, "Renamed": 1, "durable": 1} {
		items, err := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, query, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != want {
			t.Fatalf("after re-upsert, search %q = %#v, want %d items", query, items, want)
		}
	}

	// Deleting the entity (orphan GC) removes it from search via the trigger.
	purged, err := st.PurgeOrphanEntities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(purged) != 1 || purged[0] != "entity-sync" {
		t.Fatalf("purged = %#v, want entity-sync", purged)
	}
	items, err := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "Renamed", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("purged entity still searchable: %#v", items)
	}
}

func TestSearchEntitiesRevealFiltering(t *testing.T) {
	st := openSeeded(t)
	registerWidgetIndexes(t, st)
	ctx := context.Background()

	restricted := widgetEntity("entity-secret", "library-archive")
	restricted.Title = "Confidential Widget"
	if err := st.UpsertEntity(ctx, restricted); err != nil {
		t.Fatal(err)
	}
	// An unrestricted-Library control with the same searchable token so the
	// reveal+restricted annotation can be compared on a single revealed query.
	open := widgetEntity("entity-open", "library-local")
	open.Title = "Confidential-looking Public Widget"
	if err := st.UpsertEntity(ctx, open); err != nil {
		t.Fatal(err)
	}

	// Hidden == missing for the RESTRICTED item: an unrevealed scope never sees
	// entity-secret, the same as a nonexistent entity — no error, no count leak.
	// The public entity-open (same token, unrestricted Library) is still served,
	// and crucially carries Restricted=false (it isn't in a restricted Library).
	hidden, err := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "Confidential", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden) != 1 || hidden[0].ID != "entity-open" {
		t.Fatalf("unrevealed search = %#v, want only the public entity-open", hidden)
	}
	if hidden[0].Restricted {
		t.Fatalf("public entity-open carries Restricted=true in an unrevealed search: %#v", hidden[0])
	}

	revealed, err := st.SearchEntities(ctx, LibraryFilter{Revealed: true, AllLibraries: true}, "Confidential", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Revealed sees BOTH; the restricted-Library item is annotated Restricted=true,
	// the public one Restricted=false — the data the iOS per-poster tag consumes.
	byID := map[EntityID]SourceItem{}
	for _, it := range revealed {
		byID[EntityID(it.ID)] = it
	}
	if len(revealed) != 2 || byID["entity-secret"].ID == "" || byID["entity-open"].ID == "" {
		t.Fatalf("revealed search = %#v, want entity-secret and entity-open", revealed)
	}
	if !byID["entity-secret"].Restricted {
		t.Fatalf("revealed entity-secret Restricted=false, want true: %#v", byID["entity-secret"])
	}
	if byID["entity-open"].Restricted {
		t.Fatalf("revealed entity-open Restricted=true, want false: %#v", byID["entity-open"])
	}

	// AllowedIDs narrows scope the same way it does for Media: a library-local
	// allow-list sees the public entity-open but NOT entity-secret (in
	// library-archive, outside the allow-list), and entity-open stays unrestricted.
	scoped := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true}
	out, err := st.SearchEntities(ctx, scoped, "Confidential", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].ID != "entity-open" {
		t.Fatalf("allow-list search = %#v, want only the in-scope public entity-open", out)
	}
	if out[0].Restricted {
		t.Fatalf("in-scope public entity-open carries Restricted=true: %#v", out[0])
	}
}

func assertEntityIndexPresent(t *testing.T, st *Store, name string, want bool) {
	t.Helper()
	var count int
	if err := st.catalogR.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if (count == 1) != want {
		t.Fatalf("index %s present=%v, want %v", name, count == 1, want)
	}
}

func assertStoredFieldRow(t *testing.T, st *Store, table string, id EntityID, field string) {
	t.Helper()
	var count int
	if err := st.catalogR.QueryRow(
		`SELECT COUNT(*) FROM `+table+` WHERE entity_id = ? AND field = ?`, id, field,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatalf("%s row for %s.%s was purged; removed-field data must stay inert", table, id, field)
	}
}

// TestReconcileEntityFieldIndexesDropsRetiredFieldSurface covers ADR-0104 §2: a
// revision that removes a declared field drops that field's query surface (index +
// FTS contribution) at load while preserving its stored values inert, and re-adding
// it later restores both. A fresh Store on the SAME data dir models the real boot:
// the DB keeps the persisted indexes, the registry starts empty.
func TestReconcileEntityFieldIndexesDropsRetiredFieldSurface(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	open := func() *Store {
		st, err := Open(ctx, dir, Options{SeedDevelopmentData: true})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		return st
	}

	// Boot 1 — a revision declaring color(filterable), weight(sortable),
	// summary(searchable); seed one entity carrying all three.
	st := open()
	registerWidgetKindPolicy(t, st) // widget opts into FullTextSearchable
	if err := st.RegisterEntityFieldIndexes(ctx, []EntityFieldIndex{
		{Kind: "widget", Field: "color", Type: EntityFieldText, Filterable: true},
		{Kind: "widget", Field: "weight", Type: EntityFieldNumber, Sortable: true},
		{Kind: "widget", Field: "summary", Type: EntityFieldText, Searchable: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReconcileEntityFieldIndexes(ctx); err != nil {
		t.Fatalf("reconcile boot 1: %v", err)
	}
	entity := widgetEntity("entity-keep", "library-local")
	entity.Title = "Anvil"
	entity.Fields = []EntityField{
		{Name: "color", Type: EntityFieldText, Text: "red"},
		{Name: "weight", Type: EntityFieldNumber, Number: 9},
		{Name: "summary", Type: EntityFieldText, Text: "coastal beacon"},
	}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	assertEntityIndexPresent(t, st, "idx_entity_field_real_widget_weight", true)
	assertEntityIndexPresent(t, st, "idx_entity_field_text_widget_color", true)
	if items, err := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "beacon", 0); err != nil || len(items) != 1 {
		t.Fatalf("pre-revision summary search = %#v (err %v), want one", items, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Boot 2 — the revision REMOVED weight and summary, keeping only color. The
	// fresh registry knows only color, so reconcile drops the two dead surfaces.
	st = open()
	defer st.Close()
	registerWidgetKindPolicy(t, st) // widget still opts into FullTextSearchable
	if err := st.RegisterEntityFieldIndexes(ctx, []EntityFieldIndex{
		{Kind: "widget", Field: "color", Type: EntityFieldText, Filterable: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReconcileEntityFieldIndexes(ctx); err != nil {
		t.Fatalf("reconcile boot 2: %v", err)
	}
	assertEntityIndexPresent(t, st, "idx_entity_field_real_widget_weight", false) // retired field dropped
	assertEntityIndexPresent(t, st, "idx_entity_field_text_widget_color", true)   // still declared, retained
	assertEntityIndexPresent(t, st, "entity_field_real_kfv_idx", true)            // core base index never touched
	if items, err := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "beacon", 0); err != nil || len(items) != 0 {
		t.Fatalf("retired summary still searchable: %#v (err %v)", items, err)
	}
	if items, err := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "Anvil", 0); err != nil || len(items) != 1 {
		t.Fatalf("title search after reconcile = %#v (err %v), want one", items, err)
	}
	// Removed-field values stay INERT — never purged on a revision change.
	assertStoredFieldRow(t, st, "entity_field_real", "entity-keep", "weight")
	assertStoredFieldRow(t, st, "entity_field_text", "entity-keep", "summary")

	// Re-adding the fields in a later revision recreates the index and folds the
	// preserved values back into FTS (the existing load path).
	if err := st.RegisterEntityFieldIndexes(ctx, []EntityFieldIndex{
		{Kind: "widget", Field: "weight", Type: EntityFieldNumber, Sortable: true},
		{Kind: "widget", Field: "summary", Type: EntityFieldText, Searchable: true},
	}); err != nil {
		t.Fatal(err)
	}
	assertEntityIndexPresent(t, st, "idx_entity_field_real_widget_weight", true)
	if items, err := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "beacon", 0); err != nil || len(items) != 1 {
		t.Fatalf("re-added summary search = %#v (err %v), want one (values restored)", items, err)
	}
}

// TestReconcileEntityFieldIndexesClearsRemovedKind covers the whole-Kind-removal
// branch: when no active plugin declares the Kind at all, its index is dropped and
// its FTS contribution is cleared via the retired-FTS resync (the Kind being absent
// from the registry, RegisterEntityFieldIndexes never resyncs it). Data stays inert.
func TestReconcileEntityFieldIndexesClearsRemovedKind(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	open := func() *Store {
		st, err := Open(ctx, dir, Options{SeedDevelopmentData: true})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		return st
	}

	st := open()
	registerWidgetKindPolicy(t, st) // widget opts into FullTextSearchable
	if err := st.RegisterEntityFieldIndexes(ctx, []EntityFieldIndex{
		{Kind: "widget", Field: "color", Type: EntityFieldText, Filterable: true},
		{Kind: "widget", Field: "summary", Type: EntityFieldText, Searchable: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReconcileEntityFieldIndexes(ctx); err != nil {
		t.Fatalf("reconcile boot 1: %v", err)
	}
	entity := widgetEntity("entity-gone", "library-local")
	entity.Title = "Mortise"
	entity.Fields = []EntityField{
		{Name: "color", Type: EntityFieldText, Text: "green"},
		{Name: "summary", Type: EntityFieldText, Text: "dovetail joinery"},
	}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	if items, err := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "dovetail", 0); err != nil || len(items) != 1 {
		t.Fatalf("pre-removal search = %#v (err %v), want one", items, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Boot 2 — the widget Kind is gone entirely; only an unrelated Kind is declared.
	st = open()
	defer st.Close()
	if err := st.RegisterEntityFieldIndexes(ctx, []EntityFieldIndex{
		{Kind: "gizmo", Field: "label", Type: EntityFieldText, Filterable: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReconcileEntityFieldIndexes(ctx); err != nil {
		t.Fatalf("reconcile boot 2: %v", err)
	}
	assertEntityIndexPresent(t, st, "idx_entity_field_text_widget_color", false)
	if items, err := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "dovetail", 0); err != nil || len(items) != 0 {
		t.Fatalf("removed-Kind field still searchable: %#v (err %v)", items, err)
	}
	// The KIND is gone from the active set, so it no longer opts into
	// FullTextSearchable — its title stops matching too (the retired-FTS resync
	// clears the whole document). The stored row itself stays inert (below).
	if items, err := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "Mortise", 0); err != nil || len(items) != 0 {
		t.Fatalf("removed-Kind title still searchable = %#v (err %v), want none", items, err)
	}
	assertStoredFieldRow(t, st, "entity_field_text", "entity-gone", "summary")
}

func TestRegisterEntityFieldIndexesResyncsExistingEntities(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// Upserted BEFORE any registration: only the title is searchable.
	entity := widgetEntity("entity-early", "library-local")
	entity.Title = "Earlybird"
	entity.Fields = []EntityField{
		{Name: "summary", Type: EntityFieldText, Text: "predawn chorus"},
		{Name: "released", Type: EntityFieldDate, Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	items, err := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "predawn", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("unregistered field value searchable: %#v", items)
	}

	// Activation registers summary as Searchable: the resync folds the
	// pre-existing entity's value into the index without a re-upsert.
	registerWidgetIndexes(t, st)
	items, err = st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "predawn", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "entity-early" {
		t.Fatalf("post-registration search = %#v, want entity-early", items)
	}
	// Title matching survives the resync.
	items, err = st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "Earlybird", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("title search after resync = %#v, want one item", items)
	}
}

func TestQuoteSQLiteString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "''"},
		{"hello", "'hello'"},
		{"it's", "'it''s'"},
		{"''", "''''''"},
		{"a'b'c", "'a''b''c'"},
	}
	for _, c := range cases {
		if got := quoteSQLiteString(c.in); got != c.want {
			t.Errorf("quoteSQLiteString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

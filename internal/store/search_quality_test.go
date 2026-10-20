package store

import (
	"context"
	"fmt"
	"testing"
)

// visibleAndRestricted mirrors SearchEntities' own library-visibility setup
// so a test can call the unexported fuzzySearchEntitiesLocked directly (its
// exported signature stays frozen — HANDSHAKE-0063-FILTERS-1 only threads a
// filter parameter into the internal call, not into SearchEntities itself).
func visibleAndRestricted(t *testing.T, st *Store, filter LibraryFilter) ([]LibraryID, map[LibraryID]bool) {
	t.Helper()
	libraries, err := loadLibraries(context.Background(), st.catalogR)
	if err != nil {
		t.Fatal(err)
	}
	var visible []LibraryID
	restricted := map[LibraryID]bool{}
	for _, library := range libraries {
		if library.IsRestricted {
			restricted[library.ID] = true
		}
		if libraryVisible(library, filter) {
			visible = append(visible, library.ID)
		}
	}
	return visible, restricted
}

// TestFuzzySearchEntitiesFiltersInsideScanBeforeLimit is the blocker
// regression this ticket exists for: internal/store/search_quality.go:275's
// ranked-scan query caps its candidate window at fuzzySearchScanLimit (5000)
// rows in title order. The three "target" entities below carry the matching
// genre filter but sort AFTER all 5001 filler entities, so a page-then-filter
// implementation — scan the unfiltered first 5000 title-ordered rows, THEN
// check genre in Go — would find zero of them (they never entered the scan
// window) and under-return. Predicating genre=scifi INSIDE the SQL query, via
// entityFilterJoins, narrows the candidate set before the LIMIT is ever
// applied, so all three targets return regardless of their title position.
func TestFuzzySearchEntitiesFiltersInsideScanBeforeLimit(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	if err := st.RegisterEntityKindPolicies(ctx, []EntityKindPolicy{{Kind: "widget", FullTextSearchable: true}}); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterEntityFieldIndexes(ctx, []EntityFieldIndex{
		{Kind: "widget", Field: "summary", Type: EntityFieldText, Searchable: true},
		{Kind: "widget", Field: "genre", Type: EntityFieldText, Filterable: true},
	}); err != nil {
		t.Fatal(err)
	}

	tx, err := st.catalogW.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i <= fuzzySearchScanLimit; i++ {
		id := EntityID(fmt.Sprintf("entity-scale-%05d", i))
		title := fmt.Sprintf("A%05d", i) // sorts before every target title below
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO entity(id, library_id, kind, title) VALUES (?, 'library-local', 'widget', ?)`,
			id, title); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO entity_fts(entity_id, title, fields) VALUES (?, ?, 'Boundcommon Marker')`,
			id, title); err != nil {
			t.Fatal(err)
		}
	}
	targets := []EntityID{"entity-target-1", "entity-target-2", "entity-target-3"}
	for _, id := range targets {
		title := "Zzyzx " + string(id) // sorts after every filler title above
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO entity(id, library_id, kind, title) VALUES (?, 'library-local', 'widget', ?)`,
			id, title); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO entity_fts(entity_id, title, fields) VALUES (?, ?, 'Boundcommon Marker')`,
			id, title); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO entity_field_text(entity_id, kind, field, field_type, value) VALUES (?, 'widget', 'genre', 'text', 'scifi')`,
			id); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	visible, restricted := visibleAndRestricted(t, st, LibraryFilter{AllLibraries: true})
	filters := []EntityField{{Name: "genre", Type: EntityFieldText, Text: "scifi"}}

	st.mu.RLock()
	filtered, err := st.fuzzySearchEntitiesLocked(ctx, visible, restricted, "boundcommon", 10, "widget", filters)
	st.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	got := map[EntityID]bool{}
	for _, item := range filtered {
		got[EntityID(item.ID)] = true
	}
	if len(filtered) != len(targets) {
		t.Fatalf("filtered scan = %d items (want %d) — a page-then-filter implementation under-returns here: %v",
			len(filtered), len(targets), sourceItemIDs(filtered))
	}
	for _, id := range targets {
		if !got[id] {
			t.Fatalf("filtered scan missing %s beyond the %d-row scan window: %v", id, fuzzySearchScanLimit, sourceItemIDs(filtered))
		}
	}
}

// TestFuzzySearchEntitiesFilterPreservesRankOrder proves filtering narrows
// the candidate set without disturbing the fuzzy-match order the unfiltered
// scan would have produced: the filtered survivors appear in the same
// relative order they hold in the unfiltered ranked scan.
func TestFuzzySearchEntitiesFilterPreservesRankOrder(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	registerWidgetIndexes(t, st)
	if err := st.RegisterEntityFieldIndexes(ctx, []EntityFieldIndex{
		{Kind: "widget", Field: "genre", Type: EntityFieldText, Filterable: true},
		{Kind: "gadget", Field: "genre", Type: EntityFieldText, Filterable: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterEntityKindPolicies(ctx, []EntityKindPolicy{
		{Kind: "gadget", FullTextSearchable: true},
	}); err != nil {
		t.Fatal(err)
	}

	seed := []struct {
		id    EntityID
		title string
		genre string
	}{
		{"entity-beacon-exact", "Beacon", "scifi"},
		{"entity-beacon-close", "Beacony", "drama"},
		{"entity-beacon-fuzzy", "Beakon", "scifi"},
		{"entity-beacon-other", "Beaconish", "scifi"},
	}
	for _, s := range seed {
		entity := widgetEntity(s.id, "library-local")
		entity.Title = s.title
		entity.Fields = []EntityField{{Name: "genre", Type: EntityFieldText, Text: s.genre}}
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	otherKind := Entity{
		ID:        "entity-beacon-gadget",
		LibraryID: "library-local",
		Kind:      "gadget",
		Title:     "Beacon Gadget",
		Fields:    []EntityField{{Name: "genre", Type: EntityFieldText, Text: "scifi"}},
	}
	if err := st.UpsertEntity(ctx, otherKind); err != nil {
		t.Fatal(err)
	}

	filter := LibraryFilter{AllLibraries: true}

	// The unfiltered baseline is the actual exported SearchEntities call (the
	// order the criterion names), not just its internal helper.
	unfiltered, err := st.SearchEntities(ctx, filter, "beacon", 10)
	if err != nil {
		t.Fatal(err)
	}
	unfilteredOrder := map[EntityID]int{}
	for i, item := range unfiltered {
		unfilteredOrder[EntityID(item.ID)] = i
	}

	visible, restricted := visibleAndRestricted(t, st, filter)
	st.mu.RLock()
	filtered, err := st.fuzzySearchEntitiesLocked(ctx, visible, restricted, "beacon", 10, "widget",
		[]EntityField{{Name: "genre", Type: EntityFieldText, Text: "scifi"}})
	st.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 3 {
		t.Fatalf("filtered scifi hits = %v, want 3 (entity-beacon-close excluded)", sourceItemIDs(filtered))
	}
	last := -1
	for _, item := range filtered {
		pos, ok := unfilteredOrder[EntityID(item.ID)]
		if !ok {
			t.Fatalf("filtered item %s absent from the unfiltered scan", item.ID)
		}
		if EntityID(item.ID) == otherKind.ID {
			t.Fatalf("gadget-kind entity leaked into widget-kind filtered scan")
		}
		if EntityID(item.ID) == "entity-beacon-close" {
			t.Fatalf("drama-genre entity-beacon-close leaked into the scifi-filtered scan")
		}
		if pos <= last {
			t.Fatalf("filtered order %v does not preserve unfiltered rank order %v", sourceItemIDs(filtered), sourceItemIDs(unfiltered))
		}
		last = pos
	}
}

// TestFuzzySearchEntitiesFilterHonoursRevealForBothScans proves a
// restricted-Library entity stays absent from the ranked scan under an
// unrevealed filter whether or not a kind filter is also applied — filtering
// narrows the visible set further, it never widens it back open.
func TestFuzzySearchEntitiesFilterHonoursRevealForBothScans(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	registerWidgetIndexes(t, st)
	if err := st.RegisterEntityFieldIndexes(ctx, []EntityFieldIndex{
		{Kind: "widget", Field: "genre", Type: EntityFieldText, Filterable: true},
	}); err != nil {
		t.Fatal(err)
	}

	hiddenEntity := widgetEntity("entity-hidden-scifi", "library-archive")
	hiddenEntity.Title = "Confidential Beacon"
	hiddenEntity.Fields = []EntityField{{Name: "genre", Type: EntityFieldText, Text: "scifi"}}
	if err := st.UpsertEntity(ctx, hiddenEntity); err != nil {
		t.Fatal(err)
	}
	openEntity := widgetEntity("entity-open-scifi", "library-local")
	openEntity.Title = "Public Beacon"
	openEntity.Fields = []EntityField{{Name: "genre", Type: EntityFieldText, Text: "scifi"}}
	if err := st.UpsertEntity(ctx, openEntity); err != nil {
		t.Fatal(err)
	}

	unrevealed := LibraryFilter{AllLibraries: true}
	filters := []EntityField{{Name: "genre", Type: EntityFieldText, Text: "scifi"}}

	// The unfiltered baseline goes through the actual exported SearchEntities
	// entrypoint, not just the internal helper.
	unfiltered, err := st.SearchEntities(ctx, unrevealed, "beacon", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unfiltered) != 1 || unfiltered[0].ID != "entity-open-scifi" {
		t.Fatalf("unfiltered unrevealed scan = %v, want only entity-open-scifi", sourceItemIDs(unfiltered))
	}

	visible, restricted := visibleAndRestricted(t, st, unrevealed)

	st.mu.RLock()
	filtered, err := st.fuzzySearchEntitiesLocked(ctx, visible, restricted, "beacon", 10, "widget", filters)
	st.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].ID != "entity-open-scifi" {
		t.Fatalf("filtered unrevealed scan = %v, want only entity-open-scifi (hidden entity must stay absent)", sourceItemIDs(filtered))
	}
}

// TestFuzzySearchEntitiesFilterRejectsUnregisteredField proves an
// unregistered/non-Filterable field errors rather than silently falling back
// to an unfiltered scan.
func TestFuzzySearchEntitiesFilterRejectsUnregisteredField(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	registerWidgetIndexes(t, st) // color: Filterable; summary: Searchable, not Filterable

	visible, restricted := visibleAndRestricted(t, st, LibraryFilter{AllLibraries: true})
	cases := []EntityField{
		{Name: "never-registered", Type: EntityFieldText, Text: "x"},
		{Name: "summary", Type: EntityFieldText, Text: "x"}, // searchable, not filterable
		{Name: "color", Type: EntityFieldNumber, Number: 1}, // type mismatch
	}
	for i, field := range cases {
		st.mu.RLock()
		_, err := st.fuzzySearchEntitiesLocked(ctx, visible, restricted, "anything", 10, "widget", []EntityField{field})
		st.mu.RUnlock()
		if err == nil {
			t.Fatalf("case %d: invalid filter %#v accepted instead of erroring", i, field)
		}
	}
}

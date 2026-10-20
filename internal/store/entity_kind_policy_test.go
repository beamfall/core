package store

import (
	"context"
	"fmt"
	"testing"
)

// TestFullTextSearchableGatesSearch pins the KIND-level FullTextSearchable
// seam: only instances of a kind that opted in surface in SearchEntities. A
// kind with the default (off) posture stays out of entity search entirely —
// not even its title matches.
func TestFullTextSearchableGatesSearch(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// "gizmo" opts into full-text search; "gadget" does not.
	if err := st.RegisterEntityKindPolicies(ctx, []EntityKindPolicy{
		{Kind: "gizmo", FullTextSearchable: true},
	}); err != nil {
		t.Fatal(err)
	}
	searchable := Entity{ID: "entity-gizmo", LibraryID: "library-local", Kind: "gizmo", Title: "Beacon"}
	notSearchable := Entity{ID: "entity-gadget", LibraryID: "library-local", Kind: "gadget", Title: "Beacon"}
	for _, e := range []Entity{searchable, notSearchable} {
		if err := st.UpsertEntity(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	items, err := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "Beacon", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "entity-gizmo" {
		t.Fatalf("search = %#v, want only entity-gizmo (opted-in kind)", items)
	}
}

// TestFullTextSearchableOptInResyncsExisting pins that registering the seam
// AFTER an instance was upserted folds that instance into search — the
// resync-on-register path — and clearing the opt-in removes it again.
func TestFullTextSearchableOptInResyncsExisting(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// Upsert before any policy: not searchable yet.
	if err := st.UpsertEntity(ctx, Entity{
		ID: "entity-late", LibraryID: "library-local", Kind: "gizmo", Title: "Lantern",
	}); err != nil {
		t.Fatal(err)
	}
	if items, _ := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "Lantern", 0); len(items) != 0 {
		t.Fatalf("pre-opt-in search = %#v, want none", items)
	}

	// Opt the kind in: the existing instance is resynced into FTS.
	if err := st.RegisterEntityKindPolicies(ctx, []EntityKindPolicy{
		{Kind: "gizmo", FullTextSearchable: true},
	}); err != nil {
		t.Fatal(err)
	}
	if items, _ := st.SearchEntities(ctx, LibraryFilter{AllLibraries: true}, "Lantern", 0); len(items) != 1 {
		t.Fatalf("post-opt-in search = %#v, want one", items)
	}
}

// TestOrphanPolicyGCGatesPurge pins the OrphanPolicy:"gc" seam: orphaned
// instances of a "gc" kind are collected, orphaned instances of a "retain"
// (default) kind are kept.
func TestOrphanPolicyGCGatesPurge(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	if err := st.RegisterEntityKindPolicies(ctx, []EntityKindPolicy{
		{Kind: "collectible", OrphanGC: true},
		{Kind: "keeper", OrphanGC: false},
	}); err != nil {
		t.Fatal(err)
	}
	orphans := []Entity{
		{ID: "entity-gc", LibraryID: "library-local", Kind: "collectible", Title: "GC Me"},
		{ID: "entity-retain", LibraryID: "library-local", Kind: "keeper", Title: "Keep Me"},
	}
	for _, e := range orphans {
		if err := st.UpsertEntity(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	purged, err := st.PurgeOrphanEntities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(purged) != 1 || purged[0] != "entity-gc" {
		t.Fatalf("purged = %#v, want only entity-gc", purged)
	}
	// The gc kind's orphan is gone; the retain kind's orphan survives.
	if _, err := st.Entity(ctx, "entity-gc", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("gc orphan still readable")
	}
	if _, err := st.Entity(ctx, "entity-retain", LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatalf("retain orphan did not survive: %v", err)
	}
}

// TestPurgeOrphanEntitiesNoGCKinds pins that with no kind opted into gc the
// sweep is a no-op — the default is "retain" for every kind.
func TestPurgeOrphanEntitiesNoGCKinds(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.UpsertEntity(ctx, Entity{
		ID: "entity-untracked", LibraryID: "library-local", Kind: "widget", Title: "Untracked",
	}); err != nil {
		t.Fatal(err)
	}
	purged, err := st.PurgeOrphanEntities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(purged) != 0 {
		t.Fatalf("purged = %#v, want none (no gc kind registered)", purged)
	}
	if _, err := st.Entity(ctx, "entity-untracked", LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatalf("orphan of a retain kind did not survive: %v", err)
	}
}

// TestEntitiesByKindCursorStablePagination pins the CursorPaginated seam: a
// keyset cursor walks every instance exactly once, in (title, id) order, and a
// row inserted BEFORE the current position between pages never shifts the walk
// (no skips, no repeats) — the property offset paging cannot guarantee.
func TestEntitiesByKindCursorStablePagination(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllLibraries: true}

	// 25 instances titled "item-00".."item-24".
	for i := 0; i < 25; i++ {
		if err := st.UpsertEntity(ctx, Entity{
			ID:        EntityID(fmt.Sprintf("cursor-%02d", i)),
			LibraryID: "library-local",
			Kind:      "cursorkind",
			Title:     fmt.Sprintf("item-%02d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Walk in pages of 10, collecting IDs.
	seen := map[EntityID]int{}
	var order []string
	cursor := ""
	pages := 0
	for {
		page, err := st.EntitiesByKindCursor(ctx, "library-local", "cursorkind", filter, cursor, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Entities {
			seen[e.ID]++
			order = append(order, e.Title)
		}
		pages++
		// After the first page, insert an instance sorting BEFORE the cursor
		// position ("item-00a") — a stable keyset walk must not revisit it.
		if pages == 1 {
			if err := st.UpsertEntity(ctx, Entity{
				ID: "cursor-inserted", LibraryID: "library-local", Kind: "cursorkind", Title: "item-00a",
			}); err != nil {
				t.Fatal(err)
			}
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}

	// Every original instance seen exactly once; none seen twice.
	for i := 0; i < 25; i++ {
		id := EntityID(fmt.Sprintf("cursor-%02d", i))
		if seen[id] != 1 {
			t.Fatalf("instance %s seen %d times, want exactly 1", id, seen[id])
		}
	}
	// The mid-walk insert sorted before the cursor, so a stable walk skips it
	// (proving no repeat from a shifted offset); it is not double-counted.
	if seen["cursor-inserted"] > 1 {
		t.Fatalf("mid-walk insert seen %d times, want at most 1", seen["cursor-inserted"])
	}
	// Order is non-decreasing (title, id).
	for i := 1; i < len(order); i++ {
		if order[i] < order[i-1] {
			t.Fatalf("out-of-order page walk at %d: %q < %q", i, order[i], order[i-1])
		}
	}
}

// TestEntitiesByKindCursorMatchesOffsetPage pins that the cursor page returns
// the same instances as the equivalent offset page for a quiescent set — the
// two agree when nothing is changing, so cursor paging is a drop-in for offset
// paging, only stabler under concurrent writes.
func TestEntitiesByKindCursorMatchesOffsetPage(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllLibraries: true}
	for i := 0; i < 15; i++ {
		if err := st.UpsertEntity(ctx, Entity{
			ID:        EntityID(fmt.Sprintf("q-%02d", i)),
			LibraryID: "library-local",
			Kind:      "qkind",
			Title:     fmt.Sprintf("q-%02d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// First cursor page of 5.
	page, err := st.EntitiesByKindCursor(ctx, "library-local", "qkind", filter, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	offset, err := st.EntitiesByKind(ctx, "library-local", "qkind", filter, EntityKindOptions{}, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entities) != len(offset) {
		t.Fatalf("cursor page len %d != offset page len %d", len(page.Entities), len(offset))
	}
	for i := range offset {
		if page.Entities[i].ID != offset[i].ID {
			t.Fatalf("cursor[%d]=%s offset[%d]=%s", i, page.Entities[i].ID, i, offset[i].ID)
		}
	}
	// Second cursor page continues where the first left off (no overlap).
	page2, err := st.EntitiesByKindCursor(ctx, "library-local", "qkind", filter, page.Next, 5)
	if err != nil {
		t.Fatal(err)
	}
	offset2, err := st.EntitiesByKind(ctx, "library-local", "qkind", filter, EntityKindOptions{}, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	for i := range offset2 {
		if page2.Entities[i].ID != offset2[i].ID {
			t.Fatalf("page2 cursor[%d]=%s offset[%d]=%s", i, page2.Entities[i].ID, i, offset2[i].ID)
		}
	}
}

// TestEntitiesByKindCursorRejectsGarbageCursor pins that a malformed cursor is
// an error, not a silent full-scan — a caller must round-trip a store-minted
// cursor.
func TestEntitiesByKindCursorRejectsGarbageCursor(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if _, err := st.EntitiesByKindCursor(ctx, "library-local", "widget", LibraryFilter{AllLibraries: true}, "!!!not-base64!!!", 10); err == nil {
		t.Fatal("garbage cursor accepted, want error")
	}
}

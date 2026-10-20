package store

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// mediaIDsForEntity is a test helper: the ids of the Media a visible Entity
// relates to, for asserting re-pointed entity→media relations.
func mediaIDsForEntity(t *testing.T, st *Store, id EntityID) []MediaID {
	t.Helper()
	items, err := st.MediaForEntity(context.Background(), id, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("MediaForEntity(%s): %v", id, err)
	}
	out := make([]MediaID, len(items))
	for i, item := range items {
		out[i] = item.ID
	}
	return out
}

// relatedEntityIDs is a test helper: the ids of the Entities the given Entity
// points at through one relation name, for asserting entity→entity re-points.
func relatedEntityIDs(t *testing.T, st *Store, from EntityID, relation string) []EntityID {
	t.Helper()
	related, err := st.RelatedEntities(context.Background(), from, relation, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("RelatedEntities(%s, %s): %v", from, relation, err)
	}
	out := make([]EntityID, len(related))
	for i, r := range related {
		out[i] = r.Entity.ID
	}
	return out
}

// TestMergeEntitiesRepointsRelationsAndDedupesCollisions covers acceptance (a):
// the loser's entity→media and entity→entity relations are re-pointed to the
// winner, and a relation that collides with one the winner already holds is
// deduped against the unique index (one row, not two).
func TestMergeEntitiesRepointsRelationsAndDedupesCollisions(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC()

	// winner + loser of the same kind, plus a third entity that points AT the
	// loser (to exercise the inbound entity→entity re-point).
	for _, id := range []EntityID{"winner", "loser", "pointer"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, "library-local")); err != nil {
			t.Fatal(err)
		}
	}
	relations := []EntityRelation{
		// Collision: both winner and loser hold linked->media-alpha. Re-point
		// must dedupe to a single winner row.
		{FromEntityID: "winner", Name: "linked", ToMediaID: "media-alpha"},
		{FromEntityID: "loser", Name: "linked", ToMediaID: "media-alpha"},
		// Unique loser-only entity→entity relation — survives the re-point as a
		// winner row (loser -friend-> pointer becomes winner -friend-> pointer).
		{FromEntityID: "loser", Name: "friend", ToEntityID: "pointer"},
		// Inbound entity→entity: pointer -child-> loser becomes -child-> winner.
		{FromEntityID: "pointer", Name: "child", ToEntityID: "loser"},
	}
	for _, r := range relations {
		if err := st.UpsertEntityRelation(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := st.MergeEntities(ctx, "winner", "loser", LibraryFilter{AllLibraries: true}, now); err != nil {
		t.Fatalf("MergeEntities: %v", err)
	}

	// Winner relates to media-alpha exactly once — the collision deduped.
	if got := mediaIDsForEntity(t, st, "winner"); len(got) != 1 || got[0] != "media-alpha" {
		t.Fatalf("winner media = %v, want [media-alpha] (collision deduped)", got)
	}

	// The loser's outbound entity→entity relation re-pointed onto the winner.
	if ids := relatedEntityIDs(t, st, "winner", "friend"); len(ids) != 1 || ids[0] != "pointer" {
		t.Fatalf("winner friend = %v, want [pointer]", ids)
	}

	// The inbound entity→entity relation now lands on the winner.
	if ids := relatedEntityIDs(t, st, "pointer", "child"); len(ids) != 1 || ids[0] != "winner" {
		t.Fatalf("pointer child = %v, want [winner]", ids)
	}
}

// TestMergeEntitiesTrashesLoserRestorable covers acceptance (b): the loser is
// trashed (invisible to the read path, never hard-deleted) and UndoBatch
// restores it to live state.
func TestMergeEntitiesTrashesLoserRestorable(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC()

	for _, id := range []EntityID{"winner", "loser"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, "library-local")); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := st.MergeEntities(ctx, "winner", "loser", LibraryFilter{AllLibraries: true}, now)
	if err != nil {
		t.Fatalf("MergeEntities: %v", err)
	}

	// Loser is trashed: the reveal-filtered read can no longer see it, but the
	// row still exists (no hard delete).
	if _, err := st.Entity(ctx, "loser", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("trashed loser still visible to Entity()")
	}
	var trashed int64
	if scanErr := st.catalogR.QueryRowContext(ctx,
		`SELECT trashed_at_ms FROM entity WHERE id = ?`, "loser").Scan(&trashed); scanErr != nil {
		t.Fatalf("loser row missing after merge (hard-deleted?): %v", scanErr)
	}
	if trashed == 0 {
		t.Fatal("loser not marked trashed")
	}

	// Undo restores the loser to live state.
	if _, err := st.UndoBatch(ctx, batch.ID, LibraryFilter{AllLibraries: true}, time.Now().UTC()); err != nil {
		t.Fatalf("UndoBatch: %v", err)
	}
	if _, err := st.Entity(ctx, "loser", LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatalf("loser not restored after undo: %v", err)
	}
}

// TestMergeEntitiesUndoFullyReverses covers acceptance (c): one batch-undo
// restores BOTH entities' relation sets exactly as they were before the merge,
// including the deduped collision and the dropped self-loop case.
func TestMergeEntitiesUndoFullyReverses(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC()

	for _, id := range []EntityID{"winner", "loser", "pointer"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, "library-local")); err != nil {
			t.Fatal(err)
		}
	}
	relations := []EntityRelation{
		{FromEntityID: "winner", Name: "linked", ToMediaID: "media-alpha"},
		{FromEntityID: "loser", Name: "linked", ToMediaID: "media-alpha"}, // collision
		{FromEntityID: "loser", Name: "friend", ToEntityID: "pointer"},    // loser-only, re-points
		{FromEntityID: "pointer", Name: "child", ToEntityID: "loser"},     // inbound, re-points
		// A relation between winner and loser becomes a self-loop on re-point and
		// is dropped on apply — undo must restore it too.
		{FromEntityID: "winner", Name: "sibling", ToEntityID: "loser"},
	}
	for _, r := range relations {
		if err := st.UpsertEntityRelation(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	before := snapshotRelations(t, st)

	batch, err := st.MergeEntities(ctx, "winner", "loser", LibraryFilter{AllLibraries: true}, now)
	if err != nil {
		t.Fatalf("MergeEntities: %v", err)
	}
	if _, err := st.UndoBatch(ctx, batch.ID, LibraryFilter{AllLibraries: true}, time.Now().UTC()); err != nil {
		t.Fatalf("UndoBatch: %v", err)
	}

	after := snapshotRelations(t, st)
	if before != after {
		t.Fatalf("relations not fully reversed by undo:\n before=%q\n after =%q", before, after)
	}
	// And the loser is live again.
	if _, err := st.Entity(ctx, "loser", LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatalf("loser not restored: %v", err)
	}
}

// snapshotRelations returns a stable string of every entity_relation row, for
// before/after equality across an apply+undo cycle.
func snapshotRelations(t *testing.T, st *Store) string {
	t.Helper()
	rows, err := st.catalogR.QueryContext(context.Background(), `
		SELECT from_entity_id, relation,
		       COALESCE(to_entity_id, ''), COALESCE(to_media_id, ''),
		       COALESCE(attributes, ''), COALESCE(sort_order, -1)
		FROM entity_relation
		ORDER BY from_entity_id, relation, to_entity_id, to_media_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out string
	for rows.Next() {
		var from, rel, toE, toM, attrs string
		var sort int64
		if err := rows.Scan(&from, &rel, &toE, &toM, &attrs, &sort); err != nil {
			t.Fatal(err)
		}
		out += from + "|" + rel + "|" + toE + "|" + toM + "|" + attrs + "|" + strconv.FormatInt(sort, 10) + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTrashEntityHidesAndRestoreBrings covers the standalone trash/restore
// store methods: TrashEntity hides an Entity from the read path without
// hard-deleting it, and RestoreEntity (which sees past the read-path trash
// filter) brings it back.
func TestTrashEntityHidesAndRestoreBrings(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)

	if err := st.UpsertEntity(ctx, widgetEntity("solo", "library-local")); err != nil {
		t.Fatal(err)
	}
	if err := st.TrashEntity(ctx, "solo", LibraryFilter{AllLibraries: true}, time.Now().UTC()); err != nil {
		t.Fatalf("TrashEntity: %v", err)
	}
	if _, err := st.Entity(ctx, "solo", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("trashed entity still visible")
	}
	// Trashing an already-trashed / now-invisible entity is "not found" (the
	// read-path visibility check fails before the update).
	if err := st.TrashEntity(ctx, "solo", LibraryFilter{AllLibraries: true}, time.Now().UTC()); err == nil {
		t.Fatal("re-trash of hidden entity unexpectedly succeeded")
	}
	if err := st.RestoreEntity(ctx, "solo", LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatalf("RestoreEntity: %v", err)
	}
	if _, err := st.Entity(ctx, "solo", LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatalf("entity not visible after restore: %v", err)
	}
}

// TestMergeEntitiesDifferentKindErrors covers acceptance (d): merging entities
// of different Kinds is a validation error and mutates nothing.
func TestMergeEntitiesDifferentKindErrors(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)

	winner := widgetEntity("winner", "library-local") // kind "widget"
	loser := widgetEntity("loser", "library-local")
	loser.Kind = "gadget"
	if err := st.UpsertEntity(ctx, winner); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(ctx, loser); err != nil {
		t.Fatal(err)
	}

	_, err := st.MergeEntities(ctx, "winner", "loser", LibraryFilter{AllLibraries: true}, time.Now().UTC())
	if err == nil {
		t.Fatal("different-kind merge accepted")
	}
	if _, ok := err.(ValidationError); !ok {
		t.Fatalf("error type = %T, want ValidationError: %v", err, err)
	}
	// Loser untouched (still live).
	if _, err := st.Entity(ctx, "loser", LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatalf("loser mutated by a rejected merge: %v", err)
	}
}

// TestMergeEntitiesTrashedExcludedFromListing covers acceptance (e): a trashed
// (merged-away) loser is absent from EntitiesByKind and the entity-items Source.
func TestMergeEntitiesTrashedExcludedFromListing(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC()

	for _, id := range []EntityID{"winner", "loser"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, "library-local")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.MergeEntities(ctx, "winner", "loser", LibraryFilter{AllLibraries: true}, now); err != nil {
		t.Fatalf("MergeEntities: %v", err)
	}

	entities, err := st.EntitiesByKind(ctx, "library-local", "widget", LibraryFilter{AllLibraries: true}, EntityKindOptions{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entities {
		if e.ID == "loser" {
			t.Fatalf("trashed loser present in EntitiesByKind: %#v", entities)
		}
	}
	if len(entities) != 1 || entities[0].ID != "winner" {
		t.Fatalf("EntitiesByKind = %#v, want only winner", entities)
	}

	// entity-items Source likewise hides the trashed loser.
	items := mustSourceItems(t, st, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "entity-items", EntityKind: "widget"})
	for _, item := range items {
		if item.ID == "loser" {
			t.Fatalf("trashed loser present in entity-items Source: %#v", items)
		}
	}
}

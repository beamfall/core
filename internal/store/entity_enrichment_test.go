package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// TestEntitiesDueForEnrichment pins the enrichment job's walk: unchecked
// entities are due, matched ones never again, unmatched ones once their check
// is older than the retry cutoff. Records are per enrichment, trashed entities
// are skipped and the rows go with their entity.
func TestEntitiesDueForEnrichment(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	server := LibraryFilter{Revealed: true, AllLibraries: true}
	lib, err := st.CreateLibrary(ctx, "Shelf", false, "shelf", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Entity{
		{ID: "entity-box-a", LibraryID: lib.ID, Kind: "box", Title: "A"},
		{ID: "entity-box-b", LibraryID: lib.ID, Kind: "box", Title: "B"},
		{ID: "entity-box-c", LibraryID: lib.ID, Kind: "box", Title: "C"},
		{ID: "entity-box-d", LibraryID: lib.ID, Kind: "box", Title: "D"},
		{ID: "entity-maker", LibraryID: lib.ID, Kind: "maker", Title: "M"},
	} {
		if err := st.UpsertEntity(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.TrashEntity(ctx, "entity-box-d", server, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.Add(-40 * 24 * time.Hour)
	for _, rec := range []struct {
		id      EntityID
		name    string
		matched bool
		at      time.Time
	}{
		{"entity-box-a", "tags", true, old},
		{"entity-box-b", "tags", false, now},
		{"entity-box-c", "tags", false, old},
		{"entity-box-c", "other", true, now},
	} {
		if err := st.RecordEntityEnrichment(ctx, rec.id, rec.name, rec.matched, rec.at); err != nil {
			t.Fatal(err)
		}
	}
	cutoff := now.Add(-30 * 24 * time.Hour)
	due := func(name string, after EntityID) []EntityID {
		t.Helper()
		refs, err := st.EntitiesDueForEnrichment(ctx, "shelf", "box", name, after, cutoff, 10)
		if err != nil {
			t.Fatal(err)
		}
		var ids []EntityID
		for _, ref := range refs {
			if ref.LibraryID != lib.ID {
				t.Fatalf("ref %v carries library %q, want %q", ref.ID, ref.LibraryID, lib.ID)
			}
			ids = append(ids, ref.ID)
		}
		return ids
	}
	if got, want := due("tags", ""), []EntityID{"entity-box-c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("due(tags) = %v, want %v", got, want)
	}
	if got, want := due("other", ""), []EntityID{"entity-box-a", "entity-box-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("due(other) = %v, want %v", got, want)
	}
	if got, want := due("other", "entity-box-a"), []EntityID{"entity-box-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("due(other, cursor) = %v, want %v", got, want)
	}
	// A re-check replaces the earlier record.
	if err := st.RecordEntityEnrichment(ctx, "entity-box-c", "tags", true, now); err != nil {
		t.Fatal(err)
	}
	if got := due("tags", ""); len(got) != 0 {
		t.Fatalf("due(tags) after match = %v, want none", got)
	}
	if _, err := st.EntitiesDueForEnrichment(ctx, "shelf", "box", "", "", cutoff, 10); err == nil {
		t.Fatal("EntitiesDueForEnrichment accepted an empty enrichment name")
	}
	if err := st.RecordEntityEnrichment(ctx, "", "tags", true, now); err == nil {
		t.Fatal("RecordEntityEnrichment accepted an empty entity ID")
	}
}

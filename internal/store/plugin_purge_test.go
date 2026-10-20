package store

import (
	"context"
	"testing"
)

// TestPurgePluginData covers PANEL-8 / ADR-0104 Uninstall: purging a Library's
// Entities of a given Kind removes them and (via FK cascade + the FTS trigger)
// their fields and relations, while leaving other Kinds in the same Library
// untouched. Empty libraries or kinds are no-ops.
func TestPurgePluginData(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllLibraries: true}

	// Two "widget" entities with fields + a relation between them (exercises the
	// field + relation cascade), plus a surviving "gadget" of a different Kind.
	a := widgetEntity("purge-a", "library-local")
	a.Fields = []EntityField{{Name: "summary", Type: EntityFieldText, Text: "a"}}
	b := widgetEntity("purge-b", "library-local")
	for _, e := range []Entity{a, b} {
		if err := st.UpsertEntity(ctx, e); err != nil {
			t.Fatalf("upsert %s: %v", e.ID, err)
		}
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "purge-a", Name: "related", ToEntityID: "purge-b",
	}); err != nil {
		t.Fatalf("upsert relation: %v", err)
	}
	gadget := Entity{ID: "keep-gadget", LibraryID: "library-local", Kind: "gadget", Title: "Gadget"}
	if err := st.UpsertEntity(ctx, gadget); err != nil {
		t.Fatalf("upsert gadget: %v", err)
	}

	// Empty kinds → no-op.
	if n, err := st.PurgePluginData(ctx, []LibraryID{"library-local"}, nil); err != nil || n != 0 {
		t.Fatalf("empty-kinds purge: n=%d err=%v", n, err)
	}
	if n, err := st.PurgePluginData(ctx, nil, []string{"widget"}); err != nil || n != 0 {
		t.Fatalf("empty-libraries purge: n=%d err=%v", n, err)
	}

	// Purge the "widget" Kind.
	n, err := st.PurgePluginData(ctx, []LibraryID{"library-local"}, []string{"widget"})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 2 {
		t.Fatalf("purged %d entities, want 2", n)
	}

	// Both widgets are gone (the entity rows; fields + the relation cascade off them).
	for _, id := range []EntityID{"purge-a", "purge-b"} {
		if _, err := st.Entity(ctx, id, filter); err == nil {
			t.Fatalf("entity %s survived the purge", id)
		}
	}
	widgets, err := st.EntitiesByKind(ctx, "library-local", "widget", filter, EntityKindOptions{}, 50, 0)
	if err != nil {
		t.Fatalf("widgets read: %v", err)
	}
	if len(widgets) != 0 {
		t.Fatalf("widget Kind not empty after purge: %d", len(widgets))
	}

	// The other Kind in the same Library is untouched.
	got, err := st.Entity(ctx, "keep-gadget", filter)
	if err != nil {
		t.Fatalf("gadget should survive the widget purge: %v", err)
	}
	if got.Kind != "gadget" {
		t.Fatalf("unexpected survivor: %#v", got)
	}
}

func TestPurgePluginDataRollsBackAcrossLibraries(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	second, err := st.CreateLibrary(ctx, "Plugin B", false, "demo", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range []Entity{
		widgetEntity("purge-atomic-a", "library-local"),
		widgetEntity("purge-atomic-b", second.ID),
	} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatalf("upsert %s: %v", entity.ID, err)
		}
	}
	_, err = st.catalogW.ExecContext(ctx, `
		CREATE TRIGGER purge_plugin_data_fail
		BEFORE DELETE ON entity
		WHEN OLD.library_id = '`+string(second.ID)+`'
		BEGIN
			SELECT RAISE(ABORT, 'injected purge failure');
		END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PurgePluginData(ctx, []LibraryID{"library-local", second.ID}, []string{"widget"}); err == nil {
		t.Fatal("purge succeeded; want injected failure")
	}
	filter := LibraryFilter{AllLibraries: true, Revealed: true}
	for _, id := range []EntityID{"purge-atomic-a", "purge-atomic-b"} {
		if _, err := st.Entity(ctx, id, filter); err != nil {
			t.Fatalf("entity %s was purged despite rollback: %v", id, err)
		}
	}
}

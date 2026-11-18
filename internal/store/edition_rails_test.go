package store

import (
	"context"
	"database/sql"
	"reflect"
	"slices"
	"testing"
)

type editionRailFixture struct {
	st  *Store
	db  *sql.DB
	ids map[string]MediaID
}

func newEditionRailFixture(t *testing.T, encrypted bool) editionRailFixture {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: encrypted})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE library SET plugin_id = 'reels' WHERE id = 'library-local'`); err != nil {
		t.Fatal(err)
	}
	db := st.catalogW
	if encrypted {
		if err := st.SetLibraryBacking(ctx, "library-local", false, true); err != nil {
			t.Fatal(err)
		}
		if err := st.UnsealEncryptedLibraryDB(ctx, "library-local", copyEncryptedLibraryDBCipher{}); err != nil {
			t.Fatal(err)
		}
		db = st.encryptedDBs["library-local"].writer
	}
	if err := st.UpsertEntity(ctx, Entity{ID: "person", LibraryID: "library-local", Kind: "person", Title: "Person"}); err != nil {
		t.Fatal(err)
	}
	ids := map[string]MediaID{}
	for _, name := range []string{"Z parent", "A edition", "B sibling", "C other", "context"} {
		input := encryptedImport("library-local", "edition-rail-"+name, "/media/"+name+".mkv")
		input.Title = name
		media, _, err := st.ImportMediaFile(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = media.ID
		if name != "context" {
			if err := st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: "person", Name: "credit", ToMediaID: media.ID}); err != nil {
				t.Fatal(err)
			}
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := insertMediaFields(ctx, tx, media.ID, []MediaField{{Name: certificationField, Type: EntityFieldText, Text: "low"}}); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"A edition", "B sibling"} {
		if err := st.LinkMediaRelation(ctx, ids[name], MediaRelationEditionOf, ids["Z parent"]); err != nil {
			t.Fatal(err)
		}
	}
	return editionRailFixture{st: st, db: db, ids: ids}
}

func (f editionRailFixture) sourceIDs(t *testing.T, filter LibraryFilter, query SourceQuery) []MediaID {
	t.Helper()
	query.Kind = "entity-media"
	if query.EntityID == "" {
		query.EntityID = "person"
	}
	items, err := f.st.SourceItems(context.Background(), filter, query)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]MediaID, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

func TestEntityMediaEditionGroups(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		name := "catalog"
		if encrypted {
			name = "encrypted"
		}
		t.Run(name, func(t *testing.T) {
			f := newEditionRailFixture(t, encrypted)
			ctx := context.Background()
			filter := LibraryFilter{AllLibraries: true}
			want := []MediaID{f.ids["C other"], f.ids["Z parent"]}
			if got := f.sourceIDs(t, filter, SourceQuery{ExcludeEditionGroupOf: f.ids["context"]}); !reflect.DeepEqual(got, want) {
				t.Fatalf("folded = %v, want %v", got, want)
			}
			for offset, id := range want {
				if got := f.sourceIDs(t, filter, SourceQuery{ExcludeEditionGroupOf: f.ids["context"], Limit: 1, Offset: offset}); !reflect.DeepEqual(got, []MediaID{id}) {
					t.Fatalf("page %d = %v", offset, got)
				}
			}
			for _, name := range []string{"Z parent", "A edition", "B sibling"} {
				got := f.sourceIDs(t, filter, SourceQuery{ExcludeEditionGroupOf: f.ids[name], Limit: 1})
				if !reflect.DeepEqual(got, want[:1]) {
					t.Fatalf("excluding %s = %v", name, got)
				}
				if got := f.sourceIDs(t, filter, SourceQuery{ExcludeEditionGroupOf: f.ids[name], Limit: 1, Offset: 1}); len(got) != 0 {
					t.Fatalf("excluded page = %v", got)
				}
			}
			// A parent represents an eligible group even if only its editions carry this credit.
			if _, err := f.db.ExecContext(ctx, `DELETE FROM entity_relation WHERE to_media_id = ?`, f.ids["Z parent"]); err != nil {
				t.Fatal(err)
			}
			if got := f.sourceIDs(t, filter, SourceQuery{ExcludeEditionGroupOf: f.ids["context"]}); !reflect.DeepEqual(got, want) {
				t.Fatalf("edition-only credit = %v", got)
			}
			items, err := f.st.MediaForEntity(ctx, "person", filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 3 {
				t.Fatalf("playback children = %v, want both editions plus other", items)
			}
			// LibraryBrowse reads the selected plane; the all-items Source is
			// catalog-only. Neither discovery fix changes those browse paths.
			result, err := f.st.LibraryBrowse(ctx, filter, LibraryBrowseQuery{LibraryID: "library-local"})
			if err != nil {
				t.Fatal(err)
			}
			browse := result.Items
			if !encrypted {
				browse, err = f.st.SourceItems(ctx, filter, SourceQuery{Kind: "all-items", LibraryID: "library-local"})
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, id := range f.ids {
				if !slices.ContainsFunc(browse, func(item SourceItem) bool { return item.ID == id }) {
					t.Fatalf("browse lost %s", id)
				}
			}
		})
	}
}

func TestEntityMediaSourcePreservesEditionChildren(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		name := "catalog"
		if encrypted {
			name = "encrypted"
		}
		t.Run(name, func(t *testing.T) {
			f := newEditionRailFixture(t, encrypted)
			ctx := context.Background()
			filter := LibraryFilter{AllLibraries: true}
			if err := f.st.UpsertEntity(ctx, Entity{ID: "container", LibraryID: "library-local", Kind: "container", Title: "Container"}); err != nil {
				t.Fatal(err)
			}
			order := 0
			if err := f.st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: "container", Name: "contains", ToMediaID: f.ids["A edition"], SortOrder: &order}); err != nil {
				t.Fatal(err)
			}
			children, err := f.st.MediaForEntity(ctx, "container", filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(children) != 1 || children[0].ID != f.ids["A edition"] {
				t.Fatalf("playback children = %v, want only the edition", children)
			}
			items, err := f.st.SourceItems(ctx, filter, SourceQuery{Kind: "entity-media", EntityID: "container", Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(items, children) {
				t.Fatalf("plain Source = %v, want playback children %v", items, children)
			}
		})
	}
}

func TestEntityMediaEditionVisibility(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		name := "catalog"
		if encrypted {
			name = "encrypted"
		}
		t.Run(name, func(t *testing.T) {
			f := newEditionRailFixture(t, encrypted)
			ctx := context.Background()
			parent := f.ids["Z parent"]
			fallback := min(f.ids["A edition"], f.ids["B sibling"])
			if _, err := f.db.ExecContext(ctx, `UPDATE media_field_text SET value = 'high' WHERE media_id = ? AND field = 'certification'`, parent); err != nil {
				t.Fatal(err)
			}
			filter := LibraryFilter{AllLibraries: true, Certification: []CertificationConstraint{{PluginID: "reels", Allowed: []string{"low"}}}}
			want := []MediaID{fallback, f.ids["C other"]}
			if got := f.sourceIDs(t, filter, SourceQuery{ExcludeEditionGroupOf: f.ids["context"]}); !reflect.DeepEqual(got, want) {
				t.Fatalf("hidden parent = %v, want %v", got, want)
			}
			// Different credits still choose the same representative.
			for i, child := range []string{"A edition", "B sibling"} {
				entity := EntityID([]string{"first", "second"}[i])
				if err := f.st.UpsertEntity(ctx, Entity{ID: entity, LibraryID: "library-local", Kind: "category", Title: string(entity)}); err != nil {
					t.Fatal(err)
				}
				if err := f.st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: entity, Name: "credit", ToMediaID: f.ids[child]}); err != nil {
					t.Fatal(err)
				}
				if got := f.sourceIDs(t, filter, SourceQuery{EntityID: entity, ExcludeEditionGroupOf: f.ids["context"]}); !reflect.DeepEqual(got, []MediaID{fallback}) {
					t.Fatalf("credit %s = %v", entity, got)
				}
			}
			for _, id := range []MediaID{parent, "missing"} {
				if _, err := f.st.SourceItems(ctx, filter, SourceQuery{Kind: "entity-media", EntityID: "person", ExcludeEditionGroupOf: id}); err == nil {
					t.Fatalf("hidden/missing context %s succeeded", id)
				}
			}
			filter.GrantedMediaIDs = []MediaID{parent}
			if got := f.sourceIDs(t, filter, SourceQuery{ExcludeEditionGroupOf: f.ids["context"]}); !reflect.DeepEqual(got, []MediaID{f.ids["C other"], parent}) {
				t.Fatalf("granted parent = %v", got)
			}
			filter.GrantedMediaIDs = nil
			filter.Certification[0].Locked = true
			if got := f.sourceIDs(t, filter, SourceQuery{ExcludeEditionGroupOf: f.ids["context"]}); !reflect.DeepEqual(got, []MediaID{f.ids["C other"], parent}) {
				t.Fatalf("locked visible parent = %v", got)
			}
			filter.Certification = nil
			if _, err := f.db.ExecContext(ctx, `UPDATE media SET trashed_at_ms = 1 WHERE id = ?`, parent); err != nil {
				t.Fatal(err)
			}
			if got := f.sourceIDs(t, filter, SourceQuery{ExcludeEditionGroupOf: f.ids["context"]}); !reflect.DeepEqual(got, want) {
				t.Fatalf("trashed parent = %v", got)
			}
		})
	}
}

func TestEntityMediaEditionLibraryVisibility(t *testing.T) {
	f := newEditionRailFixture(t, false)
	ctx := context.Background()
	parent := f.ids["Z parent"]
	if _, err := f.db.ExecContext(ctx, `UPDATE media SET library_id = 'library-archive' WHERE id = ?`, parent); err != nil {
		t.Fatal(err)
	}
	fallback := min(f.ids["A edition"], f.ids["B sibling"])
	for _, filter := range []LibraryFilter{{AllLibraries: true}, {Revealed: true, AllowedIDs: []LibraryID{"library-local"}}} {
		if got := f.sourceIDs(t, filter, SourceQuery{ExcludeEditionGroupOf: f.ids["context"]}); !reflect.DeepEqual(got, []MediaID{fallback, f.ids["C other"]}) {
			t.Fatalf("hidden Library parent = %v", got)
		}
		if got := f.sourceIDs(t, filter, SourceQuery{ExcludeEditionGroupOf: f.ids["B sibling"]}); !reflect.DeepEqual(got, []MediaID{f.ids["C other"]}) {
			t.Fatalf("hidden-parent exclusion = %v", got)
		}
	}
	if got := f.sourceIDs(t, LibraryFilter{AllLibraries: true, Revealed: true}, SourceQuery{ExcludeEditionGroupOf: f.ids["context"]}); !reflect.DeepEqual(got, []MediaID{f.ids["C other"], parent}) {
		t.Fatalf("revealed parent = %v", got)
	}
	if _, err := f.db.ExecContext(ctx, `DELETE FROM entity_relation WHERE to_media_id != ?`, parent); err != nil {
		t.Fatal(err)
	}
	if got := f.sourceIDs(t, LibraryFilter{AllLibraries: true}, SourceQuery{ExcludeEditionGroupOf: f.ids["context"]}); len(got) != 0 {
		t.Fatalf("hidden credit leaked via visible edition: %v", got)
	}
}

func TestEntityMediaEditionChainsAndCycles(t *testing.T) {
	f := newEditionRailFixture(t, false)
	ctx := context.Background()
	filter := LibraryFilter{AllLibraries: true}
	if err := f.st.LinkMediaRelation(ctx, f.ids["Z parent"], MediaRelationEditionOf, f.ids["C other"]); err != nil {
		t.Fatal(err)
	}
	if got := f.sourceIDs(t, filter, SourceQuery{ExcludeEditionGroupOf: f.ids["context"]}); !reflect.DeepEqual(got, []MediaID{f.ids["C other"]}) {
		t.Fatalf("chain = %v", got)
	}
	if err := f.st.LinkMediaRelation(ctx, f.ids["C other"], MediaRelationEditionOf, f.ids["A edition"]); err != nil {
		t.Fatal(err)
	}
	lowest := min(f.ids["Z parent"], f.ids["A edition"], f.ids["B sibling"], f.ids["C other"])
	if got := f.sourceIDs(t, filter, SourceQuery{ExcludeEditionGroupOf: f.ids["context"]}); !reflect.DeepEqual(got, []MediaID{lowest}) {
		t.Fatalf("cycle = %v", got)
	}
	if got := f.sourceIDs(t, filter, SourceQuery{ExcludeEditionGroupOf: f.ids["B sibling"]}); len(got) != 0 {
		t.Fatalf("excluded cycle = %v", got)
	}
}

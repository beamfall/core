package store

import (
	"context"
	"testing"
)

// TestAnnotateMediaClassSetsMediaClasses pins each Media item's class and
// year; the demo sequence Entity takes the class its video Media share
// (CORE-2362). Entity years are derived by the scoped Source read.
func TestAnnotateMediaClassSetsMediaClasses(t *testing.T) {
	st := openDemoSeeded(t)
	ctx := context.Background()
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media SET media_class = 'audio' WHERE id = 'media-demo-bunny'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media SET media_class = 'video', year = 2010 WHERE id = 'media-demo-sintel'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media SET year = NULL WHERE id = 'media-demo-bunny'`); err != nil {
		t.Fatal(err)
	}
	items := []SourceItem{
		{ID: "media-demo-sintel", Kind: "media"},
		{ID: "media-demo-bunny", Kind: "media"},
		{ID: "media-does-not-exist", Kind: "media"},
		{ID: "entity-demo-sequence", Kind: "entity"},
	}
	if err := st.AnnotateMediaClass(ctx, items); err != nil {
		t.Fatalf("AnnotateMediaClass: %v", err)
	}
	want := []MediaClass{MediaClassVideo, MediaClassAudio, "", MediaClassVideo}
	// Only the item with a stored year carries one (CORE-2269).
	wantYear := []int{2010, 0, 0, 0}
	for i, item := range items {
		if item.MediaClass != want[i] {
			t.Errorf("%s MediaClass = %q, want %q", item.ID, item.MediaClass, want[i])
		}
		if item.Year != wantYear[i] {
			t.Errorf("%s Year = %d, want %d", item.ID, item.Year, wantYear[i])
		}
	}
	if err := st.AnnotateMediaClass(ctx, nil); err != nil {
		t.Fatalf("AnnotateMediaClass(nil): %v", err)
	}
}

func TestSectionSourceEntityYears(t *testing.T) {
	st, lib, hidden, media := libraryEntitiesFixture(t)
	ctx := context.Background()
	for name, year := range map[string]int{"a": 2023, "b": 2015, "c": 2001, "d": 2019} {
		if err := st.UpdateMediaMetadata(ctx, media[name], MediaMetadata{Year: &year}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.TrashMedia(ctx, media["c"], LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, media["b"], []MediaField{
		{Name: certificationField, Type: EntityFieldText, Text: "high"},
	}); err != nil {
		t.Fatal(err)
	}
	older, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: hidden.ID, Path: "/hidden/older.flac", Title: "Older", Container: "flac", Codec: "flac",
		Class: MediaClassAudio, ContentFingerprint: "fp-older", Year: 2004,
	})
	if err != nil {
		t.Fatal(err)
	}
	upsertRelatedEntity(t, st, "entity-lid-3", lib.ID, "lid", "Lid 3")
	relateEntities(t, st,
		EntityRelation{FromEntityID: "entity-lid-1", Name: "holds", ToMediaID: older.ID},
		EntityRelation{FromEntityID: "entity-lid-3", Name: "holds", ToMediaID: media["e"]},
	)

	for _, tc := range []struct {
		name      string
		libraryID LibraryID
		scope     LibraryFilter
		year      int
	}{
		{"unrevealed", "", LibraryFilter{AllLibraries: true}, 2015},
		{"revealed", "", LibraryFilter{AllLibraries: true, Revealed: true}, 2004},
		{"library-scope", lib.ID, LibraryFilter{AllLibraries: true, Revealed: true}, 2015},
		{"certification", "", LibraryFilter{AllLibraries: true, Certification: []CertificationConstraint{
			{PluginID: "shelf", Allowed: []string{"low"}, AllowUnrated: true},
		}}, 2023},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := SourceQuery{Kind: "entity-items", EntityKind: "lid", LibraryID: tc.libraryID}
			items, err := st.SourceItems(ctx, tc.scope, query)
			if err != nil {
				t.Fatal(err)
			}
			want := []struct {
				id   MediaID
				year int
			}{{"entity-lid-1", tc.year}, {"entity-lid-2", 2019}, {"entity-lid-3", 0}}
			if len(items) != len(want) {
				t.Fatalf("SourceItems = %+v, want three Entity cards", items)
			}
			for i, item := range items {
				if item.ID != want[i].id || item.Kind != "entity" || item.Year != want[i].year {
					t.Errorf("SourceItems[%d] = %s %s (%d), want entity %s (%d)", i, item.Kind, item.ID, item.Year, want[i].id, want[i].year)
				}
			}
		})
	}
}

func TestSectionSourceEntityYearsReadOpenEncryptedCatalog(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	const libraryID LibraryID = "library-local"
	cipher := copyEncryptedLibraryDBCipher{}
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	in := encryptedImport(libraryID, "fp-entity-year", "/encrypted/dated.mkv")
	in.Year = 2009
	media, _, err := st.ImportMediaFile(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	const entityID EntityID = "entity-encrypted-year"
	upsertRelatedEntity(t, st, entityID, libraryID, "box", "Dated box")
	relateEntities(t, st, EntityRelation{FromEntityID: entityID, Name: "holds", ToMediaID: media.ID})
	filter := LibraryFilter{AllLibraries: true}
	query := SourceQuery{Kind: "watchlist", ProfileID: "profile-default"}
	if err := st.SetItemWatchlisted(ctx, query.ProfileID, ItemTarget{EntityID: entityID}, true, filter); err != nil {
		t.Fatal(err)
	}
	items := mustSourceItems(t, st, filter, query)
	if len(items) != 1 || items[0].ID != MediaID(entityID) || items[0].Year != 2009 {
		t.Fatalf("open encrypted Source = %+v, want the Entity with year 2009", items)
	}
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	if items := mustSourceItems(t, st, filter, query); len(items) != 0 {
		t.Fatalf("sealed encrypted Source = %+v, want no items", items)
	}
}

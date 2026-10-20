package store

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"
)

func TestMediaFieldSearchMigrationBackfillsAndMaintainsFTS(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	before := migrationsBefore(t, catalogMigrations, "migrate/catalog", "0097")
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", before, "migrate/catalog", filepath.Join(t.TempDir(), "backups")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO library(id, title, plugin_id) VALUES ('library-search', 'Search', 'photos');
		INSERT INTO media(id, library_id, title, duration_seconds, container, codec, media_class)
		  VALUES ('media-search', 'library-search', 'Title', 1, 'jpg', 'jpeg', 'image');
		INSERT INTO media_field_text(media_id, field, field_type, value)
		  VALUES ('media-search', 'place', 'text', 'Legacy Paris');
		INSERT INTO pending_ai_tag(id, media_id, tag_kind, value, proposed_by, proposed_at_ms, status)
		  VALUES ('manual-clear-legacy', 'media-search', 'place', 'Old Paris', 'manual-clear', 123, 'rejected');
		INSERT INTO pending_ai_tag(id, media_id, tag_kind, value, proposed_by, proposed_at_ms, status)
		  VALUES ('ai-rejected-legacy', 'media-search', 'album', 'Old Album', 'fixture-ai', 456, 'rejected');
		INSERT INTO pending_ai_tag(id, media_id, tag_kind, value, proposed_by, proposed_at_ms, status)
		  VALUES ('ai-rejected-legacy-newer', 'media-search', 'album', 'Old Album', 'fixture-ai', 789, 'rejected');
	`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", catalogMigrations, "migrate/catalog", filepath.Join(t.TempDir(), "backups")); err != nil {
		t.Fatal(err)
	}
	assertFTSCount(t, db, "legacy", 1)
	if _, err := db.ExecContext(ctx, `UPDATE media_field_text SET value = 'Modern Tokyo' WHERE media_id = 'media-search' AND field = 'place'`); err != nil {
		t.Fatal(err)
	}
	assertFTSCount(t, db, "legacy", 0)
	assertFTSCount(t, db, "modern", 1)
	if _, err := db.ExecContext(ctx, `DELETE FROM media_field_text WHERE media_id = 'media-search' AND field = 'place'`); err != nil {
		t.Fatal(err)
	}
	assertFTSCount(t, db, "modern", 0)
	var rejectionCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_field_rejection WHERE media_id = 'media-search' AND field_type = ''`).Scan(&rejectionCount); err != nil {
		t.Fatal(err)
	}
	if rejectionCount != 2 {
		t.Fatalf("legacy rejection count = %d, want all 2 rejected proposals", rejectionCount)
	}
	var rejectedAt int64
	if err := db.QueryRowContext(ctx, `SELECT rejected_at_ms FROM media_field_rejection WHERE media_id = 'media-search' AND field = 'album'`).Scan(&rejectedAt); err != nil {
		t.Fatal(err)
	}
	if rejectedAt != 789 {
		t.Fatalf("deduped AI rejection timestamp = %d, want latest 789", rejectedAt)
	}
}

func TestMetadataFTSAggregatesTermsAcrossTitleAndFields(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	if _, err := st.catalogW.ExecContext(ctx, `
		UPDATE library SET plugin_id = 'photos' WHERE id = 'library-local';
		UPDATE media SET title = 'Golden Frame' WHERE id = 'media-alpha';
	`); err != nil {
		t.Fatal(err)
	}
	registerPhotoSearchPolicies(t, st)
	if err := st.SetMediaFields(ctx, "media-alpha", []MediaField{
		{Name: "place", Type: EntityFieldText, Text: "Paris France"},
		{Name: "album", Type: EntityFieldText, Text: "Summer Trip"},
	}); err != nil {
		t.Fatal(err)
	}
	assertMetadataQueries(t, st, "media-alpha")
}

func TestEncryptedMetadataFTSAggregatesTermsAcrossTitleAndFields(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	const libraryID = LibraryID("library-local")
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE library SET plugin_id = 'photos' WHERE id = ?`, libraryID); err != nil {
		t.Fatal(err)
	}
	registerPhotoSearchPolicies(t, st)
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	in := encryptedImport(libraryID, "sha256:fts-split-fields", "/encrypted/fts-split-fields.jpg")
	in.Title = "Golden Frame"
	in.Class = MediaClassImage
	media, _, err := st.ImportMediaFile(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, media.ID, []MediaField{
		{Name: "place", Type: EntityFieldText, Text: "Paris France"},
		{Name: "album", Type: EntityFieldText, Text: "Summer Trip"},
	}); err != nil {
		t.Fatal(err)
	}
	assertMetadataQueries(t, st, media.ID)
}

func registerPhotoSearchPolicies(t *testing.T, st *Store) {
	t.Helper()
	if err := st.ReplaceMediaFieldSearchPolicies(context.Background(), []MediaFieldSearchPolicy{
		{PluginID: "photos", Field: "place", Type: EntityFieldText, Searchable: true},
		{PluginID: "photos", Field: "album", Type: EntityFieldText, Searchable: true},
	}); err != nil {
		t.Fatal(err)
	}
}

func assertMetadataQueries(t *testing.T, st *Store, want MediaID) {
	t.Helper()
	for _, query := range []string{"paris summer", "golden paris"} {
		hits, err := st.SearchMedia(context.Background(), LibraryFilter{AllLibraries: true}, query, 10)
		if err != nil {
			t.Fatalf("SearchMedia(%q): %v", query, err)
		}
		if !sourceItemsContain(hits, want) {
			t.Fatalf("SearchMedia(%q) IDs = %v, want %s", query, sourceItemIDs(hits), want)
		}
		items, err := st.SourceItems(context.Background(), LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "all-items", Query: query, Limit: 10})
		if err != nil {
			t.Fatalf("SourceItems(%q): %v", query, err)
		}
		if !sourceItemsContain(items, want) {
			t.Fatalf("SourceItems(%q) IDs = %v, want %s", query, sourceItemIDs(items), want)
		}
	}
}

func assertFTSCount(t *testing.T, db queryRower, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM media_field_fts WHERE media_field_fts MATCH ?`, query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("FTS count for %q = %d, want %d", query, got, want)
	}
}

func TestMediaFieldSearchPoliciesArePluginScopedAndReplaced(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	if _, err := st.catalogW.ExecContext(ctx, `
		UPDATE library SET plugin_id = 'photos' WHERE id = 'library-local';
		UPDATE library SET plugin_id = 'archive' WHERE id = 'library-archive';
	`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []MediaID{"media-alpha", "media-archive"} {
		if err := st.SetMediaFields(ctx, id, []MediaField{{Name: "place", Type: EntityFieldText, Text: "Shared Needle"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ReplaceMediaFieldSearchPolicies(ctx, []MediaFieldSearchPolicy{
		{PluginID: "photos", Field: "place", Type: EntityFieldText, Searchable: false},
		{PluginID: "photos", Field: "place", Type: EntityFieldText, Searchable: true},
		{PluginID: "archive", Field: "place", Type: EntityFieldText},
	}); err != nil {
		t.Fatal(err)
	}
	hits, err := st.SearchMedia(ctx, LibraryFilter{Revealed: true, AllLibraries: true}, "needle", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceItemIDs(hits); len(got) != 1 || got[0] != "media-alpha" {
		t.Fatalf("plugin-scoped hits = %v, want only media-alpha", got)
	}
	if err := st.ReplaceMediaFieldSearchPolicies(ctx, []MediaFieldSearchPolicy{{PluginID: "archive", Field: "place", Type: EntityFieldText}}); err != nil {
		t.Fatal(err)
	}
	hits, err = st.SearchMedia(ctx, LibraryFilter{Revealed: true, AllLibraries: true}, "needle", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("removed declaration still authorized hits: %v", sourceItemIDs(hits))
	}
	if err := st.ReplaceMediaFieldSearchPolicies(ctx, []MediaFieldSearchPolicy{{PluginID: "photos", Field: "year", Type: EntityFieldNumber, Searchable: true}}); err == nil {
		t.Fatal("searchable non-text policy accepted")
	}
}

func TestMetadataFTSFindsMediaBeyondTitleScanAndPaginatesAfterDedupe(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE library SET plugin_id = 'photos' WHERE id = 'library-local'`); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceMediaFieldSearchPolicies(ctx, []MediaFieldSearchPolicy{
		{PluginID: "photos", Field: "place", Type: EntityFieldText, Searchable: true},
		{PluginID: "photos", Field: "album", Type: EntityFieldText, Searchable: true},
		{PluginID: "photos", Field: "note", Type: EntityFieldText, Searchable: true},
	}); err != nil {
		t.Fatal(err)
	}
	tx, err := st.catalogW.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i <= fuzzySearchScanLimit; i++ {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO media(id, library_id, title, duration_seconds, container, codec, media_class)
			VALUES (?, 'library-local', ?, 1, 'jpg', 'jpeg', 'image')
		`, MediaID(fmt.Sprintf("media-scale-%05d", i)), fmt.Sprintf("A%05d", i)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO media_field_text(media_id, field, field_type, value)
			VALUES (?, 'note', 'text', 'Boundcommon Marker')
		`, MediaID(fmt.Sprintf("media-scale-%05d", i))); err != nil {
			t.Fatal(err)
		}
	}
	targets := []MediaID{"media-scale-04998", "media-scale-04999", "media-scale-05000"}
	for _, id := range targets {
		for _, field := range []string{"place", "album"} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO media_field_text(media_id, field, field_type, value) VALUES (?, ?, 'text', 'Zzyzx Needle')`, id, field); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{AllLibraries: true}
	hits, err := st.SearchMedia(ctx, filter, "zzyzx", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceItemIDs(hits); len(got) != 3 {
		t.Fatalf("metadata hits = %v, want three deduped Media", got)
	}
	page, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "all-items", Query: "zzyzx", Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].ID != targets[1] {
		t.Fatalf("metadata page = %v, want [%s]", sourceItemIDs(page), targets[1])
	}
	bounded, err := st.SearchMedia(ctx, filter, "boundcommon", fuzzySearchScanLimit+100)
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded) != fuzzySearchScanLimit {
		t.Fatalf("bounded metadata hits = %d, want %d", len(bounded), fuzzySearchScanLimit)
	}
	if sourceItemsContain(bounded, "media-scale-05000") {
		t.Fatal("metadata candidate bound was not deterministic by rank then ID")
	}
}

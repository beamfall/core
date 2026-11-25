package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// CORE-0768 / ADR-0045: static Collections of an Encrypted Library and their
// membership live in that Library's protected plane, never in catalog.db.

func TestEncryptedLibraryCollectionAddResolveAndSealedClose(t *testing.T) {
	ctx := context.Background()
	st, first := seededEncryptedMedia(t, "Private Film Alpha")
	closed := false
	defer func() {
		if !closed {
			_ = st.Close()
		}
	}()
	libraryID := first.LibraryID
	second := importEncryptedMediaForCollection(t, st, libraryID, "Private Film Beta")
	third := importEncryptedMediaForCollection(t, st, libraryID, "Private Film Gamma")
	now := time.UnixMilli(1_800_000_000_000).UTC()
	all := LibraryFilter{AllLibraries: true}

	const name = "Secret Shelf Name 0768"
	coll, err := st.CreateCollection(ctx, Collection{LibraryID: libraryID, Name: name}, now)
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if _, err := st.AddCollectionMedia(ctx, coll.ID, []MediaID{third.ID, first.ID, second.ID}, all, now); err != nil {
		t.Fatalf("AddCollectionMedia: %v", err)
	}
	if _, err := st.ReorderCollectionMedia(ctx, coll.ID, []MediaID{second.ID, third.ID, first.ID}, all, now); err != nil {
		t.Fatalf("ReorderCollectionMedia: %v", err)
	}
	if _, err := st.RemoveCollectionMedia(ctx, coll.ID, []MediaID{third.ID}, all, now); err != nil {
		t.Fatalf("RemoveCollectionMedia: %v", err)
	}
	got, err := st.CollectionMemberMediaIDs(ctx, coll.ID, all)
	if err != nil {
		t.Fatalf("CollectionMemberMediaIDs: %v", err)
	}
	if want := []MediaID{second.ID, first.ID}; !slices.Equal(got, want) {
		t.Fatalf("members = %v, want %v", got, want)
	}
	byID, err := st.CollectionByID(ctx, coll.ID, false)
	if err != nil || byID.Name != name || byID.MemberCount != 2 {
		t.Fatalf("CollectionByID = %+v, %v", byID, err)
	}
	listed, err := st.Collections(ctx, libraryID, false)
	if err != nil || len(listed) != 1 || listed[0].ID != coll.ID {
		t.Fatalf("Collections = %+v, %v", listed, err)
	}
	renamed := name + " Renamed"
	if _, err := st.UpdateCollection(ctx, coll.ID, CollectionPatch{Name: &renamed}, now); err != nil {
		t.Fatalf("UpdateCollection: %v", err)
	}

	// An unauthorized filter hides the Collection and its members.
	other := LibraryFilter{AllowedIDs: []LibraryID{"library-elsewhere"}}
	if members, err := st.CollectionMemberMediaIDs(ctx, coll.ID, other); err != nil || len(members) != 0 {
		t.Fatalf("unauthorized members = %v, %v", members, err)
	}
	if _, err := st.AddCollectionMedia(ctx, coll.ID, []MediaID{third.ID}, other, now); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("unauthorized add err = %v, want ErrCollectionNotFound", err)
	}

	// Nothing reached catalog.db.
	assertNoCatalogCollectionRows(t, st, libraryID)

	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	if _, err := st.CollectionByID(ctx, coll.ID, true); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("sealed CollectionByID err = %v, want ErrCollectionNotFound", err)
	}
	if members, err := st.ResolveCollectionMembers(ctx, coll.ID, all); err != nil || len(members) != 0 {
		t.Fatalf("sealed members = %v, %v", members, err)
	}
	if listed, err := st.Collections(ctx, libraryID, true); err != nil || len(listed) != 0 {
		t.Fatalf("sealed Collections = %+v, %v", listed, err)
	}
	if _, err := st.CreateCollection(ctx, Collection{LibraryID: libraryID, Name: "while sealed"}, now); !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("sealed CreateCollection err = %v, want ErrEncryptedLibraryDBDisabled", err)
	}

	catalogPath := st.catalogPath
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closed = true
	assertCatalogFilesLackText(t, catalogPath, name, renamed, string(coll.ID), string(first.ID), string(second.ID))

	// Unsealing restores the original Collection and order.
	reopened, err := Open(ctx, filepath.Dir(filepath.Dir(catalogPath)), Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if err := reopened.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("re-unseal: %v", err)
	}
	got, err = reopened.CollectionMemberMediaIDs(ctx, coll.ID, all)
	if err != nil || !slices.Equal(got, []MediaID{second.ID, first.ID}) {
		t.Fatalf("restored members = %v, %v", got, err)
	}
	if restored, err := reopened.CollectionByID(ctx, coll.ID, false); err != nil || restored.Name != renamed {
		t.Fatalf("restored Collection = %+v, %v", restored, err)
	}
}

func TestEncryptedLibraryCollectionSnapshotImportAndSource(t *testing.T) {
	ctx := context.Background()
	st, first := seededEncryptedMedia(t, "Imported Private One")
	defer st.Close()
	libraryID := first.LibraryID
	second := importEncryptedMediaForCollection(t, st, libraryID, "Imported Private Two")
	now := time.UnixMilli(1_800_000_000_000).UTC()
	all := LibraryFilter{AllLibraries: true}

	const id = CollectionID("collection-imported-0768")
	coll, err := st.ImportCollectionSnapshot(ctx, Collection{ID: id, LibraryID: libraryID, Name: "Imported Secret Set"}, []MediaID{second.ID, first.ID}, now)
	if err != nil {
		t.Fatalf("ImportCollectionSnapshot: %v", err)
	}
	// A replay is additive and idempotent on the protected plane too.
	if _, err := st.ImportCollectionSnapshot(ctx, Collection{ID: id, LibraryID: libraryID, Name: "Imported Secret Set"}, []MediaID{second.ID, first.ID}, now); err != nil {
		t.Fatalf("ImportCollectionSnapshot replay: %v", err)
	}
	got, err := st.CollectionMemberMediaIDs(ctx, coll.ID, all)
	if err != nil || !slices.Equal(got, []MediaID{second.ID, first.ID}) {
		t.Fatalf("imported members = %v, %v", got, err)
	}
	st.mu.RLock()
	items, err := st.collectionSourceItemsLocked(ctx, all, SourceQuery{Value: string(id)}, 10, 0)
	st.mu.RUnlock()
	if err != nil {
		t.Fatalf("collection source: %v", err)
	}
	if len(items) != 2 || items[0].ID != second.ID || items[1].ID != first.ID {
		t.Fatalf("collection source items = %+v", items)
	}
	assertNoCatalogCollectionRows(t, st, libraryID)
}

func TestPlaintextLibraryCollectionsStayInCatalog(t *testing.T) {
	ctx := context.Background()
	st, encrypted := seededEncryptedMedia(t, "Mixed Private")
	defer st.Close()
	now := time.UnixMilli(1_800_000_000_000).UTC()
	all := LibraryFilter{AllLibraries: true}
	plainLibrary := createPlainLibraryForCollection(t, st)
	plainA := importPlainMediaForCollection(t, st, plainLibrary, "Public A")
	plainB := importPlainMediaForCollection(t, st, plainLibrary, "Public B")

	public, err := st.CreateCollection(ctx, Collection{LibraryID: plainLibrary, Name: "Public Shelf"}, now)
	if err != nil {
		t.Fatalf("CreateCollection public: %v", err)
	}
	if _, err := st.AddCollectionMedia(ctx, public.ID, []MediaID{plainB.ID, plainA.ID}, all, now); err != nil {
		t.Fatalf("AddCollectionMedia public: %v", err)
	}
	private, err := st.CreateCollection(ctx, Collection{LibraryID: encrypted.LibraryID, Name: "Private Shelf"}, now)
	if err != nil {
		t.Fatalf("CreateCollection private: %v", err)
	}
	if _, err := st.AddCollectionMedia(ctx, private.ID, []MediaID{encrypted.ID}, all, now); err != nil {
		t.Fatalf("AddCollectionMedia private: %v", err)
	}
	var catalogRows int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM collection_media WHERE collection_id = ?`, public.ID).Scan(&catalogRows); err != nil || catalogRows != 2 {
		t.Fatalf("public catalog members = %d, %v", catalogRows, err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, encrypted.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	got, err := st.CollectionMemberMediaIDs(ctx, public.ID, all)
	if err != nil || !slices.Equal(got, []MediaID{plainB.ID, plainA.ID}) {
		t.Fatalf("public members after seal = %v, %v", got, err)
	}
	if got, err := st.CollectionMemberMediaIDs(ctx, private.ID, all); err != nil || len(got) != 0 {
		t.Fatalf("private members after seal = %v, %v", got, err)
	}
}

func TestEncryptedLibraryUnsealEvacuatesLegacyPlaintextCollections(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "Legacy Private")
	closed := false
	defer func() {
		if !closed {
			_ = st.Close()
		}
	}()
	libraryID := media.LibraryID
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	const legacyName = "Legacy Plaintext Shelf 0768"
	seedLegacyCatalogCollection(t, st, libraryID, legacyName, media.ID)

	// A failed catalog delete unwinds the unseal and leaves the rows in place.
	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TRIGGER test_block_collection_delete BEFORE DELETE ON collection
		BEGIN SELECT RAISE(ABORT, 'blocked by test'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err == nil {
		t.Fatal("unseal with blocked evacuation succeeded")
	}
	if n := countCatalogCollectionRows(t, st, libraryID); n != 2 {
		t.Fatalf("catalog rows after failed evacuation = %d, want 2", n)
	}
	if db := st.encryptedDBs[libraryID]; db != nil && db.state == "unsealed-registered" {
		t.Fatal("failed evacuation left the Library unsealed")
	}
	if _, err := st.catalogW.ExecContext(ctx, `DROP TRIGGER test_block_collection_delete`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}

	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	assertNoCatalogCollectionRows(t, st, libraryID)
	all := LibraryFilter{AllLibraries: true}
	coll, err := st.CollectionByID(ctx, "collection-legacy-0768", false)
	if err != nil || coll.Name != legacyName || coll.SortOrder != 7 {
		t.Fatalf("evacuated Collection = %+v, %v", coll, err)
	}
	child, err := st.CollectionByID(ctx, "collection-legacy-child-0768", false)
	if err != nil || child.ParentID != "collection-legacy-0768" {
		t.Fatalf("evacuated child = %+v, %v", child, err)
	}
	got, err := st.CollectionMemberMediaIDs(ctx, coll.ID, all)
	if err != nil || !slices.Equal(got, []MediaID{media.ID}) {
		t.Fatalf("evacuated members = %v, %v", got, err)
	}

	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	catalogPath := st.catalogPath
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closed = true
	assertCatalogFilesLackText(t, catalogPath, legacyName)
}

func importEncryptedMediaForCollection(t *testing.T, st *Store, libraryID LibraryID, title string) Media {
	t.Helper()
	imported := encryptedImport(libraryID, "sha256:"+title, "/enc/"+title+".mkv")
	imported.Title = title
	media, _, err := st.ImportMediaFile(context.Background(), imported)
	if err != nil {
		t.Fatalf("ImportMediaFile %q: %v", title, err)
	}
	return media
}

func createPlainLibraryForCollection(t *testing.T, st *Store) LibraryID {
	t.Helper()
	lib, err := st.CreateLibrary(context.Background(), "Public 0768", false, "", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	return lib.ID
}

func importPlainMediaForCollection(t *testing.T, st *Store, libraryID LibraryID, title string) Media {
	t.Helper()
	imported := encryptedImport(libraryID, "sha256:"+title, "/plain/"+title+".mkv")
	imported.Title = title
	media, _, err := st.ImportMediaFile(context.Background(), imported)
	if err != nil {
		t.Fatalf("ImportMediaFile %q: %v", title, err)
	}
	return media
}

// seedLegacyCatalogCollection writes the rows a pre-CORE-0768 server left in
// catalog.db for an Encrypted Library. The member FK is relaxed for the insert
// because the Media exists only in the protected plane.
func seedLegacyCatalogCollection(t *testing.T, st *Store, libraryID LibraryID, name string, members ...MediaID) {
	t.Helper()
	ctx := context.Background()
	conn, err := st.catalogW.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	stmts := []struct {
		query string
		args  []any
	}{
		{`PRAGMA foreign_keys = OFF`, nil},
		{`INSERT INTO collection (id, library_id, parent_id, name, sort_order, created_at_ms, updated_at_ms)
		  VALUES ('collection-legacy-child-0768', ?, 'collection-legacy-0768', 'Legacy Child', 1, 1, 1)`, []any{libraryID}},
		{`INSERT INTO collection (id, library_id, parent_id, name, sort_order, created_at_ms, updated_at_ms)
		  VALUES ('collection-legacy-0768', ?, NULL, ?, 7, 1, 1)`, []any{libraryID, name}},
	}
	for i, member := range members {
		stmts = append(stmts, struct {
			query string
			args  []any
		}{`INSERT INTO collection_media (collection_id, media_id, sort_order, added_at_ms)
		  VALUES ('collection-legacy-0768', ?, ?, 1)`, []any{member, i}})
	}
	stmts = append(stmts, struct {
		query string
		args  []any
	}{`PRAGMA foreign_keys = ON`, nil})
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatalf("seed legacy rows %q: %v", stmt.query, err)
		}
	}
}

func countCatalogCollectionRows(t *testing.T, st *Store, libraryID LibraryID) int {
	t.Helper()
	var n int
	if err := st.catalogR.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM collection WHERE library_id = ?`, libraryID).Scan(&n); err != nil {
		t.Fatalf("count catalog collections: %v", err)
	}
	return n
}

func assertNoCatalogCollectionRows(t *testing.T, st *Store, libraryID LibraryID) {
	t.Helper()
	if n := countCatalogCollectionRows(t, st, libraryID); n != 0 {
		t.Fatalf("catalog.collection holds %d rows for Encrypted Library %s", n, libraryID)
	}
	var members int
	if err := st.catalogR.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM collection_media cm
		WHERE NOT EXISTS (SELECT 1 FROM collection c WHERE c.id = cm.collection_id)
		   OR cm.collection_id IN (SELECT id FROM collection WHERE library_id = ?)
	`, libraryID).Scan(&members); err != nil {
		t.Fatalf("count catalog members: %v", err)
	}
	if members != 0 {
		t.Fatalf("catalog.collection_media holds %d rows for Encrypted Library %s", members, libraryID)
	}
}

// assertCatalogFilesLackText scans the closed catalog.db and any sidecars byte
// for byte, so free pages and WAL frames count as leaks too.
func assertCatalogFilesLackText(t *testing.T, catalogPath string, needles ...string) {
	t.Helper()
	for _, path := range []string{catalogPath, catalogPath + "-wal", catalogPath + "-journal"} {
		data, err := os.ReadFile(path) //nolint:gosec // G304: test-only path derived from the Store's catalog path.
		if os.IsNotExist(err) && path != catalogPath {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, needle := range needles {
			if bytes.Contains(data, []byte(needle)) {
				t.Fatalf("%s contains encrypted-Library text %q", filepath.Base(path), needle)
			}
		}
	}
}

// A legacy Collection that names Media still held in catalog.db stays there
// whole: moving it would drop that member, and the Collection is no more
// exposed than its Media already is.
func TestEncryptedLibraryUnsealKeepsLegacyCollectionsNamingCatalogMedia(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "Legacy Mixed")
	defer func() { _ = st.Close() }()
	libraryID := media.LibraryID
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	seedLegacyCatalogCollection(t, st, libraryID, "Legacy Mixed Shelf", media.ID, "media-held-in-catalog")
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	if n := countCatalogCollectionRows(t, st, libraryID); n != 2 {
		t.Fatalf("catalog rows = %d, want 2 kept", n)
	}
	var members, protected int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM collection_media WHERE collection_id = 'collection-legacy-0768'`).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if err := st.encryptedDBs[libraryID].reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM collection`).Scan(&protected); err != nil {
		t.Fatal(err)
	}
	if members != 2 || protected != 0 {
		t.Fatalf("catalog members = %d (want 2), protected collections = %d (want 0)", members, protected)
	}
	if coll, err := st.CollectionByID(ctx, "collection-legacy-0768", false); err != nil || coll.Name != "Legacy Mixed Shelf" {
		t.Fatalf("kept Collection = %+v, %v", coll, err)
	}
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// TestSavedFilterReadsReportNotFoundOnlyForMissingRows proves a missing saved
// filter or smart collection is still not-found, while any other database
// failure reaches the caller instead of being reported as not-found.
func TestSavedFilterReadsReportNotFoundOnlyForMissingRows(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.SavedFilterByID(ctx, "missing"); !errors.Is(err, ErrSavedFilterNotFound) {
		t.Fatalf("missing saved filter read = %v, want ErrSavedFilterNotFound", err)
	}
	if _, err := st.DeleteSavedFilter(ctx, "missing"); !errors.Is(err, ErrSavedFilterNotFound) {
		t.Fatalf("missing saved filter delete = %v, want ErrSavedFilterNotFound", err)
	}
	if _, err := st.SmartCollectionByID(ctx, "missing"); !errors.Is(err, ErrSmartCollectionNotFound) {
		t.Fatalf("missing smart collection read = %v, want ErrSmartCollectionNotFound", err)
	}
	if _, err := st.DeleteSmartCollection(ctx, "missing"); !errors.Is(err, ErrSmartCollectionNotFound) {
		t.Fatalf("missing smart collection delete = %v, want ErrSmartCollectionNotFound", err)
	}

	for _, table := range []string{"saved_filter", "smart_collection"} {
		if _, err := st.catalogW.ExecContext(ctx, `ALTER TABLE `+table+` RENAME TO `+table+`_unavailable`); err != nil {
			t.Fatalf("rename %s: %v", table, err)
		}
	}
	requireDatabaseError := func(what string, err, notFound error) {
		t.Helper()
		if err == nil || errors.Is(err, notFound) {
			t.Fatalf("%s with failing database = %v, want the database error", what, err)
		}
	}
	_, err = st.SavedFilterByID(ctx, "missing")
	requireDatabaseError("saved filter read", err, ErrSavedFilterNotFound)
	_, err = st.DeleteSavedFilter(ctx, "missing")
	requireDatabaseError("saved filter delete", err, ErrSavedFilterNotFound)
	_, err = st.SmartCollectionByID(ctx, "missing")
	requireDatabaseError("smart collection read", err, ErrSmartCollectionNotFound)
	_, err = st.DeleteSmartCollection(ctx, "missing")
	requireDatabaseError("smart collection delete", err, ErrSmartCollectionNotFound)
}

// TestAnnotateRestrictedPropagatesLookupFailure proves a failed library lookup
// fails the read instead of leaving the items marked unrestricted.
func TestAnnotateRestrictedPropagatesLookupFailure(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Only the entity table exists, so the media lookup fails.
	if _, err := db.Exec(`CREATE TABLE entity (id TEXT PRIMARY KEY, library_id TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	st := &Store{catalogR: db}
	items := []SourceItem{{ID: "media-restricted", Kind: "media"}}
	err = st.annotateRestrictedLocked(context.Background(), map[LibraryID]bool{"library-restricted": true}, items)
	if err == nil {
		t.Fatalf("annotation with failing lookup = nil, items %#v; want the lookup error", items)
	}
}

package store

import (
	"context"
	"testing"
)

// A failure while deleting one evacuation's shared rows must leave every
// evacuation's rows in catalog.db: the unwind discards the working plane, so a
// delete that already committed for another evacuation would lose its rows
// (CORE-0706, CORE-0768, CORE-0769).
func TestEncryptedUnsealEvacuationsDeleteTogetherOrNotAtAll(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "evacuation-order")
	defer st.Close()
	libraryID := media.LibraryID
	private := string(media.ID) + " evacuation-order"

	if _, err := st.encryptedDBs[libraryID].writer.ExecContext(ctx, `
		INSERT OR REPLACE INTO encrypted_library_metadata (key, value) VALUES ('legacy_expression_evacuation', 'pending')`); err != nil {
		t.Fatalf("arm pending key: %v", err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	seedLegacyCatalogExpressions(t, st, libraryID, private)
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO artwork_candidate(id, media_id, slot, source_url, plugin_id, scraper_id, applied, created_at_ms)
		VALUES (?, ?, 'poster', 'https://art.example/order.jpg', '', '', 1, 1)`,
		ArtworkCandidateID(media.ID, "poster", "https://art.example/order.jpg"), media.ID); err != nil {
		t.Fatalf("strand candidate: %v", err)
	}

	// The identification delete is the last statement of the shared
	// transaction, so the expression deletes before it have already run.
	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TRIGGER test_block_candidate_delete BEFORE DELETE ON artwork_candidate
		BEGIN SELECT RAISE(ABORT, 'blocked by test'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err == nil {
		t.Fatal("unseal with blocked identification delete succeeded")
	}
	want := map[string]int{"saved_filter": 2, "smart_collection": 1, "saved_station": 1, "channel": 1}
	for table, n := range countCatalogExpressionRows(t, st, libraryID) {
		if n != want[table] {
			t.Fatalf("catalog.%s after failed unseal = %d, want %d", table, n, want[table])
		}
	}
	if n := identificationRowCount(t, st.catalogR, `SELECT COUNT(*) FROM artwork_candidate WHERE media_id = ?`, media.ID); n != 1 {
		t.Fatalf("shared candidates after failed unseal = %d, want 1", n)
	}

	if _, err := st.catalogW.ExecContext(ctx, `DROP TRIGGER test_block_candidate_delete`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("retry Unseal: %v", err)
	}
	assertNoCatalogExpressionRows(t, st, libraryID)
	if n := identificationRowCount(t, st.catalogR, `SELECT COUNT(*) FROM artwork_candidate WHERE media_id = ?`, media.ID); n != 0 {
		t.Fatalf("shared candidates after retry = %d, want 0", n)
	}
	if n := identificationRowCount(t, st.encryptedDBs[libraryID].reader, `SELECT COUNT(*) FROM artwork_candidate WHERE media_id = ?`, media.ID); n != 1 {
		t.Fatalf("private candidates after retry = %d, want 1", n)
	}
}

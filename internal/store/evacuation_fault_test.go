package store

import (
	"context"
	"strings"
	"testing"
)

// TestEncryptedUnsealEvacuationIsFailureAtomic injects SQL faults into the
// unseal evacuation (CORE-0706): a failed shared-catalog delete and a failed
// private-plane insert each abort the unseal with the shared rows intact and
// the plane unregistered, and the next unseal after the fault clears moves
// every row exactly once.
func TestEncryptedUnsealEvacuationIsFailureAtomic(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "evacuation-fault")
	defer func() { _ = st.Close() }()
	cipher := copyEncryptedLibraryDBCipher{}

	// A plane-side trigger that refuses any candidate whose URL names a fault,
	// standing in for a failed private insert (disk full, I/O error).
	private := st.encryptedDBs[media.LibraryID].writer
	if _, err := private.ExecContext(ctx, `
		CREATE TRIGGER fault_private_insert BEFORE INSERT ON artwork_candidate
		WHEN new.source_url LIKE '%/fault/%'
		BEGIN SELECT RAISE(ABORT, 'injected private insert fault'); END
	`); err != nil {
		t.Fatal(err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, cipher); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	strand := func(url string) {
		t.Helper()
		if _, err := st.catalogW.ExecContext(ctx, `
			INSERT OR IGNORE INTO artwork_candidate(id, media_id, slot, source_url, plugin_id, scraper_id, applied, created_at_ms)
			VALUES (?, ?, 'poster', ?, '', '', 0, 1)`,
			ArtworkCandidateID(media.ID, "poster", url), media.ID, url); err != nil {
			t.Fatalf("strand candidate: %v", err)
		}
	}
	shared := func() int {
		return identificationRowCount(t, st.catalogR, `SELECT COUNT(*) FROM artwork_candidate WHERE media_id = ?`, media.ID)
	}
	strand("https://art.example/a.jpg")
	strand("https://art.example/b.jpg")

	// Fault 1: the shared delete fails after the private copy committed.
	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TRIGGER fault_shared_delete BEFORE DELETE ON artwork_candidate
		BEGIN SELECT RAISE(ABORT, 'injected shared delete fault'); END
	`); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, media.LibraryID, cipher); err == nil || !strings.Contains(err.Error(), "injected shared delete fault") {
		t.Fatalf("unseal through a failed shared delete = %v, want the injected fault", err)
	}
	if st.EncryptedLibraryDBRegistered(media.LibraryID) {
		t.Fatal("a failed evacuation left the plane registered")
	}
	if n := shared(); n != 2 {
		t.Fatalf("shared rows after the delete fault = %d, want 2 intact", n)
	}
	if _, err := st.catalogW.ExecContext(ctx, `DROP TRIGGER fault_shared_delete`); err != nil {
		t.Fatal(err)
	}

	// Fault 2: a private insert fails; nothing is deleted from the catalog.
	strand("https://art.example/fault/c.jpg")
	if err := st.UnsealEncryptedLibraryDB(ctx, media.LibraryID, cipher); err == nil || !strings.Contains(err.Error(), "injected private insert fault") {
		t.Fatalf("unseal through a failed private insert = %v, want the injected fault", err)
	}
	if st.EncryptedLibraryDBRegistered(media.LibraryID) {
		t.Fatal("a failed private insert left the plane registered")
	}
	if n := shared(); n != 3 {
		t.Fatalf("shared rows after the insert fault = %d, want 3 intact", n)
	}

	// Restart: the faulting row is withdrawn and the next unseal converges,
	// even though an earlier attempt may already have copied rows privately.
	if _, err := st.catalogW.ExecContext(ctx, `DELETE FROM artwork_candidate WHERE source_url LIKE '%/fault/%'`); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, media.LibraryID, cipher); err != nil {
		t.Fatalf("Unseal after the faults cleared: %v", err)
	}
	if n := shared(); n != 0 {
		t.Fatalf("shared rows after recovery = %d, want 0", n)
	}
	plane := st.encryptedDBs[media.LibraryID].reader
	if n := identificationRowCount(t, plane, `SELECT COUNT(*) FROM artwork_candidate WHERE media_id = ?`, media.ID); n != 2 {
		t.Fatalf("private rows after recovery = %d, want exactly 2", n)
	}
}

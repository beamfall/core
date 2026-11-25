package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// CORE-0706 slice 2: an Encrypted Library's Unmatched Records and the
// suppressions naming them live on its own plane. Their source handles and
// candidate titles never reach catalog.db, a Sealed Library refuses new
// records, and rows an earlier build left in catalog.db move on unseal.

const (
	encryptedUnmatchedHandle = "/enc/urec-private-handle-7c1f.mkv"
	encryptedUnmatchedTitle  = "urec-private-candidate-title-7c1f"
)

func TestEncryptedUnmatchedRecordsStayOnPrivatePlane(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "encrypted-unmatched")
	closed := false
	defer func() {
		if !closed {
			_ = st.Close()
		}
	}()
	all := LibraryFilter{AllLibraries: true, Revealed: true}
	var seq int
	var schema, catalogPath string
	if err := st.catalogR.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&seq, &schema, &catalogPath); err != nil || catalogPath == "" {
		t.Fatalf("catalog path = %q, %v", catalogPath, err)
	}

	rec := UnmatchedRecord{
		Origin: UnmatchedOriginScan, LibraryID: media.LibraryID, MediaID: media.ID,
		ImporterID: "scanner", SourceHandle: encryptedUnmatchedHandle,
		CandidateTitle: encryptedUnmatchedTitle, FailureReason: UnmatchedReasonNoCandidates,
		QueuedAt: time.Now().Add(-time.Minute).UTC(),
	}
	id, err := st.QueueUnmatchedRecordOnce(ctx, rec)
	if err != nil {
		t.Fatalf("QueueUnmatchedRecordOnce: %v", err)
	}
	if again, err := st.QueueUnmatchedRecordOnce(ctx, rec); err != nil || again != id {
		t.Fatalf("repeat QueueUnmatchedRecordOnce = %q, %v; want %q", again, err, id)
	}
	plain, err := st.QueueUnmatchedRecord(ctx, UnmatchedRecord{Origin: UnmatchedOriginImport, ImportRunID: "run-1", SourceHandle: "global-handle"})
	if err != nil {
		t.Fatalf("QueueUnmatchedRecord(global): %v", err)
	}
	private := st.encryptedDBs[media.LibraryID].reader
	if n := identificationRowCount(t, private, `SELECT COUNT(*) FROM unmatched_record WHERE id = ?`, id); n != 1 {
		t.Fatalf("private unmatched_record rows = %d, want 1", n)
	}
	if n := identificationRowCount(t, st.catalogR, `SELECT COUNT(*) FROM unmatched_record WHERE id = ? OR library_id = ?`, id, media.LibraryID); n != 0 {
		t.Fatalf("shared unmatched_record rows for encrypted Library = %d", n)
	}

	records, err := st.UnmatchedRecords(ctx, all)
	if err != nil || len(records) != 2 || records[0].ID != plain || records[1].ID != id {
		t.Fatalf("UnmatchedRecords = %#v, %v; want [%s %s]", records, err, plain, id)
	}
	if got, err := st.UnmatchedRecordByID(ctx, id, all); err != nil || got.SourceHandle != encryptedUnmatchedHandle || got.MediaID != media.ID {
		t.Fatalf("UnmatchedRecordByID = %#v, %v", got, err)
	}
	if pending, err := st.UnmatchedRecordPending(ctx, id); err != nil || !pending {
		t.Fatalf("UnmatchedRecordPending = %v, %v", pending, err)
	}
	for _, suppression := range []UnmatchedSuppression{
		{Scope: SuppressionScopeCandidate, Key: CandidateSuppressionKey(id, "cand-1"), RecordID: id, SourceHandle: encryptedUnmatchedHandle},
		{Scope: SuppressionScopeFile, Key: "fingerprint-7c1f", RecordID: id, SourceHandle: encryptedUnmatchedHandle},
	} {
		if err := st.SuppressUnmatched(ctx, suppression); err != nil {
			t.Fatalf("SuppressUnmatched(%s): %v", suppression.Scope, err)
		}
	}
	if n := identificationRowCount(t, st.catalogR, `SELECT COUNT(*) FROM unmatched_suppression`); n != 0 {
		t.Fatalf("shared unmatched_suppression rows = %d, want 0", n)
	}
	if suppressed, err := st.UnmatchedSuppressed(ctx, SuppressionScopeFile, "fingerprint-7c1f"); err != nil || !suppressed {
		t.Fatalf("UnmatchedSuppressed(file) = %v, %v", suppressed, err)
	}
	if list, err := st.UnmatchedSuppressions(ctx, id); err != nil || len(list) != 2 {
		t.Fatalf("UnmatchedSuppressions = %#v, %v", list, err)
	}
	if reset, err := st.ResetUnmatchedSuppression(ctx, SuppressionScopeCandidate, CandidateSuppressionKey(id, "cand-1")); err != nil || !reset {
		t.Fatalf("ResetUnmatchedSuppression = %v, %v", reset, err)
	}
	if resolved, err := st.ResolveUnmatchedRecord(ctx, id, media.ID, time.Now().UTC()); err != nil || !resolved {
		t.Fatalf("ResolveUnmatchedRecord = %v, %v", resolved, err)
	}
	if resolved, err := st.ResolveUnmatchedRecord(ctx, id, media.ID, time.Now().UTC()); err != nil || !resolved {
		t.Fatalf("replayed ResolveUnmatchedRecord = %v, %v", resolved, err)
	}
	pendingID, err := st.QueueUnmatchedRecord(ctx, UnmatchedRecord{
		Origin: UnmatchedOriginScan, LibraryID: media.LibraryID, SourceHandle: encryptedUnmatchedHandle + ".2",
	})
	if err != nil {
		t.Fatalf("QueueUnmatchedRecord(second): %v", err)
	}

	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := st.QueueUnmatchedRecord(ctx, UnmatchedRecord{Origin: UnmatchedOriginScan, LibraryID: media.LibraryID, SourceHandle: encryptedUnmatchedHandle + ".3"}); !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("sealed QueueUnmatchedRecord error = %v, want ErrEncryptedLibraryDBDisabled", err)
	}
	if records, err := st.UnmatchedRecords(ctx, all); err != nil || len(records) != 1 || records[0].ID != plain {
		t.Fatalf("sealed UnmatchedRecords = %#v, %v; want only %s", records, err, plain)
	}
	if _, err := st.UnmatchedRecordByID(ctx, pendingID, all); !errors.Is(err, ErrUnmatchedRecordNotFound) {
		t.Fatalf("sealed UnmatchedRecordByID error = %v, want not found", err)
	}
	if pending, err := st.UnmatchedRecordPending(ctx, pendingID); err != nil || pending {
		t.Fatalf("sealed UnmatchedRecordPending = %v, %v", pending, err)
	}
	if resolved, err := st.ResolveUnmatchedRecord(ctx, pendingID, "", time.Now().UTC()); err != nil || resolved {
		t.Fatalf("sealed ResolveUnmatchedRecord = %v, %v", resolved, err)
	}
	if suppressed, err := st.UnmatchedSuppressed(ctx, SuppressionScopeFile, "fingerprint-7c1f"); err != nil || suppressed {
		t.Fatalf("sealed UnmatchedSuppressed = %v, %v", suppressed, err)
	}
	if list, err := st.UnmatchedSuppressions(ctx, ""); err != nil || len(list) != 0 {
		t.Fatalf("sealed UnmatchedSuppressions = %#v, %v", list, err)
	}

	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closed = true
	for _, suffix := range []string{"", "-wal"} {
		raw, err := os.ReadFile(catalogPath + suffix) // #nosec G304 -- the test's own temp catalog path.
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read catalog.db%s: %v", suffix, err)
		}
		for _, secret := range []string{"urec-private-handle-7c1f", encryptedUnmatchedTitle, "fingerprint-7c1f"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatalf("catalog.db%s bytes contain %q", suffix, secret)
			}
		}
	}
}

func TestEncryptedUnsealEvacuatesLegacyUnmatchedRecords(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "encrypted-unmatched-legacy")
	defer st.Close()
	all := LibraryFilter{AllLibraries: true, Revealed: true}
	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	strand := func() {
		t.Helper()
		for _, row := range []struct {
			id      string
			library any
		}{{"urec-legacy-private", string(media.LibraryID)}, {"urec-legacy-global", nil}} {
			if _, err := st.catalogW.ExecContext(ctx, `
				INSERT OR IGNORE INTO unmatched_record(id, origin, library_id, source_handle, candidate_title, queued_at_ms)
				VALUES (?, 'scan', ?, ?, ?, 1)`, row.id, row.library, encryptedUnmatchedHandle, encryptedUnmatchedTitle); err != nil {
				t.Fatalf("strand record: %v", err)
			}
		}
		if _, err := st.catalogW.ExecContext(ctx, `
			INSERT OR IGNORE INTO unmatched_suppression(scope, scope_key, record_id, source_handle, created_at_ms)
			VALUES ('source-record', 'urec-legacy-private', 'urec-legacy-private', ?, 1)`, encryptedUnmatchedHandle); err != nil {
			t.Fatalf("strand suppression: %v", err)
		}
	}
	strand()
	// While sealed, the legacy row of the Encrypted Library stays hidden.
	if records, err := st.UnmatchedRecords(ctx, all); err != nil || len(records) != 1 || records[0].ID != "urec-legacy-global" {
		t.Fatalf("sealed UnmatchedRecords = %#v, %v", records, err)
	}

	// A failed shared delete unwinds the unseal with catalog.db intact.
	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TRIGGER test_block_unmatched_evacuation BEFORE DELETE ON unmatched_record
		BEGIN SELECT RAISE(ABORT, 'blocked by test'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err == nil {
		t.Fatal("unseal with blocked evacuation delete succeeded")
	}
	if n := identificationRowCount(t, st.catalogR, `SELECT COUNT(*) FROM unmatched_record WHERE id = 'urec-legacy-private'`); n != 1 {
		t.Fatalf("shared legacy record after failed unseal = %d, want 1", n)
	}
	if _, err := st.catalogW.ExecContext(ctx, `DROP TRIGGER test_block_unmatched_evacuation`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}

	assertEvacuated := func() {
		t.Helper()
		private := st.encryptedDBs[media.LibraryID].reader
		if n := identificationRowCount(t, st.catalogR, `SELECT COUNT(*) FROM unmatched_record WHERE library_id = ?`, media.LibraryID); n != 0 {
			t.Fatalf("shared legacy records left = %d", n)
		}
		if n := identificationRowCount(t, st.catalogR, `SELECT COUNT(*) FROM unmatched_suppression WHERE record_id = 'urec-legacy-private'`); n != 0 {
			t.Fatalf("shared legacy suppressions left = %d", n)
		}
		if n := identificationRowCount(t, st.catalogR, `SELECT COUNT(*) FROM unmatched_record WHERE id = 'urec-legacy-global'`); n != 1 {
			t.Fatalf("Library-less record rows = %d, want 1", n)
		}
		if n := identificationRowCount(t, private, `SELECT COUNT(*) FROM unmatched_suppression WHERE record_id = 'urec-legacy-private'`); n != 1 {
			t.Fatalf("private legacy suppressions = %d, want 1", n)
		}
		if got, err := st.UnmatchedRecordByID(ctx, "urec-legacy-private", all); err != nil || got.SourceHandle != encryptedUnmatchedHandle {
			t.Fatalf("evacuated UnmatchedRecordByID = %#v, %v", got, err)
		}
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("retry Unseal: %v", err)
	}
	assertEvacuated()

	// A crash after the private copy but before the shared delete leaves both
	// copies; the next unseal converges.
	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	strand()
	if err := st.UnsealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("Unseal: %v", err)
	}
	assertEvacuated()
}

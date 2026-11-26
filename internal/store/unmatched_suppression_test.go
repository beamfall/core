package store

import (
	"context"
	"errors"
	"testing"
)

// queueSuppressionRecord queues the Unmatched Record a record-bearing
// suppression names: SuppressUnmatched refuses a record no plane holds.
func queueSuppressionRecord(t *testing.T, st *Store, id string) {
	t.Helper()
	if _, err := st.QueueUnmatchedRecord(context.Background(), UnmatchedRecord{
		ID: id, Origin: UnmatchedOriginScan, SourceHandle: "/media/one.mkv",
	}); err != nil {
		t.Fatalf("QueueUnmatchedRecord(%s): %v", id, err)
	}
}

// METAPARITY-7 (ADR-0068 §A): the three suppression scopes must be
// independently resettable, and each must carry the provenance the operator
// surface renders beside its own reset control.

// TestSuppressionScopesAreIndependent is the core contract: suppressing all
// three scopes and resetting ONE leaves both siblings suppressed. Resetting the
// candidate scope in particular must not un-suppress the whole record, and
// resetting the record must not restore an individually-ignored candidate.
func TestSuppressionScopesAreIndependent(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	queueSuppressionRecord(t, st, "urec-1")
	candidateKey := CandidateSuppressionKey("urec-1", "cand-9")
	all := []struct{ scope, key string }{
		{SuppressionScopeSourceRecord, "urec-1"},
		{SuppressionScopeFile, "sha256:file-1"},
		{SuppressionScopeCandidate, candidateKey},
	}
	for _, target := range all {
		if err := st.SuppressUnmatched(ctx, UnmatchedSuppression{
			Scope: target.scope, Key: target.key, RecordID: "urec-1", SourceHandle: "/media/one.mkv",
		}); err != nil {
			t.Fatalf("SuppressUnmatched(%s): %v", target.scope, err)
		}
	}

	for _, reset := range all {
		removed, err := st.ResetUnmatchedSuppression(ctx, reset.scope, reset.key)
		if err != nil || !removed {
			t.Fatalf("ResetUnmatchedSuppression(%s) = %v, %v; want a removal", reset.scope, removed, err)
		}
		for _, other := range all {
			suppressed, err := st.UnmatchedSuppressed(ctx, other.scope, other.key)
			if err != nil {
				t.Fatalf("UnmatchedSuppressed(%s): %v", other.scope, err)
			}
			want := other != reset
			if suppressed != want {
				t.Fatalf("after resetting %s, %s suppressed = %v, want %v", reset.scope, other.scope, suppressed, want)
			}
		}
		// Restore for the next iteration so each reset is tested against a
		// fully-suppressed sibling set.
		if err := st.SuppressUnmatched(ctx, UnmatchedSuppression{
			Scope: reset.scope, Key: reset.key, RecordID: "urec-1",
		}); err != nil {
			t.Fatalf("re-suppress %s: %v", reset.scope, err)
		}
	}
}

// TestCandidateSuppressionIsPerRecord keeps an ignored suggestion scoped to the
// one record it was wrong for: the same candidate stays eligible everywhere else.
func TestCandidateSuppressionIsPerRecord(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	queueSuppressionRecord(t, st, "urec-1")
	if err := st.SuppressUnmatched(ctx, UnmatchedSuppression{
		Scope:    SuppressionScopeCandidate,
		Key:      CandidateSuppressionKey("urec-1", "cand-9"),
		RecordID: "urec-1",
	}); err != nil {
		t.Fatalf("SuppressUnmatched: %v", err)
	}
	other, err := st.UnmatchedSuppressed(ctx, SuppressionScopeCandidate,
		CandidateSuppressionKey("urec-2", "cand-9"))
	if err != nil {
		t.Fatalf("UnmatchedSuppressed: %v", err)
	}
	if other {
		t.Fatal("suppressing a candidate for one record suppressed it for another record")
	}
}

// TestSuppressUnmatchedIsIdempotent covers the mockup's atomic-retry state: a
// retried suppression refreshes provenance instead of creating a second row, so
// a reset can never leave a duplicate behind.
func TestSuppressUnmatchedIsIdempotent(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	queueSuppressionRecord(t, st, "urec-1")
	for i := 0; i < 2; i++ {
		if err := st.SuppressUnmatched(ctx, UnmatchedSuppression{
			Scope: SuppressionScopeSourceRecord, Key: "urec-1",
			RecordID: "urec-1", SourceHandle: "/media/one.mkv", ActorProfileID: "profile-adult",
		}); err != nil {
			t.Fatalf("SuppressUnmatched #%d: %v", i, err)
		}
	}
	listed, err := st.UnmatchedSuppressions(ctx, "urec-1")
	if err != nil {
		t.Fatalf("UnmatchedSuppressions: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("suppressions = %d, want 1 after a retry", len(listed))
	}
	got := listed[0]
	if got.SourceHandle != "/media/one.mkv" || got.ActorProfileID != "profile-adult" || got.CreatedAt.IsZero() {
		t.Fatalf("provenance = %#v, want actor, handle, and time", got)
	}
	removed, err := st.ResetUnmatchedSuppression(ctx, SuppressionScopeSourceRecord, "urec-1")
	if err != nil || !removed {
		t.Fatalf("reset = %v, %v; want a single removal", removed, err)
	}
	if removed, err = st.ResetUnmatchedSuppression(ctx, SuppressionScopeSourceRecord, "urec-1"); err != nil || removed {
		t.Fatalf("second reset = %v, %v; want no-op", removed, err)
	}
}

// TestSuppressUnmatchedRejectsUnknownScope keeps a typo from creating a
// suppression nothing knows how to reset.
func TestSuppressUnmatchedRejectsUnknownScope(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	queueSuppressionRecord(t, st, "urec-1")
	if err := st.SuppressUnmatched(ctx, UnmatchedSuppression{Scope: "everything", Key: "urec-1"}); err == nil {
		t.Fatal("SuppressUnmatched accepted an unknown scope")
	}
	if err := st.SuppressUnmatched(ctx, UnmatchedSuppression{Scope: SuppressionScopeFile}); err == nil {
		t.Fatal("SuppressUnmatched accepted an empty key")
	}
}

// TestSuppressUnmatchedRefusesUnknownRecord keeps a suppression that names a
// record no readable plane holds (unknown, or on a sealed Encrypted Library
// plane) out of the shared catalog instead of writing it there.
func TestSuppressUnmatchedRefusesUnknownRecord(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	err := st.SuppressUnmatched(ctx, UnmatchedSuppression{
		Scope: SuppressionScopeSourceRecord, Key: "urec-missing", RecordID: "urec-missing",
		SourceHandle: "/media/secret.mkv",
	})
	if !errors.Is(err, ErrUnmatchedRecordNotFound) {
		t.Fatalf("SuppressUnmatched(unknown record) = %v, want ErrUnmatchedRecordNotFound", err)
	}
	if suppressed, err := st.UnmatchedSuppressed(ctx, SuppressionScopeSourceRecord, "urec-missing"); err != nil || suppressed {
		t.Fatalf("UnmatchedSuppressed = %v, %v; want no row written", suppressed, err)
	}
}

// TestSuppressUnmatchedFileScopeRefusesWhenEncryptedLibrarySealed ensures
// file-scoped suppressions without recordID/libraryID fail closed when any
// Encrypted Library is sealed rather than falling back globally to catalog.db,
// while confirmed plaintext catalog files still suppress cleanly.
func TestSuppressUnmatchedFileScopeRefusesWhenEncryptedLibrarySealed(t *testing.T) {
	ctx := t.Context()
	st, encMedia := seededEncryptedMedia(t, "sealed-file-suppression")
	defer func() { _ = st.Close() }()

	plainLib, err := st.CreateLibrary(ctx, "Shared Plain Library", false, "", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary(shared): %v", err)
	}
	sharedMedia, sharedFile, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: plainLib.ID, Path: "/media/shared-suppress.mkv", Title: "Shared",
		Class: MediaClassVideo, Container: "mkv", Codec: "h264", ContentFingerprint: "sha256:shared-suppress",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile(shared): %v", err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, encMedia.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}

	err = st.SuppressUnmatched(ctx, UnmatchedSuppression{
		Scope: SuppressionScopeFile, Key: "sha256:sealed-unowned", SourceHandle: "/enc/sealed.mkv",
	})
	if !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("SuppressUnmatched(file, sealed) = %v, want ErrEncryptedLibraryDBDisabled", err)
	}
	// A shared content fingerprint with a private SourceHandle from a sealed
	// Encrypted Library must also fail closed rather than leaking the path into catalog.db.
	err = st.SuppressUnmatched(ctx, UnmatchedSuppression{
		Scope: SuppressionScopeFile, Key: sharedFile.ContentFingerprint, SourceHandle: "/enc/private-copy.mkv",
	})
	if !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("SuppressUnmatched(file, shared fingerprint + sealed path) = %v, want ErrEncryptedLibraryDBDisabled", err)
	}
	if suppressed, err := st.UnmatchedSuppressed(ctx, SuppressionScopeFile, "sha256:sealed-unowned"); err != nil || suppressed {
		t.Fatalf("sealed file suppression leaked to catalog: %v, %v", suppressed, err)
	}

	if err := st.SuppressUnmatched(ctx, UnmatchedSuppression{
		Scope: SuppressionScopeFile, Key: sharedFile.ContentFingerprint, SourceHandle: sharedFile.Path,
	}); err != nil {
		t.Fatalf("SuppressUnmatched(file, shared catalog %s): %v", sharedMedia.ID, err)
	}
	if suppressed, err := st.UnmatchedSuppressed(ctx, SuppressionScopeFile, sharedFile.ContentFingerprint); err != nil || !suppressed {
		t.Fatalf("shared file suppression = %v, %v; want true", suppressed, err)
	}
}

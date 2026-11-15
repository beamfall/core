package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeKeyEraser stands in for libencrypt.Manager.Erase.
type fakeKeyEraser struct {
	mu    sync.Mutex
	calls []LibraryID
	err   error
}

func (e *fakeKeyEraser) EraseLibraryKeys(libraryID LibraryID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, libraryID)
	return e.err
}

func (e *fakeKeyEraser) setErr(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.err = err
}

func (e *fakeKeyEraser) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

// trashedEncryptedLibrary returns a store whose seeded Library is encrypted,
// unsealed, holds one Media, has a ciphertext file, a plaintext working copy
// with its WAL/SHM, and a pre-migration backup, and has been soft-deleted.
func trashedEncryptedLibrary(t *testing.T) (*Store, LibraryID, Media) {
	t.Helper()
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "vault-clip")
	t.Cleanup(func() { _ = st.Close() })
	id := media.LibraryID
	// Seal once so a ciphertext exists, then unseal to reopen the plane.
	if err := st.SealEncryptedLibraryDB(ctx, id, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, id, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	backupDir := st.encryptedLibraryBackupDir(id)
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "encrypted_library.db.bak"), []byte("old ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := st.encryptedLibraryDBPaths(id)
	for _, path := range []string{paths.ciphertext, paths.working, paths.working + "-wal", paths.working + "-shm"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("fixture is missing %s: %v", path, err)
		}
	}
	if err := st.DeleteLibrary(ctx, id); err != nil {
		t.Fatalf("DeleteLibrary: %v", err)
	}
	return st, id, media
}

func assertEncryptedLibraryFilesGone(t *testing.T, st *Store, id LibraryID) {
	t.Helper()
	paths := st.encryptedLibraryDBPaths(id)
	for _, path := range []string{filepath.Dir(paths.ciphertext), filepath.Dir(paths.working), st.encryptedLibraryBackupDir(id)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s survived the purge (stat err %v)", path, err)
		}
	}
	if st.EncryptedLibraryDBRegistered(id) {
		t.Fatal("purged library's private database is still registered")
	}
}

func serverConfigKeyExists(t *testing.T, st *Store, key string) bool {
	t.Helper()
	var n int
	if err := st.catalogR.QueryRowContext(context.Background(), `SELECT count(*) FROM server_config WHERE key = ?`, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n != 0
}

func TestPurgeEncryptedLibraryRemovesPrivateDatabaseAndKeys(t *testing.T) {
	ctx := context.Background()
	st, id, media := trashedEncryptedLibrary(t)
	eraser := &fakeKeyEraser{}
	st.SetEncryptedLibraryKeyEraser(eraser)
	trashed, err := st.TrashedLibraries(ctx, revealedAll)
	if err != nil || len(trashed) != 1 || !trashed[0].Purgeable() {
		t.Fatalf("trashed = %+v, err %v; want the encrypted library listed as purgeable", trashed, err)
	}

	purge, err := st.PurgeLibrary(ctx, id)
	if err != nil {
		t.Fatalf("PurgeLibrary(encrypted): %v", err)
	}
	if !slices.Equal(purge.MediaIDs, []MediaID{media.ID}) {
		t.Fatalf("purge.MediaIDs = %v, want the private database's media %s", purge.MediaIDs, media.ID)
	}
	assertEncryptedLibraryFilesGone(t, st, id)
	if !slices.Equal(eraser.calls, []LibraryID{id}) {
		t.Fatalf("key eraser calls = %v, want one for %s", eraser.calls, id)
	}
	if serverConfigKeyExists(t, st, libraryPurgePendingConfigPrefix+string(id)) {
		t.Fatal("a completed purge left its pending row")
	}
	if !serverConfigKeyExists(t, st, libraryPurgedConfigPrefix+string(id)) {
		t.Fatal("encrypted purge wrote no tombstone")
	}
	if got := trashedLibraryIDs(t, st, revealedAll); len(got) != 0 {
		t.Fatalf("trashed after purge = %v, want none", got)
	}
}

func TestPurgeEncryptedLibraryWithoutKeyEraserIsRefusedAndRestorable(t *testing.T) {
	ctx := context.Background()
	st, id, _ := trashedEncryptedLibrary(t)

	var validation ValidationError
	if _, err := st.PurgeLibrary(ctx, id); !errors.As(err, &validation) {
		t.Fatalf("PurgeLibrary with no key eraser = %v, want a validation error", err)
	}
	purged, skipped, err := st.SweepPurgeTrashedLibrariesBefore(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil || skipped || len(purged) != 0 {
		t.Fatalf("sweep with no key eraser = %v, skipped %v, err %v; want the library left alone", purged, skipped, err)
	}
	// Nothing is touched before the catalog commit, so the refused Library is whole.
	paths := st.encryptedLibraryDBPaths(id)
	for _, path := range []string{paths.ciphertext, paths.working} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("refused purge removed %s: %v", path, err)
		}
	}
	if serverConfigKeyExists(t, st, libraryPurgePendingConfigPrefix+string(id)) {
		t.Fatal("refused purge wrote a pending row")
	}
	if err := st.RestoreLibrary(ctx, id); err != nil {
		t.Fatalf("RestoreLibrary after a refused purge: %v", err)
	}
	if !st.EncryptedLibraryDBRegistered(id) {
		t.Fatal("restored library lost its private database")
	}

	// With the eraser installed the retention sweep purges it and audits it.
	if err := st.DeleteLibrary(ctx, id); err != nil {
		t.Fatal(err)
	}
	eraser := &fakeKeyEraser{}
	st.SetEncryptedLibraryKeyEraser(eraser)
	purged, skipped, err = st.SweepPurgeTrashedLibrariesBefore(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil || skipped || len(purged) != 1 || purged[0].LibraryID != id {
		t.Fatalf("sweep = %+v, skipped %v, err %v; want the encrypted library purged", purged, skipped, err)
	}
	assertEncryptedLibraryFilesGone(t, st, id)
	if eraser.callCount() != 1 {
		t.Fatalf("key eraser calls = %v, want one", eraser.calls)
	}
	entries, err := st.ActivityEntries(ctx, ActivityQuery{Filter: revealedAll})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(entries, func(entry ActivityEntry) bool {
		return entry.Action == ActivityLibraryPurge && entry.TargetID == string(id)
	}) {
		t.Fatalf("no library.purge activity record for %s", id)
	}
}

func TestPurgeEncryptedLibraryFailurePartwayIsFinishedByTheNextSweep(t *testing.T) {
	ctx := context.Background()
	st, id, _ := trashedEncryptedLibrary(t)
	eraser := &fakeKeyEraser{err: errors.New("secure store unavailable")}
	st.SetEncryptedLibraryKeyEraser(eraser)
	originalCleanup := cleanupEncryptedLibraryWorkingDir
	cleanupEncryptedLibraryWorkingDir = func(string, string) error { return errors.New("working copy busy") }
	t.Cleanup(func() { cleanupEncryptedLibraryWorkingDir = originalCleanup })

	// The catalog commit is the point of no return: once it lands the Library is
	// purged even though both teardown steps failed, and the pending row owes them.
	if _, err := st.PurgeLibrary(ctx, id); err != nil {
		t.Fatalf("PurgeLibrary with failing teardown: %v", err)
	}
	if got := trashedLibraryIDs(t, st, revealedAll); len(got) != 0 {
		t.Fatalf("trashed = %v; a committed purge must not leave the library half listed", got)
	}
	if !serverConfigKeyExists(t, st, libraryPurgePendingConfigPrefix+string(id)) {
		t.Fatal("failed teardown left no pending row")
	}
	if eraser.callCount() != 1 {
		t.Fatalf("keys were not erased despite the file failure: calls %v", eraser.calls)
	}
	paths := st.encryptedLibraryDBPaths(id)
	if _, err := os.Stat(paths.ciphertext); !os.IsNotExist(err) {
		t.Fatalf("ciphertext survived although its removal did not fail: %v", err)
	}

	cleanupEncryptedLibraryWorkingDir = originalCleanup
	eraser.setErr(nil)
	if _, _, err := st.SweepPurgeTrashedLibrariesBefore(ctx, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	assertEncryptedLibraryFilesGone(t, st, id)
	if serverConfigKeyExists(t, st, libraryPurgePendingConfigPrefix+string(id)) {
		t.Fatal("sweep did not clear the pending row")
	}
	if eraser.callCount() != 2 {
		t.Fatalf("key eraser calls = %v, want the retry", eraser.calls)
	}
}

func TestCompletePendingLibraryPurgesFinishesACrashStrandedPurgeAtStart(t *testing.T) {
	ctx := context.Background()
	st, id, _ := trashedEncryptedLibrary(t)
	// Commit the catalog purge and stop, as a crash right after the commit would.
	st.mu.RLock()
	_, encrypted, err := st.purgeLibraryLocked(ctx, id, 1<<62, true)
	st.mu.RUnlock()
	if err != nil || !encrypted {
		t.Fatalf("purgeLibraryLocked = encrypted %v, err %v", encrypted, err)
	}
	dataDir := st.dataDir
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, dataDir, Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	eraser := &fakeKeyEraser{}
	reopened.SetEncryptedLibraryKeyEraser(eraser)
	if err := reopened.CompletePendingLibraryPurges(ctx); err != nil {
		t.Fatalf("CompletePendingLibraryPurges: %v", err)
	}
	assertEncryptedLibraryFilesGone(t, reopened, id)
	if _, quarantined := reopened.EncryptedLibraryDBQuarantine(id); quarantined {
		t.Fatal("startup quarantine of the purged library's working copy survived")
	}
	if !slices.Equal(eraser.calls, []LibraryID{id}) {
		t.Fatalf("key eraser calls = %v, want one for %s", eraser.calls, id)
	}
	if serverConfigKeyExists(t, reopened, libraryPurgePendingConfigPrefix+string(id)) {
		t.Fatal("pending row survived completion")
	}
	// Nothing left to do: a repeat is a no-op.
	if err := reopened.CompletePendingLibraryPurges(ctx); err != nil || eraser.callCount() != 1 {
		t.Fatalf("repeat completion = %v, eraser calls %v", err, eraser.calls)
	}
}

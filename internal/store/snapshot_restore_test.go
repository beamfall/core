package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSnapshotRestorePreservesDataIntegrity verifies that backing up a
// dev-seeded store and restoring it to a new directory produces a fully
// functional store with identical schema versions and data.
//
// This catches restore regressions where catalog.db is not copied: in
// that case Open creates a fresh, empty database, so media rows are
// absent even though the restore appeared to succeed.
func TestSnapshotRestorePreservesDataIntegrity(t *testing.T) {
	ctx := context.Background()

	// Open and seed the source store. The backup destination must sit within the
	// store's sanctioned path allowlist, so register it up front.
	sourceDir := t.TempDir()
	backupDest := t.TempDir()
	st, err := Open(ctx, sourceDir, Options{SeedDevelopmentData: true, AllowedRoots: []string{backupDest}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	installBackupTestSealer(st, sourceDir)

	// Write a sentinel job to events.db so we can verify it survives restore.
	const sentinelJob = "sentinel-restore-job"
	if _, err := st.EnqueueJob(ctx, Job{
		ID:        sentinelJob,
		Kind:      "scan",
		CreatedAt: time.Unix(42, 0).UTC(),
	}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	// Capture expected schema versions before backup.
	wantCatalogVersion := migrationFileCount(t, "migrate/catalog")
	wantEventsVersion := migrationFileCount(t, "migrate/events")

	// Perform the backup.
	result, err := st.Backup(ctx, backupDest)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if result.Envelope == "" || result.Manifest != result.Envelope {
		t.Fatalf("backup result is not an encrypted set: %#v", result)
	}
	entries, err := os.ReadDir(backupDest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(result.Directory) {
		t.Fatalf("backup destination retained plaintext staging: %v", entries)
	}
	if err := filepath.WalkDir(result.Directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path) //nolint:gosec // test reads only its temporary backup set
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte("SQLite format 3\x00")) {
			t.Fatalf("published backup contains plaintext SQLite bytes: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Verify manifest schema versions match the embedded migration counts.
	if result.Catalog.SchemaVersion != wantCatalogVersion {
		t.Errorf("backup catalog schema = %d, want %d", result.Catalog.SchemaVersion, wantCatalogVersion)
	}
	if result.Events.SchemaVersion != wantEventsVersion {
		t.Errorf("backup events schema = %d, want %d", result.Events.SchemaVersion, wantEventsVersion)
	}

	// Restore to an empty directory.
	restoreDir := t.TempDir()
	opener, _ := bootstrapBackupTestCredentials(t, sourceDir, time.Now().UTC())
	if _, err := RestoreBackupWithOptions(ctx, result.Manifest, t.TempDir(), RestoreOptions{}); err == nil {
		t.Fatal("encrypted restore without a BackupKey succeeded")
	}
	if _, err := RestoreBackupWithOptions(ctx, result.Manifest, restoreDir, RestoreOptions{SnapshotOpener: opener}); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}

	// Open the restored store.
	restored, err := Open(ctx, restoreDir, Options{})
	if err != nil {
		t.Fatalf("Open restored store: %v", err)
	}
	defer restored.Close()

	// Schema version in the restored catalog must match the backup.
	gotCatalogVersion, err := userVersion(ctx, restored.catalogW)
	if err != nil {
		t.Fatalf("userVersion catalog: %v", err)
	}
	if gotCatalogVersion != wantCatalogVersion {
		t.Errorf("restored catalog user_version = %d, want %d", gotCatalogVersion, wantCatalogVersion)
	}

	gotEventsVersion, err := userVersion(ctx, restored.eventsW)
	if err != nil {
		t.Fatalf("userVersion events: %v", err)
	}
	if gotEventsVersion != wantEventsVersion {
		t.Errorf("restored events user_version = %d, want %d", gotEventsVersion, wantEventsVersion)
	}

	// The sentinel job must exist in the restored events DB.
	var count int
	if err := restored.eventsW.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM job WHERE id = ?", sentinelJob,
	).Scan(&count); err != nil {
		t.Fatalf("query restored events DB: %v", err)
	}
	if count != 1 {
		t.Errorf("restored sentinel job rows = %d, want 1 (catalog.db not copied?)", count)
	}

	// The media table must exist in the restored catalog (dev seed populates it).
	if !tableExists(t, restored.catalogW, "media") {
		t.Error("restored catalog missing media table (catalog.db not restored?)")
	}
}

func TestMigrationSessionSnapshotCapturesAllDatabasesBeforeMigrations(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	const libraryComponent = "library-secure"
	ciphertext := []byte("sealed encrypted-library database artifact")
	ciphertextDir := filepath.Join(dataDir, "db", "encrypted-library", libraryComponent)
	if err := os.MkdirAll(ciphertextDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ciphertextDir, "encrypted_library"+encryptedLibraryDBCipherExt), ciphertext, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sessions := migrationSessionDirectories(t, filepath.Join(dataDir, "backups", "pre-migration"))
	if len(sessions) != 1 {
		t.Fatalf("migration sessions = %v, want exactly one", sessions)
	}
	raw, err := os.ReadFile(filepath.Join(sessions[0], backupManifestBasename))
	if err != nil {
		t.Fatal(err)
	}
	var manifest migrationSessionManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Type != migrationSnapshotType || manifest.RestoreInstructionsVersion != migrationSnapshotVersion {
		t.Fatalf("migration manifest = %#v", manifest)
	}
	for _, name := range []string{"catalog", "events"} {
		snapshot, err := openSQLiteReader(filepath.Join(sessions[0], name+".db"))
		if err != nil {
			t.Fatal(err)
		}
		version, versionErr := userVersion(ctx, snapshot)
		closeErr := snapshot.Close()
		if versionErr != nil || closeErr != nil || version != 0 {
			t.Fatalf("%s pre-migration version=%d versionErr=%v closeErr=%v", name, version, versionErr, closeErr)
		}
	}
	got, err := os.ReadFile(filepath.Join(sessions[0], "encrypted-library", libraryComponent, "encrypted_library"+encryptedLibraryDBCipherExt))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, ciphertext) {
		t.Fatalf("encrypted-library snapshot = %q, want %q", got, ciphertext)
	}
}

func TestMigrationSnapshotSoakPrunesOldestOnlyAfterSuccessfulBoots(t *testing.T) {
	dataDir := t.TempDir()
	backupDir := filepath.Join(dataDir, "backups", "pre-migration")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for index, id := range []string{"oldest", "prior", "newest"} {
		path := filepath.Join(backupDir, "session-"+id)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		manifest := migrationSessionManifest{
			Type:                       migrationSnapshotType,
			RestoreInstructionsVersion: migrationSnapshotVersion,
			SessionID:                  id,
			CreatedAt:                  time.Date(2026, 1, index+1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		}
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, backupManifestBasename), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	for boot := 1; boot <= migrationSnapshotSoakBoots; boot++ {
		if err := recordSuccessfulMigrationBoot(dataDir); err != nil {
			t.Fatal(err)
		}
		if boot < migrationSnapshotSoakBoots {
			if _, err := os.Stat(paths[0]); err != nil {
				t.Fatalf("oldest snapshot pruned before soak boot %d: %v", boot, err)
			}
		}
	}
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatalf("oldest snapshot survived completed soak: %v", err)
	}
	for _, kept := range paths[1:] {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("retention removed current floor/newest %s: %v", kept, err)
		}
	}
}

func TestFailedMigrationBootDoesNotAdvanceSnapshotSoak(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dataDir, "backups", "pre-migration", migrationSoakStateFile)
	before, err := os.ReadFile(statePath) //nolint:gosec // G304: statePath is test-local beneath dataDir from t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	db, err := openSQLiteWriter(filepath.Join(dataDir, "db", "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 999999"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, dataDir, Options{}); err == nil {
		t.Fatal("newer-schema migration boot unexpectedly succeeded")
	}
	after, err := os.ReadFile(statePath) //nolint:gosec // G304: statePath is test-local beneath dataDir from t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("failed boot advanced soak state: before=%s after=%s", before, after)
	}
}

func TestMigrationSessionSnapshotResumesAfterCrashBetweenDatabases(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	dbDir := filepath.Join(dataDir, "db")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(dbDir, "catalog.db")
	eventsPath := filepath.Join(dbDir, "events.db")
	catalog, err := openSQLiteWriter(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	events, err := openSQLiteWriter(eventsPath)
	if err != nil {
		if closeErr := catalog.Close(); closeErr != nil {
			t.Fatalf("open events: %v; close catalog: %v", err, closeErr)
		}
		t.Fatal(err)
	}
	backupDir := filepath.Join(dataDir, "backups", "pre-migration")
	if err := migrate(ctx, slog.Default(), catalog, catalogPath, "catalog", catalogMigrations, "migrate/catalog", backupDir); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
	if err := events.Close(); err != nil {
		t.Fatal(err)
	}

	// Process termination here leaves catalog migrated and events untouched.
	resumed, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatalf("resume after between-database crash: %v", err)
	}
	defer resumed.Close()
	if sessions := migrationSessionDirectories(t, backupDir); len(sessions) != 1 {
		t.Fatalf("resume produced %d migration sessions, want one: %v", len(sessions), sessions)
	}
	for name, db := range map[string]*sql.DB{"catalog": resumed.catalogW, "events": resumed.eventsW} {
		version, err := userVersion(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if version != migrationTargetVersions()[name] {
			t.Fatalf("%s version after resume = %d, want %d", name, version, migrationTargetVersions()[name])
		}
	}
}

func TestRollbackMigrationSnapshotRestoresAllDBsWithoutKey(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	identityPath := filepath.Join(dataDir, "secrets", "identity.key")
	if err := os.MkdirAll(filepath.Dir(identityPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identityPath, []byte("keep-identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	sessions := migrationSessionDirectories(t, filepath.Join(dataDir, "backups", "pre-migration"))
	if len(sessions) != 1 {
		t.Fatalf("migration sessions = %v", sessions)
	}
	previous, err := RollbackMigrationSnapshot(ctx, filepath.Join(sessions[0], backupManifestBasename), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if previous == "" {
		t.Fatal("rollback did not retain replaced DB tree")
	}
	for _, name := range []string{"catalog", "events"} {
		db, err := openSQLiteReader(filepath.Join(dataDir, "db", name+".db"))
		if err != nil {
			t.Fatal(err)
		}
		version, versionErr := userVersion(ctx, db)
		closeErr := db.Close()
		if versionErr != nil || closeErr != nil || version != 0 {
			t.Fatalf("%s rollback version=%d versionErr=%v closeErr=%v", name, version, versionErr, closeErr)
		}
	}
	identity, err := os.ReadFile(identityPath) //nolint:gosec // G304: identityPath is test-local beneath dataDir from t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if string(identity) != "keep-identity" {
		t.Fatalf("rollback changed identity: %q", identity)
	}
}

func migrationSessionDirectories(t *testing.T, backupDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	dirs := []string{}
	for _, entry := range entries {
		if entry.IsDir() && stringsHasSessionPrefix(entry.Name()) {
			dirs = append(dirs, filepath.Join(backupDir, entry.Name()))
		}
	}
	return dirs
}

package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupCreatesPairedDatabaseSnapshotsAndManifest(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.EnqueueJob(ctx, Job{ID: "job-backup-test", Kind: "scan", CreatedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	result, err := st.backupPlaintextForTest(ctx, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result.Directory + ".partial"); !os.IsNotExist(err) {
		t.Fatalf("partial backup directory still exists: %v", err)
	}

	for _, path := range []string{result.Catalog.BackupPath, result.Events.BackupPath, result.Manifest} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", path, info.Mode().Perm())
		}
	}
	data, err := os.ReadFile(result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var manifest backupSetManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	wantCatalog := migrationFileCount(t, "migrate/catalog")
	wantEvents := migrationFileCount(t, "migrate/events")
	if manifest.SchemaVersions["catalog"] != wantCatalog {
		t.Fatalf("catalog schema = %d, want %d (migration count)", manifest.SchemaVersions["catalog"], wantCatalog)
	}
	if manifest.SchemaVersions["events"] != wantEvents {
		t.Fatalf("events schema = %d, want %d (migration count)", manifest.SchemaVersions["events"], wantEvents)
	}
	if manifest.CreatedBy != "beamfall-store" || manifest.RestoreInstructionsVersion != backupRestoreInstructionsVersion {
		t.Fatalf("manifest metadata = %#v", manifest)
	}
	if manifest.Databases["catalog"].BackupPath != result.Catalog.BackupPath {
		t.Fatal("manifest did not pair catalog snapshot")
	}
	if manifest.Databases["events"].BackupPath != result.Events.BackupPath {
		t.Fatal("manifest did not pair events snapshot")
	}
	if manifest.Databases["catalog"].SizeBytes == 0 || manifest.Databases["catalog"].SHA256 == "" {
		t.Fatal("catalog snapshot missing size/checksum")
	}
	if manifest.Databases["events"].SizeBytes == 0 || manifest.Databases["events"].SHA256 == "" {
		t.Fatal("events snapshot missing size/checksum")
	}
	if len(result.EncryptedLibraries) != 0 {
		t.Fatalf("encrypted-library snapshots = %d, want 0", len(result.EncryptedLibraries))
	}
	for key := range manifest.Databases {
		if strings.HasPrefix(key, "encrypted_library/") {
			t.Fatalf("unexpected encrypted-library manifest entry %q", key)
		}
	}

	catalog, err := openSQLite(result.Catalog.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	if !tableExists(t, catalog, "media") {
		t.Fatal("catalog snapshot missing media table")
	}

	events, err := openSQLite(result.Events.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	var count int
	if err := events.QueryRowContext(ctx, "SELECT COUNT(*) FROM job WHERE id = ?", "job-backup-test").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("backup job rows = %d, want 1", count)
	}
}

// TestBackupWithOptionsRejectsPathOutsideAllowedRoots confines the backup
// destination to the sanctioned path allowlist (PathPicker allowedRoots of
// ADR-0116, plus the store data dir) in the store layer, so the guard holds
// for every caller — not only graph dispatch. A path outside every sanctioned
// root, such as /tmp/escape, is rejected with a ValidationError before any
// filesystem mutation; a dest inside an allowed root is accepted. (CAUDIT-1,
// hard invariants 5 & 6.)
func TestBackupWithOptionsRejectsPathOutsideAllowedRoots(t *testing.T) {
	ctx := context.Background()
	allowedDir := t.TempDir()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, AllowedRoots: []string{allowedDir}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	escape := filepath.Join(t.TempDir(), "escape")
	if _, err := st.backupPlaintextWithOptions(ctx, escape, BackupOptions{}); err == nil {
		t.Fatalf("BackupWithOptions(%q) succeeded, want rejection outside allowlist", escape)
	} else {
		var validation ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("BackupWithOptions(%q) err = %v (%T), want ValidationError", escape, err, err)
		}
		if entries, statErr := os.ReadDir(escape); statErr == nil && len(entries) != 0 {
			t.Fatalf("rejected backup left entries under %q: %v", escape, entries)
		}
	}

	// A `..` escape that resolves outside every sanctioned root is rejected too
	// (canonical absolute path enforced). Keep this as a literal string:
	// filepath.Join pre-cleans and would fail to exercise the collapse.
	traversal := allowedDir + string(filepath.Separator) + ".." + string(filepath.Separator) + "sibling-escape"
	if _, err := st.backupPlaintextWithOptions(ctx, traversal, BackupOptions{}); err == nil {
		t.Fatalf("BackupWithOptions(%q) succeeded, want rejection of non-canonical escape", traversal)
	}

	// A destination inside the allowlist is accepted.
	if _, err := st.backupPlaintextForTest(ctx, allowedDir); err != nil {
		t.Fatalf("Backup into allowed root %q: %v", allowedDir, err)
	}
}

func TestBackupWithOptionsRejectsSymlinkEscapeAndRootAllowlist(t *testing.T) {
	ctx := context.Background()
	allowedDir := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(allowedDir, "outside-link")); err != nil {
		t.Fatal(err)
	}
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, AllowedRoots: []string{allowedDir}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.backupPlaintextWithOptions(ctx, filepath.Join(allowedDir, "outside-link", "backup"), BackupOptions{}); err == nil {
		t.Fatal("symlink escape backup succeeded, want rejection")
	}

	rootStore := &Store{dataDir: string(filepath.Separator), allowedRoots: []string{string(filepath.Separator)}}
	if _, err := rootStore.backupPlaintextWithOptions(ctx, filepath.Join(t.TempDir(), "backup"), BackupOptions{}); err == nil {
		t.Fatal("backup with root dataDir/allowlist succeeded, want rejection")
	}
}

func TestConfinedBackupPathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := confinedBackupPath(root, filepath.Join(root, "escape", "set.bbk")); err == nil {
		t.Fatal("backup path through an escaping symlink succeeded, want rejection")
	}
}

func TestConfinedBackupPathRejectsReplacedRoot(t *testing.T) {
	container := t.TempDir()
	root := filepath.Join(container, "root")
	moved := filepath.Join(container, "moved")
	outside := t.TempDir()
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	if _, err := confinedBackupPath(root, filepath.Join(root, "set.bbk")); err == nil {
		t.Fatal("backup path beneath a replaced root succeeded, want rejection")
	}
	if _, err := os.Stat(filepath.Join(outside, "set.bbk")); !os.IsNotExist(err) {
		t.Fatalf("replaced backup root wrote outside the intended root: %v", err)
	}
}

func TestBackupWithOptionsRejectsBeforeMkdirAll(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, AllowedRoots: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dest := filepath.Join(t.TempDir(), "outside", "backup")
	if _, err := st.backupPlaintextWithOptions(ctx, dest, BackupOptions{}); err == nil {
		t.Fatal("outside backup succeeded, want rejection")
	}
	if _, err := os.Stat(filepath.Dir(dest)); !os.IsNotExist(err) {
		t.Fatalf("guard created rejected parent dir: %v", err)
	}
}

func TestBackupIncludesEncryptedLibraryCiphertextSnapshotsAndManifest(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	cipher := wrappingEncryptedLibraryDBCipher{}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	media, _, err := st.ImportMediaFile(ctx, encryptedImport(libraryID, "sha256:backup-encrypted", "/enc/backup.mkv"))
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}

	result, err := st.backupPlaintextWithOptions(ctx, backupDir, BackupOptions{EncryptedLibraryDBCipher: cipher})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.EncryptedLibraries) != 1 {
		t.Fatalf("encrypted-library snapshots = %d, want 1", len(result.EncryptedLibraries))
	}
	snapshot := result.EncryptedLibraries[0]
	if snapshot.Database != encryptedLibraryDBLabel || snapshot.LibraryID != string(libraryID) {
		t.Fatalf("encrypted snapshot identity = %+v", snapshot)
	}
	if snapshot.MigrationDir != "migrate/encrypted_library" || snapshot.SchemaVersion != migrationFileCount(t, "migrate/encrypted_library") {
		t.Fatalf("encrypted snapshot migration metadata = %+v", snapshot)
	}
	if snapshot.CipherFormat != encryptedLibraryDBCipherExt || snapshot.State != "unsealed-snapshot-encrypted" {
		t.Fatalf("encrypted snapshot cipher metadata = %+v", snapshot)
	}
	if filepath.Base(snapshot.BackupPath) == "working.db" || strings.Contains(snapshot.BackupPath, "encrypted-library-working") {
		t.Fatalf("encrypted snapshot archived plaintext path %q", snapshot.BackupPath)
	}
	info, err := os.Stat(snapshot.BackupPath)
	if err != nil {
		t.Fatalf("stat encrypted snapshot: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("encrypted snapshot mode = %v, want 0600", info.Mode().Perm())
	}
	ciphertext, err := os.ReadFile(snapshot.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(ciphertext, []byte("SQLite format 3")) {
		t.Fatal("encrypted-library backup snapshot is plaintext SQLite")
	}

	data, err := os.ReadFile(result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var manifest backupSetManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	key := "encrypted_library/" + string(libraryID)
	if manifest.Databases[key].BackupPath != snapshot.BackupPath {
		t.Fatalf("manifest encrypted backup path = %q, want %q", manifest.Databases[key].BackupPath, snapshot.BackupPath)
	}
	if manifest.SchemaVersions[key] != snapshot.SchemaVersion {
		t.Fatalf("manifest encrypted schema = %d, want %d", manifest.SchemaVersions[key], snapshot.SchemaVersion)
	}
	if manifest.ConsistencyProtocol == "" {
		t.Fatal("manifest missing consistency protocol")
	}

	decrypted := filepath.Join(t.TempDir(), "decrypted.db")
	if err := cipher.UnsealDBFile(ctx, libraryID, snapshot.BackupPath, decrypted); err != nil {
		t.Fatalf("decrypt backup snapshot: %v", err)
	}
	db, err := openSQLite(decrypted)
	if err != nil {
		t.Fatalf("open decrypted snapshot: %v", err)
	}
	defer db.Close()
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media WHERE id = ?`, media.ID).Scan(&rows); err != nil {
		t.Fatalf("query decrypted snapshot: %v", err)
	}
	if rows != 1 {
		t.Fatalf("decrypted encrypted-library rows = %d, want 1", rows)
	}
	assertNoPlaintextEncryptedBackupFiles(t, result.Directory)
}

func TestBackupCleansPlaintextSnapshotWhenCipherLeavesInputIntact(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	dataDir := t.TempDir()
	st, err := Open(ctx, dataDir, Options{
		SeedDevelopmentData:               true,
		EnableEncryptedLibraryDBLifecycle: true,
		AllowedRoots:                      []string{backupDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	installBackupTestSealer(st, dataDir)

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	cipher := preservingEncryptedLibraryDBCipher{}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	if _, _, err := st.ImportMediaFile(ctx, encryptedImport(libraryID, "sha256:backup-preserving-cipher", "/enc/preserving.mkv")); err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}

	result, err := st.BackupWithOptions(ctx, backupDir, BackupOptions{EncryptedLibraryDBCipher: cipher})
	if err != nil {
		t.Fatalf("BackupWithOptions: %v", err)
	}
	assertNoPlaintextEncryptedBackupFiles(t, result.Directory)
}

func TestBackupSealedEncryptedLibraryStillManifestsCiphertext(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	cipher := wrappingEncryptedLibraryDBCipher{}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	if _, _, err := st.ImportMediaFile(ctx, encryptedImport(libraryID, "sha256:sealed-backup", "/enc/sealed.mkv")); err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}

	result, err := st.backupPlaintextForTest(ctx, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.EncryptedLibraries) != 1 {
		t.Fatalf("encrypted-library snapshots = %d, want 1", len(result.EncryptedLibraries))
	}
	snapshot := result.EncryptedLibraries[0]
	if snapshot.State != "sealed-ciphertext" || snapshot.SchemaVersion != migrationFileCount(t, "migrate/encrypted_library") {
		t.Fatalf("sealed encrypted snapshot = %+v", snapshot)
	}
	if _, err := os.Stat(snapshot.BackupPath); err != nil {
		t.Fatalf("stat sealed snapshot: %v", err)
	}
	assertNoPlaintextEncryptedBackupFiles(t, result.Directory)
}

func TestRestoreBackupReconstructsEncryptedLibraryCiphertextAndSchemaMetadata(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	cipher := wrappingEncryptedLibraryDBCipher{}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	media, _, err := st.ImportMediaFile(ctx, encryptedImport(libraryID, "sha256:restore-backup", "/enc/restore.mkv"))
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	result, err := st.backupPlaintextWithOptions(ctx, backupDir, BackupOptions{EncryptedLibraryDBCipher: cipher})
	if err != nil {
		t.Fatal(err)
	}

	restoreDir := t.TempDir()
	if err := RestoreBackup(ctx, result.Manifest, restoreDir); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	restored, err := Open(ctx, restoreDir, Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open restored store: %v", err)
	}
	defer restored.Close()
	if err := restored.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("Unseal restored encrypted-library DB: %v", err)
	}
	db := restored.encryptedDBs[libraryID]
	var rows int
	if err := db.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM media WHERE id = ?`, media.ID).Scan(&rows); err != nil {
		t.Fatalf("query restored encrypted-library DB: %v", err)
	}
	if rows != 1 {
		t.Fatalf("restored encrypted-library rows = %d, want 1", rows)
	}
}

func TestRestoreBackupDrillSeededStoreRowParity(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	source, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.EnqueueJob(ctx, Job{ID: "job-restore-drill", Kind: "scan", CreatedAt: time.Unix(200, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	wantCatalogRows := tableRows(t, source.catalogR, "media")
	wantEventRows := tableRows(t, source.eventsR, "job")
	result, err := source.backupPlaintextForTest(ctx, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	restoreDir := t.TempDir()
	if err := RestoreBackup(ctx, result.Manifest, restoreDir); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	restored, err := Open(ctx, restoreDir, Options{})
	if err != nil {
		t.Fatalf("Open restored store: %v", err)
	}
	defer restored.Close()
	if got := tableRows(t, restored.catalogR, "media"); got != wantCatalogRows {
		t.Fatalf("restored media rows = %d, want %d", got, wantCatalogRows)
	}
	if got := tableRows(t, restored.eventsR, "job"); got != wantEventRows {
		t.Fatalf("restored job rows = %d, want %d", got, wantEventRows)
	}
}

func TestBackupQuarantinedEncryptedLibraryFailsClosed(t *testing.T) {
	dataDir := t.TempDir()
	workingDir := filepath.Join(dataDir, "db", "encrypted-library-working", "library-crash")
	if err := os.MkdirAll(workingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workingDir, "working.db-wal"), []byte("plaintext wal"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	st, err := Open(context.Background(), dataDir, Options{EnableEncryptedLibraryDBLifecycle: true, AllowedRoots: []string{dest}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.backupPlaintextWithOptions(context.Background(), dest, BackupOptions{EncryptedLibraryDBCipher: wrappingEncryptedLibraryDBCipher{}}); !errors.Is(err, ErrEncryptedLibraryDBQuarantined) {
		t.Fatalf("BackupWithOptions err = %v, want ErrEncryptedLibraryDBQuarantined", err)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("backup destination entries after fail-closed backup = %v, want none", entries)
	}
}

func TestBackupAfterCloseReturnsErrClosed(t *testing.T) {
	backupDir := t.TempDir()
	st, err := Open(context.Background(), t.TempDir(), Options{AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.backupPlaintextForTest(context.Background(), filepath.Join(backupDir, "backup")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Backup after Close err = %v, want ErrClosed", err)
	}
}

func TestVerifyBackupSetRejectsMissingRequiredDatabases(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	st, err := Open(ctx, t.TempDir(), Options{AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	setDir := filepath.Join(backupDir, "beamfall-incomplete")
	if err := os.Mkdir(setDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := backupSetManifest{
		RestoreInstructionsVersion: 1,
		ConsistencyProtocol:        "test snapshot",
		Databases:                  map[string]BackupDatabaseSnapshot{},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(setDir, backupManifestBasename)
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyBackupSet(ctx, manifestPath); err == nil {
		t.Fatal("VerifyBackupSet accepted a manifest without catalog/events snapshots")
	}
}

func TestVerifyBackupSetRejectsSnapshotSymlinkEscape(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	st, err := Open(ctx, t.TempDir(), Options{AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	setDir := filepath.Join(backupDir, "beamfall-symlink")
	if err := os.Mkdir(setDir, 0o700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external.db")
	if err := os.WriteFile(external, []byte("external catalog bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(setDir, "catalog.db")); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(setDir, "events.db")
	if err := os.WriteFile(eventsPath, []byte("events bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalogSize, catalogSHA, err := fileSizeAndSHA256(external)
	if err != nil {
		t.Fatal(err)
	}
	eventsSize, eventsSHA, err := fileSizeAndSHA256(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := backupSetManifest{
		RestoreInstructionsVersion: 1,
		ConsistencyProtocol:        "test snapshot",
		Databases: map[string]BackupDatabaseSnapshot{
			"catalog": {Database: "catalog", SizeBytes: catalogSize, SHA256: catalogSHA},
			"events":  {Database: "events", SizeBytes: eventsSize, SHA256: eventsSHA},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(setDir, backupManifestBasename)
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyBackupSet(ctx, manifestPath); err == nil {
		t.Fatal("VerifyBackupSet accepted a snapshot symlink escaping the set")
	}
}

func TestPruneBackupsDoesNotCountPartialDirectories(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	st, err := Open(ctx, t.TempDir(), Options{AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	valid := filepath.Join(backupDir, "beamfall-20260101T000000.000000000Z")
	partial := filepath.Join(backupDir, "beamfall-20270101T000000.000000000Z.partial")
	if err := os.Mkdir(valid, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(partial, 0o700); err != nil {
		t.Fatal(err)
	}
	pruned, err := st.PruneBackups(ctx, backupDir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 0 {
		t.Fatalf("pruned = %d, want 0 published sets removed", pruned)
	}
	if _, err := os.Stat(valid); err != nil {
		t.Fatalf("valid backup was removed: %v", err)
	}
}

// TestRestoreBackupRejectsCorruptedSnapshot verifies RestoreBackup verifies the
// restored bytes against the manifest's SHA-256 and fails closed on a mismatch,
// and that a failed restore leaves no stale db/ tree behind.
func TestRestoreBackupRejectsCorruptedSnapshot(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	result, err := st.backupPlaintextForTest(ctx, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	// Tamper with the catalog snapshot so its bytes no longer match the manifest.
	if err := os.WriteFile(result.Catalog.BackupPath, []byte("corrupted-snapshot-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	restoreDir := t.TempDir()
	err = RestoreBackup(ctx, result.Manifest, restoreDir)
	if err == nil {
		t.Fatal("RestoreBackup accepted a corrupted snapshot, want integrity failure")
	}
	if !strings.Contains(err.Error(), "integrity check failed") {
		t.Fatalf("RestoreBackup err = %v, want integrity check failure", err)
	}
	// JBF-5: a failed restore must not leave a half-restored db/ tree behind.
	if _, statErr := os.Stat(filepath.Join(restoreDir, "db")); !os.IsNotExist(statErr) {
		t.Fatalf("partial db/ tree left after failed restore: %v", statErr)
	}
}

// TestRestoreBackupRejectsTruncatedSnapshot verifies the size half of the
// integrity check: a snapshot truncated to a valid-but-wrong length is
// rejected even before the hash mismatch.
func TestRestoreBackupRejectsTruncatedSnapshot(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	result, err := st.backupPlaintextForTest(ctx, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(result.Events.BackupPath, 0); err != nil {
		t.Fatal(err)
	}
	restoreDir := t.TempDir()
	if err := RestoreBackup(ctx, result.Manifest, restoreDir); err == nil || !strings.Contains(err.Error(), "integrity check failed") {
		t.Fatalf("RestoreBackup err = %v, want integrity check failure", err)
	}
}

func TestRestoreBackupRejectsNewerManifestInstructions(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	result, err := st.backupPlaintextForTest(ctx, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var manifest backupSetManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.RestoreInstructionsVersion = backupRestoreInstructionsVersion + 1
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(result.Manifest, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreBackup(ctx, result.Manifest, t.TempDir()); err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("RestoreBackup err = %v, want newer manifest refusal", err)
	}
}

func TestBackupV2RestoresOperationalPayloadAndExcludesRegenerableState(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	backupDir := t.TempDir()
	files := map[string]string{
		"secrets/server.key":         "secret-key",
		"tls/server.pem":             "tls-identity",
		"tls/acme/account.json":      "regenerable-acme",
		"config/server.json":         `{"listen":"local"}`,
		"integrations/plugin.json":   `{"enabled":true}`,
		"integrations/runtime.bin":   "regenerable-runtime",
		"stream-token.key":           "stream-token",
		"integration-state.json":     `{"cursor":"1"}`,
		"cache/regenerable/index.db": "regenerable-cache",
	}
	for rel, contents := range files {
		writeBackupTestFile(t, dataDir, rel, contents)
	}
	st, err := Open(ctx, dataDir, Options{SeedDevelopmentData: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	roots := []BackupLibraryRoot{
		{LibraryID: "library-b", Path: "/media/b", VolumeID: "volume-b"},
		{LibraryID: "library-a", Path: "/media/a", VolumeID: "volume-a"},
	}
	result, err := st.backupPlaintextWithOptions(ctx, backupDir, BackupOptions{LibraryRoots: roots})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifestFile(result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RestoreInstructionsVersion != 2 {
		t.Fatalf("manifest restore version = %d, want 2", manifest.RestoreInstructionsVersion)
	}
	rawManifest, err := os.ReadFile(result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rawManifest, []byte(`"artworkRoots"`)) || bytes.Contains(rawManifest, []byte(`"trashRoots"`)) {
		t.Fatalf("manifest v2 retained dead root fields: %s", rawManifest)
	}
	if len(manifest.LibraryRoots) != 2 || manifest.LibraryRoots[0].LibraryID != "library-a" {
		t.Fatalf("normalized library roots = %+v", manifest.LibraryRoots)
	}
	for _, rel := range []string{
		"secrets/server.key", "tls/server.pem", "config/server.json",
		"integrations/plugin.json", "stream-token.key", "integration-state.json",
	} {
		payload, ok := manifest.OperationalFiles[rel]
		if !ok {
			t.Fatalf("manifest missing operational file %q", rel)
		}
		if payload.RelativePath != rel || payload.Kind == "" || payload.SizeBytes == 0 || payload.SHA256 == "" {
			t.Fatalf("operational file %q metadata = %+v", rel, payload)
		}
		info, err := os.Stat(payload.BackupPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("operational file %q mode = %v, want 0600", rel, info.Mode().Perm())
		}
	}
	for _, rel := range []string{"tls/acme/account.json", "integrations/runtime.bin", "cache/regenerable/index.db"} {
		if _, ok := manifest.OperationalFiles[rel]; ok {
			t.Fatalf("manifest included excluded operational file %q", rel)
		}
	}
	restoreDir := t.TempDir()
	report, err := RestoreBackupWithOptions(ctx, result.Manifest, restoreDir, RestoreOptions{
		CurrentLibraryRoots: roots,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("restore warnings = %+v, want none", report.Warnings)
	}
	for _, rel := range []string{
		"secrets/server.key", "tls/server.pem", "config/server.json",
		"integrations/plugin.json", "stream-token.key", "integration-state.json",
	} {
		//nolint:gosec // test fixture: rel selects known restored files under restoreDir.
		got, err := os.ReadFile(filepath.Join(restoreDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != files[rel] {
			t.Fatalf("restored %q = %q, want %q", rel, got, files[rel])
		}
	}
	movedDir := result.Directory + "-moved"
	if err := os.Rename(result.Directory, movedDir); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreBackupWithOptions(ctx, filepath.Join(movedDir, backupManifestBasename), t.TempDir(), RestoreOptions{}); err != nil {
		t.Fatalf("restore moved backup set: %v", err)
	}
}

func TestRestoreV1IsDatabaseOnlyWithNamedWarnings(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	backupDir := t.TempDir()
	writeBackupTestFile(t, dataDir, "secrets/server.key", "not-in-v1")
	st, err := Open(ctx, dataDir, Options{SeedDevelopmentData: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	result, err := st.backupPlaintextForTest(ctx, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifestFile(result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.RestoreInstructionsVersion = 1
	manifest.OperationalFiles = nil
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(result.Manifest, data, 0o600); err != nil {
		t.Fatal(err)
	}
	restoreDir := t.TempDir()
	report, err := RestoreBackupWithOptions(ctx, result.Manifest, restoreDir, RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantCodes := []string{
		"legacy_manifest_missing_secrets", "legacy_manifest_missing_stream_token",
		"legacy_manifest_missing_tls", "legacy_manifest_missing_config",
		"legacy_manifest_missing_integration_state",
	}
	if len(report.Warnings) != len(wantCodes) {
		t.Fatalf("warnings = %+v, want codes %v", report.Warnings, wantCodes)
	}
	for i, code := range wantCodes {
		if report.Warnings[i].Code != code {
			t.Fatalf("warning[%d] = %+v, want code %q", i, report.Warnings[i], code)
		}
	}
	if _, err := os.Stat(filepath.Join(restoreDir, "secrets", "server.key")); !os.IsNotExist(err) {
		t.Fatalf("v1 restore created operational payload: %v", err)
	}
}

func TestRestoreIdentityPreconditionMatchingDivergentAndOverride(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	backupDir := t.TempDir()
	writeBackupTestFile(t, dataDir, "secrets/server.key", "backup-secret")
	writeBackupTestFile(t, dataDir, "tls/server.pem", "backup-tls")
	st, err := Open(ctx, dataDir, Options{AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	result, err := st.backupPlaintextForTest(ctx, backupDir)
	if err != nil {
		t.Fatal(err)
	}

	matching := t.TempDir()
	writeBackupTestFile(t, matching, "secrets/server.key", "backup-secret")
	writeBackupTestFile(t, matching, "tls/server.pem", "backup-tls")
	if _, err := RestoreBackupWithOptions(ctx, result.Manifest, matching, RestoreOptions{}); err != nil {
		t.Fatalf("matching identity restore: %v", err)
	}

	divergent := t.TempDir()
	writeBackupTestFile(t, divergent, "secrets/server.key", "different-secret")
	_, err = RestoreBackupWithOptions(ctx, result.Manifest, divergent, RestoreOptions{})
	if !errors.Is(err, ErrRestoreIdentityConflict) {
		t.Fatalf("divergent identity err = %v, want ErrRestoreIdentityConflict", err)
	}
	if _, statErr := os.Stat(filepath.Join(divergent, "db")); !os.IsNotExist(statErr) {
		t.Fatalf("refused restore mutated db/: %v", statErr)
	}
	//nolint:gosec // test fixture: divergent is an isolated t.TempDir path.
	got, readErr := os.ReadFile(filepath.Join(divergent, "secrets", "server.key"))
	if readErr != nil || string(got) != "different-secret" {
		t.Fatalf("refused restore mutated live identity: got %q err=%v", got, readErr)
	}

	live := t.TempDir()
	writeBackupTestFile(t, live, "db/sentinel", "live-db")
	writeBackupTestFile(t, live, "secrets/server.key", "different-secret")
	if err := PreflightRestoreIdentity(ctx, result.Manifest, live, false); !errors.Is(err, ErrRestoreIdentityConflict) {
		t.Fatalf("identity preflight err = %v, want ErrRestoreIdentityConflict", err)
	}
	if _, err := RestoreBackupWithOptions(ctx, result.Manifest, live, RestoreOptions{}); err == nil {
		t.Fatal("restore accepted a target containing a live database")
	}
	//nolint:gosec // test fixture: live is an isolated t.TempDir path.
	got, readErr = os.ReadFile(filepath.Join(live, "db", "sentinel"))
	if readErr != nil || string(got) != "live-db" {
		t.Fatalf("refused restore mutated live database: got %q err=%v", got, readErr)
	}
	if err := PreflightRestoreIdentity(ctx, result.Manifest, live, true); err != nil {
		t.Fatalf("replace identity preflight: %v", err)
	}
	if _, err := RestoreBackupWithOptions(ctx, result.Manifest, live, RestoreOptions{ReplaceIdentity: true}); err != nil {
		t.Fatalf("live-target replace restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(live, "db", "sentinel")); !os.IsNotExist(err) {
		t.Fatalf("live database sentinel survived replacement: %v", err)
	}

	if _, err := RestoreBackupWithOptions(ctx, result.Manifest, divergent, RestoreOptions{ReplaceIdentity: true}); err != nil {
		t.Fatalf("replace identity restore: %v", err)
	}
	//nolint:gosec // test fixture: divergent is an isolated t.TempDir path.
	got, err = os.ReadFile(filepath.Join(divergent, "secrets", "server.key"))
	if err != nil || string(got) != "backup-secret" {
		t.Fatalf("replaced secret = %q err=%v", got, err)
	}
}

func TestLibraryRootReconciliationWarningsNeverBlockRestore(t *testing.T) {
	recorded := []BackupLibraryRoot{
		{LibraryID: "a", Path: "/media/a", VolumeID: "va"},
		{LibraryID: "b", Path: "/media/b", VolumeID: "vb"},
		{LibraryID: "c", Path: "/media/c", VolumeID: "vc"},
	}
	oneDrift := append([]BackupLibraryRoot(nil), recorded...)
	oneDrift[0].Path = "/Volumes/a"
	warnings := reconcileLibraryRoots(recorded, oneDrift, 0)
	if len(warnings) != 1 || warnings[0].Code != "library_root_drift" {
		t.Fatalf("one-of-three drift warnings = %+v", warnings)
	}
	twoDrift := append([]BackupLibraryRoot(nil), recorded...)
	twoDrift[0].VolumeID = "wrong-a"
	twoDrift[1].VolumeID = "wrong-b"
	warnings = reconcileLibraryRoots(recorded, twoDrift, 0)
	if len(warnings) != 1 || warnings[0].Code != "library_volume_mapping" ||
		!strings.Contains(warnings[0].Message, "wrong volume mapping?") {
		t.Fatalf("two-of-three drift warnings = %+v", warnings)
	}
	warnings = reconcileLibraryRoots(recorded, nil, 0)
	if len(warnings) != 1 || warnings[0].Code != "library_root_reconciliation_unavailable" {
		t.Fatalf("unavailable reconciliation warnings = %+v", warnings)
	}
	duplicateLibrary := []BackupLibraryRoot{
		{LibraryID: "a", Path: "/media/a", VolumeID: "va"},
		{LibraryID: "a", Path: "/media/a-2", VolumeID: "va-2"},
	}
	if warnings := reconcileLibraryRoots(duplicateLibrary, duplicateLibrary, 0); len(warnings) != 0 {
		t.Fatalf("duplicate-library exact roots warnings = %+v", warnings)
	}
	extra := append(append([]BackupLibraryRoot(nil), recorded...), BackupLibraryRoot{LibraryID: "d", Path: "/media/d", VolumeID: "vd"})
	if warnings := reconcileLibraryRoots(recorded, extra, 0); len(warnings) != 1 || strings.Contains(warnings[0].Message, "0 of") {
		t.Fatalf("extra-root warnings = %+v", warnings)
	}
	disjoint := []BackupLibraryRoot{
		{LibraryID: "x", Path: "/media/x", VolumeID: "vx"},
		{LibraryID: "y", Path: "/media/y", VolumeID: "vy"},
		{LibraryID: "z", Path: "/media/z", VolumeID: "vz"},
	}
	if warnings := reconcileLibraryRoots(recorded, disjoint, 0); len(warnings) != 1 ||
		!strings.Contains(warnings[0].Message, "3 of 3") {
		t.Fatalf("disjoint-root warnings = %+v", warnings)
	}
	exactHalf := append([]BackupLibraryRoot(nil), recorded[:2]...)
	exactHalf[0].VolumeID = "wrong"
	if warnings := reconcileLibraryRoots(recorded[:2], exactHalf, 0); len(warnings) != 1 ||
		warnings[0].Code != "library_volume_mapping" {
		t.Fatalf("exact-half warnings = %+v", warnings)
	}
}

func TestRestoreReconcilesCatalogMediaReferencesWithoutBlocking(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	backupDir := t.TempDir()
	mediaRoot := t.TempDir()
	st, err := Open(ctx, dataDir, Options{SeedDevelopmentData: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	paths := []string{
		filepath.Join(mediaRoot, "one.mkv"),
		filepath.Join(mediaRoot, "two.mkv"),
		filepath.Join(mediaRoot, "three.mkv"),
	}
	for i, path := range paths {
		if err := os.WriteFile(path, []byte{byte(i + 1)}, 0o600); err != nil {
			t.Fatal(err)
		}
		mediaID := fmt.Sprintf("media-reconcile-%d", i)
		if _, err := st.catalogW.ExecContext(ctx,
			`INSERT INTO media(id, library_id, title, duration_seconds, container, codec) VALUES (?, 'library-local', ?, 1, 'mkv', 'h264')`,
			mediaID, mediaID,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := st.catalogW.ExecContext(ctx,
			`INSERT INTO media_file(id, content_fingerprint, media_id, path) VALUES (?, ?, ?, ?)`,
			"file-"+mediaID, "fingerprint-"+mediaID, mediaID, path,
		); err != nil {
			t.Fatal(err)
		}
	}
	roots := []BackupLibraryRoot{{LibraryID: "library-local", Path: mediaRoot, VolumeID: "volume-test"}}
	result, err := st.backupPlaintextWithOptions(ctx, backupDir, BackupOptions{LibraryRoots: roots})
	if err != nil {
		t.Fatal(err)
	}

	clean, err := RestoreBackupWithOptions(ctx, result.Manifest, t.TempDir(), RestoreOptions{CurrentLibraryRoots: roots})
	if err != nil {
		t.Fatal(err)
	}
	if clean.MediaReconciliation.Severity != "clean" || clean.MediaReconciliation.MissingReferences != 0 {
		t.Fatalf("clean reconciliation = %+v", clean.MediaReconciliation)
	}

	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	partial, err := RestoreBackupWithOptions(ctx, result.Manifest, t.TempDir(), RestoreOptions{CurrentLibraryRoots: roots})
	if err != nil {
		t.Fatal(err)
	}
	if partial.MediaReconciliation.Severity != "routine-drift" ||
		partial.MediaReconciliation.MissingReferences != 1 ||
		!restoreWarningsContain(partial.Warnings, "media_reference_drift") {
		t.Fatalf("partial reconciliation = %+v warnings=%+v", partial.MediaReconciliation, partial.Warnings)
	}

	if err := os.Remove(paths[1]); err != nil {
		t.Fatal(err)
	}
	skewed, err := RestoreBackupWithOptions(ctx, result.Manifest, t.TempDir(), RestoreOptions{CurrentLibraryRoots: roots})
	if err != nil {
		t.Fatal(err)
	}
	if skewed.MediaReconciliation.Severity != "wrong-volume-mapping" ||
		skewed.MediaReconciliation.MissingReferences != 2 ||
		!restoreWarningsContain(skewed.Warnings, "media_volume_mapping") {
		t.Fatalf("skewed reconciliation = %+v warnings=%+v", skewed.MediaReconciliation, skewed.Warnings)
	}
}

func TestBackupDefaultRecordsPersistedLibraryRootsAndVolumeIDs(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	mediaRoot := t.TempDir()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SetLibraryRoots(ctx, "library-local", []string{mediaRoot}); err != nil {
		t.Fatal(err)
	}
	result, err := st.backupPlaintextForTest(ctx, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifestFile(result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.LibraryRoots) != 1 ||
		manifest.LibraryRoots[0].LibraryID != "library-local" ||
		manifest.LibraryRoots[0].Path != mediaRoot ||
		manifest.LibraryRoots[0].VolumeID == "" {
		t.Fatalf("default manifest library roots = %+v", manifest.LibraryRoots)
	}
	report, err := RestoreBackupWithOptions(ctx, result.Manifest, t.TempDir(), RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if restoreWarningsContain(report.Warnings, "library_root_reconciliation_unavailable") {
		t.Fatalf("default restore skipped root reconciliation: %+v", report.Warnings)
	}
}

func restoreWarningsContain(warnings []RestoreWarning, code string) bool {
	for _, warning := range warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}

func TestRestoreRejectsOperationalTamperAndTraversalBeforeMutation(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	backupDir := t.TempDir()
	writeBackupTestFile(t, dataDir, "config/server.json", `{"safe":true}`)
	st, err := Open(ctx, dataDir, Options{AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	result, err := st.backupPlaintextForTest(ctx, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifestFile(result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	payload := manifest.OperationalFiles["config/server.json"]
	if err := os.WriteFile(payload.BackupPath, []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	restoreDir := t.TempDir()
	if _, err := RestoreBackupWithOptions(ctx, result.Manifest, restoreDir, RestoreOptions{}); err == nil {
		t.Fatal("restore accepted tampered operational payload")
	}
	if entries, err := os.ReadDir(restoreDir); err != nil || len(entries) != 0 {
		t.Fatalf("tamper refusal mutated restore target: entries=%v err=%v", entries, err)
	}

	size, checksum, err := fileSizeAndSHA256(payload.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	delete(manifest.OperationalFiles, "config/server.json")
	payload.RelativePath = "../escape"
	payload.Kind = "configuration"
	payload.SizeBytes = size
	payload.SHA256 = checksum
	manifest.OperationalFiles["../escape"] = payload
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(result.Manifest, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyBackupSet(ctx, result.Manifest); err == nil {
		t.Fatal("VerifyBackupSet accepted operational traversal")
	}
}

func TestPublishStagedRestoreRollsBackDatabaseOnLaterFailure(t *testing.T) {
	stageDir := t.TempDir()
	dataDir := t.TempDir()
	writeBackupTestFile(t, stageDir, "db/new", "restored-db")
	writeBackupTestFile(t, stageDir, "config/nested/server.json", "restored-config")
	writeBackupTestFile(t, dataDir, "db/sentinel", "live-db")
	manifest := backupSetManifest{
		OperationalFiles: map[string]BackupOperationalFile{
			"config/nested/server.json": {RelativePath: "config/nested/server.json"},
			"tls/missing.pem":           {RelativePath: "tls/missing.pem"},
		},
	}
	if err := publishStagedRestore(stageDir, dataDir, manifest, false); err == nil {
		t.Fatal("publish succeeded despite a missing staged operational file")
	}
	//nolint:gosec // test fixture: dataDir is an isolated t.TempDir path.
	got, err := os.ReadFile(filepath.Join(dataDir, "db", "sentinel"))
	if err != nil || string(got) != "live-db" {
		t.Fatalf("publish failure did not roll back database: got %q err=%v", got, err)
	}
	for _, rel := range []string{"config", "tls"} {
		if _, err := os.Stat(filepath.Join(dataDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("publish rollback left directory residue %q: %v", rel, err)
		}
	}
}

func TestInterruptedRestorePublishRecoversEveryRenameBoundary(t *testing.T) {
	boundaries := []string{
		"old:db",
		"new:db",
		"old:config/nested/server.json",
		"new:config/nested/server.json",
		"old:integrations/runtime.json",
		"new:integrations/runtime.json",
	}
	manifest := backupSetManifest{
		OperationalFiles: map[string]BackupOperationalFile{
			"config/nested/server.json": {RelativePath: "config/nested/server.json"},
			"integrations/runtime.json": {RelativePath: "integrations/runtime.json"},
		},
	}
	for _, interruptedAt := range boundaries {
		t.Run(strings.ReplaceAll(interruptedAt, "/", "_"), func(t *testing.T) {
			stageDir := t.TempDir()
			dataDir := t.TempDir()
			for _, rel := range []string{"db/generation", "config/nested/server.json", "integrations/runtime.json"} {
				writeBackupTestFile(t, stageDir, rel, "restored:"+rel)
				writeBackupTestFile(t, dataDir, rel, "live:"+rel)
			}
			err := publishStagedRestoreInterruptibly(
				stageDir,
				dataDir,
				manifest,
				false,
				func(boundary string) error {
					if boundary == interruptedAt {
						return errRestorePublishInterrupted
					}
					return nil
				},
			)
			if !errors.Is(err, errRestorePublishInterrupted) {
				t.Fatalf("publish interruption at %q returned %v", interruptedAt, err)
			}
			if err := recoverRestorePublish(dataDir); err != nil {
				t.Fatalf("recover after %q: %v", interruptedAt, err)
			}
			for _, rel := range []string{"db/generation", "config/nested/server.json", "integrations/runtime.json"} {
				//nolint:gosec // test fixture path is rooted in t.TempDir.
				got, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(rel)))
				if err != nil {
					t.Fatalf("read %s after %q: %v", rel, interruptedAt, err)
				}
				if want := "restored:" + rel; string(got) != want {
					t.Fatalf("%s after %q = %q, want %q", rel, interruptedAt, got, want)
				}
			}
			if _, err := os.Lstat(filepath.Join(dataDir, restorePublishTransactionName)); !os.IsNotExist(err) {
				t.Fatalf("transaction remains after recovery at %q: %v", interruptedAt, err)
			}
		})
	}
}

func TestInterruptedRestorePublishBeforeJournalRecoversOldState(t *testing.T) {
	stageDir := t.TempDir()
	dataDir := t.TempDir()
	manifest := backupSetManifest{
		OperationalFiles: map[string]BackupOperationalFile{
			"config/server.json": {RelativePath: "config/server.json"},
		},
	}
	for _, rel := range []string{"db/generation", "config/server.json"} {
		writeBackupTestFile(t, stageDir, rel, "restored:"+rel)
		writeBackupTestFile(t, dataDir, rel, "live:"+rel)
	}

	err := publishStagedRestoreInterruptibly(
		stageDir,
		dataDir,
		manifest,
		false,
		func(boundary string) error {
			if boundary == "staged" {
				return errRestorePublishInterrupted
			}
			return nil
		},
	)
	if !errors.Is(err, errRestorePublishInterrupted) {
		t.Fatalf("publish interruption returned %v", err)
	}
	transactionDir := filepath.Join(dataDir, restorePublishTransactionName)
	if _, err := os.Lstat(filepath.Join(transactionDir, "journal.json")); !os.IsNotExist(err) {
		t.Fatalf("pre-journal interruption published a journal: %v", err)
	}
	for _, rel := range []string{"db/generation", "config/server.json"} {
		//nolint:gosec // test fixture path is rooted in t.TempDir.
		got, err := os.ReadFile(restorePublishPath(transactionDir, "new", rel))
		if err != nil {
			t.Fatalf("read staged transaction payload %s: %v", rel, err)
		}
		if want := "restored:" + rel; string(got) != want {
			t.Fatalf("staged transaction payload %s = %q, want %q", rel, got, want)
		}
	}

	if err := recoverRestorePublish(dataDir); err != nil {
		t.Fatalf("recover pre-journal interruption: %v", err)
	}
	for _, rel := range []string{"db/generation", "config/server.json"} {
		//nolint:gosec // test fixture path is rooted in t.TempDir.
		got, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s after recovery: %v", rel, err)
		}
		if want := "live:" + rel; string(got) != want {
			t.Fatalf("%s after recovery = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Lstat(transactionDir); !os.IsNotExist(err) {
		t.Fatalf("unjournaled transaction remains after recovery: %v", err)
	}
}

func TestOpenRecoversInterruptedRestorePublishBeforeSQLiteOpen(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	live, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}

	replacementDir := t.TempDir()
	replacement, err := Open(ctx, replacementDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	stageDir := t.TempDir()
	if err := os.Rename(filepath.Join(replacementDir, "db"), filepath.Join(stageDir, "db")); err != nil {
		t.Fatal(err)
	}
	err = publishStagedRestoreInterruptibly(
		stageDir,
		dataDir,
		backupSetManifest{},
		false,
		func(boundary string) error {
			if boundary == "old:db" {
				return errRestorePublishInterrupted
			}
			return nil
		},
	)
	if !errors.Is(err, errRestorePublishInterrupted) {
		t.Fatalf("publish interruption returned %v", err)
	}

	reopened, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatalf("Open did not recover interrupted restore: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dataDir, restorePublishTransactionName)); !os.IsNotExist(err) {
		t.Fatalf("transaction remains after Open recovery: %v", err)
	}
}

func TestInterruptedRestorePublishRecoversIdentityDeletionBoundary(t *testing.T) {
	stageDir := t.TempDir()
	dataDir := t.TempDir()
	writeBackupTestFile(t, stageDir, "db/generation", "restored-db")
	writeBackupTestFile(t, stageDir, "tls/new.pem", "restored-identity")
	writeBackupTestFile(t, dataDir, "db/generation", "live-db")
	writeBackupTestFile(t, dataDir, "tls/old.pem", "obsolete-identity")
	manifest := backupSetManifest{
		OperationalFiles: map[string]BackupOperationalFile{
			"tls/new.pem": {RelativePath: "tls/new.pem"},
		},
	}
	err := publishStagedRestoreInterruptibly(
		stageDir,
		dataDir,
		manifest,
		true,
		func(boundary string) error {
			if boundary == "old:tls/old.pem" {
				return errRestorePublishInterrupted
			}
			return nil
		},
	)
	if !errors.Is(err, errRestorePublishInterrupted) {
		t.Fatalf("publish interruption returned %v", err)
	}
	if err := recoverRestorePublish(dataDir); err != nil {
		t.Fatalf("recover identity deletion: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dataDir, "tls", "old.pem")); !os.IsNotExist(err) {
		t.Fatalf("obsolete identity survived recovered replacement: %v", err)
	}
	//nolint:gosec // test fixture path is rooted in t.TempDir.
	got, err := os.ReadFile(filepath.Join(dataDir, "tls", "new.pem"))
	if err != nil || string(got) != "restored-identity" {
		t.Fatalf("replacement identity = %q, %v", got, err)
	}
}

func writeBackupTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// migrationFileCount counts the embedded migrations so version assertions
// track the catalog instead of a hardcoded constant.
func migrationFileCount(t *testing.T, dir string) int {
	t.Helper()
	var migrations fs.FS
	switch dir {
	case "migrate/catalog":
		migrations = catalogMigrations
	case "migrate/events":
		migrations = eventsMigrations
	case "migrate/encrypted_library":
		migrations = encryptedLibraryMigrations
	default:
		t.Fatalf("unknown migration dir %q", dir)
	}
	entries, err := fs.ReadDir(migrations, dir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			count++
		}
	}
	return count
}

func tableRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var rows int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

type wrappingEncryptedLibraryDBCipher struct{}

var wrappingEncryptedLibraryPrefix = []byte("beamfall-test-ciphertext\n")

type preservingEncryptedLibraryDBCipher struct {
	wrappingEncryptedLibraryDBCipher
}

// SealDBFile seals exactly like the embedded cipher but deliberately leaves the
// plaintext working file behind, so the backup path can be asserted to never
// copy plaintext into the backup set.
func (c preservingEncryptedLibraryDBCipher) SealDBFile(ctx context.Context, libraryID LibraryID, workingPath, ciphertextPath string) error {
	if err := c.wrappingEncryptedLibraryDBCipher.SealDBFile(ctx, libraryID, workingPath, ciphertextPath); err != nil {
		return err
	}
	return c.UnsealDBFile(ctx, libraryID, ciphertextPath, workingPath)
}

func (wrappingEncryptedLibraryDBCipher) UnsealDBFile(ctx context.Context, libraryID LibraryID, ciphertextPath, workingPath string) error {
	data, err := os.ReadFile(ciphertextPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(data, wrappingEncryptedLibraryPrefix) {
		return errors.New("missing test ciphertext prefix")
	}
	if err := os.MkdirAll(filepath.Dir(workingPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(workingPath, data[len(wrappingEncryptedLibraryPrefix):], 0o600)
}

func (wrappingEncryptedLibraryDBCipher) SealDBFile(ctx context.Context, libraryID LibraryID, workingPath, ciphertextPath string) error {
	data, err := os.ReadFile(workingPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ciphertextPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(ciphertextPath, append(append([]byte{}, wrappingEncryptedLibraryPrefix...), data...), 0o600); err != nil {
		return err
	}
	return wipeOrRemoveFile(workingPath)
}

func assertNoPlaintextEncryptedBackupFiles(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if name == "working.db" ||
			strings.HasSuffix(name, ".snapshot.db") ||
			strings.HasSuffix(name, ".db-wal") ||
			strings.HasSuffix(name, ".db-shm") ||
			strings.HasPrefix(name, "sqlite") {
			t.Fatalf("plaintext encrypted-library backup artifact found: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

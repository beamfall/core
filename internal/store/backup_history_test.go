package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedBackupStore opens a seeded store whose backups may be written into dest.
func seedBackupStore(t *testing.T, dest string) *Store {
	t.Helper()
	dataDir := t.TempDir()
	st, err := Open(context.Background(), dataDir, Options{SeedDevelopmentData: true, AllowedRoots: []string{dest}})
	if err != nil {
		t.Fatal(err)
	}
	installBackupTestSealer(st, dataDir)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestBackupHistoryListsSetsNewestFirstAndVerifies(t *testing.T) {
	ctx := context.Background()
	dest := t.TempDir()
	st := seedBackupStore(t, dest)

	first, err := st.Backup(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	// The set directory name embeds a nanosecond timestamp; a short sleep keeps
	// the two sets distinctly ordered without depending on clock resolution.
	time.Sleep(2 * time.Millisecond)
	second, err := st.Backup(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}

	history, err := st.BackupHistory(ctx, dest)
	if err != nil {
		t.Fatalf("BackupHistory: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2", len(history))
	}
	if history[0].Directory != second.Directory {
		t.Fatalf("newest-first: history[0] = %s, want %s", history[0].Directory, second.Directory)
	}
	if history[1].Directory != first.Directory {
		t.Fatalf("history[1] = %s, want %s", history[1].Directory, first.Directory)
	}
	for _, entry := range history {
		if !entry.Valid {
			t.Fatalf("set %s reported invalid: %s", entry.Directory, entry.Error)
		}
		if entry.SizeBytes <= 0 {
			t.Fatalf("set %s size = %d, want > 0", entry.Directory, entry.SizeBytes)
		}
		if entry.CreatedAt.IsZero() {
			t.Fatalf("set %s missing createdAt", entry.Directory)
		}
	}
}

func TestBackupHistoryRejectsDestOutsideAllowlist(t *testing.T) {
	ctx := context.Background()
	dest := t.TempDir()
	st := seedBackupStore(t, dest)
	if _, err := st.BackupHistory(ctx, t.TempDir()); err == nil {
		t.Fatal("BackupHistory accepted a destination outside the allowlist")
	}
}

func TestVerifyBackupSetFailsOnTamperedSnapshot(t *testing.T) {
	ctx := context.Background()
	dest := t.TempDir()
	st := seedBackupStore(t, dest)
	result, err := st.Backup(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyBackupSet(ctx, result.Manifest); err != nil {
		t.Fatalf("VerifyBackupSet on a fresh set: %v", err)
	}

	// Tamper with the encrypted set bytes; verification must fail closed and
	// must NOT leak the computed size/sha256 of the tampered file.
	if err := os.WriteFile(result.Envelope, append([]byte("corrupt"), byte(0)), 0o600); err != nil {
		t.Fatal(err)
	}
	err = st.VerifyBackupSet(ctx, result.Manifest)
	if err == nil {
		t.Fatal("VerifyBackupSet passed a tampered set")
	}
	if got := err.Error(); !containsAny(got, "integrity verification") || containsAny(got, "sha256") {
		t.Fatalf("verify error leaked detail: %q", got)
	}
}

func TestVerifyBackupSetRejectsManifestOutsideAllowlist(t *testing.T) {
	ctx := context.Background()
	dest := t.TempDir()
	st := seedBackupStore(t, dest)

	// A manifest.json outside the sanctioned roots must be refused, so the
	// verifier cannot be pointed at an arbitrary directory.
	rogue := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(rogue, []byte(`{"restoreInstructionsVersion":1,"consistencyProtocol":"x","databases":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyBackupSet(ctx, rogue); err == nil {
		t.Fatal("VerifyBackupSet accepted a manifest outside the allowlist")
	}
}

func TestVerifyBackupSetRejectsNonManifestBasename(t *testing.T) {
	ctx := context.Background()
	dest := t.TempDir()
	st := seedBackupStore(t, dest)
	result, err := st.Backup(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	notManifest := filepath.Join(result.Directory, backupReceiptBasename)
	if err := st.VerifyBackupSet(ctx, notManifest); err == nil {
		t.Fatal("VerifyBackupSet accepted a path whose basename is not manifest.json")
	}
}

func TestPruneBackupsKeepsNewestN(t *testing.T) {
	ctx := context.Background()
	dest := t.TempDir()
	st := seedBackupStore(t, dest)

	dirs := make([]string, 0, 4)
	for range 4 {
		result, err := st.Backup(ctx, dest)
		if err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, result.Directory)
		time.Sleep(2 * time.Millisecond)
	}

	pruned, err := st.PruneBackups(ctx, dest, 2)
	if err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	if pruned != 2 {
		t.Fatalf("pruned = %d, want 2", pruned)
	}
	// The two oldest sets are gone; the two newest remain.
	for _, gone := range dirs[:2] {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("expected %s pruned, stat err = %v", gone, err)
		}
	}
	for _, kept := range dirs[2:] {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("expected %s kept: %v", kept, err)
		}
	}
}

func TestPruneBackupsKeepBelowOneRetainsAll(t *testing.T) {
	ctx := context.Background()
	dest := t.TempDir()
	st := seedBackupStore(t, dest)
	if _, err := st.Backup(ctx, dest); err != nil {
		t.Fatal(err)
	}
	pruned, err := st.PruneBackups(ctx, dest, 0)
	if err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	if pruned != 0 {
		t.Fatalf("pruned = %d, want 0 (keep-all guard)", pruned)
	}
}

func TestReadVerifiedManifestRefusesNewerSchemaThanBinary(t *testing.T) {
	ctx := context.Background()
	dest := t.TempDir()
	st := seedBackupStore(t, dest)
	result, err := st.backupPlaintextForTest(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}

	// Rewrite the manifest so the catalog snapshot claims a schema version newer
	// than the binary's frozen migration count.
	data, err := os.ReadFile(result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var manifest backupSetManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	catalog := manifest.Databases["catalog"]
	catalog.SchemaVersion = schemaFreezeCatalogMigrations + 1
	manifest.Databases["catalog"] = catalog
	rewritten, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(result.Manifest, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RestoreBackup(ctx, result.Manifest, t.TempDir()); err == nil {
		t.Fatal("RestoreBackup accepted a manifest whose schema is newer than the binary")
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

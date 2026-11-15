package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/restorestage"
	storecore "github.com/beamfall/core/internal/store"
)

// stagedRestoreFixture backs up a data directory, stages that backup from the
// console path, writes a file the backup does not hold, and closes the store
// the way a stopping server does. It returns the data directory.
func stagedRestoreFixture(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	backupDir := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "data")
	st, err := storecore.Open(ctx, dataDir, storecore.Options{SeedDevelopmentData: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	st.SetSnapshotSealer(backupcore.NewDirectorySealer(dataDir))
	if _, err := backupcore.CreateBootstrapEscrow(dataDir, time.Now().UTC()); err != nil {
		_ = st.Close()
		t.Fatalf("provision backup key: %v", err)
	}
	result, err := st.Backup(ctx, backupDir)
	if err != nil {
		_ = st.Close()
		t.Fatalf("backup: %v", err)
	}
	escrow, err := backupcore.ReadBootstrapEscrow(dataDir, time.Now())
	if err != nil {
		_ = st.Close()
		t.Fatalf("read backup key: %v", err)
	}
	if _, err := restorestage.New(dataDir, st).Stage(ctx, result.Manifest, escrow.Key.Display(), false); err != nil {
		_ = st.Close()
		t.Fatalf("stage: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "written-after-backup"), []byte("live bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func readRestoreResult(t *testing.T, dataDir string) restorestage.Result {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dataDir, "restore-result.json")) // #nosec G304 -- test-owned temp dir
	if err != nil {
		t.Fatalf("read restore result: %v", err)
	}
	var result restorestage.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestStagedRestoreAppliesOnStartAndKeepsARollbackCopy(t *testing.T) {
	dataDir := stagedRestoreFixture(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := applyStagedRestore(context.Background(), dataDir, logger); err != nil {
		t.Fatalf("apply staged restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "written-after-backup")); !os.IsNotExist(err) {
		t.Fatalf("restored data still holds a file written after the backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "db", "catalog.db")); err != nil {
		t.Fatalf("restored catalog missing: %v", err)
	}
	result := readRestoreResult(t, dataDir)
	if !result.OK || result.Error != "" || result.BackupCreatedAt == "" {
		t.Fatalf("result = %+v", result)
	}
	if !strings.HasPrefix(result.RollbackCopy, dataDir+".pre-restore-") {
		t.Fatalf("rollback copy = %q", result.RollbackCopy)
	}
	kept, err := os.ReadFile(filepath.Join(result.RollbackCopy, "written-after-backup")) // #nosec G304 -- test-owned temp dir
	if err != nil || string(kept) != "live bytes" {
		t.Fatalf("rollback copy lost the live data: %q %v", kept, err)
	}
	if _, err := os.Stat(filepath.Join(result.RollbackCopy, restorestage.DirName)); !os.IsNotExist(err) {
		t.Fatalf("rollback copy holds the decrypted staged set: %v", err)
	}
	if claimed, _ := filepath.Glob(dataDir + ".restore-applying-*"); len(claimed) != 0 {
		t.Fatalf("decrypted staged set left beside the data directory: %v", claimed)
	}
	if _, ok, err := restorestage.Pending(dataDir); ok || err != nil {
		t.Fatalf("restore still pending after apply: %v %v", ok, err)
	}
	// A second start finds nothing staged and leaves the data alone.
	if err := applyStagedRestore(context.Background(), dataDir, logger); err != nil {
		t.Fatalf("second start: %v", err)
	}
	if snapshots, _ := filepath.Glob(dataDir + ".pre-restore-*"); len(snapshots) != 1 {
		t.Fatalf("rollback copies = %v, want one", snapshots)
	}
}

func TestStagedRestoreFailureKeepsTheOriginalAndDoesNotRetry(t *testing.T) {
	dataDir := stagedRestoreFixture(t)
	restoreFailure := errors.New("injected restore failure")
	oldRestoreBackup := restoreBackupFn
	restoreBackupFn = partialThenFailingRestore(restoreFailure)
	t.Cleanup(func() { restoreBackupFn = oldRestoreBackup })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := applyStagedRestore(context.Background(), dataDir, logger); err != nil {
		t.Fatalf("a failed staged restore whose rollback succeeded should not stop the start: %v", err)
	}
	live, err := os.ReadFile(filepath.Join(dataDir, "written-after-backup")) // #nosec G304 -- test-owned temp dir
	if err != nil || string(live) != "live bytes" {
		t.Fatalf("original data not put back: %q %v", live, err)
	}
	result := readRestoreResult(t, dataDir)
	if result.OK || !strings.Contains(result.Error, "injected restore failure") {
		t.Fatalf("result = %+v", result)
	}
	if _, ok, _ := restorestage.Pending(dataDir); ok {
		t.Fatal("a failed staged restore is retried on the next start")
	}
	if snapshots, _ := filepath.Glob(dataDir + ".pre-restore-*"); len(snapshots) != 0 {
		t.Fatalf("rollback left snapshot paths: %v", snapshots)
	}
}

func TestStagedRestoreRefusesToStartWhenTheOriginalWasNotPutBack(t *testing.T) {
	dataDir := stagedRestoreFixture(t)
	oldRestoreBackup := restoreBackupFn
	restoreBackupFn = partialThenFailingRestore(errors.New("injected restore failure"))
	oldRename := renameRestoreDataDir
	renames := 0
	renameRestoreDataDir = func(from, to string) error {
		renames++
		if strings.Contains(from, ".pre-restore-") {
			return errors.New("injected rollback failure")
		}
		return os.Rename(from, to) // #nosec G703 -- test-owned temp dir
	}
	t.Cleanup(func() {
		restoreBackupFn = oldRestoreBackup
		renameRestoreDataDir = oldRename
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := applyStagedRestore(context.Background(), dataDir, logger)
	if err == nil || !strings.Contains(err.Error(), "not put back") {
		t.Fatalf("apply err = %v, want a refusal to start", err)
	}
	snapshots, _ := filepath.Glob(dataDir + ".pre-restore-*")
	if len(snapshots) != 1 {
		t.Fatalf("original data snapshots = %v, want one", snapshots)
	}
	if _, err := os.Stat(filepath.Join(snapshots[0], "written-after-backup")); err != nil {
		t.Fatalf("original data not preserved: %v", err)
	}
	if renames == 0 {
		t.Fatal("rename seam not exercised")
	}
}

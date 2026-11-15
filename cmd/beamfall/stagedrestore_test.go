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

// startStagedRestore takes the server lock and applies a staged restore the
// way startServer does, releasing the lock it ends up holding.
func startStagedRestore(t *testing.T, dataDir string, logger *slog.Logger) error {
	t.Helper()
	release, err := lockServeDataDir(dataDir)
	if err != nil {
		return err
	}
	release, err = applyStagedRestore(context.Background(), dataDir, logger, release)
	if err != nil {
		return err
	}
	release()
	return nil
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
	if err := startStagedRestore(t, dataDir, logger); err != nil {
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
	if err := startStagedRestore(t, dataDir, logger); err != nil {
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
	if err := startStagedRestore(t, dataDir, logger); err != nil {
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
	err := startStagedRestore(t, dataDir, logger)
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
	// The journal stays, so the next start refuses instead of booting an
	// empty server beside the preserved original.
	if _, err := lockServeDataDir(dataDir); err == nil || !strings.Contains(err.Error(), snapshots[0]) {
		t.Fatalf("next start = %v, want a refusal naming the original data", err)
	}
}

func TestStagedRestoreRefusesToStartWhenTheSnapshotLeftNoDataDirectory(t *testing.T) {
	dataDir := stagedRestoreFixture(t)
	oldRename := renameRestoreDataDir
	renameRestoreDataDir = func(from, to string) error {
		if err := os.Rename(from, to); err != nil { // #nosec G703 -- test-owned temp dir
			return err
		}
		return errors.New("injected rename error after the move")
	}
	t.Cleanup(func() { renameRestoreDataDir = oldRename })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := startStagedRestore(t, dataDir, logger)
	if !errors.Is(err, errStagedRestoreUnsafe) {
		t.Fatalf("apply err = %v, want a refusal to start", err)
	}
	if _, err := os.Lstat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("data directory recreated: %v", err)
	}
	if _, err := lockServeDataDir(dataDir); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("next start = %v, want a refusal", err)
	}
	if _, err := os.Lstat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("a refused start created an empty data directory: %v", err)
	}
}

func TestStagedRestoreHoldsTheDataDirectoryAgainstASecondServer(t *testing.T) {
	dataDir := stagedRestoreFixture(t)
	oldRestoreBackup := restoreBackupFn
	var during error
	restoreBackupFn = func(ctx context.Context, manifest, dir string) error {
		release, err := lockServeDataDir(dir)
		if err == nil {
			release()
		}
		during = err
		return oldRestoreBackup(ctx, manifest, dir)
	}
	t.Cleanup(func() { restoreBackupFn = oldRestoreBackup })

	release, err := lockServeDataDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockServeDataDir(dataDir); err == nil {
		t.Fatal("a second server locked the data directory before the restore")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	release, err = applyStagedRestore(context.Background(), dataDir, logger, release)
	if err != nil {
		t.Fatalf("apply staged restore: %v", err)
	}
	if during == nil {
		t.Fatal("a second server started while the data directory was being replaced")
	}
	if _, err := lockServeDataDir(dataDir); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second server after the restore = %v, want the restored directory locked", err)
	}
	release()
	if _, ok, err := restorestage.ReadJournal(dataDir); ok || err != nil {
		t.Fatalf("journal left after a successful restore: %v %v", ok, err)
	}
	if !readRestoreResult(t, dataDir).OK {
		t.Fatal("restore did not apply")
	}
}

func TestServeRefusesAnInterruptedRestoreAndRemovesOrphanClaims(t *testing.T) {
	parent := t.TempDir()
	dataDir := filepath.Join(parent, "data")
	snapshot := dataDir + ".pre-restore-20261114T000000.000000000Z"
	if err := os.MkdirAll(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
	// A start that crashed after renaming the data directory: only the
	// journal records where the original went.
	if err := restorestage.WriteJournal(dataDir, restorestage.Journal{Snapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	if _, err := lockServeDataDir(dataDir); err == nil || !strings.Contains(err.Error(), snapshot) {
		t.Fatalf("start = %v, want a refusal naming the original data", err)
	}
	if _, err := os.Lstat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("a refused start created an empty data directory: %v", err)
	}

	// A start that crashed after claiming but before renaming leaves the
	// data intact and a decrypted copy beside it, which the next start drops.
	if err := restorestage.ClearJournal(dataDir); err != nil {
		t.Fatal(err)
	}
	orphan := dataDir + ".restore-applying-20261114T000000.000000000Z"
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	release, err := lockServeDataDir(dataDir)
	if err != nil {
		t.Fatalf("start after the journal was cleared: %v", err)
	}
	release()
	if _, err := os.Lstat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan decrypted claim kept: %v", err)
	}
}

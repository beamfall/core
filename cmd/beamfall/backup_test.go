package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/libencrypt"
	storecore "github.com/beamfall/core/internal/store"
)

func TestBackupInspectDecryptsOnlyToExplicitOutput(t *testing.T) {
	t.Parallel()

	key, ciphertext := inspectTestEnvelope(t, []byte("inspect plaintext"))
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "backup.bbk")
	outputPath := filepath.Join(dir, "restored.db")
	if err := os.WriteFile(inputPath, ciphertext, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := backup(context.Background(), []string{
		"inspect",
		"--input", inputPath,
		"--output", outputPath,
		"--key", key.Display(),
	}, &stdout)
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // test fixture: outputPath is confined to t.TempDir.
	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "inspect plaintext" {
		t.Fatalf("output = %q", got)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("output mode = %o, want 600", info.Mode().Perm())
	}
	if strings.Contains(stdout.String(), "inspect plaintext") || strings.Contains(stdout.String(), key.Display()) {
		t.Fatalf("stdout exposed secret material: %q", stdout.String())
	}
	for _, field := range []string{`"version":`, `"recipient_key_fingerprint":`, `"ephemeral_sender_key_fingerprint":`} {
		if !strings.Contains(stdout.String(), field) {
			t.Errorf("stdout missing safe metadata %s: %q", field, stdout.String())
		}
	}
}

func TestBackupInspectDecryptsV1Envelope(t *testing.T) {
	t.Parallel()

	key, err := backupcore.MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := inspectTestEnvelopeV1(t, key, []byte("legacy plaintext"))
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "backup-v1.bbk")
	outputPath := filepath.Join(dir, "restored-v1.db")
	if err := os.WriteFile(inputPath, ciphertext, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := backup(context.Background(), []string{
		"inspect",
		"--input", inputPath,
		"--output", outputPath,
		"--key", key.Display(),
	}, &stdout); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // test fixture: outputPath is confined to t.TempDir.
	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "legacy plaintext" {
		t.Fatalf("output = %q", got)
	}
	if !strings.Contains(stdout.String(), `"version":1`) {
		t.Fatalf("stdout = %q, want v1 metadata", stdout.String())
	}
}

func TestBackupInspectRequiresOutputBeforeDecrypting(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	err := backup(context.Background(), []string{
		"inspect",
		"--input", filepath.Join(t.TempDir(), "missing.bbk"),
		"--key", "sensitive-key-text",
	}, &stdout)
	if err == nil || err.Error() != "--output is required" {
		t.Fatalf("error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestBackupInspectWrongKeyPreservesErrUnwrapAndRemovesOutput(t *testing.T) {
	t.Parallel()

	_, ciphertext := inspectTestEnvelope(t, []byte("must not survive"))
	wrongKey, err := backupcore.MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "backup.bbk")
	outputPath := filepath.Join(dir, "restored.db")
	if err := os.WriteFile(inputPath, ciphertext, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err = backup(context.Background(), []string{
		"inspect",
		"--input", inputPath,
		"--output", outputPath,
		"--key", wrongKey.Display(),
	}, &stdout)
	if !errors.Is(err, libencrypt.ErrUnwrap) {
		t.Fatalf("error = %v, want ErrUnwrap", err)
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatalf("output remains after wrong key: %v", statErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestBackupInspectHelpIsDebugOnlyAndHidesKeyFlag(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	if err := backup(context.Background(), []string{"inspect", "--help"}, &stdout); err != nil {
		t.Fatal(err)
	}
	help := stdout.String()
	if !strings.Contains(help, "debug-only") {
		t.Fatalf("help does not identify debug-only command: %q", help)
	}
	if strings.Contains(help, "--key") {
		t.Fatalf("help exposed hidden key input flag: %q", help)
	}
}

func TestRestoreHelpDocumentsIdentityReplacementOverride(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	if err := restore(context.Background(), []string{"--help"}, &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "replace-identity") {
		t.Fatalf("restore help omits identity override: %q", stdout.String())
	}
}

func TestRestoreFailureRestoresOriginalAndReportsRecoveryPaths(t *testing.T) {
	manifest, dataDir, key := restoreFailureFixture(t)
	restoreFailure := errors.New("injected restore failure")
	oldRestoreBackup := restoreBackupFn
	restoreBackupFn = partialThenFailingRestore(restoreFailure)
	t.Cleanup(func() { restoreBackupFn = oldRestoreBackup })

	err := restore(context.Background(), []string{"--manifest", manifest, "--data-dir", dataDir, "--key", key}, io.Discard)
	if !errors.Is(err, restoreFailure) {
		t.Fatalf("restore err = %v, want injected failure", err)
	}
	// #nosec G304 -- dataDir is this test's own t.TempDir()-rooted live data
	// directory from restoreFailureFixture; the name is a fixed literal.
	original, readErr := os.ReadFile(filepath.Join(dataDir, "original"))
	if readErr != nil {
		t.Fatalf("read restored original: %v", readErr)
	}
	if string(original) != "original live bytes" {
		t.Fatalf("restored original = %q", original)
	}
	failed := restoreRecoveryPaths(t, dataDir, ".failed-restore-")
	if len(failed) != 1 {
		t.Fatalf("failed restore recovery paths = %v, want one", failed)
	}
	if _, statErr := os.Stat(filepath.Join(failed[0], "partial-restore")); statErr != nil {
		t.Fatalf("failed restore was not preserved: %v", statErr)
	}
	if !strings.Contains(err.Error(), dataDir) || !strings.Contains(err.Error(), failed[0]) {
		t.Fatalf("restore err omits recovery paths: %v", err)
	}
	if snapshots := restoreRecoveryPaths(t, dataDir, ".pre-restore-"); len(snapshots) != 0 {
		t.Fatalf("rollback left original snapshot paths: %v", snapshots)
	}
}

func TestRestoreFailureRestoresExistingEmptyDataDirectory(t *testing.T) {
	manifest, dataDir, key := restoreFailureFixture(t)
	if err := os.Remove(filepath.Join(dataDir, "original")); err != nil {
		t.Fatalf("empty live data directory: %v", err)
	}
	restoreFailure := errors.New("injected restore failure")
	oldRestoreBackup := restoreBackupFn
	restoreBackupFn = partialThenFailingRestore(restoreFailure)
	t.Cleanup(func() { restoreBackupFn = oldRestoreBackup })

	err := restore(context.Background(), []string{"--manifest", manifest, "--data-dir", dataDir, "--key", key}, io.Discard)
	if !errors.Is(err, restoreFailure) {
		t.Fatalf("restore err = %v, want injected failure", err)
	}
	entries, readErr := os.ReadDir(dataDir)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("restored empty data directory entries=%v err=%v", entries, readErr)
	}
	failed := restoreRecoveryPaths(t, dataDir, ".failed-restore-")
	if len(failed) != 1 {
		t.Fatalf("failed restore recovery paths = %v, want one", failed)
	}
	if _, statErr := os.Stat(filepath.Join(failed[0], "partial-restore")); statErr != nil {
		t.Fatalf("failed restore was not preserved: %v", statErr)
	}
	if !strings.Contains(err.Error(), dataDir) || !strings.Contains(err.Error(), failed[0]) {
		t.Fatalf("restore err omits recovery paths: %v", err)
	}
}

func TestRestoreSnapshotSyncFailureRollsBackDurably(t *testing.T) {
	manifest, dataDir, key := restoreFailureFixture(t)
	syncFailure := errors.New("injected snapshot sync failure")
	oldRestoreBackup := restoreBackupFn
	oldSync := syncRestoreDataDirRoot
	restoreCalled := false
	restoreBackupFn = func(context.Context, string, string) error {
		restoreCalled = true
		return nil
	}
	syncCalls := 0
	syncRestoreDataDirRoot = func(path string) error {
		syncCalls++
		if syncCalls == 1 {
			return syncFailure
		}
		return oldSync(path)
	}
	t.Cleanup(func() {
		restoreBackupFn = oldRestoreBackup
		syncRestoreDataDirRoot = oldSync
	})

	err := restore(context.Background(), []string{"--manifest", manifest, "--data-dir", dataDir, "--key", key}, io.Discard)
	if !errors.Is(err, syncFailure) {
		t.Fatalf("restore err = %v, want snapshot sync failure", err)
	}
	if restoreCalled {
		t.Fatal("restore ran after snapshot durability failed")
	}
	if syncCalls != 2 {
		t.Fatalf("parent sync calls = %d, want snapshot attempt plus rollback sync", syncCalls)
	}
	// #nosec G304 -- dataDir is the test's own t.TempDir()-rooted live path.
	original, readErr := os.ReadFile(filepath.Join(dataDir, "original"))
	if readErr != nil || string(original) != "original live bytes" {
		t.Fatalf("original after snapshot rollback = %q err=%v", original, readErr)
	}
	if snapshots := restoreRecoveryPaths(t, dataDir, ".pre-restore-"); len(snapshots) != 0 {
		t.Fatalf("snapshot rollback left recovery paths: %v", snapshots)
	}
}

func TestRestoreFailureWithoutPartialTargetRestoresOriginal(t *testing.T) {
	manifest, dataDir, key := restoreFailureFixture(t)
	restoreFailure := errors.New("injected pre-publish restore failure")
	oldRestoreBackup := restoreBackupFn
	restoreBackupFn = func(context.Context, string, string) error { return restoreFailure }
	t.Cleanup(func() { restoreBackupFn = oldRestoreBackup })

	err := restore(context.Background(), []string{"--manifest", manifest, "--data-dir", dataDir, "--key", key}, io.Discard)
	if !errors.Is(err, restoreFailure) {
		t.Fatalf("restore err = %v, want injected failure", err)
	}
	// #nosec G304 -- dataDir is the test's own t.TempDir()-rooted live path.
	original, readErr := os.ReadFile(filepath.Join(dataDir, "original"))
	if readErr != nil || string(original) != "original live bytes" {
		t.Fatalf("restored original = %q err=%v", original, readErr)
	}
	if !strings.Contains(err.Error(), dataDir) || !strings.Contains(err.Error(), "no failed restore directory") {
		t.Fatalf("restore err omits recovery status: %v", err)
	}
	if failed := restoreRecoveryPaths(t, dataDir, ".failed-restore-"); len(failed) != 0 {
		t.Fatalf("unexpected failed restore recovery paths: %v", failed)
	}
}

func TestRestoreRollbackSyncFailureReportsPreservedPaths(t *testing.T) {
	manifest, dataDir, key := restoreFailureFixture(t)
	restoreFailure := errors.New("injected restore failure")
	rollbackSyncFailure := errors.New("injected rollback sync failure")
	oldRestoreBackup := restoreBackupFn
	oldSync := syncRestoreDataDirRoot
	restoreBackupFn = partialThenFailingRestore(restoreFailure)
	syncCalls := 0
	syncRestoreDataDirRoot = func(path string) error {
		syncCalls++
		if syncCalls == 2 {
			return rollbackSyncFailure
		}
		return oldSync(path)
	}
	t.Cleanup(func() {
		restoreBackupFn = oldRestoreBackup
		syncRestoreDataDirRoot = oldSync
	})

	err := restore(context.Background(), []string{"--manifest", manifest, "--data-dir", dataDir, "--key", key}, io.Discard)
	if !errors.Is(err, restoreFailure) || !strings.Contains(err.Error(), rollbackSyncFailure.Error()) {
		t.Fatalf("restore err = %v, want restore and rollback sync failures", err)
	}
	failed := restoreRecoveryPaths(t, dataDir, ".failed-restore-")
	if len(failed) != 1 {
		t.Fatalf("failed restore recovery paths = %v, want one", failed)
	}
	if !strings.Contains(err.Error(), dataDir) || !strings.Contains(err.Error(), failed[0]) {
		t.Fatalf("restore err omits preserved paths: %v", err)
	}
	// #nosec G304 -- dataDir is the test's own t.TempDir()-rooted live path.
	original, readErr := os.ReadFile(filepath.Join(dataDir, "original"))
	if readErr != nil || string(original) != "original live bytes" {
		t.Fatalf("restored original = %q err=%v", original, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(failed[0], "partial-restore")); statErr != nil {
		t.Fatalf("failed restore recovery directory missing: %v", statErr)
	}
}

func TestRestoreRollbackFailureReportsBothPreservedDirectories(t *testing.T) {
	manifest, dataDir, key := restoreFailureFixture(t)
	restoreFailure := errors.New("injected restore failure")
	rollbackFailure := errors.New("injected rollback failure")
	oldRestoreBackup := restoreBackupFn
	oldRename := renameRestoreDataDir
	restoreBackupFn = partialThenFailingRestore(restoreFailure)
	renameCalls := 0
	renameRestoreDataDir = func(source, target string) error {
		renameCalls++
		if renameCalls == 3 {
			return rollbackFailure
		}
		// #nosec G703 -- source and target are the restore command's own
		// t.TempDir()-rooted data directory and its sibling recovery paths.
		return os.Rename(source, target)
	}
	t.Cleanup(func() {
		restoreBackupFn = oldRestoreBackup
		renameRestoreDataDir = oldRename
	})

	err := restore(context.Background(), []string{"--manifest", manifest, "--data-dir", dataDir, "--key", key}, io.Discard)
	if !errors.Is(err, restoreFailure) {
		t.Fatalf("restore err = %v, want injected restore failure", err)
	}
	if !strings.Contains(err.Error(), rollbackFailure.Error()) {
		t.Fatalf("restore err = %v, want rollback failure", err)
	}
	snapshots := restoreRecoveryPaths(t, dataDir, ".pre-restore-")
	failed := restoreRecoveryPaths(t, dataDir, ".failed-restore-")
	if len(snapshots) != 1 || len(failed) != 1 {
		t.Fatalf("recovery paths snapshots=%v failed=%v, want one of each", snapshots, failed)
	}
	if !strings.Contains(err.Error(), snapshots[0]) || !strings.Contains(err.Error(), failed[0]) {
		t.Fatalf("restore err omits preserved paths: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(snapshots[0], "original")); statErr != nil {
		t.Fatalf("original recovery directory missing: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(failed[0], "partial-restore")); statErr != nil {
		t.Fatalf("failed restore recovery directory missing: %v", statErr)
	}
}

// partialThenFailingRestore stubs restoreBackupFn so it writes a partial
// restore into the target data directory and then fails, exercising the
// rollback path. target is always this test binary's own t.TempDir()-rooted
// data directory, supplied by restoreFailureFixture via the --data-dir flag.
func partialThenFailingRestore(restoreFailure error) func(context.Context, string, string) error {
	return func(_ context.Context, _, target string) error {
		// #nosec G703 G304 -- target is the test's own t.TempDir()-rooted data
		// directory; no manifest- or archive-derived component reaches it.
		if err := os.MkdirAll(target, 0o700); err != nil {
			return err
		}
		// #nosec G703 G304 -- same t.TempDir()-rooted target as above.
		if err := os.WriteFile(filepath.Join(target, "partial-restore"), []byte("failed restore bytes"), 0o600); err != nil {
			return err
		}
		return restoreFailure
	}
}

func restoreFailureFixture(t *testing.T) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	backupDir := t.TempDir()
	sourceDir := t.TempDir()
	source, err := storecore.Open(ctx, sourceDir, storecore.Options{
		SeedDevelopmentData: true,
		AllowedRoots:        []string{backupDir},
	})
	if err != nil {
		t.Fatalf("open backup source: %v", err)
	}
	source.SetSnapshotSealer(backupcore.NewDirectorySealer(sourceDir))
	result, err := source.Backup(ctx, backupDir)
	if err != nil {
		_ = source.Close()
		t.Fatalf("backup source: %v", err)
	}
	escrow, err := backupcore.ReadBootstrapEscrow(sourceDir, time.Now())
	if err != nil {
		_ = source.Close()
		t.Fatalf("read backup key escrow: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("close backup source: %v", err)
	}
	dataDir := filepath.Join(t.TempDir(), "live")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir live data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "original"), []byte("original live bytes"), 0o600); err != nil {
		t.Fatalf("write original live data: %v", err)
	}
	return result.Manifest, dataDir, escrow.Key.Display()
}

func restoreRecoveryPaths(t *testing.T, dataDir, marker string) []string {
	t.Helper()
	paths, err := filepath.Glob(dataDir + marker + "*")
	if err != nil {
		t.Fatalf("glob restore recovery paths: %v", err)
	}
	return paths
}

func inspectTestEnvelope(t *testing.T, plaintext []byte) (backupcore.BackupKey, []byte) {
	t.Helper()

	key, err := backupcore.MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	bucket := &inspectMemoryBucket{objects: make(map[string][]byte)}
	if err := backupcore.NewService(bucket).Backup(context.Background(), key, "inspect-fixture", bytes.NewReader(plaintext)); err != nil {
		t.Fatal(err)
	}
	if len(bucket.objects) != 1 {
		t.Fatalf("backup objects = %d, want 1", len(bucket.objects))
	}
	for _, object := range bucket.objects {
		return key, object
	}
	t.Fatal("backup object missing")
	return backupcore.BackupKey{}, nil
}

func inspectTestEnvelopeV1(t *testing.T, key backupcore.BackupKey, plaintext []byte) []byte {
	t.Helper()

	dek, err := libencrypt.NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	defer libencrypt.ZeroKey(dek)
	keyBytes := key.Bytes()
	defer libencrypt.ZeroKey(keyBytes)
	wrapped, err := libencrypt.AEADWrap(keyBytes, dek)
	if err != nil {
		t.Fatal(err)
	}
	var envelope bytes.Buffer
	envelope.WriteString("BBK1")
	envelope.WriteByte(1)
	//nolint:gosec // test fixture: wrapped test key length is intentionally encoded in the uint16 wire field.
	if err := binary.Write(&envelope, binary.BigEndian, uint16(len(wrapped))); err != nil {
		t.Fatal(err)
	}
	envelope.Write(wrapped)
	if err := libencrypt.EncryptStream(&envelope, bytes.NewReader(plaintext), dek); err != nil {
		t.Fatal(err)
	}
	return envelope.Bytes()
}

type inspectMemoryBucket struct {
	objects map[string][]byte
}

func (b *inspectMemoryBucket) Put(_ context.Context, key string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	b.objects[key] = data
	return nil
}

func (b *inspectMemoryBucket) Get(_ context.Context, key string) (io.ReadCloser, error) {
	data, ok := b.objects[key]
	if !ok {
		return nil, backupcore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (b *inspectMemoryBucket) Delete(_ context.Context, key string) error {
	delete(b.objects, key)
	return nil
}

func (b *inspectMemoryBucket) List(_ context.Context, prefix string) ([]string, error) {
	var keys []string
	for key := range b.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type barrierWriter struct {
	dst     io.Writer
	entered chan struct{}
	release chan struct{}
}

func (w *barrierWriter) Write(p []byte) (int, error) {
	select {
	case <-w.entered:
	default:
		close(w.entered)
		<-w.release
	}
	return w.dst.Write(p)
}

func TestSealDirectoryRoundTripUsesEnvelopeV2(t *testing.T) {
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	public, err := key.PublicWrapKey()
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	want := []byte("SQLite format 3\x00secret fixture")
	if err := os.WriteFile(filepath.Join(source, "catalog.db"), want, 0o600); err != nil {
		t.Fatal(err)
	}
	envelope := filepath.Join(t.TempDir(), "set.bbk")
	if err := SealDirectory(context.Background(), public, source, envelope); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(envelope) //nolint:gosec // G304: envelope is a test-local path created beneath t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, want) || len(raw) < 5 || string(raw[:4]) != bbk1Magic || raw[4] != bbk1VersionV2 {
		t.Fatal("sealed directory is not an opaque BBK1 v2 envelope")
	}
	restore := filepath.Join(t.TempDir(), "restored")
	if _, err := OpenDirectory(context.Background(), key, envelope, restore); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(restore, "catalog.db")) //nolint:gosec // G304: restore is a test-local path beneath t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("restored bytes = %q, want %q", got, want)
	}
}

// Red-first evidence — SECAUDIT-20260817-BACKUP-DESCRIPTOR-TOCTOU-1
// Head e036ba3a430ea0d826815106d999acb38e756b30; exit 1:
// go test ./internal/backup -run 'TestWriteDirectoryTarRejects(File|Component|RegularInode)ReplacementAfterValidation' -count=1
// --- FAIL: TestWriteDirectoryTarRejectsFileReplacementAfterValidation
//
//	set_test.go:94: archive succeeded after a validated file was replaced by a symlink
//
// --- FAIL: TestWriteDirectoryTarRejectsComponentReplacementAfterValidation
//
//	set_test.go:131: archive succeeded after a validated path component was replaced by a symlink
//
// --- FAIL: TestWriteDirectoryTarRejectsRegularInodeReplacementAfterValidation
//
//	set_test.go:165: archive read replacement inode canary bytes
//
// Focused verify: go test ./internal/backup/... -count=1
// Head a2d07d5f95a10c2371cd8ce8ead1776ff527b98b; exit 0; elapsed 1 second.
// UTC 2026-08-20T12:46:24Z–2026-08-20T12:46:25Z.
// Log: .agent-evidence/SECAUDIT-20260817-BACKUP-DESCRIPTOR-TOCTOU-1-focused.txt
//
// Resumption focused verify — SECAUDIT-20260817-BACKUP-DESCRIPTOR-TOCTOU-1
// Command: GOCACHE=/tmp/beamfall-secaudit-go-cache go test ./internal/backup ./internal/store ./cmd/beamfall ./internal/app -run 'Test(SealDirectoryRoundTripUsesEnvelopeV2|WriteDirectoryTarRejectsFileReplacementAfterValidation|WriteDirectoryTarRejectsComponentReplacementAfterValidation|WriteDirectoryTarRejectsRegularInodeReplacementAfterValidation|SealDirectoryRejectsFileMutationAcrossStagesWithoutPublication|SealDirectoryRejectsComponentReplacementBetweenStatAndOpenWithoutPublication|SealDirectoryRejectsManifestRewriteAfterDiscoveryWithoutPublication|SealDirectoryManifestedRoundTrip|ConfinedArchivePathRejectsSymlinkEscape|ExtractDirectoryTarRejectsSymlinkEscape|BackupCreatesPairedDatabaseSnapshotsAndManifest|BackupIncludesEncryptedLibraryCiphertextSnapshotsAndManifest|RestoreBackupReconstructsEncryptedLibraryCiphertextAndSchemaMetadata|BackupV2RestoresOperationalPayloadAndExcludesRegenerableState|VerifyBackupSetRejectsMissingRequiredDatabases|RestoreRejectsOperationalTamperAndTraversalBeforeMutation|RestoreCommandVerifiesBeforeSnapshot|RestoreCommandSnapshotsCurrentDataDirThenRestores|DatabaseBackupHandlerPrunesToKeepN)$' -count=1
// Head 0c344de4e3cda41b7239f22725278cc8959f6b88; exit 0; elapsed 6 seconds.
// UTC 2026-08-21T16:33:00Z–2026-08-21T16:33:06Z.
// Log: .agent-evidence/SECAUDIT-20260817-BACKUP-DESCRIPTOR-TOCTOU-1-focused.txt
func TestWriteDirectoryTarRejectsFileReplacementAfterValidation(t *testing.T) {
	source := t.TempDir()
	path := filepath.Join(source, "catalog.db")
	validated := []byte("validated bytes")
	if err := os.WriteFile(path, validated, 0o600); err != nil {
		t.Fatal(err)
	}
	canary := bytes.Repeat([]byte("X"), len(validated))
	outside := filepath.Join(t.TempDir(), "outside.db")
	if err := os.WriteFile(outside, canary, 0o600); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	barrier := &barrierWriter{dst: &archive, entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- writeDirectoryTar(context.Background(), barrier, source) }()
	<-barrier.entered
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	close(barrier.release)

	if err := <-done; err == nil {
		t.Fatal("archive succeeded after a validated file was replaced by a symlink")
	}
	if bytes.Contains(archive.Bytes(), canary) {
		t.Fatal("archive read outside-root canary bytes")
	}
}

func TestWriteDirectoryTarRejectsComponentReplacementAfterValidation(t *testing.T) {
	source := t.TempDir()
	nested := filepath.Join(source, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	validated := []byte("validated bytes")
	if err := os.WriteFile(filepath.Join(nested, "catalog.db"), validated, 0o600); err != nil {
		t.Fatal(err)
	}
	canary := bytes.Repeat([]byte("Y"), len(validated))
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "catalog.db"), canary, 0o600); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	barrier := &barrierWriter{dst: &archive, entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- writeDirectoryTar(context.Background(), barrier, source) }()
	<-barrier.entered
	if err := os.Rename(nested, filepath.Join(source, "validated-nested")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, nested); err != nil {
		t.Fatal(err)
	}
	close(barrier.release)

	if err := <-done; err == nil {
		t.Fatal("archive succeeded after a validated path component was replaced by a symlink")
	}
	if bytes.Contains(archive.Bytes(), canary) {
		t.Fatal("archive read outside-root component canary bytes")
	}
}

func TestWriteDirectoryTarRejectsRegularInodeReplacementAfterValidation(t *testing.T) {
	source := t.TempDir()
	path := filepath.Join(source, "catalog.db")
	validated := []byte("validated bytes")
	if err := os.WriteFile(path, validated, 0o600); err != nil {
		t.Fatal(err)
	}
	canary := bytes.Repeat([]byte("Z"), len(validated))
	replacement := filepath.Join(source, "replacement.db")
	if err := os.WriteFile(replacement, canary, 0o600); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	barrier := &barrierWriter{dst: &archive, entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- writeDirectoryTar(context.Background(), barrier, source) }()
	<-barrier.entered
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	close(barrier.release)

	if err := <-done; err == nil {
		t.Fatal("archive succeeded after a validated regular file inode was replaced")
	}
	if bytes.Contains(archive.Bytes(), canary) {
		t.Fatal("archive read replacement inode canary bytes")
	}
}

type archiveStageBarrier struct {
	stage   archiveStage
	name    string
	entered chan struct{}
	release chan struct{}
}

func newArchiveStageBarrier(stage archiveStage, name string) *archiveStageBarrier {
	return &archiveStageBarrier{stage: stage, name: name, entered: make(chan struct{}), release: make(chan struct{})}
}

func (b *archiveStageBarrier) reach(stage archiveStage, name string) {
	if stage != b.stage || name != b.name {
		return
	}
	close(b.entered)
	<-b.release
}

func TestSealDirectoryRejectsFileMutationAcrossStagesWithoutPublication(t *testing.T) {
	tests := []struct {
		name        string
		stage       archiveStage
		barrierName string
		inPlace     bool
	}{
		{name: "discovery_to_stat", stage: archiveStageDiscovered},
		{name: "stat_to_open", stage: archiveStageStatted, barrierName: "catalog.db"},
		{name: "open_to_hash", stage: archiveStageOpened, barrierName: "catalog.db", inPlace: true},
		{name: "hash_to_archive", stage: archiveStageHashed, barrierName: "catalog.db", inPlace: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, path, validated := writeManifestedArchiveSource(t, "catalog.db")
			canary := bytes.Repeat([]byte("Q"), len(validated))
			outside := filepath.Join(t.TempDir(), "outside.db")
			if err := os.WriteFile(outside, canary, 0o600); err != nil {
				t.Fatal(err)
			}
			original, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			barrier := newArchiveStageBarrier(test.stage, test.barrierName)
			dest := filepath.Join(t.TempDir(), "set.bbk")
			done := sealDirectoryAtBarrier(t, source, dest, barrier)

			if test.inPlace {
				if err := os.WriteFile(path, canary, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, original.ModTime(), original.ModTime()); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			}
			close(barrier.release)
			assertSealNotPublished(t, dest, awaitSeal(t, done))
		})
	}
}

func TestSealDirectoryRejectsComponentReplacementBetweenStatAndOpenWithoutPublication(t *testing.T) {
	source, _, validated := writeManifestedArchiveSource(t, filepath.Join("nested", "catalog.db"))
	nested := filepath.Join(source, "nested")
	outside := t.TempDir()
	canary := bytes.Repeat([]byte("R"), len(validated))
	if err := os.WriteFile(filepath.Join(outside, "catalog.db"), canary, 0o600); err != nil {
		t.Fatal(err)
	}
	barrier := newArchiveStageBarrier(archiveStageStatted, "nested")
	dest := filepath.Join(t.TempDir(), "set.bbk")
	done := sealDirectoryAtBarrier(t, source, dest, barrier)
	if err := os.Rename(nested, filepath.Join(source, "validated-nested")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, nested); err != nil {
		t.Fatal(err)
	}
	close(barrier.release)
	assertSealNotPublished(t, dest, awaitSeal(t, done))
}

func TestSealDirectoryRejectsManifestRewriteAfterDiscoveryWithoutPublication(t *testing.T) {
	source, _, _ := writeManifestedArchiveSource(t, "catalog.db")
	manifestPath := filepath.Join(source, "manifest.json")
	original, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(manifestPath) //nolint:gosec // G304: manifestPath is a test-local path beneath t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	rewritten := bytes.Replace(raw, []byte(`"backup"`), []byte(`"xxxxxx"`), 1)
	if len(rewritten) != len(raw) || bytes.Equal(rewritten, raw) {
		t.Fatal("manifest rewrite fixture did not preserve size")
	}
	barrier := newArchiveStageBarrier(archiveStageDiscovered, "")
	dest := filepath.Join(t.TempDir(), "set.bbk")
	done := sealDirectoryAtBarrier(t, source, dest, barrier)
	if err := os.WriteFile(manifestPath, rewritten, 0o600); err != nil { //nolint:gosec // G703: intentional rewrite of a test-local path beneath t.TempDir.
		t.Fatal(err)
	}
	if err := os.Chtimes(manifestPath, original.ModTime(), original.ModTime()); err != nil {
		t.Fatal(err)
	}
	close(barrier.release)
	assertSealNotPublished(t, dest, awaitSeal(t, done))
}

func TestSealDirectoryManifestedRoundTrip(t *testing.T) {
	source, _, validated := writeManifestedArchiveSource(t, "catalog.db")
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	public, err := key.PublicWrapKey()
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "set.bbk")
	if err := SealDirectory(context.Background(), public, source, dest); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if _, err := OpenDirectory(context.Background(), key, dest, restored); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(restored, "catalog.db")) //nolint:gosec // G304: restored is a test-local path beneath t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, validated) {
		t.Fatalf("restored bytes = %q, want %q", got, validated)
	}
}

func writeManifestedArchiveSource(t *testing.T, relative string) (string, string, []byte) {
	t.Helper()
	source := t.TempDir()
	path := filepath.Join(source, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	validated := []byte("validated bytes")
	if err := os.WriteFile(path, validated, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(validated)
	manifest := map[string]any{
		"type": "backup",
		"databases": map[string]any{
			"catalog": map[string]any{
				"backupPath": path,
				"sizeBytes":  len(validated),
				"sha256":     hex.EncodeToString(digest[:]),
			},
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "manifest.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return source, path, validated
}

func sealDirectoryAtBarrier(t *testing.T, source, dest string, barrier *archiveStageBarrier) <-chan error {
	t.Helper()
	t.Cleanup(func() {
		select {
		case <-barrier.release:
		default:
			close(barrier.release)
		}
	})
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	public, err := key.PublicWrapKey()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- sealDirectory(ctx, public, source, dest, &directoryArchiveHooks{reachStage: barrier.reach})
	}()
	select {
	case <-barrier.entered:
		return done
	case err := <-done:
		t.Fatalf("seal returned before stage %s for %q: %v", barrier.stage, barrier.name, err)
	case <-time.After(5 * time.Second):
		t.Fatalf("seal did not reach stage %s for %q", barrier.stage, barrier.name)
	}
	return nil
}

func awaitSeal(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("seal did not return after archive barrier was released")
	}
	return nil
}

func assertSealNotPublished(t *testing.T, dest string, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("seal succeeded after the validated backup input changed")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("failed seal published destination: %v", statErr)
	}
	entries, readErr := os.ReadDir(filepath.Dir(dest))
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".backup-envelope-") {
			t.Fatalf("failed seal left partial publication %q", entry.Name())
		}
	}
}

func TestConfinedArchivePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := confinedArchivePath(root, filepath.Join("escape", "catalog.db")); err == nil {
		t.Fatal("archive path through an escaping symlink succeeded, want rejection")
	}
}

func TestExtractDirectoryTarRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	payload := []byte("must stay confined")
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: "escape/catalog.db", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractDirectoryTar(context.Background(), &archive, root); err == nil {
		t.Fatal("archive extraction through an escaping symlink succeeded, want rejection")
	}
	if _, err := os.Stat(filepath.Join(outside, "catalog.db")); !os.IsNotExist(err) {
		t.Fatalf("escaping archive wrote outside restore root: %v", err)
	}
}

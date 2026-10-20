package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func importManagedFile(t *testing.T, st *store.Store, libraryID store.LibraryID, path, title, oshash string) store.MediaID {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(title), 0o644); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          libraryID,
		Path:               path,
		Title:              title,
		Container:          "mp4",
		Codec:              "h264",
		Class:              store.MediaClassVideo,
		ContentFingerprint: store.HashMediaFilePath(path),
		OSHash:             oshash,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile(%s): %v", path, err)
	}
	return media.ID
}

// convertLeaseSeq disambiguates job IDs when one test claims several leases.
var convertLeaseSeq int

// claimConvertLease enqueues and claims a convert-kind job so a direct store
// test holds a REAL live lease: ConvertManagedFile is fenced on the job lease
// (ADR-0174 ownership succession) and has no unfenced call path. The claimed
// job carries a very high priority so a seeded queue can never win the claim.
func claimConvertLease(t *testing.T, st *store.Store) store.ConvertOwnerLease {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	convertLeaseSeq++
	id := store.JobID(fmt.Sprintf("%s-convert-lease-%d", t.Name(), convertLeaseSeq))
	if _, err := st.EnqueueJob(ctx, store.Job{ID: id, Kind: "convert-managed-file", Priority: 1 << 20, CreatedAt: now}); err != nil {
		t.Fatalf("claimConvertLease enqueue: %v", err)
	}
	claimed, err := st.ClaimNextJob(ctx, "convert-test-worker", nil, now)
	if err != nil {
		t.Fatalf("claimConvertLease claim: %v", err)
	}
	if claimed.ID != id {
		t.Fatalf("claimConvertLease claimed %q, want %q", claimed.ID, id)
	}
	return store.ConvertOwnerLease{JobID: claimed.ID, Owner: claimed.LeaseOwner}
}

func TestManagedFolderFileOpsCreateMoveDeleteRestore(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllLibraries: true}

	created, err := st.CreateManagedFolder(ctx, "library-local", root, "incoming", filter)
	if err != nil {
		t.Fatalf("CreateManagedFolder: %v", err)
	}
	if created.Kind != "folder" || created.Path != filepath.Join(canonicalRoot, "incoming") {
		t.Fatalf("created = %#v", created)
	}
	if err := os.WriteFile(filepath.Join(created.Path, "item.txt"), []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	moved, err := st.RenameManagedPath(ctx, "library-local", filepath.Join(created.Path, "item.txt"), "renamed.txt", filter)
	if err != nil {
		t.Fatalf("RenameManagedPath: %v", err)
	}
	if moved.Path != filepath.Join(canonicalRoot, "incoming", "renamed.txt") || moved.Kind != "file" {
		t.Fatalf("renamed = %#v", moved)
	}
	destination, err := st.CreateManagedFolder(ctx, "library-local", root, "organized", filter)
	if err != nil {
		t.Fatalf("CreateManagedFolder destination: %v", err)
	}
	moved, err = st.MoveManagedPath(ctx, "library-local", moved.Path, destination.Path, filter)
	if err != nil {
		t.Fatalf("MoveManagedPath: %v", err)
	}
	if moved.Path != filepath.Join(canonicalRoot, "organized", "renamed.txt") || moved.Kind != "file" {
		t.Fatalf("moved = %#v", moved)
	}
	trash, err := st.DeleteManagedPath(ctx, "library-local", moved.Path, filter, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("DeleteManagedPath: %v", err)
	}
	if trash.TrashID == "" || trash.OriginalPath != moved.Path || trash.Kind != "file" {
		t.Fatalf("trash = %#v", trash)
	}
	if _, err := os.Stat(moved.Path); !os.IsNotExist(err) {
		t.Fatalf("deleted path still exists: %v", err)
	}
	restored, err := st.RestoreManagedPath(ctx, "library-local", trash.TrashID, filter, time.Unix(1700000001, 0))
	if err != nil {
		t.Fatalf("RestoreManagedPath: %v", err)
	}
	if restored.Path != moved.Path || restored.Kind != "file" {
		t.Fatalf("restored = %#v", restored)
	}
	if data, err := os.ReadFile(moved.Path); err != nil || string(data) != "media" {
		t.Fatalf("restored file = %q, err=%v", data, err)
	}
}

func TestSweepManagedFileTrashPurgesOnlyExpiredEntries(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllLibraries: true}

	if err := os.WriteFile(filepath.Join(root, "expired.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fresh.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1700000000, 0).UTC()
	// Expired: deleted well outside the retention window.
	expiredDeletedAt := now.Add(-store.TrashRetention).Add(-time.Hour)
	expired, err := st.DeleteManagedPath(ctx, "library-local", filepath.Join(root, "expired.txt"), filter, expiredDeletedAt)
	if err != nil {
		t.Fatalf("DeleteManagedPath(expired): %v", err)
	}
	// In-window: deleted just under one retention window ago (not yet expired).
	freshDeletedAt := now.Add(-store.TrashRetention).Add(time.Hour)
	fresh, err := st.DeleteManagedPath(ctx, "library-local", filepath.Join(root, "fresh.txt"), filter, freshDeletedAt)
	if err != nil {
		t.Fatalf("DeleteManagedPath(fresh): %v", err)
	}

	purged, skipped, err := st.SweepManagedFileTrash(ctx, now)
	if err != nil {
		t.Fatalf("SweepManagedFileTrash: %v", err)
	}
	if skipped {
		t.Fatal("SweepManagedFileTrash unexpectedly skipped")
	}
	if purged != 1 {
		t.Fatalf("purged = %d, want 1 (only the expired entry)", purged)
	}

	// The expired entry's on-disk trash directory is gone.
	expiredDir := filepath.Dir(expired.TrashPath)
	if _, err := os.Stat(expiredDir); !os.IsNotExist(err) {
		t.Fatalf("expired trash dir still present: %v", err)
	}
	// The in-window entry is preserved and still restorable.
	freshDir := filepath.Dir(fresh.TrashPath)
	if _, err := os.Stat(freshDir); err != nil {
		t.Fatalf("in-window trash dir missing: %v", err)
	}
	restored, err := st.RestoreManagedPath(ctx, "library-local", fresh.TrashID, filter, now)
	if err != nil {
		t.Fatalf("RestoreManagedPath(fresh): %v", err)
	}
	if data, err := os.ReadFile(restored.Path); err != nil || string(data) != "new" {
		t.Fatalf("restored in-window file = %q, err=%v", data, err)
	}
}

func TestSweepManagedFileTrashTrashDisabledRequiresConfirmation(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true, TrashDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	purged, skipped, err := st.SweepManagedFileTrash(ctx, time.Now().UTC())
	if !errors.Is(err, store.ErrTrashDisabledConfirmationRequired) || !skipped || purged != 0 {
		t.Fatalf("SweepManagedFileTrash trash disabled purged=%d skipped=%v err=%v", purged, skipped, err)
	}
	if got := st.TrashDisabledSweepSkips(); got != 1 {
		t.Fatalf("TrashDisabledSweepSkips = %d, want 1", got)
	}
}

func TestBatchRenameManagedPathsAppliesAndUndoRestores(t *testing.T) {
	// Single-item batch renames use this same apply/UndoBatch path. Direct
	// RenameManagedPath and MoveManagedPath do not create undo batches.
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	oldA := filepath.Join(canonicalRoot, "a.mp4")
	oldB := filepath.Join(canonicalRoot, "b.mp4")
	mediaA := importManagedFile(t, st, "library-local", oldA, "Alpha", "00000000dddd0001")
	mediaB := importManagedFile(t, st, "library-local", oldB, "Beta", "00000000dddd0002")
	newA := filepath.Join(canonicalRoot, "alpha-renamed.mp4")
	newB := filepath.Join(canonicalRoot, "beta-renamed.mp4")

	batch, err := st.BatchRenameManagedPaths(ctx, "library-local", []store.ManagedPathRename{
		{Path: oldA, Name: filepath.Base(newA)},
		{Path: oldB, Name: filepath.Base(newB)},
	}, store.LibraryFilter{AllLibraries: true}, time.Unix(1700000100, 0))
	if err != nil {
		t.Fatalf("BatchRenameManagedPaths: %v", err)
	}
	if batch.ID == "" || batch.Kind != "move" || batch.Affected != 2 {
		t.Fatalf("batch = %#v, want move batch affecting 2", batch)
	}
	for path, wantContent := range map[string]string{newA: "Alpha", newB: "Beta"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("renamed file missing at %s: %v", path, err)
		}
		assertManagedFileContent(t, path, wantContent)
	}
	for mediaID, want := range map[store.MediaID]string{mediaA: newA, mediaB: newB} {
		file, err := st.PrimaryMediaFile(ctx, mediaID, store.LibraryFilter{AllLibraries: true})
		if err != nil {
			t.Fatal(err)
		}
		if file.Path != want {
			t.Fatalf("media %s path = %q, want %q", mediaID, file.Path, want)
		}
	}

	undo, err := st.UndoBatch(ctx, batch.ID, store.LibraryFilter{AllLibraries: true}, time.Unix(1700000101, 0))
	if err != nil {
		t.Fatalf("UndoBatch: %v", err)
	}
	if undo.Affected != 2 {
		t.Fatalf("undo = %#v, want 2 affected", undo)
	}
	for path, wantContent := range map[string]string{oldA: "Alpha", oldB: "Beta"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("restored file missing at %s: %v", path, err)
		}
		assertManagedFileContent(t, path, wantContent)
	}
}

func assertManagedFileContent(t *testing.T, path, want string) {
	t.Helper()
	// #nosec G304 -- path is constructed beneath a test-owned temporary directory.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("content at %s = %q, want %q", path, got, want)
	}
}

func TestBatchRenameManagedPathsCollisionRollsBack(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	oldA := filepath.Join(canonicalRoot, "a.mp4")
	oldB := filepath.Join(canonicalRoot, "b.mp4")
	mediaA := importManagedFile(t, st, "library-local", oldA, "Alpha", "00000000dddd0003")
	mediaB := importManagedFile(t, st, "library-local", oldB, "Beta", "00000000dddd0004")
	target := filepath.Join(canonicalRoot, "same.mp4")

	if _, err := st.BatchRenameManagedPaths(ctx, "library-local", []store.ManagedPathRename{
		{Path: oldA, Name: filepath.Base(target)},
		{Path: oldB, Name: filepath.Base(target)},
	}, store.LibraryFilter{AllLibraries: true}, time.Unix(1700000102, 0)); err == nil {
		t.Fatal("BatchRenameManagedPaths collision unexpectedly succeeded")
	}
	for _, path := range []string{oldA, oldB} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("original file missing after collision rollback at %s: %v", path, err)
		}
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("collision target exists after rollback: %v", err)
	}
	for mediaID, want := range map[store.MediaID]string{mediaA: oldA, mediaB: oldB} {
		file, err := st.PrimaryMediaFile(ctx, mediaID, store.LibraryFilter{AllLibraries: true})
		if err != nil {
			t.Fatal(err)
		}
		if file.Path != want {
			t.Fatalf("media %s path after rollback = %q, want %q", mediaID, file.Path, want)
		}
	}
}

// TestManagedMediaPathsSQLFilterIsolation verifies that the SQL-level path
// filter in managedMediaPaths touches only the rows that match the target path
// or its sub-tree prefix, not unrelated rows in the same library.
func TestManagedMediaPathsSQLFilterIsolation(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllLibraries: true}

	// Import two files in the same library — only one will be the rename target.
	targetPath := filepath.Join(canonicalRoot, "target.mp4")
	bystanderPath := filepath.Join(canonicalRoot, "bystander.mp4")
	targetID := importManagedFile(t, st, "library-local", targetPath, "Target", "cafecafe00000001")
	importManagedFile(t, st, "library-local", bystanderPath, "Bystander", "cafecafe00000002")

	newName := "renamed-target.mp4"
	moved, err := st.RenameManagedPath(ctx, "library-local", targetPath, newName, filter)
	if err != nil {
		t.Fatalf("RenameManagedPath: %v", err)
	}
	renamedPath := filepath.Join(canonicalRoot, newName)
	if moved.Path != renamedPath {
		t.Fatalf("moved.Path = %q, want %q", moved.Path, renamedPath)
	}

	// The renamed media's DB path must reflect the new path.
	renamedFile, err := st.PrimaryMediaFile(ctx, targetID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if renamedFile.Path != renamedPath {
		t.Fatalf("renamed media path = %q, want %q", renamedFile.Path, renamedPath)
	}

	// The bystander's path must be unchanged — SQL filter must not have touched it.
	bystanders, err := st.SearchMedia(ctx, filter, "Bystander", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(bystanders) != 1 {
		t.Fatalf("bystander search = %d items, want 1", len(bystanders))
	}
	bystanderFile, err := st.PrimaryMediaFile(ctx, bystanders[0].ID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if bystanderFile.Path != bystanderPath {
		t.Fatalf("bystander path = %q, want unchanged %q", bystanderFile.Path, bystanderPath)
	}

	// Also test folder-kind prefix match: import a file under a sub-folder, then
	// delete the folder and verify only the folder-rooted file is marked missing.
	subdir, err := st.CreateManagedFolder(ctx, "library-local", canonicalRoot, "subdir", filter)
	if err != nil {
		t.Fatalf("CreateManagedFolder: %v", err)
	}
	subdirFilePath := filepath.Join(subdir.Path, "inside.mp4")
	importManagedFile(t, st, "library-local", subdirFilePath, "Inside", "cafecafe00000003")

	trash, err := st.DeleteManagedPath(ctx, "library-local", subdir.Path, filter, time.Unix(1700010000, 0))
	if err != nil {
		t.Fatalf("DeleteManagedPath folder: %v", err)
	}
	if trash.Kind != "folder" {
		t.Fatalf("trash.Kind = %q, want folder", trash.Kind)
	}

	// After delete, the bystander and renamed file must still be available.
	renamedFile2, err := st.PrimaryMediaFile(ctx, targetID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if renamedFile2.Path != renamedPath {
		t.Fatalf("renamed path after folder delete = %q, want %q", renamedFile2.Path, renamedPath)
	}
}

func TestManagedPathOpsRejectTraversalAndRootEscape(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	outside := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllLibraries: true}

	if _, err := st.CreateManagedFolder(ctx, "library-local", root, "../escape", filter); err == nil {
		t.Fatal("CreateManagedFolder accepted traversal name")
	}
	if _, err := st.CreateManagedFolder(ctx, "library-local", outside, "x", filter); err == nil {
		t.Fatal("CreateManagedFolder accepted parent outside root")
	}
	inside := filepath.Join(root, "inside")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MoveManagedPath(ctx, "library-local", inside, outside, filter); err == nil {
		t.Fatal("MoveManagedPath accepted destination outside root")
	}
}

// TestDeleteManagedPathRenameFailureLeavesNoTrashResidue proves the manifest
// DeleteManagedPath records BEFORE the trash rename never leaks: when the rename
// itself fails, the whole trash entry (manifest included) is discarded and the
// source file is left untouched at its original path.
func TestDeleteManagedPathRenameFailureLeavesNoTrashResidue(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not deny renames")
	}
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllLibraries: true}

	parent := filepath.Join(root, "locked")
	if err := os.Mkdir(parent, 0o750); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "item.txt")
	if err := os.WriteFile(target, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A read-only parent makes the trash rename fail: the move needs write
	// permission on the source's directory to unlink the name.
	//nolint:gosec // G302 does not distinguish directories: a dir needs its
	// execute bit, and 0555 is exactly the read-only-parent state under test.
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G302: restoring owner rwx on a directory, not a file mode.
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	if _, err := st.DeleteManagedPath(ctx, "library-local", target, filter, time.Unix(1700000000, 0)); err == nil {
		t.Fatal("DeleteManagedPath with an unrenameable source: want error")
	}
	//nolint:gosec // G304: path is built from t.TempDir(), not external input.
	if data, err := os.ReadFile(target); err != nil || string(data) != "media" {
		t.Fatalf("source = %q, err=%v; want it untouched", data, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".beamfall-trash", "managed-fileops"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("trash root has %d entries, want none (the pre-rename manifest must not leak)", len(entries))
	}
}

// TestDeleteManagedPathDanglingManifestIsRecoverable exercises the only crash
// window the manifest-before-rename ordering leaves open: the manifest is durable
// but the entry is still at its original path. That state must be benign —
// restore refuses it without destroying anything, and the sweep reclaims the
// dangling record once expired while leaving the original file alone. (The
// reverse ordering's window — a trashed entry with no manifest — is invisible to
// both, i.e. an unrecoverable orphan.)
func TestDeleteManagedPathDanglingManifestIsRecoverable(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllLibraries: true}

	original := filepath.Join(root, "item.txt")
	if err := os.WriteFile(original, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	deletedAt := time.Unix(1700000000, 0).UTC()
	trash, err := st.DeleteManagedPath(ctx, "library-local", original, filter, deletedAt)
	if err != nil {
		t.Fatalf("DeleteManagedPath: %v", err)
	}
	// Simulate a crash between the durable manifest and the rename: the record is
	// on disk, the entry is not.
	if err := os.Rename(trash.TrashPath, original); err != nil {
		t.Fatal(err)
	}

	if _, err := st.RestoreManagedPath(ctx, "library-local", trash.TrashID, filter, deletedAt); err == nil {
		t.Fatal("RestoreManagedPath over a live original: want error")
	}
	//nolint:gosec // G304: path is built from t.TempDir(), not external input.
	if data, err := os.ReadFile(original); err != nil || string(data) != "media" {
		t.Fatalf("original after refused restore = %q, err=%v; want it untouched", data, err)
	}
	purged, skipped, err := st.SweepManagedFileTrash(ctx, deletedAt.Add(store.TrashRetention).Add(time.Hour))
	if err != nil {
		t.Fatalf("SweepManagedFileTrash: %v", err)
	}
	if skipped {
		t.Fatal("SweepManagedFileTrash unexpectedly skipped")
	}
	if purged != 1 {
		t.Fatalf("purged = %d, want 1 (the dangling record)", purged)
	}
	//nolint:gosec // G304: path is built from t.TempDir(), not external input.
	if data, err := os.ReadFile(original); err != nil || string(data) != "media" {
		t.Fatalf("original after sweep = %q, err=%v; want it untouched", data, err)
	}
	if _, err := os.Stat(filepath.Dir(trash.TrashPath)); !os.IsNotExist(err) {
		t.Fatalf("dangling trash dir still present: %v", err)
	}
}

// swappingConverter stands in for an FFmpeg engine whose staging artifact is
// replaced between the run and the admission — the post-check swap. Instead of a
// real output it leaves a symlink at the staging path aimed at a file outside
// every Library root, which is what an admission that re-walks the staging NAME
// would copy in and catalog as managed media.
type swappingConverter struct {
	target string
}

func (c *swappingConverter) ConvertFile(_ context.Context, _, dst, _ string) (string, string, store.MediaClass, error) {
	if err := os.Symlink(c.target, dst); err != nil {
		return "", "", "", err
	}
	return "mp4", "h264", store.MediaClassVideo, nil
}

// A staging artifact whose final component became a symlink is refused, and the
// refusal is a clean rollback: nothing is admitted into the Library root and the
// original is still in place, untrashed.
func TestConvertManagedFileRefusesSwappedStagingArtifact(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(canonicalRoot, "clip.mkv")
	importManagedFile(t, st, "library-local", src, "clip", "oshash-swap")

	outside := filepath.Join(t.TempDir(), "out-of-library.bin")
	if err := os.WriteFile(outside, []byte("never-admitted"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = st.ConvertManagedFile(ctx, "library-local", src, "h264-mp4",
		&swappingConverter{target: outside}, store.LibraryFilter{AllLibraries: true}, claimConvertLease(t, st), time.Unix(1700000000, 0))
	if err == nil {
		t.Fatal("ConvertManagedFile admitted a swapped staging artifact, want refusal")
	}
	// The refusal must come from the identity check itself, not from a later step
	// tripping over the consequences: the admission is where a swapped artifact
	// has to stop, before anything downstream reasons about it as managed media.
	if !strings.Contains(err.Error(), "changed identity") {
		t.Fatalf("ConvertManagedFile err = %v, want the admission identity refusal", err)
	}

	admitted := filepath.Join(canonicalRoot, "clip-h264-mp4.mp4")
	if _, statErr := os.Lstat(admitted); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("converted output exists after a refused admission: %v", statErr)
	}
	//nolint:gosec // G304: path is built from t.TempDir(), not external input.
	if data, readErr := os.ReadFile(src); readErr != nil || string(data) != "clip" {
		t.Fatalf("original after refused conversion = %q, err=%v; want it untouched", data, readErr)
	}
}

// The manifest is the ONLY proof a trash entry has expired, so a purge must
// refuse to read one it cannot resolve directly. Here an in-window entry's
// manifest name is re-pointed — with an ordinary same-directory symlink, the
// kind a root-scoped read would otherwise follow — at a planted record claiming
// the retention window closed. Following it turns the most destructive step in
// the managed-file path into an attacker-triggered early delete of live media.
func TestSweepManagedFileTrashRefusesSymlinkedManifest(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllLibraries: true}
	original := filepath.Join(root, "clip.mkv")
	if err := os.WriteFile(original, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0).UTC()
	// Deleted just now: the entry is well inside its retention window.
	trash, err := st.DeleteManagedPath(ctx, "library-local", original, filter, now)
	if err != nil {
		t.Fatalf("DeleteManagedPath: %v", err)
	}
	entryDir := filepath.Dir(trash.TrashPath)

	//nolint:gosec // G304: path is built from t.TempDir(), not external input.
	genuine, err := os.ReadFile(filepath.Join(entryDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read genuine manifest: %v", err)
	}
	expiredClaim := strings.Replace(string(genuine),
		now.Format(time.RFC3339Nano),
		now.Add(-store.TrashRetention).Add(-time.Hour).Format(time.RFC3339Nano), 1)
	if expiredClaim == string(genuine) {
		t.Fatalf("could not rewrite the manifest timestamp in %q", genuine)
	}
	planted := filepath.Join(entryDir, "manifest.planted.json")
	// The taint gosec tracks here is expiredClaim, not the destination: it is the
	// genuine manifest's bytes read back from disk, so the analysis treats them as
	// untrusted. Planting exactly those rewritten bytes IS the test — the sweep must
	// refuse to follow a symlinked manifest name however convincing the record it
	// points at. The destination is a fixed literal joined onto entryDir, which the
	// store derived from a t.TempDir() library root; neither is external input.
	//nolint:gosec // G703: content is this test's own rewritten manifest; path is a literal under t.TempDir().
	if err := os.WriteFile(planted, []byte(expiredClaim), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(entryDir, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	// A RELATIVE, same-directory link: nothing about it escapes the entry, so only
	// the refusal to follow a non-regular manifest name keeps it from being read.
	if err := os.Symlink("manifest.planted.json", filepath.Join(entryDir, "manifest.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	purged, skipped, err := st.SweepManagedFileTrash(ctx, now)
	if err != nil {
		t.Fatalf("SweepManagedFileTrash: %v", err)
	}
	if skipped {
		t.Fatal("SweepManagedFileTrash unexpectedly skipped")
	}
	if purged != 0 {
		t.Fatalf("purged = %d, want 0 (an entry whose expiry cannot be proved is never purged)", purged)
	}
	if _, err := os.Stat(trash.TrashPath); err != nil {
		t.Fatalf("in-window entry destroyed on a planted expiry claim: %v", err)
	}
}

// A trash manifest whose name became a symlink is refused rather than read. The
// manifest names the path a restore moves the entry back to, so following a
// substituted one would let a planted record redirect the restore.
func TestRestoreManagedPathRefusesSymlinkedManifest(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllLibraries: true}
	original := filepath.Join(root, "clip.mkv")
	if err := os.WriteFile(original, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0).UTC()
	trash, err := st.DeleteManagedPath(ctx, "library-local", original, filter, now)
	if err != nil {
		t.Fatalf("DeleteManagedPath: %v", err)
	}
	entryDir := filepath.Dir(trash.TrashPath)
	manifest := filepath.Join(entryDir, "manifest.json")
	if err := os.Rename(manifest, filepath.Join(entryDir, "manifest.aside.json")); err != nil {
		t.Fatalf("move manifest aside: %v", err)
	}
	// Relative and inside the entry directory: a root-scoped read would happily
	// follow it, so the refusal has to come from rejecting a non-regular name.
	if err := os.Symlink("manifest.aside.json", manifest); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := st.RestoreManagedPath(ctx, "library-local", trash.TrashID, filter, now); err == nil {
		t.Fatal("RestoreManagedPath read a symlinked manifest, want refusal")
	}
	if _, err := os.Lstat(original); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused restore moved the entry back: %v", err)
	}
}

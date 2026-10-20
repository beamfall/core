package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOrganizeFallsBackWhenHardlinksAreUnsupported(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "incoming", "raw.mp4")
	importFileOnDisk(t, st, "library-local", source, "Atlas", "00000000aaaa1001")
	setManagedMutationCapabilities(t, st, MountCapabilities{
		Hardlinks:              CapabilityUnsupported,
		AtomicRename:           CapabilitySupported,
		CaseSensitive:          CapabilitySupported,
		PermissionPreservation: CapabilitySupported,
	})

	batch, err := st.OrganizeApply(ctx, "library-local", "{title}", LibraryFilter{AllLibraries: true}, time.Now().UTC())
	if err != nil {
		t.Fatalf("OrganizeApply fallback: %v", err)
	}
	target := filepath.Join(root, "Atlas.mp4")
	if batch.Affected != 1 {
		t.Fatalf("OrganizeApply affected = %d, want 1", batch.Affected)
	}
	assertMovedFile(t, source, target, "Atlas bytes")
}

func TestRenameFallsBackWhenAtomicRenameIsUnsupported(t *testing.T) {
	st, root := managedMutationFixture(t)
	source := filepath.Join(root, "before.txt")
	//nolint:gosec // test fixture: verifies the fallback preserves group-readable permissions.
	if err := os.WriteFile(source, []byte("rename"), 0o640); err != nil {
		t.Fatal(err)
	}
	setManagedMutationCapabilities(t, st, MountCapabilities{
		Hardlinks:              CapabilitySupported,
		AtomicRename:           CapabilityUnsupported,
		CaseSensitive:          CapabilitySupported,
		PermissionPreservation: CapabilityUnsupported,
	})

	target := filepath.Join(root, "after.txt")
	move, err := st.prepareManagedMove(source, target)
	if err != nil {
		t.Fatalf("prepare atomic-rename-unavailable fallback: %v", err)
	}
	if move.strategy != managedMoveHardlink {
		t.Fatalf("atomic-rename-unavailable strategy = %d, want hardlink fallback", move.strategy)
	}
	renamed, err := st.RenameManagedPath(
		context.Background(), "library-local", source, "after.txt", LibraryFilter{AllLibraries: true},
	)
	if err != nil {
		t.Fatalf("RenameManagedPath fallback: %v", err)
	}
	assertMovedFile(t, source, renamed.Path, "rename")
	info, err := os.Stat(renamed.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("fallback permissions = %o, want 640", info.Mode().Perm())
	}
}

func TestCaseOnlyRenameUsesTwoStepFallback(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "Feature.MP4")
	target := filepath.Join(root, "feature.mp4")
	if err := os.WriteFile(source, []byte("case-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := MountCapabilities{
		Mount:                  "case-insensitive-fixture",
		Hardlinks:              CapabilitySupported,
		AtomicRename:           CapabilitySupported,
		CaseSensitive:          CapabilityUnsupported,
		PermissionPreservation: CapabilitySupported,
	}
	move, err := prepareManagedMoveForCapabilities(source, target, info, capabilities, capabilities)
	if err != nil {
		t.Fatalf("prepare case-only fallback: %v", err)
	}
	if move.strategy != managedMoveAtomicRename || !move.caseOnly {
		t.Fatalf("case-only move = %#v, want two-step atomic fallback", move)
	}
	var renames [][2]string
	err = executeManagedMoveWithRename(move, source, target, func(oldPath, newPath string) error {
		renames = append(renames, [2]string{oldPath, newPath})
		return os.Rename(oldPath, newPath)
	})
	if err != nil {
		t.Fatalf("execute case-only fallback: %v", err)
	}
	if len(renames) != 2 {
		t.Fatalf("case-only rename operations = %d, want two-step fallback", len(renames))
	}
	temporary := renames[0][1]
	if renames[0][0] != source || renames[1] != [2]string{temporary, target} {
		t.Fatalf("case-only rename operations = %#v, want source -> temporary -> target", renames)
	}
	if !strings.HasPrefix(filepath.Base(temporary), ".beamfall-case-rename-") {
		t.Fatalf("case-only temporary path = %q, want Beamfall-owned temporary", temporary)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(target) {
		t.Fatalf("case-only directory entries = %v, want exact target casing", entries)
	}
	//nolint:gosec // test fixture: target is constructed beneath this test's isolated t.TempDir root.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "case-only" {
		t.Fatalf("case-only target content = %q, want preserved bytes", data)
	}
}

func TestCrossDeviceMoveUsesVerifiedCopyFallback(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source", "media.bin")
	target := filepath.Join(root, "target", "media.bin")
	if err := os.MkdirAll(filepath.Dir(source), 0o750); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // test fixture: verifies the fallback preserves group-readable permissions.
	if err := os.WriteFile(source, []byte("cross-device"), 0o640); err != nil {
		t.Fatal(err)
	}
	witness := filepath.Join(root, "source", "witness.bin")
	if err := os.Link(source, witness); err != nil {
		t.Fatalf("cross-device fixture requires a local hardlink witness: %v", err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	sourceCapabilities := MountCapabilities{
		Mount:                  "source-fixture",
		Hardlinks:              CapabilitySupported,
		AtomicRename:           CapabilitySupported,
		CaseSensitive:          CapabilitySupported,
		PermissionPreservation: CapabilitySupported,
	}
	targetCapabilities := sourceCapabilities
	targetCapabilities.Mount = "target-fixture"
	move, err := prepareManagedMoveForCapabilities(
		source, target, info, sourceCapabilities, targetCapabilities,
	)
	if err != nil {
		t.Fatalf("prepare cross-device fallback: %v", err)
	}
	if move.strategy != managedMoveVerifiedCopy {
		t.Fatalf("cross-device strategy = %d, want verified-copy fallback", move.strategy)
	}
	if err := executeManagedMove(move, source, target); err != nil {
		t.Fatalf("execute cross-device fallback: %v", err)
	}
	assertMovedFile(t, source, target, "cross-device")
	targetInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	witnessInfo, err := os.Stat(witness)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(targetInfo, witnessInfo) {
		t.Fatal("cross-device fallback preserved the source inode instead of copying bytes")
	}
	if targetInfo.Mode().Perm() != 0o640 {
		t.Fatalf("cross-device fallback permissions = %o, want 640", targetInfo.Mode().Perm())
	}
}

func TestRelocateRefusesWhenAtomicRenameIsUnsupported(t *testing.T) {
	st, root := managedMutationFixture(t)
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	for _, path := range []string{source, destination} {
		if err := os.Mkdir(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	setManagedMutationCapabilities(t, st, MountCapabilities{
		Hardlinks:              CapabilitySupported,
		AtomicRename:           CapabilityUnsupported,
		CaseSensitive:          CapabilitySupported,
		PermissionPreservation: CapabilitySupported,
	})

	_, err := st.MoveManagedPath(
		context.Background(), "library-local", source, destination, LibraryFilter{AllLibraries: true},
	)
	if err == nil {
		t.Fatal("MoveManagedPath unexpectedly relocated a folder without atomic rename")
	}
	if !strings.Contains(err.Error(), `mount "`) || !strings.Contains(err.Error(), `"atomic rename"`) {
		t.Fatalf("MoveManagedPath refusal = %q, want mount and missing capability", err)
	}
	if _, statErr := os.Stat(source); statErr != nil {
		t.Fatalf("refused relocate mutated source: %v", statErr)
	}
}

func TestDestructiveMutationFallsBackWhenAtomicRenameIsUnsupported(t *testing.T) {
	st, root := managedMutationFixture(t)
	source := filepath.Join(root, "delete.txt")
	if err := os.WriteFile(source, []byte("recoverable"), 0o600); err != nil {
		t.Fatal(err)
	}
	setManagedMutationCapabilities(t, st, MountCapabilities{
		Hardlinks:              CapabilitySupported,
		AtomicRename:           CapabilityUnsupported,
		CaseSensitive:          CapabilitySupported,
		PermissionPreservation: CapabilityUnsupported,
	})

	trash, err := st.DeleteManagedPath(
		context.Background(), "library-local", source, LibraryFilter{AllLibraries: true}, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("DeleteManagedPath fallback: %v", err)
	}
	assertMovedFile(t, source, trash.TrashPath, "recoverable")
}

func managedMutationFixture(t *testing.T) (*Store, string) {
	t.Helper()
	st := openSeeded(t)
	root := t.TempDir()
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	return st, root
}

func setManagedMutationCapabilities(t *testing.T, st *Store, capabilities MountCapabilities) {
	t.Helper()
	probe := newMountCapabilityProbe(filepath.Join(t.TempDir(), "mount-capabilities.json"))
	probe.run = func(string) (MountCapabilities, error) {
		return capabilities, nil
	}
	st.mountCapabilityProbe = probe
}

func assertMovedFile(t *testing.T, source, target, want string) {
	t.Helper()
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source %q still exists after move: %v", source, err)
	}
	//nolint:gosec // test fixture: target is constructed under the fixture's t.TempDir.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read moved target %q: %v", target, err)
	}
	if string(data) != want {
		t.Fatalf("moved target content = %q, want %q", data, want)
	}
}

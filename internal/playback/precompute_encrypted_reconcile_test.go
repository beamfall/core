package playback

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var reconcileTestHeader = []byte("CT1")

func reconcileTestIsCiphertext(r io.Reader) (bool, error) {
	head := make([]byte, len(reconcileTestHeader))
	if _, err := io.ReadFull(r, head); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	return bytes.Equal(head, reconcileTestHeader), nil
}

func writeReconcileFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func reconcileGeneration(n int) string {
	return fmt.Sprintf("%064x", n)
}

func TestReconcileEncryptedPrecomputeKeepsCiphertextAndRemovesEverythingElse(t *testing.T) {
	cacheRoot := t.TempDir()
	library := filepath.Join(cacheRoot, encryptedPrecomputeLibraryRel("library-local"))
	other := filepath.Join(cacheRoot, encryptedPrecomputeLibraryRel("library-other"))
	media := filepath.Join(library, fmt.Sprintf("%x", []byte("media-alpha")))
	ciphertext := append(append([]byte{}, reconcileTestHeader...), "sealed"...)

	kept := filepath.Join(media, reconcileGeneration(1))
	writeReconcileFile(t, filepath.Join(kept, masterPlaylistName), ciphertext)
	writeReconcileFile(t, filepath.Join(kept, "720p", "index.m3u8"), ciphertext)
	writeReconcileFile(t, filepath.Join(kept, "720p", "seg-00000.m4s"), ciphertext)

	plaintext := filepath.Join(media, reconcileGeneration(2))
	writeReconcileFile(t, filepath.Join(plaintext, masterPlaylistName), ciphertext)
	writeReconcileFile(t, filepath.Join(plaintext, "720p", "seg-00000.m4s"), []byte("plain segment"))

	empty := filepath.Join(media, reconcileGeneration(3))
	writeReconcileFile(t, filepath.Join(empty, masterPlaylistName), nil)

	linked := filepath.Join(media, reconcileGeneration(4))
	writeReconcileFile(t, filepath.Join(linked, masterPlaylistName), ciphertext)
	outsideTarget := filepath.Join(t.TempDir(), "outside")
	writeReconcileFile(t, outsideTarget, []byte("outside"))
	if err := os.Symlink(outsideTarget, filepath.Join(linked, "seg.m4s")); err != nil {
		t.Fatal(err)
	}

	misnamed := filepath.Join(media, "staging.tmp")
	writeReconcileFile(t, filepath.Join(misnamed, masterPlaylistName), ciphertext)
	strayMediaFile := filepath.Join(media, "stray.txt")
	writeReconcileFile(t, strayMediaFile, []byte("plain"))
	strayLibraryFile := filepath.Join(library, "Not-Hex")
	writeReconcileFile(t, strayLibraryFile, ciphertext)
	otherGeneration := filepath.Join(other, "6d", reconcileGeneration(5))
	writeReconcileFile(t, filepath.Join(otherGeneration, "seg.m4s"), []byte("plain, another library"))

	if err := ReconcileEncryptedPrecomputedRenditions(cacheRoot, "library-local", reconcileTestIsCiphertext); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if _, err := os.Stat(filepath.Join(kept, "720p", "seg-00000.m4s")); err != nil {
		t.Fatalf("ciphertext generation: %v, want kept", err)
	}
	for _, removed := range []string{plaintext, empty, linked, misnamed, strayMediaFile, strayLibraryFile} {
		if _, err := os.Lstat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: %v, want removed", strings.TrimPrefix(removed, cacheRoot), err)
		}
	}
	if _, err := os.Stat(outsideTarget); err != nil {
		t.Fatalf("link target outside the cache: %v, want untouched", err)
	}
	if _, err := os.Stat(otherGeneration); err != nil {
		t.Fatalf("another Library's tree: %v, want untouched", err)
	}
}

func TestReconcileEncryptedPrecomputeIsANoOpWithoutATree(t *testing.T) {
	if err := ReconcileEncryptedPrecomputedRenditions(t.TempDir(), "library-local", reconcileTestIsCiphertext); err != nil {
		t.Fatalf("reconcile without a tree: %v", err)
	}
	if err := ReconcileEncryptedPrecomputedRenditions("", "library-local", reconcileTestIsCiphertext); err != nil {
		t.Fatalf("reconcile without a cache root: %v", err)
	}
	if err := ReconcileEncryptedPrecomputedRenditions(t.TempDir(), "library-local", nil); err == nil {
		t.Fatal("reconcile without a ciphertext check succeeded, want an error")
	}
}

func TestReconcileEncryptedPrecomputeFailsClosedWhenInspectionFails(t *testing.T) {
	cacheRoot := t.TempDir()
	generation := filepath.Join(cacheRoot, encryptedPrecomputeLibraryRel("library-local"), "6d", reconcileGeneration(1))
	writeReconcileFile(t, filepath.Join(generation, "seg.m4s"), []byte("CT1 sealed"))
	failing := func(io.Reader) (bool, error) { return false, errors.New("read failed") }

	if err := ReconcileEncryptedPrecomputedRenditions(cacheRoot, "library-local", failing); err == nil {
		t.Fatal("reconcile succeeded although a member could not be inspected")
	}
}

// TestEncryptedPrecomputeRefusesASymlinkedTree pins that neither the rewrap
// purge nor the unseal reconcile follows a planted encrypted-precompute link
// out of the cache.
func TestEncryptedPrecomputeRefusesASymlinkedTree(t *testing.T) {
	cacheRoot := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, fmt.Sprintf("%x", []byte("library-local")), "keep.txt")
	writeReconcileFile(t, victim, []byte("outside the cache"))
	if err := os.Symlink(outside, filepath.Join(cacheRoot, encryptedPrecomputeDir)); err != nil {
		t.Fatal(err)
	}

	if err := PurgeEncryptedPrecomputedRenditions(cacheRoot, "library-local"); err == nil {
		t.Fatal("purge through a symlinked tree succeeded, want a refusal")
	}
	if err := ReconcileEncryptedPrecomputedRenditions(cacheRoot, "library-local", reconcileTestIsCiphertext); err == nil {
		t.Fatal("reconcile through a symlinked tree succeeded, want a refusal")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("file outside the cache: %v, want untouched", err)
	}
}

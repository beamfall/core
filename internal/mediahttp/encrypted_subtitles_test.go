package mediahttp

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// fakeCiphertextMagic stands in for the libencrypt object header in these
// tests; the app wires libencrypt.HasObjectHeader in its place.
var fakeCiphertextMagic = []byte("BFE2")

func fakeIsCiphertext(r io.Reader) (bool, error) {
	head := make([]byte, len(fakeCiphertextMagic))
	if _, err := io.ReadFull(r, head); err != nil {
		return false, nil
	}
	return bytes.Equal(head, fakeCiphertextMagic), nil
}

func writeCacheFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func TestEncryptedSubtitleLibraryRelHashesTheLibraryID(t *testing.T) {
	rel := encryptedSubtitleLibraryRel(store.LibraryID("../escape"))
	dir, base := filepath.Split(rel)
	if dir != encryptedSubtitleDirName+string(filepath.Separator) || len(base) != 32 {
		t.Fatalf("rel = %q, want encrypted/<32 hex>", rel)
	}
	if encryptedSubtitleLibraryRel("a") == encryptedSubtitleLibraryRel("b") {
		t.Fatal("distinct libraries share a cache directory")
	}
}

func TestValidEncryptedSubtitleName(t *testing.T) {
	for name, want := range map[string]bool{
		"sha256_ab.0.vtt":      true,
		"sha256_ab.12.vtt":     true,
		".libenc-1.tmp":        false,
		".sha256_ab.0.vtt":     false,
		"sha256_ab.vtt":        false,
		"sha256_ab.-1.vtt":     false,
		"sha256_ab.01.vtt":     false,
		"sha256_ab.0.vtt.tmp":  false,
		"sha256_ab.x.vtt":      false,
		".0.vtt":               false,
		"sha256_ab.0.vtt.vtt2": false,
	} {
		if got := validEncryptedSubtitleName(name); got != want {
			t.Errorf("validEncryptedSubtitleName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestReconcileEncryptedSubtitlesKeepsCiphertextAndRemovesEverythingElse(t *testing.T) {
	cache := t.TempDir()
	handler := &Handler{subtitleDir: cache}
	lib := store.LibraryID("lib-enc")
	other := store.LibraryID("lib-other")
	libDir := filepath.Join(cache, encryptedSubtitleLibraryRel(lib))
	otherDir := filepath.Join(cache, encryptedSubtitleLibraryRel(other))
	ciphertext := append(append([]byte{}, fakeCiphertextMagic...), 1, 2, 3)

	writeCacheFile(t, filepath.Join(libDir, "sha256_ab.0.vtt"), ciphertext)
	writeCacheFile(t, filepath.Join(libDir, "sha256_ab.1.vtt"), []byte("WEBVTT\n\nplain cue\n"))
	writeCacheFile(t, filepath.Join(libDir, ".libenc-123.tmp"), ciphertext)
	writeCacheFile(t, filepath.Join(libDir, "notes.txt"), ciphertext)
	writeCacheFile(t, filepath.Join(libDir, "nested", "sha256_ab.2.vtt"), ciphertext)
	if err := os.Symlink(filepath.Join(libDir, "sha256_ab.0.vtt"), filepath.Join(libDir, "sha256_ab.3.vtt")); err != nil {
		t.Fatal(err)
	}
	writeCacheFile(t, filepath.Join(otherDir, "sha256_cd.0.vtt"), []byte("WEBVTT\n"))
	writeCacheFile(t, filepath.Join(cache, "sha256_ef.0.vtt"), []byte("WEBVTT\n"))

	if err := handler.ReconcileEncryptedSubtitles(lib, fakeIsCiphertext); err != nil {
		t.Fatalf("ReconcileEncryptedSubtitles: %v", err)
	}
	if got := dirNames(t, libDir); len(got) != 1 || got[0] != "sha256_ab.0.vtt" {
		t.Fatalf("library dir after reconcile = %v, want only the ciphertext object", got)
	}
	if got := dirNames(t, otherDir); len(got) != 1 {
		t.Fatalf("another library's cache was touched: %v", got)
	}
	if _, err := os.Stat(filepath.Join(cache, "sha256_ef.0.vtt")); err != nil {
		t.Fatalf("public sidecar was touched: %v", err)
	}
}

func TestReconcileEncryptedSubtitlesToleratesAMissingCache(t *testing.T) {
	if err := (&Handler{}).ReconcileEncryptedSubtitles("lib", fakeIsCiphertext); err != nil {
		t.Fatalf("no cache configured: %v", err)
	}
	handler := &Handler{subtitleDir: filepath.Join(t.TempDir(), "absent")}
	if err := handler.ReconcileEncryptedSubtitles("lib", fakeIsCiphertext); err != nil {
		t.Fatalf("absent cache: %v", err)
	}
	handler = &Handler{subtitleDir: t.TempDir()}
	if err := handler.ReconcileEncryptedSubtitles("lib", fakeIsCiphertext); err != nil {
		t.Fatalf("absent library dir: %v", err)
	}
	if err := handler.ReconcileEncryptedSubtitles("", fakeIsCiphertext); err == nil {
		t.Fatal("empty library ID accepted")
	}
	if err := handler.ReconcileEncryptedSubtitles("lib", nil); err == nil {
		t.Fatal("nil ciphertext check accepted")
	}
}

func TestPurgeEncryptedDerivativesRemovesOnlyThatLibrary(t *testing.T) {
	cache := t.TempDir()
	handler := &Handler{subtitleDir: cache}
	lib := store.LibraryID("lib-enc")
	other := store.LibraryID("lib-other")
	writeCacheFile(t, filepath.Join(cache, encryptedSubtitleLibraryRel(lib), "sha256_ab.0.vtt"), fakeCiphertextMagic)
	writeCacheFile(t, filepath.Join(cache, encryptedSubtitleLibraryRel(other), "sha256_cd.0.vtt"), fakeCiphertextMagic)
	writeCacheFile(t, filepath.Join(cache, "sha256_ef.0.vtt"), []byte("WEBVTT\n"))

	if err := handler.PurgeEncryptedDerivatives(context.Background(), lib); err != nil {
		t.Fatalf("PurgeEncryptedDerivatives: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, encryptedSubtitleLibraryRel(lib))); !os.IsNotExist(err) {
		t.Fatalf("library cache survived the purge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, encryptedSubtitleLibraryRel(other), "sha256_cd.0.vtt")); err != nil {
		t.Fatalf("another library's cache was purged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "sha256_ef.0.vtt")); err != nil {
		t.Fatalf("public sidecar was purged: %v", err)
	}
	if err := handler.PurgeEncryptedDerivatives(context.Background(), lib); err != nil {
		t.Fatalf("second purge: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := handler.PurgeEncryptedDerivatives(ctx, lib); err == nil {
		t.Fatal("cancelled purge reported success")
	}
	if err := (&Handler{}).PurgeEncryptedDerivatives(context.Background(), lib); err != nil {
		t.Fatalf("no cache configured: %v", err)
	}
}

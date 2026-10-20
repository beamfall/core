package qbittorrent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// TestValidateRoutingRejectsUnknownCategory: a routed category that does not
// exist in qBittorrent is a config error (a typo would silently never route).
func TestValidateRoutingRejectsUnknownCategory(t *testing.T) {
	src := &fakeSource{categories: []string{"films"}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-a": {t.TempDir()}}}
	ing, _ := newTestIngestor(t, src, cat, &fakeScan{}, map[string]store.LibraryID{"typo": "lib-a"})

	err := ing.ValidateRouting(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected unknown-category error, got %v", err)
	}
}

// TestValidateRoutingAcceptsKnownCategoryNoOverlap: a mapped, existing category
// whose download dirs lie outside Library roots validates clean.
func TestValidateRoutingAcceptsKnownCategoryNoOverlap(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	downloads := filepath.Join(tmp, "downloads")
	src := &fakeSource{categories: []string{"films"}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-a": {libRoot}}}
	ing, _ := newTestIngestor(t, src, cat, &fakeScan{}, map[string]store.LibraryID{"films": "lib-a"})

	if err := ing.ValidateRouting(context.Background(), []string{downloads}); err != nil {
		t.Fatalf("expected clean validation, got %v", err)
	}
}

// TestValidateRoutingRejectsDownloadDirInsideLibraryRoot: a download dir that
// overlaps a routed Library root is rejected — otherwise the scanner would see
// the same fingerprint at two paths, a Reveal-contract hazard.
func TestValidateRoutingRejectsDownloadDirInsideLibraryRoot(t *testing.T) {
	tmp := t.TempDir()
	libRoot := filepath.Join(tmp, "library")
	insideRoot := filepath.Join(libRoot, "downloads") // overlaps the Library root
	src := &fakeSource{categories: []string{"films"}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-a": {libRoot}}}
	ing, _ := newTestIngestor(t, src, cat, &fakeScan{}, map[string]store.LibraryID{"films": "lib-a"})

	err := ing.ValidateRouting(context.Background(), []string{insideRoot})
	if err == nil || !strings.Contains(err.Error(), "overlaps Library") {
		t.Fatalf("expected overlap error, got %v", err)
	}
}

// TestValidateRoutingRejectsLibraryRootInsideDownloadDir: the inverse overlap
// (a Library root nested under a download dir) is equally disqualifying.
func TestValidateRoutingRejectsLibraryRootInsideDownloadDir(t *testing.T) {
	tmp := t.TempDir()
	download := filepath.Join(tmp, "downloads")
	libRoot := filepath.Join(download, "library") // root sits inside the download dir
	src := &fakeSource{categories: []string{"films"}}
	cat := &fakeCatalog{roots: map[store.LibraryID][]string{"lib-a": {libRoot}}}
	ing, _ := newTestIngestor(t, src, cat, &fakeScan{}, map[string]store.LibraryID{"films": "lib-a"})

	err := ing.ValidateRouting(context.Background(), []string{download})
	if err == nil || !strings.Contains(err.Error(), "overlaps Library") {
		t.Fatalf("expected overlap error, got %v", err)
	}
}

// TestEscapesRoot locks the member-path traversal guard: cleaned
// relative paths that stay inside the join root are safe; absolute paths and
// anything reaching upward through ".." are rejected before they can become a
// relocation target.
func TestEscapesRoot(t *testing.T) {
	for _, p := range []string{
		"ep1.mkv",
		filepath.Join("Season 1", "ep1.mkv"),
		filepath.Clean("./ep1.mkv"),
		filepath.Clean("a/b/../c.mkv"), // -> a/c.mkv, still inside
		".",
	} {
		if escapesRoot(p) {
			t.Errorf("escapesRoot(%q) = true, want false (stays inside root)", p)
		}
	}
	escaping := []string{
		"..",
		filepath.Join("..", "evil.mkv"),
		filepath.Join("..", "..", "etc", "cron.d", "x.mkv"),
		filepath.Clean("a/../../evil.mkv"), // -> ../evil.mkv
	}
	if filepath.Separator == '/' {
		escaping = append(escaping, "/etc/passwd") // absolute on unix
	}
	for _, p := range escaping {
		if !escapesRoot(p) {
			t.Errorf("escapesRoot(%q) = false, want true (escapes root)", p)
		}
	}
}

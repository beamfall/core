package store_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// importStageHarness builds a Store with one library root and returns the store,
// the canonical root, and an all-libraries filter.
func importStageHarness(t *testing.T) (*store.Store, string, store.LibraryFilter) {
	t.Helper()
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
	return st, canonicalRoot, store.LibraryFilter{AllLibraries: true}
}

// writeOutOfLibrarySource writes a regular file under a fresh temp dir that is
// NOT a library root (the out-of-Library drag-in case).
func writeOutOfLibrarySource(t *testing.T, name, payload string) string {
	t.Helper()
	dir := t.TempDir()
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(canonical, name)
	if err := os.WriteFile(src, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// TestImportManagedFileImportsThenConverts is the core acceptance: an
// out-of-Library file, registered server-side and imported via its minted token,
// lands under a Library root with contents preserved AND is cataloged so the
// EXISTING convert primitive (ConvertManagedFile/ResolveManagedMediaFile)
// accepts it — the import-then-convert path (apple ADR-0003 §1).
func TestImportManagedFileImportsThenConverts(t *testing.T) {
	ctx := context.Background()
	st, root, filter := importStageHarness(t)
	src := writeOutOfLibrarySource(t, "drag-in.mkv", "payload-bytes")

	token, err := st.RegisterImportSource(src)
	if err != nil {
		t.Fatalf("RegisterImportSource: %v", err)
	}
	imported, err := st.ImportManagedFile(ctx, "library-local", token, root, "mkv", "h264", store.MediaClassVideo, filter)
	if err != nil {
		t.Fatalf("ImportManagedFile: %v", err)
	}

	want := filepath.Join(root, "drag-in.mkv")
	if imported.Path != want {
		t.Fatalf("imported path = %q, want %q", imported.Path, want)
	}
	if data, err := os.ReadFile(imported.Path); err != nil || string(data) != "payload-bytes" {
		t.Fatalf("imported contents = %q (err %v), want payload-bytes", data, err)
	}

	// Now-managed: the existing convert primitive accepts the imported file.
	conv := &fakeConverter{container: "mp4"}
	now := time.Unix(1700000000, 0)
	result, err := st.ConvertManagedFile(ctx, "library-local", imported.Path, "h264-mp4", conv, filter, claimConvertLease(t, st), now)
	if err != nil {
		t.Fatalf("ConvertManagedFile on imported file: %v", err)
	}
	if result.Converted.ID == "" {
		t.Fatal("convert of imported file produced no media")
	}
}

// TestImportManagedFileRefusesForgedToken proves the server-side token-provenance
// gate: a token the store never minted is refused with "unknown import token"
// (ADR-0003 §1 "the server rejects any token it did not vend").
func TestImportManagedFileRefusesForgedToken(t *testing.T) {
	ctx := context.Background()
	st, root, filter := importStageHarness(t)
	_, err := st.ImportManagedFile(ctx, "library-local", "forged-token-never-minted", root, "mkv", "h264", store.MediaClassVideo, filter)
	if err == nil || !containsMsg(err, "unknown import token") {
		t.Fatalf("forged token error = %v, want unknown import token", err)
	}
}

// TestImportManagedFileRefusesAfterInvalidate proves a previously valid token is
// refused after InvalidateImportTokens (Admin Session end — no cross-session
// replay).
func TestImportManagedFileRefusesAfterInvalidate(t *testing.T) {
	ctx := context.Background()
	st, root, filter := importStageHarness(t)
	src := writeOutOfLibrarySource(t, "drag-in.mkv", "x")
	token, err := st.RegisterImportSource(src)
	if err != nil {
		t.Fatal(err)
	}
	st.InvalidateImportTokens()
	_, err = st.ImportManagedFile(ctx, "library-local", token, root, "mkv", "h264", store.MediaClassVideo, filter)
	if err == nil || !containsMsg(err, "unknown import token") {
		t.Fatalf("post-invalidate error = %v, want unknown import token", err)
	}
}

// TestImportManagedFileRefusesEscapeDestParent proves a `..`-escape destination
// parent is refused (the dest never escapes the Library root).
func TestImportManagedFileRefusesEscapeDestParent(t *testing.T) {
	ctx := context.Background()
	st, root, filter := importStageHarness(t)
	src := writeOutOfLibrarySource(t, "drag-in.mkv", "x")
	token, err := st.RegisterImportSource(src)
	if err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(root, "..", "outside")
	_, err = st.ImportManagedFile(ctx, "library-local", token, escape, "mkv", "h264", store.MediaClassVideo, filter)
	if err == nil {
		t.Fatal("expected `..`-escape dest parent to be refused")
	}
}

// TestImportManagedFileRefusesNonRegularSource proves a non-regular source (a
// directory) is refused after the fstat regular-file check.
func TestImportManagedFileRefusesNonRegularSource(t *testing.T) {
	ctx := context.Background()
	st, root, filter := importStageHarness(t)
	dir := t.TempDir()
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	token, err := st.RegisterImportSource(canonical)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.ImportManagedFile(ctx, "library-local", token, root, "mkv", "h264", store.MediaClassVideo, filter)
	if err == nil || !containsMsg(err, "not a regular file") {
		t.Fatalf("non-regular source error = %v, want not a regular file", err)
	}
}

// TestImportManagedFileRefusesSymlinkFinalComponent documents and proves the
// SECURE, ADR-0003-faithful default: a source whose FINAL component is a symlink
// is REJECTED. The O_NOFOLLOW open of the EvalSymlinks-resolved path is what
// enforces it; EvalSymlinks is used only to canonicalize INTERMEDIATE components
// for containment. (This can be loosened to "follow unless it escapes" later.)
func TestImportManagedFileRefusesSymlinkFinalComponent(t *testing.T) {
	ctx := context.Background()
	st, root, filter := importStageHarness(t)
	target := writeOutOfLibrarySource(t, "real.mkv", "payload")
	linkDir := t.TempDir()
	canonicalLinkDir, err := filepath.EvalSymlinks(linkDir)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(canonicalLinkDir, "alias.mkv")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	token, err := st.RegisterImportSource(link)
	if err != nil {
		t.Fatal(err)
	}
	// EvalSymlinks resolves `link` to `target` for canonicalization, but the
	// containment check uses the resolved physical path and the open is
	// O_NOFOLLOW — a symlinked source is not silently followed into the library.
	// The registered locator's final component IS a symlink: it must be refused.
	if _, err := st.ImportManagedFile(ctx, "library-local", token, root, "mkv", "h264", store.MediaClassVideo, filter); err == nil {
		// Defensive: if the platform resolved the symlink before O_NOFOLLOW saw
		// it, the import still must not have created an out-of-policy file.
		t.Fatal("expected symlink-final-component source to be refused (secure default)")
	}
}

// TestImportManagedFileRefusesSourceInDestLibrary proves a source already under
// the destination library's root is refused (it is already managed; rename/move
// instead).
func TestImportManagedFileRefusesSourceInDestLibrary(t *testing.T) {
	ctx := context.Background()
	st, root, filter := importStageHarness(t)
	insrc := filepath.Join(root, "already.mkv")
	if err := os.WriteFile(insrc, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	token, err := st.RegisterImportSource(insrc)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.ImportManagedFile(ctx, "library-local", token, root, "mkv", "h264", store.MediaClassVideo, filter)
	if err == nil || !containsMsg(err, "already in the destination library") {
		t.Fatalf("in-library source error = %v, want already in the destination library", err)
	}
}

// TestImportManagedFileRefusesDestCollision proves no-clobber: when the target
// name already exists, the import is refused and the pre-existing target's
// contents are NOT touched.
func TestImportManagedFileRefusesDestCollision(t *testing.T) {
	ctx := context.Background()
	st, root, filter := importStageHarness(t)
	existing := filepath.Join(root, "drag-in.mkv")
	if err := os.WriteFile(existing, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := writeOutOfLibrarySource(t, "drag-in.mkv", "NEW")
	token, err := st.RegisterImportSource(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportManagedFile(ctx, "library-local", token, root, "mkv", "h264", store.MediaClassVideo, filter); err == nil {
		t.Fatal("expected dest collision to be refused")
	}
	if data, err := os.ReadFile(existing); err != nil || string(data) != "ORIGINAL" {
		t.Fatalf("pre-existing target clobbered: %q (err %v)", data, err)
	}
}

// TestImportManagedFileCarriesClass proves class is carried through (an audio
// import is cataloged as audio, not silently defaulted to video).
func TestImportManagedFileCarriesClass(t *testing.T) {
	ctx := context.Background()
	st, root, filter := importStageHarness(t)
	src := writeOutOfLibrarySource(t, "track.flac", "audio-bytes")
	token, err := st.RegisterImportSource(src)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := st.ImportManagedFile(ctx, "library-local", token, root, "flac", "flac", store.MediaClassAudio, filter)
	if err != nil {
		t.Fatalf("ImportManagedFile (audio): %v", err)
	}
	_, _, mediaID, err := st.ResolveManagedMediaFile(ctx, "library-local", imported.Path, filter)
	if err != nil {
		t.Fatalf("ResolveManagedMediaFile: %v", err)
	}
	class, err := st.MediaClass(ctx, mediaID)
	if err != nil {
		t.Fatalf("MediaClass: %v", err)
	}
	if class != store.MediaClassAudio {
		t.Fatalf("imported class = %q, want audio (not defaulted to video)", class)
	}
}

func containsMsg(err error, msg string) bool {
	return err != nil && strings.Contains(err.Error(), msg)
}

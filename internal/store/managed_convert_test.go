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

// fakeConverter stands in for the core FFmpeg converter: it writes a small
// payload to dst (simulating a transcode) and reports the container, so the
// store acceptance does not depend on an FFmpeg binary.
type fakeConverter struct {
	container string
	calls     int
	fail      bool
}

func (f *fakeConverter) ConvertFile(_ context.Context, src, dst, _ string) (string, string, store.MediaClass, error) {
	f.calls++
	if f.fail {
		return "", "", "", os.ErrInvalid
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", "", "", err
	}
	if err := os.WriteFile(dst, append([]byte("converted:"), data...), 0o644); err != nil {
		return "", "", "", err
	}
	return f.container, "h264", store.MediaClassVideo, nil
}

// TestConvertManagedFileWritesNewPreservesOriginal is the convert acceptance:
// a conversion writes a NEW managed file (admitted/indexed), the ORIGINAL is
// preserved in trash (recoverable, invariant 6), and the original is gone from
// its live path.
func TestConvertManagedFileWritesNewPreservesOriginal(t *testing.T) {
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
	importManagedFile(t, st, "library-local", src, "clip", "oshash-clip")

	filter := store.LibraryFilter{AllLibraries: true}
	conv := &fakeConverter{container: "mp4"}
	now := time.Unix(1700000000, 0)

	result, err := st.ConvertManagedFile(ctx, "library-local", src, "h264-mp4", conv, filter, claimConvertLease(t, st), now)
	if err != nil {
		t.Fatalf("ConvertManagedFile: %v", err)
	}
	if conv.calls != 1 {
		t.Fatalf("converter called %d times, want 1", conv.calls)
	}

	// New managed file admitted with the profile-suffixed name + new extension.
	wantDst := filepath.Join(canonicalRoot, "clip-h264-mp4.mp4")
	if result.Converted.ID == "" {
		t.Fatal("converted media has no ID")
	}
	if _, statErr := os.Stat(wantDst); statErr != nil {
		t.Fatalf("converted file not on disk: %v", statErr)
	}

	// Original preserved in trash, gone from its live path, and restorable.
	if result.Original.TrashID == "" || result.Original.OriginalPath != src {
		t.Fatalf("trash record = %#v", result.Original)
	}
	if _, statErr := os.Stat(src); !os.IsNotExist(statErr) {
		t.Fatalf("original still at live path: %v", statErr)
	}
	if _, statErr := os.Stat(result.Original.TrashPath); statErr != nil {
		t.Fatalf("trashed original missing from trash: %v", statErr)
	}
	if _, err := st.RestoreManagedPath(ctx, "library-local", result.Original.TrashID, filter, now); err != nil {
		t.Fatalf("RestoreManagedPath: %v", err)
	}
	if _, statErr := os.Stat(src); statErr != nil {
		t.Fatalf("restore did not return the original: %v", statErr)
	}
}

// TestConvertManagedFileRefusesEncryptedLibrary is the ENCPRIV-10b fail-closed
// acceptance: a managed conversion would stage a plaintext transcode and admit
// it into the Library root as a new plaintext managed file. For an Encrypted
// Library (no encrypted-plane write path yet) the conversion is refused before
// the converter runs — nothing is staged, admitted, or trashed.
func TestConvertManagedFileRefusesEncryptedLibrary(t *testing.T) {
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
	importManagedFile(t, st, "library-local", src, "clip", "oshash-clip")
	// Mark the Library Encrypted after the source is cataloged.
	if err := st.SetLibraryBacking(ctx, "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking encrypted: %v", err)
	}

	filter := store.LibraryFilter{AllLibraries: true}
	conv := &fakeConverter{container: "mp4"}
	now := time.Unix(1700000000, 0)

	_, err = st.ConvertManagedFile(ctx, "library-local", src, "h264-mp4", conv, filter, claimConvertLease(t, st), now)
	if err == nil {
		t.Fatal("ConvertManagedFile on an encrypted Library succeeded; want refusal")
	}
	if conv.calls != 0 {
		t.Fatalf("converter called %d times for an encrypted Library; want 0", conv.calls)
	}
	if _, statErr := os.Stat(filepath.Join(canonicalRoot, "clip-h264-mp4.mp4")); !os.IsNotExist(statErr) {
		t.Fatalf("encrypted-Library conversion left a plaintext output on disk: %v", statErr)
	}
	// The original is untouched — never trashed.
	if _, statErr := os.Stat(src); statErr != nil {
		t.Fatalf("original disturbed by a refused encrypted conversion: %v", statErr)
	}
}

// TestConvertManagedFileRejectedWithoutAuthority is the authority acceptance: a
// profile whose LibraryFilter does not grant visibility to the library is
// rejected before any conversion runs.
func TestConvertManagedFileRejectedWithoutAuthority(t *testing.T) {
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
	importManagedFile(t, st, "library-local", src, "clip", "oshash-clip")

	// Empty filter = no library visibility (fails closed).
	noAuthority := store.LibraryFilter{}
	conv := &fakeConverter{container: "mp4"}

	_, err = st.ConvertManagedFile(ctx, "library-local", src, "h264-mp4", conv, noAuthority, claimConvertLease(t, st), time.Unix(1700000000, 0))
	if err == nil {
		t.Fatal("conversion without authority succeeded; want rejection")
	}
	if conv.calls != 0 {
		t.Fatalf("converter ran %d times despite no authority; want 0", conv.calls)
	}
	// Original untouched.
	if _, statErr := os.Stat(src); statErr != nil {
		t.Fatalf("original disturbed by rejected conversion: %v", statErr)
	}
}

// TestConvertManagedFileRejectsNonMedia confirms a managed path that is not
// cataloged media is rejected (conversion operates only on library media).
func TestConvertManagedFileRejectsNonMedia(t *testing.T) {
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
	// A real file under the root but never imported into the catalog.
	loose := filepath.Join(canonicalRoot, "loose.bin")
	if err := os.WriteFile(loose, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllLibraries: true}
	conv := &fakeConverter{container: "mp4"}

	_, err = st.ConvertManagedFile(ctx, "library-local", loose, "h264-mp4", conv, filter, claimConvertLease(t, st), time.Unix(1700000000, 0))
	if err == nil || !strings.Contains(err.Error(), "not cataloged media") {
		t.Fatalf("convert of non-media = %v; want 'not cataloged media' rejection", err)
	}
	if conv.calls != 0 {
		t.Fatalf("converter ran on non-media; want 0 calls, got %d", conv.calls)
	}
}

// TestConvertManagedFileConverterFailureLeavesOriginal confirms a failing
// FFmpeg run aborts the conversion without admitting or trashing anything.
func TestConvertManagedFileConverterFailureLeavesOriginal(t *testing.T) {
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
	importManagedFile(t, st, "library-local", src, "clip", "oshash-clip")
	filter := store.LibraryFilter{AllLibraries: true}
	conv := &fakeConverter{container: "mp4", fail: true}

	_, err = st.ConvertManagedFile(ctx, "library-local", src, "h264-mp4", conv, filter, claimConvertLease(t, st), time.Unix(1700000000, 0))
	if err == nil {
		t.Fatal("converter failure did not fail the conversion")
	}
	if _, statErr := os.Stat(src); statErr != nil {
		t.Fatalf("original lost after converter failure: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(canonicalRoot, "clip-h264-mp4.mp4")); !os.IsNotExist(statErr) {
		t.Fatal("a converted file was admitted despite converter failure")
	}
}

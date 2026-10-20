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

// recordingConverter records the destination path the store hands the engine,
// so a test can assert WHERE the engine is told to write (ADR-0124 §6: outside
// any Library root until admission). It still produces a valid output so the
// admission + trash steps complete.
type recordingConverter struct {
	dst string
}

func (c *recordingConverter) ConvertFile(_ context.Context, src, dst, _ string) (string, string, store.MediaClass, error) {
	c.dst = dst
	data, err := os.ReadFile(src)
	if err != nil {
		return "", "", "", err
	}
	if err := os.WriteFile(dst, append([]byte("converted:"), data...), 0o644); err != nil {
		return "", "", "", err
	}
	return "mp4", "h264", store.MediaClassVideo, nil
}

// TestConvertManagedFileStagesEngineOutputOutsideLibraryRoot asserts criterion
// (a) at the store level: the FFmpeg engine is directed to a staging path that
// is NOT inside the Library root, closing the write→admission TOCTOU. The
// admitted file still lands inside the root after admission.
func TestConvertManagedFileStagesEngineOutputOutsideLibraryRoot(t *testing.T) {
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
	importManagedFile(t, st, "library-local", src, "clip", "oshash-staging")

	conv := &recordingConverter{}
	if _, err := st.ConvertManagedFile(ctx, "library-local", src,
		"h264-mp4", conv, store.LibraryFilter{AllLibraries: true}, claimConvertLease(t, st), time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("ConvertManagedFile: %v", err)
	}

	if conv.dst == "" {
		t.Fatal("converter was never called with a destination")
	}
	stagedDir, err := filepath.EvalSymlinks(filepath.Dir(conv.dst))
	if err != nil {
		t.Fatalf("resolve staged dir: %v", err)
	}
	// The engine's write target must be OUTSIDE the Library root.
	if stagedDir == canonicalRoot || strings.HasPrefix(stagedDir+string(filepath.Separator), canonicalRoot+string(filepath.Separator)) {
		t.Errorf("engine staged inside the Library root: staged=%q root=%q", stagedDir, canonicalRoot)
	}
	// It must be the server-owned convert-staging area.
	if filepath.Base(stagedDir) != "convert-staging" {
		t.Errorf("engine staged outside convert-staging: %q", stagedDir)
	}
	// After admission the converted file is inside the root, and the staging
	// artifact is gone.
	wantDst := filepath.Join(canonicalRoot, "clip-h264-mp4.mp4")
	if _, statErr := os.Stat(wantDst); statErr != nil {
		t.Fatalf("admitted file not under Library root: %v", statErr)
	}
	if _, statErr := os.Stat(conv.dst); statErr == nil {
		t.Errorf("staging artifact %q survived admission", conv.dst)
	}
}

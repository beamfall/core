package store_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func mediaUploadFixture(t *testing.T) (*store.Store, string, store.LibraryFilter) {
	t.Helper()
	st := storetest.Open(t)
	root := t.TempDir()
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return st, canonical, store.LibraryFilter{AllLibraries: true}
}

func stageUpload(t *testing.T, st *store.Store, name, rel string, data []byte, filter store.LibraryFilter, now time.Time) store.MediaUpload {
	t.Helper()
	ctx := context.Background()
	upload, err := st.CreateMediaUpload(ctx, "library-local", name, rel, int64(len(data)), filter, now)
	if err != nil {
		t.Fatalf("CreateMediaUpload: %v", err)
	}
	if upload, err = st.AppendMediaUploadChunk(ctx, upload.ID, 0, bytes.NewReader(data), filter, now); err != nil {
		t.Fatalf("AppendMediaUploadChunk: %v", err)
	}
	if !upload.Complete() {
		t.Fatalf("upload incomplete: %+v", upload)
	}
	return upload
}

// failingReader yields its bytes and then an error, like a dropped connection.
type failingReader struct{ data []byte }

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, errors.New("connection reset")
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestMediaUploadResumesAtStagedOffset(t *testing.T) {
	ctx := context.Background()
	st, root, filter := mediaUploadFixture(t)
	now := time.Now()
	data := []byte("0123456789abcdef")
	upload, err := st.CreateMediaUpload(ctx, "library-local", "clip.mkv", "Show/Season 1", int64(len(data)), filter, now)
	if err != nil {
		t.Fatalf("CreateMediaUpload: %v", err)
	}
	if upload.RelativeDir != "Show/Season 1" {
		t.Fatalf("relative dir = %q", upload.RelativeDir)
	}
	if _, err := st.AppendMediaUploadChunk(ctx, upload.ID, 0, &failingReader{data: data[:6]}, filter, now); err == nil {
		t.Fatal("interrupted chunk reported success")
	}
	status, err := st.MediaUploadStatus(ctx, upload.ID, filter, now)
	if err != nil || status.Offset != 6 {
		t.Fatalf("status = %+v, %v; want offset 6", status, err)
	}
	if _, err := st.AppendMediaUploadChunk(ctx, upload.ID, 0, bytes.NewReader(data), filter, now); !errors.Is(err, store.ErrMediaUploadOffset) {
		t.Fatalf("chunk at wrong offset: %v", err)
	}
	if _, err := st.AppendMediaUploadChunk(ctx, upload.ID, 6, bytes.NewReader(append(data[6:], 'x')), filter, now); err == nil {
		t.Fatal("chunk passing the declared size was accepted")
	}
	status, _ = st.MediaUploadStatus(ctx, upload.ID, filter, now)
	if status.Offset != 6 {
		t.Fatalf("oversize chunk left offset %d", status.Offset)
	}
	if status, err = st.AppendMediaUploadChunk(ctx, upload.ID, 6, bytes.NewReader(data[6:]), filter, now); err != nil || !status.Complete() {
		t.Fatalf("final chunk: %+v, %v", status, err)
	}
	dest := filepath.Join(root, "Show", "Season 1")
	commit, err := st.CommitMediaUpload(ctx, upload.ID, dest, store.MediaUploadKeepBoth, filter, now)
	if err != nil {
		t.Fatalf("CommitMediaUpload: %v", err)
	}
	if commit.Path != filepath.Join(dest, "clip.mkv") || commit.Root != root {
		t.Fatalf("commit = %+v", commit)
	}
	if got, err := os.ReadFile(commit.Path); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("committed bytes = %q, %v", got, err)
	}
	if _, err := st.MediaUploadStatus(ctx, upload.ID, filter, now); !errors.Is(err, store.ErrMediaUploadNotFound) {
		t.Fatalf("committed session still open: %v", err)
	}
}

func TestMediaUploadRefusesBadSessions(t *testing.T) {
	ctx := context.Background()
	st, _, filter := mediaUploadFixture(t)
	now := time.Now()
	cases := []struct {
		name, file, rel string
		size            int64
	}{
		{"traversal name", "../x.mkv", "", 1},
		{"traversal folder", "x.mkv", "a/../../b", 1},
		{"trash folder", "x.mkv", ".beamfall-trash", 1},
		{"empty", "x.mkv", "", 0},
		{"too large", "x.mkv", "", store.MaxMediaUploadBytes + 1},
	}
	for _, tc := range cases {
		if _, err := st.CreateMediaUpload(ctx, "library-local", tc.file, tc.rel, tc.size, filter, now); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
	if _, err := st.CreateMediaUpload(ctx, "library-local", "x.mkv", "", 1, store.LibraryFilter{}, now); err == nil {
		t.Error("invisible library accepted")
	}
	upload, err := st.CreateMediaUpload(ctx, "library-local", "x.mkv", "", 1, filter, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.MediaUploadStatus(ctx, upload.ID, filter, now.Add(store.MediaUploadTTL+time.Hour)); !errors.Is(err, store.ErrMediaUploadNotFound) {
		t.Fatalf("expired session: %v", err)
	}
}

func TestMediaUploadCommitConflictsAndBounds(t *testing.T) {
	ctx := context.Background()
	st, root, filter := mediaUploadFixture(t)
	now := time.Now()
	existing := filepath.Join(root, "Film (2001)", "film.mkv")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	outside := stageUpload(t, st, "film.mkv", "", []byte("new"), filter, now)
	for _, dest := range []string{t.TempDir(), filepath.Join(root, ".beamfall-trash", "x"), "relative/dir"} {
		if _, err := st.CommitMediaUpload(ctx, outside.ID, dest, store.MediaUploadKeepBoth, filter, now); err == nil {
			t.Errorf("commit to %q accepted", dest)
		}
	}
	if status, err := st.MediaUploadStatus(ctx, outside.ID, filter, now); err != nil || !status.Complete() {
		t.Fatalf("refused commit lost the staged upload: %+v, %v", status, err)
	}
	if _, err := st.CommitMediaUpload(ctx, outside.ID, filepath.Dir(existing), store.MediaUploadFail, filter, now); err == nil {
		t.Fatal("fail-on-conflict commit overwrote")
	}
	kept, err := st.CommitMediaUpload(ctx, outside.ID, filepath.Dir(existing), store.MediaUploadKeepBoth, filter, now)
	if err != nil {
		t.Fatalf("keep-both commit: %v", err)
	}
	if filepath.Base(kept.Path) != "film (2).mkv" || kept.Trashed != nil {
		t.Fatalf("keep-both = %+v", kept)
	}
	if got, _ := os.ReadFile(existing); string(got) != "old" {
		t.Fatalf("keep-both touched the existing file: %q", got)
	}

	replace := stageUpload(t, st, "film.mkv", "", []byte("newer"), filter, now)
	replaced, err := st.CommitMediaUpload(ctx, replace.ID, filepath.Dir(existing), store.MediaUploadReplace, filter, now)
	if err != nil {
		t.Fatalf("replace commit: %v", err)
	}
	if replaced.Path != existing || replaced.Trashed == nil || replaced.Trashed.OriginalPath != existing {
		t.Fatalf("replace = %+v", replaced)
	}
	if got, _ := os.ReadFile(existing); string(got) != "newer" {
		t.Fatalf("replaced bytes = %q", got)
	}
	if got, err := os.ReadFile(replaced.Trashed.TrashPath); err != nil || string(got) != "old" {
		t.Fatalf("trashed bytes = %q, %v", got, err)
	}

	// A destination crossing a symlink is refused rather than followed.
	link := filepath.Join(root, "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	linked := stageUpload(t, st, "a.mkv", "", []byte("a"), filter, now)
	if _, err := st.CommitMediaUpload(ctx, linked.ID, filepath.Join(link, "sub"), store.MediaUploadKeepBoth, filter, now); err == nil || !strings.Contains(err.Error(), "link") {
		t.Fatalf("symlinked destination: %v", err)
	}
}

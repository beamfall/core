package scan

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func enqueueHashTestScan(t *testing.T, st *store.Store, id store.JobID, root string, now time.Time) {
	t.Helper()
	job, err := NewJob(JobRequest{ID: id, LibraryID: "library-local", Root: root, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
}

type countingSampleReader struct {
	io.ReaderAt
	bytes int
}

func (r *countingSampleReader) ReadAt(p []byte, offset int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(p, offset)
	r.bytes += n
	return n, err
}

func TestScanBoundsFingerprintReadsOnSparseFile(t *testing.T) {
	root := t.TempDir()
	path := writeScanFile(t, root, "large.mp4", nil)
	const size = 8 << 30
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
	// Put one distinct marker at the first byte of each sample1 chunk.
	offsets := []int64{
		0, 660759158, 1321518316, 1982277474, 2643036632, 3303795790, 3964554948,
		4625314107, 5286073265, 5946832423, 6607591581, 7268350739, 7929109897, 8589869056,
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	sparse, err := dir.OpenFile(filepath.Base(path), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i, offset := range offsets {
		if _, err := sparse.WriteAt([]byte{byte(i + 1)}, offset); err != nil {
			_ = sparse.Close()
			t.Fatal(err)
		}
	}
	if err := sparse.Close(); err != nil {
		t.Fatal(err)
	}
	settled := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, settled, settled); err != nil {
		t.Fatal(err)
	}
	var bytesRead, fullReads int
	savedSample, savedLegacy := sampleFileContent, legacyFileContent
	t.Cleanup(func() { sampleFileContent, legacyFileContent = savedSample, savedLegacy })
	sampleFileContent = func(ctx context.Context, path string) (string, error) {
		file, err := os.OpenInRoot(root, filepath.Base(path))
		if err != nil {
			return "", err
		}
		defer file.Close()
		reader := &countingSampleReader{ReaderAt: file}
		fingerprint, err := store.SampleMediaFileContentAt(ctx, reader, size)
		bytesRead += reader.bytes
		return fingerprint, err
	}
	legacyFileContent = func(ctx context.Context, path string) (string, error) {
		fullReads++
		return "", os.ErrInvalid
	}

	result, err := Scan(t.Context(), storetest.Open(t), nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	const expected = "sample1:e0b5702a0c3922674d841f1e0deafa34a20df3bb35d051d35f11e6357db220cd"
	if len(result.Files) != 1 || result.Files[0].ContentFingerprint != expected {
		t.Fatalf("scan files = %+v, want one file with fingerprint %s", result.Files, expected)
	}
	file := result.Files[0]
	wantID := store.MediaID("media-sample1-" + strings.TrimPrefix(file.ContentFingerprint, "sample1:")[:17])
	if file.MediaID != wantID {
		t.Fatalf("Media ID = %s, want %s", file.MediaID, wantID)
	}
	if bytesRead != 14*64*1024 || fullReads != 0 {
		t.Fatalf("sample bytes = %d, full reads = %d", bytesRead, fullReads)
	}
}

func TestScanLegacyMtimePersistsAcrossReopen(t *testing.T) {
	epoch := time.Unix(0, 0).UTC()
	// Seed the rounded legacy value directly. origin/main used UnixNano()/1e6,
	// so a native mtime of -1.5ms is recorded as -1ms.
	preEpoch := time.UnixMilli(-1).UTC()
	for _, tc := range []struct {
		name      string
		mtime     time.Duration
		seed      time.Time
		want      time.Time
		fullReads int
	}{
		{name: "recorded-pre-epoch", mtime: -1500 * time.Microsecond, seed: preEpoch, want: preEpoch},
		{name: "statless-pre-epoch", mtime: -1500 * time.Microsecond, want: preEpoch, fullReads: 1},
		{name: "statless-epoch", want: epoch, fullReads: 1},
		{name: "statless-rounds-to-epoch", mtime: -500 * time.Microsecond, want: epoch, fullReads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := writeScanFile(t, root, "epoch.mp4", []byte("legacy epoch fixture"))
			mtime := time.Unix(0, int64(tc.mtime)).UTC()
			if err := os.Chtimes(path, mtime, mtime); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil || !info.ModTime().Equal(mtime) {
				t.Fatalf("file stat = %v, err = %v, want mtime %v", info, err, mtime)
			}
			ctx := t.Context()
			dir := storetest.MigratedDataDir(t)
			st := storetest.OpenAt(t, dir)
			req := Request{LibraryID: "library-local", Root: root}
			full, err := store.HashMediaFileContent(path)
			if err != nil {
				t.Fatal(err)
			}
			media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: req.LibraryID, Path: path, Title: "Epoch", Container: "mp4", Codec: "h264", ContentFingerprint: full,
				SizeBytes: info.Size(), ModTime: tc.seed,
			})
			if err != nil {
				t.Fatal(err)
			}
			var fullReads, sampleReads int
			savedLegacy, savedSample := legacyFileContent, sampleFileContent
			t.Cleanup(func() { legacyFileContent, sampleFileContent = savedLegacy, savedSample })
			sampleFileContent = func(ctx context.Context, path string) (string, error) {
				sampleReads++
				return savedSample(ctx, path)
			}
			legacyFileContent = func(ctx context.Context, path string) (string, error) {
				fullReads++
				return savedLegacy(ctx, path)
			}
			if _, err := Scan(ctx, st, nil, nil, nil, nil, nil, req); err != nil {
				t.Fatal(err)
			}
			if fullReads != tc.fullReads || sampleReads != 0 {
				t.Fatalf("first scan: full = %d, sampled = %d, want full = %d, sampled = 0", fullReads, sampleReads, tc.fullReads)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st = storetest.OpenAt(t, dir)
			preview, err := PreviewRoots(ctx, st, req.LibraryID, []string{root}, 0, ImagesBesideMedia)
			if err != nil || preview.Unchanged != 1 || len(preview.Changes) != 0 {
				t.Fatalf("preview after reopen = %+v, err = %v", preview, err)
			}
			if _, err := Scan(ctx, st, nil, nil, nil, nil, nil, req); err != nil {
				t.Fatal(err)
			}
			if fullReads != tc.fullReads || sampleReads != 0 {
				t.Fatalf("after reopen: full = %d, sampled = %d, want full = %d, sampled = 0", fullReads, sampleReads, tc.fullReads)
			}
			stored, err := st.LibraryMediaFileFingerprintByPath(ctx, req.LibraryID, path)
			if err != nil || !stored.OK || !stored.ModTime.Equal(tc.want) {
				t.Fatalf("stats after reopen and rescan = %+v, err = %v", stored, err)
			}
			file, err := st.PrimaryMediaFile(ctx, media.ID, store.LibraryFilter{AllLibraries: true})
			if err != nil || file.ContentFingerprint != full || file.Path != path {
				t.Fatalf("file after reopen and rescan = %+v, err = %v", file, err)
			}
		})
	}
}

func TestScanLegacyFingerprintCompatibility(t *testing.T) {
	ctx := t.Context()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st := storetest.Open(t)
	req := Request{LibraryID: "library-local", Root: root}
	if err := st.SetLibraryRoots(ctx, req.LibraryID, []string{root}); err != nil {
		t.Fatal(err)
	}
	legacyCalls, sampleCalls := make(map[string]int), make(map[string]int)
	savedLegacy, savedSample := legacyFileContent, sampleFileContent
	t.Cleanup(func() { legacyFileContent, sampleFileContent = savedLegacy, savedSample })
	sampleFileContent = func(ctx context.Context, path string) (string, error) {
		sampleCalls[path]++
		return savedSample(ctx, path)
	}
	legacyFileContent = func(ctx context.Context, path string) (string, error) {
		legacyCalls[path]++
		return savedLegacy(ctx, path)
	}
	cases := []struct {
		name         string
		missingMtime bool
		change       bool
		sameSize     bool
		move         bool
		missing      bool
		trashed      bool
		wantFullRead bool
	}{
		{name: "recorded"},
		{name: "replacement-with-preserved-stats", missing: true, change: true, sameSize: true},
		{name: "returning-statless", missing: true, missingMtime: true},
		{name: "managed-trash", trashed: true},
		{name: "statless", missingMtime: true, wantFullRead: true},
		{name: "moved", move: true},
		{name: "known-size-changed", missingMtime: true, change: true},
		{name: "changed-with-missing-mtime", missingMtime: true, change: true, sameSize: true, wantFullRead: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(tc.name)
			path := writeScanFile(t, root, tc.name+".mp4", payload)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			full, err := store.HashMediaFileContent(path)
			if err != nil {
				t.Fatal(err)
			}
			mtime := info.ModTime()
			if tc.missingMtime {
				mtime = time.Time{}
			}
			media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: req.LibraryID, Path: path, Title: tc.name, Container: "mp4", Codec: "h264",
				ContentFingerprint: full, SizeBytes: info.Size(), ModTime: mtime,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.SetMatchPinned(ctx, full, true); err != nil {
				t.Fatal(err)
			}
			const profile = "profile-default"
			if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
				ProfileID: profile, MediaID: media.ID, DurationSeconds: 100, PositionSeconds: 32,
			}, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
				t.Fatal(err)
			}

			scanPath := path
			if tc.move {
				scanPath = filepath.Join(root, "external-rename.mp4")
				if err := os.Rename(path, scanPath); err != nil {
					t.Fatal(err)
				}
			}
			if tc.change && !tc.missing {
				size := int(info.Size())
				if !tc.sameSize {
					size++
				}
				writeScanFile(t, root, tc.name+".mp4", []byte(strings.Repeat("x", size)))
			}
			if tc.missing || tc.trashed {
				if tc.trashed {
					if _, err := st.DeleteManagedPath(ctx, req.LibraryID, path, store.LibraryFilter{AllLibraries: true}, time.Now()); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := st.MarkMediaFileMissing(ctx, req.LibraryID, path); err != nil {
						t.Fatal(err)
					}
				}
				if _, live, err := st.MediaFileExistsByFingerprint(ctx, full); err != nil || live {
					t.Fatalf("missing fixture still live: %t, %v", live, err)
				}
				if tc.change {
					payload = []byte(strings.Repeat("x", len(payload)))
				}
				writeScanFile(t, root, filepath.Base(path), payload)
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if _, err := Scan(ctx, st, nil, nil, nil, nil, nil, req); err != nil {
					t.Fatal(err)
				}
			}
			wantFullReads := 0
			if tc.wantFullRead {
				wantFullReads = 1
			}
			if legacyCalls[path] != wantFullReads || tc.move && legacyCalls[scanPath] != 0 {
				t.Fatalf("legacy hash calls = %v, want %d for %q", legacyCalls, wantFullReads, path)
			}

			stored, err := st.LibraryMediaFileFingerprintByPath(ctx, req.LibraryID, scanPath)
			if err != nil || !stored.OK || stored.SizeBytes == 0 || stored.ModTime.IsZero() {
				t.Fatalf("stored fingerprint = %+v, err = %v", stored, err)
			}
			id, found, err := st.MediaFileExistsByFingerprint(ctx, stored.ContentFingerprint)
			if err != nil || !found {
				t.Fatalf("lookup by %q: found = %t, err = %v", stored.ContentFingerprint, found, err)
			}
			if tc.move || tc.change || tc.trashed || tc.missing {
				if !strings.HasPrefix(stored.ContentFingerprint, "sample1:") || id == media.ID {
					t.Fatalf("replacement = %q, %s, want a new sampled identity", stored.ContentFingerprint, id)
				}
				if sampleCalls[scanPath] != 1 {
					t.Fatalf("path was sampled %d times, want once", sampleCalls[scanPath])
				}
				return
			}
			if sampleCalls[path] != 0 {
				t.Fatalf("unchanged legacy file was sampled %d times", sampleCalls[path])
			}
			if stored.ContentFingerprint != full || id != media.ID {
				t.Fatalf("stored = %q, %s, want %q, %s", stored.ContentFingerprint, id, full, media.ID)
			}
			progress, found, err := st.PlaybackProgressFor(ctx, profile, id)
			if err != nil || !found || progress.PositionSeconds != 32 {
				t.Fatalf("playback progress = %+v, found = %t, err = %v", progress, found, err)
			}
			pinned, found, err := st.MatchPinned(ctx, full)
			if err != nil || !found || !pinned {
				t.Fatalf("match pin = %t, found = %t, err = %v", pinned, found, err)
			}
		})
	}
}

// Keep the catalog snapshot in memory during seal, so no plaintext is written
// outside the store's working area by this lifecycle fixture.
type scanCatalogCipher struct {
	snapshot []byte
}

func (c *scanCatalogCipher) SealDBFile(_ context.Context, _ store.LibraryID, workingPath, _ string) error {
	root, err := os.OpenRoot(filepath.Dir(workingPath))
	if err != nil {
		return err
	}
	defer root.Close()
	c.snapshot, err = root.ReadFile(filepath.Base(workingPath))
	return err
}

func (c *scanCatalogCipher) UnsealDBFile(_ context.Context, _ store.LibraryID, _, workingPath string) error {
	if c.snapshot == nil {
		return nil
	}
	return os.WriteFile(workingPath, c.snapshot, 0o600)
}

func TestScanPreservesEncryptedLegacyMediaAfterSeal(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(ctx, storetest.MigratedDataDir(t), store.Options{
		SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	req := Request{LibraryID: "library-local", Root: t.TempDir()}
	if err := st.SetLibraryBacking(ctx, req.LibraryID, false, true); err != nil {
		t.Fatal(err)
	}
	var cipher scanCatalogCipher
	if err := st.UnsealEncryptedLibraryDB(ctx, req.LibraryID, &cipher); err != nil {
		t.Fatal(err)
	}
	want := make(map[string]store.MediaFile)
	for _, name := range []string{"unchanged.mp4", "returning.mp4"} {
		path := writeScanFile(t, req.Root, name, []byte(name))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		fingerprint, err := store.HashMediaFileContent(path)
		if err != nil {
			t.Fatal(err)
		}
		_, file, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: req.LibraryID, Path: path, Title: name, Container: "mp4", Codec: "h264",
			ContentFingerprint: fingerprint, SizeBytes: info.Size(), ModTime: info.ModTime(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if name == "returning.mp4" {
			if err := st.MarkMediaFileMissing(ctx, req.LibraryID, path); err != nil {
				t.Fatal(err)
			}
		}
		want[path] = file
	}
	if err := st.SealEncryptedLibraryDB(ctx, req.LibraryID, &cipher); err != nil {
		t.Fatal(err)
	}
	if st.EncryptedLibraryDBRegistered(req.LibraryID) {
		t.Fatal("catalog stayed open after seal")
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, req.LibraryID, &cipher); err != nil {
		t.Fatal(err)
	}
	savedLegacy, savedSample := legacyFileContent, sampleFileContent
	t.Cleanup(func() { legacyFileContent, sampleFileContent = savedLegacy, savedSample })
	legacyFileContent = func(_ context.Context, path string) (string, error) {
		t.Errorf("encrypted path was hashed in full: %s", path)
		return "", os.ErrInvalid
	}
	sampleFileContent = func(ctx context.Context, path string) (string, error) {
		if filepath.Base(path) == "returning.mp4" {
			return savedSample(ctx, path)
		}
		t.Errorf("unchanged encrypted path was sampled: %s", path)
		return "", os.ErrInvalid
	}
	if _, err := ScanWithWorkerGovernor(ctx, st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(4), req); err != nil {
		t.Fatal(err)
	}
	files, err := st.MediaFilesForLibrary(ctx, req.LibraryID)
	if err != nil || len(files) != len(want) {
		t.Fatalf("encrypted files after rescan = %+v, err = %v", files, err)
	}
	for _, file := range files {
		before := want[file.Path]
		if filepath.Base(file.Path) == "returning.mp4" {
			if before.MediaID == file.MediaID || !strings.HasPrefix(file.ContentFingerprint, "sample1:") {
				t.Fatalf("returning encrypted file = %+v, want new identity after %+v", file, before)
			}
			continue
		}
		if before.MediaID != file.MediaID || before.ContentFingerprint != file.ContentFingerprint {
			t.Fatalf("encrypted file after rescan = %+v, want identity from %+v", file, before)
		}
	}
}

func TestScanAndManagedImportShareSampleIdentity(t *testing.T) {
	ctx := t.Context()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st := storetest.Open(t)
	req := Request{LibraryID: "library-local", Root: root}
	if err := st.SetLibraryRoots(ctx, req.LibraryID, []string{root}); err != nil {
		t.Fatal(err)
	}
	scanned := writeScanFile(t, root, "scanned.mp4", []byte("already scanned"))
	if _, err := Scan(ctx, st, nil, nil, nil, nil, nil, req); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	importFile := func(name, content string) (store.ManagedPath, error) {
		t.Helper()
		path := writeScanFile(t, outside, name, []byte(content))
		source, err := st.RegisterImportSource(path)
		if err != nil {
			t.Fatal(err)
		}
		return st.ImportManagedFile(ctx, req.LibraryID, source, root, "mp4", "h264", store.MediaClassVideo, store.LibraryFilter{AllLibraries: true})
	}
	var validation store.ValidationError
	if _, err := importFile("duplicate.mp4", "already scanned"); !errors.As(err, &validation) {
		t.Fatalf("import of already scanned content: %v, want validation error", err)
	}
	if _, err := os.Stat(filepath.Join(root, "duplicate.mp4")); !os.IsNotExist(err) {
		t.Fatalf("duplicate path still exists: %v", err)
	}
	imported, err := importFile("imported.mp4", "new managed bytes")
	if err != nil {
		t.Fatal(err)
	}
	settled := time.Now().Add(-time.Minute)
	if err := os.Chtimes(imported.Path, settled, settled); err != nil {
		t.Fatal(err)
	}
	before, err := st.LibraryMediaFileFingerprintByPath(ctx, req.LibraryID, imported.Path)
	if err != nil || !before.OK || !strings.HasPrefix(before.ContentFingerprint, "sample1:") {
		t.Fatalf("managed identity = %+v, err = %v", before, err)
	}
	beforeID, found, err := st.MediaFileExistsByFingerprint(ctx, before.ContentFingerprint)
	if err != nil || !found {
		t.Fatalf("managed Media ID = %s, found = %t, err = %v", beforeID, found, err)
	}
	if _, err := Scan(ctx, st, nil, nil, nil, nil, nil, req); err != nil {
		t.Fatal(err)
	}
	files, err := st.MediaFilesForLibrary(ctx, req.LibraryID)
	if err != nil {
		t.Fatal(err)
	}
	live := make(map[string]store.MediaFile)
	for _, file := range files {
		live[file.Path] = file
	}
	if len(live) != 2 || live[scanned].MediaID == "" ||
		live[imported.Path].ContentFingerprint != before.ContentFingerprint || live[imported.Path].MediaID != beforeID {
		t.Fatalf("live files = %+v, want scanned file and unchanged managed identity %q", live, before.ContentFingerprint)
	}
}

func TestManagedImportRestoresLiveLegacyIdentityAtSamePath(t *testing.T) {
	ctx := t.Context()
	st := storetest.Open(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const libraryID, profile, name = "library-local", "profile-default", "legacy.mp4"
	if err := st.SetLibraryRoots(ctx, libraryID, []string{root}); err != nil {
		t.Fatal(err)
	}
	payload := []byte("legacy content")
	path := writeScanFile(t, root, name, payload)
	legacy, err := store.HashMediaFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: libraryID, Path: path, Title: "Legacy", Container: "mp4", Codec: "h264",
		ContentFingerprint: legacy, SizeBytes: int64(len(payload)), ModTime: time.Unix(1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := st.SetMatchPinned(ctx, legacy, true); err != nil || !ok {
		t.Fatalf("set original match pin: %t, %v", ok, err)
	}
	if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
		ProfileID: profile, MediaID: media.ID, DurationSeconds: 100, PositionSeconds: 32,
	}, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	source, err := st.RegisterImportSource(writeScanFile(t, t.TempDir(), name, payload))
	if err != nil {
		t.Fatal(err)
	}
	imported, err := st.ImportManagedFile(ctx, libraryID, source, root, "mp4", "h264", store.MediaClassVideo, store.LibraryFilter{AllLibraries: true})
	if err != nil || imported.Path != path {
		t.Fatalf("reimport = %+v, err = %v, want %q", imported, err, path)
	}
	checkIdentity := func() {
		t.Helper()
		files, err := st.MediaFilesForLibrary(ctx, libraryID)
		if err != nil || len(files) != 1 || files[0].Path != path || files[0].MediaID != media.ID || files[0].ContentFingerprint != legacy {
			t.Fatalf("live files = %+v, err = %v, want legacy %q at %q on %q", files, err, legacy, path, media.ID)
		}
	}
	checkIdentity()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stubNow(t, info.ModTime().Add(time.Minute))
	result, err := Scan(ctx, st, nil, nil, nil, nil, nil, Request{LibraryID: libraryID, Root: root})
	if err != nil || result.FilesSeen != 1 || result.FilesAdded != 0 || result.FilesChanged != 0 {
		t.Fatalf("rescan = %+v, err = %v, want the fast path", result, err)
	}
	checkIdentity()
	progress, found, err := st.PlaybackProgressFor(ctx, profile, media.ID)
	if err != nil || !found || progress.PositionSeconds != 32 {
		t.Fatalf("playback progress = %+v, found = %t, err = %v", progress, found, err)
	}
	if pinned, found, err := st.MatchPinned(ctx, legacy); err != nil || !found || !pinned {
		t.Fatalf("match pin = %t, found = %t, err = %v", pinned, found, err)
	}
}

func TestScanJobYieldClosesInFlightHashReader(t *testing.T) {
	// feature:scan-pipeline
	now := time.Now().UTC()
	st := storetest.Open(t)
	root := t.TempDir()
	first := writeScanFile(t, root, "a.mp4", []byte("first"))
	writeScanFile(t, root, "b.mp4", []byte("second"))
	// Fill the two-worker completion buffer from cache so the first file can
	// checkpoint while the next file still hashes.
	if _, err := Scan(t.Context(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatal(err)
	}
	pending := writeScanFile(t, root, "c.mp4", []byte("still more data"))
	runCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	reading, closed := make(chan struct{}), make(chan struct{})
	original := sampleFileContent
	sampleFileContent = func(ctx context.Context, path string) (string, error) {
		if path != pending {
			return original(ctx, path)
		}
		file, err := os.OpenInRoot(root, "c.mp4")
		if err != nil {
			return "", err
		}
		defer func() {
			_ = file.Close()
			close(closed)
		}()
		var chunk [1]byte
		if _, err := io.ReadFull(file, chunk[:]); err != nil {
			return "", err
		}
		close(reading)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-runCtx.Done():
			return "", runCtx.Err()
		}
	}
	savedBudget, savedClock := scanJobRunBudget, scanJobClock
	t.Cleanup(func() {
		sampleFileContent = original
		scanJobRunBudget, scanJobClock = savedBudget, savedClock
	})
	scanJobRunBudget = 0
	scanJobClock = func() time.Time {
		select {
		case <-reading:
		case <-runCtx.Done():
		}
		return now
	}
	enqueueHashTestScan(t, st, "yield-mid-hash", root, now)
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, HandlerWithWorkerGovernor(st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(2)))
	if _, err := registry.RunOne(runCtx, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := runCtx.Err(); err != nil {
		t.Fatalf("scan did not yield before the outer context ended: %v", err)
	}
	select {
	case <-closed:
	default:
		t.Fatal("yield returned with the hash reader still open")
	}
	yielded, err := st.Job(t.Context(), "yield-mid-hash")
	if err != nil {
		t.Fatal(err)
	}
	if yielded.Status != store.JobStatusYielded {
		t.Fatalf("status = %s (%s), want yielded", yielded.Status, yielded.Error)
	}
	checkpoint := mustDecodeCheckpoint(t, yielded.Checkpoint)
	if checkpoint.LastPath != first || checkpoint.FilesSeen != 1 {
		t.Fatalf("checkpoint = %+v, want one completed file at %q", checkpoint, first)
	}
}

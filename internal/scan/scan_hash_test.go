package scan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	store.SampleFile
	bytes int
}

func (r *countingSampleReader) Read(p []byte) (int, error) {
	n, err := r.SampleFile.Read(p)
	r.bytes += n
	return n, err
}

func (r *countingSampleReader) ReadAt(p []byte, offset int64) (int, error) {
	n, err := r.SampleFile.ReadAt(p, offset)
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
	var readers []*countingSampleReader
	var fullReads int
	savedSample, savedLegacy := sampleScanFile, legacyFileContent
	t.Cleanup(func() { sampleScanFile, legacyFileContent = savedSample, savedLegacy })
	sampleScanFile = func(ctx context.Context, path string) (string, error) {
		return store.SampleMediaFileContentFrom(ctx, func() (store.SampleFile, error) {
			file, err := os.OpenInRoot(filepath.Dir(path), filepath.Base(path))
			if err != nil {
				return nil, err
			}
			reader := &countingSampleReader{SampleFile: file}
			readers = append(readers, reader)
			return reader, nil
		})
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
	var bytesRead int
	for _, reader := range readers {
		bytesRead += reader.bytes
	}
	if len(readers) != 1 || bytesRead != 14*64*1024 || fullReads != 0 {
		t.Fatalf("sample opens = %d, sample bytes = %d, legacy hashes = %d", len(readers), bytesRead, fullReads)
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
			savedLegacy, savedSample := legacyFileContent, sampleScanFile
			t.Cleanup(func() { legacyFileContent, sampleScanFile = savedLegacy, savedSample })
			sampleScanFile = func(ctx context.Context, path string) (string, error) {
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
			if fullReads != tc.fullReads || sampleReads != tc.fullReads {
				t.Fatalf("first scan: full = %d, sampled = %d, want full = %d, sampled = %d", fullReads, sampleReads, tc.fullReads, tc.fullReads)
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
			if fullReads != tc.fullReads || sampleReads != tc.fullReads {
				t.Fatalf("after reopen: full = %d, sampled = %d, want full = %d, sampled = %d", fullReads, sampleReads, tc.fullReads, tc.fullReads)
			}
			stored, err := st.MediaFileFingerprintByPath(ctx, path)
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
	savedLegacy, savedSample := legacyFileContent, sampleScanFile
	savedNow := now
	t.Cleanup(func() { legacyFileContent, sampleScanFile, now = savedLegacy, savedSample, savedNow })
	// Let newly admitted inodes settle without changing their actual mtimes.
	now = func() time.Time { return savedNow().Add(time.Minute) }
	sampleScanFile = func(ctx context.Context, path string) (string, error) {
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
		reimport     bool
		missingFirst bool
		wantFullRead bool
	}{
		{name: "recorded"},
		{name: "replacement-with-preserved-stats", missing: true, change: true, sameSize: true},
		{name: "returning-small", missing: true},
		{name: "returning-statless", missing: true, missingMtime: true},
		{name: "managed-trash", trashed: true},
		{name: "managed-reimport-before-scan-marks-missing", reimport: true, wantFullRead: true},
		{name: "managed-reimport-after-scan-marks-missing", reimport: true, missingFirst: true, wantFullRead: true},
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
			if tc.reimport {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if tc.missingFirst {
					if err := st.MarkMediaFileMissing(ctx, req.LibraryID, path); err != nil {
						t.Fatal(err)
					}
					if _, live, err := st.MediaFileExistsByFingerprint(ctx, full); err != nil || live {
						t.Fatalf("missing fixture still live: %t, %v", live, err)
					}
				}
				source, err := st.RegisterImportSource(writeScanFile(t, t.TempDir(), filepath.Base(path), payload))
				if err != nil {
					t.Fatal(err)
				}
				admitted, err := st.ImportManagedFile(ctx, req.LibraryID, source, root, "mp4", "h264", store.MediaClassVideo, store.LibraryFilter{AllLibraries: true})
				if err != nil || admitted.Path != path {
					t.Fatalf("managed reimport = %+v, %v, want %q", admitted, err, path)
				}
				if id, live, err := st.MediaFileExistsByFingerprint(ctx, full); err != nil || !live || id != media.ID {
					t.Fatalf("legacy match after reimport = %q, live = %t, err = %v, want %q", id, live, err, media.ID)
				}
				landed, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if landed.Size() != info.Size() || store.MediaFileModTimeMatches(info.ModTime(), landed.ModTime()) {
					t.Fatalf("expected same size and a new mtime after staging, got size %d and mtime %v", landed.Size(), landed.ModTime())
				}
			}
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

			stored, err := st.MediaFileFingerprintByPath(ctx, scanPath)
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
			if sampleCalls[path] != wantFullReads {
				t.Fatalf("legacy file was sampled %d times, want %d", sampleCalls[path], wantFullReads)
			}
			if stored.ContentFingerprint != full || id != media.ID {
				t.Fatalf("stored = %q, %s, want %q, %s", stored.ContentFingerprint, id, full, media.ID)
			}
			if tc.reimport {
				landed, err := os.Stat(path)
				if err != nil || !store.MediaFileModTimeMatches(stored.ModTime, landed.ModTime()) {
					t.Fatalf("managed reimport mtime after rescan = %v, stat = %v, err = %v", stored.ModTime, landed, err)
				}
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

func TestScanAndManagedImportShareSampleIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prober Prober
	}{
		{name: "unavailable probe"},
		{name: "failed probe", prober: fakeProber{err: errors.New("probe failed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testScanAndManagedImportShareSampleIdentity(t, tc.prober)
		})
	}
}

func testScanAndManagedImportShareSampleIdentity(t *testing.T, prober Prober) {
	t.Helper()
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
	stubNow(t, time.Now().Add(time.Minute))
	before, err := st.MediaFileFingerprintByPath(ctx, imported.Path)
	if err != nil || !before.OK || !strings.HasPrefix(before.ContentFingerprint, "sample1:") {
		t.Fatalf("managed identity = %+v, err = %v", before, err)
	}
	beforeID, found, err := st.MediaFileExistsByFingerprint(ctx, before.ContentFingerprint)
	if err != nil || !found {
		t.Fatalf("managed Media ID = %s, found = %t, err = %v", beforeID, found, err)
	}
	if before.SizeBytes != 0 || !before.ModTime.IsZero() {
		t.Fatalf("managed import recorded scan stats before the first scan: %+v", before)
	}
	if _, err := Scan(ctx, st, prober, nil, nil, nil, nil, req); err != nil {
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
	wantOSHash, err := OSHash(imported.Path)
	if err != nil || wantOSHash == "" || live[imported.Path].OSHash != wantOSHash {
		t.Fatalf("persisted OSHash = %q, expected %q, err = %v", live[imported.Path].OSHash, wantOSHash, err)
	}

}

func TestScanReturningSampleIgnoresUnrelatedLegacyStats(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	// The missing legacy row is newer and has identical stats, but its bytes
	// differ. The exact sampled identity still owns the returning file.
	const size = 1 << 20
	payload := bytes.Repeat([]byte{1}, size)
	path := writeScanFile(t, root, "returning.mp4", payload)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := storetest.Open(t)
	req := Request{LibraryID: "library-local", Root: root}
	fingerprint, err := store.SampleMediaFileContent(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := fmt.Sprintf("sha256:%x", sha256.Sum256(bytes.Repeat([]byte{2}, size)))
	const profile = "profile-default"
	ids := make(map[string]store.MediaID)
	for _, digest := range []string{fingerprint, legacy} {
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: req.LibraryID, Path: path, Title: "Returning", Container: "mp4", Codec: "h264",
			ContentFingerprint: digest, SizeBytes: info.Size(), ModTime: info.ModTime(),
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[digest] = media.ID
		if err := st.MarkMediaFileMissing(ctx, req.LibraryID, path); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
		ProfileID: profile, MediaID: ids[legacy], DurationSeconds: 100, PositionSeconds: 32,
	}, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	original := legacyFileContent
	t.Cleanup(func() { legacyFileContent = original })
	legacyFileContent = func(context.Context, string) (string, error) {
		t.Error("returning file was read in full")
		return "", os.ErrInvalid
	}
	if _, err := Scan(ctx, st, nil, nil, nil, nil, nil, req); err != nil {
		t.Fatal(err)
	}
	files, err := st.MediaFilesForLibrary(ctx, req.LibraryID)
	if err != nil || len(files) != 1 || files[0].ContentFingerprint != fingerprint || files[0].MediaID != ids[fingerprint] {
		t.Fatalf("returning file = %+v, err = %v, want identity %s", files, err, ids[fingerprint])
	}
	if progress, found, err := st.PlaybackProgressFor(ctx, profile, files[0].MediaID); err != nil || found {
		t.Fatalf("returning file inherited playback progress %+v, found = %t, err = %v", progress, found, err)
	}
}

func TestScanJobsSerializeFileSamples(t *testing.T) {
	// feature:scan-pipeline
	now := time.Now().UTC()
	st := storetest.Open(t)
	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	firstPath := writeScanFile(t, firstRoot, "first.mp4", []byte("first"))
	secondPath := writeScanFile(t, secondRoot, "second.mp4", []byte("second"))
	enqueueHashTestScan(t, st, "first", firstRoot, now)
	enqueueHashTestScan(t, st, "second", secondRoot, now.Add(time.Millisecond))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	var runs sync.WaitGroup
	defer func() {
		cancel()
		runs.Wait()
	}()
	firstHash := make(chan struct{})
	firstHashReturned := make(chan struct{})
	secondHash := make(chan struct{})
	secondWaiting := make(chan struct{})
	releaseFirst := make(chan struct{})
	originalHash, originalWait := sampleScanFile, onScanHashWait
	onScanHashWait = func(path string) {
		if path == secondPath {
			close(secondWaiting)
		}
	}
	sampleScanFile = func(hashCtx context.Context, path string) (string, error) {
		switch path {
		case firstPath:
			defer close(firstHashReturned)
			close(firstHash)
			select {
			case <-releaseFirst:
			case <-ctx.Done(): // Bound teardown even if the scan drops cancellation.
				return "", ctx.Err()
			}
		case secondPath:
			select {
			case <-firstHashReturned:
			default:
				t.Error("second job entered its hash before the first hash returned")
			}
			close(secondHash)
		}
		return originalHash(hashCtx, path)
	}
	t.Cleanup(func() { sampleScanFile, onScanHashWait = originalHash, originalWait })

	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, HandlerWithWorkerGovernor(st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(2)))
	run := func() <-chan error {
		done := make(chan error, 1)
		runs.Go(func() {
			_, err := registry.RunOne(ctx, nil, now.Add(time.Second))
			done <- err
		})
		return done
	}
	firstDone := run()
	select {
	case <-firstHash:
	case <-ctx.Done():
		t.Fatal("first job never started hashing")
	}
	secondDone := run()
	select {
	case <-secondWaiting:
	case <-secondHash:
		t.Fatal("second job entered its hash while the first job was hashing")
	case <-ctx.Done():
		t.Fatal("second job never waited for the occupied hash slot")
	}
	close(releaseFirst)
	for _, done := range []<-chan error{firstDone, secondDone} {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []store.JobID{"first", "second"} {
		job, err := st.Job(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != store.JobStatusCompleted {
			t.Fatalf("job %s status = %s (%s), want completed", id, job.Status, job.Error)
		}
	}
	select {
	case <-secondHash:
	default:
		t.Fatal("second job never started hashing after the slot was released")
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
	original := sampleScanFile
	sampleScanFile = func(ctx context.Context, path string) (string, error) {
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
		sampleScanFile = original
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

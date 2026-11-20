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
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/libencrypt"
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
	savedSample := sampleScanFile
	t.Cleanup(func() { sampleScanFile = savedSample })
	sampleScanFile = func(ctx context.Context, path string, check store.SampleFingerprintCheck) (string, error) {
		if check != nil {
			fullReads++
		}
		return store.SampleMediaFileContentFrom(ctx, path, func() (store.SampleFile, error) {
			file, err := os.OpenInRoot(filepath.Dir(path), filepath.Base(path))
			if err != nil {
				return nil, err
			}
			reader := &countingSampleReader{SampleFile: file}
			readers = append(readers, reader)
			return reader, nil
		}, check)
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

type changingSampleFile struct {
	*os.File
	beforeFirstRead func() error
}

func (f *changingSampleFile) ReadAt(p []byte, offset int64) (int, error) {
	if f.beforeFirstRead != nil {
		change := f.beforeFirstRead
		f.beforeFirstRead = nil
		if err := change(); err != nil {
			return 0, err
		}
	}
	return f.File.ReadAt(p, offset)
}

func TestScanTreatsUnstableSampleAsUnchanged(t *testing.T) {
	readErr := errors.New("injected sample read error")
	for _, tc := range []struct {
		name    string
		change  func(*os.File, string, os.FileInfo) error
		wantErr error
	}{
		{name: "grow", change: func(file *os.File, _ string, before os.FileInfo) error {
			return file.Truncate(before.Size() + 1)
		}},
		{name: "touch mtime", change: func(_ *os.File, path string, before os.FileInfo) error {
			mtime := before.ModTime().Add(time.Second)
			return os.Chtimes(path, mtime, mtime)
		}},
		{name: "truncate", change: func(file *os.File, _ string, before os.FileInfo) error {
			return file.Truncate(before.Size() - 1)
		}, wantErr: io.EOF},
		{name: "read error", change: func(*os.File, string, os.FileInfo) error {
			return readErr
		}, wantErr: readErr},
		{name: "rename over", change: func(_ *os.File, path string, _ os.FileInfo) error {
			return os.Rename(filepath.Join(filepath.Dir(path), ".replacement"), path)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			root := t.TempDir()
			dataDir := storetest.MigratedDataDir(t)
			st := storetest.OpenAt(t, dataDir)
			req := Request{LibraryID: partsLibrary(t, st), Root: root}
			filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{req.LibraryID}}
			const payload = "first part"
			path := writeScanFile(t, root, "disc-a/01.mp3", []byte(payload))
			writeScanFile(t, root, "disc-b/02.mp3", []byte("second part"))
			parsers := map[string]FilenameParser{"parts": numberedPartsParser{}}
			run := func(started time.Time) Result {
				t.Helper()
				result, err := scanFromCheckpoint(ctx, st, nil, nil, parsers, nil, nil, nil, nil, fixedGovernor(2), nil, req, jobCheckpoint{}, started, nil)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			initial := run(time.Now())
			if initial.FilesSeen != 2 || initial.MediaImported != 1 {
				t.Fatalf("fixture did not import one Media with two Parts: %+v", initial)
			}
			mediaID := initial.Files[0].MediaID
			before, err := st.MediaFilesForMedia(ctx, mediaID, filter)
			if err != nil || len(before) != 2 || before[0].Path != path {
				t.Fatalf("fixture Parts = %+v, err = %v", before, err)
			}
			stats := func(path string) store.StoredFingerprint {
				t.Helper()
				stored, err := st.MediaFileFingerprintByPath(ctx, path)
				if err != nil || !stored.OK || stored.ModTime.IsZero() {
					t.Fatalf("stored file stats for %s = %+v, err = %v", path, stored, err)
				}
				return stored
			}
			beforeStats := []store.StoredFingerprint{stats(before[0].Path), stats(before[1].Path)}
			fingerprint := before[0].ContentFingerprint
			if found, err := st.SetMatchPinned(ctx, fingerprint, true); err != nil || !found {
				t.Fatalf("set match pin: found = %t, err = %v", found, err)
			}
			const profile = "profile-default"
			resumeAudio := true
			if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
				ProfileID: profile, MediaID: mediaID, DurationSeconds: 100, PositionSeconds: 32,
			}, false, store.ResumePolicy{Class: store.MediaClassAudio, Override: &resumeAudio}); err != nil {
				t.Fatal(err)
			}
			checkParts := func() {
				t.Helper()
				files, err := st.MediaFilesForMedia(ctx, mediaID, filter)
				if err != nil || len(files) != len(before) {
					t.Fatalf("Parts = %+v, err = %v, want both Parts of %s", files, err, mediaID)
				}
				for i, file := range files {
					want := before[i]
					if file.MediaID != want.MediaID || file.Path != want.Path || file.ContentFingerprint != want.ContentFingerprint ||
						file.PartOrdinal != want.PartOrdinal {
						t.Fatalf("Part %d = %+v, want original identity and ordinal %+v", i, file, want)
					}
					if got := stats(file.Path); got != beforeStats[i] {
						t.Fatalf("Part %d stats = %+v, want %+v", i, got, beforeStats[i])
					}
				}
				if progress, found, err := st.PlaybackProgressFor(ctx, profile, mediaID); err != nil || !found || progress.PositionSeconds != 32 {
					t.Fatalf("watch state = %+v, found = %t, err = %v", progress, found, err)
				}
				if pinned, found, err := st.MatchPinned(ctx, fingerprint); err != nil || !found || !pinned {
					t.Fatalf("match pin = %t, found = %t, err = %v", pinned, found, err)
				}
			}
			checkParts()

			// Force sampling even though the bytes match. Keep the replacement's
			// size and mtime identical so only the inode check can reject it.
			mtime := beforeStats[0].ModTime.Add(time.Second)
			if err := os.Chtimes(path, mtime, mtime); err != nil {
				t.Fatal(err)
			}
			if tc.name == "rename over" {
				replacement := writeScanFile(t, filepath.Dir(path), ".replacement", bytes.Repeat([]byte{'x'}, len(payload)))
				if err := os.Chtimes(replacement, mtime, mtime); err != nil {
					t.Fatal(err)
				}
			}
			writeScanFile(t, root, "disc-a/01_cover.jpg", jpegBytes("between Parts"))
			disc, err := os.OpenRoot(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			defer disc.Close()
			original := sampleScanFile
			t.Cleanup(func() { sampleScanFile = original })
			var injected bool
			var changeErr, sampleErr error
			sampleScanFile = func(ctx context.Context, candidate string, check store.SampleFingerprintCheck) (string, error) {
				if candidate != path {
					return original(ctx, candidate, check)
				}
				digest, err := store.SampleMediaFileContentFrom(ctx, path, func() (store.SampleFile, error) {
					file, err := disc.OpenFile(filepath.Base(path), os.O_RDWR, 0)
					if err != nil {
						return nil, err
					}
					info, err := file.Stat()
					if err != nil {
						_ = file.Close()
						return nil, err
					}
					return &changingSampleFile{File: file, beforeFirstRead: func() error {
						injected = true
						changeErr = tc.change(file, path, info)
						return changeErr
					}}, nil
				}, check)
				sampleErr = err
				return digest, err
			}
			cutoff := time.UnixMilli(max(before[0].LastSeenAt.UnixMilli(), before[1].LastSeenAt.UnixMilli()) + 1)
			first := run(cutoff)
			if !injected || changeErr != nil && !errors.Is(changeErr, readErr) ||
				sampleErr == nil || tc.wantErr != nil && !errors.Is(sampleErr, tc.wantErr) {
				t.Fatalf("injection reached = %t, mutation error = %v, sample error = %v", injected, changeErr, sampleErr)
			}
			if first.FilesSeen != 3 || first.MediaImported != 2 || first.FilesAdded != 1 || first.FilesChanged != 0 || first.FilesMissing != 0 {
				t.Fatalf("unstable scan did not complete both Parts normally: %+v", first)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st = storetest.OpenAt(t, dataDir)
			checkParts()

			sampleScanFile = original
			if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, mtime, mtime); err != nil {
				t.Fatal(err)
			}
			stable := run(cutoff.Add(time.Millisecond))
			if stable.FilesSeen != 3 || stable.MediaImported != 2 || stable.FilesMissing != 0 {
				t.Fatalf("stable retry did not complete both Parts: %+v", stable)
			}
			beforeStats[0].ModTime = mtime
			checkParts()
		})
	}

	t.Run("new path with read error", func(t *testing.T) {
		ctx := t.Context()
		root := t.TempDir()
		path := writeScanFile(t, root, "new.mp4", []byte("new bytes"))
		st := storetest.Open(t)
		req := Request{LibraryID: "library-local", Root: root}
		logs := captureScanWarnings(t)
		original := sampleScanFile
		t.Cleanup(func() { sampleScanFile = original })
		var reads int
		sampleScanFile = func(_ context.Context, candidate string, _ store.SampleFingerprintCheck) (string, error) {
			if candidate == path {
				reads++
			}
			return "", readErr
		}
		for attempt := 1; attempt <= 2; attempt++ {
			logs.Reset()
			result, err := Scan(ctx, st, nil, nil, nil, nil, nil, req)
			if err != nil || result.FilesSeen != 0 || reads != attempt {
				t.Fatalf("unstable scan %d = %+v, reads = %d, err = %v, want one read attempt per scan and no admission", attempt, result, reads, err)
			}
			const message = `msg="scan: file sample could not be verified"`
			log := logs.String()
			_, warning, _ := strings.Cut(log, message)
			warning, _, _ = strings.Cut(warning, "\n")
			if strings.Count(log, message) != 1 || !strings.Contains(warning, path) || !strings.Contains(warning, readErr.Error()) {
				t.Errorf("scan %d warnings = %q, want one sample warning with path %q and error %q", attempt, log, path, readErr)
			}
		}
		sampleScanFile = original
		second, err := Scan(ctx, st, nil, nil, nil, nil, nil, req)
		if err != nil || len(second.Files) != 1 || second.Files[0].Path != path || second.FilesAdded != 1 {
			t.Fatalf("stable scan = %+v, err = %v, want one new file", second, err)
		}
	})

	t.Run("resume onto duplicate", func(t *testing.T) {
		ctx := t.Context()
		root := t.TempDir()
		dataDir := storetest.MigratedDataDir(t)
		st := storetest.OpenAt(t, dataDir)
		req := Request{LibraryID: "library-local", Root: root}
		payload := []byte("same bytes")
		firstPath := writeScanFile(t, root, "a.mp4", payload)
		initial, err := Scan(ctx, st, nil, nil, nil, nil, nil, req)
		if err != nil || len(initial.Files) != 1 {
			t.Fatalf("fixture scan = %+v, err = %v", initial, err)
		}
		copyPath := writeScanFile(t, root, "b.mp4", payload)
		mtime := time.Now().Add(-time.Minute)
		if err := os.Chtimes(firstPath, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		original := sampleScanFile
		t.Cleanup(func() { sampleScanFile = original })
		reads := 0
		sampleScanFile = func(ctx context.Context, path string, check store.SampleFingerprintCheck) (string, error) {
			if path == firstPath {
				reads++
				return "", readErr
			}
			return original(ctx, path, check)
		}
		started := time.Now()
		var checkpoint jobCheckpoint
		_, err = scanFromCheckpoint(ctx, st, nil, nil, nil, nil, nil, nil, nil, nil, nil, req, jobCheckpoint{}, started, func(cp jobCheckpoint, _ store.JobProgress) error {
			checkpoint = cp
			return jobs.ErrYielded
		})
		if !errors.Is(err, jobs.ErrYielded) || reads != 1 || checkpoint.LastPath != firstPath || checkpoint.FilesSeen != 1 || checkpoint.MediaImported != 1 {
			t.Fatalf("checkpoint = %+v, reads = %d, err = %v, want the unchanged file counted before yield", checkpoint, reads, err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		st = storetest.OpenAt(t, dataDir)
		resumed, err := scanFromCheckpoint(ctx, st, nil, nil, nil, nil, nil, nil, nil, nil, nil, req, checkpoint, started, nil)
		if err != nil || resumed.FilesSeen != 2 || resumed.MediaImported != 1 || len(resumed.Files) != 1 ||
			resumed.Files[0].Path != copyPath || resumed.Files[0].MediaID != initial.Files[0].MediaID {
			t.Fatalf("resumed scan = %+v, err = %v, want the stable copy with the same Media counted once", resumed, err)
		}
	})
}

type legacyProbeParser struct {
	titles []string
}

func (p *legacyProbeParser) Parse(_ string, probe ParseProbe) (ParsedMedia, bool) {
	title := "Legacy"
	if probe.Tags["title"] != "" {
		title = probe.Tags["title"]
	}
	p.titles = append(p.titles, title)
	return ParsedMedia{Title: title}, true
}

func TestScanStopsOnUnstableSampleLookupFailure(t *testing.T) {
	ctx := t.Context()
	root, dataDir := t.TempDir(), storetest.MigratedDataDir(t)
	st := storetest.OpenAt(t, dataDir)
	req := Request{LibraryID: "library-local", Root: root}
	path := writeScanFile(t, root, "a.mp4", []byte("legacy bytes"))
	next := writeScanFile(t, root, "b.mp4", []byte("new bytes"))
	digest, err := store.HashMediaFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	media, file, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: req.LibraryID, Path: path, Title: "Legacy", Container: "mp4", Codec: "h264", ContentFingerprint: digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stored, err := st.MediaFileFingerprintInLibrary(ctx, req.LibraryID, path); err != nil || !stored.OK || stored.ContentFingerprint != digest {
		t.Fatalf("legacy fixture = %+v, err = %v", stored, err)
	}
	db := storetest.OpenSQLiteDB(t)
	if _, err := db.ExecContext(ctx, `ATTACH DATABASE ? AS fixture`, filepath.Join(dataDir, "db", "catalog.db")); err != nil {
		t.Fatal(err)
	}
	setSize := func(size any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `UPDATE fixture.media_file SET size_bytes = ? WHERE path = ?`, size, path); err != nil {
			t.Fatal(err)
		}
	}
	original := sampleScanFile
	t.Cleanup(func() { sampleScanFile = original })
	const invalidSize = "injected lookup failure"
	var lookupErr error
	var reachedNext bool
	sampleScanFile = func(ctx context.Context, candidate string, check store.SampleFingerprintCheck) (string, error) {
		switch candidate {
		case path:
			if check == nil {
				t.Fatal("fixture did not reach the legacy comparison")
			}
			return original(ctx, candidate, func(_ io.Reader, sample string) (string, error) {
				setSize(invalidSize)
				_, lookupErr = st.MediaFileFingerprintInLibrary(ctx, req.LibraryID, path)
				return check(iotest.ErrReader(io.ErrUnexpectedEOF), sample)
			})
		case next:
			// A scan that incorrectly continues can finish its missing-file sweep.
			reachedNext = true
			setSize(file.SizeBytes)
		}
		return original(ctx, candidate, check)
	}
	cutoff := time.UnixMilli(file.LastSeenAt.UnixMilli() + 1)
	result, scanErr := scanFromCheckpoint(ctx, st, nil, nil, nil, nil, nil, nil, nil, nil, nil, req, jobCheckpoint{}, cutoff, nil)
	setSize(file.SizeBytes)
	if lookupErr == nil || !strings.Contains(lookupErr.Error(), invalidSize) {
		t.Fatalf("scoped lookup injection = %v", lookupErr)
	}
	if scanErr == nil || !strings.Contains(scanErr.Error(), lookupErr.Error()) || reachedNext {
		t.Fatalf("scan = %+v, error = %v, reached next file = %t, want scoped lookup failure", result, scanErr, reachedNext)
	}
	files, err := st.MediaFilesForMedia(ctx, media.ID, store.LibraryFilter{AllowedIDs: []store.LibraryID{req.LibraryID}})
	if err != nil || len(files) != 1 || files[0].ContentFingerprint != digest || files[0].LastSeenAt.UnixMilli() != file.LastSeenAt.UnixMilli() {
		t.Fatalf("legacy Media files after failed scan = %+v, err = %v", files, err)
	}
	sampleScanFile = original
	retry, err := Scan(ctx, st, nil, nil, nil, nil, nil, req)
	if err != nil || len(retry.Files) != 2 || retry.Files[0].MediaID != media.ID || retry.FilesMissing != 0 {
		t.Fatalf("stable retry = %+v, err = %v, want legacy Media %s and the new file", retry, err, media.ID)
	}
}

func TestScanTreatsFailedLegacyCompareAsUnchanged(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "full read error"
		if replace {
			name = "same-stat replacement during full read"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			root := t.TempDir()
			dataDir := storetest.MigratedDataDir(t)
			st := storetest.OpenAt(t, dataDir)
			library := partsLibrary(t, st)
			req := Request{LibraryID: library, Root: root}
			const originalBytes = "original"
			const replacementBytes = "replaced"
			path := writeScanFile(t, root, "legacy.mp4", []byte(originalBytes))
			digest, err := store.HashMediaFileContent(path)
			if err != nil {
				t.Fatal(err)
			}
			media, file, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: library, Path: path, Title: "Legacy", Container: "mp4", Codec: "h264", ContentFingerprint: digest,
			})
			if err != nil {
				t.Fatal(err)
			}
			mtime := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
			if replace {
				// The legacy digest differs from both the sampled inode and its
				// replacement. Accepting a sample/full mismatch must not admit it.
				if err := os.WriteFile(path, []byte("modified"), 0o600); err != nil {
					t.Fatal(err)
				}
				replacement := writeScanFile(t, root, ".replacement", []byte(replacementBytes))
				for _, name := range []string{path, replacement} {
					if err := os.Chtimes(name, mtime, mtime); err != nil {
						t.Fatal(err)
					}
					info, err := os.Stat(name)
					if err != nil || info.Size() != int64(len(originalBytes)) || !info.ModTime().Equal(mtime) {
						t.Fatalf("replacement fixture %q stat = %v, err = %v", name, info, err)
					}
				}
			}
			original := sampleScanFile
			t.Cleanup(func() { sampleScanFile = original })
			var reads int
			var readBytes bytes.Buffer
			var mutationErr, fingerprintErr error
			sampleScanFile = func(ctx context.Context, path string, check store.SampleFingerprintCheck) (string, error) {
				if check == nil {
					return original(ctx, path, nil)
				}
				fingerprint, err := original(ctx, path, func(file io.Reader, sample string) (string, error) {
					reads++
					if !replace {
						return check(iotest.ErrReader(io.ErrUnexpectedEOF), sample)
					}
					mutationErr = os.Rename(filepath.Join(root, ".replacement"), path)
					if mutationErr != nil {
						return "", mutationErr
					}
					return check(io.TeeReader(file, &readBytes), sample)
				})
				fingerprintErr = err
				return fingerprint, err
			}
			parser := &legacyProbeParser{}
			prober := &countingProber{probe: ffmpeg.Probe{Tags: map[string]string{"title": "Unverified title"}}}
			cutoff := time.UnixMilli(file.LastSeenAt.UnixMilli() + 1)
			first, err := scanFromCheckpoint(ctx, st, prober, nil, map[string]FilenameParser{"parts": parser}, nil, nil, nil, nil, nil, nil, req, jobCheckpoint{}, cutoff, nil)
			if reads != 1 || fingerprintErr == nil || mutationErr != nil || replace && readBytes.String() != "modified" {
				t.Fatalf("comparison injection: reads = %d, bytes = %q, mutation error = %v, fingerprint error = %v", reads, &readBytes, mutationErr, fingerprintErr)
			}
			if err != nil || first.FilesSeen != 1 || first.FilesMissing != 0 || len(first.Files) != 1 || first.Files[0].MediaID != media.ID {
				t.Fatalf("scan after failed compare = %+v, err = %v, want original Media %s", first, err, media.ID)
			}
			if prober.calls != 0 || len(parser.titles) == 0 || slices.Contains(parser.titles, "Unverified title") {
				t.Fatalf("ordinary unchanged flow probed %d times and parsed %v", prober.calls, parser.titles)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st = storetest.OpenAt(t, dataDir)
			persisted, err := st.MediaFileFingerprintByPath(ctx, path)
			if err != nil || !persisted.OK || persisted.ContentFingerprint != digest || persisted.SizeBytes != 0 || !persisted.ModTime.IsZero() {
				t.Fatalf("persisted identity = %+v, err = %v, want legacy fingerprint with unrecorded stats", persisted, err)
			}
			sampleScanFile = original
			second, err := Scan(ctx, st, nil, nil, nil, nil, nil, req)
			if err != nil || len(second.Files) != 1 {
				t.Fatalf("stable retry = %+v, err = %v", second, err)
			}
			if replace {
				want, err := store.SampleMediaFileContentChecked(ctx, path, nil)
				if err != nil || second.Files[0].MediaID == media.ID || second.Files[0].ContentFingerprint != want {
					t.Fatalf("stable replacement = %+v, want a new Media with fingerprint %q, err = %v", second.Files[0], want, err)
				}
			} else if second.Files[0].MediaID != media.ID || second.Files[0].ContentFingerprint != digest {
				t.Fatalf("stable retry = %+v, want original Media %s", second, media.ID)
			}
		})
	}
}

type scanCatalogCipher struct{ *libencrypt.Manager }

func (c scanCatalogCipher) UnsealDBFile(_ context.Context, library store.LibraryID, ciphertext, working string) error {
	return c.Manager.UnsealDBFile(string(library), ciphertext, working, libencrypt.LibraryDBObjectContext(string(library)))
}

func (c scanCatalogCipher) SealDBFile(_ context.Context, library store.LibraryID, working, ciphertext string) error {
	return c.Manager.SealDBFile(string(library), working, ciphertext, libencrypt.LibraryDBObjectContext(string(library)))
}

type scanSecrets map[string]string

func (s scanSecrets) Secret(ref string) (string, error) {
	if value, ok := s[ref]; ok {
		return value, nil
	}
	return "", os.ErrNotExist
}

func (s scanSecrets) SetSecret(ref, value string) error {
	if value == "" {
		delete(s, ref)
	} else {
		s[ref] = value
	}
	return nil
}

func TestScanTreatsEncryptedRenameOverAsUnchanged(t *testing.T) {
	ctx := t.Context()
	req := Request{LibraryID: "library-local", Root: t.TempDir()}
	dataDir := storetest.MigratedDataDir(t)
	open := func() *store.Store {
		t.Helper()
		st, err := store.Open(ctx, dataDir, store.Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	}
	st := open()
	if err := st.SetLibraryBacking(ctx, req.LibraryID, false, true); err != nil {
		t.Fatal(err)
	}
	cipher := scanCatalogCipher{libencrypt.NewManagerWithParams(scanSecrets{}, libencrypt.TestArgon2Params)}
	if _, err := cipher.Provision(string(req.LibraryID), libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, req.LibraryID, cipher); err != nil {
		t.Fatal(err)
	}
	path := writeScanFile(t, req.Root, "private.mp4", []byte("original"))
	initial, err := Scan(ctx, st, nil, nil, nil, nil, nil, req)
	if err != nil || len(initial.Files) != 1 || !strings.HasPrefix(initial.Files[0].ContentFingerprint, "sample1:") {
		t.Fatalf("encrypted fixture = %+v, err = %v", initial, err)
	}
	before, err := st.MediaFilesForLibrary(ctx, req.LibraryID)
	if err != nil || len(before) != 1 || before[0].Path != path || before[0].MediaID != initial.Files[0].MediaID {
		t.Fatalf("encrypted files = %+v, err = %v", before, err)
	}
	if public, err := st.MediaFileFingerprintByPath(ctx, path); err != nil || public.OK {
		t.Fatalf("encrypted fixture leaked into public catalog: %+v, err = %v", public, err)
	}
	replacement := writeScanFile(t, req.Root, ".replacement", []byte("replaced"))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	original := sampleScanFile
	t.Cleanup(func() { sampleScanFile = original })
	var injected bool
	var renameErr, sampleErr error
	sampleScanFile = func(ctx context.Context, name string, check store.SampleFingerprintCheck) (string, error) {
		fingerprint, err := store.SampleMediaFileContentFrom(ctx, name, func() (store.SampleFile, error) {
			file, err := os.OpenInRoot(req.Root, filepath.Base(name))
			if err != nil {
				return nil, err
			}
			return &changingSampleFile{File: file, beforeFirstRead: func() error {
				injected = true
				renameErr = os.Rename(replacement, name)
				return renameErr
			}}, nil
		}, check)
		sampleErr = err
		return fingerprint, err
	}
	cutoff := time.UnixMilli(before[0].LastSeenAt.UnixMilli() + 1)
	result, err := scanFromCheckpoint(ctx, st, nil, nil, nil, nil, nil, nil, nil, nil, nil, req, jobCheckpoint{}, cutoff, nil)
	if !injected || renameErr != nil || sampleErr == nil {
		t.Fatalf("rename injection reached = %t, rename error = %v, sample error = %v", injected, renameErr, sampleErr)
	}
	if err != nil || result.FilesSeen != 1 || result.FilesMissing != 0 || len(result.Files) != 1 || result.Files[0].MediaID != before[0].MediaID {
		t.Fatalf("unstable encrypted scan = %+v, err = %v, want unchanged row", result, err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, req.LibraryID, cipher); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = open()
	if err := st.UnsealEncryptedLibraryDB(ctx, req.LibraryID, cipher); err != nil {
		t.Fatal(err)
	}
	after, err := st.MediaFilesForLibrary(ctx, req.LibraryID)
	if err != nil || len(after) != 1 || after[0].MediaID != before[0].MediaID ||
		after[0].ContentFingerprint != before[0].ContentFingerprint || after[0].Path != path ||
		after[0].SizeBytes != before[0].SizeBytes || !after[0].ModTime.Equal(before[0].ModTime) {
		t.Fatalf("encrypted files after reopen = %+v, err = %v, before = %+v", after, err, before)
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
			savedSample := sampleScanFile
			t.Cleanup(func() { sampleScanFile = savedSample })
			sampleScanFile = func(ctx context.Context, path string, check store.SampleFingerprintCheck) (string, error) {
				sampleReads++
				if check != nil {
					fullReads++
				}
				return savedSample(ctx, path, check)
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
	savedSample := sampleScanFile
	savedNow := now
	t.Cleanup(func() { sampleScanFile, now = savedSample, savedNow })
	// Let newly admitted inodes settle without changing their actual mtimes.
	now = func() time.Time { return savedNow().Add(time.Minute) }
	sampleScanFile = func(ctx context.Context, path string, check store.SampleFingerprintCheck) (string, error) {
		sampleCalls[path]++
		if check != nil {
			legacyCalls[path]++
		}
		return savedSample(ctx, path, check)
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
	fingerprint, err := store.SampleMediaFileContentChecked(ctx, path, nil)
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
	original := sampleScanFile
	t.Cleanup(func() { sampleScanFile = original })
	sampleScanFile = func(ctx context.Context, path string, check store.SampleFingerprintCheck) (string, error) {
		if check != nil {
			t.Error("returning file was read in full")
		}
		return original(ctx, path, check)
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

func TestScanJobsSerializeFingerprintReads(t *testing.T) {
	// feature:scan-pipeline
	t.Run("sample", func(t *testing.T) { testScanJobsSerializeFingerprintReads(t, false) })
	t.Run("legacy", func(t *testing.T) { testScanJobsSerializeFingerprintReads(t, true) })
}

func testScanJobsSerializeFingerprintReads(t *testing.T, legacy bool) {
	t.Helper()
	now := time.Now().UTC()
	st := storetest.Open(t)
	firstRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secondRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	firstPath := writeScanFile(t, firstRoot, "first.mp4", []byte("first"))
	secondPath := writeScanFile(t, secondRoot, "second.mp4", []byte("second"))
	if legacy {
		for _, path := range []string{firstPath, secondPath} {
			fingerprint, err := store.HashMediaFileContent(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
				LibraryID: "library-local", Path: path, Title: filepath.Base(path),
				Container: "mp4", Codec: "h264", ContentFingerprint: fingerprint,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	enqueueHashTestScan(t, st, "first", firstRoot, now)
	enqueueHashTestScan(t, st, "second", secondRoot, now.Add(time.Millisecond))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	var runs sync.WaitGroup
	defer func() {
		cancel()
		runs.Wait()
	}()
	firstHash := make(chan struct{})
	firstReaderClosed := make(chan struct{})
	secondHash := make(chan struct{})
	secondWaiting := make(chan struct{})
	releaseFirst := make(chan struct{})
	originalHash, originalWait := sampleScanFile, onScanHashWait
	onScanHashWait = func(path string) {
		if path == secondPath {
			close(secondWaiting)
		}
	}
	sampleScanFile = func(hashCtx context.Context, path string, check store.SampleFingerprintCheck) (string, error) {
		if (check != nil) != legacy {
			t.Errorf("%s: legacy comparison = %t, want %t", path, check != nil, legacy)
		}
		waitForRelease := func() error {
			close(firstHash)
			select {
			case <-releaseFirst:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		switch path {
		case firstPath:
			defer close(firstReaderClosed)
			if check == nil {
				if err := waitForRelease(); err != nil {
					return "", err
				}
			} else {
				compare := check
				check = func(file io.Reader, sample string) (string, error) {
					if err := waitForRelease(); err != nil {
						return "", err
					}
					return compare(file, sample)
				}
			}
		case secondPath:
			select {
			case <-firstReaderClosed:
			default:
				t.Error("second job entered its hash before the first reader closed")
			}
			close(secondHash)
		}
		return originalHash(hashCtx, path, check)
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
	wait := func(ready <-chan struct{}, step string) {
		t.Helper()
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", step)
		}
	}
	firstDone := run()
	wait(firstHash, "first job to start fingerprinting")
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
	sampleScanFile = func(ctx context.Context, path string, check store.SampleFingerprintCheck) (string, error) {
		if path != pending {
			return original(ctx, path, check)
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

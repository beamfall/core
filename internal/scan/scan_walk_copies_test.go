package scan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestScanHealsVerifiedCopyAfterReopenAndUndo(t *testing.T) {
	t.Run("legacy first", func(t *testing.T) { testScanHealsVerifiedCopy(t, "first", "second") })
	t.Run("legacy last", func(t *testing.T) { testScanHealsVerifiedCopy(t, "second", "first") })
}

func testScanHealsVerifiedCopy(t *testing.T, legacyDir, sampleDir string) {
	ctx := t.Context()
	root, dir := t.TempDir(), storetest.MigratedDataDir(t)
	st, err := store.Open(ctx, dir, store.Options{SeedDevelopmentData: true, TrashDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	data := []byte("identical file bytes")
	legacyPath := writeScanFile(t, root, legacyDir+"/Truncated (Edition.mp4", data)
	samplePath := writeScanFile(t, root, sampleDir+"/Complete Title.mp4", data)
	const libraryID = "library-local"
	legacy, oldFile := seedLegacyCopy(t, st, libraryID, legacyPath)
	prober := fakeProber{probe: ffmpeg.Probe{Container: "mp4", AudioCodec: "aac", DurationSeconds: 145}}
	if _, err := Scan(ctx, st, prober, nil, nil, nil, nil, Request{LibraryID: libraryID, Root: root}); err != nil {
		t.Fatal(err)
	}
	sampleFile := copyAtPath(t, st, libraryID, samplePath)
	if !strings.HasPrefix(sampleFile.ContentFingerprint, "sample1:") || sampleFile.MediaID == legacy {
		t.Fatalf("sampled file with trash disabled = %+v", sampleFile)
	}

	const profile = "profile-default"
	eventTime := time.Now().Add(-time.Minute).UnixMilli()
	for _, progress := range []store.PlaybackProgress{
		{ProfileID: profile, MediaID: sampleFile.MediaID, PlayCount: 3, PositionSeconds: 31, DurationSeconds: 100, EventTimeMS: eventTime},
		{ProfileID: profile, MediaID: legacy, PlayCount: 2, PositionSeconds: 12, DurationSeconds: 100, EventTimeMS: eventTime + 1},
	} {
		if err := st.UpsertPlaybackProgress(ctx, progress, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st = storetest.OpenAt(t, dir)
	scanCopyRoot(t, st, libraryID, root)
	touched := oldFile.ModTime.Add(time.Minute)
	if err := os.Chtimes(legacyPath, touched, touched); err != nil {
		t.Fatal(err)
	}
	scanCopyRoot(t, st, libraryID, root)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = storetest.OpenAt(t, dir)

	filter := store.LibraryFilter{AllLibraries: true}
	media, err := st.Media(ctx, sampleFile.MediaID, filter)
	if err != nil || media.Title != "Complete Title" || media.Codec != "aac" ||
		media.DurationSeconds != 145 || media.Class != store.MediaClassAudio {
		t.Fatalf("survivor = %+v, err = %v", media, err)
	}
	if _, err := st.Media(ctx, legacy, filter); err == nil {
		t.Fatal("legacy Media is still visible")
	}
	files, err := st.MediaFilesForMedia(ctx, media.ID, filter)
	if err != nil || len(files) != 2 || files[0].ContentFingerprint != sampleFile.ContentFingerprint ||
		files[0].Path != samplePath || files[1].ContentFingerprint != oldFile.ContentFingerprint || files[1].Path != legacyPath ||
		files[0].VersionLabel != "" || files[1].VersionLabel == "" {
		t.Fatalf("reopened survivor files = %+v, err = %v", files, err)
	}
	clock, err := st.MediaFileFingerprintByPath(ctx, legacyPath)
	if err != nil || !clock.OK || !store.MediaFileModTimeMatches(clock.ModTime, touched) {
		t.Fatalf("legacy stat after touch = %+v, err = %v", clock, err)
	}
	assertCopyVersionsPlayable(t, st, media.ID, files)

	progress, ok, err := st.PlaybackProgressFor(ctx, profile, media.ID)
	if err != nil || !ok || progress.PlayCount != 5 || progress.PositionSeconds != 31 {
		t.Fatalf("merged playback progress = %+v, found = %v, err = %v", progress, ok, err)
	}
	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{Filter: filter})
	if err != nil {
		t.Fatal(err)
	}
	var batchID string
	for _, entry := range entries {
		if entry.Action == store.ActivityMediaMerge {
			if batchID != "" {
				t.Fatal("rescan recorded another merge")
			}
			batchID = entry.TargetID
		}
	}
	if batchID == "" {
		t.Fatal("scan did not journal a version merge")
	}
	if _, err := st.UndoBatch(ctx, batchID, filter, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = storetest.OpenAt(t, dir)
	scanCopyRoot(t, st, libraryID, root)
	if got := copyAtPath(t, st, libraryID, legacyPath); got.MediaID != legacy || got.VersionLabel != "" || got.ContentFingerprint != oldFile.ContentFingerprint {
		t.Fatalf("legacy file after undo and rescan = %+v", got)
	}
	if got := copyAtPath(t, st, libraryID, samplePath); got.MediaID != media.ID || got.VersionLabel != "" || got.ContentFingerprint != sampleFile.ContentFingerprint {
		t.Fatalf("sampled file after undo and rescan = %+v", got)
	}
	if _, err := st.Media(ctx, legacy, filter); err != nil {
		t.Fatalf("legacy Media after undo and rescan: %v", err)
	}
}

func TestScanVerifiedCopyCandidates(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		collision    bool
		otherLibrary bool
		wantMerge    bool
		replace      bool
	}{
		{name: "new copy", wantMerge: true},
		{name: "OSHash collision", collision: true},
		{name: "different Libraries", otherLibrary: true},
		{name: "statless replacement outside the walk", replace: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			st := storetest.Open(t)
			legacyRoot, sampleRoot := t.TempDir(), t.TempDir()
			legacyLibrary, sampleLibrary := store.LibraryID("library-local"), store.LibraryID("library-local")
			if scenario.otherLibrary {
				sampleLibrary = "library-archive"
			} else if !scenario.replace {
				sampleRoot = legacyRoot
			}

			data := bytes.Repeat([]byte{3}, oshashChunkSize*3)
			legacyPath := writeScanFile(t, legacyRoot, "first/original.mp4", data)
			legacy, oldFile := seedLegacyCopyWithStat(t, st, legacyLibrary, legacyPath, !scenario.replace)
			if scenario.collision {
				data[oshashChunkSize+1]++
			}
			samplePath := writeScanFile(t, sampleRoot, "second/copy.mp4", data)
			if hash, err := OSHash(samplePath); err != nil || hash != oldFile.OSHash {
				t.Fatalf("fixture OSHash = %q, err = %v, want %q", hash, err, oldFile.OSHash)
			}
			if scenario.replace {
				data[oshashChunkSize+1]++
				if err := os.WriteFile(legacyPath, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			scanCopyRoot(t, st, sampleLibrary, sampleRoot)
			if scenario.otherLibrary {
				scanCopyRoot(t, st, legacyLibrary, legacyRoot)
			}
			sample := copyAtPath(t, st, sampleLibrary, samplePath)
			if !strings.HasPrefix(sample.ContentFingerprint, "sample1:") || sample.MediaID == legacy {
				t.Fatalf("new file = %+v, legacy Media = %s", sample, legacy)
			}
			old := copyAtPath(t, st, legacyLibrary, legacyPath)
			wantOwner := legacy
			if scenario.wantMerge {
				wantOwner = sample.MediaID
			}
			if old.MediaID != wantOwner || old.ContentFingerprint != oldFile.ContentFingerprint {
				t.Fatalf("legacy file = %+v, want same file on %s", old, wantOwner)
			}
		})
	}
}

func TestScanVerifiedCopyPrecedesTitleMerge(t *testing.T) {
	for _, scenario := range []struct {
		name               string
		existing, failHash bool
	}{
		{name: "new split"},
		{name: "existing split", existing: true},
		{name: "transient hash failure", existing: true, failHash: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			st := storetest.Open(t)
			root := t.TempDir()
			library := createVersionLibrary(t, st)
			data := []byte("identical bytes")
			legacy := writeScanFile(t, root, "Same Title.mkv", data)
			sample := writeScanFile(t, root, "Same Title.mp4", data)
			legacyID, oldFile := seedLegacyCopy(t, st, library, legacy)
			var sampleID store.MediaID
			if scenario.existing {
				fingerprint, err := store.SampleMediaFileContentChecked(t.Context(), sample, nil)
				if err != nil {
					t.Fatal(err)
				}
				media, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
					LibraryID: library, Path: sample, Title: "Same Title", Container: "mp4", Codec: "h264",
					ContentFingerprint: fingerprint, SizeBytes: oldFile.SizeBytes, OSHash: oldFile.OSHash,
				})
				if err != nil {
					t.Fatal(err)
				}
				sampleID = media.ID
			}
			scan := func() {
				t.Helper()
				if _, err := Scan(t.Context(), st, nil, nil, map[string]FilenameParser{"titled": titledParser{title: "Same Title"}},
					nil, nil, Request{LibraryID: library, Root: root}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario.failHash {
				original := sampleScanFile
				t.Cleanup(func() { sampleScanFile = original })
				reached := false
				sampleScanFile = func(ctx context.Context, path string, check store.SampleFingerprintCheck) (string, error) {
					if path == sample && check != nil && !reached {
						return original(ctx, path, func(io.Reader, string) (string, error) {
							reached = true
							return "", io.ErrUnexpectedEOF
						})
					}
					return original(ctx, path, check)
				}
				for _, id := range []store.MediaID{sampleID, legacyID} {
					if err := st.ApplyImportedPlays(t.Context(), "profile-default", id, 2, time.Now(), store.EngagementProvenance{}); err != nil {
						t.Fatal(err)
					}
				}
				scan()
				if !reached || copyAtPath(t, st, library, sample).MediaID != sampleID || copyAtPath(t, st, library, legacy).MediaID != legacyID {
					t.Fatal("hash failure did not leave the possible copies separate")
				}
			}
			scan()
			sampled := copyAtPath(t, st, library, sample)
			folded := copyAtPath(t, st, library, legacy)
			if sampled.MediaID == legacyID || folded.MediaID != sampled.MediaID ||
				(scenario.existing && sampled.MediaID != sampleID) || sampled.VersionLabel != "" || folded.VersionLabel == "" {
				t.Fatalf("sampled = %+v, folded = %+v", sampled, folded)
			}
			if scenario.failHash {
				got, found, err := st.PlaybackProgressFor(t.Context(), "profile-default", sampleID)
				if err != nil || !found || got.PlayCount != 4 {
					t.Fatalf("progress after hash retry = %+v, found = %v, err = %v", got, found, err)
				}
			}
		})
	}
}

func TestScanVerifiedCopyKeepsSampledVersionFirstWithParsedLabel(t *testing.T) {
	ctx := t.Context()
	root, dir := t.TempDir(), storetest.MigratedDataDir(t)
	st := storetest.OpenAt(t, dir)
	library := createVersionLibrary(t, st)
	data := []byte("identical audio bytes")
	sample := writeScanFile(t, root, "Title (2026) - Remaster.mp4", data)
	legacy := writeScanFile(t, root, "Title (2026).mkv", data)
	legacyID, _ := seedLegacyCopy(t, st, library, legacy)
	prober := fakeProber{probe: ffmpeg.Probe{Container: "mp4", AudioCodec: "aac", DurationSeconds: 145}}
	if _, err := Scan(ctx, st, prober, nil, map[string]FilenameParser{"titled": labelledTitleParser{}},
		nil, nil, Request{LibraryID: library, Root: root}); err != nil {
		t.Fatal(err)
	}
	keep := copyAtPath(t, st, library, sample).MediaID
	if keep == legacyID || copyAtPath(t, st, library, legacy).MediaID != keep {
		t.Fatal("fixture did not fold into the sampled Media")
	}
	third := writeScanFile(t, root, "Title (2026) - Theatrical.mp4", []byte("other audio bytes"))
	rescanVersionLibrary(t, st, library, root)
	legacyFile := copyAtPath(t, st, library, legacy)
	touched := legacyFile.ModTime.Add(time.Minute)
	if err := os.Chtimes(legacy, touched, touched); err != nil {
		t.Fatal(err)
	}
	rescanVersionLibrary(t, st, library, root)
	writeScanFile(t, root, "Title (2026).mkv", []byte("replacement audio bytes"))
	rescanVersionLibrary(t, st, library, root)
	replacement := copyAtPath(t, st, library, legacy)
	if replacement.ContentFingerprint == legacyFile.ContentFingerprint || replacement.VersionLabel != legacyFile.VersionLabel {
		t.Fatalf("replacement = %+v, want new bytes in slot %q", replacement, legacyFile.VersionLabel)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = storetest.OpenAt(t, dir)
	filter := store.LibraryFilter{AllLibraries: true}
	files, err := st.MediaFilesForMedia(ctx, keep, filter)
	if err != nil || len(files) != 3 || files[0].Path != sample || files[0].VersionLabel != "" ||
		files[1].Path != legacy || files[2].Path != third || files[2].VersionLabel != "Theatrical" {
		t.Fatalf("reopened Versions = %+v, err = %v", files, err)
	}
	media, err := st.Media(ctx, keep, filter)
	if err != nil || media.Codec != "aac" || media.DurationSeconds != 145 {
		t.Fatalf("reopened probe facts = %+v, err = %v", media, err)
	}
}

func TestScanVerifiedCopySharesDefaultSlotWithUnlabelledSibling(t *testing.T) {
	for _, first := range []string{"Remaster", "unlabelled"} {
		t.Run(first+" first", func(t *testing.T) {
			st := storetest.Open(t)
			root := t.TempDir()
			library := createVersionLibrary(t, st)
			data := []byte("identical audio bytes")
			addSibling := func() string {
				return writeScanFile(t, root, "album/Title (2026).mp4", []byte("different audio bytes"))
			}
			var sibling string
			if first == "unlabelled" {
				sibling = addSibling()
				rescanVersionLibrary(t, st, library, root)
			}
			sample := writeScanFile(t, root, "album/Title (2026) - Remaster.mp4", data)
			rescanVersionLibrary(t, st, library, root)
			keep := copyAtPath(t, st, library, sample).MediaID
			legacy := writeScanFile(t, root, "other/Title (2026).mp4", data)
			seedLegacyCopy(t, st, library, legacy)
			rescanVersionLibrary(t, st, library, root)
			if copyAtPath(t, st, library, legacy).MediaID != keep {
				t.Fatal("fixture did not fold the legacy copy into the sampled Media")
			}
			if first == "Remaster" {
				sibling = addSibling()
				rescanVersionLibrary(t, st, library, root)
			}
			remaster, unlabelled, folded := copyAtPath(t, st, library, sample), copyAtPath(t, st, library, sibling), copyAtPath(t, st, library, legacy)
			if remaster.MediaID != keep || remaster.VersionLabel != "Remaster" || unlabelled.MediaID != keep ||
				unlabelled.VersionLabel != "" || folded.MediaID != keep || folded.VersionLabel == "" || folded.VersionLabel == "Remaster" {
				t.Fatalf("Remaster = %+v, unlabelled = %+v, legacy = %+v", remaster, unlabelled, folded)
			}
		})
	}
}

// A pair whose digests differ is distinct content, so the default title rule
// applies to it like any other same-title file.
func TestScanDigestMismatchedCopyFollowsTitleRule(t *testing.T) {
	st := storetest.Open(t)
	root := t.TempDir()
	library := createVersionLibrary(t, st)
	const first, second = "Title.2026.a.mkv", "Title.2026.b.mp4"
	data := bytes.Repeat([]byte{3}, oshashChunkSize*3)
	legacy := writeScanFile(t, root, first, data)
	legacyID, oldFile := seedLegacyCopy(t, st, library, legacy)
	if _, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
		LibraryID: library, Path: legacy, Title: "Title", Container: "matroska", Codec: "h264",
		ContentFingerprint: oldFile.ContentFingerprint, OSHash: oldFile.OSHash, SizeBytes: oldFile.SizeBytes, ModTime: oldFile.ModTime,
		VideoWidthHint: 1920, VideoHeightHint: 1080,
	}); err != nil {
		t.Fatal(err)
	}
	data[oshashChunkSize+1]++
	sample := writeScanFile(t, root, second, data)
	scanReleaseLibrary(t, st, library, root, frameProber{first: {1920, 1080}, second: {1920, 1080}})
	sampled, legacyFile := copyAtPath(t, st, library, sample), copyAtPath(t, st, library, legacy)
	if sampled.OSHash != oldFile.OSHash || !strings.HasPrefix(sampled.ContentFingerprint, "sample1:") {
		t.Fatalf("fixture did not produce a collision candidate: sample = %+v, legacy = %+v", sampled, oldFile)
	}
	if sampled.MediaID != legacyID || sampled.VersionLabel != "MP4" || legacyFile.ContentFingerprint != oldFile.ContentFingerprint {
		t.Fatalf("sample = %+v, legacy = %+v, want the distinct MP4 as a Version of %s", sampled, legacyFile, legacyID)
	}
}

// A legacy file replaced at the same size after the walk must not release the
// pair into title grouping on its stale catalog digest. The walk records the
// mtime of a legacy file seeded without one, so the copy check sees the change.
func TestScanLegacyCopyReplacedAfterWalkSitsOut(t *testing.T) {
	st := storetest.Open(t)
	root := t.TempDir()
	library := createVersionLibrary(t, st)
	const first, second = "Title.2026.a.mkv", "Title.2026.b.mp4"
	data := bytes.Repeat([]byte{4}, oshashChunkSize*3)
	legacy := writeScanFile(t, root, first, data)
	legacyID, oldFile := seedLegacyCopyWithStat(t, st, library, legacy, false)
	if _, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
		LibraryID: library, Path: legacy, Title: "Title", Container: "matroska", Codec: "h264",
		ContentFingerprint: oldFile.ContentFingerprint, OSHash: oldFile.OSHash, SizeBytes: oldFile.SizeBytes,
		VideoWidthHint: 1920, VideoHeightHint: 1080,
	}); err != nil {
		t.Fatal(err)
	}
	data[oshashChunkSize+1]++
	sample := writeScanFile(t, root, second, data)
	original := sampleScanFile
	t.Cleanup(func() { sampleScanFile = original })
	replaced := false
	sampleScanFile = func(ctx context.Context, path string, check store.SampleFingerprintCheck) (string, error) {
		if path == sample && check != nil && !replaced {
			replaced = true // the copy check is about to verify the sample
			writeScanFile(t, root, first, data)
		}
		return original(ctx, path, check)
	}
	scanReleaseLibrary(t, st, library, root, frameProber{first: {1920, 1080}, second: {1920, 1080}})
	sampled, legacyFile := copyAtPath(t, st, library, sample), copyAtPath(t, st, library, legacy)
	if !replaced || sampled.OSHash != oldFile.OSHash || !strings.HasPrefix(sampled.ContentFingerprint, "sample1:") {
		t.Fatalf("fixture did not produce a collision candidate: sample = %+v, legacy = %+v", sampled, oldFile)
	}
	if sampled.MediaID == legacyID || legacyFile.MediaID != legacyID {
		t.Fatalf("sample = %+v, legacy = %+v: a stale legacy digest released the pair into title grouping", sampled, legacyFile)
	}
}

func TestScanCopyCollisionDoesNotExcludeDistinctTitleSibling(t *testing.T) {
	st := storetest.Open(t)
	ctx, root := t.Context(), t.TempDir()
	library := createVersionLibrary(t, st)
	data := bytes.Repeat([]byte{3}, oshashChunkSize*3)
	legacy := writeScanFile(t, root, "other/Title (2026).mp4", data)
	legacyID, oldFile := seedLegacyCopy(t, st, library, legacy)
	data[oshashChunkSize+1]++
	sample := writeScanFile(t, root, "album/Title (2026).mp4", data)
	sibling := writeScanFile(t, root, "album/Title (2026).mkv", []byte("different rendition"))
	for _, path := range []string{sample, sibling} {
		fingerprint, err := store.SampleMediaFileContentChecked(ctx, path, nil)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		oshash, err := OSHash(path)
		if err != nil || (path == sample && oshash != oldFile.OSHash) {
			t.Fatalf("fixture OSHash for %s = %q, err = %v", path, oshash, err)
		}
		if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library, Path: path, Title: "Title", Container: "mp4", Codec: "h264", ContentFingerprint: fingerprint,
			OSHash: oshash, SizeBytes: info.Size(), ModTime: info.ModTime(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	sampleID := copyAtPath(t, st, library, sample).MediaID
	if copyAtPath(t, st, library, sibling).MediaID == sampleID {
		t.Fatal("fixture already grouped the title siblings")
	}
	for range 2 {
		rescanVersionLibrary(t, st, library, root)
		if copyAtPath(t, st, library, sample).MediaID != sampleID ||
			copyAtPath(t, st, library, sibling).MediaID != sampleID || copyAtPath(t, st, library, legacy).MediaID != legacyID {
			t.Fatal("the title siblings should group and the digest-mismatched pair should stay separate")
		}
	}
}

func TestScanGroupsKnownResolutionsAfterUnknownResolution(t *testing.T) {
	st := storetest.Open(t)
	library, root := createVersionLibrary(t, st), t.TempDir()
	const first, second, third = "Title.2026.a.mkv", "Title.2026.b.mkv", "Title.2026.c.mkv"
	unknown := writeScanFile(t, root, first, []byte("unknown resolution"))
	low := writeScanFile(t, root, second, []byte("720p rendition"))
	high := writeScanFile(t, root, third, []byte("1080p rendition"))
	prober := frameProber{second: {1280, 720}, third: {1920, 1080}}
	scanReleaseLibrary(t, st, library, root, prober)
	firstFile, lowFile, highFile := copyAtPath(t, st, library, unknown), copyAtPath(t, st, library, low), copyAtPath(t, st, library, high)
	if lowFile.MediaID != highFile.MediaID || firstFile.MediaID == lowFile.MediaID ||
		lowFile.VersionLabel != "720p" || highFile.VersionLabel != "1080p" {
		t.Fatalf("unknown = %+v, 720p = %+v, 1080p = %+v, want the known resolutions grouped separately", firstFile, lowFile, highFile)
	}
}

func TestScanTitleMergeCallBound(t *testing.T) {
	st := storetest.Open(t)
	library, root := createVersionLibrary(t, st), t.TempDir()
	const count = 100
	for i := range count {
		writeScanFile(t, root, fmt.Sprintf("Title.2026.%03d.mkv", i), fmt.Appendf(nil, "distinct bytes %03d", i))
	}
	prober := fakeProber{probe: ffmpeg.Probe{Container: "matroska", VideoCodec: "h264", HasVideo: true, VideoWidth: 1920, VideoHeight: 1080}}
	result, err := Scan(t.Context(), st, prober, nil, nil, nil, nil, Request{LibraryID: library, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]store.MediaID, 0, count)
	for _, file := range result.Files {
		if slices.Contains(ids, file.MediaID) {
			t.Fatalf("fixture grouped Media %s", file.MediaID)
		}
		ids = append(ids, file.MediaID)
	}
	if len(ids) != count {
		t.Fatalf("fixture has %d Media, want %d", len(ids), count)
	}
	original := mergeScanSplitVersions
	t.Cleanup(func() { mergeScanSplitVersions = original })
	calls := 0
	mergeScanSplitVersions = func(st *store.Store, ctx context.Context, library store.LibraryID, ids []store.MediaID, now time.Time, opts ...store.VersionMergeOptions) (store.VersionMerge, error) {
		calls++
		if calls >= count {
			t.Fatalf("merge calls reached %d, want at most %d", calls, count-1)
		}
		return original(st, ctx, library, ids, now, opts...)
	}
	state := scanWalkState{st: st, ctx: t.Context(), req: Request{LibraryID: library}}
	state.mergeTitleVersions(slices.Clone(ids), nil)
	if calls == 0 {
		t.Fatal("fixture never called MergeSplitVersions")
	}
}

func TestScanTitleMergeSkipsUnverifiedCopies(t *testing.T) {
	ids := []store.MediaID{"media-a", "media-b", "media-c"}
	original := mergeScanSplitVersions
	t.Cleanup(func() { mergeScanSplitVersions = original })
	var groups [][]store.MediaID
	mergeScanSplitVersions = func(_ *store.Store, _ context.Context, _ store.LibraryID, group []store.MediaID, _ time.Time, _ ...store.VersionMergeOptions) (store.VersionMerge, error) {
		groups = append(groups, slices.Clone(group))
		return store.VersionMerge{Survivor: group[0]}, nil
	}
	state := scanWalkState{ctx: t.Context()}
	state.mergeTitleVersions(slices.Clone(ids), unverifiedCopies{ids[0]: true})
	if len(groups) != 1 || !slices.Equal(groups[0], ids[1:]) {
		t.Fatalf("merge groups = %v, want one batch of %v", groups, ids[1:])
	}
}

func TestScanVerifiedCopyKeepsMissingVersionReservation(t *testing.T) {
	st := storetest.Open(t)
	root := t.TempDir()
	const libraryID = "library-local"
	data := []byte("identical bytes")
	legacy := writeScanFile(t, root, "first/main.mp4", data)
	legacyID, _ := seedLegacyCopy(t, st, libraryID, legacy)
	alternateData := []byte("distinct bytes")
	alternate := writeScanFile(t, root, "first/alternate.mp4", alternateData)
	fingerprint, err := store.HashMediaFileContent(alternate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportMediaFileVersion(t.Context(), store.MediaVersionImport{MediaID: legacyID, Label: "Alternate",
		File: store.ImportMediaFile{LibraryID: libraryID, Path: alternate, ContentFingerprint: fingerprint,
			Title: "Alternate", Container: "mp4", Codec: "h264"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alternate); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkMediaFileMissing(t.Context(), libraryID, alternate); err != nil {
		t.Fatal(err)
	}
	replacement := writeScanFile(t, root, "first/alternate.mp4", []byte("replaced bytes"))
	replacementDigest, err := store.HashMediaFileContent(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportMediaFileVersion(t.Context(), store.MediaVersionImport{MediaID: legacyID, Label: "Replacement",
		File: store.ImportMediaFile{LibraryID: libraryID, Path: replacement, ContentFingerprint: replacementDigest,
			Title: "Replacement", Container: "mp4", Codec: "h264"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(replacement); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkMediaFileMissing(t.Context(), libraryID, replacement); err != nil {
		t.Fatal(err)
	}
	sample := writeScanFile(t, root, "second/copy.mp4", data)
	scanCopyRoot(t, st, libraryID, root)
	survivor := copyAtPath(t, st, libraryID, sample).MediaID
	if survivor == legacyID || copyAtPath(t, st, libraryID, legacy).MediaID != survivor {
		t.Fatal("fixture did not merge")
	}
	if purged, err := st.PurgeTrashedBefore(t.Context(), time.Now().Add(time.Hour)); err != nil || purged != 1 {
		t.Fatalf("purge of loser = %d, err = %v", purged, err)
	}
	writeScanFile(t, root, "first/alternate.mp4", alternateData)
	scanCopyRoot(t, st, libraryID, root)
	got := copyAtPath(t, st, libraryID, alternate)
	if got.MediaID != survivor || got.ContentFingerprint != fingerprint || got.VersionLabel != "Alternate" {
		t.Fatalf("returning Version = %+v", got)
	}
}

type cancelOnDoneContext struct {
	context.Context
	cancel context.CancelFunc
}

func (ctx cancelOnDoneContext) Done() <-chan struct{} {
	ctx.cancel()
	return ctx.Context.Done()
}

func TestScanTitleMergeCancellationCanResume(t *testing.T) {
	st := storetest.Open(t)
	root := t.TempDir()
	library := createVersionLibrary(t, st)
	const first = "Title.2026.1080p.mkv"
	const second = "Title.2026.2160p.mp4"
	firstPath := writeScanFile(t, root, first, []byte("first file"))
	secondPath := writeScanFile(t, root, second, []byte("other file"))
	prober := frameProber{first: {1920, 800}, second: {3840, 1600}}
	req := Request{LibraryID: library, Root: root}
	if _, err := Scan(t.Context(), st, prober, nil, nil, nil, nil, req); err != nil {
		t.Fatal(err)
	}
	if copyAtPath(t, st, library, firstPath).MediaID == copyAtPath(t, st, library, secondPath).MediaID {
		t.Fatal("fixture should start with two Media")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	original := mergeScanSplitVersions
	t.Cleanup(func() { mergeScanSplitVersions = original })
	reached := false
	mergeScanSplitVersions = func(st *store.Store, ctx context.Context, library store.LibraryID, ids []store.MediaID, now time.Time, opts ...store.VersionMergeOptions) (store.VersionMerge, error) {
		reached = true
		return original(st, cancelOnDoneContext{Context: ctx, cancel: cancel}, library, ids, now, opts...)
	}
	parsers := map[string]FilenameParser{"titled": titledParser{title: "Title"}}
	start := time.Now()
	result, err := scanFromCheckpoint(ctx, st, nil, nil, parsers, nil, nil, nil, nil, nil, nil,
		req, jobCheckpoint{}, start, nil)
	if !reached || !errors.Is(err, context.Canceled) || result.interrupted == nil ||
		!slices.Contains(result.interrupted.SplitVersionDirs, root) {
		t.Fatalf("merge reached = %v, checkpoint = %+v, err = %v", reached, result.interrupted, err)
	}
	mergeScanSplitVersions = original
	if _, err := scanFromCheckpoint(t.Context(), st, nil, nil, parsers, nil, nil, nil, nil, nil, nil,
		req, *result.interrupted, start, nil); err != nil {
		t.Fatal(err)
	}
	if copyAtPath(t, st, library, firstPath).MediaID != copyAtPath(t, st, library, secondPath).MediaID {
		t.Fatal("resumed scan did not retry the title merge")
	}
}

func TestScanVerifiedCopyHashCancellationCanResume(t *testing.T) {
	st := storetest.Open(t)
	root := t.TempDir()
	const libraryID = "library-local"
	data := []byte("identical bytes")
	legacy := writeScanFile(t, root, "first/legacy.mp4", data)
	seedLegacyCopy(t, st, libraryID, legacy)
	sample := writeScanFile(t, root, "second/sample.mp4", data)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	original := sampleScanFile
	t.Cleanup(func() { sampleScanFile = original })
	reached := false
	sampleScanFile = func(ctx context.Context, path string, check store.SampleFingerprintCheck) (string, error) {
		if path == sample && check != nil && !reached {
			return original(ctx, path, func(reader io.Reader, sample string) (string, error) {
				reached = true
				cancel()
				return check(reader, sample)
			})
		}
		return original(ctx, path, check)
	}
	req := Request{LibraryID: libraryID, Root: root}
	start := time.Now()
	result, err := scanFromCheckpoint(ctx, st, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		req, jobCheckpoint{}, start, nil)
	if !reached || !errors.Is(err, context.Canceled) || result.interrupted == nil || result.interrupted.LastPath != sample {
		t.Fatalf("hash reached = %v, checkpoint = %+v, err = %v", reached, result.interrupted, err)
	}
	if _, err := scanFromCheckpoint(t.Context(), st, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		req, *result.interrupted, start, nil); err != nil {
		t.Fatal(err)
	}
	if copyAtPath(t, st, libraryID, legacy).MediaID != copyAtPath(t, st, libraryID, sample).MediaID {
		t.Fatal("resumed scan did not merge the pair")
	}
}

func seedLegacyCopy(t *testing.T, st *store.Store, libraryID store.LibraryID, path string) (store.MediaID, store.MediaFile) {
	t.Helper()
	return seedLegacyCopyWithStat(t, st, libraryID, path, true)
}

func seedLegacyCopyWithStat(t *testing.T, st *store.Store, libraryID store.LibraryID, path string, recordMtime bool) (store.MediaID, store.MediaFile) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := store.HashMediaFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	oshash, err := OSHash(path)
	if err != nil {
		t.Fatal(err)
	}
	mtime := time.Time{}
	if recordMtime {
		mtime = info.ModTime()
	}
	media, file, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
		LibraryID: libraryID, Path: path, Title: "Truncated (Edition", Container: "mp4", Codec: "hevc",
		ContentFingerprint: digest, OSHash: oshash, ModTime: mtime, SizeBytes: info.Size(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return media.ID, file
}

func scanCopyRoot(t *testing.T, st *store.Store, id store.LibraryID, root string) {
	t.Helper()
	if _, err := Scan(t.Context(), st, nil, nil, nil, nil, nil, Request{LibraryID: id, Root: root}); err != nil {
		t.Fatal(err)
	}
}

func copyAtPath(t *testing.T, st *store.Store, id store.LibraryID, path string) store.MediaFile {
	t.Helper()
	files, err := st.MediaFilesForLibrary(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Path == path {
			return file
		}
	}
	t.Fatalf("no live file at %s in %s", path, id)
	return store.MediaFile{}
}

func assertCopyVersionsPlayable(t *testing.T, st *store.Store, id store.MediaID, files []store.MediaFile) {
	t.Helper()
	ctx := t.Context()
	scope, err := auth.NewService(st).Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	tokens := streamtoken.NewService([]byte("version-copy-test"), time.Now)
	service := playback.NewService(st, tokens, time.Now)
	req := playback.InfoRequest{MediaID: string(id), CapabilityProfile: playback.CapabilityProfile{
		Client: "beamfall-test", Containers: []string{"mp4"}, DirectCodecs: []string{"aac", "hevc"},
	}}
	info, err := service.Info(ctx, scope, req)
	if err != nil || len(info.Versions) != len(files) {
		t.Fatalf("playback Versions = %+v, err = %v", info, err)
	}
	for _, version := range info.Versions {
		req.SelectedVersion = version.ID
		selected, err := service.Info(ctx, scope, req)
		if err != nil || selected.Decision != "direct-play" || len(selected.Streams) == 0 {
			t.Fatalf("playback of %q = %+v, err = %v", version.Label, selected, err)
		}
		grant, err := tokens.Verify(selected.Streams[0].Token, streamtoken.TargetMediaDirect, scope)
		if err != nil {
			t.Fatal(err)
		}
		fingerprint := grant.FileFingerprint
		if fingerprint == "" {
			fingerprint = files[0].ContentFingerprint
		}
		if !slices.ContainsFunc(files, func(file store.MediaFile) bool {
			return file.VersionLabel == version.Label && file.ContentFingerprint == fingerprint
		}) {
			t.Fatalf("Version %q did not bind to its file: %+v", version.Label, grant)
		}
	}
}

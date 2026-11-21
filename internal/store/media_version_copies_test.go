package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMergeVerifiedCopyRestoresAllVersions(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	const library = "library-local"
	root := t.TempDir()
	importFile := func(name, fingerprint, oshash string, width int) ImportMediaFile {
		return ImportMediaFile{
			LibraryID: library, Path: filepath.Join(root, name), Title: name, ContentFingerprint: fingerprint,
			Container: "mp4", Codec: "h264", OSHash: oshash, SizeBytes: 8, ModTime: time.Now(),
			VideoWidthHint: width, VideoHeightHint: 800,
		}
	}
	legacy, _, err := st.ImportMediaFile(ctx, importFile("old.mp4", "sha256:old", "same", 1280))
	if err != nil {
		t.Fatal(err)
	}
	sample, sampleFile, err := st.ImportMediaFile(ctx, importFile("new.mp4", "sample1:new", "same", 1920))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportMediaFileVersion(ctx, MediaVersionImport{
		MediaID: legacy.ID, File: importFile("alternate.mp4", "sha256:alternate", "different", 1280), Label: "2",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportMediaFileVersion(ctx, MediaVersionImport{
		MediaID: sample.ID, File: importFile("other.mp4", "sample1:other", "another", 1920), Label: "2",
		AnchorFingerprint: sampleFile.ContentFingerprint, AnchorLabel: "1080p",
	}); err != nil {
		t.Fatal(err)
	}
	pairs, err := st.VersionCopyCandidates(ctx, library)
	if err != nil || len(pairs) != 1 {
		t.Fatalf("copy candidates = %+v, err = %v", pairs, err)
	}
	ids := []MediaID{legacy.ID, sample.ID}
	stale := pairs[0]
	stale.Sample.Path += ".stale"
	result, err := st.MergeSplitVersions(ctx, library, ids, time.Now(), VersionMergeOptions{Survivor: sample.ID, VerifiedCopy: &stale})
	if err != nil || result.BatchID != "" {
		t.Fatalf("merge with a stale file path = %+v, err = %v", result, err)
	}
	result, err = st.MergeSplitVersions(ctx, library, ids, time.Now(), VersionMergeOptions{Survivor: sample.ID, VerifiedCopy: &pairs[0]})
	if err != nil || result.BatchID == "" || result.Survivor != sample.ID {
		t.Fatalf("merge = %+v, err = %v, want survivor %s", result, err, sample.ID)
	}
	filter := LibraryFilter{AllLibraries: true}
	files, err := st.MediaFilesForMedia(ctx, sample.ID, filter)
	if err != nil || len(files) != 4 || files[0].ContentFingerprint != sampleFile.ContentFingerprint {
		t.Fatalf("survivor versions = %+v, err = %v", files, err)
	}
	got, err := st.Media(ctx, sample.ID, filter)
	if err != nil || got.VideoWidth != 1920 {
		t.Fatalf("survivor probe width = %d, err = %v", got.VideoWidth, err)
	}
	if _, err := st.UndoBatch(ctx, result.BatchID, filter, time.Now()); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[MediaID]map[string]string{
		legacy.ID: {"old.mp4": "", "alternate.mp4": "2"},
		sample.ID: {"new.mp4": "1080p", "other.mp4": "2"},
	} {
		if got := versionLabels(t, st, id); !equalLabels(got, want) {
			t.Fatalf("restored %s Versions = %v, want %v", id, got, want)
		}
	}
	if pairs, err := st.VersionCopyCandidates(ctx, library); err != nil || len(pairs) != 0 {
		t.Fatalf("candidates after undo = %+v, err = %v", pairs, err)
	}
}

func TestVerifiedCopyProgressSurvivesRetryAndProjectionRebuild(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	const survivor, legacy, profile = "media-alpha", "media-beta", "profile-default"
	write := func(media MediaID, position, plays int, updated, played int64) {
		t.Helper()
		event := playbackProjectionEvent{
			Operation: playbackProjectionOperationProgress, MediaID: string(media), ProfileID: profile,
			PositionSeconds: position, DurationSeconds: 100, PlayCount: plays, ExplicitCount: plays,
			UpdatedAtMS: updated, LastPlayedAtMS: &played,
		}
		tx, err := st.eventsW.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := appendPlaybackProjectionEvent(ctx, tx, event); err != nil {
			t.Fatal(err)
		}
		if err := applyPlaybackProjectionEvent(ctx, tx, event); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	copyProgress := func() {
		t.Helper()
		tx, err := st.eventsW.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		events, err := versionMergeProgressEventsTx(ctx, tx, legacy, survivor, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if err := recordVersionMergeProgressTx(ctx, tx, event); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	check := func(wantPlays int) {
		t.Helper()
		var position, plays int
		var lastPlayed int64
		err := st.eventsR.QueryRowContext(ctx, `
			SELECT position_seconds, play_count, last_played_at_ms FROM playback_progress
			WHERE media_id = ? AND profile_id = ?
		`, survivor, profile).Scan(&position, &plays, &lastPlayed)
		if err != nil || position != 50 || plays != wantPlays || lastPlayed != 1900 {
			t.Fatalf("position = %d, plays = %d, last played = %d, err = %v", position, plays, lastPlayed, err)
		}
	}
	write(survivor, 50, 2, 2000, 1900)
	write(legacy, 12, 3, 3000, 1700)
	copyProgress()
	check(5)
	copyProgress()
	check(5)
	write(legacy, 12, 4, 1800, 1800)
	copyProgress()
	check(6)
	if err := st.rebuildEngagementProjections(ctx); err != nil {
		t.Fatal(err)
	}
	check(6)
}

func TestVersionCopyCandidatesSkipMultipartMedia(t *testing.T) {
	for _, prefix := range []string{"sample1:", "sha256:"} {
		t.Run(prefix, func(t *testing.T) {
			st := openSeeded(t)
			ctx := t.Context()
			root := t.TempDir()
			const library = "library-local"
			sample := ImportMediaFile{
				LibraryID: library, Path: filepath.Join(root, "sample.mp4"), Title: "Sample",
				Container: "mp4", Codec: "h264", ContentFingerprint: "sample1:sample", OSHash: "same", SizeBytes: 8,
			}
			legacy := sample
			legacy.Path, legacy.Title, legacy.ContentFingerprint = filepath.Join(root, "legacy.mp4"), "Legacy", "sha256:legacy"
			multi, single := sample, legacy
			if prefix == "sha256:" {
				multi, single = legacy, sample
			}
			tail := multi
			tail.Path, tail.ContentFingerprint, tail.OSHash = filepath.Join(root, "tail.mp4"), prefix+"tail", "another"
			_, parts, err := st.ImportMediaFileParts(ctx, []ImportMediaFilePart{
				{File: multi, SourceOrdinal: 1}, {File: tail, SourceOrdinal: 2},
			})
			if err != nil || len(parts) != 2 || parts[1].PartOrdinal != 1 {
				t.Fatalf("multipart fixture = %+v, err = %v", parts, err)
			}
			if _, _, err := st.ImportMediaFile(ctx, single); err != nil {
				t.Fatal(err)
			}
			if pairs, err := st.VersionCopyCandidates(ctx, library); err != nil || len(pairs) != 0 {
				t.Fatalf("multipart copy candidates = %+v, err = %v", pairs, err)
			}
		})
	}
}

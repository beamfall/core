package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPlaybackRenditionsAreVisibleCacheObjectsNotMediaFiles(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	cache := filepath.Join(dir, "cache", "rendition-720p")
	imported := ImportMediaFile{
		LibraryID:           "library-local",
		Path:                source,
		Title:               "Cached Item",
		Container:           "mp4",
		Codec:               "hevc",
		ContentFingerprint:  "sha256:playback-cache-source",
		DurationSecondsHint: 120,
		VideoWidthHint:      3840,
		VideoHeightHint:     2160,
		Streams: []MediaFileStream{
			{Index: 0, Type: "video", Codec: "hevc", Profile: "main10", ColorTransfer: "smpte2084"},
		},
	}
	media, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	rendition := PlaybackRendition{
		ID:                 "rendition-720p",
		MediaID:            media.ID,
		ContentFingerprint: "sha256:playback-cache-source",
		Kind:               PlaybackRenditionKindHLS,
		Status:             PlaybackRenditionReady,
		RungID:             "720p",
		Codec:              "h264",
		Height:             720,
		Bandwidth:          4_256_000,
		Path:               cache,
		SizeBytes:          1024,
		CreatedAt:          time.Unix(200, 0).UTC(),
		LastUsedAt:         time.Unix(201, 0).UTC(),
	}
	if err := st.UpsertPlaybackRendition(ctx, rendition); err != nil {
		t.Fatalf("UpsertPlaybackRendition: %v", err)
	}

	got, err := st.ReadyPlaybackRenditions(ctx, media.ID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("ReadyPlaybackRenditions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ReadyPlaybackRenditions len = %d, want 1", len(got))
	}
	if got[0].ID != rendition.ID || got[0].Path != cache || got[0].Height != 720 {
		t.Fatalf("rendition = %+v, want cached metadata", got[0])
	}

	files, err := st.MediaFilesForMedia(ctx, media.ID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("MediaFilesForMedia: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("MediaFilesForMedia len = %d, want source file only", len(files))
	}
	if files[0].Path == cache {
		t.Fatalf("precomputed cache path appeared as a user-owned media file: %+v", files[0])
	}
}

func TestPlaybackRenditionsRespectRevealFilterAndStatus(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Unix(300, 0).UTC()
	if err := st.UpsertPlaybackRendition(ctx, PlaybackRendition{
		ID:                 "restricted-ready",
		MediaID:            "media-archive",
		ContentFingerprint: "sha256:restricted",
		Kind:               PlaybackRenditionKindHLS,
		Status:             PlaybackRenditionReady,
		RungID:             "480p",
		Codec:              "h264",
		Height:             480,
		Bandwidth:          1_756_000,
		Path:               filepath.Join(t.TempDir(), "restricted"),
		SizeBytes:          100,
		CreatedAt:          now,
		LastUsedAt:         now,
	}); err != nil {
		t.Fatalf("Upsert restricted: %v", err)
	}
	if err := st.UpsertPlaybackRendition(ctx, PlaybackRendition{
		ID:                 "local-failed",
		MediaID:            "media-alpha",
		ContentFingerprint: "sha256:failed",
		Kind:               PlaybackRenditionKindHLS,
		Status:             PlaybackRenditionFailed,
		RungID:             "480p",
		Codec:              "h264",
		Height:             480,
		Bandwidth:          1_756_000,
		Path:               filepath.Join(t.TempDir(), "failed"),
		SizeBytes:          100,
		CreatedAt:          now,
		LastUsedAt:         now,
	}); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	hidden, err := st.ReadyPlaybackRenditions(ctx, "media-archive", LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("hidden ReadyPlaybackRenditions: %v", err)
	}
	if len(hidden) != 0 {
		t.Fatalf("hidden restricted renditions = %+v, want none", hidden)
	}
	failed, err := st.ReadyPlaybackRenditions(ctx, "media-alpha", LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("failed ReadyPlaybackRenditions: %v", err)
	}
	if len(failed) != 0 {
		t.Fatalf("failed renditions = %+v, want none", failed)
	}
}

func TestEvictPlaybackRenditionsOverBudgetMarksOldestDisposableObjects(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	base := time.Unix(400, 0).UTC()
	for i, id := range []string{"old", "middle", "new"} {
		if err := st.UpsertPlaybackRendition(ctx, PlaybackRendition{
			ID:                 PlaybackRenditionID(id),
			MediaID:            "media-alpha",
			ContentFingerprint: "sha256:budget",
			Kind:               PlaybackRenditionKindHLS,
			Status:             PlaybackRenditionReady,
			RungID:             "480p",
			Codec:              "h264",
			Height:             480,
			Bandwidth:          1_756_000,
			Path:               filepath.Join(t.TempDir(), id),
			SizeBytes:          100,
			CreatedAt:          base.Add(time.Duration(i) * time.Second),
			LastUsedAt:         base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("Upsert %s: %v", id, err)
		}
	}

	evicted, err := st.EvictPlaybackRenditionsOverBudget(ctx, 150, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("EvictPlaybackRenditionsOverBudget: %v", err)
	}
	if len(evicted) != 2 || evicted[0].ID != "old" || evicted[1].ID != "middle" {
		t.Fatalf("evicted = %+v, want old then middle", evicted)
	}
	ready, err := st.ReadyPlaybackRenditions(ctx, "media-alpha", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("ReadyPlaybackRenditions after eviction: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != "new" {
		t.Fatalf("ready after eviction = %+v, want only new", ready)
	}
}

func TestPlaybackRenditionCacheHealthReportsCountsAndBytes(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	base := time.Unix(900, 0).UTC()
	rows := []struct {
		id     string
		status PlaybackRenditionStatus
		bytes  int64
	}{
		{"ready-a", PlaybackRenditionReady, 100},
		{"ready-b", PlaybackRenditionReady, 250},
		{"generating", PlaybackRenditionGenerating, 0},
		{"failed", PlaybackRenditionFailed, 0},
		{"evicted", PlaybackRenditionEvicted, 0},
	}
	for _, r := range rows {
		if err := st.UpsertPlaybackRendition(ctx, PlaybackRendition{
			ID:                 PlaybackRenditionID(r.id),
			MediaID:            "media-alpha",
			ContentFingerprint: "sha256:" + r.id,
			Kind:               PlaybackRenditionKindHLS,
			Status:             r.status,
			RungID:             "480p",
			Codec:              "h264",
			Height:             480,
			Bandwidth:          1_756_000,
			Path:               filepath.Join(t.TempDir(), r.id),
			SizeBytes:          r.bytes,
			CreatedAt:          base,
			LastUsedAt:         base,
		}); err != nil {
			t.Fatalf("Upsert %s: %v", r.id, err)
		}
	}

	stats, err := st.PlaybackRenditionCacheHealth(ctx)
	if err != nil {
		t.Fatalf("PlaybackRenditionCacheHealth: %v", err)
	}
	if stats.Ready != 2 || stats.ReadyBytes != 350 {
		t.Fatalf("ready = %d (%d bytes), want 2 (350 bytes)", stats.Ready, stats.ReadyBytes)
	}
	if stats.Generating != 1 || stats.Failed != 1 || stats.Evicted != 1 {
		t.Fatalf("stats = %+v, want generating/failed/evicted all 1", stats)
	}
}

func TestPlaybackRenditionValidation(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	err := st.UpsertPlaybackRendition(ctx, PlaybackRendition{
		ID:                 "bad-status",
		MediaID:            "media-alpha",
		ContentFingerprint: "sha256:bad-status",
		Kind:               PlaybackRenditionKindHLS,
		Status:             "complete",
		RungID:             "480p",
		Codec:              "h264",
		Height:             480,
		Bandwidth:          1_756_000,
		Path:               filepath.Join(t.TempDir(), "bad"),
		SizeBytes:          100,
		CreatedAt:          time.Unix(500, 0).UTC(),
		LastUsedAt:         time.Unix(500, 0).UTC(),
	})
	if err == nil {
		t.Fatal("UpsertPlaybackRendition accepted invalid status")
	}
}

//go:build unix

package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// CORE-2380 extends CORE-1343's no-segments-past-EOF rule to playbackInfo.
func TestPlaybackInfoRefusesResumeBeyondTruncatedSource(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "truncated.mkv")
	if err := os.WriteFile(source, []byte("incomplete download"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: source, Title: "Truncated", Container: "mkv", Codec: "hevc", Class: store.MediaClassVideo,
		ContentFingerprint: "fp-truncated", DurationSecondsHint: 6906, VideoWidthHint: 1280, VideoHeightHint: 720,
		Streams: []store.MediaFileStream{videoStream("hevc", "Main", 0, ""), audioStream(1, "aac", 2)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{ProfileID: scope.ProfileID, MediaID: media.ID, PositionSeconds: 163, DurationSeconds: 6906}, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	transcoder := &scriptedTranscoder{}
	transcoder.start = func(_ int, dir string, startSegment int) []string {
		writeArtifact(t, dir, "init.mp4", []byte("init"))
		for segment := startSegment; segment < 38; segment++ {
			writeArtifact(t, dir, fmt.Sprintf("seg%05d.m4s", segment), mediaSegment())
		}
		return []string{"sh", "-c", "echo 'File ended prematurely' >&2; exit 0"}
	}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	service.SetSessions(sessions)
	profile := CapabilityProfile{Client: "web-h264", Containers: []string{"mp4"}, VideoCodecs: []VideoCodecCap{{Codec: "h264", MaxHeight: 1080}}, AudioCodecs: []AudioCodecCap{{Codec: "aac", MaxChannels: 2}}}
	request := InfoRequest{MediaID: string(media.ID), CapabilityProfile: profile}
	before, err := service.Info(ctx, scope, request)
	if err != nil || before.Timeline.PositionSeconds != 163 || len(before.Streams) == 0 {
		t.Fatalf("baseline = %+v, %v", before, err)
	}
	key := SessionKey{SessionID: scope.SessionID, MediaID: media.ID}
	if _, err := sessions.FilePath(ctx, key, source, 6906, 1280, 720, false, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.FilePath(ctx, key, source, 6906, 1280, 720, false, "720p/seg00038.m4s"); !errors.Is(err, ErrSourceEnded) {
		t.Fatalf("truncated fixture did not reach EOF detection: %v", err)
	}
	check := func() {
		t.Helper()
		info, err := service.Info(ctx, scope, request)
		if err != nil {
			t.Fatal(err)
		}
		if info.Decision != "unavailable" || info.Reason != "media-source-incomplete" || len(info.Streams) != 0 || info.Timeline.PositionSeconds != 0 {
			t.Fatalf("known incomplete source still offers playback/resume: %+v", info)
		}
	}
	check()
	sessions.TerminatePlayback(scope.SessionID, media.ID)
	check() // A new player/session must not forget the same file's observed EOF.
	if err := os.WriteFile(source, []byte("completed download with more media bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := service.Info(ctx, scope, request)
	if err != nil || after.Decision == "unavailable" || after.Timeline.PositionSeconds != 163 {
		t.Fatalf("completed download remains blocked: %+v, %v", after, err)
	}
}

func TestIncompleteSourceObservationTracksOnlyTheObservedFileGeneration(t *testing.T) {
	for _, changeBeforeObservation := range []bool{false, true} {
		t.Run(fmt.Sprint(changeBeforeObservation), func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "download.mkv")
			if err := os.WriteFile(source, []byte("partial"), 0o600); err != nil {
				t.Fatal(err)
			}
			observed, err := os.Stat(source)
			if err != nil {
				t.Fatal(err)
			}
			var cache incompleteSourceCache
			if !changeBeforeObservation {
				cache.remember(source, observed)
				if !cache.contains(source) {
					t.Fatal("unchanged truncated source not remembered")
				}
			}
			// An atomic replacement may have the same size and mtime. Inode identity
			// must still invalidate the old encoder's observation.
			replacement := filepath.Join(dir, "completed.mkv")
			if err := os.WriteFile(replacement, []byte("updated"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(replacement, observed.ModTime(), observed.ModTime()); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, source); err != nil {
				t.Fatal(err)
			}
			if changeBeforeObservation {
				cache.remember(source, observed)
			}
			if cache.contains(source) {
				t.Fatal("replacement poisoned by old source's EOF")
			}
		})
	}
}

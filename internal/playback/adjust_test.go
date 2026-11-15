package playback

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

func hlsGrant(t *testing.T, token string) streamtoken.Grant {
	t.Helper()
	grant, err := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() }).
		UnverifiedGrant(token, streamtoken.TargetMediaHLS)
	if err != nil {
		t.Fatalf("HLS grant: %v", err)
	}
	return grant
}

func TestInfoRejectsOutOfRangeAdjustment(t *testing.T) {
	service, st, scope := liveComposedService(t)
	media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-invalid")
	for name, adjustment := range map[string]PlaybackAdjustment{
		"pitch above":   {PitchShiftSemitones: 7},
		"pitch below":   {PitchShiftSemitones: -7},
		"tempo slow":    {Tempo: 0.74},
		"tempo fast":    {Tempo: 1.26},
		"tempo nan":     {Tempo: math.NaN()},
		"tempo negated": {Tempo: -1},
	} {
		_, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: audioOnlyClient, Adjustment: &adjustment})
		if !errors.Is(err, ErrInvalidRequest) || !strings.HasPrefix(err.Error(), "adjustment.") {
			t.Errorf("%s: err = %v, want an invalid request naming the adjustment field", name, err)
		}
	}
	for _, adjustment := range []PlaybackAdjustment{{PitchShiftSemitones: 6, Tempo: 0.75}, {PitchShiftSemitones: -6, Tempo: 1.25}} {
		if _, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: audioOnlyClient, Adjustment: &adjustment}); err != nil {
			t.Errorf("%+v at the range edge: %v", adjustment, err)
		}
	}
}

func TestInfoAdjustedAudioIsAnAudioOnlyTranscode(t *testing.T) {
	service, st, scope := liveComposedService(t)
	media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-adjusted")
	adjustment := PlaybackAdjustment{PitchShiftSemitones: -2, Tempo: 0.9}
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: audioOnlyClient, Adjustment: &adjustment})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Decision != "transcode" || resp.Reason != "playback-adjustment" || len(resp.Streams) != 1 {
		t.Fatalf("decision = %q reason = %q streams = %#v, want one adjusted transcode", resp.Decision, resp.Reason, resp.Streams)
	}
	stream := resp.Streams[0]
	if stream.Kind != "hls" || stream.ID != "adjusted-main" || len(stream.Renditions) != 1 || stream.Renditions[0].ID != audioOnlyRungID || stream.Renditions[0].Height != 0 {
		t.Fatalf("stream = %#v, want one audio-only HLS rung", stream)
	}
	if grant := hlsGrant(t, stream.Token); grant.PitchShiftSemitones != -2 || grant.Tempo != 0.9 || grant.ComposedPicture || grant.AudioCopy {
		t.Fatalf("grant = %+v, want the adjustment bound to an audio-only stream", grant)
	}
	if resp.Adjustment == nil || *resp.Adjustment != adjustment {
		t.Fatalf("response adjustment = %+v, want %+v echoed", resp.Adjustment, adjustment)
	}
	if resp.Timeline.DurationSeconds != media.DurationSeconds {
		t.Fatalf("timeline = %+v, want song time (%ds)", resp.Timeline, media.DurationSeconds)
	}
}

func TestInfoDefaultAdjustmentChangesNothing(t *testing.T) {
	service, st, scope := liveComposedService(t)
	media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-default")
	plain, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: audioOnlyClient})
	if err != nil {
		t.Fatal(err)
	}
	for _, adjustment := range []PlaybackAdjustment{{}, {Tempo: 1}} {
		resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: audioOnlyClient, Adjustment: &adjustment})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Decision != plain.Decision || resp.Reason != plain.Reason || len(resp.Streams) != len(plain.Streams) || resp.Streams[0].Kind != plain.Streams[0].Kind || resp.Adjustment != nil {
			t.Fatalf("%+v: decision %q/%q streams %#v adjustment %+v, want the unadjusted %q/%q", adjustment, resp.Decision, resp.Reason, resp.Streams, resp.Adjustment, plain.Decision, plain.Reason)
		}
	}
}

func TestInfoAdjustmentThatCannotApplyPlaysUnadjusted(t *testing.T) {
	adjustment := PlaybackAdjustment{PitchShiftSemitones: 3}
	disallowed := false
	t.Run("video Media", func(t *testing.T) {
		service, st, scope := liveComposedService(t)
		mediaID := importProbedMedia(t, st, "fp-adjust-video", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
			videoStream("hevc", "Main 10", 0, ""),
			audioStream(1, "aac", 2),
		})
		h264Only := CapabilityProfile{Client: "web-h264", Containers: []string{"mp4"}, VideoCodecs: []VideoCodecCap{{Codec: "h264", MaxHeight: 1080}}, AudioCodecs: []AudioCodecCap{{Codec: "aac", MaxChannels: 2}}}
		resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: h264Only, Adjustment: &adjustment})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Adjustment != nil || resp.Reason == "playback-adjustment" || len(resp.Streams) == 0 {
			t.Fatalf("decision %q/%q adjustment %+v, want the unadjusted ladder", resp.Decision, resp.Reason, resp.Adjustment)
		}
		if grant := hlsGrant(t, resp.Streams[0].Token); grant.PitchShiftSemitones != 0 || grant.Tempo != 0 {
			t.Fatalf("grant = %+v, want no adjustment on video", grant)
		}
	})
	t.Run("transcode refused", func(t *testing.T) {
		service, st, scope := liveComposedService(t)
		setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{TranscodeAllowed: &disallowed})
		media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-refused")
		resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: audioOnlyClient, Adjustment: &adjustment})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Decision != "direct-play" || resp.Adjustment != nil {
			t.Fatalf("decision %q adjustment %+v, want unadjusted direct play", resp.Decision, resp.Adjustment)
		}
	})
	t.Run("next item", func(t *testing.T) {
		service, st, scope := liveComposedService(t)
		media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-main")
		next := importAudioMedia(t, st, filepath.Join(t.TempDir(), "next.mp3"), "fp-next")
		resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), NextMediaID: string(next.ID), CapabilityProfile: audioOnlyClient, Adjustment: &adjustment})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Adjustment == nil || resp.Next == nil || resp.Next.Decision != "direct-play" {
			t.Fatalf("main adjustment %+v next %+v, want only the main item adjusted", resp.Adjustment, resp.Next)
		}
	})
}

func TestInfoAdjustedComposedPictureIsAlwaysLive(t *testing.T) {
	service, st, scope := liveComposedService(t)
	media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-composed-adjusted")
	picture := ComposedPicture{TimedText: composedTestText}
	insertComposedRendition(t, st, media, "fp-composed-adjusted", picture)
	service.SetPictureComposer(staticComposer(picture, true, nil))
	adjustment := PlaybackAdjustment{Tempo: 1.1}
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: videoClient, Adjustment: &adjustment})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Decision != "transcode" || resp.Reason != "composed-picture" || resp.Adjustment == nil {
		t.Fatalf("decision = %q reason = %q adjustment %+v, want a live composed stream despite the Ready rendition", resp.Decision, resp.Reason, resp.Adjustment)
	}
	if grant := hlsGrant(t, resp.Streams[0].Token); !grant.ComposedPicture || grant.PlaybackRenditionID != "" || grant.Tempo != 1.1 {
		t.Fatalf("grant = %+v, want a live composed grant carrying tempo 1.1", grant)
	}
}

func adjustedFilePath(sessions *Sessions, key SessionKey, adjust ffmpeg.AudioAdjust, name string) (string, error) {
	return sessions.FilePathShaped(context.Background(), key, "/src/song.flac", 120, 0, 0, false, false,
		ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{Codec: "aac", Channels: 2}, nil, StreamShape{Adjust: adjust}, PriorityNormal, name)
}

func TestAdjustedSessionEncodesOneAudioOnlyRungOnTheOutputTimeline(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-adjusted", MediaID: "media-song"}
	adjust := ffmpeg.AudioAdjust{PitchSemitones: 2, Tempo: 0.8}
	masterPath, err := adjustedFilePath(sessions, key, adjust, masterPlaylistName)
	if err != nil {
		t.Fatal(err)
	}
	master, err := os.ReadFile(filepath.Clean(masterPath))
	if err != nil || strings.Count(string(master), "#EXT-X-STREAM-INF") != 1 || !strings.Contains(string(master), audioOnlyRungID+"/index.m3u8") || strings.Contains(string(master), "RESOLUTION") {
		t.Fatalf("master = %q err %v, want only the audio rung", master, err)
	}
	// 120s of song at tempo 0.8 lasts 150s: 38 segments of 4s.
	index, err := os.ReadFile(filepath.Join(filepath.Dir(masterPath), audioOnlyRungID, "index.m3u8"))
	if err != nil || strings.Count(string(index), "#EXTINF") != 38 {
		t.Fatalf("index = %q err %v, want 38 segments", index, err)
	}
	if _, err := adjustedFilePath(sessions, key, adjust, audioOnlyRungID+"/seg00037.m4s"); err != nil {
		t.Fatalf("last output segment: %v", err)
	}
	if _, err := adjustedFilePath(sessions, key, adjust, audioOnlyRungID+"/seg00038.m4s"); err == nil {
		t.Fatal("a segment past the adjusted end must be refused")
	}
	if len(transcoder.renditions) != 1 {
		t.Fatalf("encodes = %d, want 1", len(transcoder.renditions))
	}
	if rendition := transcoder.renditions[0]; !rendition.AudioOnly || rendition.AudioAdjust() != adjust {
		t.Fatalf("rendition = %+v adjust %+v, want an audio-only encode carrying %+v", rendition, rendition.AudioAdjust(), adjust)
	}
}

// TestAdjustmentChangeRestartsAtTheSameSongPosition proves a new tempo
// replaces the running session, and the replacement's first encoder starts at
// the output segment holding the song position playback info noted.
func TestAdjustmentChangeRestartsAtTheSameSongPosition(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-retempo", MediaID: "media-song"}
	if _, err := adjustedFilePath(sessions, key, ffmpeg.AudioAdjust{Tempo: 0.8}, audioOnlyRungID+"/init.mp4"); err != nil {
		t.Fatal(err)
	}
	// The viewer is 40s into the song when the tempo changes to 1.25: output
	// 32s, segment 8. Then back to 0.8: output 50s, segment 12.
	sessions.NoteResumePosition(key, 40)
	if _, err := adjustedFilePath(sessions, key, ffmpeg.AudioAdjust{Tempo: 1.25}, audioOnlyRungID+"/init.mp4"); err != nil {
		t.Fatal(err)
	}
	sessions.NoteResumePosition(key, 40)
	if _, err := adjustedFilePath(sessions, key, ffmpeg.AudioAdjust{Tempo: 0.8}, audioOnlyRungID+"/init.mp4"); err != nil {
		t.Fatal(err)
	}
	transcoder.mu.Lock()
	starts := append([]int(nil), transcoder.segmentStarts...)
	transcoder.mu.Unlock()
	if len(starts) != 3 || starts[0] != 0 || starts[1] != 8 || starts[2] != 12 {
		t.Fatalf("encoder starts = %v, want [0 8 12]", starts)
	}
	// A position noted while an unchanged session runs is dropped by its next
	// request rather than carried to a later session.
	sessions.NoteResumePosition(key, 40)
	if _, err := adjustedFilePath(sessions, key, ffmpeg.AudioAdjust{Tempo: 0.8}, audioOnlyRungID+"/init.mp4"); err != nil {
		t.Fatal(err)
	}
	sessions.mu.Lock()
	_, kept := sessions.resumeHints[key]
	sessions.mu.Unlock()
	if kept {
		t.Fatal("the unchanged session must drop the noted position")
	}
}

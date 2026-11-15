package playback

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// withComposedRecipeSupport states the FFmpeg build one test means: whether it
// can run the graphics and the timed-text composition recipes.
func withComposedRecipeSupport(t *testing.T, graphics, timedText bool) {
	t.Helper()
	previous := composedRecipeSupported
	composedRecipeSupported = func(text bool, _ ffmpeg.Rendition) bool {
		if text {
			return timedText
		}
		return graphics
	}
	t.Cleanup(func() { composedRecipeSupported = previous })
}

// videoClientWithH264 is videoClient declaring an H.264 envelope.
func videoClientWithH264(maxHeight, maxBitrate int) CapabilityProfile {
	profile := videoClient
	profile.VideoCodecs = []VideoCodecCap{{Codec: "h264", MaxHeight: maxHeight, MaxBitrate: maxBitrate}}
	return profile
}

// liveComposedInfo plays one audio Media with a timed-text picture and no Ready
// composed rendition.
func liveComposedInfo(t *testing.T, service *Service, st *store.Store, scope auth.Scope, profile CapabilityProfile) InfoResponse {
	t.Helper()
	media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-limits")
	service.SetPictureComposer(staticComposer(ComposedPicture{TimedText: composedTestText}, true, nil))
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: profile})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestLiveComposedPictureHonoursClientAndQuirkLimits(t *testing.T) {
	cap360 := QuirkEntry{
		ID:       "test-client-360-ceiling",
		Note:     "fixture",
		Selector: QuirkSelector{Client: videoClient.Client},
		Override: QuirkOverride{MaxHeight: 360},
	}
	cases := []struct {
		name       string
		profile    CapabilityProfile
		quirk      *QuirkEntry
		wantHeight int
	}{
		{"no limit keeps 720p", videoClientWithH264(0, 0), nil, 720},
		{"client H.264 height ceiling", videoClientWithH264(360, 0), nil, 360},
		{"client H.264 bitrate ceiling", videoClientWithH264(0, 1_200_000), nil, 360},
		// Each rung's budget is its video maxrate plus the ladder's 256 kbps
		// audio and mux allowance: 1756k, 1256k, 856k.
		{"budget exactly the 720p total", videoClientWithH264(0, 1_756_000), nil, 720},
		{"budget just under the 720p total", videoClientWithH264(0, 1_755_999), nil, 480},
		{"budget exactly the 480p total", videoClientWithH264(0, 1_256_000), nil, 480},
		{"budget just under the 480p total", videoClientWithH264(0, 1_255_999), nil, 360},
		{"budget exactly the 360p total", videoClientWithH264(0, 856_000), nil, 360},
		{"server quirk height cap", videoClient, &cap360, 360},
		{"quirk tighter than the client", videoClientWithH264(1080, 0), &cap360, 360},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, st, scope := liveComposedService(t)
			if tc.quirk != nil {
				installQuirks(t, service, *tc.quirk)
			}
			resp := liveComposedInfo(t, service, st, scope, tc.profile)
			if resp.Decision != "transcode" || resp.Reason != "composed-picture" || len(resp.Streams) != 1 {
				t.Fatalf("decision = %q reason = %q, want a live composed stream", resp.Decision, resp.Reason)
			}
			renditions := resp.Streams[0].Renditions
			if len(renditions) != 1 || renditions[0].ID != ComposedRungID || renditions[0].Height != tc.wantHeight {
				t.Fatalf("renditions = %#v, want one %dp composed rung", renditions, tc.wantHeight)
			}
			if limit := tc.profile.VideoCodecs; len(limit) > 0 && limit[0].MaxBitrate > 0 && renditions[0].Bandwidth > limit[0].MaxBitrate {
				t.Fatalf("advertised bandwidth %d exceeds the client budget %d", renditions[0].Bandwidth, limit[0].MaxBitrate)
			}
			tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() })
			grant, err := tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
			if err != nil || !grant.ComposedPicture {
				t.Fatalf("grant = %+v err %v, want a live composed grant", grant, err)
			}
			// The byte layer rebuilds the session from the grant alone, so the
			// shape has to round-trip through it.
			rung, err := composedLiveRung(LadderCapsFromGrant(grant))
			if err != nil || rung.Height != tc.wantHeight || rung.Bandwidth != renditions[0].Bandwidth {
				t.Fatalf("session rung = %+v err %v, want %dp advertised as %d", rung, err, tc.wantHeight, renditions[0].Bandwidth)
			}
			if rung.Bandwidth != rung.VideoKbps*1000+256_000 {
				t.Fatalf("rung = %+v, want the advertised total to be the video maxrate plus the 256 kbps audio and mux allowance", rung)
			}
		})
	}
}

func TestLiveComposedPictureFallsBackToAudioWhenNoShapeFits(t *testing.T) {
	cases := []struct {
		name    string
		profile CapabilityProfile
	}{
		{"client height below every shape", videoClientWithH264(240, 0)},
		{"client bitrate below every shape", videoClientWithH264(0, 400_000)},
		{"budget just under the 360p total", videoClientWithH264(0, 855_999)},
		{"budget that only covers the 360p video", videoClientWithH264(0, 600_000)},
		{"client declares no H.264", func() CapabilityProfile {
			p := videoClient
			p.VideoCodecs = []VideoCodecCap{{Codec: "hevc"}}
			return p
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, st, scope := liveComposedService(t)
			resp := liveComposedInfo(t, service, st, scope, tc.profile)
			if resp.Decision != "direct-play" {
				t.Fatalf("decision = %q reason = %q, want direct play of the audio", resp.Decision, resp.Reason)
			}
			if jobs := composedJobs(t, st); len(jobs) != 0 {
				t.Fatalf("composed jobs = %+v, want none", jobs)
			}
		})
	}
}

func TestLiveComposedPictureNeedsCapableFFmpeg(t *testing.T) {
	graphics := func(t *testing.T) ComposedPicture {
		path := filepath.Join(t.TempDir(), "song.cdg")
		if err := os.WriteFile(path, make([]byte, 24*4), 0o600); err != nil {
			t.Fatal(err)
		}
		return ComposedPicture{GraphicsPath: path}
	}
	cases := []struct {
		name               string
		picture            func(*testing.T) ComposedPicture
		graphicsOK, textOK bool
		wantLive           bool
	}{
		{"timed text without libass plays the audio", func(*testing.T) ComposedPicture { return ComposedPicture{TimedText: composedTestText} }, true, false, false},
		{"timed text with a capable build goes live", func(*testing.T) ComposedPicture { return ComposedPicture{TimedText: composedTestText} }, true, true, true},
		{"graphics need no libass", graphics, true, false, true},
		{"graphics without the encoder play the audio", graphics, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withComposedRecipeSupport(t, tc.graphicsOK, tc.textOK)
			service, st, scope := liveComposedService(t)
			media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-capability")
			service.SetPictureComposer(staticComposer(tc.picture(t), true, nil))
			resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: videoClient})
			if err != nil {
				t.Fatal(err)
			}
			live := resp.Decision == "transcode" && resp.Reason == "composed-picture"
			if live != tc.wantLive || (!tc.wantLive && resp.Decision != "direct-play") {
				t.Fatalf("decision = %q reason = %q, want live %v", resp.Decision, resp.Reason, tc.wantLive)
			}
		})
	}
}

// A Ready rendition was encoded earlier, so a build that cannot compose live
// still serves it.
func TestReadyComposedRenditionNeedsNoCapableFFmpeg(t *testing.T) {
	withComposedRecipeSupport(t, false, false)
	service, st, scope := liveComposedService(t)
	media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-ready-incapable")
	picture := ComposedPicture{TimedText: composedTestText}
	insertComposedRendition(t, st, media, "fp-ready-incapable", picture)
	service.SetPictureComposer(staticComposer(picture, true, nil))
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: videoClient})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Decision != "precomputed-rendition" || resp.Reason != "composed-picture" {
		t.Fatalf("decision = %q reason = %q, want the Ready rendition", resp.Decision, resp.Reason)
	}
}

func TestComposedShapedSessionEncodesTheGrantedShape(t *testing.T) {
	t.Parallel()
	for _, height := range []int{720, 480, 360} {
		transcoder := &fakeTranscoder{}
		sessions := NewSessions(transcoder, t.TempDir(), nil)
		sessions.SetPictureComposer(func(context.Context, store.MediaID, string) (ComposedPicture, bool, error) {
			return ComposedPicture{TimedText: composedTestText}, true, nil
		})
		for _, name := range []string{masterPlaylistName, ComposedRungID + "/seg00000.m4s"} {
			if _, err := sessions.FilePathShaped(context.Background(), SessionKey{SessionID: "s", MediaID: "m"}, "/src/song.mp3", 30, 0, 0,
				false, false, ladderCaps{ClientMaxHeight: height}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil,
				StreamShape{ComposedPicture: true}, PriorityNormal, name); err != nil {
				t.Fatalf("%dp %s: %v", height, name, err)
			}
		}
		sessions.Close()
		if len(transcoder.renditions) != 1 || transcoder.renditions[0].Height != height {
			t.Fatalf("%dp: encodes = %+v, want one %dp composed encode", height, transcoder.renditions, height)
		}
	}
}

// The capability check sees the encode the session will run: the chosen
// shape, the negotiated audio and the requested adjustment.
func TestLiveComposedCapabilityCheckSeesTheSessionEncode(t *testing.T) {
	var checked []ffmpeg.Rendition
	previous := composedRecipeSupported
	composedRecipeSupported = func(_ bool, rendition ffmpeg.Rendition) bool {
		checked = append(checked, rendition)
		return true
	}
	t.Cleanup(func() { composedRecipeSupported = previous })
	service, st, scope := liveComposedService(t)
	media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-checked")
	service.SetPictureComposer(staticComposer(ComposedPicture{TimedText: composedTestText}, true, nil))
	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(media.ID),
		CapabilityProfile: videoClientWithH264(360, 0),
		Adjustment:        &PlaybackAdjustment{Tempo: 0.8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Reason != "composed-picture" || len(checked) != 1 {
		t.Fatalf("reason = %q, checks = %d, want one checked live composed stream", resp.Reason, len(checked))
	}
	got := checked[0]
	if got.Height != 360 || got.VideoKbps != 600 || got.AudioCodec == "" {
		t.Fatalf("checked rendition = %+v, want the 360p shape with the negotiated audio", got)
	}
	want := ffmpeg.Rendition{Height: 360, VideoKbps: 600, AudioCodec: got.AudioCodec, AudioChannels: got.AudioChannels, AudioGainDB: got.AudioGainDB}.
		WithAudioAdjust(ffmpeg.AudioAdjust{Tempo: 0.8})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("checked rendition = %+v, want %+v (carrying the tempo change)", got, want)
	}
}

// A build without the adjustment's filters declines the adjustment: the song
// plays, composed and unadjusted, instead of a stream that fails to start.
func TestAdjustmentDeclinedWithoutItsFilters(t *testing.T) {
	previous := audioAdjustSupported
	audioAdjustSupported = func(ffmpeg.Rendition) bool { return false }
	t.Cleanup(func() { audioAdjustSupported = previous })
	cases := map[string]struct {
		profile      CapabilityProfile
		wantDecision string
	}{
		"composed picture plays unadjusted": {videoClient, "transcode"},
		"audio plays unadjusted":            {audioOnlyClient, "direct-play"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			service, st, scope := liveComposedService(t)
			media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-declined")
			service.SetPictureComposer(staticComposer(ComposedPicture{TimedText: composedTestText}, true, nil))
			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           string(media.ID),
				CapabilityProfile: tc.profile,
				Adjustment:        &PlaybackAdjustment{PitchShiftSemitones: 2},
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Adjustment != nil || resp.Reason == "playback-adjustment" {
				t.Fatalf("adjustment = %+v reason = %q, want the adjustment declined", resp.Adjustment, resp.Reason)
			}
			if resp.Decision != tc.wantDecision {
				t.Fatalf("decision = %q reason = %q, want %q", resp.Decision, resp.Reason, tc.wantDecision)
			}
		})
	}
}

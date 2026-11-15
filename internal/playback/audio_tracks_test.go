package playback

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

func multiAudioStreams() []store.MediaFileStream {
	return []store.MediaFileStream{
		videoStream("h264", "High", 0, ""),
		{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Language: "eng", Default: true},
		{Index: 2, Type: "audio", Codec: "aac", Channels: 2, Language: "fra"},
		{Index: 3, Type: "audio", Codec: "aac", Channels: 2, Language: "eng", Commentary: true},
		{Index: 4, Type: "subtitle", Codec: "subrip", Language: "eng"},
	}
}

func TestResolveAudioTrack(t *testing.T) {
	streams := multiAudioStreams()
	sel := resolveAudioTrack(streams, nil, nil)
	if sel.fileDefault != 1 || sel.chosen != 1 || sel.overridesDefault() || sel.language() != "eng" {
		t.Fatalf("no selection = %+v, want default stream 1 chosen", sel)
	}
	if len(sel.tracks) != 3 || !sel.tracks[0].Selected || !sel.tracks[0].Default || !sel.tracks[2].Commentary {
		t.Fatalf("tracks = %+v, want three audio tracks, the first default and selected", sel.tracks)
	}

	picked := 2
	sel = resolveAudioTrack(streams, &picked, nil)
	if sel.chosen != 2 || !sel.overridesDefault() || sel.language() != "fra" || !sel.tracks[1].Selected || sel.tracks[0].Selected {
		t.Fatalf("selection 2 = %+v, want stream 2 chosen over the default", sel)
	}

	notAudio := 4
	if sel = resolveAudioTrack(streams, &notAudio, nil); sel.chosen != 1 {
		t.Fatalf("subtitle index selection chose %d, want the default 1", sel.chosen)
	}

	// No default disposition: the first audio stream is the default.
	streams[1].Default = false
	if sel = resolveAudioTrack(streams, nil, nil); sel.fileDefault != 1 || sel.chosen != 1 {
		t.Fatalf("no default disposition = %+v, want stream 1", sel)
	}
	streams[2].Default = true
	if sel = resolveAudioTrack(streams, nil, nil); sel.fileDefault != 2 || sel.chosen != 2 || !sel.tracks[1].Default {
		t.Fatalf("default disposition on stream 2 = %+v, want stream 2", sel)
	}

	if sel = resolveAudioTrack([]store.MediaFileStream{videoStream("h264", "High", 0, "")}, nil, nil); sel.tracks != nil || sel.chosen != -1 || sel.overridesDefault() {
		t.Fatalf("silent file = %+v, want no tracks", sel)
	}
}

// CORE-1709: playbackInfo lists every audio track on every route, and picking
// a non-default one moves direct play onto the audio-copy rung with that
// track named in the stream grant.
func TestInfoAudioTrackSelection(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-multi-audio", "mp4", "h264", 1920, 1080, multiAudioStreams())
	profile := main10_1080p

	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: profile})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play for the default track", resp.Decision)
	}
	if len(resp.AudioTracks) != 3 || !resp.AudioTracks[0].Selected || resp.AudioTracks[0].StreamIndex != 1 {
		t.Fatalf("AudioTracks = %+v, want three tracks with stream 1 selected", resp.AudioTracks)
	}
	// The direct-play response's HLS sibling maps the same default track.
	for _, stream := range resp.Streams {
		if stream.Kind != "hls" {
			continue
		}
		grant, err := service.tokens.UnverifiedGrant(stream.Token, streamtoken.TargetMediaHLS)
		if err != nil {
			t.Fatalf("HLS sibling token: %v", err)
		}
		if grant.AudioStreamIndex != 2 {
			t.Fatalf("sibling AudioStreamIndex = %d, want 2 (stream 1 + 1)", grant.AudioStreamIndex)
		}
	}

	picked := 2
	resp, err = service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: profile, SelectedAudioIndex: &picked})
	if err != nil {
		t.Fatalf("Info with selected audio: %v", err)
	}
	if resp.Decision != "direct-stream" || resp.Reason != "audio-transcode-only" {
		t.Fatalf("Decision = %q (%q), want direct-stream audio-transcode-only for a non-default track", resp.Decision, resp.Reason)
	}
	if len(resp.Streams) != 1 || resp.Streams[0].Kind != "hls" {
		t.Fatalf("Streams = %+v, want one HLS stream", resp.Streams)
	}
	grant, err := service.tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
	if err != nil {
		t.Fatalf("HLS token: %v", err)
	}
	if grant.AudioStreamIndex-1 != picked || !grant.AudioCopy {
		t.Fatalf("grant = AudioStreamIndex %d AudioCopy %v, want stream %d on the audio-copy rung", grant.AudioStreamIndex, grant.AudioCopy, picked)
	}
	if !resp.AudioTracks[1].Selected || resp.AudioTracks[0].Selected {
		t.Fatalf("AudioTracks = %+v, want stream 2 selected", resp.AudioTracks)
	}

	// Picking the default track keeps direct play.
	defaultTrack := 1
	resp, err = service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: profile, SelectedAudioIndex: &defaultTrack})
	if err != nil {
		t.Fatalf("Info with default audio: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play when the default track is picked", resp.Decision)
	}
}

// A single-track file names no audio stream in the grant: FFmpeg's own
// choice is that track, and existing session keys stay unchanged.
func TestInfoSingleAudioTrackLeavesGrantUnbound(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-single-audio", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "truehd", 8),
	})
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: main10_1080p})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if len(resp.AudioTracks) != 1 || !resp.AudioTracks[0].Selected {
		t.Fatalf("AudioTracks = %+v, want the one track selected", resp.AudioTracks)
	}
	grant, err := service.tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
	if err != nil {
		t.Fatalf("HLS token: %v", err)
	}
	if grant.AudioStreamIndex != 0 {
		t.Fatalf("AudioStreamIndex = %d, want 0 for a single-track file", grant.AudioStreamIndex)
	}
}

// CORE-1680: with no explicit pick, the Profile's audio languages choose the
// stream, in preference order; a main track beats commentary in the same
// language, and probed ISO 639-2 codes match BCP 47 preferences.
func TestResolveAudioTrackHonoursLanguagePreferences(t *testing.T) {
	streams := multiAudioStreams()
	for _, tc := range []struct {
		name      string
		languages []string
		want      int
	}{
		{"no preference keeps the default", nil, 1},
		{"preferred language", []string{"fr"}, 2},
		{"first available preference wins", []string{"de", "fr-CA", "en"}, 2},
		{"default wins within a language", []string{"en"}, 1},
		{"unmatched preference keeps the default", []string{"ja"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sel := resolveAudioTrack(streams, nil, tc.languages)
			if sel.chosen != tc.want {
				t.Fatalf("chosen = %d, want %d", sel.chosen, tc.want)
			}
		})
	}
	// Commentary only wins when it is the only track in the language.
	commentaryFirst := []store.MediaFileStream{
		{Index: 1, Type: "audio", Codec: "aac", Language: "fra", Default: true},
		{Index: 2, Type: "audio", Codec: "aac", Language: "eng", Commentary: true},
		{Index: 3, Type: "audio", Codec: "aac", Language: "eng"},
	}
	if sel := resolveAudioTrack(commentaryFirst, nil, []string{"en"}); sel.chosen != 3 {
		t.Fatalf("chosen = %d, want the main English track 3", sel.chosen)
	}
	// An explicit pick beats the preference.
	picked := 3
	if sel := resolveAudioTrack(streams, &picked, []string{"fr"}); sel.chosen != 3 {
		t.Fatalf("chosen = %d, want the explicit pick 3", sel.chosen)
	}
}

// CORE-1680: a two-language MP4 the browser could direct-play still plays the
// Profile's preferred language: the decision moves to the audio-copy rung
// with that stream mapped, and the response marks it selected.
func TestInfoAppliesAudioLanguagePreferenceOnDirectPlayFile(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-two-language", "mp4", "h264", 1920, 1080, multiAudioStreams())
	setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{AudioLanguages: &[]string{"fr"}})

	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: main10_1080p})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-stream" {
		t.Fatalf("Decision = %q (%q), want direct-stream for the preferred non-default track", resp.Decision, resp.Reason)
	}
	grant, err := service.tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
	if err != nil {
		t.Fatalf("HLS token: %v", err)
	}
	if grant.AudioStreamIndex != 3 {
		t.Fatalf("AudioStreamIndex = %d, want 3 (French stream 2 + 1)", grant.AudioStreamIndex)
	}
	if !resp.AudioTracks[1].Selected || resp.AudioTracks[0].Selected {
		t.Fatalf("AudioTracks = %+v, want the French stream selected", resp.AudioTracks)
	}

	// A preference the default already satisfies keeps direct play.
	setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{AudioLanguages: &[]string{"en"}})
	resp, err = service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: main10_1080p})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play when the default track is preferred", resp.Decision)
	}
}

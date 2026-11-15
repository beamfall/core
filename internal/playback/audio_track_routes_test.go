package playback

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// CORE-1874: passthrough is decided on the track the stream maps. A client
// that bitstreams TrueHD must not be handed a selected plain DTS track as a
// copy because the file's default track happens to be TrueHD; picking the
// TrueHD track of a file whose default is DTS does pass it through.
func TestInfoPassthroughFollowsSelectedAudioTrack(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	truehd := audioStream(1, "truehd", 8)
	truehd.Default = true
	dts := audioStream(2, "dts", 6)
	dts.Profile = "DTS"
	mediaID := importProbedMedia(t, st, "fp-passthrough-pick", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		truehd,
		dts,
	})
	profile := main10_1080p
	profile.AudioCodecs = append([]AudioCodecCap{{Codec: "truehd", MaxChannels: 8}}, profile.AudioCodecs...)
	profile.DownstreamAudio = "passthrough"

	hlsGrant := func(selected int) streamtoken.Grant {
		t.Helper()
		resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: profile, SelectedAudioIndex: &selected})
		if err != nil {
			t.Fatalf("Info(selected %d): %v", selected, err)
		}
		for _, stream := range resp.Streams {
			if stream.Kind != "hls" {
				continue
			}
			grant, err := service.tokens.UnverifiedGrant(stream.Token, streamtoken.TargetMediaHLS)
			if err != nil {
				t.Fatalf("HLS token: %v", err)
			}
			return grant
		}
		t.Fatalf("Info(selected %d) streams = %+v, want an HLS stream", selected, resp.Streams)
		return streamtoken.Grant{}
	}

	picked := hlsGrant(2)
	if picked.AudioStreamIndex != 3 || picked.HLSAudioCodec != "eac3" {
		t.Fatalf("selected DTS grant = AudioStreamIndex %d codec %q, want stream 2 encoded to E-AC-3, not copied", picked.AudioStreamIndex, picked.HLSAudioCodec)
	}
	if got := hlsGrant(1); got.AudioStreamIndex != 2 || got.HLSAudioCodec != "copy" {
		t.Fatalf("default TrueHD grant = AudioStreamIndex %d codec %q, want passthrough", got.AudioStreamIndex, got.HLSAudioCodec)
	}
}

func TestChosenStreamsKeepsOnlyTheChosenAudio(t *testing.T) {
	streams := multiAudioStreams()
	picked := 2
	got := resolveAudioTrack(streams, &picked, nil).chosenStreams(streams)
	var audio []int
	for _, stream := range got {
		if stream.Type == "audio" {
			audio = append(audio, stream.Index)
		}
	}
	if len(got) != 3 || len(audio) != 1 || audio[0] != 2 {
		t.Fatalf("chosenStreams = %+v, want video, audio 2 and the subtitle", got)
	}
	silent := []store.MediaFileStream{videoStream("h264", "High", 0, "")}
	if got := resolveAudioTrack(silent, nil, nil).chosenStreams(silent); len(got) != 1 {
		t.Fatalf("silent chosenStreams = %+v, want the input unchanged", got)
	}
}

// CORE-1873: a non-default audio track on a composed-picture song is composed
// live with that track named in the grant, even when a precomputed composed
// rendition (which carries the default track) is Ready; the live session's
// rendition then maps it.
func TestComposedPictureFollowsSelectedAudioTrack(t *testing.T) {
	ctx := context.Background()
	service, st, scope := liveComposedService(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "song.mka")
	if err := os.WriteFile(path, []byte("beamfall audio bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	main := audioStream(0, "aac", 2)
	main.Default = true
	alternate := audioStream(1, "aac", 2)
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:           "library-local",
		Path:                path,
		Title:               "Song fp-two-tracks",
		Container:           "mka",
		Codec:               "aac",
		Class:               store.MediaClassAudio,
		ContentFingerprint:  "fp-two-tracks",
		DurationSecondsHint: 3,
		Streams:             []store.MediaFileStream{main, alternate},
	})
	if err != nil {
		t.Fatal(err)
	}
	picture := ComposedPicture{TimedText: composedTestText}
	id := insertComposedRendition(t, st, media, "fp-two-tracks", picture)
	service.SetPictureComposer(staticComposer(picture, true, nil))

	resp, err := service.Info(ctx, scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: videoClient})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Decision != "precomputed-rendition" {
		t.Fatalf("default track decision = %q, want the Ready rendition %q", resp.Decision, id)
	}

	selected := 1
	resp, err = service.Info(ctx, scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: videoClient, SelectedAudioIndex: &selected})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Decision != "transcode" || resp.Reason != "composed-picture" || len(resp.Streams) != 1 {
		t.Fatalf("selected track decision = %q reason %q streams %+v, want a live composed stream", resp.Decision, resp.Reason, resp.Streams)
	}
	grant, err := service.tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
	if err != nil || !grant.ComposedPicture || grant.AudioStreamIndex != selected+1 {
		t.Fatalf("grant = %+v err %v, want a live composed grant naming stream %d", grant, err, selected)
	}

	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	sessions.SetPictureComposer(func(context.Context, store.MediaID, string) (ComposedPicture, bool, error) {
		return picture, true, nil
	})
	index := grant.AudioStreamIndex - 1
	key := SessionKey{SessionID: "session-default", MediaID: media.ID}
	if _, err := sessions.FilePathShaped(ctx, key, path, 30, 0, 0, false, false, ladderCaps{}, VideoSignal{}, -1,
		ffmpeg.AudioProfile{}, &index, StreamShape{ComposedPicture: true}, PriorityNormal, ComposedRungID+"/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	if len(transcoder.renditions) != 1 {
		t.Fatalf("encodes = %d, want 1", len(transcoder.renditions))
	}
	rendition := transcoder.renditions[0]
	if _, ok := rendition.Picture(); !ok || rendition.AudioStreamIndex == nil || *rendition.AudioStreamIndex != selected {
		t.Fatalf("composed rendition audio = %v, want stream %d", rendition.AudioStreamIndex, selected)
	}
}

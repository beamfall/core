package playback

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

func TestAACPaddingSamples(t *testing.T) {
	for decoded, want := range map[int64]int{
		88064: 0,    // priming plus content fill 87 frames exactly
		88065: 1023, // one sample over opens a frame
		88200: 888,
		44100: 956,
		96000: 256,
	} {
		if got := aacPaddingSamples(decoded); got != want {
			t.Fatalf("aacPaddingSamples(%d) = %d, want %d", decoded, got, want)
		}
	}
}

func TestStampGapless(t *testing.T) {
	source := store.MediaFileStream{Index: 0, Type: "audio", Codec: "mp3", Channels: 2, SampleRate: 44100, EncoderDelaySamples: 1105, EncoderPaddingSamples: 551, DecodedSamples: 88200}
	aac := ffmpeg.AudioProfile{Codec: "aac", Channels: 2}
	fresh := func() []StreamChoice {
		return []StreamChoice{
			{ID: "direct", Kind: "direct-play"},
			{ID: "hls-main", Kind: "hls"},
			{ID: "hls-precomputed", Kind: "precomputed-hls"},
			{ID: "audio-copy", Kind: "hls"},
		}
	}

	choices := fresh()
	stampGapless(choices, []store.MediaFileStream{source}, aac, 0)
	if got, want := choices[0].Gapless, (&GaplessInfo{SampleRate: 44100, EncoderDelaySamples: 1105, EncoderPaddingSamples: 551}); got == nil || *got != *want {
		t.Fatalf("direct play gapless = %+v, want the source's %+v", got, want)
	}
	if got, want := choices[1].Gapless, (&GaplessInfo{SampleRate: 44100, EncoderDelaySamples: 1024, EncoderPaddingSamples: 888}); got == nil || *got != *want {
		t.Fatalf("live AAC transcode gapless = %+v, want the encoder's %+v", got, want)
	}
	if choices[2].Gapless != nil || choices[3].Gapless != nil {
		t.Fatalf("only direct play and live transcodes carry gapless trims: %+v", choices)
	}

	for name, tc := range map[string]struct {
		streams []store.MediaFileStream
		audio   ffmpeg.AudioProfile
		resume  int
	}{
		"video source":      {[]store.MediaFileStream{{Type: "video", Codec: "h264"}, source}, aac, 0},
		"resumed session":   {[]store.MediaFileStream{source}, aac, 30},
		"unmeasured length": {[]store.MediaFileStream{withDecoded(source, 0)}, aac, 0},
		"non-AAC output":    {[]store.MediaFileStream{source}, ffmpeg.AudioProfile{Codec: "opus", Channels: 2}, 0},
		"unencodable rate":  {[]store.MediaFileStream{withRate(source, 192000)}, aac, 0},
	} {
		choices := fresh()
		stampGapless(choices, tc.streams, tc.audio, tc.resume)
		if choices[1].Gapless != nil {
			t.Fatalf("%s: live transcode must not claim the encoder's trim: %+v", name, choices[1].Gapless)
		}
	}

	// A source declaring no gapless facts gives direct play nothing to trim.
	choices = fresh()
	undeclared := source
	undeclared.EncoderDelaySamples, undeclared.EncoderPaddingSamples = 0, 0
	stampGapless(choices, []store.MediaFileStream{undeclared}, aac, 0)
	if choices[0].Gapless != nil {
		t.Fatalf("undeclared source direct play gapless = %+v", choices[0].Gapless)
	}
}

func withDecoded(s store.MediaFileStream, decoded int64) store.MediaFileStream {
	s.DecodedSamples = decoded
	return s
}

func withRate(s store.MediaFileStream, rate int) store.MediaFileStream {
	s.SampleRate = rate
	return s
}

// TestInfoCarriesGaplessForDirectPlay is the wire-up: the gapless facts the
// scan stored survive the store round trip and reach the direct-play choice.
func TestInfoCarriesGaplessForDirectPlay(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	path := filepath.Join(t.TempDir(), "tone.mp3")
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:           "library-local",
		Path:                path,
		Title:               "Gapless tone",
		Container:           "mp3",
		Codec:               "mp3",
		Class:               store.MediaClassAudio,
		ContentFingerprint:  "fp-gapless-mp3",
		DurationSecondsHint: 2,
		Streams: []store.MediaFileStream{{
			Index: 0, Type: "audio", Codec: "mp3", Channels: 2,
			SampleRate: 44100, EncoderDelaySamples: 1105, EncoderPaddingSamples: 551, DecodedSamples: 88200,
		}},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: browserProfile})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" || len(resp.Streams) == 0 {
		t.Fatalf("decision = %q with %d streams, want direct-play", resp.Decision, len(resp.Streams))
	}
	if got, want := resp.Streams[0].Gapless, (&GaplessInfo{SampleRate: 44100, EncoderDelaySamples: 1105, EncoderPaddingSamples: 551}); got == nil || *got != *want {
		t.Fatalf("direct-play gapless = %+v, want %+v", got, want)
	}
}

// gaplessJoinToleranceSamples is the longest near-silent run the join between
// two gapless pieces may show: a 440 Hz tone at amplitude 1/8 stays under the
// silence threshold for about 5 samples at each zero crossing, while an
// untrimmed AAC join leaves a run of roughly two frames (~2000 samples).
const gaplessJoinToleranceSamples = 64

// gaplessSilence is the amplitude under which a decoded sample counts as
// silent.
const gaplessSilence = 0.02

// TestTranscodedGaplessPiecesJoinWithoutGap cuts one continuous synthetic tone
// into two AAC M4A pieces, transcodes each through the live HLS session
// pipeline, decodes the fMP4 output, trims each by the delay and padding
// playback info reports, and checks the joined audio has the exact content
// length and no silence at the join (CORE-1626 AC4).
func TestTranscodedGaplessPiecesJoinWithoutGap(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	const (
		rate         = 44100
		pieceSeconds = 5
		pieceSamples = pieceSeconds * rate
	)
	dir := t.TempDir()
	tone := filepath.Join(dir, "tone.wav")
	runFFmpeg(t, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate="+strconv.Itoa(rate)+":duration="+strconv.Itoa(2*pieceSeconds), "-ac", "1", tone)
	first := filepath.Join(dir, "piece-a.m4a")
	second := filepath.Join(dir, "piece-b.m4a")
	runFFmpeg(t, "-i", tone, "-t", strconv.Itoa(pieceSeconds), "-c:a", "aac", first)
	runFFmpeg(t, "-ss", strconv.Itoa(pieceSeconds), "-i", tone, "-c:a", "aac", second)

	sessions := newTestSessions(t, ffmpeg.New(), t.TempDir(), nil)
	defer sessions.Close()
	var joined, untrimmed []float32
	for i, piece := range []string{first, second} {
		probe, err := ffmpeg.New().Probe(context.Background(), piece)
		if err != nil {
			t.Fatalf("probe %s: %v", piece, err)
		}
		streams := gaplessStoreStreams(probe)
		choices := []StreamChoice{{ID: "hls-piece", Kind: "hls"}}
		stampGapless(choices, streams, ffmpeg.AudioProfile{Codec: "aac", Channels: 2}, 0)
		trim := choices[0].Gapless
		if trim == nil {
			t.Fatalf("piece %d: no gapless trim for streams %+v", i, streams)
		}

		key := SessionKey{SessionID: "session-gapless", MediaID: store.MediaID(fmt.Sprintf("media-piece-%d", i))}
		var stream []byte
		var segmentDir string
		for segment := 0; segment*segmentSeconds < pieceSeconds; segment++ {
			path, err := sessions.FilePath(context.Background(), key, piece, pieceSeconds, 0, 0, false, fmt.Sprintf("%s/seg%05d.m4s", sourceRungID, segment))
			if err != nil {
				t.Fatalf("piece %d segment %d: %v", i, segment, err)
			}
			segmentDir = filepath.Dir(path)
			data, err := os.ReadFile(path) // #nosec G304 -- path is beneath a test-owned temporary directory.
			if err != nil {
				t.Fatal(err)
			}
			stream = append(stream, data...)
		}
		initSegment, err := os.ReadFile(filepath.Join(segmentDir, initSegmentName)) // #nosec G304 -- path is beneath a test-owned temporary directory.
		if err != nil {
			t.Fatal(err)
		}
		fmp4 := filepath.Join(dir, fmt.Sprintf("piece-%d.mp4", i))
		if err := os.WriteFile(fmp4, append(initSegment, stream...), 0o600); err != nil { // #nosec G703 -- path is beneath a test-owned temporary directory.
			t.Fatal(err)
		}
		samples := decodeMonoF32(t, fmp4, rate)

		if got, want := len(samples), trim.EncoderDelaySamples+pieceSamples+trim.EncoderPaddingSamples; got != want {
			t.Fatalf("piece %d decodes to %d samples, want delay %d + content %d + padding %d = %d", i, got, trim.EncoderDelaySamples, pieceSamples, trim.EncoderPaddingSamples, want)
		}
		joined = append(joined, samples[trim.EncoderDelaySamples:len(samples)-trim.EncoderPaddingSamples]...)
		untrimmed = append(untrimmed, samples...)
	}

	if len(joined) != 2*pieceSamples {
		t.Fatalf("joined length = %d, want %d", len(joined), 2*pieceSamples)
	}
	if run := longestSilentRun(joined[pieceSamples-4096 : pieceSamples+4096]); run > gaplessJoinToleranceSamples {
		t.Fatalf("trimmed join has a %d-sample silent run, tolerance %d", run, gaplessJoinToleranceSamples)
	}
	// The check discriminates: the same pieces joined untrimmed show the gap.
	if run := longestSilentRun(untrimmed[len(untrimmed)/2-4096 : len(untrimmed)/2+4096]); run <= gaplessJoinToleranceSamples {
		t.Fatalf("untrimmed join shows only a %d-sample silent run; the gap check cannot discriminate", run)
	}
}

// gaplessStoreStreams maps the probe's audio streams onto store rows, as the
// scan does.
func gaplessStoreStreams(probe ffmpeg.Probe) []store.MediaFileStream {
	var streams []store.MediaFileStream
	for _, s := range probe.Streams {
		if s.Type != "audio" {
			continue
		}
		streams = append(streams, store.MediaFileStream{
			Index: s.Index, Type: s.Type, Codec: s.Codec, Channels: s.Channels,
			SampleRate: s.SampleRate, EncoderDelaySamples: s.EncoderDelaySamples,
			EncoderPaddingSamples: s.EncoderPaddingSamples, DecodedSamples: s.DecodedSamples,
		})
	}
	return streams
}

func runFFmpeg(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("ffmpeg", append([]string{"-v", "error", "-y"}, args...)...) // #nosec G204 -- fixed test arguments and temp paths
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %q: %v: %s", args, err, out)
	}
}

// decodeMonoF32 decodes path's audio to mono 32-bit float samples at rate.
// The live encode's edit list starts at media time 0, so the decoder trims
// nothing and hands back the priming and padding a player trims by the
// reported delay and padding.
func decodeMonoF32(t *testing.T, path string, rate int) []float32 {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", path, "-map", "0:a:0", "-ac", "1", "-ar", strconv.Itoa(rate), "-f", "f32le", "-") // #nosec G204 -- test-owned temp path
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("decode %s: %v: %s", path, err, stderr.String())
	}
	samples := make([]float32, len(out)/4)
	for i := range samples {
		samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(out[4*i:]))
	}
	return samples
}

func longestSilentRun(samples []float32) int {
	longest, run := 0, 0
	for _, s := range samples {
		if math.Abs(float64(s)) < gaplessSilence {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

// TestSessionKeepsPrimingOnlyForPicturelessSources: a source with no frame
// size is encoded from its start without a seek, so the AAC priming the
// gapless trim counts survives; a video source keeps its seek.
func TestSessionKeepsPrimingOnlyForPicturelessSources(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		width, height int
		rung          string
		want          bool
	}{
		{0, 0, sourceRungID, true},
		{64, 64, "64p", false},
	} {
		transcoder := &fakeTranscoder{}
		sessions := newTestSessions(t, transcoder, t.TempDir(), nil)
		key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}
		if _, err := sessions.FilePath(context.Background(), key, "/src/piece.m4a", 120, tc.width, tc.height, false, tc.rung+"/seg00000.m4s"); err != nil {
			t.Fatalf("%dx%d segment: %v", tc.width, tc.height, err)
		}
		if err := sessions.Close(); err != nil {
			t.Fatal(err)
		}
		if len(transcoder.renditions) == 0 || transcoder.renditions[0].KeepPriming != tc.want {
			t.Fatalf("%dx%d renditions = %+v, want KeepPriming %v", tc.width, tc.height, transcoder.renditions, tc.want)
		}
	}
}

// TestHLSGaplessFollowsSelectedAudioTrack checks the trim a transcoded stream
// reports comes from the track the server maps, not the file's first audio
// stream: a selected 44.1 kHz AAC track must not carry the arithmetic of a
// 48 kHz TrueHD default ahead of it.
func TestHLSGaplessFollowsSelectedAudioTrack(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	path := filepath.Join(t.TempDir(), "tracks.mka")
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:           "library-local",
		Path:                path,
		Title:               "Two tracks",
		Container:           "matroska",
		Codec:               "truehd",
		Class:               store.MediaClassAudio,
		ContentFingerprint:  "fp-gapless-tracks",
		DurationSecondsHint: 2,
		Streams: []store.MediaFileStream{
			{Index: 0, Type: "audio", Codec: "truehd", Channels: 8, SampleRate: 48000, DecodedSamples: 96000, Default: true},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, SampleRate: 44100, DecodedSamples: 88200},
		},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	selected := 1
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: browserProfile, SelectedAudioIndex: &selected})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	want := GaplessInfo{SampleRate: 44100, EncoderDelaySamples: aacEncoderDelaySamples, EncoderPaddingSamples: aacPaddingSamples(88200)}
	for _, stream := range resp.Streams {
		if stream.Kind != "hls" || !strings.HasPrefix(stream.ID, "hls-") {
			continue
		}
		if stream.Gapless == nil || *stream.Gapless != want {
			t.Fatalf("HLS gapless = %+v, want %+v", stream.Gapless, want)
		}
		return
	}
	t.Fatalf("streams = %+v, want a transcoded HLS stream", resp.Streams)
}

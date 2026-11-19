package playback

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// TestPrimedAACGrid pins the layout to what FFmpeg's HLS segmenter writes for
// a primed AAC encode at 44.1 kHz on a 4 s grid: a new segment at the first
// frame at or past each grid line.
func TestPrimedAACGrid(t *testing.T) {
	t.Parallel()
	aac := ffmpeg.AudioProfile{Codec: "aac", Channels: 2}
	stream := func(decoded int64) store.MediaFileStream {
		return store.MediaFileStream{Type: "audio", SampleRate: 44100, DecodedSamples: decoded}
	}
	for _, tc := range []struct {
		decoded    int64
		segments   int
		tailFrames int
	}{
		{176400, 2, 1}, // 4.00 s: the last frame starts at 4.017 s
		{175518, 1, 0}, // 3.98 s: every frame starts before 4 s
		{171990, 1, 0}, // 3.90 s
		{352359, 3, 1}, // 7.99 s
		{352800, 3, 1}, // 8.00 s
	} {
		got := primedAACGrid(stream(tc.decoded), aac, segmentSeconds)
		if got.segments != tc.segments {
			t.Fatalf("decoded %d: %d segments, want %d", tc.decoded, got.segments, tc.segments)
		}
		if tc.tailFrames > 0 {
			if want := float64(tc.tailFrames*aacFrameSamples) / 44100; got.tailSeconds != want {
				t.Fatalf("decoded %d: tail %.6f s, want %.6f s", tc.decoded, got.tailSeconds, want)
			}
		}
	}
	for name, tc := range map[string]struct {
		stream  store.MediaFileStream
		profile ffmpeg.AudioProfile
	}{
		"unmeasured":  {store.MediaFileStream{Type: "audio", SampleRate: 44100}, aac},
		"resampled":   {store.MediaFileStream{Type: "audio", SampleRate: 192000, DecodedSamples: 768000}, aac},
		"not AAC":     {stream(176400), ffmpeg.AudioProfile{Codec: "ac3", Channels: 6}},
		"no stream":   {store.MediaFileStream{}, aac},
		"default AAC": {stream(176400), ffmpeg.AudioProfile{}},
		"normalized":  {stream(176400), ffmpeg.AudioProfile{Codec: "AAC"}},
	} {
		got := primedAACGrid(tc.stream, tc.profile, segmentSeconds)
		if wantSet := strings.Contains("default AAC normalized", name); (got.segments > 0) != wantSet {
			t.Fatalf("%s: grid %+v, want set %v", name, got, wantSet)
		}
	}
}

// TestTranscodedGaplessTailSegmentIsServed encodes a 4.0 s 44.1 kHz source
// through the live audio-only HLS session, fetches every segment its playlist
// lists, and checks the trimmed audio is exactly the source's 176,400 samples
// (CORE-1863). The encoder's priming and padding push its last frame past
// 4 s, so the encode writes two segments where the integer duration implies
// one.
func TestTranscodedGaplessTailSegmentIsServed(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	const (
		rate          = 44100
		sourceSamples = 4 * rate
	)
	dir := t.TempDir()
	tone := filepath.Join(dir, "tone.wav")
	runFFmpeg(t, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate="+strconv.Itoa(rate)+":duration=4", "-ac", "1", tone)
	source := filepath.Join(dir, "piece.m4a")
	runFFmpeg(t, "-i", tone, "-c:a", "aac", source)
	probe, err := ffmpeg.New().Probe(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	streams := gaplessStoreStreams(probe)
	if len(streams) == 0 || streams[0].DecodedSamples != sourceSamples {
		t.Fatalf("source streams = %+v, want %d decoded samples", streams, sourceSamples)
	}
	aac := ffmpeg.AudioProfile{Codec: "aac", Channels: 2}
	choices := []StreamChoice{{ID: "hls-piece", Kind: "hls"}}
	stampGapless(choices, streams, aac, 0)
	trim := choices[0].Gapless
	if trim == nil {
		t.Fatalf("no gapless trim for streams %+v", streams)
	}

	sessions := newTestSessions(t, ffmpeg.New(), t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-gapless-tail", MediaID: "media-piece"}
	shape := StreamShape{AudioOnly: true, AudioSource: streams[0]}
	fetch := func(name string) string {
		t.Helper()
		path, err := sessions.FilePathShaped(context.Background(), key, source, 4, 0, 0, false, false, ladderCaps{}, VideoSignal{}, -1, aac, nil, shape, PriorityNormal, audioOnlyRungID+"/"+name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return path
	}
	playlist, err := os.ReadFile(fetch("index.m3u8")) // #nosec G304,G703 -- path is beneath a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, line := range strings.Split(string(playlist), "\n") {
		if strings.HasSuffix(line, ".m4s") {
			listed = append(listed, line)
		}
	}
	if len(listed) != 2 {
		t.Fatalf("playlist lists %v, want both segments the encoder writes:\n%s", listed, playlist)
	}
	var stream []byte
	var segmentDir string
	for _, name := range listed {
		path := fetch(name)
		segmentDir = filepath.Dir(path)
		data, err := os.ReadFile(path) // #nosec G304,G703 -- path is beneath a test-owned temporary directory.
		if err != nil {
			t.Fatal(err)
		}
		stream = append(stream, data...)
	}
	initSegment, err := os.ReadFile(filepath.Join(segmentDir, initSegmentName)) // #nosec G304,G703 -- path is beneath a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	fmp4 := filepath.Join(dir, "served.mp4")
	if err := os.WriteFile(fmp4, append(initSegment, stream...), 0o600); err != nil { // #nosec G703 -- path is beneath a test-owned temporary directory.
		t.Fatal(err)
	}
	samples := decodeMonoF32(t, fmp4, rate)
	if got := len(samples) - trim.EncoderDelaySamples - trim.EncoderPaddingSamples; got != sourceSamples {
		t.Fatalf("served audio trims to %d samples (decoded %d, delay %d, padding %d), want %d", got, len(samples), trim.EncoderDelaySamples, trim.EncoderPaddingSamples, sourceSamples)
	}
}

// TestResumedPrimedEncodeServesTheTailSegment covers a primed session whose
// first encoder starts past segment zero, as a resume or far seek starts it.
// The 7.99 s source's playlist lists three segments; an encode started at 4 s
// with its own priming ends inside the second, so the tail must still be
// written and served.
func TestResumedPrimedEncodeServesTheTailSegment(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	const rate = 44100
	dir := t.TempDir()
	tone := filepath.Join(dir, "tone.wav")
	runFFmpeg(t, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate="+strconv.Itoa(rate)+":duration=7.99", "-ac", "1", tone)
	source := filepath.Join(dir, "piece.m4a")
	runFFmpeg(t, "-i", tone, "-c:a", "aac", source)
	probe, err := ffmpeg.New().Probe(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	streams := gaplessStoreStreams(probe)
	if len(streams) == 0 || streams[0].DecodedSamples <= 0 {
		t.Fatalf("source streams = %+v, want measured decoded samples", streams)
	}
	aac := ffmpeg.AudioProfile{Codec: "aac", Channels: 2}
	if grid := primedAACGrid(streams[0], aac, segmentSeconds); grid.segments != 3 {
		t.Fatalf("grid = %+v, want three segments", grid)
	}
	sessions := newTestSessions(t, ffmpeg.New(), t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-gapless-resume", MediaID: "media-piece"}
	shape := StreamShape{AudioOnly: true, AudioSource: streams[0]}
	for _, name := range []string{"index.m3u8", "seg00001.m4s", "seg00002.m4s"} {
		if _, err := sessions.FilePathShaped(context.Background(), key, source, 7, 0, 0, false, false, ladderCaps{}, VideoSignal{}, -1, aac, nil, shape, PriorityNormal, audioOnlyRungID+"/"+name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// A seek straight to the tail starts the first encoder there. The tail's
	// grid line (8 s) lies past the source's last sample, so the encode must
	// start earlier rather than seek past the end and write nothing.
	tailKey := SessionKey{SessionID: "session-gapless-tail-first", MediaID: "media-piece"}
	for _, name := range []string{"index.m3u8", "seg00002.m4s"} {
		if _, err := sessions.FilePathShaped(context.Background(), tailKey, source, 7, 0, 0, false, false, ladderCaps{}, VideoSignal{}, -1, aac, nil, shape, PriorityNormal, audioOnlyRungID+"/"+name); err != nil {
			t.Fatalf("tail first %s: %v", name, err)
		}
	}
}

func TestPrimedRestartSegmentReachesTheTail(t *testing.T) {
	const rate = 44100
	grid := primedAACGrid(store.MediaFileStream{Type: "audio", SampleRate: rate, DecodedSamples: 352359}, ffmpeg.AudioProfile{Codec: "aac"}, segmentSeconds)
	if grid.segments != 3 {
		t.Fatalf("grid = %+v, want three segments", grid)
	}
	// Segment 2's grid line (8 s, sample 352800) lies past the last decoded
	// sample, and an encode started at segment 1 ends a segment short, so
	// both restart from zero.
	for _, tc := range []struct{ asked, want int }{{0, 0}, {1, 0}, {2, 0}, {3, 3}} {
		if got := grid.restartSegment(tc.asked); got != tc.want {
			t.Errorf("restartSegment(%d) = %d, want %d", tc.asked, got, tc.want)
		}
	}
	long := primedAACGrid(store.MediaFileStream{Type: "audio", SampleRate: rate, DecodedSamples: 60 * rate}, ffmpeg.AudioProfile{Codec: "aac"}, segmentSeconds)
	if got := long.restartSegment(5); got != 5 {
		t.Errorf("long restartSegment(5) = %d, want 5 (no step back away from the tail)", got)
	}
	if got := (primedGrid{}).restartSegment(4); got != 4 {
		t.Errorf("unprimed restartSegment(4) = %d, want 4", got)
	}
}

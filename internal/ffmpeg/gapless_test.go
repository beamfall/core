package ffmpeg

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"
)

// The fixtures below mirror ffprobe 9's output for the three containers the
// gapless probe reads, synthesized from a 2 s 44.1 kHz (48 kHz for Opus) tone.

// gaplessM4AFixture is an AAC M4A: the MP4 edit list starts the first packet
// at pts -1024 with skip_samples 1024; the last packet is already shortened
// by its padding.
const gaplessM4AFixture = `{
  "packets": [
    {"stream_index": 0, "pts": -1024, "duration": 1024, "side_data_list": [{"skip_samples": 1024, "discard_padding": 0}]},
    {"stream_index": 0, "pts": 0, "duration": 1024},
    {"stream_index": 0, "pts": 87040, "duration": 1024},
    {"stream_index": 0, "pts": 88064, "duration": 136, "side_data_list": [{"skip_samples": 0, "discard_padding": 888}]}
  ],
  "streams": [{"index": 0, "sample_rate": "44100", "time_base": "1/44100"}]
}`

// gaplessMP3Fixture is a LAME MP3: the first packet starts at pts 0 and the
// last stays a whole frame, so the padding must be subtracted again.
const gaplessMP3Fixture = `{
  "packets": [
    {"stream_index": 0, "pts": 0, "duration": 368640, "side_data_list": [{"skip_samples": 1105, "discard_padding": 0}]},
    {"stream_index": 0, "pts": 368640, "duration": 368640},
    {"stream_index": 0, "pts": 28016640, "duration": 368640},
    {"stream_index": 0, "pts": 28385280, "duration": 368640, "side_data_list": [{"skip_samples": 0, "discard_padding": 551}]}
  ],
  "streams": [{"index": 0, "sample_rate": "44100", "time_base": "1/14112000"}]
}`

// gaplessOpusFixture is an Ogg Opus file: pre-skip 312, end trimmed by the
// granule position.
const gaplessOpusFixture = `{
  "packets": [
    {"stream_index": 0, "pts": -312, "duration": 960, "side_data_list": [{"skip_samples": 312, "discard_padding": 0}]},
    {"stream_index": 0, "pts": 648, "duration": 960},
    {"stream_index": 0, "pts": 95688, "duration": 312, "side_data_list": [{"skip_samples": 0, "discard_padding": 648}]}
  ],
  "streams": [{"index": 0, "sample_rate": "48000", "time_base": "1/48000"}]
}`

func TestParseGapless(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		want gaplessFacts
	}{
		{"m4a", gaplessM4AFixture, gaplessFacts{SampleRate: 44100, DelaySamples: 1024, PaddingSamples: 888, DecodedSamples: 88200}},
		{"mp3", gaplessMP3Fixture, gaplessFacts{SampleRate: 44100, DelaySamples: 1105, PaddingSamples: 551, DecodedSamples: 88200}},
		{"opus", gaplessOpusFixture, gaplessFacts{SampleRate: 48000, DelaySamples: 312, PaddingSamples: 648, DecodedSamples: 96000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts, err := parseGapless([]byte(tc.doc))
			if err != nil {
				t.Fatal(err)
			}
			if got := facts[0]; got != tc.want {
				t.Fatalf("facts = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseGaplessUndeclaredAndMalformed(t *testing.T) {
	// A stream with no side data declares neither; a stream whose time base
	// is unusable or which has no packets yields no facts at all.
	facts, err := parseGapless([]byte(`{
  "packets": [
    {"stream_index": 1, "pts": 0, "duration": 1152},
    {"stream_index": 1, "pts": 1152, "duration": 1152},
    {"stream_index": 2, "pts": 0, "duration": 1152}
  ],
  "streams": [
    {"index": 1, "sample_rate": "44100", "time_base": "1/44100"},
    {"index": 2, "sample_rate": "44100", "time_base": "0/1"},
    {"index": 3, "sample_rate": "44100", "time_base": "1/44100"}
  ]
}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := facts[1], (gaplessFacts{SampleRate: 44100, DecodedSamples: 2304}); got != want {
		t.Fatalf("undeclared stream = %+v, want %+v", got, want)
	}
	if _, ok := facts[2]; ok {
		t.Fatal("a stream with an unusable time base must yield no facts")
	}
	if _, ok := facts[3]; ok {
		t.Fatal("a stream with no packets must yield no facts")
	}
	if _, err := parseGapless([]byte("not json")); err == nil {
		t.Fatal("malformed probe output must be an error")
	}
}

func TestParseITunSMPB(t *testing.T) {
	for _, tc := range []struct {
		tag            string
		delay, padding int64
		ok             bool
	}{
		{" 00000000 00000840 000001C0 0000000000046E00", 0x840, 0x1c0, true},
		{"00000000 00000400 00000378", 1024, 888, true},
		{"00000000 00000000 00000000", 0, 0, false},
		{"00000000 00000840", 0, 0, false},
		{"00000000 zz 000001C0", 0, 0, false},
		{"00000000 00100000 000001C0", 0, 0, false},
		{"", 0, 0, false},
	} {
		delay, padding, ok := parseITunSMPB(tc.tag)
		if delay != tc.delay || padding != tc.padding || ok != tc.ok {
			t.Fatalf("parseITunSMPB(%q) = %d, %d, %v; want %d, %d, %v", tc.tag, delay, padding, ok, tc.delay, tc.padding, tc.ok)
		}
	}
}

func TestApplyGapless(t *testing.T) {
	newProbe := func(tag string) Probe {
		probe := Probe{Streams: []ProbeStream{
			{Index: 0, Type: "video"},
			{Index: 1, Type: "audio"},
			{Index: 2, Type: "audio"},
		}}
		if tag != "" {
			probe.Tags = map[string]string{"itunsmpb": tag}
		}
		return probe
	}
	facts := map[int]gaplessFacts{
		1: {SampleRate: 44100, DelaySamples: 1024, PaddingSamples: 888, DecodedSamples: 88200},
		2: {SampleRate: 48000, DelaySamples: 312, PaddingSamples: 648, DecodedSamples: 96000},
	}

	probe := newProbe("")
	applyGapless(&probe, facts)
	if s := probe.Streams[0]; s.SampleRate != 0 || s.EncoderDelaySamples != 0 {
		t.Fatalf("video stream got gapless facts: %+v", s)
	}
	if s := probe.Streams[1]; s.SampleRate != 44100 || s.EncoderDelaySamples != 1024 || s.EncoderPaddingSamples != 888 || s.DecodedSamples != 88200 {
		t.Fatalf("first audio = %+v", s)
	}
	if s := probe.Streams[2]; s.SampleRate != 48000 || s.EncoderDelaySamples != 312 || s.EncoderPaddingSamples != 648 || s.DecodedSamples != 96000 {
		t.Fatalf("second audio = %+v", s)
	}

	// A tag agreeing on the delay overrides the padding and keeps the length.
	probe = newProbe("00000000 00000400 00000300")
	applyGapless(&probe, facts)
	if s := probe.Streams[1]; s.EncoderDelaySamples != 1024 || s.EncoderPaddingSamples != 768 || s.DecodedSamples != 88200 {
		t.Fatalf("agreeing tag: %+v", s)
	}
	// A tag disagreeing on the delay wins, and the measured length is dropped;
	// it never applies beyond the first audio stream.
	probe = newProbe("00000000 00000840 000001C0")
	applyGapless(&probe, facts)
	if s := probe.Streams[1]; s.EncoderDelaySamples != 0x840 || s.EncoderPaddingSamples != 0x1c0 || s.DecodedSamples != 0 {
		t.Fatalf("disagreeing tag: %+v", s)
	}
	if s := probe.Streams[2]; s.EncoderDelaySamples != 312 || s.DecodedSamples != 96000 {
		t.Fatalf("tag leaked to the second audio stream: %+v", s)
	}
}

func TestGaplessArgsReadsHeadAndTailOfLongFiles(t *testing.T) {
	if args := gaplessArgs("/lib/a.m4a", 10); slices.Contains(args, "-read_intervals") {
		t.Fatalf("a short file must be read whole: %q", args)
	}
	args := gaplessArgs("/lib/a.m4a", 300)
	if !containsPair(args, "-read_intervals", "%+#16,295%") || !containsPair(args, "-select_streams", "a") {
		t.Fatalf("long file args = %q", args)
	}
	if args[len(args)-2] != "--" || args[len(args)-1] != "/lib/a.m4a" {
		t.Fatalf("the path must follow --: %q", args)
	}
}

func TestSegmentArgsKeepPrimingOmitsZeroSeek(t *testing.T) {
	audio := Rendition{AudioOnly: true, AudioCodec: "aac", AudioChannels: 2, KeepPriming: true}
	if args := hlsSegmentArgs("/lib/a.m4a", "/d", 0, 4, audio); slices.Contains(args, "-ss") {
		t.Fatalf("segment 0 with KeepPriming must not seek: %q", args)
	}
	if args := hlsSegmentArgs("/lib/a.m4a", "/d", 2, 4, audio); !containsPair(args, "-ss", "8") {
		t.Fatalf("a later segment must still seek: %q", args)
	}
	if args := keyframeSegmentArgs("/lib/a.m4a", "/d", 0, 0, audio); slices.Contains(args, "-ss") {
		t.Fatalf("a keyframe start at 0 with KeepPriming must not seek: %q", args)
	}
	if args := keyframeSegmentArgs("/lib/a.m4a", "/d", 1, 6*time.Second, audio); !slices.Contains(args, "-ss") {
		t.Fatalf("a later keyframe start must still seek: %q", args)
	}
	audio.KeepPriming = false
	if args := hlsSegmentArgs("/lib/a.m4a", "/d", 0, 4, audio); !containsPair(args, "-ss", "0") {
		t.Fatalf("without KeepPriming segment 0 keeps its seek: %q", args)
	}
}

// TestProbeGaplessRealEncodes encodes one tone with each gapless-declaring
// encoder FFmpeg ships and checks the probe reports the delay, the padding
// and the exact content length.
func TestProbeGaplessRealEncodes(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	const seconds = 2
	for _, tc := range []struct {
		name, encoder string
		rate          int
	}{
		{"tone.m4a", "aac", 44100},
		{"tone.mp3", "libmp3lame", 44100},
		{"tone.opus", "libopus", 48000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), tc.name)
			cmd := exec.Command("ffmpeg", "-v", "error", // #nosec G204 -- test-owned encoder and temp paths
				"-f", "lavfi", "-i", "sine=frequency=440:sample_rate="+strconv.Itoa(tc.rate)+":duration="+strconv.Itoa(seconds),
				"-c:a", tc.encoder, "-y", src)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Skipf("encoder %s unavailable: %v: %s", tc.encoder, err, out)
			}
			probe, err := New().Probe(context.Background(), src)
			if err != nil {
				t.Fatal(err)
			}
			var audio *ProbeStream
			for i := range probe.Streams {
				if probe.Streams[i].Type == "audio" {
					audio = &probe.Streams[i]
					break
				}
			}
			if audio == nil {
				t.Fatalf("no audio stream in %+v", probe.Streams)
			}
			if audio.SampleRate != tc.rate || audio.EncoderDelaySamples <= 0 || audio.EncoderPaddingSamples <= 0 {
				t.Fatalf("gapless facts = %+v", audio)
			}
			if want := int64(seconds * tc.rate); audio.DecodedSamples != want {
				t.Fatalf("DecodedSamples = %d, want %d", audio.DecodedSamples, want)
			}
		})
	}
}

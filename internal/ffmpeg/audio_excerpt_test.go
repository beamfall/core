package ffmpeg

import (
	"context"
	"encoding/binary"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAudioExcerptRejectsUnboundedRequests(t *testing.T) {
	cases := []struct {
		name                     string
		start, seconds           float64
		sampleRate, channelCount int
	}{
		{"negative start", -1, 10, 22050, 2},
		{"zero length", 0, 0, 22050, 2},
		{"too long", 0, maxAudioExcerptSeconds + 1, 22050, 2},
		{"low rate", 0, 10, 4000, 2},
		{"high rate", 0, 10, 96000, 2},
		{"no channels", 0, 10, 22050, 0},
		{"too many channels", 0, 10, 22050, 6},
		{"over capture cap", 0, maxAudioExcerptSeconds, maxAudioExcerptSampleRate, 2},
		{"nan start", math.NaN(), 10, 22050, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New().AudioExcerpt(context.Background(), "/tmp/x.flac", tc.start, tc.seconds, tc.sampleRate, tc.channelCount); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

func TestDecodeFloat32LEDropsPartialFrameAndRejectsNonFinite(t *testing.T) {
	raw := make([]byte, 0, 20)
	for _, v := range []float32{0.5, -0.25, 1, 0} {
		raw = binary.LittleEndian.AppendUint32(raw, math.Float32bits(v))
	}
	raw = append(raw, 1, 2, 3, 4) // half of a stereo frame
	got, err := decodeFloat32LE(raw, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0] != 0.5 || got[1] != -0.25 {
		t.Fatalf("got %v", got)
	}
	bad := binary.LittleEndian.AppendUint32(nil, math.Float32bits(float32(math.Inf(1))))
	if _, err := decodeFloat32LE(bad, 1); err == nil {
		t.Fatal("expected a non-finite sample error")
	}
}

func TestAudioExcerptRealFFmpeg(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	src := filepath.Join(t.TempDir(), "tone.wav")
	cmd := exec.Command(ffmpegPath, //nolint:gosec // test-owned path inside t.TempDir
		"-v", "error",
		"-f", "lavfi",
		"-i", "sine=frequency=440:sample_rate=44100:duration=4",
		"-y", src,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate fixture: %v: %s", err, output)
	}
	got, err := New().AudioExcerpt(context.Background(), src, 1, 2, 22050, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := 2 * 22050 * 2
	if diff := len(got) - want; diff < -2*1024 || diff > 2*1024 {
		t.Fatalf("samples = %d, want about %d", len(got), want)
	}
	var peak float32
	for _, v := range got {
		peak = max(peak, float32(math.Abs(float64(v))))
	}
	if peak < 0.05 || peak > 1.01 { // lavfi sine is 1/8 full scale, panned to stereo
		t.Fatalf("peak = %v, want a decoded tone", peak)
	}
	if _, err := New().AudioExcerpt(context.Background(), src, 30, 2, 22050, 2); err == nil {
		t.Fatal("expected an error for a window past the end")
	}
}

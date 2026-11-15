package encderiv

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"io"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
)

// TestRunCaptureGeneratesAssetsWithRealFFmpeg runs each generated-asset pipe
// variant (CORE-1966) against an encrypted moov-at-end source served through
// RunCapture, and checks the outputs parse and nothing lands in the temp dir.
func TestRunCaptureGeneratesAssetsWithRealFFmpeg(t *testing.T) {
	fixture := ffmpegFixture(t, false)
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	runner := ffmpeg.New()
	capture := func(t *testing.T, maxBytes int64, run func(context.Context, ffmpeg.PipeInput, io.Writer) error) []byte {
		t.Helper()
		out, err := RunCapture(ctx, Capture{
			Open:     encryptedSource(t, fixture),
			Size:     int64(len(fixture)),
			MaxBytes: maxBytes,
			Revoked:  func(context.Context) error { return nil },
			Transcode: func(ctx context.Context, in Input, w io.Writer) error {
				return run(ctx, ffmpeg.PipeInput{URL: in.URL, Secret: in.Secret}, w)
			},
		})
		if errors.Is(err, ffmpeg.ErrPipeConfinementUnavailable) {
			t.Skip(err)
		}
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	t.Run("sprite sheets", func(t *testing.T) {
		out := capture(t, 8<<20, func(ctx context.Context, in ffmpeg.PipeInput, w io.Writer) error {
			return runner.SpriteSheetsToPipe(ctx, in, 1, 80, 3, 2, w)
		})
		sheets, err := ffmpeg.SplitJPEGStream(out, 4)
		if err != nil {
			t.Fatal(err)
		}
		// 12 one-second frames on a 3x2 grid make two full sheets.
		if len(sheets) != 2 {
			t.Fatalf("sheets = %d, want 2", len(sheets))
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(sheets[0]))
		if err != nil || cfg.Width != 240 || cfg.Height != 120 {
			t.Fatalf("sheet = %+v, %v; want 240x120", cfg, err)
		}
	})
	t.Run("frame grab", func(t *testing.T) {
		out := capture(t, 4<<20, func(ctx context.Context, in ffmpeg.PipeInput, w io.Writer) error {
			return runner.FrameGrabToPipe(ctx, in, 6, 80, w)
		})
		frames, err := ffmpeg.SplitJPEGStream(out, 1)
		if err != nil || len(frames) != 1 {
			t.Fatalf("frames = %d, %v; want 1", len(frames), err)
		}
		if cfg, err := jpeg.DecodeConfig(bytes.NewReader(frames[0])); err != nil || cfg.Width != 80 {
			t.Fatalf("frame = %+v, %v; want width 80", cfg, err)
		}
	})
	t.Run("sample clip", func(t *testing.T) {
		out := capture(t, 16<<20, func(ctx context.Context, in ffmpeg.PipeInput, w io.Writer) error {
			return runner.SampleClipToPipe(ctx, in, []float64{1, 6}, 1.5, 80, 12, w)
		})
		var types []string
		for _, box := range topLevelBoxes(t, out) {
			types = append(types, box.typ)
		}
		if len(types) < 3 || types[0] != "ftyp" || types[1] != "moov" || !slices.Contains(types, "moof") {
			t.Fatalf("clip boxes = %v, want a fragmented MP4", types)
		}
	})
	t.Run("waveform", func(t *testing.T) {
		var amps []float64
		capture(t, 1, func(ctx context.Context, in ffmpeg.PipeInput, _ io.Writer) (err error) {
			amps, err = runner.WaveformAmplitudesFromPipe(ctx, in)
			return err
		})
		if len(amps) == 0 || amps[len(amps)/2] <= 0 {
			t.Fatalf("waveform = %d windows, want a non-silent level", len(amps))
		}
	})
	t.Run("silence", func(t *testing.T) {
		var spans [][2]float64
		capture(t, 1, func(ctx context.Context, in ffmpeg.PipeInput, _ io.Writer) (err error) {
			spans, err = runner.SilenceSpansFromPipe(ctx, in)
			return err
		})
		if len(spans) != 0 {
			t.Fatalf("silence spans in a continuous tone: %v", spans)
		}
	})
	t.Run("loudness", func(t *testing.T) {
		var integrated, peak float64
		capture(t, 1, func(ctx context.Context, in ffmpeg.PipeInput, _ io.Writer) (err error) {
			integrated, _, peak, err = runner.LoudnessMeasureFromPipe(ctx, in)
			return err
		})
		if math.IsNaN(integrated) || integrated > -5 || integrated < -40 || peak > 0 {
			t.Fatalf("loudness = %v LUFS, peak %v dBTP; want a measured tone", integrated, peak)
		}
	})
	assertNoFiles(t, tmpDir)
}

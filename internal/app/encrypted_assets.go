package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/beamfall/core/internal/encderiv"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/genassets"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/mediahttp"
)

// assetPipeRunner is the FFmpeg seam the encrypted asset generator drives.
// Satisfied by *ffmpeg.Runner.
type assetPipeRunner interface {
	SpriteSheetsToPipe(ctx context.Context, in ffmpeg.PipeInput, intervalSeconds, tileWidth, columns, rows int, stdout io.Writer) error
	FrameGrabToPipe(ctx context.Context, in ffmpeg.PipeInput, atSeconds float64, width int, stdout io.Writer) error
	SampleClipToPipe(ctx context.Context, in ffmpeg.PipeInput, offsets []float64, segmentSeconds float64, width, maxFPS int, stdout io.Writer) error
	WaveformAmplitudesFromPipe(ctx context.Context, in ffmpeg.PipeInput) ([]float64, error)
	SilenceSpansFromPipe(ctx context.Context, in ffmpeg.PipeInput) ([][2]float64, error)
	LoudnessMeasureFromPipe(ctx context.Context, in ffmpeg.PipeInput) (integratedLUFS, loudnessRangeLU, truePeakDBTP float64, err error)
}

// Output bounds for the encrypted asset kinds. A sprite sheet is a 10x10 grid
// of 160px-wide tiles and a marker thumbnail a single 320px frame; the preview
// is a short low-resolution clip. Analyses write nothing to stdout.
const (
	encryptedSheetMaxBytes    = 4 << 20
	encryptedThumbMaxBytes    = 4 << 20
	encryptedPreviewMaxBytes  = 64 << 20
	encryptedAnalysisMaxBytes = 1
)

// encryptedAssetGenerator derives an Unsealed Encrypted Library's Generated
// Assets through the shared encrypted-derivative helper (ADR-0235): encderiv
// serves the decrypted source on a per-item loopback endpoint, FFmpeg reads it
// there and writes the asset to stdout (or parses its analysis from stderr),
// and everything stays in memory until genassets encrypts it.
type encryptedAssetGenerator struct {
	content mediahttp.EncryptedByteReadSeeker
	runner  assetPipeRunner
}

var _ genassets.EncryptedGenerator = encryptedAssetGenerator{}

func (g encryptedAssetGenerator) SpriteSheets(ctx context.Context, src genassets.EncryptedSource, intervalSeconds, tileWidth, columns, rows, maxSheets int) ([][]byte, error) {
	if maxSheets <= 0 {
		return nil, errors.New("encrypted sprite sheets need a bounded count")
	}
	out, err := g.capture(ctx, src, int64(maxSheets)*encryptedSheetMaxBytes, func(ctx context.Context, in ffmpeg.PipeInput, stdout io.Writer) error {
		return g.runner.SpriteSheetsToPipe(ctx, in, intervalSeconds, tileWidth, columns, rows, stdout)
	})
	if err != nil {
		return nil, err
	}
	return ffmpeg.SplitJPEGStream(out, maxSheets)
}

func (g encryptedAssetGenerator) FrameGrab(ctx context.Context, src genassets.EncryptedSource, atSeconds float64, width int) ([]byte, error) {
	out, err := g.capture(ctx, src, encryptedThumbMaxBytes, func(ctx context.Context, in ffmpeg.PipeInput, stdout io.Writer) error {
		return g.runner.FrameGrabToPipe(ctx, in, atSeconds, width, stdout)
	})
	if err != nil {
		return nil, err
	}
	images, err := ffmpeg.SplitJPEGStream(out, 1)
	if err != nil {
		return nil, err
	}
	if len(images) != 1 {
		return nil, fmt.Errorf("frame grab produced %d images, want 1", len(images))
	}
	return images[0], nil
}

func (g encryptedAssetGenerator) SampleClip(ctx context.Context, src genassets.EncryptedSource, offsets []float64, segmentSeconds float64, width, maxFPS int) ([]byte, error) {
	out, err := g.capture(ctx, src, encryptedPreviewMaxBytes, func(ctx context.Context, in ffmpeg.PipeInput, stdout io.Writer) error {
		return g.runner.SampleClipToPipe(ctx, in, offsets, segmentSeconds, width, maxFPS, stdout)
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("sample clip produced no output")
	}
	return out, nil
}

func (g encryptedAssetGenerator) WaveformAmplitudes(ctx context.Context, src genassets.EncryptedSource) ([]float64, error) {
	var samples []float64
	err := g.analyse(ctx, src, func(ctx context.Context, in ffmpeg.PipeInput) (err error) {
		samples, err = g.runner.WaveformAmplitudesFromPipe(ctx, in)
		return err
	})
	return samples, err
}

func (g encryptedAssetGenerator) SilenceSpans(ctx context.Context, src genassets.EncryptedSource) ([][2]float64, error) {
	var spans [][2]float64
	err := g.analyse(ctx, src, func(ctx context.Context, in ffmpeg.PipeInput) (err error) {
		spans, err = g.runner.SilenceSpansFromPipe(ctx, in)
		return err
	})
	return spans, err
}

func (g encryptedAssetGenerator) LoudnessMeasure(ctx context.Context, src genassets.EncryptedSource) (integratedLUFS, loudnessRangeLU, truePeakDBTP float64, err error) {
	err = g.analyse(ctx, src, func(ctx context.Context, in ffmpeg.PipeInput) (err error) {
		integratedLUFS, loudnessRangeLU, truePeakDBTP, err = g.runner.LoudnessMeasureFromPipe(ctx, in)
		return err
	})
	return integratedLUFS, loudnessRangeLU, truePeakDBTP, err
}

// analyse runs an analysis whose result is parsed by the tool call itself; it
// writes nothing to stdout, so the capture bound is minimal. The result is
// discarded by the caller when the capture fails or is revoked.
func (g encryptedAssetGenerator) analyse(ctx context.Context, src genassets.EncryptedSource, run func(context.Context, ffmpeg.PipeInput) error) error {
	_, err := g.capture(ctx, src, encryptedAnalysisMaxBytes, func(ctx context.Context, in ffmpeg.PipeInput, _ io.Writer) error {
		return run(ctx, in)
	})
	return err
}

// capture serves src's decrypted bytes to one tool run through encderiv and
// returns its stdout, bounded by maxBytes.
func (g encryptedAssetGenerator) capture(ctx context.Context, src genassets.EncryptedSource, maxBytes int64, run func(context.Context, ffmpeg.PipeInput, io.Writer) error) ([]byte, error) {
	if g.content == nil || g.runner == nil {
		return nil, errors.New("encrypted asset generation is not configured")
	}
	if src.Revoked == nil {
		return nil, errors.New("encrypted asset generation needs a revocation check")
	}
	open := func() (io.ReadSeekCloser, error) {
		return g.content.OpenDecryptReader(src.LibraryID, src.Path, string(libencrypt.ObjectClassMediaBytes), src.Fingerprint)
	}
	size, err := plaintextSize(open)
	if err != nil {
		return nil, err
	}
	return encderiv.RunCapture(ctx, encderiv.Capture{
		Open:     open,
		Size:     size,
		MaxBytes: maxBytes,
		Revoked:  src.Revoked,
		Transcode: func(ctx context.Context, in encderiv.Input, stdout io.Writer) error {
			return run(ctx, ffmpeg.PipeInput{URL: in.URL, Secret: in.Secret}, stdout)
		},
	})
}

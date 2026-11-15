package app

import (
	"context"
	"fmt"
	"io"

	"github.com/beamfall/core/internal/encderiv"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
)

// offlinePackPresets maps each reduced offline pack quality to its FFmpeg
// preset. Original quality never transcodes, so it has no preset.
var offlinePackPresets = map[string]string{
	offlinepack.QualityHigh:   "pack-1080p",
	offlinepack.QualityMedium: "pack-720p",
	offlinepack.QualityLow:    "pack-480p",
}

// videoConverter is the slice of the FFmpeg runner the pack transcoder uses.
type videoConverter interface {
	Convert(ctx context.Context, src, dst string, profile ffmpeg.ConvertProfile) error
}

// pipeTranscoder is the slice of the FFmpeg runner the encrypted-library pack
// transcoder uses.
type pipeTranscoder interface {
	TranscodeToPipe(ctx context.Context, in ffmpeg.PipeInput, profile ffmpeg.ConvertProfile, stdout io.Writer) error
}

// offlinePackProfile is the preset for quality, carrying the source's video
// signal so an HDR source is tone-mapped to SDR BT.709 by the live
// transcode's chain (CORE-1744).
func offlinePackProfile(quality string, streams []store.MediaFileStream) (ffmpeg.ConvertProfile, error) {
	profile, ok := ffmpeg.PackProfileByName(offlinePackPresets[quality])
	if !ok {
		return ffmpeg.ConvertProfile{}, fmt.Errorf("no offline pack preset for quality %q", quality)
	}
	signal := playback.VideoSignalFromStreams(streams)
	return profile.WithSourceVideo(signal.ColorTransfer, signal.DoViProfile), nil
}

// offlinePackTranscoder adapts the FFmpeg runner to the offline pack service's
// Transcoder, refusing a quality with no preset.
func offlinePackTranscoder(runner videoConverter) offlinepack.Transcoder {
	return func(ctx context.Context, src, dst, quality string, streams []store.MediaFileStream) error {
		profile, err := offlinePackProfile(quality, streams)
		if err != nil {
			return err
		}
		return runner.Convert(ctx, src, dst, profile)
	}
}

// offlinePackPipeTranscoder adapts the FFmpeg runner to the offline pack
// service's PipeTranscoder for Encrypted Libraries (ADR-0235): FFmpeg reads the
// decrypted source from the loopback endpoint and writes a fragmented MP4 to
// stdout under the same presets as offlinePackTranscoder.
func offlinePackPipeTranscoder(runner pipeTranscoder) offlinepack.PipeTranscoder {
	return func(ctx context.Context, input encderiv.Input, stdout io.Writer, quality string, streams []store.MediaFileStream) error {
		profile, err := offlinePackProfile(quality, streams)
		if err != nil {
			return err
		}
		return runner.TranscodeToPipe(ctx, ffmpeg.PipeInput{URL: input.URL, Secret: input.Secret}, profile, stdout)
	}
}

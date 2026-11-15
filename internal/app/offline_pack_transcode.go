package app

import (
	"context"
	"fmt"

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

// offlinePackTranscoder adapts the FFmpeg runner to the offline pack service's
// Transcoder, refusing a quality with no preset. The source's video signal
// rides on the preset, so an HDR source is tone-mapped to SDR BT.709 by the
// live transcode's chain (CORE-1744).
func offlinePackTranscoder(runner videoConverter) offlinepack.Transcoder {
	return func(ctx context.Context, src, dst, quality string, streams []store.MediaFileStream) error {
		profile, ok := ffmpeg.PackProfileByName(offlinePackPresets[quality])
		if !ok {
			return fmt.Errorf("no offline pack preset for quality %q", quality)
		}
		signal := playback.VideoSignalFromStreams(streams)
		return runner.Convert(ctx, src, dst, profile.WithSourceVideo(signal.ColorTransfer, signal.DoViProfile))
	}
}

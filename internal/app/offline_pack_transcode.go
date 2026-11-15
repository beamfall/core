package app

import (
	"context"
	"fmt"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/offlinepack"
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
// Transcoder, refusing a quality with no preset.
func offlinePackTranscoder(runner videoConverter) offlinepack.Transcoder {
	return func(ctx context.Context, src, dst, quality string) error {
		profile, ok := ffmpeg.PackProfileByName(offlinePackPresets[quality])
		if !ok {
			return fmt.Errorf("no offline pack preset for quality %q", quality)
		}
		return runner.Convert(ctx, src, dst, profile)
	}
}

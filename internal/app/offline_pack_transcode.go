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
// live transcode's chain (CORE-1744). The stream the signal is read from is
// mapped explicitly, so the tone-map decision always describes the stream
// FFmpeg encodes (CORE-1850).
func offlinePackTranscoder(runner videoConverter) offlinepack.Transcoder {
	return func(ctx context.Context, src, dst, quality string, streams []store.MediaFileStream) error {
		profile, ok := ffmpeg.PackProfileByName(offlinePackPresets[quality])
		if !ok {
			return fmt.Errorf("no offline pack preset for quality %q", quality)
		}
		video, ok := packVideoStream(streams)
		if !ok {
			return runner.Convert(ctx, src, dst, profile)
		}
		signal := playback.VideoSignalFromStreams([]store.MediaFileStream{video})
		profile = profile.WithSourceVideo(signal.ColorTransfer, signal.DoViProfile).WithSourceVideoStream(video.Index)
		return runner.Convert(ctx, src, dst, profile)
	}
}

// packVideoStream picks the video stream a pack transcode encodes: the first
// one flagged default, else the first video stream. A source that pairs a
// Dolby Vision profile 5 stream with an SDR default then encodes the SDR
// stream instead of refusing on the stream it would not have used.
func packVideoStream(streams []store.MediaFileStream) (store.MediaFileStream, bool) {
	first, found := store.MediaFileStream{}, false
	for _, stream := range streams {
		if stream.Type != "video" {
			continue
		}
		if stream.Default {
			return stream, true
		}
		if !found {
			first, found = stream, true
		}
	}
	return first, found
}

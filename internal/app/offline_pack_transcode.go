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
	Probe(ctx context.Context, path string) (ffmpeg.Probe, error)
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
// Transcoder, refusing a quality with no preset. The source's video signal
// rides on the preset, so an HDR source is tone-mapped to SDR BT.709 by the
// live transcode's chain (CORE-1744). The stream the signal is read from is
// mapped explicitly, so the tone-map decision always describes the stream
// FFmpeg encodes (CORE-1850). A source whose inventory records no video stream
// (scanned without a probe) is probed first, so an HDR source is not taken
// for SDR (CORE-1845).
func offlinePackTranscoder(runner videoConverter) offlinepack.Transcoder {
	return func(ctx context.Context, src, dst, quality string, streams []store.MediaFileStream) error {
		profile, err := offlinePackProfile(quality, streams)
		if err != nil {
			return err
		}
		video, ok := packVideoStream(streams)
		if !ok {
			probe, err := runner.Probe(ctx, src)
			if err != nil {
				return fmt.Errorf("probe offline pack source: %w", err)
			}
			video, ok = packVideoStream(probedVideoStreams(probe.Streams))
		}
		if !ok {
			return runner.Convert(ctx, src, dst, profile)
		}
		signal := playback.VideoSignalFromStreams([]store.MediaFileStream{video})
		profile = profile.WithSourceVideo(signal.ColorTransfer, signal.DoViProfile).WithSourceVideoStream(video.Index)
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
		if video, ok := packVideoStream(streams); ok {
			signal := playback.VideoSignalFromStreams([]store.MediaFileStream{video})
			profile = profile.WithSourceVideo(signal.ColorTransfer, signal.DoViProfile).WithSourceVideoStream(video.Index)
		}
		return runner.TranscodeToPipe(ctx, ffmpeg.PipeInput{URL: input.URL, Secret: input.Secret}, profile, stdout)
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

// probedVideoStreams maps an on-demand probe's video streams onto the store's
// stream rows, keeping only what the pack transcode decides on. Embedded cover
// art is skipped, as the scan's inventory skips it.
func probedVideoStreams(streams []ffmpeg.ProbeStream) []store.MediaFileStream {
	var out []store.MediaFileStream
	for _, stream := range streams {
		if stream.Type != "video" || stream.AttachedPicture {
			continue
		}
		out = append(out, store.MediaFileStream{
			Index: stream.Index, Type: stream.Type, Codec: stream.Codec, Default: stream.Default,
			ColorTransfer: stream.ColorTransfer, ColorPrimaries: stream.ColorPrimaries,
			DoViProfile: stream.DoViProfile, DoViBLCompatID: stream.DoViBLCompatID,
		})
	}
	return out
}

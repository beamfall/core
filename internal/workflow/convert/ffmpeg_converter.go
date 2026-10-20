package convert

import (
	"context"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// runner is the subset of *ffmpeg.Runner this adapter needs (one transcode op),
// kept as an interface so the workflow is testable without a real FFmpeg binary.
type runner interface {
	Convert(ctx context.Context, src, dst string, profile ffmpeg.ConvertProfile) error
}

// FFmpegConverter adapts the core FFmpeg Runner to the store's
// ManagedFileConverter seam: it resolves a named preset to a ConvertProfile,
// runs the transcode as a least-privilege child, and reports the output
// container so the store names the admitted file. An unknown profile is
// rejected before any FFmpeg run.
type FFmpegConverter struct {
	runner runner
}

// NewFFmpegConverter wires the adapter over a core FFmpeg Runner.
func NewFFmpegConverter(r *ffmpeg.Runner) FFmpegConverter {
	return FFmpegConverter{runner: r}
}

// ConvertFile implements store.ManagedFileConverter.
func (c FFmpegConverter) ConvertFile(ctx context.Context, src, dst, profile string) (string, string, store.MediaClass, error) {
	preset, ok := ffmpeg.ConvertProfileByName(profile)
	if !ok {
		return "", "", "", store.ValidationError("unknown conversion profile")
	}
	if err := c.runner.Convert(ctx, src, dst, preset); err != nil {
		return "", "", "", err
	}
	// An audio-only preset admits as audio class with its audio codec; a video
	// preset admits as video with its video codec (the primary stream).
	if preset.AudioOnly {
		return preset.Container, preset.AudioCodec, store.MediaClassAudio, nil
	}
	codec := preset.VideoCodec
	if codec == "" {
		codec = "copy"
	}
	return preset.Container, codec, store.MediaClassVideo, nil
}

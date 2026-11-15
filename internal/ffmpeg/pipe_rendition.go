package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"
)

// RenditionToPipe encodes a precompute rendition of the loopback source to a
// fragmented MP4 on stdout (ADR-0235 D3), forcing a keyframe every
// segmentSeconds so the caller can cut HLS segments on that cadence. It is the
// software-only, single-input form of the HLS rendition encode: no hardware
// device, no copied video, no burned subtitle and no composed picture, since
// each of those reaches beyond the one loopback input or the encoder the
// sandbox allows.
func (r *Runner) RenditionToPipe(ctx context.Context, in PipeInput, rendition Rendition, segmentSeconds int, stdout io.Writer) error {
	port, err := loopbackPort(in.URL)
	if err != nil {
		return err
	}
	if stdout == nil {
		return errors.New("ffmpeg pipe rendition: no output writer")
	}
	if segmentSeconds <= 0 {
		return errors.New("ffmpeg pipe rendition: segment cadence must be positive")
	}
	if err := pipeRenditionRefusal(rendition); err != nil {
		return err
	}
	args := pipeRenditionArgs(in.URL, pipeReadTimeout(in), rendition, segmentSeconds)
	return r.runPipeTool(ctx, port, in, "ffmpeg", "ffmpeg pipe rendition", args, stdout)
}

func pipeRenditionRefusal(rendition Rendition) error {
	_, burns := rendition.burnsSubtitle()
	_, composed := rendition.Picture()
	switch {
	case rendition.AudioOnly, rendition.CopyVideo:
		return errors.New("ffmpeg pipe rendition: only re-encoded video renditions are supported")
	case burns || composed:
		return errors.New("ffmpeg pipe rendition: burned subtitles and composed pictures are not supported")
	case rendition.hwAccel != "" || rendition.toneMapAccel != "":
		return errors.New("ffmpeg pipe rendition: hardware acceleration is not supported")
	}
	if reason := rendition.ToneMapRefusal(); reason != "" {
		return fmt.Errorf("%w: %s", ErrToneMapRefused, reason)
	}
	return nil
}

func pipeRenditionArgs(inputURL string, timeout time.Duration, rendition Rendition, segmentSeconds int) []string {
	args := append([]string{"-v", "error", "-nostdin", "-xerror"}, rendition.inputArgs()...)
	args = append(args, pipeInputOptions(timeout)...)
	args = append(args, "-i", inputURL)
	args = append(args, rendition.videoArgs()...)
	args = append(args, rendition.selectedStreamMapArgs()...)
	args = append(args, "-force_key_frames", "expr:gte(t,n_forced*"+strconv.Itoa(segmentSeconds)+")")
	args = append(args, rendition.args()...)
	args = append(args, rendition.audioArgs()...)
	return append(args, "-movflags", pipeMovflags, "-f", "mp4", "pipe:1")
}

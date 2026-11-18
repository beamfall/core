package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Generated assets for an Encrypted Library (ADR-0235, CORE-1966). Each
// variant reads the loopback source under the same confinement and input
// options as TranscodeToPipe and never names an output file: images and the
// preview clip go to stdout for the caller to capture and encrypt, and the
// analyses print to bounded in-memory buffers that are parsed here.

// pipeImageArgs ends an image run: MJPEG frames concatenated on pipe:1.
// SplitJPEGStream separates them.
var pipeImageArgs = []string{"-c:v", "mjpeg", "-f", "image2pipe", "pipe:1"}

// pipeAnalysisLogArgs keep the info-level events an analysis prints on stderr
// while dropping the banner and the progress line.
var pipeAnalysisLogArgs = []string{"-v", "info", "-hide_banner", "-nostats", "-nostdin"}

func pipeHead(inputURL string, timeout time.Duration, pre ...string) []string {
	args := append([]string{"-v", "error", "-nostdin"}, pre...)
	args = append(args, pipeInputOptions(timeout)...)
	return append(args, "-i", inputURL)
}

func pipeSpriteSheetArgs(inputURL string, timeout time.Duration, intervalSeconds, tileWidth, columns, rows, frameCount int) []string {
	// Keyframes only on one thread, as SpriteSheets (CORE-2570). The pipe path
	// has no GPU grant, so it always decodes in software, and it does not retry
	// decoding every frame: a failed pass may already have written to stdout.
	args := pipeHead(inputURL, timeout, spriteSheetDecodeArgs(spriteSheetPass{keyframesOnly: true})...)
	args = append(args,
		"-vf", spriteSheetFilter(intervalSeconds, tileWidth, columns, rows, frameCount),
		"-q:v", "5")
	return append(args, pipeImageArgs...)
}

func pipeFrameGrabArgs(inputURL string, timeout time.Duration, atSeconds float64, width int) []string {
	args := pipeHead(inputURL, timeout, "-ss", strconv.FormatFloat(atSeconds, 'f', 3, 64))
	args = append(args, "-frames:v", "1", "-vf", fmt.Sprintf("scale=%d:-2", width))
	return append(args, pipeImageArgs...)
}

func pipeSampleClipArgs(inputURL string, timeout time.Duration, offsets []float64, segmentSeconds float64, width, maxFPS int) []string {
	selects := make([]string, len(offsets))
	for i, offset := range offsets {
		selects[i] = fmt.Sprintf("between(t,%.3f,%.3f)", offset, offset+segmentSeconds)
	}
	filter := fmt.Sprintf("select='%s',setpts=N/FRAME_RATE/TB,scale=%d:-2,fps=%d",
		strings.Join(selects, "+"), width, maxFPS)
	args := pipeHead(inputURL, timeout)
	return append(args,
		"-an",
		"-vf", filter,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "28",
		"-pix_fmt", "yuv420p",
		"-movflags", pipeMovflags,
		"-f", "mp4", "pipe:1")
}

func pipeAudioAnalysisArgs(inputURL string, timeout time.Duration, logArgs []string, filter string) []string {
	args := append(append([]string(nil), logArgs...), pipeInputOptions(timeout)...)
	return append(args, "-i", inputURL, "-map", "0:a:0", "-af", filter, "-f", "null", "-")
}

// SpriteSheetsToPipe is SpriteSheets for a loopback source: the sheets are
// written to stdout as concatenated JPEGs, in order.
func (r *Runner) SpriteSheetsToPipe(ctx context.Context, in PipeInput, intervalSeconds, tileWidth, columns, rows, frameCount int, stdout io.Writer) error {
	port, err := loopbackPort(in.URL)
	if err != nil {
		return err
	}
	if stdout == nil {
		return errors.New("ffmpeg pipe sprite sheets: no output writer")
	}
	if frameCount <= 0 {
		return errors.New("sprite sheets need a positive frame count")
	}
	args := pipeSpriteSheetArgs(in.URL, pipeReadTimeout(in), intervalSeconds, tileWidth, columns, rows, frameCount)
	return r.runPipeTool(ctx, port, in, "ffmpeg", "ffmpeg pipe sprite sheets", args, stdout)
}

// FrameGrabToPipe is FrameGrab for a loopback source: the one JPEG frame is
// written to stdout.
func (r *Runner) FrameGrabToPipe(ctx context.Context, in PipeInput, atSeconds float64, width int, stdout io.Writer) error {
	port, err := loopbackPort(in.URL)
	if err != nil {
		return err
	}
	if stdout == nil {
		return errors.New("ffmpeg pipe frame grab: no output writer")
	}
	args := pipeFrameGrabArgs(in.URL, pipeReadTimeout(in), atSeconds, width)
	return r.runPipeTool(ctx, port, in, "ffmpeg", "ffmpeg pipe frame grab", args, stdout)
}

// SampleClipToPipe is SampleClip for a loopback source: the clip is written to
// stdout as a fragmented MP4, since faststart rewrites a file afterwards.
func (r *Runner) SampleClipToPipe(ctx context.Context, in PipeInput, offsets []float64, segmentSeconds float64, width, maxFPS int, stdout io.Writer) error {
	port, err := loopbackPort(in.URL)
	if err != nil {
		return err
	}
	switch {
	case stdout == nil:
		return errors.New("ffmpeg pipe sample clip: no output writer")
	case len(offsets) == 0:
		return errors.New("sample clip needs at least one offset")
	}
	args := pipeSampleClipArgs(in.URL, pipeReadTimeout(in), offsets, segmentSeconds, width, maxFPS)
	return r.runPipeTool(ctx, port, in, "ffmpeg", "ffmpeg pipe sample clip", args, stdout)
}

// WaveformAmplitudesFromPipe is WaveformAmplitudes for a loopback source. The
// duration comes from ProbePipeDuration and the per-window levels from a
// bounded stdout capture.
func (r *Runner) WaveformAmplitudesFromPipe(ctx context.Context, in PipeInput) ([]float64, error) {
	port, err := loopbackPort(in.URL)
	if err != nil {
		return nil, err
	}
	duration, err := r.ProbePipeDuration(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg waveform probe: %w", err)
	}
	windowSamples := duration.Seconds() * waveformSampleRate / waveformWindows
	if windowSamples < 1 {
		windowSamples = 1
	}
	filter := fmt.Sprintf(
		"aformat=channel_layouts=mono:sample_rates=%d,asetnsamples=n=%d:p=0,astats=metadata=1:reset=1,ametadata=print:file=-",
		waveformSampleRate, int(windowSamples))
	out := &boundedBuffer{limit: captureLimitBytes}
	args := pipeAudioAnalysisArgs(in.URL, pipeReadTimeout(in), []string{"-v", "error", "-nostdin"}, filter)
	if err := r.runPipeTool(ctx, port, in, "ffmpeg", "ffmpeg pipe waveform analysis", args, out); err != nil {
		return nil, err
	}
	amps := parseWaveformAmplitudes(string(out.bytes()))
	if len(amps) == 0 {
		return nil, errors.New("ffmpeg waveform analysis produced no windows")
	}
	return amps, nil
}

// SilenceSpansFromPipe is SilenceSpans for a loopback source; silencedetect's
// events are read from a bounded stderr capture.
func (r *Runner) SilenceSpansFromPipe(ctx context.Context, in PipeInput) ([][2]float64, error) {
	port, err := loopbackPort(in.URL)
	if err != nil {
		return nil, err
	}
	filter := fmt.Sprintf("silencedetect=noise=%gdB:d=%g", silenceThresholdDB, silenceMinDurationSec)
	stderr := &boundedBuffer{limit: captureLimitBytes}
	args := pipeAudioAnalysisArgs(in.URL, pipeReadTimeout(in), pipeAnalysisLogArgs, filter)
	if err := r.runPipeToolWith(ctx, port, in, "ffmpeg", "ffmpeg pipe silence analysis", args, io.Discard, stderr); err != nil {
		return nil, err
	}
	return parseSilenceSpans(string(stderr.bytes())), nil
}

// LoudnessMeasureFromPipe measures a loopback source's EBU R128 loudness by
// decoding it through ebur128. Container tags are not consulted, so the result
// is always a measurement. framelog=quiet leaves only the summary on stderr.
func (r *Runner) LoudnessMeasureFromPipe(ctx context.Context, in PipeInput) (integratedLUFS, loudnessRangeLU, truePeakDBTP float64, err error) {
	port, err := loopbackPort(in.URL)
	if err != nil {
		return 0, 0, 0, err
	}
	stderr := &boundedBuffer{limit: captureLimitBytes}
	args := pipeAudioAnalysisArgs(in.URL, pipeReadTimeout(in), pipeAnalysisLogArgs, "ebur128=peak=true:framelog=quiet")
	if err := r.runPipeToolWith(ctx, port, in, "ffmpeg", "ffmpeg pipe loudness analysis", args, io.Discard, stderr); err != nil {
		return 0, 0, 0, err
	}
	stats, ok := parseEBUR128Summary(string(stderr.bytes()))
	if !ok {
		return 0, 0, 0, errors.New("ffmpeg loudness analysis produced no summary")
	}
	return stats.IntegratedLUFS, stats.LoudnessRangeLU, stats.TruePeakDBTP, nil
}

// ErrJPEGStream is returned when a byte stream is not a whole sequence of
// JPEG images.
var ErrJPEGStream = errors.New("ffmpeg: malformed JPEG stream")

// SplitJPEGStream splits the concatenated JPEGs an image2pipe run writes into
// one slice per image, aliasing data. It walks each image's marker segments
// and entropy-coded data to its EOI marker, so a 0xFFD9 inside a segment
// cannot end an image early. More than maxImages images, trailing bytes, an
// image without a scan or a truncated image fail with ErrJPEGStream.
func SplitJPEGStream(data []byte, maxImages int) ([][]byte, error) {
	var images [][]byte
	for pos := 0; pos < len(data); {
		if len(images) >= maxImages {
			return nil, fmt.Errorf("%w: more than %d images", ErrJPEGStream, maxImages)
		}
		end, err := jpegImageEnd(data, pos)
		if err != nil {
			return nil, err
		}
		images = append(images, data[pos:end])
		pos = end
	}
	return images, nil
}

// jpegImageEnd returns the offset just past the EOI of the image at start.
func jpegImageEnd(data []byte, start int) (int, error) {
	if len(data)-start < 2 || data[start] != 0xFF || data[start+1] != 0xD8 {
		return 0, fmt.Errorf("%w: no start-of-image marker", ErrJPEGStream)
	}
	i := start + 2
	scanned := false
	for {
		if i >= len(data) || data[i] != 0xFF {
			return 0, fmt.Errorf("%w: expected a marker", ErrJPEGStream)
		}
		for i < len(data) && data[i] == 0xFF {
			i++ // fill bytes
		}
		if i >= len(data) {
			return 0, fmt.Errorf("%w: truncated marker", ErrJPEGStream)
		}
		marker := data[i]
		i++
		switch {
		case marker == 0xD9:
			if !scanned {
				return 0, fmt.Errorf("%w: image has no scan", ErrJPEGStream)
			}
			return i, nil
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			continue // no length field
		case marker == 0x00 || marker == 0xD8:
			return 0, fmt.Errorf("%w: unexpected marker", ErrJPEGStream)
		}
		if len(data)-i < 2 {
			return 0, fmt.Errorf("%w: truncated segment", ErrJPEGStream)
		}
		length := int(data[i])<<8 | int(data[i+1])
		if length < 2 || len(data)-i < length {
			return 0, fmt.Errorf("%w: bad segment length", ErrJPEGStream)
		}
		i += length
		if marker == 0xDA {
			scanned = true
			i = jpegEntropyEnd(data, i)
		}
	}
}

// jpegEntropyEnd skips entropy-coded data, where 0xFF is followed by a stuffed
// zero or a restart marker, and returns the offset of the next real marker (or
// len(data) when there is none).
func jpegEntropyEnd(data []byte, i int) int {
	for i < len(data) {
		if data[i] != 0xFF {
			i++
			continue
		}
		if i+1 < len(data) {
			next := data[i+1]
			if next == 0x00 || (next >= 0xD0 && next <= 0xD7) {
				i += 2
				continue
			}
		}
		return i
	}
	return i
}

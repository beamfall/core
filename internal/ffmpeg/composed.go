package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Composed-picture canvas: the 720p frame a composed rendition is drawn on
// (ADR-0057 precomputed renditions; see ComposedPicture). A live composed
// stream under a client or quirk height limit draws a smaller canvas of the
// same shape (see ComposedCanvas).
const (
	composedWidth  = 1280
	composedHeight = 720
	composedFPS    = 30
	// A graphics track is drawn at its own aspect, pillarboxed to the canvas.
	// 1000x720 keeps a 300x216 graphics frame's shape (25:18) to the pixel.
	composedGraphicsWidth = 1000
)

// ComposedCanvas is the canvas a composed rendition of the given height is
// drawn on: 16:9 at that height, with even dimensions for yuv420p. A height of
// zero or above the 720p canvas draws the 720p canvas, so a composed encode
// never upscales.
func ComposedCanvas(height int) (width, canvasHeight int) {
	if height <= 0 || height > composedHeight {
		height = composedHeight
	}
	height -= height % 2
	return evenRound(float64(height) * composedWidth / composedHeight), height
}

// evenRound rounds to the nearest even integer.
func evenRound(value float64) int {
	return 2 * int(value/2+0.5)
}

// ErrComposedPictureOutsideConfinement reports a picture input outside the
// encode's confinement: a graphics track not beside the audio source, or a
// timed-text document not inside the output directory. The input is refused
// before FFmpeg starts rather than the sandbox being widened.
var ErrComposedPictureOutsideConfinement = errors.New("ffmpeg: composed picture input is outside the encode's confinement")

// ComposedPicture is where a composed rendition's picture comes from when the
// source has none of its own. Exactly one field is set:
//
//   - GraphicsPath: a sibling file FFmpeg decodes as a video stream (a graphics
//     subcode track), scaled without smoothing and pillarboxed.
//   - TimedTextPath: an ASS document rendered by libass over a black canvas.
type ComposedPicture struct {
	GraphicsPath  string
	TimedTextPath string
}

func (p ComposedPicture) validate() error {
	if (p.GraphicsPath == "") == (p.TimedTextPath == "") {
		return errors.New("ffmpeg: a composed picture sets exactly one of graphics or timed text")
	}
	return nil
}

// StartBackgroundComposedHLS encodes an audio source plus a composed picture
// into the same CMAF HLS layout StartBackgroundHLS writes, at a fixed 720p
// canvas, for precomputed rendition generation. The encode is confined like
// any HLS job: the source's directory read-only, dir read-write. A timed-text
// document must therefore be inside dir, and a graphics track beside the
// source (checked after resolving symlinks).
func (r *Runner) StartBackgroundComposedHLS(ctx context.Context, audioSrc, dir string, picture ComposedPicture, rendition Rendition) (*HLSProcess, error) {
	if err := checkComposedInputs(audioSrc, dir, picture); err != nil {
		return nil, err
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is not available: %w", err)
	}
	cmd := r.generationCommand(ctx, dirConfinement(audioSrc, dir), ffmpegPath, composedHLSArgs(audioSrc, dir, picture, rendition)...)
	process, err := startHLSProcess(cmd)
	if err != nil {
		return nil, fmt.Errorf("starting ffmpeg composed encode: %w", err)
	}
	return process, nil
}

// startComposedHLSSegments is StartHLSSegments for a rendition that carries a
// composed picture: the live form of a composed rendition, encoded from
// startSegment into the same numbered-segment layout as every live transcode,
// under the same confinement rules as the precomputed encode.
func (r *Runner) startComposedHLSSegments(ctx context.Context, audioSrc, dir string, picture ComposedPicture, startSegment, segmentSeconds int, rendition Rendition) (*HLSProcess, error) {
	if err := checkComposedInputs(audioSrc, dir, picture); err != nil {
		return nil, err
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is not available: %w", err)
	}
	conf := dirConfinement(audioSrc, dir)
	cmd := r.hardenedCommand(ctx, ffmpegPath, conf.ReadOnly, conf.ReadWrite, composedSegmentArgs(audioSrc, dir, picture, startSegment, segmentSeconds, rendition))
	process, err := startHLSProcess(cmd)
	if err != nil {
		return nil, fmt.Errorf("starting ffmpeg composed transcode: %w", err)
	}
	return process, nil
}

// checkComposedInputs refuses a composed encode whose inputs fall outside its
// confinement: the source's directory is read-only and dir read-write, so a
// timed-text document must be inside dir and a graphics track beside the
// source (checked after resolving symlinks).
func checkComposedInputs(audioSrc, dir string, picture ComposedPicture) error {
	if err := picture.validate(); err != nil {
		return err
	}
	for _, path := range []string{audioSrc, dir, picture.GraphicsPath, picture.TimedTextPath} {
		if err := checkPath(path); err != nil {
			return err
		}
	}
	if picture.GraphicsPath != "" {
		if err := inDirectory(picture.GraphicsPath, filepath.Dir(audioSrc)); err != nil {
			return err
		}
	}
	if picture.TimedTextPath != "" {
		if err := inDirectory(picture.TimedTextPath, dir); err != nil {
			return err
		}
	}
	return nil
}

// composedHLSArgs builds the precomputed composed encode of the whole source
// into the event-playlist layout StartBackgroundHLS writes.
func composedHLSArgs(audioSrc, dir string, picture ComposedPicture, rendition Rendition) []string {
	args := append([]string{"-v", "error"}, composedEncodeArgs(audioSrc, picture, rendition, 0, 4)...)
	return append(args, hlsMuxArgs(dir)...)
}

// composedSegmentArgs builds the live composed encode from startSegment, with
// the progress stream and numbered-segment muxer every live transcode uses.
func composedSegmentArgs(audioSrc, dir string, picture ComposedPicture, startSegment, segmentSeconds int, rendition Rendition) []string {
	args := append([]string{"-v", "error"}, hlsProgressArgs()...)
	args = append(args, composedEncodeArgs(audioSrc, picture, rendition, startSegment*segmentSeconds, segmentSeconds)...)
	return append(args, segmentMuxArgs(dir, startSegment, segmentSeconds)...)
}

// composedEncodeArgs is the composition recipe the precomputed and live
// encodes share: input 0 is the picture, input 1 the audio source. The picture
// is software-encoded (a flat canvas is cheap, and composition never needs a
// hardware path), the audio goes through the rendition's normal audio recipe,
// and the audio's length decides the output's: a graphics track goes quiet for
// long stretches, so its last frame is held.
//
// A positive startSeconds starts the output at that position (the source
// position it plays, under an adjusted tempo). The audio
// input seeks there. A graphics track is drawn incrementally, so it is decoded
// from its start and trimmed rather than seeked; a timed-text canvas instead
// has its clock advanced so libass renders the text due at that position. The
// picture's timestamps then restart at zero, beside the seeked audio's.
func composedEncodeArgs(audioSrc string, picture ComposedPicture, rendition Rendition, startSeconds, segmentSeconds int) []string {
	var args, filters []string
	// startSeconds is on the output timeline; the picture and the audio are
	// read at the source position it plays, which an adjusted tempo scales.
	// Under a tempo the picture is retimed with the audio: it is drawn at
	// source time and its frames are stamped at output time.
	tempo := rendition.adjust.tempo()
	source := formatSeconds(rendition.adjust.SourceSeconds(float64(startSeconds)))
	width, height := ComposedCanvas(rendition.Height)
	if picture.GraphicsPath != "" {
		args = append(args, "-i", picture.GraphicsPath)
		filters = []string{
			fmt.Sprintf("scale=%d:%d:flags=neighbor", evenRound(float64(composedGraphicsWidth*height)/composedHeight), height),
			fmt.Sprintf("pad=%d:%d:(ow-iw)/2:0", width, height),
			fmt.Sprintf("fps=%d", composedFPS),
			"tpad=stop=-1:stop_mode=clone",
		}
		switch {
		case tempo != 1 && startSeconds > 0:
			filters = append(filters, "trim=start="+source, "setpts=(PTS-STARTPTS)/"+formatFactor(tempo), fmt.Sprintf("fps=%d", composedFPS))
		case tempo != 1:
			filters = append(filters, "setpts=PTS/"+formatFactor(tempo), fmt.Sprintf("fps=%d", composedFPS))
		case startSeconds > 0:
			filters = append(filters, "trim=start="+source, "setpts=PTS-STARTPTS")
		}
	} else {
		args = append(args, "-f", "lavfi", "-i", fmt.Sprintf("color=c=black:s=%dx%d:r=%d", width, height, composedFPS))
		switch {
		case tempo != 1:
			filters = append(filters, fmt.Sprintf("setpts=PTS*%s+%s/TB", formatFactor(tempo), source))
		case startSeconds > 0:
			filters = append(filters, fmt.Sprintf("setpts=PTS+%s/TB", source))
		}
		filters = append(filters, "ass=filename="+escapeFilterValue(picture.TimedTextPath))
		switch {
		case tempo != 1:
			filters = append(filters, "setpts=(PTS-STARTPTS)/"+formatFactor(tempo))
		case startSeconds > 0:
			filters = append(filters, "setpts=PTS-STARTPTS")
		}
	}
	filters = append(filters, "format=yuv420p")
	if startSeconds > 0 {
		args = append(args, "-ss", source)
	}
	args = append(args, inputProtocolArgs()...)
	args = append(args, "-i", audioSrc,
		"-map", "0:v:0", "-map", "1:a:0",
		"-vf", strings.Join(filters, ","),
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p",
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segmentSeconds),
	)
	if rendition.VideoKbps > 0 {
		args = append(args, "-maxrate", fmt.Sprintf("%dk", rendition.VideoKbps), "-bufsize", fmt.Sprintf("%dk", 2*rendition.VideoKbps))
	}
	args = append(args, rendition.audioArgs()...)
	return append(args, "-shortest")
}

// escapeFilterValue makes an arbitrary string safe as one filter option value
// inside a filtergraph. FFmpeg unquotes twice: first the filtergraph parser
// (where '[', ']', ',', ';' are separators), then the filter's option parser
// (where ':' separates options). So the value is escaped for the option level
// first, and that result escaped again for the graph level — only the value,
// so the ',' between filters keeps its meaning.
func escapeFilterValue(value string) string {
	option := strings.NewReplacer(`\`, `\\`, `'`, `\'`, `:`, `\:`).Replace(value)
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`, `[`, `\[`, `]`, `\]`, `,`, `\,`, `;`, `\;`).Replace(option)
}

// inDirectory reports whether file sits directly in dir, comparing both after
// resolving symlinks — so a symlinked file pointing elsewhere is refused.
func inDirectory(file, dir string) error {
	resolvedFile, err := filepath.EvalSymlinks(file)
	if err != nil {
		return err
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if filepath.Dir(resolvedFile) != filepath.Clean(resolvedDir) {
		return ErrComposedPictureOutsideConfinement
	}
	return nil
}

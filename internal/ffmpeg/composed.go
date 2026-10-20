package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Composed-picture canvas: the fixed 720p frame a composed rendition is drawn
// on (ADR-0057 precomputed renditions; see ComposedPicture).
const (
	composedWidth  = 1280
	composedHeight = 720
	composedFPS    = 30
	// A graphics track is drawn at its own aspect, pillarboxed to the canvas.
	// 1000x720 keeps a 300x216 graphics frame's shape (25:18) to the pixel.
	composedGraphicsWidth = 1000
)

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
	if err := picture.validate(); err != nil {
		return nil, err
	}
	for _, path := range []string{audioSrc, dir, picture.GraphicsPath, picture.TimedTextPath} {
		if err := checkPath(path); err != nil {
			return nil, err
		}
	}
	if picture.GraphicsPath != "" {
		if err := inDirectory(picture.GraphicsPath, filepath.Dir(audioSrc)); err != nil {
			return nil, err
		}
	}
	if picture.TimedTextPath != "" {
		if err := inDirectory(picture.TimedTextPath, dir); err != nil {
			return nil, err
		}
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

// composedHLSArgs builds the composed encode: input 0 is the picture, input 1
// the audio source. The picture is software-encoded (a flat canvas is cheap,
// and composition never needs a hardware path), the audio goes through the
// rendition's normal audio recipe, and the audio's length decides the output's:
// a graphics track goes quiet for long stretches, so its last frame is held.
func composedHLSArgs(audioSrc, dir string, picture ComposedPicture, rendition Rendition) []string {
	args := []string{"-v", "error"}
	var filters []string
	if picture.GraphicsPath != "" {
		args = append(args, "-i", picture.GraphicsPath)
		filters = []string{
			fmt.Sprintf("scale=%d:%d:flags=neighbor", composedGraphicsWidth, composedHeight),
			fmt.Sprintf("pad=%d:%d:(ow-iw)/2:0", composedWidth, composedHeight),
			fmt.Sprintf("fps=%d", composedFPS),
			"tpad=stop=-1:stop_mode=clone",
		}
	} else {
		args = append(args, "-f", "lavfi", "-i", fmt.Sprintf("color=c=black:s=%dx%d:r=%d", composedWidth, composedHeight, composedFPS))
		filters = []string{"ass=filename=" + escapeFilterValue(picture.TimedTextPath)}
	}
	filters = append(filters, "format=yuv420p")
	args = append(args, inputProtocolArgs()...)
	args = append(args, "-i", audioSrc,
		"-map", "0:v:0", "-map", "1:a:0",
		"-vf", strings.Join(filters, ","),
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p",
		"-force_key_frames", "expr:gte(t,n_forced*4)",
	)
	if rendition.VideoKbps > 0 {
		args = append(args, "-maxrate", fmt.Sprintf("%dk", rendition.VideoKbps), "-bufsize", fmt.Sprintf("%dk", 2*rendition.VideoKbps))
	}
	args = append(args, rendition.audioArgs()...)
	args = append(args, "-shortest")
	return append(args, hlsMuxArgs(dir)...)
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

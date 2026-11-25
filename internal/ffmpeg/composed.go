package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/beamfall/core/internal/ffmpeg/cdgrender"
)

// Composed-picture canvas: the 540p frame a composed rendition is drawn on
// (ADR-0057 precomputed renditions; see ComposedPicture). A live composed
// stream under a client or quirk height limit draws a smaller canvas of the
// same shape (see ComposedCanvas).
const (
	composedWidth  = 960
	composedHeight = 540
	composedFPS    = 30
)

// ComposedCanvas is the canvas a composed rendition of the given height is
// drawn on: 16:9 at that height, with even dimensions for yuv420p. A height of
// zero or above the 540p canvas draws the 540p canvas, so a composed encode
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
//   - GraphicsPath: a sibling graphics subcode track (CD+G), drawn by the
//     Oscillux CD+G presenter and piped to FFmpeg as raw frames.
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
// into the same CMAF HLS layout StartBackgroundHLS writes, at a fixed 540p
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
	rendition = r.withQuality(rendition)
	cmd := r.generationCommand(ctx, dirConfinement(audioSrc, dir), ffmpegPath, composedHLSArgs(audioSrc, dir, picture, rendition)...)
	process, err := startComposedProcess(ctx, cmd, audioSrc, picture, rendition, 0)
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
	rendition = r.withQuality(rendition)
	if !rendition.adjust.IsZero() && rendition.adjust.PitchSemitones != 0 && rubberbandSupported(ctx) {
		rendition = rendition.withRubberband(true)
	}
	cmd := r.hardenedCommand(ctx, ffmpegPath, conf.ReadOnly, conf.ReadWrite, composedSegmentArgs(audioSrc, dir, picture, startSegment, segmentSeconds, rendition))
	process, err := startComposedProcess(ctx, cmd, audioSrc, picture, rendition, startSegment*segmentSeconds)
	if err != nil {
		return nil, fmt.Errorf("starting ffmpeg composed transcode: %w", err)
	}
	return process, nil
}

// startComposedProcess starts a composed encode. For a graphics picture it
// also runs the Oscillux CD+G presenter in-process, drawing the canvas into
// the encode's stdin from the source position startSeconds plays, at the
// rendition's tempo (CORE-3150).
//
// Nothing here blocks on the graphics file: a feeder goroutine opens it inside
// the source directory and streams it to the presenter, which runs before the
// encoder starts so the done channel is always signalled. When the encoder
// exits, Wait cuts the stream (a stalled read cannot hold it) and joins the
// presenter, reporting a failure that would otherwise end the picture early
// behind a clean FFmpeg exit.
func startComposedProcess(ctx context.Context, cmd *exec.Cmd, audioSrc string, picture ComposedPicture, rendition Rendition, startSeconds int) (*HLSProcess, error) {
	if picture.GraphicsPath == "" {
		return startHLSProcessContext(ctx, cmd, false)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: composed graphics pipe: %w", err)
	}
	stream := newGraphicsStream(filepath.Dir(audioSrc), picture.GraphicsPath)
	done := make(chan error, 1)
	go func() {
		err := cdgrender.Render(ctx, stream, pw, composedGraphicsOptions(rendition, startSeconds))
		// Settle the outcome before FFmpeg can see EOF and Wait can cut.
		res := stream.result(err)
		_ = pw.Close()
		done <- res
	}()
	// stop ends a presenter blocked on either side: the cut ends a read, and
	// closing pw ends a frame write the encoder will never take.
	stop := func() error {
		stream.cut()
		_ = pw.Close()
		return <-done
	}
	cmd.Stdin = pr
	process, err := startHLSProcessWith(ctx, cmd, false, stop)
	_ = pr.Close()
	if err != nil {
		_ = stop()
		return nil, err
	}
	return process, nil
}

// composedGraphicsOptions is the presenter's canvas and clock for an encode
// whose output starts at startSeconds: the picture starts at the source second
// that output second plays and advances at the rendition's tempo.
func composedGraphicsOptions(rendition Rendition, startSeconds int) cdgrender.Options {
	width, height := ComposedCanvas(rendition.Height)
	return cdgrender.Options{
		Width: width, Height: height, FPS: composedFPS,
		Start: rendition.adjust.SourceSeconds(float64(startSeconds)),
		Tempo: rendition.adjust.tempo(),
	}
}

// composedGraphicsLimit bounds a graphics track: the presenter holds the
// whole track in memory. CD+G is 7200 bytes a second, so 128 MiB is about five
// hours, well past any song.
const composedGraphicsLimit = 128 << 20

// errGraphicsCut ends the presenter's input when the encoder has gone.
var errGraphicsCut = errors.New("ffmpeg: composed graphics stream cut")

// graphicsStream feeds a graphics track to the presenter through a pipe. A
// feeder goroutine opens and copies the file, so a stalled filesystem blocks
// only that goroutine; cut closes the pipe so the presenter's read returns.
type graphicsStream struct {
	r       *io.PipeReader
	mu      sync.Mutex
	openErr error
	wasCut  bool
}

func newGraphicsStream(sourceDir, path string) *graphicsStream {
	r, w := io.Pipe()
	s := &graphicsStream{r: r}
	go func() {
		f, err := openGraphics(sourceDir, path)
		if err != nil {
			s.mu.Lock()
			s.openErr = err
			s.mu.Unlock()
			_ = w.CloseWithError(err)
			return
		}
		n, err := io.Copy(w, io.LimitReader(f, composedGraphicsLimit+1))
		_ = f.Close()
		if err == nil && n > composedGraphicsLimit {
			err = fmt.Errorf("ffmpeg: composed graphics exceed %d bytes", composedGraphicsLimit)
			s.mu.Lock()
			s.openErr = err
			s.mu.Unlock()
		}
		_ = w.CloseWithError(err) // nil closes with io.EOF
	}()
	return s
}

func (s *graphicsStream) Read(p []byte) (int, error) { return s.r.Read(p) }

func (s *graphicsStream) cut() {
	s.mu.Lock()
	s.wasCut = true
	s.mu.Unlock()
	_ = s.r.CloseWithError(errGraphicsCut)
}

// result is the presenter's outcome: the open failure when the track could
// not be read, nothing when the stream was cut because the encoder had gone,
// and otherwise the presenter's own error.
func (s *graphicsStream) result(renderErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.openErr != nil:
		return s.openErr
	case s.wasCut:
		return nil
	default:
		return renderErr
	}
}

// openGraphics opens a graphics track checkComposedInputs placed in the
// source's directory (after resolving symlinks), through an os.Root on that
// directory: the server reads only what that directory holds, the same
// directory the encoder's confinement grants by name, even if the track was
// swapped for a symlink since the check. The track must be a regular file,
// and opening it does not block on a FIFO.
func openGraphics(sourceDir, path string) (*os.File, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: opening composed graphics: %w", err)
	}
	resolvedDir, err := filepath.EvalSymlinks(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: opening composed graphics: %w", err)
	}
	if filepath.Dir(resolved) != filepath.Clean(resolvedDir) {
		return nil, ErrComposedPictureOutsideConfinement
	}
	root, err := os.OpenRoot(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: opening composed graphics: %w", err)
	}
	defer func() { _ = root.Close() }()
	name := filepath.Base(resolved)
	f, err := root.OpenFile(name, os.O_RDONLY|graphicsOpenFlags, 0)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: opening composed graphics: %w", err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("ffmpeg: composed graphics %s is not a regular file", name)
	}
	return f, nil
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
	return append(args, segmentMuxArgs(dir, startSegment, strconv.Itoa(startSegment*segmentSeconds), strconv.Itoa(segmentSeconds))...)
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
// input seeks there. The graphics presenter starts at that source position
// itself; a timed-text canvas instead has its clock advanced so libass renders
// the text due at that position. The picture's timestamps then restart at
// zero, beside the seeked audio's.
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
		// The Oscillux CD+G presenter draws the whole canvas and writes it to
		// FFmpeg's stdin (startComposedProcess); it starts at the source
		// position and runs at the tempo itself, so the picture needs no
		// trim or retiming here (CORE-3150).
		args = append(args, "-f", "rawvideo", "-pix_fmt", "rgba",
			"-s", fmt.Sprintf("%dx%d", width, height), "-r", strconv.Itoa(composedFPS), "-i", "pipe:0")
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
	// The audio map follows the track the playback decision selected
	// (CORE-1873), else the source's first audio stream.
	audioMap := "1:a:0"
	if rendition.AudioStreamIndex != nil {
		audioMap = fmt.Sprintf("1:%d", *rendition.AudioStreamIndex)
	}
	args = append(args, "-i", audioSrc,
		"-map", "0:v:0", "-map", audioMap,
		"-vf", strings.Join(filters, ","),
		"-c:v", "libx264", "-threads", "4", "-preset", "veryfast", "-crf", rendition.softwareCRF(), "-sc_threshold", "0", "-pix_fmt", "yuv420p",
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

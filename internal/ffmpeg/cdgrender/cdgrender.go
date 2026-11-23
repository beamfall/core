// Package cdgrender draws a CD+G graphics track as raw RGBA frames with the
// Oscillux CD+G presenter (CORE-3150). The presenter is Oscillux's
// oscillux-cdg-render WASI command, embedded at the release pinned in
// pin.json and run in-process by wazero. The module sees only its arguments,
// stdin and stdout: no files, network or real clock.
package cdgrender

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

//go:embed oscillux-cdg-render.wasm
var module []byte

// Options picks the frame size and the source time each frame shows: frame n
// shows source second Start + n*Tempo/FPS.
type Options struct {
	Width, Height int
	FPS           int
	Start         float64
	Tempo         float64
}

func (o Options) args() []string {
	return []string{
		"oscillux-cdg-render",
		"--width", strconv.Itoa(o.Width),
		"--height", strconv.Itoa(o.Height),
		"--fps", strconv.Itoa(o.FPS),
		"--start", strconv.FormatFloat(o.Start, 'f', -1, 64),
		"--tempo", strconv.FormatFloat(o.Tempo, 'f', -1, 64),
	}
}

var (
	compileOnce sync.Once
	runtime     wazero.Runtime
	compiled    wazero.CompiledModule
	compileErr  error
)

// compile builds the module once per process; every render instantiates the
// shared compiled module, which wazero allows concurrently. The runtime does
// not close modules on context done: that check, at every guest call and
// loop, made the presenter about four times slower (22 against 91 frames a
// second at 960x540 on an i7-8700K), below real time. A render stops on its
// context through its streams instead (Render).
func compile() (wazero.Runtime, wazero.CompiledModule, error) {
	compileOnce.Do(func() {
		ctx := context.Background()
		rt := wazero.NewRuntime(ctx)
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
			compileErr = fmt.Errorf("cdgrender: instantiating WASI: %w", err)
			return
		}
		cm, err := rt.CompileModule(ctx, module)
		if err != nil {
			compileErr = fmt.Errorf("cdgrender: compiling the presenter: %w", err)
			return
		}
		runtime, compiled = rt, cm
	})
	return runtime, compiled, compileErr
}

// stderrLimit bounds the diagnostics kept from one render.
const stderrLimit = 4096

type tail struct{ b []byte }

func (t *tail) Write(p []byte) (int, error) {
	if room := stderrLimit - len(t.b); room > 0 {
		t.b = append(t.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// readerOnly and writerOnly hide an *os.File from wazero, which would
// otherwise use its descriptor directly, so a caller can close the file to end
// a blocked read or write without the descriptor being reused under the
// module. They also replace every error with errStream: wazero reports a
// closed file as EBADF, which Rust's standard streams treat as success, so a
// presenter writing to a closed file would never stop. Once ctx ends they
// fail every call, which is how a render stops on its context: the presenter
// reads the whole stream before drawing and writes every frame, so it never
// runs longer than one frame without calling one of them.
type readerOnly struct {
	ctx context.Context
	r   io.Reader
}

type writerOnly struct {
	ctx context.Context
	w   io.Writer
}

var errStream = errors.New("cdgrender: stream closed")

func (r readerOnly) Read(p []byte) (int, error) {
	if r.ctx.Err() != nil {
		return 0, errStream
	}
	n, err := r.r.Read(p)
	if err != nil && err != io.EOF {
		err = errStream
	}
	return n, err
}

func (w writerOnly) Write(p []byte) (int, error) {
	if w.ctx.Err() != nil {
		return 0, errStream
	}
	n, err := w.w.Write(p)
	if err != nil {
		err = errStream
	}
	return n, err
}

// Render reads the whole graphics stream and writes frames to out until a
// write fails (the reader is gone, which is the normal end) or ctx ends. It
// returns nil for both, and an error when the presenter refuses its options
// or cannot run. The caller may close stream or out from another goroutine
// to stop a render blocked on either.
func Render(ctx context.Context, stream io.Reader, out io.Writer, o Options) error {
	rt, cm, err := compile()
	if err != nil {
		return err
	}
	var diag tail
	cfg := wazero.NewModuleConfig().
		WithName("").
		WithArgs(o.args()...).
		WithStdin(readerOnly{ctx, stream}).
		WithStdout(writerOnly{ctx, out}).
		WithStderr(&diag)
	mod, err := rt.InstantiateModule(ctx, cm, cfg)
	if mod != nil {
		_ = mod.Close(context.Background())
	}
	var exit *sys.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exit) && exit.ExitCode() == 0:
		return nil
	case ctx.Err() != nil:
		return nil
	case errors.As(err, &exit):
		return fmt.Errorf("cdgrender: presenter exited %d: %s", exit.ExitCode(), diag.b)
	default:
		return fmt.Errorf("cdgrender: %w", err)
	}
}

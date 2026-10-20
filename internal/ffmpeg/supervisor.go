package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// The bounded fail-closed FFmpeg supervisor. Every
// production ffmpeg/ffprobe spawn that captures output runs through Runner.run:
// it is the single choke point that (1) caps how much stdout/stderr a child can
// make the server buffer, killing the process tree the instant either stream
// overflows, (2) kills and reaps the whole process tree on context
// cancellation/timeout, and (3) always waits on the child so no zombie or orphan
// is left behind. Raw exec-and-capture (cmd.Output/CombinedOutput/Run) is
// forbidden in production code — a guard test enforces it — so a malicious media
// file can never drive an un-supervised spawn.

// captureLimitBytes bounds stdout and stderr each captured from an ffmpeg/
// ffprobe child. A well-formed probe JSON is a few KB and error logs a few
// lines, so the cap is generous for every legitimate use while fail-closing a
// decompression-bomb / runaway-log attempt: on overflow the supervisor kills the
// process tree and returns ErrOutputOverflow rather than buffering unbounded
// bytes into the server heap. 16 MiB per stream.
const captureLimitBytes = 16 << 20

// ErrOutputOverflow is returned when a supervised child's stdout or stderr
// exceeds captureLimitBytes; the child's process tree has been killed and reaped.
var ErrOutputOverflow = errors.New("ffmpeg: process output exceeded the bounded capture limit")

// boundedBuffer captures up to limit bytes and, the first time a write would
// exceed the cap, marks itself overflowed and fires onOverflow exactly once
// (asynchronously, so a Write from an exec copy goroutine never blocks on the
// kill). Further writes are accepted and discarded so the child's own writes
// never error-loop while the supervisor tears it down.
type boundedBuffer struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	limit      int
	over       bool
	onOverflow func()
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	var trigger func()
	if !b.over {
		room := b.limit - b.buf.Len()
		if len(p) <= room {
			b.buf.Write(p)
		} else {
			if room > 0 {
				b.buf.Write(p[:room])
			}
			b.over = true
			trigger = b.onOverflow
		}
	}
	b.mu.Unlock()
	if trigger != nil {
		go trigger()
	}
	return len(p), nil // always fully "consume": the supervisor, not backpressure, stops the child
}

func (b *boundedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

func (b *boundedBuffer) overflowed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.over
}

// run executes cmd under the bounded fail-closed supervisor and returns its
// captured (bounded) stdout and stderr. cmd must already be built for the target
// binary (via hardenedCommand/generationCommand/convertCommand) but must NOT
// have had its Stdout/Stderr or process attributes set — run owns those. It
// applies the parent-loss death signal (Linux) and puts the child in its own
// process group so a kill reaps the whole tree; on ctx cancel/timeout or an
// output overflow it kills the tree and always waits, so the supervisor never
// leaves an orphan. A start failure is wrapped; an overflow returns
// ErrOutputOverflow; otherwise the child's own exit error is returned.
func (r *Runner) run(ctx context.Context, cmd *exec.Cmd) (stdout, stderr []byte, err error) {
	var killOnce sync.Once
	kill := func() { killOnce.Do(func() { killProcessGroup(cmd) }) }
	out := &boundedBuffer{limit: captureLimitBytes, onOverflow: kill}
	errb := &boundedBuffer{limit: captureLimitBytes, onOverflow: kill}
	runErr := r.runWith(ctx, cmd, out, errb)
	if out.overflowed() || errb.overflowed() {
		// Fail closed: the child overran the capture cap and its tree has been
		// killed; report the overflow regardless of the exit/kill error.
		return out.bytes(), errb.bytes(), ErrOutputOverflow
	}
	return out.bytes(), errb.bytes(), runErr
}

// runWith is the core supervisor loop: it applies the parent-loss death signal
// (Linux) and a dedicated process group, streams the child's output into the
// caller-supplied writers (which own their own bounding — a boundedBuffer's
// head cap or a tailBuffer's rolling tail), and on ctx cancel/timeout kills the
// whole process tree and reaps it. It always waits on the child, so no orphan or
// zombie survives. A start failure is wrapped; otherwise the child's own exit
// error (or ctx.Err() on cancellation) is returned. Callers that want bounded
// byte slices back use run; callers with a custom bounded writer use this.
func (r *Runner) runWith(ctx context.Context, cmd *exec.Cmd, stdout, stderr io.Writer) error {
	return supervise(ctx, cmd, stdout, stderr)
}

// supervise is the package-level supervisor loop shared by Runner.run/runWith
// and the package-level hardware-accel probe. It applies the parent-loss death
// signal (Linux) and a dedicated process group, streams the child's output into
// the caller's bounded writers, and on ctx cancel/timeout kills the whole
// process tree and reaps it — always waiting on the child so no orphan or zombie
// survives. A start failure is wrapped; otherwise the child's own exit error (or
// ctx.Err() on cancellation) is returned.
func supervise(ctx context.Context, cmd *exec.Cmd, stdout, stderr io.Writer) error {
	applySysProcAttr(cmd)      // Linux Pdeathsig: reap the child if the server dies (parent loss)
	configureProcessGroup(cmd) // own process group: a kill takes the whole tree, not just the leader
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if startErr := cmd.Start(); startErr != nil {
		return fmt.Errorf("ffmpeg: start failed: %w", startErr)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		killProcessGroup(cmd)
		<-done // always reap: no orphaned transcode survives a cancel/timeout
		return ctx.Err()
	case waitErr := <-done:
		return waitErr
	}
}

// runDetail runs cmd under the supervisor for a spawn whose real output lands in
// files (transcode/extract/scale), so only a failure diagnostic is wanted. It
// returns the trimmed stderr (falling back to stdout, then the raw error) as the
// human-facing detail, or "" on success. Callers wrap the detail in their own
// operation-named error.
func (r *Runner) runDetail(ctx context.Context, cmd *exec.Cmd) (detail string, err error) {
	stdout, stderr, err := r.run(ctx, cmd)
	if err == nil {
		return "", nil
	}
	return captureDetail(stdout, stderr, err), err
}

// captureDetail folds a supervised child's bounded output into a single
// diagnostic string: stderr first (where ffmpeg logs at -v error), then stdout,
// then the process error itself if both streams were empty.
func captureDetail(stdout, stderr []byte, err error) string {
	if detail := strings.TrimSpace(string(stderr)); detail != "" {
		return detail
	}
	if detail := strings.TrimSpace(string(stdout)); detail != "" {
		return detail
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

// productionProtocolAllowlist is the least-privilege set of I/O protocols an
// ffmpeg/ffprobe input demuxer may use for server-side media processing:
// local files only. It denies every network and remote protocol
// (http, https, tcp, udp, rtmp, rtp, srtp, …), so a crafted container can never
// coerce ffmpeg into opening a URL — SSRF and data-exfiltration through a
// malicious media file are closed. It is applied as an input option immediately
// before each -i, scoping the input demuxer without restricting a legitimate
// local output (pipe/file) muxer.
const productionProtocolAllowlist = "file"

// inputProtocolArgs returns the least-privilege protocol-allowlist input option
// that MUST precede each -i (and the ffprobe input path). Keeping it a helper
// gives one source of truth for the allowlist and lets a guard test assert every
// input-reading spawn carries it.
func inputProtocolArgs() []string {
	return []string{"-protocol_whitelist", productionProtocolAllowlist}
}

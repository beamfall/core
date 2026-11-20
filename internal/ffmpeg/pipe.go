package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Encrypted-library derivative transcode (ADR-0235). An encrypted Library's
// source never exists as a plaintext file, so FFmpeg reads it from a
// loopback-only, token-guarded HTTP endpoint that decrypts ranges on demand, and
// writes a fragmented MP4 to its stdout for the caller to encrypt as it streams.
// The child gets no writable path at all: its only reach outside its own
// address space is that one loopback port.
//
// The transcode and the ffprobe run that measures its source first are the only
// spawns that may read over a network protocol. Every other input keeps the
// file-only productionProtocolAllowlist.

// pipeProtocolAllowlist is the input protocol set for a pipe transcode: plain
// HTTP over TCP, nothing else (no file, no https, no crypto, no concat).
const pipeProtocolAllowlist = "http,tcp"

// pipeFormatAllowlist pins the input demuxer to single-resource containers,
// video and the common music formats (MP3, FLAC, WAV, AIFF, raw AAC) alike.
// Playlist and manifest demuxers (hls, dash, concat, image sequences) are left
// out because they open further URLs a crafted source could choose.
const pipeFormatAllowlist = "mov,mp4,m4a,3gp,3g2,mj2,matroska,webm,mpegts,mpeg,avi,flv,asf,ogg,mp3,flac,wav,w64,aiff,aac"

// pipeMovflags fragments the output at every keyframe with an empty moov up
// front, so the MP4 can be written to a pipe without seeking back. faststart
// and global_sidx are deliberately absent: both rewrite the file afterwards.
const pipeMovflags = "frag_keyframe+empty_moov+default_base_moof"

// defaultPipeReadTimeout bounds one blocked read or write on the input.
const defaultPipeReadTimeout = 30 * time.Second

// pipeStderrLimit caps the diagnostic capture of a pipe transcode or probe.
const pipeStderrLimit = 64 << 10

// pipeProbeStdoutLimit caps a duration probe's output, which is one number.
const pipeProbeStdoutLimit = 4 << 10

var (
	// ErrPipeConfinementUnavailable is returned when this host cannot give the
	// pipe transcode an OS-enforced sandbox (Linux landlock through the shim,
	// or macOS sandbox-exec). The transcode refuses rather than run unconfined.
	ErrPipeConfinementUnavailable = errors.New("ffmpeg pipe transcode: no OS-enforced confinement on this host")
	// errPipeInput refuses any input that is not a plain http URL on
	// 127.0.0.1. It never echoes the URL, which carries a bearer token.
	errPipeInput = errors.New("ffmpeg pipe transcode: input must be a plain http URL on 127.0.0.1")
	// errPipeDuration refuses a probe that names no positive, finite duration.
	errPipeDuration = errors.New("ffprobe pipe probe: the source's duration is unknown")
)

// PipeInput names the loopback source of a pipe transcode. URL and Secret carry
// a bearer token: they are passed to FFmpeg but never logged, and any error
// detail that echoes them is redacted.
type PipeInput struct {
	URL    string
	Secret string
	// Timeout bounds one blocked read on the input; zero means 30 seconds.
	Timeout time.Duration
}

// TranscodeToPipe transcodes the loopback HTTP source in to a fragmented MP4
// written to stdout, under profile's codecs and bounds. FastStart is ignored.
// The input must be an http URL on 127.0.0.1; FFmpeg runs with that loopback
// port as its only network reach and no writable path, and the call refuses
// with ErrPipeConfinementUnavailable on a host that cannot enforce that.
//
// FFmpeg can exit 0 on a truncated input, so a nil error does not prove the
// output is whole: the caller validates what it read from stdout.
func (r *Runner) TranscodeToPipe(ctx context.Context, in PipeInput, profile ConvertProfile, stdout io.Writer) error {
	port, err := loopbackPort(in.URL)
	if err != nil {
		return err
	}
	if stdout == nil {
		return errors.New("ffmpeg pipe transcode: no output writer")
	}
	if profile.Container != "mp4" || profile.AudioOnly {
		return fmt.Errorf("ffmpeg pipe transcode: profile %q does not produce a video MP4", profile.Name)
	}
	if reason := profile.toneMapRefusal(); reason != "" {
		return fmt.Errorf("%w: %s", ErrToneMapRefused, reason)
	}
	profile.FastStart = false
	args := pipeArgs(in.URL, pipeReadTimeout(in), profile)
	return r.runPipeTool(ctx, port, in, "ffmpeg", "ffmpeg pipe transcode", args, stdout)
}

// ProbePipeDuration measures the playback length of the loopback HTTP source
// in with ffprobe, under the same confinement and input options as
// TranscodeToPipe. It fails unless ffprobe names a positive, finite duration
// for the container: "N/A", zero and non-numbers are refused.
func (r *Runner) ProbePipeDuration(ctx context.Context, in PipeInput) (time.Duration, error) {
	port, err := loopbackPort(in.URL)
	if err != nil {
		return 0, err
	}
	stdout := &boundedBuffer{limit: pipeProbeStdoutLimit}
	if err := r.runPipeTool(ctx, port, in, "ffprobe", "ffprobe pipe probe", pipeProbeArgs(in.URL, pipeReadTimeout(in)), stdout); err != nil {
		return 0, err
	}
	return parsePipeDuration(stdout.bytes())
}

// ExtractSubtitleToPipe extracts the text subtitle stream at the absolute
// ffprobe stream index from the loopback HTTP source in and converts it to
// WebVTT, under the same confinement and input options as TranscodeToPipe: the
// track never reaches a file. FFmpeg's output is held in a buffer of at most
// limit bytes, and a track that runs past it kills FFmpeg and fails with
// ErrOutputOverflow; only a complete, successful extraction is written to
// stdout, in one piece.
//
// Like ExtractSubtitleToWebVTT, decode errors are not made fatal, since one bad
// cue should not cost the track; the caller's endpoint is what proves the
// whole source was served.
func (r *Runner) ExtractSubtitleToPipe(ctx context.Context, in PipeInput, streamIndex, limit int, stdout io.Writer) error {
	port, err := loopbackPort(in.URL)
	if err != nil {
		return err
	}
	switch {
	case stdout == nil:
		return errors.New("ffmpeg pipe subtitle extract: no output writer")
	case streamIndex < 0:
		return fmt.Errorf("ffmpeg pipe subtitle extract: stream index must be >= 0, got %d", streamIndex)
	case limit <= 0:
		return errors.New("ffmpeg pipe subtitle extract: output limit must be positive")
	}
	out := &boundedBuffer{limit: limit}
	args := pipeSubtitleArgs(in.URL, pipeReadTimeout(in), streamIndex)
	if err := r.runPipeTool(ctx, port, in, "ffmpeg", "ffmpeg pipe subtitle extract", args, out); err != nil {
		return err
	}
	_, err = stdout.Write(out.bytes())
	return err
}

// runPipeTool runs the FFmpeg tool named by tool against a loopback input
// under the pipe confinement, streaming its stdout to stdout. what names the
// run in its errors, and a failure's diagnostic is redacted of in's URL and
// secret. A bounded stdout that overflows kills the tool, so an overproducing
// child cannot hold the worker until another limit fires.
func (r *Runner) runPipeTool(ctx context.Context, port int, in PipeInput, tool, what string, args []string, stdout io.Writer) error {
	return r.runPipeToolWith(ctx, port, in, tool, what, args, stdout, nil)
}

// runPipeToolWith is runPipeTool with a caller-sized stderr capture, for an
// analysis whose result FFmpeg prints on stderr. A nil stderr gets the default
// diagnostic bound. An overflow of either buffer kills the tool.
func (r *Runner) runPipeToolWith(ctx context.Context, port int, in PipeInput, tool, what string, args []string, stdout io.Writer, stderr *boundedBuffer) error {
	toolPath, err := exec.LookPath(tool)
	if err != nil {
		return fmt.Errorf("%s is not available: %w", tool, err)
	}
	cmd, err := r.pipeCommand(ctx, runtime.GOOS, port, toolPath, pipeArgsForToolContext(ctx, toolPath, args))
	if err != nil {
		return err
	}
	cmd.Env = pipeEnv(os.Environ())
	return r.runPipeCmdWith(ctx, cmd, in, what, stdout, stderr)
}

// runPipeCmd supervises a confined pipe command built by runPipeTool. A
// bounded stdout that overflows kills the command, as stderr does.
func (r *Runner) runPipeCmd(ctx context.Context, cmd *exec.Cmd, in PipeInput, what string, stdout io.Writer) error {
	return r.runPipeCmdWith(ctx, cmd, in, what, stdout, nil)
}

func (r *Runner) runPipeCmdWith(ctx context.Context, cmd *exec.Cmd, in PipeInput, what string, stdout io.Writer, stderr *boundedBuffer) error {
	var killOnce sync.Once
	kill := func() { killOnce.Do(func() { killProcessGroup(cmd) }) }
	if stderr == nil {
		stderr = &boundedBuffer{limit: pipeStderrLimit}
	}
	stderr.onOverflow = kill
	boundedOut, _ := stdout.(*boundedBuffer)
	if boundedOut != nil && boundedOut.onOverflow == nil {
		boundedOut.onOverflow = kill
	}
	runErr := r.runWith(ctx, cmd, stdout, stderr)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s interrupted: %w", what, ctxErr)
	}
	if stderr.overflowed() || (boundedOut != nil && boundedOut.overflowed()) {
		return ErrOutputOverflow
	}
	if runErr == nil {
		return nil
	}
	detail := stderr.bytes()
	if len(detail) > pipeStderrLimit {
		detail = detail[len(detail)-pipeStderrLimit:]
	}
	return fmt.Errorf("%s: %s", what, redactPipeDetail(captureDetail(nil, detail, runErr), in))
}

func pipeReadTimeout(in PipeInput) time.Duration {
	if in.Timeout <= 0 {
		return defaultPipeReadTimeout
	}
	return in.Timeout
}

// pipeInputOptions scope a pipe run's one input: HTTP over TCP only, no
// redirects, a bounded blocked read and single-resource demuxers. A build
// whose http protocol lacks -max_redirects runs without it (pipeArgsForTool).
func pipeInputOptions(timeout time.Duration) []string {
	return []string{
		"-protocol_whitelist", pipeProtocolAllowlist,
		"-max_redirects", "0",
		"-rw_timeout", strconv.FormatInt(timeout.Microseconds(), 10),
		"-format_whitelist", pipeFormatAllowlist,
	}
}

// pipeArgs builds the FFmpeg argv for a pipe transcode: input options scoped to
// the one loopback URL, the profile's encode, and fragmented MP4 to pipe:1.
// -xerror makes a decode error fatal: without it FFmpeg exits 0 on a truncated
// or corrupt input, having encoded only what it could read.
func pipeArgs(inputURL string, timeout time.Duration, profile ConvertProfile) []string {
	args := append([]string{"-v", "error", "-nostdin", "-xerror"}, pipeInputOptions(timeout)...)
	args = append(args, "-i", inputURL)
	args = append(args, profile.encodeArgs()...)
	return append(args, "-movflags", pipeMovflags, "-f", "mp4", "pipe:1")
}

// pipeSubtitleArgs builds the FFmpeg argv that converts one subtitle stream of
// the loopback source to WebVTT on pipe:1, under the same input options as
// pipeArgs.
func pipeSubtitleArgs(inputURL string, timeout time.Duration, streamIndex int) []string {
	args := append([]string{"-v", "error", "-nostdin"}, pipeInputOptions(timeout)...)
	return append(args,
		"-i", inputURL,
		"-map", "0:"+strconv.Itoa(streamIndex),
		"-c:s", "webvtt",
		"-f", "webvtt", "pipe:1")
}

// pipeProbeArgs builds the ffprobe argv that prints the loopback source's
// container duration in seconds, alone on one line, under the same input
// options as pipeArgs. ffprobe never reads stdin and has no -nostdin.
func pipeProbeArgs(inputURL string, timeout time.Duration) []string {
	args := append([]string{"-v", "error"}, pipeInputOptions(timeout)...)
	return append(args,
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		"-i", inputURL)
}

// parsePipeDuration reads ffprobe's container duration in seconds. Anything
// but a positive, finite number that fits a time.Duration is refused, and the
// output is never echoed.
func parsePipeDuration(out []byte) (time.Duration, error) {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return 0, errPipeDuration
	}
	nanos := seconds * float64(time.Second)
	if nanos >= math.MaxInt64 || nanos < 1 {
		return 0, errPipeDuration
	}
	return time.Duration(math.Round(nanos)), nil
}

// loopbackPort accepts only http://127.0.0.1:<port>/<path> with a conservative
// path alphabet, so FFmpeg's URL parser and Go's cannot disagree about the
// host, and returns the port.
func loopbackPort(raw string) (int, error) {
	const prefix = "http://127.0.0.1:"
	rest, ok := strings.CutPrefix(raw, prefix)
	if !ok {
		return 0, errPipeInput
	}
	portText, path, ok := strings.Cut(rest, "/")
	if !ok || portText == "" || len(portText) > 5 || !pipePathAllowed(path) {
		return 0, errPipeInput
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != portText {
		return 0, errPipeInput
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Opaque != "" || u.RawQuery != "" ||
		u.ForceQuery || u.Fragment != "" || u.Host != net.JoinHostPort("127.0.0.1", portText) {
		return 0, errPipeInput
	}
	return port, nil
}

func pipePathAllowed(path string) bool {
	for _, c := range path {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '/' || c == '-' || c == '_' || c == '.' || c == '~':
		default:
			return false
		}
	}
	return true
}

// pipeConfinementRefusal reports whether goos can confine a pipe transcode.
// Linux needs the re-exec shim to apply mandatory landlock; macOS wraps the
// child in sandbox-exec (checked at wrap time); nothing else is supported.
func pipeConfinementRefusal(goos, shimPath string) error {
	switch goos {
	case "linux":
		if shimPath == "" {
			return ErrPipeConfinementUnavailable
		}
		return nil
	case "darwin":
		return nil
	default:
		return ErrPipeConfinementUnavailable
	}
}

// pipeCommand builds the confined command for a pipe run of toolPath (FFmpeg
// or ffprobe). On Linux it runs the shim with --require-landlock and no
// granted paths, so a kernel without landlock ABI 3 or later (the first that
// fences truncate(2)) refuses the launch. On macOS it wraps the (optionally
// shimmed) command in a deny-default Seatbelt profile whose only network grant
// is the loopback port and which grants no writable path.
func (r *Runner) pipeCommand(ctx context.Context, goos string, port int, toolPath string, args []string) (*exec.Cmd, error) {
	binPath, binArgs := r.niceWrap(toolPath, args)
	return r.loopbackCommand(ctx, goos, port, binPath, binArgs)
}

// loopbackCommand is pipeCommand without the niceness wrapper: the same
// confinement around binPath and binArgs, for a run a viewer is waiting on.
func (r *Runner) loopbackCommand(ctx context.Context, goos string, port int, binPath string, binArgs []string) (*exec.Cmd, error) {
	if err := pipeConfinementRefusal(goos, r.shimPath); err != nil {
		return nil, err
	}
	if goos == "linux" {
		shimArgs := append([]string{shimSubcommand, "--require-landlock", "--", binPath}, binArgs...)
		// #nosec G204 -- the shim path is the server's own executable and the
		// arguments are a fixed subcommand, the resolved tool path and its argv.
		return exec.CommandContext(ctx, r.shimPath, shimArgs...), nil
	}
	inner := r.hardenedCommand(ctx, binPath, nil, nil, binArgs)
	sandboxPath, sandboxArgs, wrapped := sandboxWrapNet(port, inner.Path, inner.Args[1:])
	if !wrapped {
		return nil, ErrPipeConfinementUnavailable
	}
	// #nosec G204 -- sandbox-exec is resolved from PATH and wraps the confined
	// tool command built above.
	return exec.CommandContext(ctx, sandboxPath, sandboxArgs...), nil
}

// pipeEnv drops proxy settings so FFmpeg's HTTP client always connects to the
// loopback endpoint directly and never hands the token to a proxy.
func pipeEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		switch strings.ToLower(key) {
		case "http_proxy", "https_proxy", "all_proxy":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// redactPipeDetail strips the input URL and its secret from an FFmpeg
// diagnostic, which may quote the URL it failed to read.
func redactPipeDetail(detail string, in PipeInput) string {
	for _, secret := range []string{in.URL, in.Secret} {
		if secret != "" {
			detail = strings.ReplaceAll(detail, secret, "[redacted]")
		}
	}
	return detail
}

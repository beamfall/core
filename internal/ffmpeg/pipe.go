package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
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
// This is the only spawn that may read over a network protocol. Every other
// input keeps the file-only productionProtocolAllowlist.

// pipeProtocolAllowlist is the input protocol set for a pipe transcode: plain
// HTTP over TCP, nothing else (no file, no https, no crypto, no concat).
const pipeProtocolAllowlist = "http,tcp"

// pipeFormatAllowlist pins the input demuxer to single-resource containers.
// Playlist and manifest demuxers (hls, dash, concat, image sequences) are left
// out because they open further URLs a crafted source could choose.
const pipeFormatAllowlist = "mov,mp4,m4a,3gp,3g2,mj2,matroska,webm,mpegts,mpeg,avi,flv,asf,ogg"

// pipeMovflags fragments the output at every keyframe with an empty moov up
// front, so the MP4 can be written to a pipe without seeking back. faststart
// and global_sidx are deliberately absent: both rewrite the file afterwards.
const pipeMovflags = "frag_keyframe+empty_moov+default_base_moof"

// defaultPipeReadTimeout bounds one blocked read or write on the input.
const defaultPipeReadTimeout = 30 * time.Second

// pipeStderrLimit caps the diagnostic capture of a pipe transcode.
const pipeStderrLimit = 64 << 10

var (
	// ErrPipeConfinementUnavailable is returned when this host cannot give the
	// pipe transcode an OS-enforced sandbox (Linux landlock through the shim,
	// or macOS sandbox-exec). The transcode refuses rather than run unconfined.
	ErrPipeConfinementUnavailable = errors.New("ffmpeg pipe transcode: no OS-enforced confinement on this host")
	// errPipeInput refuses any input that is not a plain http URL on
	// 127.0.0.1. It never echoes the URL, which carries a bearer token.
	errPipeInput = errors.New("ffmpeg pipe transcode: input must be a plain http URL on 127.0.0.1")
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
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = defaultPipeReadTimeout
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg is not available: %w", err)
	}
	cmd, err := r.pipeCommand(ctx, runtime.GOOS, port, ffmpegPath, pipeArgs(in.URL, timeout, profile))
	if err != nil {
		return err
	}
	cmd.Env = pipeEnv(os.Environ())
	var killOnce sync.Once
	kill := func() { killOnce.Do(func() { killProcessGroup(cmd) }) }
	stderr := &boundedBuffer{limit: pipeStderrLimit, onOverflow: kill}
	runErr := r.runWith(ctx, cmd, stdout, stderr)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("ffmpeg pipe transcode interrupted: %w", ctxErr)
	}
	if stderr.overflowed() {
		return ErrOutputOverflow
	}
	if runErr == nil {
		return nil
	}
	return fmt.Errorf("ffmpeg pipe transcode: %s", redactPipeDetail(captureDetail(nil, stderr.bytes(), runErr), in))
}

// pipeArgs builds the FFmpeg argv for a pipe transcode: input options scoped to
// the one loopback URL, the profile's encode, and fragmented MP4 to pipe:1.
// -xerror makes a decode error fatal: without it FFmpeg exits 0 on a truncated
// or corrupt input, having encoded only what it could read.
func pipeArgs(inputURL string, timeout time.Duration, profile ConvertProfile) []string {
	args := []string{
		"-v", "error", "-nostdin", "-xerror",
		"-protocol_whitelist", pipeProtocolAllowlist,
		"-max_redirects", "0",
		"-rw_timeout", strconv.FormatInt(timeout.Microseconds(), 10),
		"-format_whitelist", pipeFormatAllowlist,
		"-i", inputURL,
	}
	args = append(args, profile.encodeArgs()...)
	return append(args, "-movflags", pipeMovflags, "-f", "mp4", "pipe:1")
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

// pipeCommand builds the confined pipe-transcode command. On Linux it runs the
// shim with --require-landlock and no granted paths, so a kernel without
// landlock refuses the launch. On macOS it wraps the (optionally shimmed)
// command in a deny-default Seatbelt profile whose only network grant is the
// loopback port and which grants no writable path.
func (r *Runner) pipeCommand(ctx context.Context, goos string, port int, ffmpegPath string, args []string) (*exec.Cmd, error) {
	if err := pipeConfinementRefusal(goos, r.shimPath); err != nil {
		return nil, err
	}
	binPath, binArgs := r.niceWrap(ffmpegPath, args)
	if goos == "linux" {
		shimArgs := append([]string{shimSubcommand, "--require-landlock", "--", binPath}, binArgs...)
		// #nosec G204 -- the shim path is the server's own executable and the
		// arguments are a fixed subcommand, the resolved FFmpeg path and its argv.
		return exec.CommandContext(ctx, r.shimPath, shimArgs...), nil
	}
	inner := r.hardenedCommand(ctx, binPath, nil, nil, binArgs)
	sandboxPath, sandboxArgs, wrapped := sandboxWrapNet(port, inner.Path, inner.Args[1:])
	if !wrapped {
		return nil, ErrPipeConfinementUnavailable
	}
	// #nosec G204 -- sandbox-exec is resolved from PATH and wraps the confined
	// FFmpeg command built above.
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

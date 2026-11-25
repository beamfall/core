package ffmpeg

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ShimFailureExitCode is the exit status the ffmpeg-shim uses when it fails
// before exec'ing its target: a malformed argv, an rlimit or confinement
// refusal, or an exec failure. It is distinct from every status FFmpeg itself
// returns for a decode, so a caller can tell "the hardening wrapper never ran
// FFmpeg" from "FFmpeg rejected the bytes" (CORE-0700). FFmpeg exits with the
// low byte of a negative AVERROR; 126 would need errno 130, which no supported
// platform raises from a decode.
const ShimFailureExitCode = 126

// decodeVerifyWindowTimeout bounds the wall-clock time of one decode-verify
// sample window. A window decodes decodeVerifySampleSeconds of media, which
// takes seconds even in software on weak hardware; a hostile file that wedges
// the demuxer (a seek loop, a stalled read) would otherwise hold a sweep slot
// until RLIMIT_CPU fires hours later. A timeout is a resource verdict, never a
// corrupt-file one: the item is counted as failed and retried next sweep. It
// is a variable only so a test can shorten it.
var decodeVerifyWindowTimeout = 5 * time.Minute

// decodeVerifyStderrTailBytes bounds the stderr tail decode-verify keeps for
// the diagnostic on an infrastructure failure.
const decodeVerifyStderrTailBytes = 2048

// FFmpeg exit statuses that mean the decoder or demuxer rejected the bytes it
// opened. FFmpeg 6.1 and later exit with the low byte of the failing AVERROR;
// older builds exit 1 for any failed conversion and 69 when the decode error
// rate is exceeded.
const (
	exitInvalidData    = 183 // AVERROR_INVALIDDATA
	exitEOF            = 187 // AVERROR_EOF: the container ends before its frames
	exitLegacyFailure  = 1   // pre-6.1 "Conversion failed!"
	exitLegacyErrorCap = 69  // pre-6.1 decode error rate exceeded
)

// decodeVerdict maps the outcome of one supervised decode-verify run to the
// seam's contract: broken=true only when FFmpeg ran and its decoder rejected
// the bytes. Everything else is an error that must never flag the file:
//
//   - a signal death (SIGKILL from the OOM killer or the supervisor, SIGXCPU or
//     SIGXFSZ from an rlimit, a SIGSEGV in a decoder),
//   - the hardening shim failing before FFmpeg ran (ShimFailureExitCode),
//   - an FFmpeg exit that names a system or resource failure (ENOENT, EACCES,
//     ENOMEM, EIO, a missing decoder or protocol), and
//   - a start failure, cancellation, timeout or surviving process group.
//
// stderrTail is the bounded tail of FFmpeg's diagnostics, quoted in the error.
func decodeVerdict(runErr error, stderrTail string) (bool, error) {
	if runErr == nil {
		return false, nil
	}
	if survived := survivedError(runErr); survived != nil {
		return false, fmt.Errorf("ffmpeg decode-verify: %w", survived)
	}
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		return false, fmt.Errorf("ffmpeg decode-verify: %w", runErr)
	}
	code := exitErr.ExitCode()
	switch code {
	case exitInvalidData, exitEOF, exitLegacyFailure, exitLegacyErrorCap:
		return true, nil
	}
	detail := lastLine(stderrTail)
	switch {
	case code < 0:
		return false, fmt.Errorf("ffmpeg decode-verify: %s, not a decode verdict%s", exitErr.String(), detail)
	case code == ShimFailureExitCode:
		return false, fmt.Errorf("ffmpeg decode-verify: the hardening shim failed before ffmpeg ran%s", detail)
	default:
		return false, fmt.Errorf("ffmpeg decode-verify: exit status %d is not a decoder data verdict%s", code, detail)
	}
}

// lastLine returns the last non-empty line of s as a ": <line>" suffix, or ""
// when s holds none.
func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
	}
	if s == "" {
		return ""
	}
	return ": " + s
}

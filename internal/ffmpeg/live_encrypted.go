package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// EncryptedLiveEndpoint is one encrypted live run's loopback endpoint
// (ADR-0236): FFmpeg reads the plaintext source from Source and uploads its
// init segment, media segments and playlist under Output with HTTP PUT. Both
// URLs carry the run's token, which Secret repeats so diagnostics can redact
// it. Timeout bounds a blocked source read; zero means the pipe default.
type EncryptedLiveEndpoint struct {
	Source  string
	Output  string
	Secret  string
	Timeout time.Duration
}

// ErrEncryptedLiveShape refuses a rendition the encrypted plane does not carry
// for 1.0: a burned-in subtitle or a composed picture (ADR-0236 D4).
var ErrEncryptedLiveShape = errors.New("ffmpeg: this stream shape is not available on an encrypted Library")

// StartEncryptedHLSSegments is StartHLSSegments for an Encrypted Library: the
// same segmenting, numbering and timestamps, but FFmpeg keeps no path at all.
// It reads the source from the endpoint and uploads every output file to it,
// under the pipe confinement (no writable path, one loopback port). Only
// software decoders, encoders and tone-mapping run, because a hardware device
// sits outside that confinement.
func (r *Runner) StartEncryptedHLSSegments(ctx context.Context, ep EncryptedLiveEndpoint, startSegment, segmentSeconds int, rendition Rendition) (*HLSProcess, error) {
	return r.startEncryptedHLSSegments(ctx, runtime.GOOS, ep, startSegment, segmentSeconds, rendition)
}

func (r *Runner) startEncryptedHLSSegments(ctx context.Context, goos string, ep EncryptedLiveEndpoint, startSegment, segmentSeconds int, rendition Rendition) (*HLSProcess, error) {
	args, port, err := encryptedSegmentArgs(ep, startSegment, segmentSeconds, r.withQuality(rendition))
	if err != nil {
		return nil, err
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is not available: %w", err)
	}
	// Live playback is not niced: a viewer is waiting on it.
	cmd, err := r.loopbackCommand(ctx, goos, port, ffmpegPath, pipeArgsForTool(ffmpegPath, args))
	if err != nil {
		return nil, err
	}
	cmd.Env = pipeEnv(os.Environ())
	process, err := startWithheldHLSProcess(cmd)
	if err != nil {
		return nil, fmt.Errorf("starting ffmpeg transcode: %s", redactSecret(err.Error(), ep.Secret))
	}
	return process, nil
}

// startWithheldHLSProcess starts cmd with its stderr discarded, so nothing the
// process writes there reaches a log (see HLSProcess.stderrWithheld).
func startWithheldHLSProcess(cmd *exec.Cmd) (*HLSProcess, error) {
	cmd.Stderr = io.Discard
	cmd.WaitDelay = hlsStderrWaitDelay
	process, err := startHLSProcess(cmd)
	if err != nil {
		return nil, err
	}
	process.stderrWithheld = true
	return process, nil
}

// encryptedSegmentArgs validates the endpoint and the rendition and builds the
// argv. It returns the endpoint's port for the sandbox grant.
func encryptedSegmentArgs(ep EncryptedLiveEndpoint, startSegment, segmentSeconds int, rendition Rendition) ([]string, int, error) {
	if _, ok := rendition.Picture(); ok {
		return nil, 0, ErrEncryptedLiveShape
	}
	if _, ok := rendition.burnsSubtitle(); ok {
		return nil, 0, ErrEncryptedLiveShape
	}
	if reason := rendition.ToneMapRefusal(); reason != "" {
		return nil, 0, errors.New("ffmpeg: " + reason)
	}
	if !rendition.adjust.IsZero() && !rendition.AudioOnly {
		return nil, 0, errors.New("ffmpeg: an adjusted encode must be audio-only or a composed picture")
	}
	if startSegment < 0 || segmentSeconds <= 0 {
		return nil, 0, fmt.Errorf("ffmpeg: invalid segment start %d of %ds", startSegment, segmentSeconds)
	}
	port, err := loopbackPort(ep.Source)
	if err != nil {
		return nil, 0, err
	}
	outPort, err := loopbackPort(ep.Output + "/")
	if err != nil || outPort != port || strings.HasSuffix(ep.Output, "/") {
		return nil, 0, errPipeInput
	}
	// Software only, whatever the caller's rendition carried.
	rendition = rendition.withHWAccel("").withToneMapAccel("")
	timeout := ep.Timeout
	if timeout <= 0 {
		timeout = defaultPipeReadTimeout
	}
	io := segmentIO{
		input:        ep.Source,
		inputOptions: pipeInputOptions(timeout),
		outputArgs: []string{
			// The output is an HTTP upload, so it gets its own allowlist
			// (ADR-0236 D5). temp_file applies only to the file protocol.
			"-method", "PUT",
			"-protocol_whitelist", pipeProtocolAllowlist,
			"-hls_segment_filename", ep.Output + "/seg%05d.m4s",
			"--", ep.Output + "/live.m3u8",
		},
	}
	return segmentArgsFor(io, startSegment, segmentSeconds, rendition), port, nil
}

// redactSecret replaces every occurrence of secret in s.
func redactSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

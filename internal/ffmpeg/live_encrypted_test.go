package ffmpeg

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

const testLiveToken = "0123456789abcdef0123456789abcdef"

func testEncryptedEndpoint() EncryptedLiveEndpoint {
	base := "http://127.0.0.1:43210/" + testLiveToken
	return EncryptedLiveEndpoint{Source: base + "/source", Output: base + "/out/r", Secret: testLiveToken}
}

func TestEncryptedSegmentArgsRunSoftwareAndUploadOverLoopback(t *testing.T) {
	ep := testEncryptedEndpoint()
	for _, accel := range []HWAccel{"", HWAccelVideoToolbox, HWAccelVAAPI, HWAccelNVENC, HWAccelQSV} {
		t.Run(string(accel)+"-", func(t *testing.T) {
			rendition := Rendition{Height: 720, VideoCodec: "h264", VideoKbps: 3000, AudioCodec: "aac", AudioChannels: 2}.
				withHWAccel(accel).withToneMapAccel(accel)
			args, port, err := encryptedSegmentArgs(ep, 3, 4, rendition)
			if err != nil {
				t.Fatal(err)
			}
			if port != 43210 {
				t.Fatalf("port %d", port)
			}
			joined := strings.Join(args, " ")
			for _, hw := range []string{"-hwaccel", "videotoolbox", "vaapi", "nvenc", "qsv", "cuda"} {
				if strings.Contains(joined, hw) {
					t.Fatalf("encrypted args carry %q: %v", hw, args)
				}
			}
			if !containsArgPair(args, "-i", ep.Source) {
				t.Fatalf("input is not the endpoint source: %v", args)
			}
			tail := []string{
				"-method", "PUT",
				"-protocol_whitelist", pipeProtocolAllowlist,
				"-hls_segment_filename", ep.Output + "/seg%05d.m4s",
				"--", ep.Output + "/live.m3u8",
			}
			if len(args) < len(tail) || !equalArgs(args[len(args)-len(tail):], tail) {
				t.Fatalf("output tail %v, want %v", args[max(0, len(args)-len(tail)):], tail)
			}
			if !containsArgPair(args, "-start_number", "3") {
				t.Fatalf("segment numbering lost: %v", args)
			}
			// No argument names a filesystem path.
			for _, arg := range args {
				if strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, "file:") {
					t.Fatalf("argument %q names a path", arg)
				}
			}
		})
	}
}

func TestEncryptedSegmentArgsRefuseShapesAndEndpoints(t *testing.T) {
	plain := Rendition{Height: 720, VideoCodec: "h264", AudioCodec: "aac", AudioChannels: 2}
	ep := testEncryptedEndpoint()
	other := ep
	other.Output = strings.Replace(ep.Output, "43210", "43211", 1)
	slash := ep
	slash.Output += "/"
	remote := ep
	remote.Output = strings.Replace(ep.Output, "127.0.0.1", "192.0.2.1", 1)
	file := ep
	file.Source = "file:///tmp/source.mp4"
	for name, tc := range map[string]struct {
		ep        EncryptedLiveEndpoint
		start     int
		segSec    int
		rendition Rendition
		want      error
	}{
		"composed picture": {ep, 0, 4, plain.WithComposedPicture(ComposedPicture{TimedTextPath: "/x.lrc"}), ErrEncryptedLiveShape},
		"burned subtitle":  {ep, 0, 4, plain.WithBurnSubtitle(2), ErrEncryptedLiveShape},
		"adjusted video":   {ep, 0, 4, plain.WithAudioAdjust(AudioAdjust{PitchSemitones: 2}), nil},
		"negative start":   {ep, -1, 4, plain, nil},
		"zero duration":    {ep, 0, 0, plain, nil},
		"other port":       {other, 0, 4, plain, errPipeInput},
		"trailing slash":   {slash, 0, 4, plain, errPipeInput},
		"remote output":    {remote, 0, 4, plain, errPipeInput},
		"file source":      {file, 0, 4, plain, errPipeInput},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := encryptedSegmentArgs(tc.ep, tc.start, tc.segSec, tc.rendition)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("err %v, want %v", err, tc.want)
			}
			if _, err := New().StartEncryptedHLSSegments(context.Background(), tc.ep, tc.start, tc.segSec, tc.rendition); err == nil {
				t.Fatal("StartEncryptedHLSSegments accepted it")
			}
		})
	}
	// An audio-only adjusted encode is a plain audio rung and is allowed.
	audio := Rendition{AudioOnly: true, AudioCodec: "aac", AudioChannels: 2}.WithAudioAdjust(AudioAdjust{PitchSemitones: 2})
	if _, _, err := encryptedSegmentArgs(ep, 0, 4, audio); err != nil {
		t.Fatalf("adjusted audio-only rung refused: %v", err)
	}
}

func TestRedactSecret(t *testing.T) {
	if got := redactSecret("GET /tok/source and /tok/out/r", "tok"); got != "GET /[redacted]/source and /[redacted]/out/r" {
		t.Fatalf("redacted %q", got)
	}
	if got := redactSecret("no token here", ""); got != "no token here" {
		t.Fatalf("empty secret changed the text: %q", got)
	}
}

// TestEncryptedRunWithholdsStderr: an encrypted run's FFmpeg decodes an
// Encrypted Library's plaintext, so nothing it writes to stderr may reach the
// tail the session engine logs.
func TestEncryptedRunWithholdsStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	const marker = "decoded-source-bytes"
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", "echo "+marker+" >&2; exit 3")
	p, err := startWithheldHLSProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err == nil {
		t.Fatal("process exit status lost")
	}
	if tail := p.StderrTail(); strings.Contains(tail, marker) || tail == "" {
		t.Fatalf("StderrTail %q, want a placeholder without the process output", tail)
	}
}

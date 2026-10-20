package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// forbiddenCapturePatterns are the raw exec-and-capture calls production ffmpeg
// code must never make: they buffer unbounded child output and reap nothing on
// cancel/timeout. Every production spawn goes through the bounded fail-closed
// supervisor (Runner.run/runWith/supervise) instead. supervisor.go is the one
// file allowed to touch the exec plumbing directly.
var forbiddenCapturePatterns = []string{".CombinedOutput()", ".Output()", "cmd.Run()"}

// supervisorSourceFile is the single production file exempt from the raw-capture
// ban — it IS the supervisor, so it owns Start/Wait/kill on the child.
const supervisorSourceFile = "supervisor.go"

// TestNoRawExecCaptureInProductionCode enforces "prohibit raw production exec/
// capture": a static scan of every non-test .go file in the
// package fails if any raw capture call survives outside the supervisor. This is
// the guard that keeps a future spawn from bypassing the bounded supervisor.
func TestNoRawExecCaptureInProductionCode(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == supervisorSourceFile {
			continue
		}
		data, err := os.ReadFile(name) // #nosec G304 -- test scans this package's own source files by name
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		for _, bad := range forbiddenCapturePatterns {
			if strings.Contains(src, bad) {
				t.Errorf("%s contains raw capture %q — route the spawn through the bounded supervisor (Runner.run/runWith)", name, bad)
			}
		}
	}
}

// TestInputProtocolArgsIsLeastPrivilege pins the least-privilege protocol
// allowlist to file-only: no network or remote protocol is permitted for an
// ffmpeg/ffprobe input.
func TestInputProtocolArgsIsLeastPrivilege(t *testing.T) {
	got := inputProtocolArgs()
	want := []string{"-protocol_whitelist", "file"}
	if !slices.Equal(got, want) {
		t.Fatalf("inputProtocolArgs() = %v, want %v", got, want)
	}
}

// TestHLSArgsCarryFileOnlyProtocolAllowlist proves the shipping transcode path
// applies the least-privilege allowlist as an input option immediately before
// the -i it guards.
func TestHLSArgsCarryFileOnlyProtocolAllowlist(t *testing.T) {
	args := hlsArgs("/src/movie.mkv", "/out", Rendition{Height: 720, VideoKbps: 4000})
	iIdx := slices.Index(args, "-i")
	if iIdx < 2 {
		t.Fatalf("no -i in hlsArgs output: %v", args)
	}
	if args[iIdx-2] != "-protocol_whitelist" || args[iIdx-1] != "file" {
		t.Fatalf("protocol allowlist not immediately before -i: %v", args)
	}
}

// TestProtocolAllowlistDeniesNetworkInput proves the allowlist actually denies a
// network protocol at the ffmpeg layer: ffprobe with `-protocol_whitelist file`
// rejects an http input up front (before any network I/O) rather than dialing
// out. This is the defense-in-depth that stops a crafted container from making
// ffmpeg fetch a URL.
func TestProtocolAllowlistDeniesNetworkInput(t *testing.T) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed; skipping real-binary protocol-denial test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// A link-local address that will not be dialed: the whitelist rejects the
	// protocol before any connection attempt.
	args := append(append([]string{"-v", "error"}, inputProtocolArgs()...),
		"-i", "http://169.254.169.254/latest/meta-data")
	out, err := exec.CommandContext(ctx, ffprobe, args...).CombinedOutput() // #nosec G204 -- fixed test args: file-only allowlist + a static URL
	if err == nil {
		t.Fatalf("ffprobe accepted an http input under a file-only allowlist:\n%s", out)
	}
	lower := strings.ToLower(string(out))
	if !strings.Contains(lower, "whitelist") && !strings.Contains(lower, "protocol") {
		t.Fatalf("http input failed for an unexpected reason (want a protocol/whitelist rejection):\n%s", out)
	}
}

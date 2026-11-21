package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// FFmpeg 8.0.1's http protocol help (Alpine 3.23): no max_redirects.
//
// #nosec G101 -- FFmpeg option listing, not a credential.
const httpHelpWithoutMaxRedirects = `http AVOptions:
  -seekable          <boolean>    .D......... control seekability of connection (default auto)
  -reconnect_streamed <boolean>    .D......... auto reconnect streamed / non seekable streams (default false)
`

// FFmpeg 7.1.4-Jellyfin and 9.0.2 list it.
const httpHelpWithMaxRedirects = httpHelpWithoutMaxRedirects +
	"  -max_redirects     <int>        .D......... Maximum number of redirects (from 0 to INT_MAX) (default 8)\n"

func withHTTPProtocolHelp(t *testing.T, run httpProtocolHelpRunner) *int {
	t.Helper()
	calls := 0
	maxRedirectsMu.Lock()
	prevRun, prevCache := httpProtocolHelp, maxRedirectsByTool
	httpProtocolHelp = func(ctx context.Context, toolPath string) ([]byte, error) {
		calls++
		return run(ctx, toolPath)
	}
	maxRedirectsByTool = map[string]*maxRedirectsEntry{}
	maxRedirectsMu.Unlock()
	t.Cleanup(func() {
		maxRedirectsMu.Lock()
		httpProtocolHelp, maxRedirectsByTool = prevRun, prevCache
		maxRedirectsMu.Unlock()
	})
	return &calls
}

func TestPipeArgsForToolKeepsRedirectGuardWhenSupported(t *testing.T) {
	withHTTPProtocolHelp(t, func(context.Context, string) ([]byte, error) {
		return []byte(httpHelpWithMaxRedirects), nil
	})
	args := pipeProbeArgs("http://127.0.0.1:1234/src", time.Second)
	got := mustPipeArgs(t, "/opt/ffmpeg-9/ffprobe", args)
	if !slices.Equal(got, args) {
		t.Fatalf("argv changed on a build with -max_redirects:\n got %q\nwant %q", got, args)
	}
	if !hasPair(got, "-max_redirects", "0") {
		t.Fatalf("argv lost the redirect guard: %q", got)
	}
}

func TestPipeArgsForToolDropsRedirectGuardOnlyWhenUnsupported(t *testing.T) {
	calls := withHTTPProtocolHelp(t, func(context.Context, string) ([]byte, error) {
		return []byte(httpHelpWithoutMaxRedirects), nil
	})
	profile := ConvertProfile{Name: "t", Container: "mp4", VideoCodec: "libx264", AudioCodec: "aac"}
	args := pipeArgs("http://127.0.0.1:1234/src", time.Second, profile)
	got := mustPipeArgs(t, "/usr/bin/ffmpeg", args)
	if slices.Contains(got, "-max_redirects") {
		t.Fatalf("argv kept -max_redirects on a build without it: %q", got)
	}
	if want := withoutMaxRedirects(args); !slices.Equal(got, want) || len(got) != len(args)-2 {
		t.Fatalf("argv = %q, want only the -max_redirects pair removed from %q", got, args)
	}
	// The rest of the input scoping is untouched.
	for _, pair := range [][2]string{{"-protocol_whitelist", pipeProtocolAllowlist}, {"-format_whitelist", pipeFormatAllowlist}} {
		if !hasPair(got, pair[0], pair[1]) {
			t.Fatalf("argv lost %s: %q", pair[0], got)
		}
	}
	_ = mustPipeArgs(t, "/usr/bin/ffmpeg", args)
	if *calls != 1 {
		t.Fatalf("help probed %d times for one tool, want 1", *calls)
	}
}

func TestPipeArgsForToolKeepsGuardWhenListingFails(t *testing.T) {
	withHTTPProtocolHelp(t, func(context.Context, string) ([]byte, error) {
		return nil, errors.New("exit status 1")
	})
	args := pipeProbeArgs("http://127.0.0.1:1234/src", time.Second)
	if got := mustPipeArgs(t, "/broken/ffprobe", args); !slices.Equal(got, args) {
		t.Fatalf("a failed listing dropped the guard: %q", got)
	}
}

// TestPipeArgsForToolAcceptedByRealBinary runs the adapted input options
// through the resolved FFmpeg's own argv parser: whatever the build, the argv
// must parse (it then fails to connect to a closed port, not on an option).
func TestPipeArgsForToolAcceptedByRealBinary(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not on PATH", tool)
		}
		withHTTPProtocolHelp(t, defaultHTTPProtocolHelp)
		args := append([]string{"-hide_banner", "-v", "error"}, mustPipeArgs(t, path, pipeInputOptions(time.Second))...)
		args = append(args, "-i", "http://127.0.0.1:9/src")
		if tool == "ffmpeg" {
			args = append(args, "-f", "null", "-")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		// #nosec G204 -- resolved tool with fixed arguments and a closed loopback port.
		out, _ := exec.CommandContext(ctx, path, args...).CombinedOutput()
		cancel()
		for _, bad := range []string{"Unrecognized option", "Option not found", "Error splitting the argument list"} {
			if bytes.Contains(out, []byte(bad)) {
				t.Fatalf("%s rejected the pipe input options: %s", tool, out)
			}
		}
	}
}

func hasPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestPipeArgsReprobeWhenToolReplacedInPlace(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "ffprobe")
	if err := os.WriteFile(tool, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	help := httpHelpWithMaxRedirects
	calls := withHTTPProtocolHelp(t, func(context.Context, string) ([]byte, error) {
		return []byte(help), nil
	})
	args := pipeInputOptions(time.Second)
	if got := mustPipeArgs(t, tool, args); !hasPair(got, maxRedirectsOption, "0") {
		t.Fatalf("supported build lost the guard: %v", got)
	}
	// Replace the binary at the same path with a build that lacks the option.
	help = httpHelpWithoutMaxRedirects
	if err := os.WriteFile(tool, []byte("new build"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := mustPipeArgs(t, tool, args); slices.Contains(got, maxRedirectsOption) {
		t.Fatalf("replaced unsupported build kept the guard: %v", got)
	}
	if *calls != 2 {
		t.Fatalf("probes = %d, want 2 (one per binary identity)", *calls)
	}
}

func mustPipeArgs(t *testing.T, toolPath string, args []string) []string {
	t.Helper()
	got, err := pipeArgsForToolContext(context.Background(), toolPath, args)
	if err != nil {
		t.Fatalf("pipeArgsForToolContext(%s) = %v", toolPath, err)
	}
	return got
}

// TestPipeArgsForToolFailsOnSurvivorAndAsksAgain injects a surviving group
// through the help runner: no argv comes back, so no pipe job starts, and the
// survivor is not cached as a verdict (CORE-2757).
func TestPipeArgsForToolFailsOnSurvivorAndAsksAgain(t *testing.T) {
	survived := true
	calls := withHTTPProtocolHelp(t, func(context.Context, string) ([]byte, error) {
		if survived {
			return nil, fmt.Errorf("%w: process group 7", errGroupSurvived)
		}
		return []byte(httpHelpWithMaxRedirects), nil
	})
	args := pipeProbeArgs("http://127.0.0.1:1234/src", time.Second)
	if got, err := pipeArgsForToolContext(t.Context(), "/usr/bin/ffprobe", args); !errors.Is(err, errGroupSurvived) || got != nil {
		t.Fatalf("pipeArgsForToolContext = %q, %v; want no argv and the survivor", got, err)
	}
	survived = false
	if got := mustPipeArgs(t, "/usr/bin/ffprobe", args); !slices.Equal(got, args) {
		t.Fatalf("argv after the survivor cleared = %q, want %q", got, args)
	}
	if *calls != 2 {
		t.Fatalf("help probed %d times, want 2: a survivor is not a cached verdict", *calls)
	}
}

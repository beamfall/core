package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/beamfall/core/internal/ffmpeg"
)

// rlimitReport is the JSON the re-exec target prints so the parent can confirm
// the shim lowered the child's rlimits before exec.
type rlimitReport struct {
	NoFileCur uint64 `json:"nofile_cur"`
	NoFileMax uint64 `json:"nofile_max"`
	FSizeCur  uint64 `json:"fsize_cur"`
	FSizeMax  uint64 `json:"fsize_max"`
	CPUMax    uint64 `json:"cpu_max"`
}

// shimReportEnv, when set, makes TestMain print this process's current rlimits as
// rlimitReport JSON and exit — letting the shim test exec "the ffmpeg binary"
// (this very test binary) and read back the limits it inherited.
const shimReportEnv = "BEAMFALL_SHIM_TEST_REPORT"

// TestMain gives the test binary two extra roles the rlimit end-to-end test
// re-execs into, because a `go test` binary never calls package main's main()/
// run() — so the real ffmpeg-shim dispatch must be reproduced here:
//
//   - shim role: args start with "ffmpeg-shim" → run the real ffmpegShim
//     (applies rlimits, then execs the next arg). This is the genuine shim code
//     path under test, not a reimplementation.
//   - report role: the shim execs the test binary in report mode (gated on the
//     BEAMFALL_SHIM_TEST_REPORT env, which only the final child honors because
//     the shim role is checked first) → print the inherited rlimits and exit.
//
// Everything else runs the normal test suite.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "ffmpeg-shim" {
		if err := ffmpegShim(context.Background(), os.Args[2:]); err != nil {
			_, _ = os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		os.Exit(0) // unreachable on success (exec replaces the image)
	}
	if os.Getenv(shimReportEnv) != "" {
		rep := rlimitReport{}
		var l unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &l); err == nil {
			rep.NoFileCur, rep.NoFileMax = l.Cur, l.Max
		}
		if err := unix.Getrlimit(unix.RLIMIT_FSIZE, &l); err == nil {
			rep.FSizeCur, rep.FSizeMax = l.Cur, l.Max
		}
		if err := unix.Getrlimit(unix.RLIMIT_CPU, &l); err == nil {
			rep.CPUMax = l.Max
		}
		_ = json.NewEncoder(os.Stdout).Encode(rep)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestParseShimArgs pins the shim argv parser: --ro/--rw collect their paths,
// "--" terminates the flag section, and the remainder is the verbatim command.
func TestParseShimArgs(t *testing.T) {
	ro, rw, argv, err := parseShimArgs([]string{
		"--ro", "/media/in",
		"--rw", "/var/out",
		"--rw", "/tmp/seg",
		"--",
		"/usr/bin/ffmpeg", "-i", "/media/in/m.mkv",
	})
	if err != nil {
		t.Fatalf("parseShimArgs: %v", err)
	}
	if !reflect.DeepEqual(ro, []string{"/media/in"}) {
		t.Errorf("ro = %q", ro)
	}
	if !reflect.DeepEqual(rw, []string{"/var/out", "/tmp/seg"}) {
		t.Errorf("rw = %q", rw)
	}
	if !reflect.DeepEqual(argv, []string{"/usr/bin/ffmpeg", "-i", "/media/in/m.mkv"}) {
		t.Errorf("argv = %q", argv)
	}
}

func TestParseShimArgsRejectsMissingCommand(t *testing.T) {
	if _, _, _, err := parseShimArgs([]string{"--ro", "/x", "--"}); err == nil {
		t.Fatal("parseShimArgs accepted an empty command after --")
	}
	if _, _, _, err := parseShimArgs([]string{"--ro", "/x"}); err == nil {
		t.Fatal("parseShimArgs accepted args with no -- terminator")
	}
	if _, _, _, err := parseShimArgs([]string{"--ro"}); err == nil {
		t.Fatal("parseShimArgs accepted --ro with no path")
	}
	if _, _, _, err := parseShimArgs([]string{"--bogus", "--", "/bin/true"}); err == nil {
		t.Fatal("parseShimArgs accepted an unknown flag")
	}
}

// TestFFmpegShimAppliesRlimits is the real end-to-end check of criterion (a) on
// darwin: it runs the shim (this test binary's ffmpeg-shim subcommand) which
// lowers its rlimits and execs "the ffmpeg binary" — here the same test binary
// in report mode — and confirms the exec'd child inherited the lowered limits.
// This exercises the genuine syscall path (Getrlimit/Setrlimit + syscall.Exec),
// not a mock. Landlock is not exercised here (Linux-only); only the portable
// rlimit + exec half runs on darwin.
func TestFFmpegShimAppliesRlimits(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	dir := t.TempDir()
	// Shim argv: confine to a real dir (so the parse succeeds), then exec this
	// binary in report mode as the stand-in "ffmpeg".
	cmd := exec.Command(self, append([]string{"ffmpeg-shim", "--ro", dir, "--"}, self)...)
	cmd.Env = append(os.Environ(), shimReportEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shim run failed: %v\n%s", err, out)
	}
	// The report is the last JSON line (the shim itself is silent on success).
	line := lastJSONLine(string(out))
	if line == "" {
		t.Fatalf("no rlimit report in output:\n%s", out)
	}
	var rep rlimitReport
	if err := json.Unmarshal([]byte(line), &rep); err != nil {
		t.Fatalf("decode report %q: %v", line, err)
	}

	want := ffmpeg.DefaultRlimits()
	// NOFILE and FSIZE are enforced on darwin and Linux alike; assert the child
	// inherited exactly the lowered ceilings (the host defaults are far higher).
	if rep.NoFileMax != want.NoFile {
		t.Errorf("child RLIMIT_NOFILE max = %d, want %d (shim did not lower it)", rep.NoFileMax, want.NoFile)
	}
	if rep.NoFileCur != want.NoFile {
		t.Errorf("child RLIMIT_NOFILE cur = %d, want %d", rep.NoFileCur, want.NoFile)
	}
	if rep.FSizeMax != want.FSizeBytes {
		t.Errorf("child RLIMIT_FSIZE max = %d, want %d", rep.FSizeMax, want.FSizeBytes)
	}
	if rep.CPUMax != want.CPUSeconds {
		t.Errorf("child RLIMIT_CPU max = %d, want %d", rep.CPUMax, want.CPUSeconds)
	}
}

// TestFFmpegShimRejectsBadArgs proves the shim surfaces a parse error rather
// than exec'ing something unexpected.
func TestFFmpegShimRejectsBadArgs(t *testing.T) {
	if err := ffmpegShim(context.Background(), []string{"--ro", "/x"}); err == nil {
		t.Fatal("ffmpegShim accepted args with no command")
	}
}

func lastJSONLine(out string) string {
	var last string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "{") && strings.HasSuffix(l, "}") {
			last = l
		}
	}
	return last
}

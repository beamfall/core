package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
)

// Adversarial end-to-end checks of the mandatory media confinement
// (CORE-0700). Real Runner entrypoints spawn through the real spawn seam and
// the real ffmpeg-shim (this test binary in its shim role), with this host's
// real kernel confinement: landlock on Linux, Seatbelt on macOS. The "ffmpeg"
// and "ffprobe" they launch are hard links to this test binary that, in the
// probe role, behave like a decoder that a crafted file has taken over: they
// try to read and write outside the job's grants and report what the kernel
// allowed.

const (
	// confineProbeEnv carries the probe spec (JSON) to the hostile tool.
	confineProbeEnv = "BEAMFALL_CONFINE_PROBE"
	// confineSleeperEnv makes this binary a grandchild that just waits to be
	// killed, standing in for a helper process FFmpeg forked.
	confineSleeperEnv = "BEAMFALL_CONFINE_SLEEPER"
	// confineReportPrefix marks the hostile tool's report line on stderr.
	confineReportPrefix = "CONFINE-PROBE "
	// confineProbeExit is the hostile tool's exit status after reporting.
	confineProbeExit = 3
)

// confineProbeSpec tells the hostile tool what to attempt.
type confineProbeSpec struct {
	Secret       string `json:"secret"`         // file outside every grant: must stay unreadable
	Input        string `json:"input"`          // file in the read-only grant: must be readable (control)
	ReadOnlyDir  string `json:"read_only_dir"`  // read-only grant: must not be writable
	OutsideDir   string `json:"outside_dir"`    // outside every grant: must not be writable
	ReadWriteDir string `json:"read_write_dir"` // optional read-write grant: must be writable (control)
	Sleeper      bool   `json:"sleeper"`        // fork a grandchild, record its pid, then block
}

// confineProbeReport is what the hostile tool observed: "ok" or the error.
type confineProbeReport struct {
	SecretRead     string `json:"secret_read"`
	InputRead      string `json:"input_read"`
	ReadOnlyWrite  string `json:"read_only_write"`
	OutsideWrite   string `json:"outside_write"`
	ReadWriteWrite string `json:"read_write_write,omitempty"`
}

// runConfineProbeRoles runs this binary as the sleeper or the hostile tool
// when TestMain was exec'd into one of those roles, and exits; otherwise it
// returns. The sleeper is checked first: it inherits the probe env.
func runConfineProbeRoles() {
	if os.Getenv(confineSleeperEnv) != "" {
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}
	if len(os.Args) >= 3 && os.Args[1] == "-hide_banner" && os.Args[2] == "-h" {
		os.Exit(0)
	}
	raw := os.Getenv(confineProbeEnv)
	if raw == "" {
		return
	}
	var spec confineProbeSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		_, _ = os.Stderr.WriteString("bad probe spec: " + err.Error() + "\n")
		os.Exit(2)
	}
	rep := confineProbeReport{
		SecretRead:    attempt(func() error { _, err := os.ReadFile(spec.Secret); return err }),
		InputRead:     attempt(func() error { _, err := os.ReadFile(spec.Input); return err }),
		ReadOnlyWrite: attempt(func() error { return writeProbeFile(spec.ReadOnlyDir) }),
		OutsideWrite:  attempt(func() error { return writeProbeFile(spec.OutsideDir) }),
	}
	if spec.ReadWriteDir != "" {
		rep.ReadWriteWrite = attempt(func() error { return writeProbeFile(spec.ReadWriteDir) })
	}
	line, _ := json.Marshal(rep)
	_, _ = os.Stderr.WriteString(confineReportPrefix + string(line) + "\n")
	if spec.Sleeper {
		forkSleeperAndBlock(spec.ReadWriteDir)
	}
	os.Exit(confineProbeExit)
}

func attempt(fn func() error) string {
	if err := fn(); err != nil {
		return err.Error()
	}
	return "ok"
}

func writeProbeFile(dir string) error {
	return os.WriteFile(filepath.Join(dir, "escaped.txt"), []byte("escaped"), 0o600) // #nosec G703 -- the confinement probe deliberately writes outside its grant to prove the sandbox refuses it
}

// forkSleeperAndBlock starts a grandchild in this process group, records its
// pid in dir/grandchild.pid, and blocks until killed.
func forkSleeperAndBlock(dir string) {
	self, err := os.Executable()
	if err != nil {
		os.Exit(4)
	}
	child := exec.Command(self) // #nosec G204 -- re-exec of this test binary
	child.Env = append(os.Environ(), confineSleeperEnv+"=1")
	if err := child.Start(); err != nil {
		_, _ = os.Stderr.WriteString("sleeper start: " + err.Error() + "\n")
		os.Exit(5)
	}
	if err := os.WriteFile(filepath.Join(dir, "grandchild.pid"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		_, _ = os.Stderr.WriteString("pid write: " + err.Error() + "\n")
		os.Exit(6)
	}
	time.Sleep(2 * time.Minute)
	os.Exit(0)
}

// confineFixture is one hostile-tool setup: a PATH holding "ffmpeg" and
// "ffprobe" hard links to this binary, plus the directories the probe targets.
type confineFixture struct {
	inputDir, outDir, outsideDir string
	input, secret                string
}

func newConfineFixture(t *testing.T) confineFixture {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "darwin":
	default:
		t.Skipf("no kernel media confinement on %s; the seam refuses it (internal/ffmpeg TestSpawnSeamRefusesUnconfinableHosts)", runtime.GOOS)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		linkOrCopy(t, self, filepath.Join(bin, name))
	}
	t.Setenv("PATH", bin)
	f := confineFixture{inputDir: t.TempDir(), outDir: t.TempDir(), outsideDir: t.TempDir()}
	f.input = filepath.Join(f.inputDir, "in.mkv")
	f.secret = filepath.Join(f.outsideDir, "secret.txt")
	for path, body := range map[string]string{f.input: "media", f.secret: "secret"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f confineFixture) spec(readWrite bool, sleeper bool) confineProbeSpec {
	s := confineProbeSpec{Secret: f.secret, Input: f.input, ReadOnlyDir: f.inputDir, OutsideDir: f.outsideDir, Sleeper: sleeper}
	if readWrite {
		s.ReadWriteDir = f.outDir
	}
	return s
}

func setProbeSpec(t *testing.T, spec confineProbeSpec) {
	t.Helper()
	raw, err := json.Marshal(spec) // #nosec G117 -- Secret is a test fixture path the probe must fail to read
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(confineProbeEnv, string(raw))
}

func linkOrCopy(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.Link(src, dst); err == nil {
		return
	}
	in, err := os.Open(src) // #nosec G304 -- this test binary
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o700) // #nosec G302 G304 -- test tool must be executable
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// confinedRunner is a Runner hardened exactly as the server hardens it.
func confinedRunner(t *testing.T) *ffmpeg.Runner {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := ffmpeg.New()
	r.SetHardeningShim(self)
	return r
}

// parseConfineReport finds the hostile tool's report in captured output, or
// fails with the output when the tool never ran.
func parseConfineReport(t *testing.T, output string) confineProbeReport {
	t.Helper()
	_, rest, ok := strings.Cut(output, confineReportPrefix)
	if !ok {
		t.Fatalf("hostile tool never reported; output:\n%s", output)
	}
	line, _, _ := strings.Cut(rest, "\n")
	var rep confineProbeReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &rep); err != nil {
		t.Fatalf("bad report %q: %v", line, err)
	}
	return rep
}

func assertConfined(t *testing.T, f confineFixture, rep confineProbeReport, readWrite bool) {
	t.Helper()
	if rep.InputRead != "ok" {
		t.Errorf("control: the granted input was not readable (%s); the probe proves nothing", rep.InputRead)
	}
	if rep.SecretRead == "ok" {
		t.Errorf("confinement breach: read %s outside every grant", f.secret)
	}
	if rep.ReadOnlyWrite == "ok" {
		t.Errorf("confinement breach: wrote into the read-only grant %s", f.inputDir)
	}
	if rep.OutsideWrite == "ok" {
		t.Errorf("confinement breach: wrote into %s outside every grant", f.outsideDir)
	}
	for _, dir := range []string{f.inputDir, f.outsideDir} {
		if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); err == nil {
			t.Errorf("confinement breach: escaped.txt exists in %s", dir)
		}
	}
	if readWrite && rep.ReadWriteWrite != "ok" {
		t.Errorf("control: the read-write grant was not writable (%s)", rep.ReadWriteWrite)
	}
	if !readWrite && rep.ReadWriteWrite != "" {
		t.Errorf("unexpected read-write attempt: %s", rep.ReadWriteWrite)
	}
}

// requireMandatoryConfinement asserts mandatory kernel confinement for encrypted
// pipe runs: on a host this platform cannot enforce mandatory confinement on, the
// spawn must fail with the platform's mandatory confinement refusal before the
// hostile tool runs.
func requireMandatoryConfinement(t *testing.T, err error) {
	t.Helper()
	if hostCanConfineMedia() {
		return
	}
	msg := err.Error()
	if strings.Contains(msg, confineReportPrefix) {
		t.Fatalf("host cannot apply mandatory pipe confinement, yet the hostile tool ran:\n%s", msg)
	}
	switch runtime.GOOS {
	case "darwin":
		if !errors.Is(err, ffmpeg.ErrPipeConfinementUnavailable) {
			t.Fatalf("pipe spawn failed for non-confinement reason on darwin: %v", err)
		}
	case "linux":
		if !strings.Contains(msg, "landlock confinement is required") {
			t.Fatalf("pipe spawn failed for non-confinement reason on linux: %v", err)
		}
	}
	t.Skipf("host cannot apply mandatory pipe confinement; the spawn was refused as required: %s", msg)
}

func assertPipeConfined(t *testing.T, f confineFixture, rep confineProbeReport) {
	t.Helper()
	if rep.SecretRead == "ok" {
		t.Errorf("pipe confinement breach: read %s outside every grant", f.secret)
	}
	if rep.ReadOnlyWrite == "ok" {
		t.Errorf("pipe confinement breach: wrote into read-only path %s", f.inputDir)
	}
	if rep.OutsideWrite == "ok" {
		t.Errorf("pipe confinement breach: wrote into %s outside every grant", f.outsideDir)
	}
	for _, dir := range []string{f.inputDir, f.outsideDir} {
		if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); err == nil {
			t.Errorf("pipe confinement breach: escaped.txt exists in %s", dir)
		}
	}
}

// TestConfinedProbeCannotEscapeItsReadOnlyGrant: an ffprobe run (read-only
// confinement to the input's directory) can read its input and nothing else,
// and can write nowhere on hosts with filesystem confinement; on non-Landlock
// Linux kernels ordinary probes run best-effort with rlimits only, while Darwin
// without sandbox-exec refuses launch.
func TestConfinedProbeCannotEscapeItsReadOnlyGrant(t *testing.T) {
	f := newConfineFixture(t)
	setProbeSpec(t, f.spec(false, false))
	_, err := confinedRunner(t).Probe(context.Background(), f.input)
	if err == nil {
		t.Fatal("Probe succeeded; the hostile ffprobe always exits non-zero")
	}
	if !hostHasFilesystemConfinement() {
		if runtime.GOOS != "linux" {
			if strings.Contains(err.Error(), confineReportPrefix) || !errors.Is(err, ffmpeg.ErrConfinementUnavailable) {
				t.Fatalf("expected ErrConfinementUnavailable without running child on %s, got: %v", runtime.GOOS, err)
			}
			t.Skipf("host lacks mandatory confinement (%s); launch refused cleanly: %v", runtime.GOOS, err)
		}
		rep := parseConfineReport(t, err.Error())
		if rep.InputRead != "ok" {
			t.Errorf("control: the granted input was not readable (%s)", rep.InputRead)
		}
		return
	}
	rep := parseConfineReport(t, err.Error())
	assertConfined(t, f, rep, false)
}

// TestConfinedHLSCannotEscapeAndLeavesNoDescendants: a live HLS encode
// (input read-only, session directory read-write) is confined to those grants
// where supported, falls back to rlimits on kernels without Landlock, and
// cancelling it always kills the whole process tree including forked grandchildren.
func TestConfinedHLSCannotEscapeAndLeavesNoDescendants(t *testing.T) {
	f := newConfineFixture(t)
	setProbeSpec(t, f.spec(true, true))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proc, err := confinedRunner(t).StartHLS(ctx, f.input, f.outDir, ffmpeg.Rendition{AudioOnly: true})
	if err != nil {
		if !hostHasFilesystemConfinement() && runtime.GOOS != "linux" && errors.Is(err, ffmpeg.ErrConfinementUnavailable) {
			t.Skipf("host lacks mandatory confinement (%s); launch refused cleanly: %v", runtime.GOOS, err)
		}
		t.Fatalf("StartHLS: %v", err)
	}
	pidPath := filepath.Join(f.outDir, "grandchild.pid")
	var grandchild int
	for deadline := time.Now().Add(20 * time.Second); grandchild == 0; {
		if raw, err := os.ReadFile(pidPath); err == nil && len(raw) > 0 { // #nosec G304 -- test-owned path
			grandchild, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
			continue
		}
		if !proc.Running() {
			_ = proc.Wait()
			t.Fatalf("HLS process exited before forking its grandchild; stderr:\n%s", proc.StderrTail())
		}
		if time.Now().After(deadline) {
			t.Fatalf("no grandchild pid after 20s; stderr:\n%s", proc.StderrTail())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	done := make(chan struct{})
	go func() { _ = proc.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("cancelled HLS process did not exit")
	}
	for deadline := time.Now().Add(5 * time.Second); !processGone(grandchild); {
		if time.Now().After(deadline) {
			_ = syscall.Kill(grandchild, syscall.SIGKILL)
			t.Fatalf("grandchild %d survived cancellation of the confined HLS process", grandchild)
		}
		time.Sleep(20 * time.Millisecond)
	}
	rep := parseConfineReport(t, proc.StderrTail())
	if hostHasFilesystemConfinement() {
		assertConfined(t, f, rep, true)
	}
}

// TestConfinedEncryptedPipeRequiresMandatoryConfinement checks that
// encrypted-library pipe runs enforce mandatory confinement (including Landlock
// ABI >= 3 on Linux) and refuse before launching ffprobe when unsupported.
func TestConfinedEncryptedPipeRequiresMandatoryConfinement(t *testing.T) {
	f := newConfineFixture(t)
	setProbeSpec(t, f.spec(false, false))
	_, err := confinedRunner(t).ProbePipeDuration(context.Background(), ffmpeg.PipeInput{
		URL: "http://127.0.0.1:4321/stream",
	})
	if err == nil {
		t.Fatal("ProbePipeDuration succeeded; the hostile ffprobe always exits non-zero")
	}
	requireMandatoryConfinement(t, err)
	assertPipeConfined(t, f, parseConfineReport(t, err.Error()))
}

// processGone reports whether pid has exited (a zombie counts: it was killed
// and only waits for an init that may not reap in a container).
func processGone(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return true
	}
	status, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status") // #nosec G304 -- test-owned pid
	return err == nil && strings.Contains(string(status), "\nState:\tZ")
}

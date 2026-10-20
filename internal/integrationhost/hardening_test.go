package integrationhost

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestDefaultWorkerRlimitsAreSane guards the rlimit ceilings against an
// accidental zeroing (a zero ceiling would make a worker unconstrained-to-broken)
// and pins the documented defaults so a change is deliberate (mirrors
// internal/ffmpeg.TestDefaultRlimitsAreSane — ADR-0058 §4).
func TestDefaultWorkerRlimitsAreSane(t *testing.T) {
	lim := DefaultWorkerRlimits()
	if lim.CPUSeconds == 0 || lim.ASBytes == 0 || lim.NoFile == 0 {
		t.Fatalf("a zero rlimit ceiling would break or unbound the worker: %+v", lim)
	}
	if lim.CPUSeconds != 30*60 {
		t.Errorf("RLIMIT_CPU default changed: got %d, want %d", lim.CPUSeconds, 30*60)
	}
	if lim.ASBytes != 1<<30 {
		t.Errorf("RLIMIT_AS default changed: got %d, want %d", lim.ASBytes, 1<<30)
	}
	if lim.NoFile != 256 {
		t.Errorf("RLIMIT_NOFILE default changed: got %d, want 256", lim.NoFile)
	}
}

// TestWorkerCommandWrapsWithShim proves the spawn seam re-execs the worker-shim
// when a shimPath is configured: the built command runs the shim (not the
// artifact directly), and the artifact binary + args appear after the "--"
// terminator. Mirrors TestHardenedCommandWrapsWithShim in internal/ffmpeg.
func TestWorkerCommandWrapsWithShim(t *testing.T) {
	binDir := t.TempDir()
	shimPath := filepath.Join(binDir, "beamfall")
	artifactPath := filepath.Join(binDir, "worker")
	for _, path := range []string{shimPath, artifactPath} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := &Supervisor{shimPath: shimPath, sandboxPath: "/usr/bin/sandbox-exec"}
	cmd, err := s.workerCommand(
		artifactPath,
		[]string{"--host-api-socket", "/run/bf-worker-foo/host.sock"},
		nil,
		t.TempDir(),
	)
	if err != nil {
		t.Fatalf("workerCommand: %v", err)
	}
	if cmd.Path == shimPath {
		t.Fatal("worker command bypassed the mandatory OS sandbox")
	}
	chain := []string{shimPath, shimSubcommand, "--", artifactPath}
	if runtime.GOOS == "linux" {
		// bwrap re-homes both binaries inside the sandbox mount namespace, so
		// the chain names the in-sandbox paths and the binds map host to sandbox.
		const shimInSandbox, artifactInSandbox = "/run/beamfall-worker/shim", "/run/beamfall-worker/artifact"
		if !containsOrdered(cmd.Args, []string{"--ro-bind", shimPath, shimInSandbox, "--ro-bind", artifactPath, artifactInSandbox}) {
			t.Fatalf("workerCommand args omit the shim/artifact binds: %q", cmd.Args)
		}
		chain = []string{shimInSandbox, shimSubcommand, "--", artifactInSandbox}
	}
	if !containsOrdered(cmd.Args, chain) {
		t.Fatalf("workerCommand args omit shim/artifact chain: %q", cmd.Args)
	}
}

func containsOrdered(got, want []string) bool {
	index := 0
	for _, value := range got {
		if index < len(want) && value == want[index] {
			index++
		}
	}
	return index == len(want)
}

func TestWorkerCommandRejectsMissingShim(t *testing.T) {
	s := &Supervisor{}
	cmd, err := s.workerCommand(
		"/opt/plugins/worker",
		[]string{"--host-api-socket", "/run/bf-worker-foo/host.sock"},
		nil,
		t.TempDir(),
	)
	if cmd != nil || !errors.Is(err, ErrWorkerShimRequired) {
		t.Fatalf("workerCommand = (%v, %v), want nil, ErrWorkerShimRequired", cmd, err)
	}
}

func TestWorkerCommandRejectsMissingSandbox(t *testing.T) {
	s := &Supervisor{shimPath: "/opt/beamfall"}
	cmd, err := s.workerCommand("/opt/plugins/worker", nil, nil, t.TempDir())
	if cmd != nil || !errors.Is(err, ErrWorkerSandboxRequired) {
		t.Fatalf("workerCommand = (%v, %v), want nil, ErrWorkerSandboxRequired", cmd, err)
	}
}

func TestSupervisorRunRejectsMissingShimBeforeLaunch(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Integration Worker launch is released only on Linux and Darwin")
	}
	supervisor := NewSupervisor("missing-shim", Artifact{Path: "/missing/worker"}, nil, nil, nil)
	if err := supervisor.Run(t.Context()); !errors.Is(err, ErrWorkerShimRequired) {
		t.Fatalf("Run error = %v, want %v", err, ErrWorkerShimRequired)
	}
}

func TestSupervisorReverifiesObjectBeforeEachLaunch(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Integration Worker launch is released only on Linux and Darwin")
	}
	path := filepath.Join(t.TempDir(), "worker")
	if err := os.WriteFile(path, []byte("verified"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	supervisor := NewSupervisor("fixture", Artifact{Path: path, VerifiedSHA256: digest}, nil, nil, nil)
	supervisor.SetHardeningShim("/missing-but-not-reached")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("mutated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Run(t.Context()); !errors.Is(err, ErrVerification) {
		t.Fatalf("Run after object mutation = %v, want ErrVerification before launch", err)
	}
}

func TestWorkerOSBoundaryDeniesSecretAndRawNetwork(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Integration Worker launch is released only on Linux and Darwin")
	}
	sandboxPath := defaultWorkerSandboxPath()
	if sandboxPath == "" {
		t.Skip("host has no worker sandbox executable; production launch fails closed")
	}
	runtimeDir := t.TempDir()
	secretRoot := t.TempDir()
	secretPath := filepath.Join(secretRoot, "unrelated-secret")
	if err := os.WriteFile(secretPath, []byte("must-not-be-readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, err := sandboxedWorkerCommand(
		sandboxPath,
		fixtureWorkerPath,
		fixtureWorkerPath,
		[]string{"--isolation-secret", secretPath, "--isolation-network"},
		runtimeDir,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		if exit := new(exec.ExitError); errors.As(err, &exit) {
			t.Fatalf("sandboxed adversarial worker exited %d: %s", exit.ExitCode(), output)
		}
		t.Fatalf("sandboxed adversarial worker: %v: %s", err, output)
	}
}

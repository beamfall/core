//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestWorkerShimGuardianReadinessFailureReapsProcess(t *testing.T) {
	before := openGuardianTestFDCount(t)
	lifetimeR, lifetimeW, err := os.Pipe()
	if err != nil {
		t.Fatalf("lifetime pipe: %v", err)
	}

	cmd := exec.Command("/bin/sh", "-c", "exec /bin/sleep 30")
	err = startWorkerShimGuardianProcess(lifetimeR, cmd, 25*time.Millisecond)
	_ = lifetimeR.Close()
	_ = lifetimeW.Close()
	if err == nil {
		t.Fatal("guardian without a readiness signal unexpectedly succeeded")
	}
	if cmd.Process == nil || cmd.ProcessState == nil {
		t.Fatalf("failed guardian was not reaped: process=%v state=%v", cmd.Process, cmd.ProcessState)
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("failed guardian pid %d still exists: %v", cmd.Process.Pid, err)
	}
	assertGuardianTestFDCount(t, before)
}

func TestWorkerShimGuardianStartFailureLeavesNoProcess(t *testing.T) {
	before := openGuardianTestFDCount(t)
	lifetimeR, lifetimeW, err := os.Pipe()
	if err != nil {
		t.Fatalf("lifetime pipe: %v", err)
	}

	cmd := exec.Command("/definitely/missing/beamfall-guardian")
	startErr := startWorkerShimGuardianProcess(lifetimeR, cmd, time.Second)
	_ = lifetimeR.Close()
	_ = lifetimeW.Close()
	if startErr == nil {
		t.Fatal("missing guardian executable unexpectedly started")
	}
	if cmd.Process != nil {
		t.Fatalf("start failure left process %v", cmd.Process)
	}
	assertGuardianTestFDCount(t, before)
}

func TestWorkerShimRejectsMissingGuardianLifetime(t *testing.T) {
	err := runWorkerShimWithGuardian([]string{"/bin/true"}, 0)
	if err == nil {
		t.Fatal("worker shim accepted a missing guardian lifetime descriptor")
	}
}

func TestWorkerShimExecWaitsForGuardianReadiness(t *testing.T) {
	dir := t.TempDir()
	beamfall := filepath.Join(dir, "beamfall")
	// #nosec G204 -- go is fixed; output is this test's private temporary directory.
	build := exec.Command("go", "build", "-o", beamfall, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build beamfall: %v\n%s", err, out)
	}
	marker := filepath.Join(dir, "artifact-started")
	artifact := filepath.Join(dir, "worker")
	script := "#!/bin/sh\nprintf 'started\\n' > \"$1\"\n/bin/sleep 30\n"
	if err := os.WriteFile(artifact, []byte(script), 0o700); err != nil { //nolint:gosec // test artifact must be executable
		t.Fatalf("write artifact: %v", err)
	}

	lifetimeR, lifetimeW, err := os.Pipe()
	if err != nil {
		t.Fatalf("lifetime pipe: %v", err)
	}
	defer func() { _ = lifetimeW.Close() }()
	// #nosec G204 -- beamfall and artifact are built inside this test's private directory.
	cmd := exec.Command(beamfall,
		"worker-shim", "--guardian-fd", "3", "--", artifact, marker,
	)
	cmd.Env = []string{}
	cmd.ExtraFiles = []*os.File{lifetimeR}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = lifetimeR.Close()
		t.Fatalf("start worker shim: %v", err)
	}
	_ = lifetimeR.Close()
	t.Cleanup(func() {
		_ = lifetimeW.Close()
		if cmd.ProcessState == nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
		}
	})
	waitForGuardianTestPath(t, marker)

	// Artifact exec is downstream of the readiness byte. TERM immediately after
	// observing artifact start therefore cannot beat signal.Ignore in guardian.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatalf("TERM worker group: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(-cmd.Process.Pid, 0); err != nil {
		t.Fatalf("guardian did not pin the group after immediate TERM: %v", err)
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatalf("KILL worker group: %v", err)
	}
	_ = lifetimeW.Close()
	if err := cmd.Wait(); err == nil {
		t.Fatal("TERM/KILL test worker unexpectedly exited successfully")
	}
}

func waitForGuardianTestPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func openGuardianTestFDCount(t *testing.T) int {
	t.Helper()
	directory, err := os.Open("/dev/fd")
	if err != nil {
		t.Fatalf("open /dev/fd: %v", err)
	}
	defer func() { _ = directory.Close() }()
	names, err := directory.Readdirnames(-1)
	if err != nil {
		t.Fatalf("read /dev/fd: %v", err)
	}
	return len(names)
}

func assertGuardianTestFDCount(t *testing.T, want int) {
	t.Helper()
	if got := openGuardianTestFDCount(t); got != want {
		t.Fatalf("open fd count = %d, want %d after error cleanup", got, want)
	}
}

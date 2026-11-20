//go:build unix

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
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

// TestWorkerShimGuardianKillsMidForkChildren is the CORE-2716 regression for
// the guardian's final KILL: on macOS one group SIGKILL misses a member's child
// that is still being forked. Each worker ignores TERM, as plugin workers may,
// and forks without pause, so the guardian's KILL lands mid-fork.
func TestWorkerShimGuardianKillsMidForkChildren(t *testing.T) {
	if testing.Short() {
		t.Skip("builds beamfall and forks continuously")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("the mid-fork miss is an XNU behaviour; Linux restarts the fork")
	}
	dir := t.TempDir()
	beamfall := filepath.Join(dir, "beamfall")
	// #nosec G204 -- go is fixed; output is this test's private temporary directory.
	build := exec.Command("go", "build", "-o", beamfall, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build beamfall: %v\n%s", err, out)
	}
	artifact := filepath.Join(dir, "worker")
	// The worker holds the witness FIFO named by $1 open, so every process it
	// forks does too, and reports readiness once TERM is ignored. Each child is
	// killed at once and would end by itself after 3 s anyway, so only a child
	// forked while the guardian's KILL lands outlives the loop, and the test
	// never signals a group it may no longer own.
	script := "#!/bin/sh\ntrap '' TERM\nexec 9>\"$1\"\nprintf r >&9\nwhile :; do /bin/sleep 3 & kill -9 $!; done\n"
	if err := os.WriteFile(artifact, []byte(script), 0o700); err != nil { //nolint:gosec // test artifact must be executable
		t.Fatalf("write artifact: %v", err)
	}

	const workers = 16
	var survived atomic.Int64
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var outlived bool
			var err error
			// Under the shim's default 1 GiB RLIMIT_AS a Go process can abort at
			// start on Linux; that is not what this test measures, so it retries.
			for attempt := range 3 {
				witness := filepath.Join(dir, fmt.Sprintf("witness-%d-%d", i, attempt))
				outlived, err = runGuardedForkingWorker(beamfall, artifact, witness)
				if !errors.Is(err, errWorkerNotReady) {
					break
				}
				t.Log(err)
			}
			if err != nil {
				t.Error(err)
				return
			}
			if outlived {
				survived.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := survived.Load(); n > 0 {
		t.Fatalf("%d of %d guardian-killed worker groups kept a live member", n, workers)
	}
}

var errWorkerNotReady = errors.New("worker never became ready")

// runGuardedForkingWorker starts artifact under the worker shim and its
// guardian, waits until it forks, then drops the guardian's lifetime pipe and
// reaps the shim. It reports whether any worker process outlived the
// guardian's KILL: the witness FIFO reaches EOF only once every holder exits.
func runGuardedForkingWorker(beamfall, artifact, witness string) (outlived bool, err error) {
	if err := syscall.Mkfifo(witness, 0o600); err != nil {
		return false, fmt.Errorf("witness fifo: %w", err)
	}
	// Opening a FIFO blocks until the worker opens it, so it runs aside. The
	// reader then sees the readiness byte, and EOF once all writers are gone.
	ready := make(chan error, 1)
	eof := make(chan error, 1)
	defer joinWitnessReader(witness, eof)
	go func() {
		f, err := os.Open(witness) // #nosec G304 -- test-owned FIFO.
		if err != nil {
			ready <- err
			eof <- err
			return
		}
		defer f.Close()
		_, err = f.Read(make([]byte, 1))
		ready <- err
		if err == nil {
			_, err = io.Copy(io.Discard, f)
		}
		eof <- err
	}()

	lifetimeR, lifetimeW, err := os.Pipe()
	if err != nil {
		return false, fmt.Errorf("lifetime pipe: %w", err)
	}
	// #nosec G204 -- beamfall and artifact are built inside the test's private directory.
	cmd := exec.Command(beamfall, "worker-shim", "--guardian-fd", "3", "--", artifact, witness)
	cmd.Env = []string{}
	cmd.ExtraFiles = []*os.File{lifetimeR}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A file, not a buffer: with a buffer Wait would also wait for every
	// descendant holding stderr, so a survivor would be gone before the check.
	stderr, err := os.CreateTemp(filepath.Dir(witness), "stderr-*")
	if err != nil {
		_ = lifetimeR.Close()
		_ = lifetimeW.Close()
		return false, fmt.Errorf("stderr file: %w", err)
	}
	defer stderr.Close()
	cmd.Stderr = stderr
	err = cmd.Start()
	_ = lifetimeR.Close()
	if err != nil {
		_ = lifetimeW.Close()
		return false, fmt.Errorf("start worker shim: %w", err)
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-ready:
		if err != nil {
			_ = lifetimeW.Close()
			<-waitErr
			return false, fmt.Errorf("worker readiness: %w", err)
		}
	case err := <-waitErr:
		_ = lifetimeW.Close()
		return false, fmt.Errorf("%w: shim exited: %v: %s", errWorkerNotReady, err, readStderr(stderr))
	case <-time.After(10 * time.Second):
		_ = lifetimeW.Close()
		err := <-waitErr
		return false, fmt.Errorf("worker never became ready: %v: %s", err, readStderr(stderr))
	}
	time.Sleep(20 * time.Millisecond)
	_ = lifetimeW.Close()
	select {
	case err := <-waitErr:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return false, fmt.Errorf("worker shim exit = %v, want SIGKILL", err)
		}
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
			return false, fmt.Errorf("worker shim exit = %v, want SIGKILL", err)
		}
	case <-time.After(10 * time.Second):
		// Only the leader: os.Process never signals a reaped pid, and its
		// children end by themselves.
		_ = cmd.Process.Kill()
		<-waitErr
		return false, fmt.Errorf("guardian did not kill worker group %d", cmd.Process.Pid)
	}
	select {
	case err := <-eof:
		eof <- err // for joinWitnessReader
		if err != nil {
			return false, fmt.Errorf("witness read: %w", err)
		}
		return false, nil
	case <-time.After(time.Second):
		return true, nil
	}
}

// joinWitnessReader waits for the reader goroutine of runGuardedForkingWorker.
// If no worker ever opened the FIFO, the reader is still blocked in open, so
// it opens the write end once to release it.
func joinWitnessReader(witness string, eof <-chan error) {
	for {
		select {
		case <-eof:
			return
		case <-time.After(time.Millisecond):
		}
		if f, err := os.OpenFile(witness, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil { // #nosec G304 -- test-owned FIFO.
			_ = f.Close()
		}
	}
}

func readStderr(f *os.File) string {
	b, _ := os.ReadFile(f.Name())
	return string(b)
}

package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestHLSProcessLaunchPreservesArgumentBytes(t *testing.T) {
	arg := "input-\xff"
	cmd := exec.CommandContext(t.Context(), "sh", "-c", `printf %s "$1"`, "sh", arg) // #nosec G204 -- fixed test command
	var output bytes.Buffer
	cmd.Stdout = &output
	p := startHLSPauseTestProcess(t, cmd)
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != arg {
		t.Fatalf("encoder argument = %q, want %q", got, arg)
	}
}

func TestHLSProcessStopReapsRunningForkers(t *testing.T) {
	for range 8 {
		firstPID := filepath.Join(t.TempDir(), "forking.pid")
		cmd := exec.CommandContext(t.Context(), "sh", "-c", `
			sleep 30 & echo $! > "$1"
			while :; do
				true & wait "$!"
			done
		`, "sh", firstPID) // #nosec G204 -- fixed fixture with a test-owned path
		p := startHLSPauseTestProcess(t, cmd)
		pid := waitForPidFile(t, firstPID)
		p.Stop()
		if err := unix.Kill(-hlsEncoderPID(p), 0); err != unix.ESRCH {
			t.Fatalf("forking encoder group survived Stop: %v", err)
		}
		if err := unix.Kill(pid, 0); err != unix.ESRCH {
			t.Fatalf("known descendant %d survived Stop: %v", pid, err)
		}
	}
}

func TestHLSProcessReapsDescendantAfterStopBound(t *testing.T) {
	// An exit-event stop holds the child in the kernel after SIGKILL, like a
	// delayed syscall exit, until the tracer releases it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pidFile := filepath.Join(t.TempDir(), "delayed.pid")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", `sleep 30 & echo $! > "$1"; wait`, "sh", pidFile) // #nosec G204 -- fixed fixture with a test-owned path
	p := startHLSPauseTestProcess(t, cmd)
	pid := waitForPidFile(t, pidFile)
	if err := unix.PtraceAttach(pid); err != nil {
		t.Fatalf("attach to the delayed descendant: %v", err)
	}
	defer func() {
		_ = unix.PtraceDetach(pid)
	}()
	if status := waitForTraceEvent(t, pid); status.StopSignal() != unix.SIGSTOP {
		t.Fatalf("attach stop status = %v", status)
	}
	if err := unix.PtraceSetOptions(pid, unix.PTRACE_O_TRACEEXIT); err != nil {
		t.Fatal(err)
	}
	if err := unix.PtraceCont(pid, 0); err != nil {
		t.Fatal(err)
	}
	cancel()
	p.Stop()
	status := waitForTraceEvent(t, pid)
	if status.TrapCause() != unix.PTRACE_EVENT_EXIT {
		t.Fatalf("SIGKILL did not trigger the delayed exit: %v", status)
	}
	if unix.Kill(hlsEncoderPID(p), 0) != unix.ESRCH || !processAlive(cmd.Process.Pid) {
		t.Fatal("expected the encoder leader reaped and its supervisor still running")
	}
	select {
	case <-p.reaper.done:
		t.Fatal("supervisor returned while the descendant was still exiting")
	default:
	}

	if err := unix.PtraceDetach(pid); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.reaper.done:
	case <-time.After(hlsPauseTestBound):
		t.Fatal("supervisor did not finish after the delayed exit")
	}
	if err := unix.Kill(pid, 0); err != unix.ESRCH {
		t.Fatalf("delayed descendant %d was not collected: %v", pid, err)
	}
}

func waitForTraceEvent(t *testing.T, pid int) unix.WaitStatus {
	t.Helper()
	var status unix.WaitStatus
	for deadline := time.Now().Add(hlsPauseTestBound); time.Now().Before(deadline); {
		got, err := unix.Wait4(pid, &status, unix.WNOHANG|unix.WALL, nil)
		if got == pid {
			return status
		}
		if err != nil && err != unix.EINTR {
			t.Fatalf("wait for traced child %d: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no trace event for child %d", pid)
	return 0
}

const nonHLSProbeHelper = "BEAMFALL_NON_HLS_PROBE_HELPER"

func TestHLSReapingLeavesNonHLSDescendantsToSystemReaper(t *testing.T) {
	// Model a reaping PID 1 outside the process running HLS and ordinary
	// probes. Keep that process alive until we have collected the orphan.
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 0, 0, 0, 0); err != nil {
			t.Error(err)
		}
	})

	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	defer reader.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 4*hlsPauseTestBound)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestHLSNonHLSProbeHelper$") // #nosec G204 -- this test binary
	cmd.Env = append(os.Environ(), nonHLSProbeHelper+"=1")
	cmd.Dir = dir
	cmd.Stdin = reader
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = writer.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("probe helper: %v\n%s", err, output.String())
		}
	}()

	pid := waitForPidFile(t, filepath.Join(dir, "cancelled.pid"))
	var status unix.WaitStatus
	for deadline := time.Now().Add(hlsPauseTestBound); time.Now().Before(deadline); {
		got, err := unix.Wait4(pid, &status, unix.WNOHANG, nil)
		if got == pid && err == nil && status.Signal() == unix.SIGKILL {
			return
		}
		if got != 0 || err != nil {
			t.Fatalf("system reaper wait for cancelled non-HLS descendant %d = %d, %v, %v", pid, got, status, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("cancelled non-HLS descendant %d did not exit", pid)
}

func TestHLSNonHLSProbeHelper(t *testing.T) {
	if os.Getenv(nonHLSProbeHelper) != "1" {
		return
	}
	startHLSPauseTestProcess(t, exec.CommandContext(t.Context(), "sleep", "30"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", `sleep 30 & echo $! > probe.pid; wait`)
	done := make(chan error, 1)
	go func() { done <- supervise(ctx, cmd, io.Discard, io.Discard) }()
	pid := waitForPidFile(t, "probe.pid")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ordinary probe: %v", err)
	}
	if err := os.WriteFile("cancelled.pid", []byte(strconv.Itoa(pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

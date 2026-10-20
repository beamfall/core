//go:build unix

package ffmpeg

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestHLSProcessConcurrentCancelStopWaitReapsGroup(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "descendant.pid")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 30 & echo $! > \"$1\"; wait", "sh", pidPath) // #nosec G204 -- test-owned path
	process, err := startHLSProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	// The shell's `>` redirection creates descendant.pid before `echo` writes to
	// it, so a nil error alone does not mean the pid is published: an empty read
	// satisfied this poll and then failed Atoi below. It passed in isolation and
	// only lost the race under concurrent lanes, which is exactly the shape that
	// reaches a merge-train gate rather than a developer's run.
	var raw []byte
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		raw, err = os.ReadFile(pidPath) // #nosec G304 -- pidPath is confined to this test's t.TempDir.
		if err == nil && len(strings.TrimSpace(string(raw))) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		process.Stop()
		t.Fatalf("descendant pid not published: %v", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		process.Stop()
		t.Fatal("descendant pid file never became non-empty within the deadline")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		process.Stop()
		t.Fatal(err)
	}

	cancel()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = process.Wait() }()
	}
	process.Stop()
	wg.Wait()

	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant process %d survived HLS cancellation", pid)
}

func TestHLSProcessNaturalLeaderExitReapsDescendant(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "natural-descendant.pid")
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "sleep 30 & echo $! > \"$1\"; exit 0", "sh", pidPath) // #nosec G204 -- test-owned path
	process, err := startHLSProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("natural leader exit: %v", err)
	}
	raw, err := os.ReadFile(pidPath) // #nosec G304 -- pidPath is confined to this test's t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant process %d survived natural HLS leader exit", pid)
}

// TestBoundedBufferCapsAndSignalsOnce proves the capture buffer never retains
// more than its limit and fires its overflow signal exactly once, however many
// over-limit writes arrive.
func TestBoundedBufferCapsAndSignalsOnce(t *testing.T) {
	fired := make(chan struct{}, 4) // onOverflow runs async (go trigger); observe via a channel
	b := &boundedBuffer{limit: 8, onOverflow: func() { fired <- struct{}{} }}
	if _, err := b.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if b.overflowed() {
		t.Fatal("overflowed before exceeding the cap")
	}
	if _, err := b.Write([]byte("567890")); err != nil { // crosses the cap
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("more and more")); err != nil { // further overflow writes
		t.Fatal(err)
	}
	if !b.overflowed() {
		t.Fatal("did not mark overflow after exceeding the cap")
	}
	if got := len(b.bytes()); got != 8 {
		t.Fatalf("retained %d bytes, want the 8-byte cap", got)
	}
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("overflow never signalled")
	}
	select {
	case <-fired:
		t.Fatal("overflow signalled more than once")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestSuperviseReturnsOverflowAndKills proves a child that floods output past the
// capture cap is fail-closed: run returns ErrOutputOverflow and the process is
// killed (the call returns promptly rather than buffering /dev/zero forever).
func TestSuperviseReturnsOverflowAndKills(t *testing.T) {
	r := New()
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, err := r.run(ctx, exec.CommandContext(ctx, "sh", "-c", "cat /dev/zero"))
	if !errors.Is(err, ErrOutputOverflow) {
		t.Fatalf("err = %v, want ErrOutputOverflow", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("overflow kill took %s — the flood was buffered, not fail-closed", elapsed)
	}
}

// TestSuperviseStartupFailure proves a spawn that cannot start returns a wrapped
// start error rather than hanging or panicking.
func TestSuperviseStartupFailure(t *testing.T) {
	r := New()
	_, _, err := r.run(context.Background(), exec.Command("/nonexistent/beamfall-ffmpeg-binary"))
	if err == nil || !strings.Contains(err.Error(), "start failed") {
		t.Fatalf("err = %v, want a wrapped start failure", err)
	}
}

// TestSuperviseCancelReapsPromptly proves context cancellation kills and reaps a
// long-running child promptly (no orphan, no hang waiting for the sleep).
func TestSuperviseCancelReapsPromptly(t *testing.T) {
	r := New()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	_, _, err := r.run(ctx, exec.CommandContext(ctx, "sh", "-c", "sleep 30"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancel reap took %s — the child was not killed", elapsed)
	}
}

// TestSuperviseTimeoutReaps proves a deadline kills and reaps the child, mapping
// to DeadlineExceeded.
func TestSuperviseTimeoutReaps(t *testing.T) {
	r := New()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := r.run(ctx, exec.CommandContext(ctx, "sh", "-c", "sleep 30"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout reap took %s", elapsed)
	}
}

// TestSuperviseNonzeroExitReturnsExitError proves a normal non-zero exit is
// surfaced as an *exec.ExitError (the signal DecodeVerify keys "broken" on),
// distinct from the supervisor's own cancellation/overflow errors.
func TestSuperviseNonzeroExitReturnsExitError(t *testing.T) {
	r := New()
	_, _, err := r.run(context.Background(), exec.Command("sh", "-c", "exit 3"))
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v, want *exec.ExitError", err)
	}
}

// TestSuperviseKillsProcessTreeNoOrphan proves the supervisor reaps the whole
// process tree on cancel: a child that backgrounds a grandchild leaves no
// survivor — the grandchild pid is dead after the supervised call returns.
func TestSuperviseKillsProcessTreeNoOrphan(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	// sh backgrounds a long sleep (the grandchild), records its pid, then waits —
	// so the whole group is alive until the supervisor kills it.
	script := "sleep 60 & echo $! > " + pidFile + "; wait"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := New()
	done := make(chan struct{})
	go func() {
		_, _, _ = r.run(ctx, exec.CommandContext(ctx, "sh", "-c", script)) // #nosec G204 -- test-controlled script, not user input
		close(done)
	}()

	grandchild := waitForPidFile(t, pidFile)
	cancel()
	<-done

	// Give the group kill a moment to propagate, then assert the grandchild is gone.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(grandchild) {
			return // reaped: no orphan survived
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Clean up if it somehow survived, then fail.
	_ = syscall.Kill(grandchild, syscall.SIGKILL)
	t.Fatalf("grandchild pid %d survived the supervised cancel — the process tree was not reaped", grandchild)
}

func waitForPidFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path) // #nosec G304 -- test reads a temp pid file it created
		if err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("grandchild pid file %s never appeared", path)
	return 0
}

// processAlive reports whether pid is still a live process (signal 0 probes
// existence without delivering a signal).
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// TestRunWithDiscardStillReaps confirms the discard-output path (DecodeVerify /
// hwaccel probe) is supervised: a cancelled sleep returns promptly via runWith.
func TestRunWithDiscardStillReaps(t *testing.T) {
	r := New()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := r.runWith(ctx, exec.CommandContext(ctx, "sh", "-c", "sleep 30"), io.Discard, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("runWith timeout reap took %s", elapsed)
	}
}

//go:build unix

package integrationhost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestConfinedChildRetiresDescendantsOnCancel proves the cancellation path:
// cancelling the context must return promptly AND leave no member of the
// child's process group running. Without the group retirement the leaked
// `sleep` keeps the output pipe open and the call never returns.
func TestConfinedChildRetiresDescendantsOnCancel(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "descendant.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// #nosec G204 -- fixed test script, no external input.
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 60 & echo $! > "+pidPath+"; wait")
	done := make(chan error, 1)
	go func() {
		_, err := runConfinedChild(ctx, cmd)
		done <- err
	}()

	pid := waitForDescendantPID(t, pidPath)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled run error = %v, want context.Canceled", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runConfinedChild did not return after cancellation")
	}
	assertProcessGone(t, pid)
}

// TestConfinedChildRetiresDescendantsOnNormalExit proves the success path
// reaps too: the direct child exits cleanly while a descendant it forked is
// still running, and that descendant must not outlive the call.
func TestConfinedChildRetiresDescendantsOnNormalExit(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "descendant.pid")
	// #nosec G204 -- fixed test script, no external input.
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c",
		"sleep 60 >/dev/null 2>&1 & echo $! > "+pidPath+"; echo done")

	out, err := runConfinedChild(context.Background(), cmd)
	if err != nil {
		t.Fatalf("runConfinedChild: %v", err)
	}
	if strings.TrimSpace(string(out)) != "done" {
		t.Fatalf("output = %q, want %q", out, "done")
	}
	pid := readDescendantPID(t, pidPath)
	assertProcessGone(t, pid)
}

func waitForDescendantPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		// #nosec G304 -- path is the pid file this test created under its own
		// t.TempDir(); no caller supplies it.
		if data, err := os.ReadFile(path); err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("descendant pid never appeared at %s", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readDescendantPID(t *testing.T, path string) int {
	t.Helper()
	// #nosec G304 -- path is the pid file this test created under its own
	// t.TempDir(); no caller supplies it.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("descendant %d survived process-group retirement", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

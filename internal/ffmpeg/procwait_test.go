//go:build linux || darwin

package ffmpeg

import (
	"os/exec"
	"testing"
	"time"
)

func TestWaitForProcessExitLeavesLeaderWaitable(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := waitForProcessExit(cmd.Process.Pid); err != nil {
		_ = cmd.Wait()
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("leader was reaped before process-group cleanup: %v", err)
	}
}

// TestWaitForProcessExitAcceptsAnAlreadyExitedLeader covers a leader that
// exits before the wait registers: it is a zombie by then, and the wait must
// report a clean exit rather than an error that reads as an encoder crash.
func TestWaitForProcessExitAcceptsAnAlreadyExitedLeader(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // let the leader exit and become a zombie
	if err := waitForProcessExit(cmd.Process.Pid); err != nil {
		_ = cmd.Wait()
		t.Fatalf("already-exited leader: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("leader exit status: %v", err)
	}
}

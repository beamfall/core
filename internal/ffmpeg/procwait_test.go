//go:build linux || darwin

package ffmpeg

import (
	"os/exec"
	"testing"
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

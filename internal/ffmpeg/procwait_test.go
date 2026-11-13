//go:build linux || darwin

package ffmpeg

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
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

// TestWaitForProcessExitOnAnExitedChild proves a child that exited before the
// wait began counts as exited, not as a failed wait: on darwin the kqueue
// NOTE_EXIT registration on such a child fails with ESRCH.
func TestWaitForProcessExitOnAnExitedChild(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait until the child is a zombie: exited, its status not yet reaped.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if exited, err := childExited(cmd.Process); err == nil && exited {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Wait()
			t.Fatal("child never exited")
		}
	}
	if err := waitForProcessExit(cmd.Process.Pid); err != nil {
		_ = cmd.Wait()
		t.Fatalf("wait on an exited child: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("exit status: %v", err)
	}
}

// childExited reports whether p has exited without reaping it.
func childExited(p *os.Process) (bool, error) {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(p.Pid)).Output() // #nosec G204 -- fixed ps argv; the pid is our own child
	if err != nil {
		return false, err
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Z"), nil
}

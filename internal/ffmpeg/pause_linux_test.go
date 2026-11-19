package ffmpeg

import (
	"bytes"
	"os"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

func becomeHLSStopTestSubreaper(t *testing.T) {
	t.Helper()
	// Only the disposable helper adopts orphans. The package test process
	// keeps its normal child-reaping behavior.
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Fatalf("set test subreaper: %v", err)
	}
}

func reapHLSStopTestDescendant(t *testing.T, pid int) bool {
	t.Helper()
	var status unix.WaitStatus
	// Wait for this adopted child only. Reaping any child could consume the
	// leader's wait status before exec.Cmd.Wait does.
	reaped, err := unix.Wait4(pid, &status, unix.WNOHANG, nil)
	if err == unix.EINTR {
		return false
	}
	if err != nil {
		t.Fatalf("reap adopted descendant %d: %v", pid, err)
	}
	if reaped == pid && status.Signal() != unix.SIGKILL {
		t.Errorf("Stop ended the descendant with %v, want SIGKILL", status)
	}
	return reaped == pid
}

func hlsProcessStopped(pid int) (bool, error) {
	status, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status") // #nosec G304 -- test-owned PID
	return bytes.Contains(status, []byte("\nState:\tT ")), err
}

//go:build linux || darwin

package ffmpeg

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const hlsPauseTestBound = 3 * time.Second

func TestHLSProcessPauseResumeKeepsPID(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	var output bytes.Buffer
	cmd := exec.CommandContext(t.Context(), "sh", "-c", `read -r line && printf '%s:%s\n' "$$" "$line"`)
	cmd.Stdin = reader
	cmd.Stdout = &output
	process := startHLSPauseTestProcess(t, cmd)
	pid := cmd.Process.Pid
	if !process.Pause() {
		t.Fatal("Pause refused a running process")
	}
	waitForStoppedHLSProcess(t, pid)
	if _, err := io.WriteString(writer, "continued\n"); err != nil {
		t.Fatal(err)
	}
	if !process.Resume() {
		t.Fatal("Resume refused a paused process")
	}
	select {
	case err := <-waitForHLSPauseTestExit(process):
		if err != nil {
			t.Fatalf("resumed process failed: %v", err)
		}
	case <-time.After(hlsPauseTestBound):
		t.Fatal("resumed process did not complete")
	}
	if want := strconv.Itoa(pid) + ":continued\n"; output.String() != want {
		t.Fatalf("output after resume = %q, want %q", output.String(), want)
	}
}

func TestHLSProcessStopReapsStoppedLeaderAndDescendant(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "descendant.pid")
	cmd := exec.CommandContext(t.Context(), "sh", "-c", `sleep 30 & echo $! > "$1"; wait`, "sh", pidPath) // #nosec G204 -- test-owned path
	process := startHLSPauseTestProcess(t, cmd)
	descendant := waitForPidFile(t, pidPath)
	if !process.Pause() {
		t.Fatal("Pause refused a running group")
	}
	leader := cmd.Process.Pid
	waitForStoppedHLSProcess(t, leader)
	waitForStoppedHLSProcess(t, descendant)

	deadline := time.Now().Add(hlsPauseTestBound)
	stopped := make(chan struct{})
	go func() {
		process.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Until(deadline)):
		t.Fatal("Stop did not finish within the reap bound")
	}
	if cmd.ProcessState == nil {
		t.Fatal("Stop returned before reaping the leader")
	}
	if signal := cmd.ProcessState.Sys().(syscall.WaitStatus).Signal(); signal != syscall.SIGKILL {
		t.Errorf("Stop ended the leader with %s, want SIGKILL", signal)
	}
	for {
		// Signal zero also sees a zombie. Require ESRCH for both PIDs, so the
		// test covers adoption and reaping when run under the container init.
		leaderErr := syscall.Kill(leader, 0)
		descendantErr := syscall.Kill(descendant, 0)
		if leaderErr == syscall.ESRCH && descendantErr == syscall.ESRCH {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("group not reaped within %s: leader %d: %v, descendant %d: %v",
				hlsPauseTestBound, leader, leaderErr, descendant, descendantErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHLSProcessPauseRacesNaturalExit(t *testing.T) {
	for range 16 {
		cmd := exec.CommandContext(t.Context(), "sh", "-c", "exit 0")
		process := startHLSPauseTestProcess(t, cmd)
		if err := raceHLSPauseWithExit(t, process); err != nil {
			t.Fatalf("natural exit failed: %v", err)
		}
		if processAlive(cmd.Process.Pid) {
			t.Fatalf("leader %d survived Wait", cmd.Process.Pid)
		}
		assertHLSPauseRefused(t, process)
	}
}

func TestHLSProcessPauseRefusedAfterStop(t *testing.T) {
	for _, tt := range []struct {
		name      string
		terminate func(*HLSProcess)
	}{
		{"Stop", (*HLSProcess).Stop},
		{"Kill before Wait", (*HLSProcess).Kill},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "sleep", "30")
			process := startHLSPauseTestProcess(t, cmd)
			if !process.Pause() {
				t.Fatal("Pause refused a running process")
			}
			tt.terminate(process)
			assertHLSPauseRefused(t, process)
		})
	}
}

func startHLSPauseTestProcess(t *testing.T, cmd *exec.Cmd) *HLSProcess {
	t.Helper()
	process, err := startHLSProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(process.Stop)
	return process
}

func waitForStoppedHLSProcess(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(hlsPauseTestBound); time.Now().Before(deadline); {
		stopped, err := hlsProcessStopped(t, pid)
		if err != nil {
			t.Fatalf("query process %d state: %v", pid, err)
		}
		if stopped {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d did not stop", pid)
}

func hlsProcessStopped(t *testing.T, pid int) (bool, error) {
	pidArg := strconv.Itoa(pid)
	if runtime.GOOS == "linux" {
		// The slim runtime image does not install ps.
		status, err := os.ReadFile("/proc/" + pidArg + "/status") // #nosec G304 -- test-owned PID
		return bytes.Contains(status, []byte("\nState:\tT ")), err
	}
	state, err := exec.CommandContext(t.Context(), "ps", "-o", "stat=", "-p", pidArg).Output() // #nosec G204 -- test-owned PID
	return strings.HasPrefix(strings.TrimSpace(string(state)), "T"), err
}

func waitForHLSPauseTestExit(process *HLSProcess) <-chan error {
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	return done
}

func raceHLSPauseWithExit(t *testing.T, process *HLSProcess) error {
	t.Helper()
	done := waitForHLSPauseTestExit(process)
	deadline := time.NewTimer(hlsPauseTestBound)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		process.Pause()
		process.Resume()
		select {
		case err := <-done:
			return err
		case <-deadline.C:
			t.Fatal("Pause and Resume blocked natural exit")
		case <-ticker.C:
		}
	}
}

func assertHLSPauseRefused(t *testing.T, process *HLSProcess) {
	t.Helper()
	if process.Pause() {
		t.Error("Pause succeeded during or after teardown")
	}
	if process.Resume() {
		t.Error("Resume succeeded during or after teardown")
	}
}

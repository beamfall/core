//go:build linux || darwin

package ffmpeg

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

const hlsPauseTestBound = 3 * time.Second
const hlsStopHelperEnv = "BEAMFALL_FFMPEG_HLS_STOP_HELPER"

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
	dir := t.TempDir()
	// Allow time for fixture setup and race-detector shutdown around the reap
	// bound. The parent can still kill and Wait for a wedged helper.
	ctx, cancel := context.WithTimeout(t.Context(), 4*hlsPauseTestBound)
	defer cancel()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestHLSProcessStopHelper$") // #nosec G204 -- this test binary
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), hlsStopHelperEnv+"=1")
	// The watchdog targets only its own child. A private session lets the
	// kernel send SIGHUP and SIGCONT if the stopped fixture group is orphaned.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.WaitDelay = time.Second
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bounded Stop helper: %v (watchdog: %v)\n%s", err, ctx.Err(), output)
	}
}

func TestHLSProcessStopHelper(t *testing.T) {
	if os.Getenv(hlsStopHelperEnv) != "1" {
		return
	}
	becomeHLSStopTestSubreaper(t)
	// The fixture can also exit on EOF if the helper dies before pausing it.
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	const descendantFile = "descendant.pid"
	cmd := exec.CommandContext(context.Background(), "sh", "-c", `read -r line <&3 & echo $! > "$1"; wait`, "sh", descendantFile)
	cmd.ExtraFiles = []*os.File{reader}
	process, err := startHLSProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	leader := cmd.Process.Pid
	descendant := waitForPidFile(t, descendantFile)
	if !process.Pause() {
		t.Fatal("Pause refused a running group")
	}
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
	if !time.Now().Before(deadline) {
		t.Fatal("Stop did not finish within the reap bound")
	}
	if cmd.ProcessState == nil {
		t.Fatal("Stop returned before reaping the leader")
	}
	signal := cmd.ProcessState.Sys().(syscall.WaitStatus).Signal()
	descendantReaped := false
	for {
		if !descendantReaped {
			descendantReaped = reapHLSStopTestDescendant(t, descendant)
		}
		// Signal zero also sees zombies. Require both PIDs to disappear.
		// CORE-2650 tracks production reaping of adopted descendants when Beamfall runs as PID 1.
		leaderErr := syscall.Kill(leader, 0)
		descendantErr := syscall.Kill(descendant, 0)
		if leaderErr == syscall.ESRCH && descendantErr == syscall.ESRCH {
			if !time.Now().Before(deadline) {
				t.Fatal("group reaped after the reap bound")
			}
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("group not reaped within %s: leader %d: %v, descendant %d: %v",
				hlsPauseTestBound, leader, leaderErr, descendant, descendantErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if signal != syscall.SIGKILL {
		t.Errorf("Stop ended the leader with %s, want SIGKILL", signal)
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
		stopped, err := hlsProcessStopped(pid)
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

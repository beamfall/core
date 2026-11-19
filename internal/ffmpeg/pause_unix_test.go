//go:build linux || darwin

package ffmpeg

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

const hlsPauseTestBound = 3 * time.Second
const hlsReapTestBound = time.Second
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
	// The fixture can also exit on EOF if the helper dies before pausing it.
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	const descendantFile = "descendant.pid"
	// The descendant inherits ignored SIGHUP before its PID is published.
	// Keep the pipe writer open through the assertion so orphaning the group
	// cannot end the descendant via SIGHUP or EOF.
	cmd := exec.CommandContext(context.Background(), "sh", "-c", `trap '' HUP; read -r line <&3 & echo $! > "$1"; wait`, "sh", descendantFile)
	cmd.ExtraFiles = []*os.File{reader}
	// Let the descendant assertion run even if only the leader is killed.
	cmd.Stderr = os.Stderr
	process, err := startHLSProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	// The helper can fail on the deadline even if Stop is stuck in Wait.
	// Its private session provides cleanup if it exits with a stopped group.
	defer process.Kill()
	leader := cmd.Process.Pid
	descendant := waitForPidFile(t, descendantFile)
	if !process.Pause() {
		t.Fatal("Pause refused a running group")
	}
	waitForStoppedHLSProcess(t, leader)
	waitForStoppedHLSProcess(t, descendant)

	ctx, cancel := context.WithTimeout(t.Context(), hlsReapTestBound)
	defer cancel()
	deadline, _ := ctx.Deadline()
	select {
	case <-stopHLSProcess(process):
		if !time.Now().Before(deadline) {
			t.Fatalf("Stop joined the leader after %s", hlsReapTestBound)
		}
	case <-ctx.Done():
		t.Fatalf("Stop did not join the leader within %s", hlsReapTestBound)
	}
	if cmd.ProcessState == nil {
		t.Fatal("Stop returned before joining the leader")
	}
	if signal := cmd.ProcessState.Sys().(syscall.WaitStatus).Signal(); signal != syscall.SIGKILL {
		t.Errorf("Stop ended the leader with %s, want SIGKILL", signal)
	}
	for time.Now().Before(deadline) {
		if hlsDescendantExited(descendant) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant %d did not exit within %s", descendant, hlsReapTestBound)
}

func TestHLSProcessStopKillsWithoutTermGrace(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "leader.pid")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	cmd := exec.CommandContext(t.Context(), "sh", "-c", `
		trap 'exit 23' TERM
		cat <&3 &
		echo $$ > "$1"
		wait
	`, "sh", pidFile) // #nosec G204 -- test-owned path
	cmd.ExtraFiles = []*os.File{reader}
	process := startHLSPauseTestProcess(t, cmd)
	if pid := waitForPidFile(t, pidFile); pid != cmd.Process.Pid {
		t.Fatalf("TERM trap ready in process %d, want leader %d", pid, cmd.Process.Pid)
	}
	process.Stop()
	if cmd.ProcessState == nil {
		t.Fatal("Stop returned before joining the leader")
	}
	if signal := cmd.ProcessState.Sys().(syscall.WaitStatus).Signal(); signal != syscall.SIGKILL {
		t.Fatalf("Stop allowed the TERM trap to run: %s", cmd.ProcessState)
	}
}

func TestHLSProcessPauseRacesNaturalExit(t *testing.T) {
	for range 16 {
		cmd := exec.CommandContext(t.Context(), "sh", "-c", "exit 0")
		process := startHLSPauseTestProcess(t, cmd)
		if err := raceHLSPauseWithExit(t, process); err != nil {
			t.Fatalf("natural exit failed: %v", err)
		}
		if pid := cmd.Process.Pid; processAlive(pid) {
			t.Fatalf("leader %d survived Wait", pid)
		}
		assertHLSPauseRefused(t, process)
	}
}

func TestHLSProcessWaitObserverErrorLeavesEncoderRunning(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), hlsPauseTestBound)
	defer cancel()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	var output bytes.Buffer
	cmd := exec.CommandContext(ctx, "sh", "-c", `read -r line && printf '%s\n' "$line"`)
	cmd.Stdin = reader
	cmd.Stdout = &output
	process := startHLSPauseTestProcess(t, cmd)
	observerCalled := make(chan struct{})
	process.observeExit = func(int) error {
		close(observerCalled)
		return syscall.ENOSYS
	}
	done := waitForHLSPauseTestExit(process)
	select {
	case <-observerCalled:
	case <-ctx.Done():
		t.Fatal("Wait did not call the exit observer")
	}

	// Keep the encoder blocked on input until Wait has disabled group signaling.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for process.Resume() {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("Wait did not switch to leader-only signaling")
		}
	}
	if _, err := io.WriteString(writer, "continued\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Wait interrupted a healthy encoder after observer failure: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("encoder did not finish after Wait fell back")
	}
	if got := output.String(); got != "continued\n" {
		t.Fatalf("encoder output = %q, want continued", got)
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

type blockingHLSOutput struct {
	entered chan struct{}
	release chan struct{}
}

func (w blockingHLSOutput) Write(data []byte) (int, error) {
	close(w.entered)
	<-w.release
	return len(data), nil
}

func TestHLSProcessStopJoinsWait(t *testing.T) {
	output := blockingHLSOutput{entered: make(chan struct{}), release: make(chan struct{})}
	cmd := exec.CommandContext(t.Context(), "sh", "-c", "printf ready; exec sleep 30")
	cmd.Stdout = output
	process := startHLSPauseTestProcess(t, cmd)
	release := sync.OnceFunc(func() { close(output.release) })
	defer release()
	select {
	case <-output.entered:
	case <-time.After(hlsPauseTestBound):
		t.Fatal("encoder did not reach the blocked output writer")
	}
	stopped := stopHLSProcess(process)
	select {
	case <-stopped:
		t.Fatal("Stop returned while exec.Cmd.Wait was still blocked")
	case <-time.After(hlsReapTestBound + 200*time.Millisecond):
		// Hold the output writer past the former Stop timeout.
	}
	release()
	select {
	case <-stopped:
	case <-time.After(hlsPauseTestBound):
		t.Fatal("Stop did not join after the output writer was released")
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

func stopHLSProcess(process *HLSProcess) <-chan struct{} {
	stopped := make(chan struct{})
	go func() {
		process.Stop()
		close(stopped)
	}()
	return stopped
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

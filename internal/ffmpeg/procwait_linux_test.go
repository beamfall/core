//go:build linux && !android

package ffmpeg

import (
	"bytes"
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

// TestGroupListsLiveMemberSeesRunningNotZombie checks the Linux /proc scan:
// a running member is live, and the same member as an unreaped zombie is not.
func TestGroupListsLiveMemberSeesRunningNotZombie(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if !groupListsLiveMember(pid) {
		t.Fatal("a running member was not listed as live")
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if err := waitForProcessExit(pid); err != nil {
		t.Fatal(err)
	}
	if groupListsLiveMember(pid) {
		t.Fatal("a group holding only its zombie leader was listed as live")
	}
}

func TestParseProcStatTakesFieldsAfterTheLastParen(t *testing.T) {
	state, pgid, ok := parseProcStat([]byte("42 (a) b) (c) S 1 77 77 0"))
	if !ok || state != 'S' || pgid != 77 {
		t.Fatalf("parseProcStat = %c, %d, %v; want S, 77, true", state, pgid, ok)
	}
	if _, _, ok := parseProcStat([]byte("42 no-paren S 1 77")); ok {
		t.Fatal("a line without comm parsed")
	}
}

// TestSuperviseReportsAPipeHolderTheKillCannotEnd is the CORE-2821
// regression. A group member in uninterruptible I/O outlives the KILL and
// keeps the output pipe open; before the fix the Linux sweep was a no-op, so
// the drain waited on it forever and the reporter never heard. The lister
// stands in for that member, and a holder outside the group keeps the pipe
// open as it would.
func TestSuperviseReportsAPipeHolderTheKillCannotEnd(t *testing.T) {
	groupLister = func(int) bool { return true }
	var heard []error
	SetGroupSurvivorReporter(func(err error) { heard = append(heard, err) })
	t.Cleanup(func() {
		groupLister = groupListsLiveMember
		SetGroupSurvivorReporter(nil)
	})

	pidFile := filepath.Join(t.TempDir(), "holder.pid")
	// The holder writes its pid only once setsid has taken it out of the
	// group, and the leader exits only after that, so the group KILL never
	// reaches it.
	// #nosec G204 -- fixed shell and test-owned script.
	cmd := exec.Command("/bin/sh", "-c",
		`setsid /bin/sh -c 'echo $$ > "$1"; exec sleep 30' sh "$1" &
		while [ ! -s "$1" ]; do :; done; printf ok`, "sh", pidFile)
	t.Cleanup(func() { killHolder(t, pidFile) })

	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- supervise(context.Background(), cmd, &out, nil) }()
	select {
	case err := <-done:
		if !errors.Is(err, errGroupSurvived) {
			t.Fatalf("supervise = %v, want the survivor", err)
		}
	case <-time.After(groupSweepDeadline + 5*time.Second):
		t.Fatal("a pipe-holding survivor blocked the drain")
	}
	if len(heard) != 1 || !errors.Is(heard[0], errGroupSurvived) {
		t.Fatalf("reporter heard %v, want the one survivor", heard)
	}
}

func killHolder(t *testing.T, pidFile string) {
	t.Helper()
	raw, err := os.ReadFile(pidFile) // #nosec G304 -- a test temp file
	if err != nil {
		return
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

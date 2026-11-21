//go:build linux

package integrationhost

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

// The direct worker stays an unreaped zombie from its exit until retire is
// done, so its process ID, the group ID, cannot be reused while retire signals
// the group, and retire does not count the zombie as a live member
// (CORE-2750).
func TestLeaderStaysReservedThroughRetire(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 3")
	tree, err := newWorkerProcessTree(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.close()
	if err := tree.start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	leader := &leaderWait{cmd: cmd}
	leader.awaitExit()
	assertZombieLeader := func(when string) {
		t.Helper()
		state, group, ok := procStateAndGroup(pid)
		if !ok || state != 'Z' || group != pid {
			t.Fatalf("%s: leader %d = state %q group %d present %v, want an unreaped zombie leading its group", when, pid, state, group, ok)
		}
	}
	assertZombieLeader("before retire")
	if processGroupHasLiveMember(pid) {
		t.Fatal("the zombie leader counted as a live member")
	}
	if err := tree.retire(0, workerKillDeadline); err != nil {
		t.Fatalf("retire: %v", err)
	}
	assertZombieLeader("after retire")
	var exitErr *exec.ExitError
	if err := leader.reap(); !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("reap = %v, want exit status 3", err)
	}
	if _, _, ok := procStateAndGroup(pid); ok {
		t.Fatalf("leader %d still present after reap", pid)
	}
}

func TestSuperviseWorkerKeepsLeaderExitStatus(t *testing.T) {
	err := superviseWorker(context.Background(), exec.Command("/bin/sh", "-c", "exit 4"), nil)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 4 {
		t.Fatalf("superviseWorker = %v, want exit status 4", err)
	}
}

func TestParseProcStat(t *testing.T) {
	for _, tc := range []struct {
		line  string
		state byte
		pgid  int
		ok    bool
	}{
		{"42 (sh) S 1 42 42 0 -1", 'S', 42, true},
		{"43 (a) Z (b) Z 42 7 7 0", 'Z', 7, true},
		{"44 (sh", 0, 0, false},
		{"45 (sh) S 1", 0, 0, false},
	} {
		state, pgid, ok := parseProcStat([]byte(tc.line))
		if state != tc.state || pgid != tc.pgid || ok != tc.ok {
			t.Errorf("parseProcStat(%q) = %q %d %v, want %q %d %v", tc.line, state, pgid, ok, tc.state, tc.pgid, tc.ok)
		}
	}
}

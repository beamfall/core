package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The shim holds a pin on its group from before the guardian joins it until
// the guardian reports readiness, and reaps it before returning, so the
// guardian is never in an unpinned group and the artifact inherits no child
// (CORE-2754).
func TestWorkerShimPinsGroupUntilGuardianReady(t *testing.T) {
	dir := t.TempDir()
	release := filepath.Join(dir, "release")
	if err := syscall.Mkfifo(release, 0o600); err != nil {
		t.Fatal(err)
	}
	lifetimeR, lifetimeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lifetimeR.Close(); _ = lifetimeW.Close() }()

	// The stand-in guardian reports readiness only once the test lets it.
	// #nosec G204 -- fixed shell; the FIFO is in this test's private directory.
	cmd := exec.Command("/bin/sh", "-c", `read -r _ < "$0"; printf '\245' >&4; exec /bin/sleep 30`, release)
	started := make(chan error, 1)
	go func() { started <- startWorkerShimGuardianProcess(lifetimeR, cmd, 10*time.Second) }()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	pin := waitForZombieChildInGroup(t, syscall.Getpgrp(), true)
	f, err := os.OpenFile(release, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("go\n")
	_ = f.Close()
	if err := <-started; err != nil {
		t.Fatalf("start guardian: %v", err)
	}
	if got := waitForZombieChildInGroup(t, syscall.Getpgrp(), false); got != 0 {
		t.Fatalf("shim pin %d still unreaped after readiness", got)
	}
	if err := syscall.Kill(pin, 0); err == nil {
		t.Fatalf("shim pin %d still exists after readiness", pin)
	}
}

// waitForZombieChildInGroup waits until a zombie child of this process is (or,
// with want false, is no longer) a member of group pgid and returns its pid,
// or 0 when there is none.
func waitForZombieChildInGroup(t *testing.T, pgid int, want bool) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		procs, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
		if err != nil {
			t.Fatalf("list group %d: %v", pgid, err)
		}
		found := 0
		for i := range procs {
			if procs[i].Proc.P_stat == 5 && int(procs[i].Eproc.Ppid) == os.Getpid() {
				found = int(procs[i].Proc.P_pid)
			}
		}
		if (found != 0) == want {
			return found
		}
		if time.Now().After(deadline) {
			t.Fatalf("zombie child in group %d: found %d, want present=%v", pgid, found, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

//go:build unix && !darwin

package integrationhost

import (
	"errors"
	"syscall"
	"time"
)

// validProcessGroup accepts group 1 only as the caller's own group: a worker
// sandbox (bwrap --new-session) puts the shim and its guardian in group 1 of
// their own PID namespace. groupTarget then signals it as kill(0, sig), never
// as kill(-1, sig), which would reach every process the caller may signal.
func validProcessGroup(pgid int) bool {
	return pgid > 1 || (pgid == 1 && syscall.Getpgrp() == 1)
}

// groupTarget is the kill(2) target for group pgid: 0 for the caller's own
// group, so group 1 is never signalled as -1.
func groupTarget(pgid int) int {
	if pgid == syscall.Getpgrp() {
		return 0
	}
	return -pgid
}

// pinProcessGroup returns an empty pin: outside XNU one group KILL reaches
// every member, so callers rely on the pins they already hold.
func pinProcessGroup(int) (*ProcessGroupPin, error) { return &ProcessGroupPin{}, nil }

// killProcessGroup sends one group KILL: outside XNU a fork racing it is
// restarted, so it reaches every member. A leaving caller dies of it too.
func killProcessGroup(pgid int, _ bool, _ time.Duration) error {
	if err := syscall.Kill(groupTarget(pgid), syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// processGroupHasLiveMember reports whether group pgid has a member. Linux
// counts zombies until they are reaped.
func processGroupHasLiveMember(pgid int) bool {
	return !errors.Is(syscall.Kill(groupTarget(pgid), 0), syscall.ESRCH)
}

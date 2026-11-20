package integrationhost

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// processGroupKillInterval spaces the KILLs killProcessGroup repeats and
	// the probes that confirm a group has no live member.
	processGroupKillInterval = 2 * time.Millisecond
	// processGroupQuietProbes is how many probes in a row must find no live
	// member before a group counts as empty.
	processGroupQuietProbes = 3
)

// validProcessGroup refuses 1 as well as non-positive IDs: group 1 is
// launchd's, and kill(-1, sig) would signal every process the caller may
// signal.
func validProcessGroup(pgid int) bool { return pgid > 1 }

// pinProcessGroup places a child in group pgid that exits at once and stays an
// unreaped zombie until release. A process ID still in use cannot be handed
// out again, so the group ID stays reserved after every other member, the
// caller included, has gone. It is /bin/sh because the worker sandbox lets the
// guardian exec only the shim, the artifact and /bin/sh; its exit status does
// not matter.
func pinProcessGroup(pgid int) (*ProcessGroupPin, error) {
	pin := exec.Command("/bin/sh", "-c", "exit 0")
	pin.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}
	if err := pin.Start(); err != nil {
		return nil, fmt.Errorf("pin process group %d: %w", pgid, err)
	}
	return &ProcessGroupPin{cmd: pin}, nil
}

func killProcessGroup(pgid int, leave bool, timeout time.Duration) error {
	if leave {
		if err := syscall.Setpgid(0, 0); err != nil {
			// Still a member: the caller dies of this one KILL.
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			return fmt.Errorf("leave process group %d: %w", pgid, err)
		}
	}
	deadline := time.Now().Add(timeout)
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	for processGroupHasLiveMember(pgid) {
		if time.Now().After(deadline) {
			return fmt.Errorf("process group %d survived SIGKILL", pgid)
		}
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		time.Sleep(processGroupKillInterval)
	}
	return nil
}

// processGroupHasLiveMember lists group pgid until several listings in a row
// find no member that is not a zombie. It reads the kernel's member list
// rather than probing with signal 0, which answers EPERM both for a group of
// zombies and for a live member the caller may not signal. One listing is not
// enough: a parent that exits during it can hide a child it forked meanwhile.
// A failed listing counts as a live member.
func processGroupHasLiveMember(pgid int) bool {
	for i := range processGroupQuietProbes {
		if groupListsLiveMember(pgid) {
			return true
		}
		if i+1 < processGroupQuietProbes {
			time.Sleep(processGroupKillInterval)
		}
	}
	return false
}

// zombieState is SZOMB from XNU's sys/proc.h.
const zombieState = 5

func groupListsLiveMember(pgid int) bool {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
	if err != nil {
		return true
	}
	for i := range procs {
		if procs[i].Proc.P_stat != zombieState {
			return true
		}
	}
	return false
}

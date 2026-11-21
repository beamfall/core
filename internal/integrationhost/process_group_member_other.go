//go:build unix && !darwin && !linux

package integrationhost

import (
	"errors"
	"syscall"
)

// processGroupHasLiveMember reports whether group pgid has a member, zombies
// included until they are reaped.
func processGroupHasLiveMember(pgid int) bool {
	return !errors.Is(syscall.Kill(groupTarget(pgid), 0), syscall.ESRCH)
}

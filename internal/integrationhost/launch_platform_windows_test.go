//go:build windows

package integrationhost

import (
	"errors"
	"testing"
)

func TestWindowsWorkerLaunchRejectsBeforeSpawnSetup(t *testing.T) {
	// Supervisor.Run validates before creating pipes or starting a process.
	// Rejection is the Windows containment guarantee until transport is supported.
	err := validateWorkerLaunch(`Z:\missing\beamfall.exe`)
	if !errors.Is(err, ErrWorkerPlatformUnsupported) {
		t.Fatalf("platform validation error = %v, want %v", err, ErrWorkerPlatformUnsupported)
	}
}

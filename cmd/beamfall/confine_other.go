//go:build !linux

package main

import (
	"fmt"
	"runtime"

	"github.com/beamfall/core/internal/integrationhost"
)

// applyConfinement is a no-op on non-Linux platforms: landlock is
// a Linux LSM with no portable equivalent, so on darwin (the dev/gate host) and
// other systems the ffmpeg child is bounded by rlimits only, not filesystem
// confinement. The roPaths/rwPaths/binary set is computed identically on every
// platform (so it is unit-testable here); only the kernel enforcement is
// Linux-only. binary is the exec target whose directory must remain reachable.
func applyConfinement(_, _ []string, _ string) error { return nil }

// applyLocalInferenceConfinement fails closed everywhere landlock is absent.
// Local inference carries a stricter contract than the ffmpeg shim: ADR-0127 §1
// requires an OS-enforced deny-all profile, so a host with no kernel
// enforcement must refuse the launch rather than fall back to rlimits. On
// darwin the confined command is built by integrationhost as a sandbox-exec
// invocation and never reaches this shim at all.
func applyLocalInferenceConfinement(_, _ []string, _ string) error {
	return fmt.Errorf("%w: no landlock enforcement on %s",
		integrationhost.ErrLocalInferenceConfinementUnavailable, runtime.GOOS)
}

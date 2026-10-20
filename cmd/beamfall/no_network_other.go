//go:build !linux

package main

import (
	"fmt"
	"runtime"

	"github.com/beamfall/core/internal/integrationhost"
)

func applyNoNetwork() error { return nil }

// applyLocalInferenceNoNetwork fails closed off linux: seccomp is the only
// enforcement this shim has, so a platform without it cannot honour the
// no-network half of the ADR-0127 §1 profile and must refuse the launch.
func applyLocalInferenceNoNetwork() error {
	return fmt.Errorf("%w: no seccomp enforcement on %s",
		integrationhost.ErrLocalInferenceConfinementUnavailable, runtime.GOOS)
}

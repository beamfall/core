package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// workerShim is the hardening re-exec target for every integration worker spawn.
// internal/integrationhost launches
//
//	beamfall worker-shim -- <artifact> <args...>
//
// This process lowers its own resource limits (rlimits) then execs the worker
// artifact in place via syscall.Exec. Because exec replaces the process image
// without forking, the worker artifact keeps this shim's PID, so the parent's
// Pdeathsig orphan guard still binds it (ADR-0058, mirrors ADR-0012 §10 for
// ffmpeg-shim).
//
// The shim always execs the worker with an empty environment: the Supervisor
// already enforces cmd.Env=[] on the command it spawns (the shim), and the shim
// carries that discipline through to the artifact it execs so the worker
// inherits nothing from the shim's own process environment.
//
// The worker-shim subcommand is an internal hardening detail, not an
// operator-facing command; it is dispatched from run()'s switch but
// intentionally omitted from help text.
func workerShim(_ context.Context, args []string) error {
	if len(args) == 1 && args[0] == "--guardian" {
		return runWorkerShimGuardian()
	}
	argv, guardianFD, err := parseWorkerShimArgs(args)
	if err != nil {
		return err
	}
	return runWorkerShimWithGuardian(argv, guardianFD)
}

// parseWorkerShimArgs extracts the target command from the worker-shim argv.
// The grammar is:
//
//	[--guardian-fd <fd>] -- <artifact> <args...>
//
// At least the artifact binary must follow "--".
func parseWorkerShimArgs(args []string) (argv []string, guardianFD int, err error) {
	for len(args) > 0 {
		current := args[0]
		args = args[1:]
		switch current {
		case "--guardian-fd":
			if len(args) == 0 {
				return nil, 0, errors.New("worker-shim: --guardian-fd requires a descriptor")
			}
			value := args[0]
			args = args[1:]
			guardianFD, err = strconv.Atoi(value)
			if err != nil || guardianFD < 3 {
				return nil, 0, fmt.Errorf("worker-shim: invalid guardian descriptor %q", value)
			}
		case "--":
			argv = args
			args = nil
		default:
			return nil, 0, fmt.Errorf("worker-shim: unexpected argument %q", current)
		}
	}
	if len(argv) == 0 {
		return nil, 0, errors.New("worker-shim: missing command after --")
	}
	return argv, guardianFD, nil
}

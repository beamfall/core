package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// Mandatory platform confinement at the common spawn seam (CORE-0700).
//
// Once the server has configured the hardening shim (SetHardeningShim,
// SetProbeHardeningShim), every FFmpeg/ffprobe spawn built by shimCommand runs
// under kernel-enforced filesystem confinement scoped to the job's
// confinement, or it does not run at all:
//
//   - linux: the shim is told --require-landlock, so a kernel without landlock,
//     or with a landlock ABI below the one that fences truncate(2), refuses the
//     launch (the shim exits ShimFailureExitCode) instead of running FFmpeg on
//     rlimits alone.
//   - darwin: the shim runs inside `/usr/bin/sandbox-exec` with a deny-default
//     Seatbelt profile that grants only the job's paths. A host without
//     sandbox-exec refuses the launch.
//   - every other platform: refused. There is no kernel confinement the server
//     knows how to apply there.
//
// A refusal is reported through exec.Cmd.Err, so Start fails with
// ErrConfinementUnavailable before any process exists and no caller can read
// it as FFmpeg's verdict on the media. There is deliberately no operator
// switch to run media unconfined.
//
// A Runner without a configured shim (the default for every non-server
// process, including test binaries) still execs FFmpeg directly: hardening is
// enabled by the server entrypoint, and that entrypoint's shim resolution
// fails closed (DefaultShimPath) rather than leaving the shim unset.

// ErrConfinementUnavailable reports a media spawn refused because this host
// cannot apply the mandatory kernel confinement.
var ErrConfinementUnavailable = errors.New("ffmpeg: mandatory media confinement is unavailable on this host")

// unresolvedShimPath is what DefaultShimPath returns when the server binary
// cannot be located. It is not a usable path: the spawn seam refuses it, so a
// server that cannot find its own binary runs no media instead of running it
// unconfined.
const unresolvedShimPath = "\x00beamfall-ffmpeg-shim-unresolved"

// spawnGOOS is the platform whose confinement the seam applies; a test seam so
// each platform's command is checkable on any host.
var spawnGOOS = runtime.GOOS

// sandboxExecPath is the macOS Seatbelt launcher. It is an absolute path, not a
// PATH lookup, so a writable PATH entry cannot stand in for it; a variable so a
// test can point it at a missing file.
var sandboxExecPath = "/usr/bin/sandbox-exec"

// shimCommand wraps binPath in the shim at shimPath under conf and the host's
// mandatory confinement, or execs it directly when shimPath is empty.
// Transcodes take shimPath from their Runner and hardware probes from
// SetProbeHardeningShim, so a probe runs under the same wrapping as the job it
// certifies.
func shimCommand(ctx context.Context, shimPath, binPath string, conf confinement, args []string) *exec.Cmd {
	if shimPath == "" {
		return exec.CommandContext(ctx, binPath, args...) // #nosec G204 -- binPath is the resolved ffmpeg/ffprobe path and args are a []string, never a shell string
	}
	argv, err := confinedArgv(spawnGOOS, shimPath, binPath, conf, args)
	if err != nil {
		return refusedCommand(ctx, binPath, args, err)
	}
	// #nosec G204 -- argv[0] is this server binary or /usr/bin/sandbox-exec and
	// the argv is built as a []string, never a shell string.
	return exec.CommandContext(ctx, argv[0], argv[1:]...)
}

// confinedArgv builds the full argv of a confined spawn on goos, or reports
// why goos cannot confine it.
func confinedArgv(goos, shimPath, binPath string, conf confinement, args []string) ([]string, error) {
	if shimPath == unresolvedShimPath {
		return nil, fmt.Errorf("%w: the server binary to re-exec as the hardening shim could not be resolved", ErrConfinementUnavailable)
	}
	shimArgs := buildShimArgs(conf, binPath, args)
	switch goos {
	case "linux":
		// --require-landlock goes right after the subcommand, ahead of the "--"
		// that starts the target command.
		required := append([]string{shimPath, shimArgs[0], "--require-landlock"}, shimArgs[1:]...)
		return required, nil
	case "darwin":
		if !executableFile(sandboxExecPath) {
			return nil, fmt.Errorf("%w: %s is not available", ErrConfinementUnavailable, sandboxExecPath)
		}
		profile := sandboxProfileFor(conf, shimPath, binPath)
		return append([]string{sandboxExecPath, "-p", profile, shimPath}, shimArgs...), nil
	default:
		return nil, fmt.Errorf("%w: no supported kernel confinement on %s", ErrConfinementUnavailable, goos)
	}
}

// refusedCommand is a command whose Start fails with err before any process is
// created, so a refused confinement flows through every caller's ordinary
// start-failure path.
func refusedCommand(ctx context.Context, binPath string, args []string, err error) *exec.Cmd {
	// #nosec G204 -- never started: Err makes Start return before exec.
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Err = err
	return cmd
}

// executableFile reports whether path is a regular file with an execute bit.
func executableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

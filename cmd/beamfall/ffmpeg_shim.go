package main

import (
	"context"
	"errors"
	"fmt"
)

// ffmpegShim is the hardening re-exec target for every ffmpeg/ffprobe spawn.
// internal/ffmpeg launches `beamfall ffmpeg-shim --ro <dir>...
// --rw <dir>... -- <binary> <args...>`; this process lowers its own resource
// limits (rlimits — portable), confines its filesystem reach to the allowed
// paths (landlock — Linux only), then execs the real binary in place via
// syscall.Exec. Because exec replaces the process image without forking, the
// ffmpeg/ffprobe process keeps this shim's PID, so the parent's Pdeathsig orphan
// guard still binds it.
//
// The shim is an internal hardening detail, not an operator-facing command; it
// is dispatched from run()'s switch but intentionally omitted from any help
// text. It writes nothing to stdout/stderr on the happy path — it execs and is
// gone — so it is transparent to the caller's CombinedOutput()/Output() capture
// of the real binary's streams.
func ffmpegShim(_ context.Context, args []string) error {
	req, err := parseShimArgs(args)
	if err != nil {
		return err
	}
	return runFFmpegShim(req)
}

// ffmpegShimRequest is one parsed shim invocation.
type ffmpegShimRequest struct {
	ro, rw []string // job directories, read-only and read-write
	argv   []string // the target binary and its arguments
	// requireLandlock makes kernel confinement a precondition.
	requireLandlock bool
	// dri grants the GPU render nodes and the OpenCL ICD registry.
	dri bool
}

// parseShimArgs splits the shim argv into the read-only paths, read-write paths,
// the target command (binary + its args), and the confinement flags. The
// grammar is:
//
//	[--require-landlock] [--dri] --ro <dir> ... --rw <dir> ... -- <binary> <args...>
//
// --require-landlock turns the best-effort Linux landlock confinement into a
// precondition: a kernel without landlock, or with a landlock ABI below 3 that
// cannot fence truncate(2), refuses the launch instead of running ffmpeg on
// rlimits or a partial ruleset. The encrypted-library pipe transcode sets it, because
// that child reads decrypted bytes over loopback and must never run unconfined.
// --dri adds the render-node and OpenCL registry grants a QSV or VA-API job
// needs (CORE-2158); internal/ffmpeg sets it only for such a job or its probe.
// A bare "--" terminates the flag section; everything after it is the command,
// taken verbatim (so a media path that looks like a flag is never reinterpreted
// here). At least the binary must follow "--".
func parseShimArgs(args []string) (ffmpegShimRequest, error) {
	var req ffmpegShimRequest
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--ro":
			if i+1 >= len(args) {
				return ffmpegShimRequest{}, errors.New("ffmpeg-shim: --ro requires a path")
			}
			req.ro = append(req.ro, args[i+1]) // #nosec G602 -- bounds checked just above
			i += 2
		case "--rw":
			if i+1 >= len(args) {
				return ffmpegShimRequest{}, errors.New("ffmpeg-shim: --rw requires a path")
			}
			req.rw = append(req.rw, args[i+1]) // #nosec G602 -- bounds checked just above
			i += 2
		case "--require-landlock":
			req.requireLandlock = true
			i++
		case "--dri":
			req.dri = true
			i++
		case "--":
			req.argv = args[i+1:]
			i = len(args)
		default:
			return ffmpegShimRequest{}, fmt.Errorf("ffmpeg-shim: unexpected argument %q", args[i])
		}
	}
	if len(req.argv) == 0 {
		return ffmpegShimRequest{}, errors.New("ffmpeg-shim: missing command after --")
	}
	return req, nil
}

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
	roPaths, rwPaths, argv, err := parseShimArgs(args)
	if err != nil {
		return err
	}
	return runFFmpegShim(roPaths, rwPaths, argv)
}

// parseShimArgs splits the shim argv into the read-only paths, read-write paths,
// and the target command (binary + its args). The grammar is:
//
//	--ro <dir> ... --rw <dir> ... -- <binary> <args...>
//
// A bare "--" terminates the flag section; everything after it is the command,
// taken verbatim (so a media path that looks like a flag is never reinterpreted
// here). At least the binary must follow "--".
func parseShimArgs(args []string) (ro, rw, argv []string, err error) {
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--ro":
			if i+1 >= len(args) {
				return nil, nil, nil, errors.New("ffmpeg-shim: --ro requires a path")
			}
			ro = append(ro, args[i+1])
			i += 2
		case "--rw":
			if i+1 >= len(args) {
				return nil, nil, nil, errors.New("ffmpeg-shim: --rw requires a path")
			}
			rw = append(rw, args[i+1])
			i += 2
		case "--":
			argv = args[i+1:]
			i = len(args)
		default:
			return nil, nil, nil, fmt.Errorf("ffmpeg-shim: unexpected argument %q", args[i])
		}
	}
	if len(argv) == 0 {
		return nil, nil, nil, errors.New("ffmpeg-shim: missing command after --")
	}
	return ro, rw, argv, nil
}

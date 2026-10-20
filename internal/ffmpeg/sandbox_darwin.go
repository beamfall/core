//go:build darwin

package ffmpeg

import "os/exec"

// sandboxExecBinary is the macOS userland entrypoint to the kernel Seatbelt
// sandbox. It is the only supported way to launch a child under a named SBPL
// profile from a non-entitled process; ADR-0124 §6 names it explicitly.
const sandboxExecBinary = "sandbox-exec"

// sandboxWrap wraps a convert spawn in the macOS `sandbox-exec` deny-all SBPL
// profile, confining the FFmpeg child to exactly roPaths
// (read-only) and rwPaths (read-write) plus the read-only system trees the
// binary needs to load. It returns the command to launch and reports whether
// wrapping was applied. When `sandbox-exec` is not on PATH it returns the
// original command unwrapped (false) — defense-in-depth that never breaks a
// legitimate transcode, mirroring the un-hardened fallback of the SEC-2 shim.
//
// The wrapped invocation is:
//
//	sandbox-exec -p <profile> <binPath> <args...>
//
// `-p` takes the profile inline (not a file), so no temp profile file is
// written. The kernel enforces the profile on the child and every process it
// execs (including the SEC-2 shim's re-exec of ffmpeg), so a crafted media file
// that exploits ffmpeg cannot read or write outside the allow-list.
func sandboxWrap(roPaths, rwPaths []string, binPath string, args []string) (string, []string, bool) {
	sandboxExec, err := exec.LookPath(sandboxExecBinary)
	if err != nil {
		return binPath, args, false
	}
	profile := sandboxProfile(roPaths, rwPaths)
	wrapped := append([]string{"-p", profile, binPath}, args...)
	return sandboxExec, wrapped, true
}

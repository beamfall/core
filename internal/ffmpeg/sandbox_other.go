//go:build !darwin

package ffmpeg

// sandboxWrap is a pass-through on non-darwin platforms. macOS
// uses `sandbox-exec` for OS-enforced filesystem confinement of the convert
// child; on Linux that role is filled by the SEC-2 re-exec shim (seccomp-bpf +
// landlock), applied independently in the shim, so there is nothing to wrap
// here. The profile builder (sandbox.go) stays platform-neutral and remains
// unit-testable everywhere; only the wrapping is darwin-specific.
func sandboxWrap(_, _ []string, binPath string, args []string) (string, []string, bool) {
	return binPath, args, false
}

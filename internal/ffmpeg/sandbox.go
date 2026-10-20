package ffmpeg

import (
	"fmt"
	"path/filepath"
	"strings"
)

// OS-enforced FFmpeg sandbox for the conversion workflow. The
// FFmpeg child that transcodes a user's media is the platform's primary RCE
// surface — a crafted input file that exploits a decoder must not be able to
// read or write anything beyond the two paths the job legitimately needs
// (read-only staged input; the convert-staging/trash write root). On Linux that
// containment is the SEC-2 re-exec shim (seccomp-bpf + landlock). On macOS the
// shim cannot landlock, so the convert path additionally wraps the spawn in
// `sandbox-exec` with a named SBPL profile: a deny-all-by-default Seatbelt
// profile that whitelists only the allow-list paths. This is OS-enforced by the
// kernel sandbox, not a mere argument convention, and it explicitly OVERRIDES
// the ADR-0099 TCC/parent-delegated full-disk inheritance — the child does not
// inherit the server's filesystem authority.
//
// The profile lives here (platform-neutral, unit-testable as a string) while
// the actual `sandbox-exec` wrapping is darwin-only (sandbox_darwin.go); on
// every other platform sandboxWrap is a pass-through (sandbox_other.go).

// sandboxProfile builds the SBPL (Seatbelt) profile string for one convert job:
// deny everything by default, then allow exactly the syscalls and paths a
// transcode needs. roPaths are granted file-read*; rwPaths are granted
// file-read* and file-write*. Network is denied entirely. Process exec/fork are
// allowed so the shim can exec ffmpeg and ffmpeg can spawn its own threads, and
// the common read-only system trees (the binary, dyld, shared libraries) are
// granted read so the binary actually loads.
//
// Paths are emitted as `subpath` rules (the whole directory subtree) using a
// quoted literal so a path containing SBPL metacharacters cannot break out of
// the string; an empty path is skipped.
func sandboxProfile(roPaths, rwPaths []string) string {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n")
	// Import the system base profile: it grants exactly the dyld/shared-cache,
	// mach-bootstrap, and sysctl surface every macOS process needs to load and
	// start (without it a deny-default child aborts in dyld before main). It does
	// NOT grant filesystem write or network access — those stay denied by default,
	// so the deny-all posture for the job's own data is preserved.
	b.WriteString("(import \"system.sb\")\n")
	// Allow process machinery: exec the real binary and fork worker threads.
	// Without these the sandboxed shim cannot even exec ffmpeg.
	b.WriteString("(allow process-exec*)\n")
	b.WriteString("(allow process-fork)\n")
	// Deny all network access explicitly (defense in depth over deny-default):
	// the convert child has no business reaching the network.
	b.WriteString("(deny network*)\n")
	// Read-only system trees where ffmpeg and its dylibs may live outside the
	// dyld shared cache (Homebrew prefixes); read-only, never writable.
	for _, p := range sandboxSystemReadRoots() {
		b.WriteString(sandboxRule("file-read*", p))
	}
	// Job read-only inputs.
	for _, p := range sandboxResolvePaths(roPaths) {
		b.WriteString(sandboxRule("file-read*", p))
	}
	// Job read-write outputs (the staging/trash write root): both read and write.
	for _, p := range sandboxResolvePaths(rwPaths) {
		b.WriteString(sandboxRule("file-read*", p))
		b.WriteString(sandboxRule("file-write*", p))
	}
	return b.String()
}

// sandboxResolvePaths expands each allow-list path to both its literal form and
// its symlink-resolved real form, de-duplicated. macOS resolves SBPL subpath
// rules against the kernel's canonical (real) path, but a job path may arrive
// through a symlinked prefix — most importantly the per-process temp dir under
// /var/folders, which is a symlink to /private/var/folders. Granting only the
// literal path would let the kernel see the resolved path and deny the write
// (an abort trap). Granting both forms keeps a legitimate convert working while
// still scoping the grant to exactly the job's directory subtree.
func sandboxResolvePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths)*2)
	out := make([]string, 0, len(paths)*2)
	add := func(p string) {
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for _, p := range paths {
		add(p)
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			add(resolved)
		}
	}
	return out
}

// sandboxRule emits one SBPL filesystem rule scoped to a directory subtree. The
// path is rendered as a quoted literal so metacharacters in the path cannot
// alter the profile grammar. An empty path produces no rule.
func sandboxRule(op, path string) string {
	if path == "" {
		return ""
	}
	return fmt.Sprintf("(allow %s (subpath %s))\n", op, sandboxQuote(path))
}

// sandboxQuote renders a path as an SBPL string literal, escaping backslashes
// and double quotes so the path cannot terminate the literal early.
func sandboxQuote(path string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(path) + `"`
}

// sandboxSystemReadRoots are the read-only directory subtrees where ffmpeg and
// its shared libraries may live OUTSIDE the dyld shared cache that the imported
// system.sb already grants: the Homebrew prefixes (Apple-silicon and Intel) and
// the standard /usr/lib path. Granting whole subtrees (not individual files)
// keeps the profile small and tolerates layout differences. These are read-only
// — never writable.
func sandboxSystemReadRoots() []string {
	return []string{
		"/usr/lib",
		"/opt/homebrew", // ffmpeg + its dylibs on Apple-silicon Homebrew hosts
		"/usr/local",    // ffmpeg + its dylibs on Intel Homebrew hosts
		"/dev/null",
	}
}

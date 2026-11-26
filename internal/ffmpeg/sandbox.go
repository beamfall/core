package ffmpeg

import (
	"debug/macho"
	"fmt"
	"os"
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
	return sandboxProfileNet(roPaths, rwPaths, 0)
}

// sandboxProfileNet is sandboxProfile with one exception to the network
// denial: when port is non-zero, outbound TCP to that loopback port. The
// encrypted-library pipe transcode reads its source from that port and is
// given no other reach.
func sandboxProfileNet(roPaths, rwPaths []string, port int) string {
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
	if port > 0 && port <= 65535 {
		fmt.Fprintf(&b, "(allow network-outbound (remote ip \"localhost:%d\"))\n", port)
	}
	// VideoToolbox: the IOSurface, GPU and media-engine user clients a hardware
	// encode or decode opens. Without them a VideoToolbox job fails under the
	// profile (encode exit 187, decode exit 69, which the decode verdict would
	// read as broken media). These are device clients, not filesystem or
	// network reach.
	b.WriteString(sandboxVideoToolboxRule)
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

// sandboxVideoToolboxRule grants the IOKit user clients VideoToolbox opens.
const sandboxVideoToolboxRule = `(allow iokit-open (iokit-user-client-class "IOSurfaceRootUserClient" "AGXDeviceUserClient" "AppleAVE2UserClient" "AppleAVDUserClient" "IOSurfaceAcceleratorClient"))` + "\n"

// sandboxProfileFor is the profile the spawn seam runs one confined job under
// on macOS: the job's own paths and loopback port, plus literal read access to
// the shim binary, the executable itself, and any non-system runtime shared
// libraries it links (never the executable's parent directory subtree).
func sandboxProfileFor(conf confinement, shimPath, binPath string) string {
	var b strings.Builder
	b.WriteString(sandboxProfileNet(conf.ReadOnly, conf.ReadWrite, conf.LoopbackPort))
	literals := []string{shimPath, binPath}
	literals = append(literals, sandboxBinaryRuntimeLibs(binPath)...)
	for _, p := range sandboxResolvePaths(literals) {
		fmt.Fprintf(&b, "(allow file-read* (literal %s))\n", sandboxQuote(p))
	}
	return b.String()
}

// sandboxBinaryRuntimeLibs inspects binPath's Mach-O load commands (if binPath
// exists and is a Mach-O binary on disk) and returns concrete shared-library
// paths outside sandboxSystemReadRoots that dyld needs to load it, including
// inherited @rpath chains and reexported/weak/upward dylibs. Sibling files in
// the executable's or library directories are never granted.
func sandboxBinaryRuntimeLibs(binPath string) []string {
	resolvedBin, err := filepath.EvalSymlinks(binPath)
	if err != nil {
		resolvedBin = binPath
	}
	if resolvedBin == "" {
		return nil
	}
	execDirs := uniqueAbsPaths([]string{filepath.Dir(resolvedBin), filepath.Dir(binPath)})
	type libQueueItem struct {
		path            string
		inheritedRpaths []string
	}
	queue := []libQueueItem{{path: resolvedBin}}
	seenObj := map[string]bool{resolvedBin: true}
	seenRpathForObj := map[string]map[string]bool{resolvedBin: {}}
	var out []string
	seenLib := map[string]bool{}
	const maxLibs = 64
	const maxVisits = 256

	for visits := 0; len(queue) > 0 && len(seenLib) < maxLibs && visits < maxVisits; visits++ {
		cur := queue[0]
		queue = queue[1:]
		imports, rawRpaths := readMachoDeps(cur.path)
		if len(imports) == 0 {
			continue
		}
		loaderDir := filepath.Dir(cur.path)
		ownRpaths := resolveMachoRpaths(execDirs, loaderDir, rawRpaths)
		combinedRpaths := uniqueAbsPaths(append(ownRpaths, cur.inheritedRpaths...))
		for _, imp := range imports {
			for _, cand := range resolveMachoImportPaths(imp, execDirs, loaderDir, combinedRpaths) {
				if isCoveredBySystemSandboxRoot(cand) {
					continue
				}
				info, err := os.Stat(cand)
				if err != nil || info.IsDir() {
					continue
				}
				if !seenLib[cand] {
					seenLib[cand] = true
					out = append(out, cand)
				}
				realCand := cand
				if r, err := filepath.EvalSymlinks(cand); err == nil {
					realCand = r
				}
				if !seenObj[realCand] {
					if len(seenObj) < maxLibs {
						seenObj[realCand] = true
						seenRpathForObj[realCand] = rpathSet(combinedRpaths)
						queue = append(queue, libQueueItem{path: realCand, inheritedRpaths: combinedRpaths})
					}
				} else if extra := newRpaths(seenRpathForObj[realCand], combinedRpaths); len(extra) > 0 && len(queue) < maxVisits {
					for _, rp := range extra {
						seenRpathForObj[realCand][rp] = true
					}
					queue = append(queue, libQueueItem{path: realCand, inheritedRpaths: extra})
				}
				break
			}
		}
	}
	return out
}

func readMachoDeps(path string) (imports, rpaths []string) {
	if f, err := macho.Open(path); err == nil {
		defer func() { _ = f.Close() }()
		return machoFileDeps(f)
	}
	if fat, err := macho.OpenFat(path); err == nil {
		defer func() { _ = fat.Close() }()
		for _, arch := range fat.Arches {
			imps, rps := machoFileDeps(arch.File)
			imports = append(imports, imps...)
			rpaths = append(rpaths, rps...)
		}
	}
	return imports, rpaths
}

const (
	loadCmdLoadWeakDylib   macho.LoadCmd = 0x80000018
	loadCmdReexportDylib   macho.LoadCmd = 0x8000001f
	loadCmdLazyLoadDylib   macho.LoadCmd = 0x20
	loadCmdLoadUpwardDylib macho.LoadCmd = 0x80000023
)

func machoFileDeps(f *macho.File) (imports, rpaths []string) {
	imports, _ = f.ImportedLibraries()
	for _, load := range f.Loads {
		switch l := load.(type) {
		case *macho.Rpath:
			if l.Path != "" {
				rpaths = append(rpaths, l.Path)
			}
		case macho.LoadBytes:
			if len(l) < 24 || f.ByteOrder == nil {
				continue
			}
			cmd := macho.LoadCmd(f.ByteOrder.Uint32(l[0:4]))
			switch cmd {
			case loadCmdLoadWeakDylib, loadCmdReexportDylib, loadCmdLazyLoadDylib, loadCmdLoadUpwardDylib:
				nameOff := f.ByteOrder.Uint32(l[8:12])
				if nameOff >= 24 && int(nameOff) < len(l) {
					if name := machoCString(l[nameOff:]); name != "" {
						imports = append(imports, name)
					}
				}
			}
		}
	}
	return imports, rpaths
}

func machoCString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func resolveMachoRpaths(execDirs []string, loaderDir string, rawRpaths []string) []string {
	var out []string
	for _, rp := range rawRpaths {
		out = append(out, expandMachoPath(rp, execDirs, loaderDir)...)
	}
	return uniqueAbsPaths(out)
}

func resolveMachoImportPaths(imp string, execDirs []string, loaderDir string, rpaths []string) []string {
	if strings.HasPrefix(imp, "@rpath/") {
		rel := strings.TrimPrefix(imp, "@rpath/")
		out := make([]string, 0, len(rpaths))
		for _, base := range rpaths {
			if filepath.IsAbs(base) {
				out = append(out, filepath.Clean(filepath.Join(base, rel)))
			}
		}
		return out
	}
	return expandMachoPath(imp, execDirs, loaderDir)
}

func expandMachoPath(p string, execDirs []string, loaderDir string) []string {
	switch {
	case strings.HasPrefix(p, "@executable_path/"):
		rel := strings.TrimPrefix(p, "@executable_path/")
		out := make([]string, 0, len(execDirs))
		for _, execDir := range execDirs {
			if filepath.IsAbs(execDir) {
				out = append(out, filepath.Clean(filepath.Join(execDir, rel)))
			}
		}
		return out
	case p == "@executable_path":
		return uniqueAbsPaths(execDirs)
	case strings.HasPrefix(p, "@loader_path/"):
		if !filepath.IsAbs(loaderDir) {
			return nil
		}
		return []string{filepath.Clean(filepath.Join(loaderDir, strings.TrimPrefix(p, "@loader_path/")))}
	case p == "@loader_path":
		if !filepath.IsAbs(loaderDir) {
			return nil
		}
		return []string{filepath.Clean(loaderDir)}
	default:
		if !filepath.IsAbs(p) {
			return nil
		}
		return []string{filepath.Clean(p)}
	}
}

func uniqueAbsPaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	var out []string
	for _, p := range paths {
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		c := filepath.Clean(p)
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

func rpathSet(paths []string) map[string]bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return set
}

func newRpaths(seen map[string]bool, candidates []string) []string {
	var extra []string
	for _, p := range candidates {
		if !seen[p] {
			extra = append(extra, p)
		}
	}
	return extra
}

func isCoveredBySystemSandboxRoot(p string) bool {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	for _, root := range append(sandboxSystemReadRoots(), "/System") {
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return false
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

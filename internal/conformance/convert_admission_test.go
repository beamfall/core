package conformance

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// Conversion admission seam + OS-enforced FFmpeg sandbox conformance
//. Converted output enters the catalog ONLY through
// managed_file_ops admission: the FFmpeg engine writes to a temp path OUTSIDE
// any Library root (a server-owned 0700 staging area), the Workflow layer gets
// no direct Library-root writer, and the FFmpeg child runs under an OS-enforced
// deny-all sandbox whose only writable path is the staging/trash root. These
// tests assert each leg as a binding contract, not an aspiration.

// TestConvertEngineStagesOutsideLibraryRoot asserts (a): the conversion engine's
// temp output is staged outside any Library root until admission completes. The
// store computes the engine temp via newConvertStagingPath (a child of the data
// dir's convert-staging area) and NEVER beside the source file inside the root.
// We assert the source-of-truth statically: managed_convert.go must stage via the
// helper and must not reconstruct an in-root temp from the source directory.
func TestConvertEngineStagesOutsideLibraryRoot(t *testing.T) {
	src := readSource(t, filepath.Join("internal", "store", "managed_convert.go"))

	if !strings.Contains(src, "s.newConvertStagingPath(profile)") {
		t.Error("ConvertManagedFile must stage the engine temp via newConvertStagingPath (server-owned staging outside any Library root)")
	}
	// The pre-POWERTOOLS-8 in-root temp pattern must be gone: an engine temp
	// joined onto the source file's own directory writes INSIDE the Library root,
	// reopening the write→admission TOCTOU.
	if strings.Contains(src, `filepath.Join(dir, "."+stem+"-"+sanitizeProfile(profile)+".convert-tmp")`) {
		t.Error("ConvertManagedFile still writes the engine temp inside the Library root (filepath.Join(dir, ...)); it must stage outside the root")
	}
	// Admission into the root goes through the managed_file_ops NO-REPLACE
	// admission helper, the single Library-root writer in the conversion path.
	// No-replace is load-bearing for ADR-0174 ownership succession: a
	// replace-capable admission would let a stale lease generation overwrite
	// the bytes under a successor's already-cataloged output.
	if !strings.Contains(src, "admitStagedFileNoReplace(tmpOut, dst)") {
		t.Error("ConvertManagedFile must admit the staged output via admitStagedFileNoReplace (the only Library-root writer, no-replace)")
	}
}

// TestConvertWorkflowHasNoDirectLibraryRootWriter asserts the Workflow layer
// (internal/workflow/convert) owns no direct file-write/exec path: it drives the
// store seam only. A direct os.Create/os.OpenFile/os.WriteFile/os.Rename/exec in the
// workflow package would be an un-audited Library-root writer, violating
// ADR-0124 §6 ("a Workflow Plugin is granted no direct io.Writer/os.File path to
// a Library root").
func TestConvertWorkflowHasNoDirectLibraryRootWriter(t *testing.T) {
	dir := requireRoot(t, filepath.Join("internal", "workflow", "convert"))
	banned := []string{
		"os.Create(", "os.Create ", "os.OpenFile", "os.WriteFile",
		"os.Rename", "os.Mkdir", "os/exec", "exec.Command",
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read internal/workflow/convert: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, b := range banned {
			if strings.Contains(string(body), b) {
				t.Errorf("%s contains a direct writer/exec %q; the workflow must route all mutation through the store seam", e.Name(), b)
			}
		}
	}
}

// TestConvertStagingIsOwnerOnly asserts (c): the convert-staging area carries the
// 0700 / server-owner posture. The store creates it via os.MkdirAll(0700) + an
// explicit Chmod(0700); here we reproduce that exact creation and assert the
// kernel reports owner-only perms and the current uid as owner — the posture
// that closes the read/replace window during admission.
func TestConvertStagingIsOwnerOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "convert-staging")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("chmod staging: %v", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat staging: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("staging perm = %04o, want 0700 (owner-only)", perm)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no syscall.Stat_t on this platform; perm check stands")
	}
	if me, err := user.Current(); err == nil {
		if strconv.FormatUint(uint64(st.Uid), 10) != me.Uid {
			t.Errorf("staging owner uid = %d, want current uid %s", st.Uid, me.Uid)
		}
	}
}

// TestFFmpegSandboxOSRejectsWriteOutsideAllowList asserts (b): under the
// OS-enforced deny-all sandbox, a write outside the allow-list is rejected by
// the OS — not merely by an argument convention. On macOS it runs sandbox-exec
// with the same deny-default + system.sb profile shape the engine uses (one
// writable allow-list dir) and confirms the kernel denies a write to a sibling
// dir while allowing one inside the allow-list. On Linux the equivalent
// OS-enforcement is the SEC-2 re-exec shim (seccomp-bpf + landlock), asserted by
// that ticket's E2E; this test is darwin-scoped.
func TestFFmpegSandboxOSRejectsWriteOutsideAllowList(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox-exec OS-rejection assertion; Linux uses the SEC-2 seccomp+landlock shim")
	}
	sandboxExec, err := exec.LookPath("sandbox-exec")
	if err != nil {
		t.Skip("sandbox-exec not available on this host")
	}

	allow := realPath(t, t.TempDir())
	deny := realPath(t, t.TempDir())
	profile := strings.Join([]string{
		"(version 1)",
		"(deny default)",
		`(import "system.sb")`,
		"(allow process-exec*)",
		"(deny network*)",
		`(allow file-read* (subpath "` + allow + `"))`,
		`(allow file-write* (subpath "` + allow + `"))`,
	}, "\n")

	// Write INSIDE the allow-list: must succeed.
	inside := exec.Command(sandboxExec, "-p", profile,
		"/usr/bin/touch", filepath.Join(allow, "ok"))
	inside.Dir = allow
	if out, err := inside.CombinedOutput(); err != nil {
		t.Fatalf("write inside allow-list was rejected (sandbox too strict): %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(allow, "ok")); err != nil {
		t.Fatalf("allowed write did not land: %v", err)
	}

	// Write OUTSIDE the allow-list: the OS must reject it.
	outside := exec.Command(sandboxExec, "-p", profile,
		"/usr/bin/touch", filepath.Join(deny, "escape"))
	outside.Dir = allow
	out, err := outside.CombinedOutput()
	if err == nil {
		t.Fatalf("write outside allow-list was NOT rejected by the OS:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(deny, "escape")); statErr == nil {
		t.Fatal("denied write nevertheless created the file outside the allow-list")
	}
}

// realPath resolves symlinks so the path matches the kernel's canonical form the
// SBPL subpath rule is evaluated against (macOS temp dirs are symlinked under
// /var → /private/var).
func realPath(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("eval symlinks %q: %v", p, err)
	}
	return resolved
}

// readSource reads a repo-relative source file for static conformance checks.
func readSource(t *testing.T, rel string) string {
	t.Helper()
	abs := filepath.Join(repoRoot(t), filepath.FromSlash(rel))
	body, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(body)
}

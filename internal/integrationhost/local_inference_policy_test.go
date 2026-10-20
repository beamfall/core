package integrationhost

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicyRefusesDeniedHostRoots(t *testing.T) {
	roots := policyRoots(t)
	checked := 0
	for _, denied := range []string{"/etc", "/dev", "/proc", "/sys", "/"} {
		if info, err := os.Stat(denied); err != nil || !info.IsDir() {
			continue // not a tree on this host; the entry still guards hosts that have it
		}
		checked++
		if _, err := BuildLocalInferencePolicy(denied, roots.model, roots.work, roots.binary); !errors.Is(err, ErrLocalInferenceRootEscape) {
			t.Fatalf("input root %q error = %v, want ErrLocalInferenceRootEscape", denied, err)
		}
	}
	if checked == 0 {
		t.Fatal("no denied host tree existed to assert against")
	}
}

func TestPolicyRefusesSymlinkedRootEscape(t *testing.T) {
	roots := policyRoots(t)
	link := filepath.Join(t.TempDir(), "input")
	if err := os.Symlink("/etc", link); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildLocalInferencePolicy(link, roots.model, roots.work, roots.binary); !errors.Is(err, ErrLocalInferenceRootEscape) {
		t.Fatalf("symlinked escape error = %v, want ErrLocalInferenceRootEscape", err)
	}
}

func TestPolicyRecordsResolvedRootsNotDeclaredLinks(t *testing.T) {
	roots := policyRoots(t)
	link := filepath.Join(t.TempDir(), "input-link")
	if err := os.Symlink(roots.input, link); err != nil {
		t.Fatal(err)
	}
	policy, err := BuildLocalInferencePolicy(link, roots.model, roots.work, roots.binary)
	if err != nil {
		t.Fatal(err)
	}
	if policy.InputRoot == link {
		t.Fatalf("policy kept the declared link %q instead of its resolved target", link)
	}
	if policy.InputRoot != realTestPath(t, roots.input) {
		t.Fatalf("resolved input root = %q, want %q", policy.InputRoot, realTestPath(t, roots.input))
	}
}

func TestPolicyRefusesNestedRoots(t *testing.T) {
	roots := policyRoots(t)
	nested := filepath.Join(roots.work, "model")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildLocalInferencePolicy(roots.input, nested, roots.work, roots.binary); !errors.Is(err, ErrLocalInferenceRootEscape) {
		t.Fatalf("model root inside the work root error = %v, want ErrLocalInferenceRootEscape", err)
	}
	if _, err := BuildLocalInferencePolicy(roots.model, roots.model, roots.work, roots.binary); !errors.Is(err, ErrLocalInferenceRootEscape) {
		t.Fatalf("duplicate root error = %v, want ErrLocalInferenceRootEscape", err)
	}
}

func TestPolicyRefusesBinaryInsideWorkRoot(t *testing.T) {
	roots := policyRoots(t)
	binary := filepath.Join(roots.work, "infer")
	// #nosec G306 -- resolveConfinedBinary refuses a target with no execute
	// bit, so 0700 is the minimum that reaches the root-separation check this
	// test asserts; the file is private to the test's own t.TempDir().
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildLocalInferencePolicy(roots.input, roots.model, roots.work, binary); !errors.Is(err, ErrLocalInferenceRootEscape) {
		t.Fatalf("binary inside the writable work root error = %v, want ErrLocalInferenceRootEscape", err)
	}
}

func TestPolicyRequiresExecutableRegularBinary(t *testing.T) {
	roots := policyRoots(t)
	plain := filepath.Join(t.TempDir(), "infer")
	if err := os.WriteFile(plain, []byte("weights"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildLocalInferencePolicy(roots.input, roots.model, roots.work, plain); err == nil {
		t.Fatal("non-executable binary accepted")
	}
	if _, err := BuildLocalInferencePolicy(roots.input, roots.model, roots.work, roots.work); err == nil {
		t.Fatal("directory accepted as the exec target")
	}
}

func TestPolicyReachDeniesConfigurationAndCredentialPaths(t *testing.T) {
	roots := policyRoots(t)
	policy, err := BuildLocalInferencePolicy(roots.input, roots.model, roots.work, roots.binary)
	if err != nil {
		t.Fatal(err)
	}
	// An unset HOME would turn the two per-user cases into relative paths, and
	// the assertion would degrade to trivially true without saying so. Skip
	// loudly instead, so the coverage loss is visible in the test log.
	home := os.Getenv("HOME")
	if !filepath.IsAbs(home) {
		t.Skipf("HOME = %q is not an absolute path; the per-user cases cannot be built", home)
	}
	denied := []string{
		"/etc/hosts", "/etc/passwd", "/etc/master.passwd", "/etc/shadow",
		"/etc/ssl/private/server.key", "/proc/self/environ", "/dev/mem",
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".aws", "credentials"),
	}
	for _, path := range denied {
		if reach := policy.Reach(path); reach != ReachNone {
			t.Fatalf("policy reaches %q with %q, want %q", path, reach, ReachNone)
		}
	}
	granted := map[string]string{
		filepath.Join(roots.input, "media.bin"):   ReachRead,
		filepath.Join(roots.model, "weights.bin"): ReachRead,
		filepath.Join(roots.work, "labels.json"):  ReachReadWrite,
		roots.binary:                              ReachExecute,
	}
	for path, want := range granted {
		if reach := policy.Reach(path); reach != want {
			t.Fatalf("policy reach for %q = %q, want %q", path, reach, want)
		}
	}
}

func TestPolicyLoaderDirsCarryNoConfigurationTrees(t *testing.T) {
	for _, dir := range LocalInferenceLoaderDirs() {
		if strings.HasPrefix(dir, "/etc") || strings.HasPrefix(dir, "/dev") {
			t.Fatalf("loader inventory grants the host tree %q", dir)
		}
	}
}

type policyFixture struct{ input, model, work, binary string }

func policyRoots(t *testing.T) policyFixture {
	t.Helper()
	req := localInferenceRequest(t)
	return policyFixture{input: req.InputRoot, model: req.ModelRoot, work: req.WorkRoot, binary: req.Binary}
}

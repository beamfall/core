package integrationhost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Reach values name the access a confined local inference child has to one
// host path. Everything the policy does not name resolves to ReachNone, which
// is the deny-all posture ADR-0127 §1 requires of the v1 CPU-only profile.
const (
	ReachNone      = "none"
	ReachRead      = "read"
	ReachReadWrite = "read-write"
	ReachExecute   = "read-execute"
)

// deniedHostRoots are host trees a declared local inference root may never
// resolve into. A root that lands here is refused at policy-build time, so a
// mis-declared or symlinked root becomes a launch refusal instead of a silent
// grant over host configuration, credentials, or device nodes.
var deniedHostRoots = []string{"/etc", "/proc", "/sys", "/dev", "/boot", "/root", "/var/run"}

// loaderDirCandidates are the only directories outside the three declared roots
// that a policy ever grants, and only as read+execute: the host dynamic loader
// must reach the inference binary's shared objects for the child to start at
// all. Host configuration (/etc), device nodes, and certificate/font trees are
// deliberately absent — the v1 profile is CPU-only and has no network, so it
// needs none of them.
var loaderDirCandidates = []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64", "/usr/local/lib"}

// LocalInferencePolicy is the complete filesystem inventory of one local
// inference child: the three declared roots (input and model read-only, work
// read-write), the exec target itself, and the loader directories that exist on
// this host. It is computed before any confinement is applied so the launch can
// be refused rather than narrowed when the inventory is not satisfiable.
type LocalInferencePolicy struct {
	InputRoot  string
	ModelRoot  string
	WorkRoot   string
	Binary     string
	LoaderDirs []string
}

// ErrLocalInferenceRootEscape reports a declared root that resolves outside the
// least-privilege inventory: into a denied host tree, or into another root.
var ErrLocalInferenceRootEscape = errors.New("integrationhost: local inference root escape")

// BuildLocalInferencePolicy resolves and validates the declared roots into the
// confinement inventory. Every root is symlink-resolved first, so the policy
// names real inodes and a root pointing at a denied tree is rejected on its
// resolved target rather than its declared spelling. It is the single gate both
// the parent launcher and the hardening shim compute their grants from.
func BuildLocalInferencePolicy(inputRoot, modelRoot, workRoot, binary string) (LocalInferencePolicy, error) {
	if err := checkDeclaredPaths(inputRoot, modelRoot, workRoot, binary); err != nil {
		return LocalInferencePolicy{}, err
	}
	input, err := resolveConfinedRoot("input root", inputRoot)
	if err != nil {
		return LocalInferencePolicy{}, err
	}
	model, err := resolveConfinedRoot("model root", modelRoot)
	if err != nil {
		return LocalInferencePolicy{}, err
	}
	if err := CheckLocalInferenceModelRoot(model); err != nil {
		return LocalInferencePolicy{}, err
	}
	work, err := resolveConfinedRoot("work root", workRoot)
	if err != nil {
		return LocalInferencePolicy{}, err
	}
	binaryPath, err := resolveConfinedBinary(binary)
	if err != nil {
		return LocalInferencePolicy{}, err
	}
	if err := checkRootSeparation(input, model, work, binaryPath); err != nil {
		return LocalInferencePolicy{}, err
	}
	return LocalInferencePolicy{
		InputRoot:  input,
		ModelRoot:  model,
		WorkRoot:   work,
		Binary:     binaryPath,
		LoaderDirs: existingDirs(loaderDirCandidates),
	}, nil
}

// checkDeclaredPaths requires every declared path before any filesystem work.
func checkDeclaredPaths(inputRoot, modelRoot, workRoot, binary string) error {
	for _, declared := range []struct{ name, path string }{
		{"input root", inputRoot},
		{"model root", modelRoot},
		{"work root", workRoot},
		{"binary", binary},
	} {
		if declared.path == "" {
			return fmt.Errorf("integrationhost: local inference %s required", declared.name)
		}
		if !filepath.IsAbs(declared.path) {
			return fmt.Errorf("integrationhost: local inference %s must be absolute", declared.name)
		}
	}
	return nil
}

// LocalInferenceLoaderDirs are the read+execute directories that exist on this
// host. The kernel-enforcement layer reads them through this accessor so the
// inventory has exactly one definition on every platform.
func LocalInferenceLoaderDirs() []string { return existingDirs(loaderDirCandidates) }

// ReadOnlyPaths are the grants the child receives with read access only.
func (p LocalInferencePolicy) ReadOnlyPaths() []string {
	return []string{p.InputRoot, p.ModelRoot}
}

// ReadWritePaths are the grants the child may also write.
func (p LocalInferencePolicy) ReadWritePaths() []string { return []string{p.WorkRoot} }

// Reach reports the access the confined child has to path. It is the policy's
// own account of its blast radius, so a test can assert that host configuration
// and credential paths are unreachable without executing the child.
func (p LocalInferencePolicy) Reach(path string) string {
	resolved := resolveExisting(path)
	switch {
	case pathWithin(p.WorkRoot, resolved):
		return ReachReadWrite
	case pathWithin(p.InputRoot, resolved), pathWithin(p.ModelRoot, resolved):
		return ReachRead
	case resolved == p.Binary, withinAny(p.LoaderDirs, resolved):
		return ReachExecute
	}
	return ReachNone
}

// resolveConfinedRoot resolves one declared root to a real directory and
// refuses it when it lands in a denied host tree.
func resolveConfinedRoot(name, root string) (string, error) {
	resolved, err := realSandboxPath(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("integrationhost: local inference %s: %w", name, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("integrationhost: local inference %s %q is not a directory", name, resolved)
	}
	if err := checkDeniedHostRoot(name, root, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// checkDeniedHostRoot refuses a root that is, contains, or sits inside a denied
// host tree. Both the declared and the resolved spelling are checked, and the
// denied trees are themselves symlink-resolved, so a platform that reaches /etc
// through a link cannot be used to sidestep the list.
func checkDeniedHostRoot(name, declared, resolved string) error {
	if resolved == string(filepath.Separator) {
		return fmt.Errorf("%w: %s may not be the filesystem root", ErrLocalInferenceRootEscape, name)
	}
	for _, denied := range deniedHostTrees() {
		for _, candidate := range []string{filepath.Clean(declared), resolved} {
			if pathWithin(denied, candidate) || pathWithin(candidate, denied) {
				return fmt.Errorf("%w: %s %q resolves into the denied host tree %q",
					ErrLocalInferenceRootEscape, name, resolved, denied)
			}
		}
	}
	return nil
}

// deniedHostTrees expands the denied list with each entry's resolved target so
// the check is exact on hosts where these trees are links.
func deniedHostTrees() []string {
	trees := make([]string, 0, len(deniedHostRoots)*2)
	for _, tree := range deniedHostRoots {
		trees = append(trees, tree)
		if resolved, err := filepath.EvalSymlinks(tree); err == nil && resolved != tree {
			trees = append(trees, resolved)
		}
	}
	return trees
}

// resolveConfinedBinary resolves the exec target and requires a real executable
// file, so the policy grants execute on one inode rather than on a directory.
func resolveConfinedBinary(binary string) (string, error) {
	resolved, err := realSandboxPath(binary)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("integrationhost: local inference binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("integrationhost: local inference binary %q is not a regular file", resolved)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("integrationhost: local inference binary %q is not executable", resolved)
	}
	return resolved, nil
}

// checkRootSeparation refuses any nesting that would widen a grant: a read-only
// root inside the write root becomes writable, a write root inside a read-only
// root punches a hole in it, and an exec target inside the write root is a
// binary the child could rewrite between runs.
func checkRootSeparation(input, model, work, binary string) error {
	roots := map[string]string{"input root": input, "model root": model, "work root": work}
	for nameA, a := range roots {
		for nameB, b := range roots {
			if nameA == nameB {
				continue
			}
			if pathWithin(a, b) {
				return fmt.Errorf("%w: %s %q lies inside %s %q", ErrLocalInferenceRootEscape, nameB, b, nameA, a)
			}
		}
	}
	if pathWithin(work, binary) {
		return fmt.Errorf("%w: binary %q lies inside the writable work root", ErrLocalInferenceRootEscape, binary)
	}
	return nil
}

// existingDirs keeps the candidates that are real directories on this host.
func existingDirs(candidates []string) []string {
	dirs := make([]string, 0, len(candidates))
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// resolveExisting resolves path when it exists and otherwise resolves the
// deepest existing ancestor, so a not-yet-created file under a symlinked root
// is still judged against the real tree.
func resolveExisting(path string) string {
	clean := filepath.Clean(path)
	for dir := clean; ; dir = filepath.Dir(dir) {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, strings.TrimPrefix(clean, dir))
		}
		if dir == filepath.Dir(dir) {
			return clean
		}
	}
}

// pathWithin reports whether path is parent itself or lies beneath it.
func pathWithin(parent, path string) bool {
	if parent == "" {
		return false
	}
	if parent == path {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(parent, string(filepath.Separator))+string(filepath.Separator))
}

func withinAny(parents []string, path string) bool {
	for _, parent := range parents {
		if pathWithin(parent, path) {
			return true
		}
	}
	return false
}

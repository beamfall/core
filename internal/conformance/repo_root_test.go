package conformance

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot resolves the repository root from this source file's location
// (internal/conformance is two directories below the root), so the
// conformance walkers scan real paths regardless of the test working
// directory. A missing expected root is a test failure, never a skip — that
// is what made the gates vacuous before.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	return root
}

// requireRoot returns the absolute path for a scan root and fails the test if
// it does not exist.
func requireRoot(t *testing.T, root string) string {
	t.Helper()
	abs := filepath.Join(repoRoot(t), filepath.FromSlash(root))
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("expected scan root %q is missing: %v", root, err)
	}
	return abs
}

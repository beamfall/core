package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The test binary re-invokes main so the flag wiring itself is exercised —
// a unit test on a extracted predicate would keep passing if main stopped
// calling it.
func TestMain(m *testing.M) {
	if os.Getenv("WIRE_CONTRACT_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// --refresh-current promises the emitted bytes are the validated same-version
// contract; --inventory swaps the payload after validation. The combination
// must be rejected, or a refresh can exit 0 while emitting non-contract bytes.
func TestRefreshCurrentRejectsInventoryCombination(t *testing.T) {
	// os.Args[0] is the test binary re-invoking itself (WIRE_CONTRACT_MAIN
	// gate above), not external input — the standard self-exec CLI test shape.
	cmd := exec.Command(os.Args[0], //nolint:gosec // G204: self-exec of the test binary, no tainted input
		"-refresh-current", "-inventory",
		"-registry", "does-not-matter.json", "-version", "1.0.0")
	cmd.Env = append(os.Environ(), "WIRE_CONTRACT_MAIN=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got success with output: %s", out)
	}
	if !strings.Contains(string(out), "--refresh-current is incompatible with --inventory") {
		t.Fatalf("expected the incompatibility rejection, got: %s", out)
	}
}

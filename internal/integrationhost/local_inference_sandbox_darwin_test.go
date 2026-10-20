//go:build darwin

package integrationhost

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The tests in this file are the darwin half of the denial evidence. They do
// not read the generated profile text: each one launches a real child under the
// real generated sandbox-exec argv and requires the OS to refuse the attempt.
// Every case carries an unconfined control that performs the identical attempt
// successfully, so a refusal can only come from the profile — never from a
// missing file, a wrong port, or a child that failed to start.

// TestLocalInferenceSandboxRefusesHostReadsOutsideDeclaredRoots is the executed
// counterpart of the Reach model's ReachNone claim over host configuration and
// operator material. The positive minimum-permission run uses /bin/echo, which
// opens nothing, so it cannot cover this.
func TestLocalInferenceSandboxRefusesHostReadsOutsideDeclaredRoots(t *testing.T) {
	requireSandboxExec(t)

	offRoot := filepath.Join(t.TempDir(), "operator-material.bin")
	if err := os.WriteFile(offRoot, []byte("opaque fixture bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	probes := unconfinedReadablePaths(t, "/etc/hosts", "/etc/passwd", offRoot)
	if len(probes) < 2 {
		t.Skipf("this host offers too few readable off-root paths to assert against: %q", probes)
	}

	for _, path := range probes {
		out, err := runConfined(t, "/bin/cat", path)
		if err == nil {
			t.Fatalf("confined child read the off-root path %q: %s", path, out)
		}
	}
}

// TestLocalInferenceSandboxRefusesNetworkSockets requires an observed socket
// refusal rather than the presence of (deny network*) in the generated argv.
// The child connects to a listener this test owns, so the unconfined control is
// guaranteed to succeed and the confined attempt has a live peer to reach.
func TestLocalInferenceSandboxRefusesNetworkSockets(t *testing.T) {
	requireSandboxExec(t)

	const dialer = "/usr/bin/nc"
	if _, err := os.Stat(dialer); err != nil {
		t.Skipf("no loopback dialer at %s on this host: %v", dialer, err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	dialArgs := []string{"-z", "-w", "2", "127.0.0.1", port}

	// #nosec G204 -- dialer is the file-scoped constant /usr/bin/nc and dialArgs
	// are this test's own literals plus the port of a listener it opened.
	if out, err := exec.Command(dialer, dialArgs...).CombinedOutput(); err != nil {
		t.Skipf("unconfined loopback connect failed on this host: %v %s", err, out)
	}
	// The same binary must still start under the profile, so a refusal below is
	// the socket being denied and not a child that never ran.
	if out, _ := runConfined(t, dialer, "-h"); !strings.Contains(string(out), "usage:") {
		t.Fatalf("confined dialer did not start; output was %q", out)
	}

	out, err := runConfined(t, dialer, dialArgs...)
	if err == nil {
		t.Fatalf("confined child opened a loopback socket to 127.0.0.1:%s: %s", port, out)
	}
}

// runConfined launches binary under the profile the production launcher
// generates, by routing through LocalInferenceRuntime.Command rather than
// rebuilding the argv here.
func runConfined(t *testing.T, binary string, args ...string) ([]byte, error) {
	t.Helper()
	req := localInferenceRequest(t)
	req.Binary = binary
	req.Args = args
	cmd, err := LocalInferenceRuntime{}.Command(context.Background(), req)
	if err != nil {
		t.Fatalf("Command for %q: %v", binary, err)
	}
	if cmd.Path != LocalInferenceSandboxExecPath {
		t.Fatalf("confined command path = %q, want %q", cmd.Path, LocalInferenceSandboxExecPath)
	}
	return cmd.CombinedOutput()
}

// unconfinedReadablePaths keeps the candidates an unconfined child can read, so
// each confined refusal is asserted against a proven-reachable control.
func unconfinedReadablePaths(t *testing.T, candidates ...string) []string {
	t.Helper()
	readable := make([]string, 0, len(candidates))
	for _, path := range candidates {
		// #nosec G204 -- the binary is the literal /bin/cat and path comes from
		// this test's own candidate list (/etc probes plus a t.TempDir() file).
		if err := exec.Command("/bin/cat", path).Run(); err == nil {
			readable = append(readable, path)
		}
	}
	return readable
}

func requireSandboxExec(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(LocalInferenceSandboxExecPath); err != nil {
		t.Skipf("no confinement binary at %s on this host: %v", LocalInferenceSandboxExecPath, err)
	}
}

// TestLocalInferenceSandboxExecIsNotResolvedFromPATH fences the confinement
// binary to its fixed system location. A $PATH entry ahead of /usr/bin that
// carries an executable named sandbox-exec must not become what confines the
// child: if the launcher ever goes back to exec.LookPath, the decoy planted
// here wins and this test fails.
func TestLocalInferenceSandboxExecIsNotResolvedFromPATH(t *testing.T) {
	requireSandboxExec(t)

	decoyDir := t.TempDir()
	decoy := filepath.Join(decoyDir, "sandbox-exec")
	// #nosec G306 -- the decoy must carry an execute bit to be a credible
	// exec.LookPath winner; it is a private file under this test's t.TempDir().
	if err := os.WriteFile(decoy, []byte("#!/bin/sh\nexec \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", decoyDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if found, err := exec.LookPath("sandbox-exec"); err != nil || found != decoy {
		t.Fatalf("decoy did not win the $PATH lookup (found %q, err %v); the test cannot prove anything", found, err)
	}

	cmd, err := LocalInferenceRuntime{}.Command(context.Background(), localInferenceRequest(t))
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if cmd.Path != LocalInferenceSandboxExecPath {
		t.Fatalf("confined command path = %q, want the fixed %q", cmd.Path, LocalInferenceSandboxExecPath)
	}
}

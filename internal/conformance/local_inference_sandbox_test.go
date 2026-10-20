package conformance

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/integrationhost"
)

func TestLocalInferenceSandboxProfileOSRejectsEscapes(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox-exec OS-rejection assertion; Linux uses the local-inference-shim seccomp+landlock path")
	}
	sandboxExec, err := exec.LookPath("sandbox-exec")
	if err != nil {
		t.Skip("sandbox-exec not available on this host")
	}

	input := realPath(t, t.TempDir())
	model := realPath(t, t.TempDir())
	work := realPath(t, t.TempDir())
	deny := realPath(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(input, "media.bin"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(model, "weights.bin"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deny, "secret"), []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile, err := integrationhost.LocalInferenceSandboxProfile(input, model, work)
	if err != nil {
		t.Fatal(err)
	}

	allowed := exec.Command(sandboxExec, "-p", profile, "/bin/cat", filepath.Join(input, "media.bin"))
	allowed.Dir = work
	if out, err := allowed.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("allowed input read failed: %v\n%s", err, out)
	}
	allowed = exec.Command(sandboxExec, "-p", profile, "/usr/bin/touch", filepath.Join(work, "ok"))
	allowed.Dir = work
	if out, err := allowed.CombinedOutput(); err != nil {
		t.Fatalf("allowed work write failed: %v\n%s", err, out)
	}
	deniedRead := exec.Command(sandboxExec, "-p", profile, "/bin/cat", filepath.Join(deny, "secret"))
	deniedRead.Dir = work
	if out, err := deniedRead.CombinedOutput(); err == nil {
		t.Fatalf("read outside the three local-inference roots was not OS-rejected:\n%s", out)
	}
	deniedWrite := exec.Command(sandboxExec, "-p", profile, "/usr/bin/touch", filepath.Join(model, "escape"))
	deniedWrite.Dir = work
	if out, err := deniedWrite.CombinedOutput(); err == nil {
		t.Fatalf("write outside local-inference work root was not OS-rejected:\n%s", out)
	}
}

func TestLocalInferenceSandboxContractIsCPUOnlyNoNetwork(t *testing.T) {
	src := readSource(t, filepath.Join("internal", "integrationhost", "local_inference.go")) +
		readSource(t, filepath.Join("cmd", "beamfall", "local_inference_shim.go")) +
		readSource(t, filepath.Join("cmd", "beamfall", "no_network_linux.go"))
	for _, want := range []string{"(deny network*)", "applyNoNetwork()", "applyConfinement([]string{inputRoot, modelRoot}, []string{workRoot}"} {
		if !strings.Contains(src, want) {
			t.Fatalf("local inference sandbox contract missing %q", want)
		}
	}
	for _, forbidden := range []string{"/dev/dri", "/dev/kfd", "cuda", "metal", "iokit"} {
		if strings.Contains(strings.ToLower(src), forbidden) {
			t.Fatalf("local inference v1 must not grant GPU/device-node access: found %q", forbidden)
		}
	}
}

package capabilities

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const capabilitiesPkg = "github.com/beamfall/core/internal/capabilities"

// TestReleaseIdentityLinkTime (CORE-1096): the link-time ReleaseVersion and
// ReleaseCommit reach Current(), which `beamfall version` prints, and a binary
// linked without them and without VCS metadata (a Docker build context has no
// .git) reports "development" for both.
func TestReleaseIdentityLinkTime(t *testing.T) {
	if testing.Short() {
		t.Skip("links a helper binary")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	build := func(t *testing.T, ldflags string) string {
		t.Helper()
		out := filepath.Join(t.TempDir(), "printidentity")
		cmd := exec.Command(goBin, "build", "-buildvcs=false", "-trimpath", "-ldflags="+ldflags, "-o", out, "./testdata/printidentity") //nolint:gosec // G204: the test's own toolchain and fixed flags
		cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build: %v\n%s", err, msg)
		}
		got, err := exec.Command(out).Output() //nolint:gosec // G204: the helper binary this test just built
		if err != nil {
			t.Fatalf("run printidentity: %v", err)
		}
		return string(got)
	}

	const commit = "0123456789abcdef0123456789abcdef01234567"
	stamped := build(t, "-X "+capabilitiesPkg+".ReleaseVersion=v1.0.0 -X "+capabilitiesPkg+".ReleaseCommit="+commit)
	if want := "v1.0.0\n" + commit + "\n"; stamped != want {
		t.Fatalf("stamped identity = %q, want %q", stamped, want)
	}
	unstamped := build(t, "-X "+capabilitiesPkg+".ReleaseCommit=")
	if want := "development\ndevelopment\n"; unstamped != want {
		t.Fatalf("unstamped identity = %q, want %q", unstamped, want)
	}
}

// TestDockerfileStampsBuildIdentity (CORE-1096): the repo Dockerfile links
// BEAMFALL_VERSION and BEAMFALL_COMMIT into the binary, labels the image with
// them, and exports the commit as /health's buildSHA. Without the build args
// the version is a source-derived development label, never a release number.
func TestDockerfileStampsBuildIdentity(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := strings.ReplaceAll(string(raw), "\\\n", " ")
	buildStage, runtimeStage, ok := strings.Cut(dockerfile, " AS runtime")
	if !ok {
		t.Fatal("Dockerfile has no runtime stage")
	}
	for _, want := range []string{
		`ARG BEAMFALL_VERSION=""`,
		`ARG BEAMFALL_COMMIT=""`,
		"-X " + capabilitiesPkg + ".ReleaseVersion=${version}",
		"-X " + capabilitiesPkg + ".ReleaseCommit=${BEAMFALL_COMMIT}",
	} {
		if !strings.Contains(buildStage, want) {
			t.Errorf("build stage lacks %q", want)
		}
	}
	for _, want := range []string{
		`ARG BEAMFALL_VERSION=""`,
		`ARG BEAMFALL_COMMIT=""`,
		`org.opencontainers.image.version="${BEAMFALL_VERSION:-development}"`,
		`org.opencontainers.image.revision="${BEAMFALL_COMMIT}"`,
		"BEAMFALL_BUILD_SHA=${BEAMFALL_COMMIT}",
	} {
		if !strings.Contains(runtimeStage, want) {
			t.Errorf("runtime stage lacks %q", want)
		}
	}

	// Evaluate the build stage's version expression the way its RUN does.
	match := regexp.MustCompile(`version="([^"]+)";`).FindStringSubmatch(buildStage)
	if match == nil {
		t.Fatal("build stage has no version= assignment")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source-id"), []byte("abcdef012345\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	expr := strings.ReplaceAll(match[1], "/out/source-id", filepath.Join(dir, "source-id"))
	eval := func(version string) string {
		cmd := exec.Command("sh", "-c", `printf %s "`+expr+`"`) //nolint:gosec // G204: evaluates the repo Dockerfile's own version expression
		cmd.Env = append(os.Environ(), "BEAMFALL_VERSION="+version)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("evaluate %q: %v", expr, err)
		}
		return string(out)
	}
	if got := eval("v1.0.0"); got != "v1.0.0" {
		t.Errorf("stamped version = %q, want v1.0.0", got)
	}
	got := eval("")
	if got != "development+src.abcdef012345" {
		t.Errorf("unstamped version = %q, want development+src.abcdef012345", got)
	}
	if _, isRelease := parseNumericVersion(got); isRelease {
		t.Errorf("unstamped version %q parses as a release number", got)
	}
}

// parseNumericVersion mirrors internal/updatecheck: a leading numeric
// component is what makes a version comparable as a release.
func parseNumericVersion(v string) (string, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	return v, regexp.MustCompile(`^[0-9]+(\.[0-9]+)*([-+].*)?$`).MatchString(v)
}

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

	// CORE-1395: a non-empty first-party catalog is validated before the
	// cross-compile links it, so a malformed value fails the image build.
	validate := `go run ./cmd/beamfall release build-catalog --catalog "${BEAMFALL_FIRST_PARTY_BUILDS}"`
	link := capabilitiesPkg + ".FirstPartyBuildManifest=${BEAMFALL_FIRST_PARTY_BUILDS}"
	if v, l := strings.Index(buildStage, validate), strings.Index(buildStage, link); v < 0 || l < 0 || v > l {
		t.Errorf("build stage must validate BEAMFALL_FIRST_PARTY_BUILDS (%d) before linking it (%d)", v, l)
	}
	if !strings.Contains(buildStage, `if [ -n "${BEAMFALL_FIRST_PARTY_BUILDS}" ]; then`) {
		t.Error("catalog validation is not skipped for an unset value")
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

// TestReleasePathsPassFirstPartyBuilds (CORE-1395): every Core release path
// links the first-party build catalog. release.yml hands release.sh the
// manifest and the image build the validated catalog, and refuses to run
// without one; docker-bake forwards the catalog to the Dockerfile.
func TestReleasePathsPassFirstPartyBuilds(t *testing.T) {
	read := func(parts ...string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	bake := read("docker-bake.hcl")
	if !regexp.MustCompile(`(?m)^\s*BEAMFALL_FIRST_PARTY_BUILDS\s*=\s*FIRST_PARTY_BUILDS_CATALOG\s*$`).MatchString(bake) {
		t.Error("docker-bake.hcl does not pass FIRST_PARTY_BUILDS_CATALOG as BEAMFALL_FIRST_PARTY_BUILDS")
	}
	workflow := read(".github", "workflows", "release.yml")
	for _, want := range []string{
		"FIRST_PARTY_BUILDS_MANIFEST: ${{ vars.FIRST_PARTY_BUILDS_MANIFEST }}",
		`if [ -z "${FIRST_PARTY_BUILDS_MANIFEST}" ]; then`,
		"release build-catalog --manifest /tmp/first-party-builds.json",
		"FIRST_PARTY_BUILDS: /tmp/first-party-builds.json",
		`if [ -z "${FIRST_PARTY_BUILDS_CATALOG}" ]; then`,
		"FIRST_PARTY_BUILDS_CATALOG: ${{ needs.attest.outputs.first_party_builds_catalog }}",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("release.yml is missing %q", want)
		}
	}
	// The manifest reaches release.sh before it runs, not after.
	if m, r := strings.Index(workflow, "FIRST_PARTY_BUILDS: /tmp/first-party-builds.json"), strings.Index(workflow, "script/release.sh"); m < 0 || r < 0 || m > r {
		t.Error("release.yml must set FIRST_PARTY_BUILDS on the step that runs script/release.sh")
	}
}

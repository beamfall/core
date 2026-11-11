package capabilities

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CORE-1096: a link-time commit stamp wins over missing VCS build info.
func TestCommitPrefersLinkTimeStamp(t *testing.T) {
	prev := ReleaseCommit
	t.Cleanup(func() { ReleaseCommit = prev })
	ReleaseCommit = "0123456789abcdef0123456789abcdef01234567"
	if got := Commit(); got != ReleaseCommit {
		t.Fatalf("Commit() = %q, want stamped %q", got, ReleaseCommit)
	}
	if got := Current().Commit; got != ReleaseCommit {
		t.Fatalf("Current().Commit = %q, want stamped %q", got, ReleaseCommit)
	}
	ReleaseCommit = "  "
	if got := Commit(); got == "  " || got == "" {
		t.Fatalf("blank stamp must fall back, got %q", got)
	}
}

// CORE-1096: the container build context has no .git, so the Dockerfile must
// stamp version and commit through build args into -ldflags and the OCI
// labels, and an unstamped build must say so instead of passing for a release.
func TestDockerfileStampsReleaseIdentity(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(raw)
	for _, want := range []string{
		"ARG BEAMFALL_VERSION=development-unstamped",
		"ARG BEAMFALL_COMMIT=",
		"-X github.com/beamfall/core/internal/capabilities.ReleaseVersion=${BEAMFALL_VERSION}",
		"-X github.com/beamfall/core/internal/capabilities.ReleaseCommit=${BEAMFALL_COMMIT}",
		`org.opencontainers.image.version="${BEAMFALL_VERSION}"`,
		`org.opencontainers.image.revision="${BEAMFALL_COMMIT:-unstamped}"`,
	} {
		if !strings.Contains(dockerfile, want) {
			t.Errorf("Dockerfile missing %q", want)
		}
	}
}

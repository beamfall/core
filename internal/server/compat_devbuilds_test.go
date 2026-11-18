package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/store"
)

// devBuildsHarness drives a server whose dev-build file lives in a temp dir.
// It owns the file's clock, so each write is picked up on the next request
// without waiting out devBuildsRecheckInterval.
type devBuildsHarness struct {
	t     *testing.T
	srv   *Server
	path  string
	mu    sync.Mutex
	clock time.Time
	ticks int
}

func newDevBuildsHarness(t *testing.T, release ...nativeBuild) *devBuildsHarness {
	t.Helper()
	linkNativeBuilds(t, nil, release...)
	path := filepath.Join(t.TempDir(), "dev-builds.json")
	t.Setenv(devBuildsFileEnv, path)
	srv, _ := newTestServerWithStore(t)
	if srv.devBuilds == nil {
		t.Fatal("server ignored " + devBuildsFileEnv)
	}
	h := &devBuildsHarness{t: t, srv: srv, path: path, clock: time.Now().Add(time.Hour)}
	srv.devBuilds.mu.Lock()
	srv.devBuilds.now = h.now
	srv.devBuilds.mu.Unlock()
	return h
}

func (h *devBuildsHarness) now() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.clock
}

// advance moves the file clock past the recheck interval.
func (h *devBuildsHarness) advance() {
	h.mu.Lock()
	h.clock = h.clock.Add(devBuildsRecheckInterval)
	h.mu.Unlock()
}

// write replaces the dev-build file with data and gives it a fresh mtime.
func (h *devBuildsHarness) write(data []byte) {
	h.t.Helper()
	if err := os.WriteFile(h.path, data, 0o600); err != nil {
		h.t.Fatal(err)
	}
	h.ticks++
	stamp := time.Unix(2_000_000+int64(h.ticks), 0)
	if err := os.Chtimes(h.path, stamp, stamp); err != nil {
		h.t.Fatal(err)
	}
	h.advance()
}

func (h *devBuildsHarness) writeBuilds(builds ...nativeBuild) {
	h.t.Helper()
	data, err := json.Marshal(struct {
		Schema string        `json:"schema"`
		Builds []nativeBuild `json:"builds"`
	}{capabilities.FirstPartyBuildManifestSchema, builds})
	if err != nil {
		h.t.Fatal(err)
	}
	h.write(data)
}

func (h *devBuildsHarness) remove() {
	h.t.Helper()
	if err := os.Remove(h.path); err != nil {
		h.t.Fatal(err)
	}
	h.advance()
}

// devViewerBuild is a local debug Android viewer build, as
// client_build_identity.py --dev describes one.
func devViewerBuild(commit string) nativeBuild {
	buildID := "android-viewer-app-1.0-7-dev"
	return nativeBuild{
		BuildID:       buildID,
		Surface:       string(capabilities.SurfaceAndroidViewer),
		ArtifactHash:  compatTestArtifactHash(buildID + "@" + commit),
		Sequence:      7,
		Distributions: []string{"direct"},
		Roles:         []string{string(capabilities.EnrollmentRoleViewer), string(capabilities.EnrollmentRoleManager)},
	}
}

// TestRuntimeDevBuildIsAdmittedWithoutARebuild is the CORE-2396 acceptance:
// a debug build from any commit is refused until the operator writes its
// record into the dev-build file, then enrolls and passes the handshake on
// the running server, as a viewer and as a manager. A contract whose digest
// does not match the record is still refused.
func TestRuntimeDevBuildIsAdmittedWithoutARebuild(t *testing.T) {
	h := newDevBuildsHarness(t, shippedViewerBuild(2))
	dev := devViewerBuild("c0ffee")
	contract := dev.contract()

	before := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &contract)
	assertDeviceBuilds(t, h.srv, before.DeviceID, nil)
	assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, contract, before.authorize))

	h.writeBuilds(dev)

	// The device paired before the record existed obtains its build at handshake.
	assertServable(t, compatHandshakeRecord(t, h.srv, contract, before.authorize))
	for _, role := range []capabilities.EnrollmentRole{capabilities.EnrollmentRoleViewer, capabilities.EnrollmentRoleManager} {
		device := enrollFirstPartyDevice(t, h.srv, role, &contract)
		assertDeviceBuilds(t, h.srv, device.DeviceID, map[store.DeviceSurface]string{store.DeviceSurface(dev.Surface): dev.BuildID})
		assertServable(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))
	}

	tampered := contract
	tampered.ArtifactHash = devViewerBuild("other-commit").ArtifactHash
	stranger := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &tampered)
	assertDeviceBuilds(t, h.srv, stranger.DeviceID, nil)
	assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, tampered, stranger.authorize))

	// The release build is unaffected.
	release := shippedViewerBuild(2).contract()
	viewer := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &release)
	assertServable(t, compatHandshakeRecord(t, h.srv, release, viewer.authorize))
}

// TestRuntimeDevBuildsFailClosed covers revocation: removing the file or
// writing an invalid one drops every dev record on the running server.
func TestRuntimeDevBuildsFailClosed(t *testing.T) {
	h := newDevBuildsHarness(t, shippedViewerBuild(2))
	dev := devViewerBuild("c0ffee")
	contract := dev.contract()
	h.writeBuilds(dev)
	device := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &contract)
	assertServable(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))

	h.write([]byte(`{"schema":"` + capabilities.FirstPartyBuildManifestSchema + `","builds":[{"buildID":"android-viewer-app-1.0-7-dev","surface":"android-viewer","artifactHash":"` + dev.ArtifactHash + `","sequence":7,"distributions":["direct","appStore"],"roles":["viewer"]}]}`))
	assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))

	h.writeBuilds(dev)
	assertServable(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))

	h.remove()
	assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))

	h.writeBuilds(dev)
	h.write([]byte("{not json"))
	assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))
}

// TestRuntimeDevBuildCannotReplaceAReleaseRecord covers a dev record that
// reuses a build id the release linked in: the release record stands, so the
// dev digest is refused and the release digest still verifies.
func TestRuntimeDevBuildCannotReplaceAReleaseRecord(t *testing.T) {
	release := devViewerBuild("release")
	release.Distributions = []string{"direct", "appStore"}
	h := newDevBuildsHarness(t, release)
	impostor := devViewerBuild("impostor")
	h.writeBuilds(impostor)

	contract := impostor.contract()
	device := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &contract)
	assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))

	genuine := release.contract()
	assertServable(t, compatHandshakeRecord(t, h.srv, genuine, device.authorize))
}

// TestRuntimeDevBuildsSurviveAPolicyInstall covers CORE-1396's wholesale
// catalog replace: installing a new policy keeps the operator's dev records.
func TestRuntimeDevBuildsSurviveAPolicyInstall(t *testing.T) {
	h := newDevBuildsHarness(t, shippedViewerBuild(2))
	dev := devViewerBuild("c0ffee")
	contract := dev.contract()
	h.writeBuilds(dev)
	device := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &contract)
	assertServable(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))

	policy, pluginGeneration := h.srv.compat.snapshotInstallInputs()
	h.srv.SetCompatPolicy(policy, "next-revision", pluginGeneration)
	assertServable(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))
}

// TestRefusedHandshakeIsLogged covers the CORE-2295 diagnosis gap: a refused
// handshake logs one line naming the build and the server-local finding.
func TestRefusedHandshakeIsLogged(t *testing.T) {
	h := newDevBuildsHarness(t, shippedViewerBuild(2))
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	contract := devViewerBuild("c0ffee").contract()
	device := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &contract)
	logs.Reset()
	assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))

	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if strings.Contains(line, "compatibility handshake refused") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("refusal log lines = %d, want 1:\n%s", len(lines), logs.String())
	}
	for _, want := range []string{"build=" + contract.BuildID, "finding=", "reason=unknown-build", "artifactHash=" + contract.ArtifactHash} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("refusal log %q lacks %q", lines[0], want)
		}
	}
	if strings.Contains(lines[0], "finding=verified") {
		t.Fatalf("refusal log reports a verified build: %q", lines[0])
	}
}

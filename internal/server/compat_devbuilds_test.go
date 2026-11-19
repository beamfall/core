package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
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
	if strings.Contains(lines[0], contract.ArtifactHash) {
		t.Fatalf("refusal log carries the whole artifact hash: %q", lines[0])
	}
	for _, want := range []string{"build=" + contract.BuildID, "finding=unknown-build", "reason=unknown-build", "artifactHash=" + contract.ArtifactHash[:12]} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("refusal log %q lacks %q", lines[0], want)
		}
	}
}

// manifestStatus is the status of an authorized manifest read, which serves
// only under a current compatibility decision.
func manifestStatus(srv *Server, authorize func(*http.Request)) int {
	req := newJSONRequest(http.MethodGet, "/api/manifest", nil)
	authorize(req)
	return compatServe(srv, req).Code
}

// TestRuntimeDevBuildRevocationNeedsNoHandshake covers revocation of an
// admitted device that never handshakes again: its next request after the
// file is removed, or made unreadable, is no longer served.
func TestRuntimeDevBuildRevocationNeedsNoHandshake(t *testing.T) {
	for name, revoke := range map[string]func(h *devBuildsHarness){
		"removed": func(h *devBuildsHarness) { h.remove() },
		"unreadable": func(h *devBuildsHarness) {
			if err := os.Chmod(h.path, 0); err != nil {
				h.t.Fatal(err)
			}
			if f, err := os.Open(h.path); err == nil {
				_ = f.Close()
				h.t.Skip("file permissions do not restrict this user")
			}
			h.advance()
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newDevBuildsHarness(t, shippedViewerBuild(2))
			dev := devViewerBuild("c0ffee")
			contract := dev.contract()
			h.writeBuilds(dev)
			device := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &contract)
			assertServable(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))
			if code := manifestStatus(h.srv, device.authorize); code != http.StatusOK {
				t.Fatalf("manifest while admitted = %d", code)
			}

			revoke(h)
			if code := manifestStatus(h.srv, device.authorize); code == http.StatusOK {
				t.Fatal("manifest still served after the dev record was revoked")
			}
			assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))
		})
	}
}

// TestRefusedHandshakeLogsOnlyAHashPrefixForAValidBuild covers a cataloged
// build refused for entitlement: its valid digest stays out of the log.
func TestRefusedHandshakeLogsOnlyAHashPrefixForAValidBuild(t *testing.T) {
	h := newDevBuildsHarness(t, shippedViewerBuild(2))
	dev := devViewerBuild("c0ffee")
	dev.Roles = []string{string(capabilities.EnrollmentRoleViewer)}
	h.writeBuilds(dev)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	contract := dev.contract()
	manager := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleManager, &contract)
	assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, contract, manager.authorize))
	if strings.Contains(logs.String(), contract.ArtifactHash) {
		t.Fatalf("logs carry the whole valid artifact hash:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "compatibility handshake refused") {
		t.Fatalf("no refusal line:\n%s", logs.String())
	}
}

// TestRuntimeDevBuildRebuildKeepsItsDevice is the CORE-2573 acceptance: a
// device that verified an operator-admitted dev build keeps passing the
// handshake after a rebuild changes the artifact digest, with no file edit and
// no restart. The refused-then-trusted flow runs on one server instance, and a
// rebuilt digest still never admits a device without that mapping.
func TestRuntimeDevBuildRebuildKeepsItsDevice(t *testing.T) {
	h := newDevBuildsHarness(t, shippedViewerBuild(2))
	dev := devViewerBuild("c0ffee")
	contract := dev.contract()

	// Refused: the operator has not admitted this build yet.
	device := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &contract)
	assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))

	// Trusted on the running server once the operator writes the record.
	h.writeBuilds(dev)
	assertServable(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))
	assertDeviceBuilds(t, h.srv, device.DeviceID, map[store.DeviceSurface]string{store.DeviceSurface(dev.Surface): dev.BuildID})

	// Two rebuilds later the file still names the first digest.
	for _, commit := range []string{"rebuild-1", "rebuild-2"} {
		rebuilt := contract
		rebuilt.ArtifactHash = devViewerBuild(commit).ArtifactHash
		assertServable(t, compatHandshakeRecord(t, h.srv, rebuilt, device.authorize))
	}

	rebuilt := contract
	rebuilt.ArtifactHash = devViewerBuild("rebuild-3").ArtifactHash

	// A device without the mapping cannot use the rebuilt digest, neither at
	// enrollment nor at handshake.
	stranger := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &rebuilt)
	assertDeviceBuilds(t, h.srv, stranger.DeviceID, nil)
	assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, rebuilt, stranger.authorize))
	// The handshake rebind (CORE-1393) ran on that handshake and recorded nothing.
	assertDeviceBuilds(t, h.srv, stranger.DeviceID, nil)

	// A malformed digest is never admitted, even for the mapped device.
	malformed := contract
	malformed.ArtifactHash = "not-a-digest"
	assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, malformed, device.authorize))

	// Removing the record suspends the mapped device too, without a restart.
	h.remove()
	assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, rebuilt, device.authorize))

	// The mapping outlives the record: re-listing the build id, even with
	// another digest, restores the device. Lasting revocation is unpairing the
	// device or revoking the build.
	h.writeBuilds(devViewerBuild("relisted"))
	assertServable(t, compatHandshakeRecord(t, h.srv, rebuilt, device.authorize))
}

// TestReleaseBuildDigestStaysExactForAMappedDevice covers the boundary of the
// CORE-2573 exception: a linked release record never admits another digest,
// even for a device that already verified that build.
func TestReleaseBuildDigestStaysExactForAMappedDevice(t *testing.T) {
	release := shippedViewerBuild(2)
	h := newDevBuildsHarness(t, release)
	contract := release.contract()
	device := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &contract)
	assertServable(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))

	substituted := contract
	substituted.ArtifactHash = devViewerBuild("substituted").ArtifactHash
	assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, substituted, device.authorize))
}

// TestDevRebuildExceptionKeepsEveryOtherCheck covers the edges of the
// CORE-2573 exception: a mapped dev device presenting a rebuilt digest is
// still refused when any other build-provenance condition fails, and the
// refusal leaves its mapping as it was.
func TestDevRebuildExceptionKeepsEveryOtherCheck(t *testing.T) {
	cases := map[string]func(h *devBuildsHarness, dev nativeBuild, rebuilt capabilities.ClientContract) capabilities.ClientContract{
		// The record no longer grants the device's role. Dev records are
		// direct-only (ParseDevBuildManifest), so role is the entitlement the
		// operator file can withdraw.
		"forbidden role": func(h *devBuildsHarness, dev nativeBuild, rebuilt capabilities.ClientContract) capabilities.ClientContract {
			dev.Roles = []string{string(capabilities.EnrollmentRoleManager)}
			h.writeBuilds(dev)
			return rebuilt
		},
		// A release floor for the surface rises above the dev build's ordinal.
		"raised rollback floor": func(h *devBuildsHarness, dev nativeBuild, rebuilt capabilities.ClientContract) capabilities.ClientContract {
			policy, pluginGeneration := h.srv.compat.snapshotInstallInputs()
			policy.Builds.MinimumSequence = map[capabilities.Surface]int{capabilities.Surface(dev.Surface): dev.Sequence + 1}
			h.srv.SetCompatPolicy(policy, "raised-floor", pluginGeneration)
			return rebuilt
		},
		// The contract claims a surface other than the device's pinned one.
		"wrong pinned surface": func(_ *devBuildsHarness, _ nativeBuild, rebuilt capabilities.ClientContract) capabilities.ClientContract {
			rebuilt.Surface = string(capabilities.SurfaceIOSViewer)
			return rebuilt
		},
		// Another dev build id the operator listed, which this device never
		// verified: the exception follows the mapping, not the file.
		"another dev build id": func(h *devBuildsHarness, dev nativeBuild, rebuilt capabilities.ClientContract) capabilities.ClientContract {
			other := dev
			other.BuildID = "android-viewer-app-1.0-8-dev"
			other.Sequence = 8
			other.ArtifactHash = compatTestArtifactHash(other.BuildID + "@c0ffee")
			h.writeBuilds(dev, other)
			rebuilt.BuildID = other.BuildID
			return rebuilt
		},
	}
	for name, breakCheck := range cases {
		t.Run(name, func(t *testing.T) {
			h := newDevBuildsHarness(t, shippedViewerBuild(2))
			dev := devViewerBuild("c0ffee")
			contract := dev.contract()
			h.writeBuilds(dev)
			device := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &contract)
			assertServable(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))
			mapping := map[store.DeviceSurface]string{store.DeviceSurface(dev.Surface): dev.BuildID}
			assertDeviceBuilds(t, h.srv, device.DeviceID, mapping)

			rebuilt := contract
			rebuilt.ArtifactHash = devViewerBuild("rebuild-1").ArtifactHash
			assertServable(t, compatHandshakeRecord(t, h.srv, rebuilt, device.authorize))

			presented := breakCheck(h, dev, rebuilt)
			assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, presented, device.authorize))
			assertDeviceBuilds(t, h.srv, device.DeviceID, mapping)
		})
	}
}

// captureLogs routes the default logger to a buffer for the rest of the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &logs
}

// devRebuildLines returns the dev-rebuild audit lines in logs.
func devRebuildLines(logs *bytes.Buffer) []string {
	var lines []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "compatibility handshake admitted a rebuilt dev build") {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestDevRebuildAuditUsesTheAdmittingCatalog covers CORE-2579: the audit line
// for a rebuilt dev build names the digest of the catalog that admitted the
// handshake, even when the dev-build file changes between acceptance and the
// audit write.
func TestDevRebuildAuditUsesTheAdmittingCatalog(t *testing.T) {
	cases := map[string]func(h *devBuildsHarness){
		"record removed": func(h *devBuildsHarness) { h.remove() },
		"hash replaced":  func(h *devBuildsHarness) { h.writeBuilds(devViewerBuild("replaced")) },
	}
	for name, reload := range cases {
		t.Run(name, func(t *testing.T) {
			logs := captureLogs(t)
			h := newDevBuildsHarness(t, shippedViewerBuild(2))
			dev := devViewerBuild("c0ffee")
			contract := dev.contract()
			h.writeBuilds(dev)
			device := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &contract)
			assertServable(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))

			rebuilt := contract
			rebuilt.ArtifactHash = devViewerBuild("rebuild-1").ArtifactHash
			ctx := t.Context()
			scope, err := h.srv.auth.ScopeFromRequest(ctx, compatHandshakeRequest(t, rebuilt, device.authorize))
			if err != nil {
				t.Fatal(err)
			}
			surface, err := h.srv.persistedCompatSurface(ctx, scope)
			if err != nil {
				t.Fatal(err)
			}
			_, result, accepted, cataloged, err := h.srv.acceptCompatHandshake(ctx, scope, rebuilt, surface, false, false)
			if err != nil || !accepted || !result.Servable() {
				t.Fatalf("accept rebuilt = %+v, accepted %v, err %v; want servable", result, accepted, err)
			}
			if cataloged != dev.ArtifactHash {
				t.Fatalf("admitting catalog digest = %q, want %q", cataloged, dev.ArtifactHash)
			}

			// The file changes after acceptance; the live catalog no longer
			// names the digest that admitted the handshake.
			reload(h)
			h.srv.devBuilds.refresh()
			if live, _ := h.srv.compat.snapshot().Builds.DevRebuildDigest(rebuilt); live == dev.ArtifactHash {
				t.Fatal("reload left the admitting record in place; the case proves nothing")
			}

			logs.Reset()
			h.srv.logDevRebuildHandshake(ctx, scope, rebuilt, cataloged)
			lines := devRebuildLines(logs)
			if len(lines) != 1 {
				t.Fatalf("dev-rebuild audit lines = %d, want 1:\n%s", len(lines), logs.String())
			}
			for _, want := range []string{
				"artifactHash=" + artifactHashPrefix(rebuilt.ArtifactHash),
				"catalogedArtifactHash=" + artifactHashPrefix(dev.ArtifactHash),
			} {
				if !strings.Contains(lines[0], want) {
					t.Fatalf("audit line %q lacks %q", lines[0], want)
				}
			}
		})
	}
}

// TestDevRebuildAuditOnlyForTheException checks that exact dev, release and
// refused handshakes write no dev-rebuild audit line, and an admitted rebuild
// writes exactly one.
func TestDevRebuildAuditOnlyForTheException(t *testing.T) {
	logs := captureLogs(t)
	release := shippedViewerBuild(2)
	h := newDevBuildsHarness(t, release)
	dev := devViewerBuild("c0ffee")
	contract := dev.contract()
	h.writeBuilds(dev)
	device := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &contract)
	releaseContract := release.contract()
	releaseDevice := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &releaseContract)
	rebuilt := contract
	rebuilt.ArtifactHash = devViewerBuild("rebuild-1").ArtifactHash
	stranger := enrollFirstPartyDevice(t, h.srv, capabilities.EnrollmentRoleViewer, &rebuilt)
	logs.Reset()

	assertServable(t, compatHandshakeRecord(t, h.srv, contract, device.authorize))
	assertServable(t, compatHandshakeRecord(t, h.srv, releaseContract, releaseDevice.authorize))
	assertUnknownBuild(t, compatHandshakeRecord(t, h.srv, rebuilt, stranger.authorize))
	if lines := devRebuildLines(logs); len(lines) != 0 {
		t.Fatalf("exact, release and refused handshakes wrote dev-rebuild audit lines:\n%s", strings.Join(lines, "\n"))
	}

	assertServable(t, compatHandshakeRecord(t, h.srv, rebuilt, device.authorize))
	if lines := devRebuildLines(logs); len(lines) != 1 {
		t.Fatalf("admitted rebuild wrote %d dev-rebuild audit lines, want 1:\n%s", len(lines), logs.String())
	}
}

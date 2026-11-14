package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/store"
)

// firstPartyNativeSurfaces are the shipped native surfaces and the device role
// each one enrolls with.
var firstPartyNativeSurfaces = []struct {
	surface capabilities.Surface
	role    capabilities.EnrollmentRole
}{
	{capabilities.SurfaceMacOSManager, capabilities.EnrollmentRoleManager},
	{capabilities.SurfaceIOSViewer, capabilities.EnrollmentRoleViewer},
	{capabilities.SurfaceIPadManager, capabilities.EnrollmentRoleManager},
	{capabilities.SurfaceTVOSViewer, capabilities.EnrollmentRoleViewer},
	{capabilities.SurfaceAndroidViewer, capabilities.EnrollmentRoleViewer},
}

// linkFirstPartyBuilds sets the link-time first-party build manifest the way
// script/release.sh does, with one shipped build per native surface, and
// restores it when the test ends.
func linkFirstPartyBuilds(t *testing.T) {
	t.Helper()
	type build struct {
		BuildID       string   `json:"buildID"`
		Surface       string   `json:"surface"`
		ArtifactHash  string   `json:"artifactHash"`
		Sequence      int      `json:"sequence"`
		Distributions []string `json:"distributions"`
		Roles         []string `json:"roles"`
	}
	manifest := struct {
		Schema string  `json:"schema"`
		Builds []build `json:"builds"`
	}{Schema: capabilities.FirstPartyBuildManifestSchema}
	for _, native := range firstPartyNativeSurfaces {
		buildID := compatTestBuildID(string(native.surface))
		manifest.Builds = append(manifest.Builds, build{
			BuildID:       buildID,
			Surface:       string(native.surface),
			ArtifactHash:  compatTestArtifactHash(buildID),
			Sequence:      compatTestBuildSequence,
			Distributions: []string{"direct", "appStore"},
			Roles:         []string{string(native.role)},
		})
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := capabilities.EncodeFirstPartyBuildManifest(data)
	if err != nil {
		t.Fatalf("EncodeFirstPartyBuildManifest: %v", err)
	}
	restore := capabilities.FirstPartyBuildManifest
	t.Cleanup(func() { capabilities.FirstPartyBuildManifest = restore })
	capabilities.FirstPartyBuildManifest = encoded
}

// TestServerBootInstallsTheLinkedFirstPartyBuildCatalog covers
// COMPAT-NATIVE-BUILD-CATALOG-UNWIRED-1: a server built by New, with no test
// policy installed, knows every first-party native build its release linked.
func TestServerBootInstallsTheLinkedFirstPartyBuildCatalog(t *testing.T) {
	linkFirstPartyBuilds(t)
	srv, _ := newTestServerWithStore(t)
	catalog := srv.compat.snapshot().Builds
	for _, native := range firstPartyNativeSurfaces {
		contract := compatTestContract(string(native.surface))
		enrollment := capabilities.Enrollment{ID: "session:unbound", Role: native.role, Distribution: capabilities.DistributionDirect}
		// Known build, no mapping for this enrollment yet: refused for the
		// mapping, not as an unknown build.
		if got := capabilities.VerifyBuildProvenance(contract, enrollment, catalog); got != capabilities.ProvenanceEnrollmentMismatch {
			t.Fatalf("%s shipped build = %s, want %s", native.surface, got, capabilities.ProvenanceEnrollmentMismatch)
		}
		contract.BuildID += "-unshipped"
		if got := capabilities.VerifyBuildProvenance(contract, enrollment, catalog); got != capabilities.ProvenanceUnknownBuild {
			t.Fatalf("%s unshipped build = %s, want %s", native.surface, got, capabilities.ProvenanceUnknownBuild)
		}
	}
	if len(catalog.EnrollmentBindings) != 0 {
		t.Fatalf("boot catalog carries enrollment mappings %v; only enrollment may create them", catalog.EnrollmentBindings)
	}
}

func TestServerBootRefusesAMalformedLinkedBuildCatalog(t *testing.T) {
	restore := capabilities.FirstPartyBuildManifest
	t.Cleanup(func() { capabilities.FirstPartyBuildManifest = restore })
	capabilities.FirstPartyBuildManifest = "not base64!"
	defer func() {
		if recover() == nil {
			t.Fatal("New booted with a malformed first-party build catalog")
		}
	}()
	newTestServerWithStore(t)
}

// TestShippedNativeBuildEnrollsAndPassesTheHandshake covers
// COMPAT-ENROLLMENT-BUILD-MAPPING-1 end to end on a production-built server:
// a shipped build of each native surface pairs, enrolls with its Client
// Contract, and its handshake is servable. A sibling device in the same role
// and distribution class gains no mapping from it, and a tampered digest is
// refused.
func TestShippedNativeBuildEnrollsAndPassesTheHandshake(t *testing.T) {
	linkFirstPartyBuilds(t)
	srv, _ := newTestServerWithStore(t)
	for _, native := range firstPartyNativeSurfaces {
		t.Run(string(native.surface), func(t *testing.T) {
			contract := compatTestContract(string(native.surface))

			bound := enrollFirstPartyDevice(t, srv, native.role, &contract)
			response := compatHandshakeRecord(t, srv, contract, bound.authorize)
			if response.Decision != string(capabilities.DecisionCompatible) && response.Decision != string(capabilities.DecisionDegraded) {
				t.Fatalf("shipped %s build handshake = %+v, want servable", native.surface, response)
			}
			manifest := newJSONRequest(http.MethodGet, "/api/manifest", nil)
			bound.authorize(manifest)
			if rec := compatServe(srv, manifest); rec.Code != http.StatusOK {
				t.Fatalf("manifest after a servable handshake = %d: %s", rec.Code, rec.Body.String())
			}

			// CORE-1393: a sibling in the same role and distribution class
			// gains nothing from this device's mapping. It has no row, and a
			// handshake with a tampered digest records none and is refused.
			tampered := contract
			tampered.ArtifactHash = compatTestArtifactHash("tampered")
			sibling := enrollFirstPartyDevice(t, srv, native.role, nil)
			assertDeviceBuilds(t, srv, sibling.DeviceID, nil)
			assertUnknownBuild(t, compatHandshakeRecord(t, srv, tampered, sibling.authorize))
			assertDeviceBuilds(t, srv, sibling.DeviceID, nil)

			unverified := enrollFirstPartyDevice(t, srv, native.role, &tampered)
			assertDeviceBuilds(t, srv, unverified.DeviceID, nil)
			assertUnknownBuild(t, compatHandshakeRecord(t, srv, tampered, unverified.authorize))
			assertDeviceBuilds(t, srv, bound.DeviceID, map[store.DeviceSurface]string{store.DeviceSurface(native.surface): contract.BuildID})
		})
	}
}

// TestEnrollmentKeepsTheDeviceWhenTheBuildMappingCannotBeWritten covers the
// failure after a successful enrollment: the device, its session and the spent
// pairing code already exist, so the client must still receive its device
// secret. The device is left without a mapped build and its handshake is
// refused as an unknown build, as for a build that did not verify.
func TestEnrollmentKeepsTheDeviceWhenTheBuildMappingCannotBeWritten(t *testing.T) {
	linkFirstPartyBuilds(t)
	srv, _ := newTestServerWithStore(t)
	srv.deviceBuilds = failingDeviceBuildStore{srv.deviceBuilds}
	contract := compatTestContract(string(capabilities.SurfaceIOSViewer))

	enrolled := enrollFirstPartyDevice(t, srv, capabilities.EnrollmentRoleViewer, &contract)
	if enrolled.DeviceSecret == "" {
		t.Fatal("enrollment withheld the device secret")
	}
	assertUnknownBuild(t, compatHandshakeRecord(t, srv, contract, enrolled.authorize))
}

type failingDeviceBuildStore struct{ deviceBuildStore }

func (failingDeviceBuildStore) BindDeviceBuild(context.Context, store.DeviceID, store.DeviceSurface, string, time.Time) error {
	return errors.New("device build store unavailable")
}

func assertUnknownBuild(t *testing.T, response compatHandshakeResponse) {
	t.Helper()
	if response.Decision != "client-too-old" || response.ReasonCode != "unknown-build" {
		t.Fatalf("handshake = %+v, want client-too-old/unknown-build", response)
	}
}

type enrolledFirstPartyDevice struct {
	pairing.EnrollResponse
}

func (d enrolledFirstPartyDevice) authorize(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", d.SessionID)
	req.Header.Set("X-Beamfall-Device", d.DeviceID)
	req.Header.Set("X-Beamfall-Device-Secret", d.DeviceSecret)
}

// enrollFirstPartyDevice pairs a new device in role through the public routes,
// presenting contract at enrollment when it is non-nil.
func enrollFirstPartyDevice(t *testing.T, srv *Server, role capabilities.EnrollmentRole, contract *capabilities.ClientContract) enrolledFirstPartyDevice {
	t.Helper()
	codeReq := newJSONRequest(http.MethodPost, "/api/pairing-codes", strings.NewReader(fmt.Sprintf(`{"deviceRole":%q}`, role)))
	authorizeManager(codeReq)
	codeRec := compatServe(srv, codeReq)
	if codeRec.Code != http.StatusCreated {
		t.Fatalf("pairing code = %d: %s", codeRec.Code, codeRec.Body.String())
	}
	var code pairing.CodeResponse
	if err := json.Unmarshal(codeRec.Body.Bytes(), &code); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(enrollDeviceRequest{
		EnrollRequest:  pairing.EnrollRequest{Code: code.Code, DeviceName: "Shipped " + string(role)},
		ClientContract: contract,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, newJSONRequest(http.MethodPost, "/api/enroll", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("enroll = %d: %s", rec.Code, rec.Body.String())
	}
	var enrolled enrolledFirstPartyDevice
	if err := json.Unmarshal(rec.Body.Bytes(), &enrolled.EnrollResponse); err != nil {
		t.Fatal(err)
	}
	if enrolled.DeviceRole != string(role) {
		t.Fatalf("enrolled role = %q, want %q", enrolled.DeviceRole, role)
	}
	return enrolled
}

// nativeBuild is one first-party build in a test link-time manifest.
type nativeBuild struct {
	BuildID       string   `json:"buildID"`
	Surface       string   `json:"surface"`
	ArtifactHash  string   `json:"artifactHash"`
	Sequence      int      `json:"sequence"`
	Distributions []string `json:"distributions"`
	Roles         []string `json:"roles"`
}

// shippedViewerBuild is a shipped Android viewer build at release ordinal
// sequence.
func shippedViewerBuild(sequence int) nativeBuild {
	buildID := fmt.Sprintf("%s-build-%d", capabilities.SurfaceAndroidViewer, sequence)
	return nativeBuild{
		BuildID:       buildID,
		Surface:       string(capabilities.SurfaceAndroidViewer),
		ArtifactHash:  compatTestArtifactHash(buildID),
		Sequence:      sequence,
		Distributions: []string{"direct", "appStore"},
		Roles:         []string{string(capabilities.EnrollmentRoleViewer)},
	}
}

func (b nativeBuild) contract() capabilities.ClientContract {
	contract := compatTestContract(b.Surface)
	contract.BuildID = b.BuildID
	contract.ArtifactHash = b.ArtifactHash
	return contract
}

// linkNativeBuilds sets the link-time first-party build manifest to builds,
// with the given per-surface rollback floors, and restores it when the test
// ends.
func linkNativeBuilds(t *testing.T, floors map[string]int, builds ...nativeBuild) {
	t.Helper()
	manifest := struct {
		Schema          string         `json:"schema"`
		Builds          []nativeBuild  `json:"builds"`
		MinimumSequence map[string]int `json:"minimumSequence,omitempty"`
	}{Schema: capabilities.FirstPartyBuildManifestSchema, Builds: builds, MinimumSequence: floors}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := capabilities.EncodeFirstPartyBuildManifest(data)
	if err != nil {
		t.Fatalf("EncodeFirstPartyBuildManifest: %v", err)
	}
	restore := capabilities.FirstPartyBuildManifest
	t.Cleanup(func() { capabilities.FirstPartyBuildManifest = restore })
	capabilities.FirstPartyBuildManifest = encoded
}

// assertDeviceBuilds checks the exact device-to-build mapping rows of a device.
func assertDeviceBuilds(t *testing.T, srv *Server, deviceID string, want map[store.DeviceSurface]string) {
	t.Helper()
	rows, err := srv.deviceBuilds.DeviceBuildBindings(context.Background(), store.DeviceID(deviceID))
	if err != nil {
		t.Fatalf("DeviceBuildBindings(%s): %v", deviceID, err)
	}
	got := make(map[store.DeviceSurface]string, len(rows))
	for _, row := range rows {
		got[row.Surface] = row.BuildID
	}
	if len(got) != len(want) {
		t.Fatalf("device %s builds = %v, want %v", deviceID, got, want)
	}
	for surface, build := range want {
		if got[surface] != build {
			t.Fatalf("device %s builds = %v, want %v", deviceID, got, want)
		}
	}
}

func assertServable(t *testing.T, response compatHandshakeResponse) {
	t.Helper()
	if response.Decision != string(capabilities.DecisionCompatible) && response.Decision != string(capabilities.DecisionDegraded) {
		t.Fatalf("handshake = %+v, want servable", response)
	}
}

// TestNativeBuildUpgradeRebindsAtHandshake covers CORE-1393: an enrolled
// device that installs a newer shipped build is servable without re-pairing,
// and its mapping moves to the newer build. Reinstalling the older build,
// which is still above the rollback floor, moves it back.
func TestNativeBuildUpgradeRebindsAtHandshake(t *testing.T) {
	v1, v2 := shippedViewerBuild(2), shippedViewerBuild(3)
	linkNativeBuilds(t, nil, v1, v2)
	srv, _ := newTestServerWithStore(t)
	surface := store.DeviceSurface(capabilities.SurfaceAndroidViewer)

	enrolledV1 := v1.contract()
	device := enrollFirstPartyDevice(t, srv, capabilities.EnrollmentRoleViewer, &enrolledV1)
	assertServable(t, compatHandshakeRecord(t, srv, v1.contract(), device.authorize))
	assertDeviceBuilds(t, srv, device.DeviceID, map[store.DeviceSurface]string{surface: v1.BuildID})

	assertServable(t, compatHandshakeRecord(t, srv, v2.contract(), device.authorize))
	assertDeviceBuilds(t, srv, device.DeviceID, map[store.DeviceSurface]string{surface: v2.BuildID})
	manifest := newJSONRequest(http.MethodGet, "/api/manifest", nil)
	device.authorize(manifest)
	if rec := compatServe(srv, manifest); rec.Code != http.StatusOK {
		t.Fatalf("manifest after the upgrade handshake = %d: %s", rec.Code, rec.Body.String())
	}

	assertServable(t, compatHandshakeRecord(t, srv, v1.contract(), device.authorize))
	assertDeviceBuilds(t, srv, device.DeviceID, map[store.DeviceSurface]string{surface: v1.BuildID})
}

// TestUnmappedDeviceObtainsItsBuildAtHandshake covers a device enrolled
// before migration 0112, or without a Client Contract: it has no mapping and
// gets one on its first handshake with a build it is entitled to.
func TestUnmappedDeviceObtainsItsBuildAtHandshake(t *testing.T) {
	v1 := shippedViewerBuild(2)
	linkNativeBuilds(t, nil, v1)
	srv, _ := newTestServerWithStore(t)

	device := enrollFirstPartyDevice(t, srv, capabilities.EnrollmentRoleViewer, nil)
	assertDeviceBuilds(t, srv, device.DeviceID, nil)
	assertServable(t, compatHandshakeRecord(t, srv, v1.contract(), device.authorize))
	assertDeviceBuilds(t, srv, device.DeviceID, map[store.DeviceSurface]string{store.DeviceSurface(capabilities.SurfaceAndroidViewer): v1.BuildID})
}

// TestHandshakeRebindRefusesDowngradeAndTampering covers the builds a
// handshake must never record: one below the surface's rollback floor (for an
// unmapped device and for a mapped one) and one whose artifact digest does not
// match the catalog.
func TestHandshakeRebindRefusesDowngradeAndTampering(t *testing.T) {
	v1, v2, v3 := shippedViewerBuild(1), shippedViewerBuild(2), shippedViewerBuild(3)
	linkNativeBuilds(t, map[string]int{string(capabilities.SurfaceAndroidViewer): 2}, v1, v2, v3)
	srv, _ := newTestServerWithStore(t)
	surface := store.DeviceSurface(capabilities.SurfaceAndroidViewer)

	unmapped := enrollFirstPartyDevice(t, srv, capabilities.EnrollmentRoleViewer, nil)
	assertUnknownBuild(t, compatHandshakeRecord(t, srv, v1.contract(), unmapped.authorize))
	assertDeviceBuilds(t, srv, unmapped.DeviceID, nil)

	enrolledV2 := v2.contract()
	device := enrollFirstPartyDevice(t, srv, capabilities.EnrollmentRoleViewer, &enrolledV2)
	assertUnknownBuild(t, compatHandshakeRecord(t, srv, v1.contract(), device.authorize))
	tampered := v3.contract()
	tampered.ArtifactHash = compatTestArtifactHash("tampered")
	assertUnknownBuild(t, compatHandshakeRecord(t, srv, tampered, device.authorize))
	assertDeviceBuilds(t, srv, device.DeviceID, map[store.DeviceSurface]string{surface: v2.BuildID})
}

// TestHandshakeRebindTouchesOnlyTheCallingDevice covers a sibling device in
// the same role and distribution class: one device's upgrade adds or changes
// nothing for another.
func TestHandshakeRebindTouchesOnlyTheCallingDevice(t *testing.T) {
	v1, v2 := shippedViewerBuild(2), shippedViewerBuild(3)
	linkNativeBuilds(t, nil, v1, v2)
	srv, _ := newTestServerWithStore(t)
	surface := store.DeviceSurface(capabilities.SurfaceAndroidViewer)

	enrolledV1 := v1.contract()
	upgrading := enrollFirstPartyDevice(t, srv, capabilities.EnrollmentRoleViewer, &enrolledV1)
	staying := enrollFirstPartyDevice(t, srv, capabilities.EnrollmentRoleViewer, &enrolledV1)
	unmapped := enrollFirstPartyDevice(t, srv, capabilities.EnrollmentRoleViewer, nil)

	assertServable(t, compatHandshakeRecord(t, srv, v2.contract(), upgrading.authorize))
	assertDeviceBuilds(t, srv, upgrading.DeviceID, map[store.DeviceSurface]string{surface: v2.BuildID})
	assertDeviceBuilds(t, srv, staying.DeviceID, map[store.DeviceSurface]string{surface: v1.BuildID})
	assertDeviceBuilds(t, srv, unmapped.DeviceID, nil)
	assertServable(t, compatHandshakeRecord(t, srv, v1.contract(), staying.authorize))
	assertDeviceBuilds(t, srv, staying.DeviceID, map[store.DeviceSurface]string{surface: v1.BuildID})
}

// TestHandshakeRebindRequiresARoleEntitledBuild covers a viewer device
// presenting a shipped build that only manager devices may run: nothing is
// recorded and the handshake is refused.
func TestHandshakeRebindRequiresARoleEntitledBuild(t *testing.T) {
	managerOnly := shippedViewerBuild(2)
	managerOnly.Roles = []string{string(capabilities.EnrollmentRoleManager)}
	linkNativeBuilds(t, nil, managerOnly)
	srv, _ := newTestServerWithStore(t)

	device := enrollFirstPartyDevice(t, srv, capabilities.EnrollmentRoleViewer, nil)
	assertUnknownBuild(t, compatHandshakeRecord(t, srv, managerOnly.contract(), device.authorize))
	assertDeviceBuilds(t, srv, device.DeviceID, nil)
}

// TestHandshakeRebindReportsTheRealRefusalForANewerBuild covers a newer
// verified build that is refused for another reason, here one that needs a
// newer server. The device's mapping moves to the build it now runs, so the
// refusal says server-too-old instead of telling the user to update an app
// that is already newer than the server. Reinstalling the previous build,
// still above the rollback floor, is servable again without re-pairing, and
// once the server supports the newer build that is servable too.
func TestHandshakeRebindReportsTheRealRefusalForANewerBuild(t *testing.T) {
	v1, v2 := shippedViewerBuild(2), shippedViewerBuild(3)
	linkNativeBuilds(t, nil, v1, v2)
	srv, _ := newTestServerWithStore(t)
	surface := store.DeviceSurface(capabilities.SurfaceAndroidViewer)

	enrolledV1 := v1.contract()
	device := enrollFirstPartyDevice(t, srv, capabilities.EnrollmentRoleViewer, &enrolledV1)
	tooNew := v2.contract()
	tooNew.CoreContractRevRange = [2]capabilities.Revision{2, 2}
	response := compatHandshakeRecord(t, srv, tooNew, device.authorize)
	if response.Decision != string(capabilities.DecisionServerTooOld) {
		t.Fatalf("handshake needing a newer server = %+v, want %s", response, capabilities.DecisionServerTooOld)
	}
	assertDeviceBuilds(t, srv, device.DeviceID, map[store.DeviceSurface]string{surface: v2.BuildID})

	assertServable(t, compatHandshakeRecord(t, srv, v1.contract(), device.authorize))
	assertDeviceBuilds(t, srv, device.DeviceID, map[store.DeviceSurface]string{surface: v1.BuildID})

	policy := srv.compat.snapshot()
	policy.SupportedCoreRevisions = [2]capabilities.Revision{1, 2}
	srv.SetCompatPolicy(policy, srv.buildSHA, 0)
	assertServable(t, compatHandshakeRecord(t, srv, tooNew, device.authorize))
	assertDeviceBuilds(t, srv, device.DeviceID, map[store.DeviceSurface]string{surface: v2.BuildID})
}

// TestHandshakeRebindKeepsThePinnedDeviceSurface covers a device already
// pinned to the Android viewer surface presenting a shipped iOS viewer build
// its role is entitled to: the device surface check refuses it and no row is
// written for the other surface.
func TestHandshakeRebindKeepsThePinnedDeviceSurface(t *testing.T) {
	android := shippedViewerBuild(2)
	ios := shippedViewerBuild(2)
	ios.Surface = string(capabilities.SurfaceIOSViewer)
	ios.BuildID = string(capabilities.SurfaceIOSViewer) + "-build-2"
	ios.ArtifactHash = compatTestArtifactHash(ios.BuildID)
	linkNativeBuilds(t, nil, android, ios)
	srv, _ := newTestServerWithStore(t)
	surface := store.DeviceSurface(capabilities.SurfaceAndroidViewer)

	enrolled := android.contract()
	device := enrollFirstPartyDevice(t, srv, capabilities.EnrollmentRoleViewer, &enrolled)
	assertServable(t, compatHandshakeRecord(t, srv, android.contract(), device.authorize))
	response := compatHandshakeRecord(t, srv, ios.contract(), device.authorize)
	if response.Decision == string(capabilities.DecisionCompatible) || response.Decision == string(capabilities.DecisionDegraded) {
		t.Fatalf("other-surface handshake on a pinned device = %+v, want a refusal", response)
	}
	assertDeviceBuilds(t, srv, device.DeviceID, map[store.DeviceSurface]string{surface: android.BuildID})
}

// TestHandshakeRebindRequiresADistributionEntitledBuild covers a directly
// distributed device presenting a shipped build released only through the app
// store: nothing is recorded and the handshake is refused.
func TestHandshakeRebindRequiresADistributionEntitledBuild(t *testing.T) {
	storeOnly := shippedViewerBuild(2)
	storeOnly.Distributions = []string{"appStore"}
	linkNativeBuilds(t, nil, storeOnly)
	srv, _ := newTestServerWithStore(t)

	device := enrollFirstPartyDevice(t, srv, capabilities.EnrollmentRoleViewer, nil)
	assertUnknownBuild(t, compatHandshakeRecord(t, srv, storeOnly.contract(), device.authorize))
	assertDeviceBuilds(t, srv, device.DeviceID, nil)
}

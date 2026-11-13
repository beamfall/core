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
// and distribution class that did not enroll with that build is still refused.
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

			sibling := enrollFirstPartyDevice(t, srv, native.role, nil)
			assertUnknownBuild(t, compatHandshakeRecord(t, srv, contract, sibling.authorize))

			tampered := contract
			tampered.ArtifactHash = compatTestArtifactHash("tampered")
			unverified := enrollFirstPartyDevice(t, srv, native.role, &tampered)
			assertUnknownBuild(t, compatHandshakeRecord(t, srv, contract, unverified.authorize))
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

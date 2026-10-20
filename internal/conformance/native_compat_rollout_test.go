package conformance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/server"
	"github.com/beamfall/core/internal/store"
)

var nativeRolloutSurfaces = []capabilities.Surface{
	capabilities.SurfaceMacOSManager,
	capabilities.SurfaceIOSViewer,
	capabilities.SurfaceIPadManager,
	capabilities.SurfaceTVOSViewer,
	capabilities.SurfaceAndroidViewer,
}

func TestNativeCompatibilityRolloutFailsClosedPerSurface(t *testing.T) {
	for i, surface := range nativeRolloutSurfaces {
		t.Run(string(surface), func(t *testing.T) {
			t.Run("unknown build", func(t *testing.T) {
				world := nativeRolloutWorld(t)
				bindNativeRolloutSurface(t, world.store, surface)
				contract := nativeRolloutContract(surface)
				contract.BuildID = "unknown-first-party-build"
				response := nativeRolloutHandshake(t, world.srv, contract)
				if response.Decision != string(capabilities.DecisionClientTooOld) || response.ReasonCode != string(capabilities.ReasonUnknownBuild) || response.DecisionID != "" {
					t.Fatalf("unknown build response = %+v, want fail-closed unknown-build", response)
				}
			})

			t.Run("mismatched build surface", func(t *testing.T) {
				world := nativeRolloutWorld(t)
				bindNativeRolloutSurface(t, world.store, surface)
				other := nativeRolloutSurfaces[(i+1)%len(nativeRolloutSurfaces)]
				response := nativeRolloutHandshake(t, world.srv, nativeRolloutContract(other))
				if response.Decision != string(capabilities.DecisionClientTooOld) || response.ReasonCode != string(capabilities.ReasonUnknownBuild) || response.DecisionID != "" {
					t.Fatalf("mismatched build response = %+v, want fail-closed unknown-build", response)
				}
			})

			t.Run("absent decision", func(t *testing.T) {
				world := nativeRolloutWorld(t)
				bindNativeRolloutSurface(t, world.store, surface)
				req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
				authorizeNativeRollout(req)
				rec := httptest.NewRecorder()
				world.srv.ServeHTTP(rec, req)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("native request without decision = %d: %s, want 403", rec.Code, rec.Body.String())
				}
			})

			t.Run("first verified handshake persists binding", func(t *testing.T) {
				world := nativeRolloutWorld(t)
				response := nativeRolloutHandshake(t, world.srv, nativeRolloutContract(surface))
				if response.Decision != string(capabilities.DecisionCompatible) || response.DecisionID == "" {
					t.Fatalf("verified first handshake = %+v, want accepted decision", response)
				}
				device, err := world.store.Device(context.Background(), "device-tv")
				if err != nil {
					t.Fatal(err)
				}
				if device.Surface != store.DeviceSurface(surface) {
					t.Fatalf("persisted surface = %q, want %q", device.Surface, surface)
				}
			})
		})
	}
}

func TestNativeDeviceSurfaceVocabularyParity(t *testing.T) {
	for _, surface := range nativeRolloutSurfaces {
		if !capabilities.IsNativeSurface(surface) {
			t.Errorf("capabilities does not classify %q as native", surface)
		}
		normalized, err := store.NormalizeDeviceSurface(store.DeviceSurface(surface))
		if err != nil || string(normalized) != string(surface) {
			t.Errorf("store surface %q = %q, %v", surface, normalized, err)
		}
	}
	if capabilities.IsNativeSurface(capabilities.SurfaceCoreWebUI) {
		t.Fatal("core-web-ui classified as a native rollout surface")
	}
}

type nativeRolloutResponse struct {
	Decision   string `json:"decision"`
	DecisionID string `json:"decisionID"`
	ReasonCode string `json:"reasonCode"`
}

func nativeRolloutWorld(t *testing.T) compatDurabilityWorld {
	t.Helper()
	world := bootCompatDurabilityServer(t, t.TempDir())
	world.srv.SetCompatPolicy(nativeRolloutPolicy(), "native-rollout-build", 0)
	return world
}

func nativeRolloutPolicy() capabilities.Policy {
	catalog := capabilities.BuildCatalog{MinimumSequence: make(map[capabilities.Surface]int)}
	for _, surface := range nativeRolloutSurfaces {
		buildID := nativeRolloutBuildID(surface)
		catalog.Builds = append(catalog.Builds, capabilities.BuildRecord{
			BuildID:       buildID,
			Surface:       surface,
			ArtifactHash:  clientCompatArtifactHash(buildID),
			Sequence:      clientCompatBuildSequence,
			Distributions: []capabilities.Distribution{capabilities.DistributionDirect},
			Roles:         []capabilities.EnrollmentRole{capabilities.EnrollmentRoleViewer},
		})
		catalog.EnrollmentBindings = append(catalog.EnrollmentBindings, capabilities.EnrollmentBuildBinding{
			EnrollmentID: "session:session-default",
			Surface:      surface,
			BuildID:      buildID,
		})
		catalog.MinimumSequence[surface] = clientCompatBuildSequence
	}
	return capabilities.Policy{
		Builds:                      catalog,
		SupportedCoreRevisions:      [2]capabilities.Revision{1, 1},
		SupportedRenderingRevisions: [2]capabilities.Revision{1, 1},
		RequiredRenderingStamps:     []string{"core-web-ui-v1"},
		SupportedDeveloperAPIMajor:  1,
		EmbeddedUIRevision:          capabilities.EmbeddedUIContractRevision,
	}
}

func nativeRolloutBuildID(surface capabilities.Surface) string {
	return string(surface) + "-native-rollout-build"
}

func nativeRolloutContract(surface capabilities.Surface) capabilities.ClientContract {
	contract := capabilities.EmbeddedUIContract()
	contract.Surface = string(surface)
	contract.AppVersion = "native-rollout-test"
	contract.BuildID = nativeRolloutBuildID(surface)
	contract.ArtifactHash = clientCompatArtifactHash(contract.BuildID)
	return contract
}

func bindNativeRolloutSurface(t *testing.T, st *store.Store, surface capabilities.Surface) {
	t.Helper()
	if err := st.BindDeviceSurface(context.Background(), "device-tv", store.DeviceSurface(surface)); err != nil {
		t.Fatal(err)
	}
}

func nativeRolloutHandshake(t *testing.T, srv *server.Server, contract capabilities.ClientContract) nativeRolloutResponse {
	t.Helper()
	req := durabilityNativeRequest(t, "session-default", contract)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handshake status = %d: %s", rec.Code, rec.Body.String())
	}
	var response nativeRolloutResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func authorizeNativeRollout(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-default")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

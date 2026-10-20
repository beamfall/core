package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/store"
)

// compatTestSurfaces is the set of client surfaces these tests present builds
// for. Each gets its own catalog record: a build belongs to one surface.
var compatTestSurfaces = []string{
	string(capabilities.SurfaceCoreWebUI),
	string(capabilities.SurfaceMacOSManager),
	string(capabilities.SurfaceIOSViewer),
	string(capabilities.SurfaceIPadManager),
	string(capabilities.SurfaceTVOSViewer),
	string(capabilities.SurfaceAndroidViewer),
	string(capabilities.SurfaceDeveloperAPIClient),
	string(capabilities.SurfaceOriginNode),
}

const compatTestBuildSequence = 2

func compatTestBuildID(surface string) string {
	return surface + "-test-build"
}

func compatTestArtifactHash(buildID string) string {
	sum := sha256.Sum256([]byte(buildID))
	return hex.EncodeToString(sum[:])
}

// compatTestCatalog is the server-known build record set these tests install.
func compatTestCatalog() capabilities.BuildCatalog {
	catalog := capabilities.BuildCatalog{MinimumSequence: map[capabilities.Surface]int{}}
	for _, surface := range compatTestSurfaces {
		buildID := compatTestBuildID(surface)
		catalog.Builds = append(catalog.Builds, capabilities.BuildRecord{
			BuildID:      buildID,
			Surface:      capabilities.Surface(surface),
			ArtifactHash: compatTestArtifactHash(buildID),
			Sequence:     compatTestBuildSequence,
			Distributions: []capabilities.Distribution{
				capabilities.DistributionDirect,
				capabilities.DistributionAppStore,
			},
			Roles: []capabilities.EnrollmentRole{
				capabilities.EnrollmentRoleViewer,
				capabilities.EnrollmentRoleManager,
			},
		})
		for _, enrollmentID := range []string{"session:session-default", "session:session-manager"} {
			catalog.EnrollmentBindings = append(catalog.EnrollmentBindings, capabilities.EnrollmentBuildBinding{
				EnrollmentID: enrollmentID,
				Surface:      capabilities.Surface(surface),
				BuildID:      buildID,
			})
		}
		catalog.MinimumSequence[capabilities.Surface(surface)] = compatTestBuildSequence
	}
	return catalog
}

// compatTestServer is a test server whose compatibility policy knows the client
// builds these tests present. The default policy knows only the embedded UI.
func compatTestServer(t *testing.T) *Server {
	t.Helper()
	srv := newTestServer(t)
	policy := srv.compat.snapshot()
	policy.Builds = compatTestCatalog()
	srv.SetCompatPolicy(policy, srv.buildSHA, 0)
	return srv
}

func compatTestContract(surface string) capabilities.ClientContract {
	contract := capabilities.EmbeddedUIContract()
	contract.Surface = surface
	contract.AppVersion = "test"
	contract.BuildID = compatTestBuildID(surface)
	contract.ArtifactHash = compatTestArtifactHash(contract.BuildID)
	return contract
}

func compatHandshakeRequest(t *testing.T, contract capabilities.ClientContract, authorize func(*http.Request)) *http.Request {
	t.Helper()
	body, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	req := newJSONRequest(http.MethodPost, "/api/compat/handshake", bytes.NewReader(body))
	if authorize != nil {
		authorize(req)
	}
	return req
}

func compatHandshakeRecord(t *testing.T, srv *Server, contract capabilities.ClientContract, authorize func(*http.Request)) compatHandshakeResponse {
	t.Helper()
	req := compatHandshakeRequest(t, contract, authorize)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handshake status = %d: %s", rec.Code, rec.Body.String())
	}
	var response compatHandshakeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode handshake: %v", err)
	}
	return response
}

func authorizeViewerCookies(req *http.Request) {
	addViewerCookies(req)
	req.Header.Set(auth.CSRFHeader, "1")
}

func cookieManifestRequest() *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	addViewerCookies(req)
	return req
}

func authorizeManagerCookies(req *http.Request) {
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "session-manager"})         //nolint:gosec // test request cookie
	req.AddCookie(&http.Cookie{Name: auth.DeviceCookie, Value: "device-mac"})               //nolint:gosec // test request cookie
	req.AddCookie(&http.Cookie{Name: auth.DeviceSecretCookie, Value: "dev-manager-secret"}) //nolint:gosec // test request cookie
	req.Header.Set(auth.CSRFHeader, "1")
}

func compatBoundRouteRequest(path string) *http.Request {
	switch path {
	case "/api/graphql":
		req := newJSONRequest(http.MethodPost, path, strings.NewReader(
			`{"query":"query { serverInfo { name } }"}`,
		))
		authorizeViewerCookies(req)
		return req
	case "/api/playback-info":
		req := newJSONRequest(http.MethodPost, path, strings.NewReader(`{}`))
		authorizeViewerCookies(req)
		return req
	case "/api/pairing-codes":
		req := newJSONRequest(http.MethodPost, path, strings.NewReader(`{"deviceRole":"viewer"}`))
		authorizeManagerCookies(req)
		return req
	default:
		return cookieManifestRequest()
	}
}

func TestCompatDefaultsMatchPublishedServerCapabilities(t *testing.T) {
	srv := compatTestServer(t)
	policy := srv.compat.snapshot()
	if len(policy.KnownCapabilities) != len(srv.capabilities) {
		t.Fatalf("compat capabilities = %v, published capabilities = %v", policy.KnownCapabilities, srv.capabilities)
	}
	for _, capability := range srv.capabilities {
		if !policy.KnownCapabilities[capability] {
			t.Errorf("published capability %q missing from compatibility policy", capability)
		}
	}

	srv.SetBuildMetadata("next-build", "", "")
	policy = srv.compat.snapshot()
	for _, capability := range srv.capabilities {
		if !policy.KnownCapabilities[capability] {
			t.Errorf("build metadata refresh dropped default capability %q", capability)
		}
	}
}

func TestCompatHandshakeBootstrapAuthenticationAndCSRF(t *testing.T) {
	srv := compatTestServer(t)
	contract := compatTestContract("core-web-ui")

	bootstrapReq := compatHandshakeRequest(t, contract, nil)
	bootstrapRec := httptest.NewRecorder()
	srv.ServeHTTP(bootstrapRec, bootstrapReq)
	if bootstrapRec.Code != http.StatusOK || bootstrapRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bootstrap status/cache = %d/%q", bootstrapRec.Code, bootstrapRec.Header().Get("Cache-Control"))
	}
	var bootstrap map[string]any
	if err := json.Unmarshal(bootstrapRec.Body.Bytes(), &bootstrap); err != nil {
		t.Fatal(err)
	}
	if bootstrap["decision"] != "bootstrap" || bootstrap["messageClass"] != "compat-bootstrap" || len(bootstrap) != 2 {
		t.Fatalf("bootstrap response = %#v, want coarse two-field response", bootstrap)
	}

	noCSRF := compatHandshakeRequest(t, contract, addViewerCookies)
	noCSRFRec := httptest.NewRecorder()
	srv.ServeHTTP(noCSRFRec, noCSRF)
	if noCSRFRec.Code != http.StatusForbidden {
		t.Fatalf("cookie handshake without CSRF = %d, want 403", noCSRFRec.Code)
	}

	accepted := compatHandshakeRecord(t, srv, contract, authorizeViewerCookies)
	if accepted.Decision != "compatible" || accepted.DecisionID == "" || accepted.MessageClass != "compat-compatible" {
		t.Fatalf("authenticated handshake = %#v", accepted)
	}
}

func TestCompatRefusalDoesNotBlockCredentialLogout(t *testing.T) {
	srv := compatTestServer(t)
	srv.compat.markRefused("session:session-default")
	req := newJSONRequest(http.MethodPost, "/api/web/logout", strings.NewReader(`{}`))
	authorizeViewerCookies(req)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("logout under compatibility refusal = %d: %s", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Cookies()) != 3 {
		t.Fatalf("logout cleared %d credential cookies, want 3", len(rec.Result().Cookies()))
	}
}

func TestCompatSharedBoundaryCoversNamedWebRoutes(t *testing.T) {
	for _, path := range []string{
		"/api/graphql",
		"/api/playback-info",
		"/api/manifest",
		"/api/pairing-codes",
	} {
		t.Run(path, func(t *testing.T) {
			srv := compatTestServer(t)
			initial := httptest.NewRecorder()
			srv.ServeHTTP(initial, compatBoundRouteRequest(path))
			if initial.Code == http.StatusForbidden {
				t.Fatalf("server-derived decision blocked %s: %s", path, initial.Body.String())
			}

			authorize := authorizeViewerCookies
			if path == "/api/pairing-codes" {
				authorize = authorizeManagerCookies
			}
			accepted := compatHandshakeRecord(t, srv, compatTestContract("core-web-ui"), authorize)
			if accepted.Decision != "compatible" {
				t.Fatalf("%s handshake = %#v", path, accepted)
			}
			policy := srv.compat.snapshot()
			srv.SetCompatPolicy(policy, "next-build", 1)

			stale := httptest.NewRecorder()
			srv.ServeHTTP(stale, compatBoundRouteRequest(path))
			if stale.Code != http.StatusForbidden {
				t.Fatalf("%s after invalidation = %d, want 403", path, stale.Code)
			}
		})
	}
}

func TestCompatEmbeddedSkewRefusesEveryNamedWebRoute(t *testing.T) {
	for _, path := range []string{
		"/api/graphql",
		"/api/playback-info",
		"/api/manifest",
		"/api/pairing-codes",
	} {
		t.Run(path, func(t *testing.T) {
			srv := compatTestServer(t)
			policy := srv.compat.snapshot()
			policy.EmbeddedUIRevision++
			srv.SetCompatPolicy(policy, srv.buildSHA, 0)

			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, compatBoundRouteRequest(path))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s with embedded skew = %d, want 403", path, rec.Code)
			}
			var response compatHandshakeResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Decision != "server-installation-inconsistent" {
				t.Fatalf("%s embedded-skew decision = %q", path, response.Decision)
			}
		})
	}
}

func TestCompatCookieWithNonBearerAuthorizationRemainsWebBound(t *testing.T) {
	srv := compatTestServer(t)
	policy := srv.compat.snapshot()
	policy.EmbeddedUIRevision++
	srv.SetCompatPolicy(policy, srv.buildSHA, 0)

	req := cookieManifestRequest()
	req.Header.Set("Authorization", "Basic opaque")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("cookie request with non-Bearer authorization = %d, want 403", rec.Code)
	}
	var response compatHandshakeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Decision != "server-installation-inconsistent" {
		t.Fatalf("decision = %q, want server-installation-inconsistent", response.Decision)
	}
}

func TestCompatCookieProvisionalDecisionExpiresWithoutRenewal(t *testing.T) {
	srv := compatTestServer(t)
	now := time.Unix(1_800_000_000, 0)
	srv.compat.now = func() time.Time { return now }

	first := httptest.NewRecorder()
	srv.ServeHTTP(first, cookieManifestRequest())
	if first.Code != http.StatusOK || first.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("provisional request = %d cache %q: %s", first.Code, first.Header().Get("Cache-Control"), first.Body.String())
	}

	now = now.Add(compatProvisionalTTL + time.Second)
	expired := httptest.NewRecorder()
	srv.ServeHTTP(expired, cookieManifestRequest())
	if expired.Code != http.StatusForbidden || expired.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expired provisional request = %d cache %q, want 403/no-store", expired.Code, expired.Header().Get("Cache-Control"))
	}
}

func TestCompatGenerationRenewalIsAsymmetric(t *testing.T) {
	t.Run("server-derived renews", func(t *testing.T) {
		srv := compatTestServer(t)
		first := httptest.NewRecorder()
		srv.ServeHTTP(first, cookieManifestRequest())
		if first.Code != http.StatusOK {
			t.Fatalf("initial derived request = %d", first.Code)
		}
		policy := srv.compat.snapshot()
		srv.SetCompatPolicy(policy, "next-build", 1)
		renewed := httptest.NewRecorder()
		srv.ServeHTTP(renewed, cookieManifestRequest())
		if renewed.Code != http.StatusOK {
			t.Fatalf("derived request after generation bump = %d: %s", renewed.Code, renewed.Body.String())
		}
	})

	t.Run("submitted refuses", func(t *testing.T) {
		srv := compatTestServer(t)
		response := compatHandshakeRecord(t, srv, compatTestContract("core-web-ui"), authorizeViewerCookies)
		if response.DecisionID == "" {
			t.Fatal("submitted handshake returned no decision id")
		}
		policy := srv.compat.snapshot()
		srv.SetCompatPolicy(policy, "next-build", 1)
		refused := httptest.NewRecorder()
		srv.ServeHTTP(refused, cookieManifestRequest())
		if refused.Code != http.StatusForbidden {
			t.Fatalf("submitted request after generation bump = %d, want 403", refused.Code)
		}
	})
}

func TestCompatRefusedRehandshakeDeletesPriorAcceptance(t *testing.T) {
	srv := compatTestServer(t)
	accepted := compatHandshakeRecord(t, srv, compatTestContract("core-web-ui"), authorizeViewerCookies)
	if accepted.Decision != "compatible" {
		t.Fatalf("first decision = %q", accepted.Decision)
	}
	refusedContract := compatTestContract("core-web-ui")
	refusedContract.BuildID = ""
	refused := compatHandshakeRecord(t, srv, refusedContract, authorizeViewerCookies)
	if refused.Decision != "client-too-old" || refused.DecisionID != "" {
		t.Fatalf("refused re-handshake = %#v", refused)
	}
	next := httptest.NewRecorder()
	srv.ServeHTTP(next, cookieManifestRequest())
	if next.Code != http.StatusForbidden {
		t.Fatalf("request after refused re-handshake = %d, want 403", next.Code)
	}
}

func TestCompatHandshakeDoesNotReflectUnknownRequestedCapability(t *testing.T) {
	contract := compatTestContract("core-web-ui")
	const unknown = "torrent-plugin-endpoint"
	contract.RequestedCapabilities = []string{unknown}
	req := compatHandshakeRequest(t, contract, authorizeViewerCookies)
	rec := httptest.NewRecorder()

	compatTestServer(t).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handshake status = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), unknown) {
		t.Fatalf("handshake reflected unknown requested capability: %s", rec.Body.String())
	}
	var response compatHandshakeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Decision != "degraded" || response.ReasonCode != "unknown-capability" {
		t.Fatalf("handshake response = %#v, want degraded unknown-capability", response)
	}
}

func TestCompatNativePinnedSurfacesAndInvalidation(t *testing.T) {
	surfaces := []string{
		"macos-manager",
		"ios-viewer",
		"ipad-manager",
		"tvos-viewer",
		"android-viewer",
	}
	for i, surface := range surfaces {
		srv := compatTestServer(t)
		response := compatHandshakeRecord(t, srv, compatTestContract(surface), authorizeViewer)
		if response.Decision != "compatible" || response.DecisionID == "" {
			t.Fatalf("%s handshake = %#v", surface, response)
		}
		normal := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
		authorizeViewer(normal)
		normalRec := httptest.NewRecorder()
		srv.ServeHTTP(normalRec, normal)
		if normalRec.Code != http.StatusOK || normalRec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s normal request = %d cache %q", surface, normalRec.Code, normalRec.Header().Get("Cache-Control"))
		}
		policy := srv.compat.snapshot()
		srv.SetCompatPolicy(policy, "build-"+surface, i+1)
		stale := httptest.NewRequest(http.MethodPost, "/api/playback-info", strings.NewReader(`{}`))
		stale.Header.Set("Content-Type", "application/json")
		authorizeViewer(stale)
		staleRec := httptest.NewRecorder()
		srv.ServeHTTP(staleRec, stale)
		if staleRec.Code != http.StatusForbidden {
			t.Fatalf("%s invalidated token-mint request = %d, want 403", surface, staleRec.Code)
		}
	}
}

func TestCompatV1NeverGatedByRefusedDecision(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	key := mintKey(t, st, "compat-v1", "", store.ScopeReadCatalog)
	contract := compatTestContract("developer-api-client")
	handshake := compatHandshakeRecord(t, srv, contract, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+key)
	})
	if handshake.Decision != "client-too-old" || handshake.ReasonCode != "unverified-distribution" {
		t.Fatalf("advisory /v1 handshake = %#v, want refused unverified distribution", handshake)
	}
	rec := v1Get(t, srv, "/v1/libraries", key)
	if rec.Code != http.StatusOK {
		t.Fatalf("/v1 after refused advisory handshake = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCompatAuthFingerprintCoversAuthorizationTriggers(t *testing.T) {
	base := auth.Scope{
		DeviceID:          "device",
		DeviceRole:        store.DeviceRoleViewer,
		DistributionClass: store.DeviceDistributionDirect,
		ProfileID:         "profile",
		Profile:           store.ProfileAdult,
		LibraryIDs:        []store.LibraryID{"b", "a"},
	}
	baseFingerprint := compatAuthFingerprint(base)
	mutations := []auth.Scope{
		func() auth.Scope { changed := base; changed.DeviceRole = store.DeviceRoleManager; return changed }(),
		func() auth.Scope { changed := base; changed.ProfileID = "other"; return changed }(),
		func() auth.Scope { changed := base; changed.Profile = store.ProfileManaged; return changed }(),
		func() auth.Scope { changed := base; changed.Revealed = true; return changed }(),
		func() auth.Scope { changed := base; changed.Admin = true; return changed }(),
		func() auth.Scope {
			changed := base
			changed.DistributionClass = store.DeviceDistributionAppStore
			return changed
		}(),
	}
	registry := capabilities.NewRegistry()
	key := capabilities.BindingKey{
		Subject:         "session:compat-auth",
		Surface:         "core-web-ui",
		Distribution:    capabilities.DistributionDirect,
		AuthFingerprint: baseFingerprint,
		Generation:      1,
	}
	if _, accepted, err := registry.Accept(
		key,
		compatTestContract("core-web-ui"),
		capabilities.Result{Decision: capabilities.DecisionCompatible},
		time.Unix(1_800_000_000, 0),
		time.Hour,
	); err != nil || !accepted {
		t.Fatalf("seed compatibility record = accepted %t, error %v", accepted, err)
	}
	for i, changed := range mutations {
		changedFingerprint := compatAuthFingerprint(changed)
		if changedFingerprint == baseFingerprint {
			t.Errorf("authorization mutation %d did not change fingerprint", i)
		}
		_, err := registry.Resolve(key.Subject, capabilities.ResolveKey{
			Subject:         key.Subject,
			Distribution:    key.Distribution,
			AuthFingerprint: changedFingerprint,
			Generation:      key.Generation,
		}, time.Unix(1_800_000_000, 0))
		if !errors.Is(err, capabilities.ErrAuthChanged) {
			t.Errorf("authorization mutation %d resolved with %v, want auth-changed refusal", i, err)
		}
	}
	reordered := base
	reordered.LibraryIDs = []store.LibraryID{"a", "b"}
	if compatAuthFingerprint(reordered) != baseFingerprint {
		t.Fatal("library ordering changed canonical auth fingerprint")
	}
}

func TestCompatGenerationUsesEveryDurableInput(t *testing.T) {
	base := CompatGenerationInputs{
		BuildRevision:              "build-a",
		CoreRevisions:              [2]capabilities.Revision{1, 1},
		RenderingRevisions:         [2]capabilities.Revision{1, 1},
		RequiredRenderingStamps:    []string{"stamp"},
		RequiredVocabularies:       []string{"vocabulary"},
		KnownCapabilities:          map[string]bool{"known": true},
		SupportedDeveloperAPIMajor: 1,
		RevokedBuilds:              map[string]bool{"revoked": true},
		EmbeddedUIRevision:         1,
		PluginPolicyGeneration:     1,
	}
	wantDifferent := []CompatGenerationInputs{
		func() CompatGenerationInputs { v := base; v.BuildRevision = "build-b"; return v }(),
		func() CompatGenerationInputs { v := base; v.CoreRevisions[1] = 2; return v }(),
		func() CompatGenerationInputs { v := base; v.RenderingRevisions[1] = 2; return v }(),
		func() CompatGenerationInputs {
			v := base
			v.RequiredRenderingStamps = []string{"next-stamp"}
			return v
		}(),
		func() CompatGenerationInputs {
			v := base
			v.RequiredVocabularies = []string{"next-vocabulary"}
			return v
		}(),
		func() CompatGenerationInputs {
			v := base
			v.KnownCapabilities = map[string]bool{"other": true}
			return v
		}(),
		func() CompatGenerationInputs { v := base; v.SupportedDeveloperAPIMajor = 2; return v }(),
		func() CompatGenerationInputs { v := base; v.RevokedBuilds = map[string]bool{"other": true}; return v }(),
		func() CompatGenerationInputs { v := base; v.EmbeddedUIRevision = 2; return v }(),
		func() CompatGenerationInputs { v := base; v.PluginPolicyGeneration = 2; return v }(),
	}
	generation := CompatGeneration(base)
	for i, changed := range wantDifferent {
		if CompatGeneration(changed) == generation {
			t.Errorf("durable generation input %d was inert", i)
		}
	}
}

func TestCompatGenerationSeparatesAdjacentSets(t *testing.T) {
	base := CompatGenerationInputs{
		KnownCapabilities: map[string]bool{"known": true},
		RevokedBuilds:     map[string]bool{"revoked": true},
	}
	movedAcrossBoundary := base
	movedAcrossBoundary.KnownCapabilities = map[string]bool{"known": true, "revoked": true}
	movedAcrossBoundary.RevokedBuilds = nil
	if CompatGeneration(base) == CompatGeneration(movedAcrossBoundary) {
		t.Fatal("generation encoding did not preserve the capability/revocation boundary")
	}
}

func TestCompatEveryGenerationInputRefusesSubmittedRecord(t *testing.T) {
	tests := map[string]func(capabilities.Policy, string, int) (capabilities.Policy, string, int){
		"build revision": func(policy capabilities.Policy, _ string, plugin int) (capabilities.Policy, string, int) {
			return policy, "next-build", plugin
		},
		"core revisions": func(policy capabilities.Policy, build string, plugin int) (capabilities.Policy, string, int) {
			policy.SupportedCoreRevisions[1]++
			return policy, build, plugin
		},
		"rendering revisions": func(policy capabilities.Policy, build string, plugin int) (capabilities.Policy, string, int) {
			policy.SupportedRenderingRevisions[1]++
			return policy, build, plugin
		},
		"rendering stamps": func(policy capabilities.Policy, build string, plugin int) (capabilities.Policy, string, int) {
			policy.RequiredRenderingStamps = append(policy.RequiredRenderingStamps, "future-stamp")
			return policy, build, plugin
		},
		"fixed vocabularies": func(policy capabilities.Policy, build string, plugin int) (capabilities.Policy, string, int) {
			policy.RequiredVocabularies = append(policy.RequiredVocabularies, "future-vocabulary")
			return policy, build, plugin
		},
		"capability set": func(policy capabilities.Policy, build string, plugin int) (capabilities.Policy, string, int) {
			policy.KnownCapabilities["compat-test-capability"] = true
			return policy, build, plugin
		},
		"developer API major": func(policy capabilities.Policy, build string, plugin int) (capabilities.Policy, string, int) {
			policy.SupportedDeveloperAPIMajor++
			return policy, build, plugin
		},
		"revoked builds": func(policy capabilities.Policy, build string, plugin int) (capabilities.Policy, string, int) {
			policy.RevokedBuilds["another-build"] = true
			return policy, build, plugin
		},
		"embedded UI revision": func(policy capabilities.Policy, build string, plugin int) (capabilities.Policy, string, int) {
			policy.EmbeddedUIRevision++
			return policy, build, plugin
		},
		"plugin policy": func(policy capabilities.Policy, build string, plugin int) (capabilities.Policy, string, int) {
			return policy, build, plugin + 1
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			srv := compatTestServer(t)
			compatHandshakeRecord(t, srv, compatTestContract("core-web-ui"), authorizeViewerCookies)
			policy := srv.compat.snapshot()
			policy, build, plugin := mutate(policy, srv.buildSHA, 0)
			srv.SetCompatPolicy(policy, build, plugin)

			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, cookieManifestRequest())
			if rec.Code != http.StatusForbidden {
				t.Fatalf("request after %s change = %d, want 403", name, rec.Code)
			}
		})
	}
}

type compatEventStore struct {
	events []store.OutboxEvent
	read   bool
}

func (s *compatEventStore) ReadEventOutbox(context.Context, int64, int) ([]store.OutboxEvent, error) {
	if s.read {
		return nil, nil
	}
	s.read = true
	return append([]store.OutboxEvent(nil), s.events...), nil
}

func (*compatEventStore) Media(context.Context, store.MediaID, store.LibraryFilter) (store.Media, error) {
	return store.Media{}, errors.New("unexpected Media lookup")
}

func (*compatEventStore) LibraryVisible(context.Context, store.LibraryID, store.LibraryFilter) (bool, error) {
	return false, errors.New("unexpected Library lookup")
}

func TestCompatEventGuardRunsBeforeEveryEmission(t *testing.T) {
	srv := compatTestServer(t)
	srv.events = &compatEventStore{events: []store.OutboxEvent{
		{Seq: 1, Kind: "job.first", AggregateType: "job", AggregateID: "one"},
		{Seq: 2, Kind: "job.second", AggregateType: "job", AggregateID: "two"},
	}}
	calls := 0
	guard := func(context.Context) (store.ProfileID, store.LibraryFilter, error) {
		calls++
		if calls == 2 {
			return "", store.LibraryFilter{}, errors.New("decision invalidated")
		}
		return "profile-default", store.LibraryFilter{AllLibraries: true}, nil
	}
	rec := httptest.NewRecorder()
	_, err := srv.drainEvents(context.Background(), rec, rec, "", store.LibraryFilter{}, 0, guard)
	if err == nil {
		t.Fatal("drainEvents succeeded after per-event invalidation")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: job.first") || strings.Contains(body, "event: job.second") {
		t.Fatalf("emitted events after invalidation: %q", body)
	}

	v1 := compatTestServer(t)
	v1.events = &compatEventStore{events: []store.OutboxEvent{
		{Seq: 1, Kind: "job.first", AggregateType: "job", AggregateID: "one"},
		{Seq: 2, Kind: "job.second", AggregateType: "job", AggregateID: "two"},
	}}
	if err := v1.compat.registry.Invalidate(v1.compat.snapshot().Generation + 1); err != nil {
		t.Fatalf("Invalidate() = %v", err)
	}
	v1Rec := httptest.NewRecorder()
	if _, err := v1.drainEvents(
		context.Background(),
		v1Rec,
		v1Rec,
		"",
		store.LibraryFilter{},
		0,
		nil,
	); err != nil {
		t.Fatalf("/v1-compatible drain after invalidation = %v", err)
	}
	if v1Body := v1Rec.Body.String(); !strings.Contains(v1Body, "event: job.first") ||
		!strings.Contains(v1Body, "event: job.second") {
		t.Fatalf("/v1-compatible drain lost events after invalidation: %q", v1Body)
	}
}

func TestCompatUnboundNativeResponseIsUnchanged(t *testing.T) {
	srv := compatTestServer(t)
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}
	before := request()
	policy := srv.compat.snapshot()
	srv.SetCompatPolicy(policy, "next-build", 1)
	after := request()
	if before.Code != after.Code || before.Body.String() != after.Body.String() {
		t.Fatalf("unbound native response changed: before=%d/%q after=%d/%q",
			before.Code, before.Body.String(), after.Code, after.Body.String())
	}
	if before.Header().Get("Cache-Control") != "" || after.Header().Get("Cache-Control") != "" {
		t.Fatalf("unbound native cache headers = %q/%q, want unchanged empty values",
			before.Header().Get("Cache-Control"), after.Header().Get("Cache-Control"))
	}
}

// TestCompatHandshakeRefusesUnverifiedBuildProvenance drives the ADR-0063
// build-provenance gate through the live handshake route: an unknown build, a
// mismatched artifact digest, and a rolled-back build are each refused a
// compatible decision and mint no decision id, while the enrolled build the
// server knows still succeeds.
func TestCompatHandshakeRefusesUnverifiedBuildProvenance(t *testing.T) {
	const surface = "macos-manager"

	t.Run("legitimate enrolled build", func(t *testing.T) {
		srv := compatTestServer(t)
		response := compatHandshakeRecord(t, srv, compatTestContract(surface), authorizeViewer)
		if response.Decision != "compatible" || response.DecisionID == "" {
			t.Fatalf("enrolled build handshake = %#v, want a compatible decision with an id", response)
		}
	})

	cases := []struct {
		name      string
		prepare   func(*Server, *capabilities.ClientContract)
		authorize func(*http.Request)
	}{
		{
			name: "unknown build id",
			prepare: func(_ *Server, contract *capabilities.ClientContract) {
				contract.BuildID = "build-the-server-never-released"
			},
		},
		{
			name: "mismatched artifact hash",
			prepare: func(_ *Server, contract *capabilities.ClientContract) {
				contract.ArtifactHash = compatTestArtifactHash("substituted-artifact")
			},
		},
		{
			name: "rolled back build",
			prepare: func(srv *Server, _ *capabilities.ClientContract) {
				policy := srv.compat.snapshot()
				policy.Builds.MinimumSequence[capabilities.Surface(surface)] = compatTestBuildSequence + 1
				srv.SetCompatPolicy(policy, srv.buildSHA, 0)
			},
		},
		{
			name: "same enrollment classes without an exact build mapping",
			prepare: func(_ *Server, _ *capabilities.ClientContract) {
			},
			authorize: authorizeRevealed,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			srv := compatTestServer(t)
			contract := compatTestContract(surface)
			test.prepare(srv, &contract)

			authorize := test.authorize
			if authorize == nil {
				authorize = authorizeViewer
			}
			response := compatHandshakeRecord(t, srv, contract, authorize)
			if response.Decision == "compatible" || response.Decision == "degraded" {
				t.Fatalf("handshake = %#v, want a refusal", response)
			}
			if response.Decision != "client-too-old" || response.ReasonCode != "unknown-build" {
				t.Fatalf("handshake = %#v, want client-too-old/unknown-build", response)
			}
			if response.DecisionID != "" {
				t.Fatalf("refused handshake minted decision id %q", response.DecisionID)
			}

			bound := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
			authorize(bound)
			boundRec := httptest.NewRecorder()
			srv.ServeHTTP(boundRec, bound)
			if boundRec.Code != http.StatusForbidden {
				t.Fatalf("decision-bound request after refusal = %d, want 403", boundRec.Code)
			}
		})
	}
}

// TestCompatEmbeddedArtifactIsServerSupplied proves the browser bundle this
// binary ships still handshakes with the contract it actually submits (no
// artifact digest of its own), while a native credential cannot claim the
// embedded-only mapping even when it copies the correct build id and digest.
func TestCompatEmbeddedArtifactIsServerSupplied(t *testing.T) {
	browserContract := capabilities.EmbeddedUIContract()
	browserContract.ArtifactHash = ""

	srv := compatTestServer(t)
	accepted := compatHandshakeRecord(t, srv, browserContract, authorizeViewerCookies)
	if accepted.Decision != "compatible" || accepted.DecisionID == "" {
		t.Fatalf("embedded browser handshake = %#v, want a compatible decision", accepted)
	}

	native := compatTestServer(t)
	nativeContract := browserContract
	nativeContract.ArtifactHash = capabilities.EmbeddedUIContract().ArtifactHash
	refused := compatHandshakeRecord(t, native, nativeContract, authorizeViewer)
	if refused.Decision != "client-too-old" || refused.ReasonCode != "unknown-build" {
		t.Fatalf("native handshake claiming the embedded build = %#v, want client-too-old/unknown-build", refused)
	}
	if refused.DecisionID != "" {
		t.Fatalf("refused native handshake minted decision id %q", refused.DecisionID)
	}
}

// The compatibility middleware must hand every bound route the decision
// identity (generation + subject) so downstream cache entries can be keyed on
// it.
func TestCompatDecisionBindingReachesByteRoutes(t *testing.T) {
	srv := newTestServer(t)
	capture := func(req *http.Request) (generation, subject string, bound bool) {
		next := func(_ http.ResponseWriter, r *http.Request) {
			generation, subject, bound = mediahttp.CompatBindingFromContext(r.Context())
		}
		rec := httptest.NewRecorder()
		srv.requireCompatDecision(next)(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("bound request = %d: %s", rec.Code, rec.Body.String())
		}
		return generation, subject, bound
	}

	generation, subject, bound := capture(cookieManifestRequest())
	if !bound {
		t.Fatal("web-credential request carried no compatibility binding")
	}
	if generation == "" {
		t.Fatal("decided request carried no decision generation")
	}
	if subject != "session:session-default" {
		t.Fatalf("binding subject = %q, want the decision's subject", subject)
	}

	policy := srv.compat.snapshot()
	srv.SetCompatPolicy(policy, "next-build", 1)
	renewedGeneration, _, _ := capture(cookieManifestRequest())
	if renewedGeneration == generation {
		t.Fatalf("generation after policy bump = %q, want a value distinct from %q", renewedGeneration, generation)
	}

	managerRequest := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	authorizeManagerCookies(managerRequest)
	_, managerSubject, _ := capture(managerRequest)
	if managerSubject == subject {
		t.Fatalf("second subject binding = %q, want a value distinct from %q", managerSubject, subject)
	}

	// A not-yet-promoted native credential has no rollout surface or decision
	// record on file, so the legacy binding remains incomplete and uncacheable.
	undecided := newTestServer(t)
	nativeRequest := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	authorizeViewer(nativeRequest)
	var nativeGeneration, nativeSubject string
	var nativeBound bool
	nativeRec := httptest.NewRecorder()
	undecided.requireCompatDecision(func(_ http.ResponseWriter, r *http.Request) {
		nativeGeneration, nativeSubject, nativeBound = mediahttp.CompatBindingFromContext(r.Context())
	})(nativeRec, nativeRequest)
	if !nativeBound {
		t.Fatal("native request carried no compatibility binding")
	}
	if nativeGeneration != "" {
		t.Fatalf("undecided native generation = %q, want an unestablished decision", nativeGeneration)
	}
	if nativeSubject == "" {
		t.Fatal("native request binding carried no subject")
	}
}

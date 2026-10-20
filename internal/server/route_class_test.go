package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// TestEveryMountedRouteIsClassified is the GCR-012-A completeness gate: every
// route the server mounts must resolve to a non-empty AccessClass. Arming the
// loopback test-fault control adds the one route that is not mounted by default,
// so the classifier is exercised against the full runtime route table. A new
// route added without a classification fails here.
func TestEveryMountedRouteIsClassified(t *testing.T) {
	srv := newTestServer(t)
	srv.SetTestFaults(true) // mounts POST /api/test/faults so it is covered too.

	sawTestFaults := false
	for _, route := range srv.Routes() {
		if route.Method == http.MethodPost && route.Pattern == "/api/test/faults" {
			sawTestFaults = true
		}
		if route.RouteAccessClass() == AccessClassUnclassified {
			t.Errorf("route %s %s has no AccessClass: classify it in exactRouteClasses (route_class.go) before mounting", route.Method, route.Pattern)
		}
	}
	if !sawTestFaults {
		t.Fatal("armed test-fault route not present in Routes(); completeness gate would silently miss it")
	}
}

// TestAccessClassPolicyConsistency pins the per-class invariants the auth, CORS,
// and CSRF enforcement depends on (GCR-012-B): the public developer line is
// exactly the /v1/ prefix and nothing else, and no private/UI/internal route
// leaks onto it.
func TestAccessClassPolicyConsistency(t *testing.T) {
	srv := newTestServer(t)
	srv.SetTestFaults(true)

	for _, route := range srv.Routes() {
		underV1 := strings.HasPrefix(route.Pattern, "/v1/")
		isPublic := route.RouteAccessClass() == AccessPublicV1
		if underV1 != isPublic {
			t.Errorf("route %s %s: /v1 prefix=%v but public-v1 class=%v — the public line must be exactly the /v1/ surface", route.Method, route.Pattern, underV1, isPublic)
		}
	}

	// exactRouteClasses must never classify a /v1 route: /v1 is structural, and a
	// literal /v1 entry here would let the map and the prefix rule disagree.
	for key := range exactRouteClasses {
		if strings.Contains(key, " /v1/") {
			t.Errorf("exactRouteClasses must not list a /v1 route (%q); /v1 is classified structurally", key)
		}
	}
}

func TestAccessClassPolicyMatrix(t *testing.T) {
	srv := newTestServer(t)
	type policyCase struct {
		name                   string
		class                  AccessClass
		method                 string
		path                   string
		policyOnly             bool
		crossCredential        string
		cookieCredential       string
		wantAnonymous          int
		wantCrossOrigin        int
		wantAllowOrigin        string
		wantCookieMutation     int
		cookieUsesMountedRoute bool
	}
	// #nosec G101 -- fixture tokens below are deliberately invalid bearer strings, not credentials.
	tests := []policyCase{
		{
			name:               "public-v1",
			class:              AccessPublicV1,
			method:             http.MethodGet,
			path:               "/v1/libraries",
			crossCredential:    "Bearer bfk_invalid",
			cookieCredential:   "Bearer bfk_invalid",
			wantAnonymous:      http.StatusUnauthorized,
			wantCrossOrigin:    http.StatusUnauthorized,
			wantAllowOrigin:    "*",
			wantCookieMutation: http.StatusNoContent,
		},
		{
			name:                   "private-ui",
			class:                  AccessPrivateUI,
			method:                 http.MethodGet,
			path:                   "/api/manifest",
			wantAnonymous:          http.StatusUnauthorized,
			wantCrossOrigin:        http.StatusForbidden,
			wantCookieMutation:     http.StatusForbidden,
			cookieUsesMountedRoute: true,
		},
		{
			name:               "admin-automation",
			class:              AccessAdminAutomation,
			method:             http.MethodGet,
			path:               "/",
			policyOnly:         true,
			crossCredential:    "Bearer admin-token",
			cookieCredential:   "Bearer admin-token",
			wantAnonymous:      http.StatusUnauthorized,
			wantCrossOrigin:    http.StatusForbidden,
			wantCookieMutation: http.StatusNoContent,
		},
		{
			name:               "internal-only",
			class:              AccessInternalOnly,
			method:             http.MethodGet,
			path:               "/api/setup",
			wantAnonymous:      http.StatusOK,
			wantCrossOrigin:    http.StatusForbidden,
			wantCookieMutation: http.StatusNoContent,
		},
	}

	serve := func(tc policyCase, req *http.Request, policyOnly bool) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		if policyOnly {
			if applyAccessPolicy(rec, req, tc.class) {
				rec.WriteHeader(http.StatusNoContent)
			}
		} else {
			srv.ServeHTTP(rec, req)
		}
		return rec
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			anonymous := httptest.NewRequest(tc.method, tc.path, nil)
			if rec := serve(tc, anonymous, tc.policyOnly); rec.Code != tc.wantAnonymous {
				t.Errorf("unauthenticated status = %d, want %d", rec.Code, tc.wantAnonymous)
			}

			cross := httptest.NewRequest(tc.method, tc.path, nil)
			cross.Host = "beamfall.test"
			cross.Header.Set("Origin", "https://evil.test")
			cross.Header.Set("Authorization", tc.crossCredential)
			crossRec := serve(tc, cross, tc.policyOnly)
			if crossRec.Code != tc.wantCrossOrigin {
				t.Errorf("cross-origin status = %d, want %d", crossRec.Code, tc.wantCrossOrigin)
			}
			if got := crossRec.Header().Get("Access-Control-Allow-Origin"); got != tc.wantAllowOrigin {
				t.Errorf("cross-origin allow-origin = %q, want %q", got, tc.wantAllowOrigin)
			}

			cookiePath := "/"
			cookiePolicyOnly := true
			if tc.cookieUsesMountedRoute {
				cookiePath = "/api/graphql"
				cookiePolicyOnly = false
			}
			cookieMutation := newJSONRequest(http.MethodPost, cookiePath, nil)
			addViewerCookies(cookieMutation)
			cookieMutation.Header.Set("Authorization", tc.cookieCredential)
			if rec := serve(tc, cookieMutation, cookiePolicyOnly); rec.Code != tc.wantCookieMutation {
				t.Errorf("cookie mutation without CSRF status = %d, want %d", rec.Code, tc.wantCookieMutation)
			}
		})
	}
}

// TestScopedKeySurfaceSplit pins the ADR-0040 surface boundary end to end:
// one admin-scoped read-catalog key
// is authorized on the matching /v1/ route, refused on a /v1/ route outside
// its scope set, and refused outright on the private GraphQL surface — for a
// mutation and for a read alike — with no resolver side effect.
func TestScopedKeySurfaceSplit(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	key := mintKey(t, st, "surface-split", "", store.ScopeReadCatalog)

	if rec := v1Get(t, srv, "/v1/libraries", key); rec.Code != http.StatusOK {
		t.Fatalf("read-catalog key GET /v1/libraries = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := v1Get(t, srv, "/v1/sessions", key); rec.Code != http.StatusForbidden {
		t.Fatalf("read-catalog key GET /v1/sessions = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	mutation := `{"query":"mutation C($name:String!){ createTag(name:$name){ id } }","variables":{"name":"Boundary Probe"}}`
	if rec := v1Post(t, srv, "/api/graphql", key, mutation); rec.Code != http.StatusForbidden {
		t.Fatalf("scoped key POST /api/graphql mutation = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if rec := v1Post(t, srv, "/api/graphql", key, `{"query":"query { libraries { id } }"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("scoped key POST /api/graphql query = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	tags, err := st.Tags(context.Background(), true)
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("tags = %+v, want none — the scoped key reached the createTag resolver", tags)
	}
}

func TestPackKeyIsNotGenericCrossOriginByteStreaming(t *testing.T) {
	srv := newTestServer(t)

	key := httptest.NewRequest(http.MethodGet, "/api/pack/key/grant", nil)
	key.Host = "beamfall.test"
	key.Header.Set("Origin", "https://evil.test")
	key.Header.Set("X-Beamfall-Session", "session-default")
	keyRec := httptest.NewRecorder()
	srv.ServeHTTP(keyRec, key)
	if keyRec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin pack key status = %d, want 403", keyRec.Code)
	}
	if got := keyRec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("cross-origin pack key allow-origin = %q, want empty", got)
	}

	content := httptest.NewRequest(http.MethodGet, "/api/pack/content/grant/media", nil)
	content.Host = "beamfall.test"
	content.Header.Set("Origin", "https://evil.test")
	contentRec := httptest.NewRecorder()
	srv.ServeHTTP(contentRec, content)
	if got := contentRec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("cross-origin encrypted pack content allow-origin = %q, want *", got)
	}
}

// v1PublicLine is the frozen set of public /v1 route patterns (GCR-012-D): the
// deliberate private->public promotion boundary. Promoting a route onto /v1 is a
// contract change (docs/api/openapi.json is the pinned wire line), so it must be
// a deliberate edit here — this test fails if the mounted /v1 surface drifts.
var v1PublicLine = []string{
	"GET /v1/libraries",
	"GET /v1/libraries/{libraryID}/media",
	"GET /v1/media/{mediaID}",
	"GET /v1/sessions",
	"POST /v1/playback-control",
	"POST /v1/scans",
	"GET /v1/backups/{libraryID}/status",
	"POST /v1/backups/media",
	"GET /v1/events",
}

// TestV1RouteSetIsPinned pins the public /v1 line: the mounted /v1 routes must
// equal v1PublicLine exactly. Adding or removing a /v1 route without updating
// the pin (and the promotion rule in docs/api/route-classes.md) fails here.
func TestV1RouteSetIsPinned(t *testing.T) {
	srv := newTestServer(t)

	var mounted []string
	for _, route := range srv.Routes() {
		if route.RouteAccessClass() == AccessPublicV1 {
			mounted = append(mounted, route.Method+" "+route.Pattern)
		}
	}

	want := append([]string(nil), v1PublicLine...)
	sort.Strings(mounted)
	sort.Strings(want)

	if len(mounted) != len(want) {
		t.Fatalf("public /v1 line drifted: mounted %d routes %v, pinned %d %v", len(mounted), mounted, len(want), want)
	}
	for i := range want {
		if mounted[i] != want[i] {
			t.Errorf("public /v1 line mismatch at %d: mounted %q, pinned %q", i, mounted[i], want[i])
		}
	}
}

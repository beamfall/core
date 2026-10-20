package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/beam"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/observability"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/reveal"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

type testServerGraphHandler struct {
	*graph.Handler
}

type sequencedSessionRevokerJobStats struct {
	*store.Store
	errors []error
	calls  int
}

func (r *sequencedSessionRevokerJobStats) TerminateSession(context.Context, store.SessionID) error {
	err := r.errors[r.calls]
	r.calls++
	return err
}

func (h testServerGraphHandler) FinishAuditedCommand(
	ctx context.Context,
	scope auth.Scope,
	operation, operationID string,
	outcome AuditOutcome,
) error {
	return h.Handler.FinishAuditedCommand(
		ctx,
		scope,
		operation,
		operationID,
		observability.AuditOutcome(outcome),
	)
}

func newJSONRequest(method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestRoutesAreRecorded(t *testing.T) {
	srv := newTestServer(t)
	got := srv.Routes()
	want := []Route{
		{Method: http.MethodGet, Pattern: "/health"},
		{Method: http.MethodGet, Pattern: "/metrics", RevealClass: reveal.Aggregate},
		{Method: http.MethodGet, Pattern: "/source"},
		{Method: http.MethodPost, Pattern: "/api/graphql", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/api/compat/handshake", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/api/media/artwork-upload/{recordID}", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/api/manifest", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/api/playback-info", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/api/preview-info", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/api/beam/offers", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/api/beam/claims", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/api/pairing-codes", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/api/enroll", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/api/moderation/report", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/api/moderation/block", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/api/moderation/blocks", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/api/moderation/contact"},
		{Method: http.MethodGet, Pattern: "/api/push/notifications/{id}", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/api/media/direct/", RevealClass: reveal.ContentBytes},
		{Method: http.MethodGet, Pattern: "/api/media/artwork-candidate/", RevealClass: reveal.ContentBytes},
		{Method: http.MethodGet, Pattern: "/api/media/artwork/", RevealClass: reveal.ContentBytes},
		{Method: http.MethodPost, Pattern: "/api/media/artwork/upload", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/api/entity/artwork/", RevealClass: reveal.ContentBytes},
		{Method: http.MethodGet, Pattern: "/api/media/hls/", RevealClass: reveal.ContentBytes},
		{Method: http.MethodGet, Pattern: "/api/media/assets/", RevealClass: reveal.ContentBytes},
		{Method: http.MethodGet, Pattern: "/api/media/subtitle/", RevealClass: reveal.ContentBytes},
		{Method: http.MethodGet, Pattern: "/api/media/sidecar/", RevealClass: reveal.ContentBytes},
		{Method: http.MethodPost, Pattern: "/api/media/subtitle-pin", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/api/media/subtitle-offset", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/api/media/proxy/", RevealClass: reveal.ContentBytes},
		{Method: http.MethodGet, Pattern: "/api/pack/authority", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/api/pack/manifest/", RevealClass: reveal.ContentBytes},
		{Method: http.MethodGet, Pattern: "/api/pack/content/", RevealClass: reveal.ContentBytes},
		{Method: http.MethodGet, Pattern: "/api/pack/key/", RevealClass: reveal.ContentBytes},
		{Method: http.MethodGet, Pattern: "/api/pack/status/", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/{$}"},
		{Method: http.MethodGet, Pattern: "/web/", RevealClass: reveal.ContentBytes},
		{Method: http.MethodPost, Pattern: "/api/web/enroll", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/api/web/logout", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/api/web/promote", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/api/setup", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/api/setup"},
		{Method: http.MethodGet, Pattern: "/api/events", RevealClass: reveal.EventFanout},
		{Method: http.MethodPost, Pattern: "/api/group/calibrate", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/v1/libraries", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/v1/libraries/{libraryID}/media", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/v1/media/{mediaID}", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/v1/sessions", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/v1/playback-control", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/v1/scans", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/v1/backups/{libraryID}/status", RevealClass: reveal.ContentJSON},
		{Method: http.MethodPost, Pattern: "/v1/backups/media", RevealClass: reveal.ContentJSON},
		{Method: http.MethodGet, Pattern: "/v1/events", RevealClass: reveal.EventFanout},
	}
	if len(got) != len(want) {
		t.Fatalf("len(Routes) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Routes()[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestRouteSurfacesAreDerivedFromMountedRoutes(t *testing.T) {
	srv := newTestServer(t)
	got := srv.Surfaces()
	var want []reveal.Surface
	for _, route := range srv.Routes() {
		if route.RevealClass == "" {
			continue
		}
		want = append(want, reveal.Surface{
			ID:    route.Method + " " + route.Pattern,
			Class: route.RevealClass,
		})
	}
	if len(got) != len(want) {
		t.Fatalf("len(Surfaces) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Surfaces()[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

// TestProxyRouteDispatchesAndDegrades covers the proxy-tee route wiring:
// unwired it degrades to 503 (honest-degrade, like the V1 read
// routes), and once SetProxyTee is wired the route dispatches to the handler.
func TestProxyRouteDispatchesAndDegrades(t *testing.T) {
	srv := newTestServer(t)

	// Unwired: 503, never a 404/panic.
	unwired := httptest.NewRequest(http.MethodGet, "/api/media/proxy/some-token", nil)
	unwiredRec := httptest.NewRecorder()
	srv.ServeHTTP(unwiredRec, unwired)
	if unwiredRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired status = %d, want 503", unwiredRec.Code)
	}

	// Wired: the route dispatches to the supplied handler.
	dispatched := false
	srv.SetProxyTee(func(w http.ResponseWriter, r *http.Request) {
		dispatched = true
		w.WriteHeader(http.StatusOK)
	})
	wired := httptest.NewRequest(http.MethodGet, "/api/media/proxy/some-token", nil)
	wiredRec := httptest.NewRecorder()
	srv.ServeHTTP(wiredRec, wired)
	if !dispatched || wiredRec.Code != http.StatusOK {
		t.Fatalf("wired dispatch=%v status=%d, want dispatched + 200", dispatched, wiredRec.Code)
	}
}

func TestProxyRouteStatusesArePrivateAndUnstorable(t *testing.T) {
	srv := &Server{}
	for _, tc := range []struct {
		status int
		wired  bool
	}{
		{status: http.StatusOK, wired: true},
		{status: http.StatusPartialContent, wired: true},
		{status: http.StatusRequestedRangeNotSatisfiable, wired: true},
		{status: http.StatusUnauthorized, wired: true},
		{status: http.StatusNotFound, wired: true},
		{status: http.StatusBadGateway, wired: true},
		{status: http.StatusServiceUnavailable},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			if tc.wired {
				srv.proxyTee = func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.status)
				}
			} else {
				srv.proxyTee = nil
			}
			rec := httptest.NewRecorder()

			srv.proxyStream(rec, httptest.NewRequest(http.MethodGet, "/api/media/proxy/some-token", nil))

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if got, want := rec.Header().Get("Cache-Control"), "private, no-store"; got != want {
				t.Fatalf("Cache-Control = %q, want %q", got, want)
			}
		})
	}
}

func TestStreamTokenByteRouteErrorsArePrivateAndUnstorable(t *testing.T) {
	srv := newTestServer(t)
	srv.SetProxyTee(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "missing stream token", http.StatusUnauthorized)
	})
	paths := []string{
		"/api/media/assets/",
		"/api/media/subtitle/",
		"/api/media/sidecar/",
		"/api/media/hls/",
		"/api/media/artwork-candidate/",
		"/api/media/direct/",
		"/api/media/proxy/",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()

			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if got, want := rec.Header().Get("Cache-Control"), "private, no-store"; got != want {
				t.Fatalf("Cache-Control = %q, want %q", got, want)
			}
		})
	}
}

func TestLogRequestFailureRedactsTokenPathForClientFailures(t *testing.T) {
	const token = "canary-stream-token-0xDEAD"
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			logs.Reset()
			req := httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil)

			logRequestFailure(context.Background(), req, status, "request refused", nil)

			logOut := logs.String()
			if strings.Contains(logOut, token) {
				t.Fatalf("%d log leaked the token: %q", status, logOut)
			}
			if !strings.Contains(logOut, "path=/api/media/direct/[redacted]") {
				t.Fatalf("%d log lost the redacted route: %q", status, logOut)
			}
			if !strings.Contains(logOut, fmt.Sprintf("status=%d", status)) {
				t.Fatalf("log = %q, want status %d", logOut, status)
			}
		})
	}
}

func TestPackAuthorityRouteDispatchesAndDegrades(t *testing.T) {
	srv := newTestServer(t)

	unwired := httptest.NewRequest(http.MethodGet, "/api/pack/authority", nil)
	unwiredRec := httptest.NewRecorder()
	srv.ServeHTTP(unwiredRec, unwired)
	if unwiredRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired status = %d, want 503", unwiredRec.Code)
	}

	dispatched := false
	srv.SetPackHandlers(func(w http.ResponseWriter, _ *http.Request) {
		dispatched = true
		w.WriteHeader(http.StatusOK)
	}, nil, nil, nil, nil)
	wired := httptest.NewRequest(http.MethodGet, "/api/pack/authority", nil)
	wiredRec := httptest.NewRecorder()
	srv.ServeHTTP(wiredRec, wired)
	if !dispatched || wiredRec.Code != http.StatusOK {
		t.Fatalf("wired dispatch=%v status=%d, want dispatched + 200", dispatched, wiredRec.Code)
	}
}

func TestSetupRequiredProbe(t *testing.T) {
	probe := func(srv *Server) bool {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/setup", nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var body map[string]bool
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode setup probe: %v", err)
		}
		if _, ok := body["required"]; !ok || len(body) != 1 {
			t.Fatalf("body = %#v, want exactly {\"required\": bool}", body)
		}
		return body["required"]
	}

	// Fresh server with the bootstrap token installed: setup is required. The
	// probe must answer without any session or CSRF header (unenrolled browser).
	srv := newTestServer(t)
	srv.pairing.SetBootstrapToken("first-run-token")
	if required := probe(srv); !required {
		t.Fatalf("required = false, want true while the setup token is installed")
	}

	// After the token is consumed (here: cleared, as a successful Bootstrap
	// would), the probe reports setup is no longer required.
	srv.pairing.SetBootstrapToken("")
	if required := probe(srv); required {
		t.Fatalf("required = true, want false after the setup token is consumed")
	}
}

func TestManagementRouteContractsAreMounted(t *testing.T) {
	srv := newTestServer(t)
	mounted := make(map[string]Route, len(srv.Routes()))
	for _, route := range srv.Routes() {
		mounted[route.Method+" "+route.Pattern] = route
	}
	for _, contract := range ManagementRouteContracts() {
		key := contract.Method + " " + contract.Pattern
		route, ok := mounted[key]
		if !ok {
			t.Errorf("published management route is not mounted: %s", key)
			continue
		}
		if route.RouteAccessClass() == AccessClassUnclassified {
			t.Errorf("published management route is unclassified: %s", key)
		}
	}
}

func TestManifestRoute(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var doc manifest.Document
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if doc.ProfileID != "profile-default" {
		t.Fatalf("ProfileID = %q, want profile-default", doc.ProfileID)
	}
	if len(doc.Libraries) != 1 {
		t.Fatalf("len(Libraries) = %d, want 1", len(doc.Libraries))
	}
}

func TestManifestRouteRequiresSession(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestPushNotificationFetchRequiresAuthorizedSession(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	if err := st.CreateNotification(ctx, store.Notification{
		ID:                 "note-open",
		RecipientProfileID: "profile-default",
		Kind:               store.NotificationSystem,
		Title:              "Ready",
		Body:               "Open body",
		Ref:                "ref-open",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateNotification(ctx, store.Notification{
		ID:                 "note-managed",
		RecipientProfileID: "profile-managed",
		Kind:               store.NotificationSystem,
		Title:              "Managed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateNotification(ctx, store.Notification{
		ID:                 "note-restricted",
		RecipientProfileID: "profile-default",
		Kind:               store.NotificationSystem,
		Title:              "Restricted",
		LibraryID:          "library-archive",
	}); err != nil {
		t.Fatal(err)
	}

	unauth := httptest.NewRequest(http.MethodGet, "/api/push/notifications/note-open", nil)
	unauthRec := httptest.NewRecorder()
	srv.ServeHTTP(unauthRec, unauth)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d, want 401", unauthRec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/push/notifications/note-open", nil)
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("open status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body pushNotificationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != "note-open" || body.Kind != string(store.NotificationSystem) || body.Title != "Ready" || body.Body != "Open body" || body.Ref != "ref-open" {
		t.Fatalf("body = %#v", body)
	}

	cross := httptest.NewRequest(http.MethodGet, "/api/push/notifications/note-managed", nil)
	authorizeViewer(cross)
	crossRec := httptest.NewRecorder()
	srv.ServeHTTP(crossRec, cross)
	if crossRec.Code != http.StatusNotFound {
		t.Fatalf("cross-profile status = %d, want 404", crossRec.Code)
	}

	hidden := httptest.NewRequest(http.MethodGet, "/api/push/notifications/note-restricted", nil)
	authorizeViewer(hidden)
	hiddenRec := httptest.NewRecorder()
	srv.ServeHTTP(hiddenRec, hidden)
	if hiddenRec.Code != http.StatusNotFound {
		t.Fatalf("hidden restricted status = %d, want 404", hiddenRec.Code)
	}

	revealed := httptest.NewRequest(http.MethodGet, "/api/push/notifications/note-restricted", nil)
	authorizeRevealed(revealed)
	revealedRec := httptest.NewRecorder()
	srv.ServeHTTP(revealedRec, revealed)
	if revealedRec.Code != http.StatusOK {
		t.Fatalf("revealed restricted status = %d, want 200: %s", revealedRec.Code, revealedRec.Body.String())
	}
}

func TestManifestRouteWithRevealedSession(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	authorizeRevealed(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var doc manifest.Document
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(doc.Libraries) != 2 {
		t.Fatalf("len(Libraries) = %d, want 2", len(doc.Libraries))
	}
}

func TestMetricsRoute(t *testing.T) {
	srv := newTestServer(t)
	// A scrape now has to carry the credential even from this host, so this
	// body-shape test presents it rather than asserting over a 403 body.
	req := authorizedScrape(t, srv)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "beamfall_up 1\n") {
		t.Fatalf("metrics body = %q, want beamfall_up metric first", body)
	}
	// With the job store wired but empty, the ADR-0146 §8 queue-depth SLI reads
	// zero — a real gauge value (empty queue), not an omitted line.
	if !strings.Contains(body, "beamfall_jobs_queue_depth 0\n") {
		t.Fatalf("metrics body = %q, want beamfall_jobs_queue_depth 0", body)
	}
}

func TestMetricsRejectsRemoteByDefault(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "203.0.113.7:5000"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestMetricsRejectsRelayedLoopbackByDefault(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	req = req.WithContext(ContextWithRelayedRequest(req.Context()))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestMetricsAllowsRemoteWithExplicitPublicOptIn(t *testing.T) {
	srv := newTestServer(t)
	srv.SetPublicMetrics(true)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "203.0.113.7:5000"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestMetricsRefusesEveryClientWithoutTheCredential covers the GCR-012-H
// invariant across every request shape the deployment can produce. Each shape
// is driven twice against the same server: once with no Authorization header,
// and once presenting the installed credential.
//
// The uncredentialed direction is 403 for every shape, including a client that
// really is on this host with no hop header at all — the shape round 2 served
// and the operator withdrew on 2026-08-07, because a same-host `proxy_pass`
// with no declared CIDR produces that identical request and no predicate
// separates them. The credentialed direction is 200 for every shape, which is
// the same statement read the other way: nothing about the peer address, the
// hop headers, or BEAMFALL_KNOWN_PROXIES moves the outcome, so the invariant
// does not depend on any of them being configured. Only the public opt-in rows
// are served uncredentialed, and that is the operator asking for it explicitly.
func TestMetricsRefusesEveryClientWithoutTheCredential(t *testing.T) {
	tests := []struct {
		name               string
		remoteAddr         string
		header             http.Header
		declaredProxy      bool
		publicMetrics      bool
		relayed            bool
		wantUncredentialed int
	}{
		{
			name:               "client on this host",
			remoteAddr:         "127.0.0.1:5000",
			wantUncredentialed: http.StatusForbidden,
		},
		{
			name:               "client on this host with a blank hop header",
			remoteAddr:         "127.0.0.1:5000",
			header:             http.Header{"X-Forwarded-For": {""}},
			wantUncredentialed: http.StatusForbidden,
		},
		{
			name:               "undeclared same-host proxy on its forwarded-for hop",
			remoteAddr:         "127.0.0.1:5000",
			header:             http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			wantUncredentialed: http.StatusForbidden,
		},
		{
			name:               "undeclared same-host proxy on its via hop",
			remoteAddr:         "127.0.0.1:5000",
			header:             http.Header{"Via": {"1.1 gateway"}},
			wantUncredentialed: http.StatusForbidden,
		},
		{
			name:               "declared same-host proxy with no hop header at all",
			remoteAddr:         "127.0.0.1:5000",
			declaredProxy:      true,
			wantUncredentialed: http.StatusForbidden,
		},
		{
			name:               "declared same-host proxy for the client it resolves",
			remoteAddr:         "127.0.0.1:5000",
			header:             http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			declaredProxy:      true,
			wantUncredentialed: http.StatusForbidden,
		},
		{
			name:               "declared same-host proxy on a repeated forwarded-for header",
			remoteAddr:         "127.0.0.1:5000",
			header:             http.Header{"X-Forwarded-For": {"203.0.113.7", "198.51.100.9"}},
			declaredProxy:      true,
			wantUncredentialed: http.StatusForbidden,
		},
		{
			name:               "declared same-host proxy on an unparseable forwarded-for value",
			remoteAddr:         "127.0.0.1:5000",
			header:             http.Header{"X-Forwarded-For": {"not-an-address"}},
			declaredProxy:      true,
			wantUncredentialed: http.StatusForbidden,
		},
		{
			name:               "off-host peer",
			remoteAddr:         "203.0.113.7:5000",
			wantUncredentialed: http.StatusForbidden,
		},
		{
			name:               "relay-fronted loopback peer",
			remoteAddr:         "127.0.0.1:5000",
			relayed:            true,
			wantUncredentialed: http.StatusForbidden,
		},
		{
			name:               "public opt-in serves an off-host peer",
			remoteAddr:         "203.0.113.7:5000",
			publicMetrics:      true,
			wantUncredentialed: http.StatusOK,
		},
		{
			name:               "public opt-in serves a proxied client",
			remoteAddr:         "127.0.0.1:5000",
			header:             http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			declaredProxy:      true,
			publicMetrics:      true,
			wantUncredentialed: http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			credential := scrapeProbeValue(t)
			scrape := func(authorization string) *httptest.ResponseRecorder {
				srv := newTestServer(t)
				srv.SetPublicMetrics(tt.publicMetrics)
				if tt.declaredProxy {
					proxies, err := ParseKnownProxies("127.0.0.0/8", false)
					if err != nil {
						t.Fatal(err)
					}
					srv.knownProxies = proxies
				}
				srv.SetMetricsScrapeToken(credential)
				req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
				req.RemoteAddr = tt.remoteAddr
				for name, values := range tt.header {
					for _, value := range values {
						req.Header.Add(name, value)
					}
				}
				if authorization != "" {
					req.Header.Set("Authorization", authorization)
				}
				if tt.relayed {
					req = req.WithContext(ContextWithRelayedRequest(req.Context()))
				}
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				return rec
			}
			if rec := scrape(""); rec.Code != tt.wantUncredentialed {
				t.Fatalf("no credential: status = %d, want %d; body %q", rec.Code, tt.wantUncredentialed, rec.Body.String())
			}
			// The credential is the only way in that does not depend on the
			// operator having opted the surface public, so it must work from
			// every shape above — otherwise the refusals are just an outage.
			if rec := scrape("Bearer " + credential); rec.Code != http.StatusOK {
				t.Fatalf("with the credential: status = %d, want %d; body %q", rec.Code, http.StatusOK, rec.Body.String())
			}
		})
	}
}

// TestProxiedRequestStillReachesOtherInternalRoutes keeps the GCR-012-H
// tightening route-local. /health, the AGPL source offer, and the first-run
// probe share /metrics' access class but carry no library data, so they must
// stay reachable through the reverse proxy the deployment runbook recommends.
func TestProxiedRequestStillReachesOtherInternalRoutes(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/health", "/api/setup", "/api/moderation/contact"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.RemoteAddr = "127.0.0.1:5000"
			req.Header.Set("X-Forwarded-For", "203.0.113.7")
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code == http.StatusForbidden {
				t.Fatalf("proxied request to %s was refused: %s", path, rec.Body.String())
			}
		})
	}
}

func TestGraphQLRoute(t *testing.T) {
	srv := newTestServer(t)
	req := newJSONRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":"query { serverInfo { name } }"}`)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data map[string]map[string]string `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode GraphQL response: %v", err)
	}
	if resp.Data["serverInfo"]["name"] != "Beamfall" {
		t.Fatalf("serverInfo.name = %q, want Beamfall", resp.Data["serverInfo"]["name"])
	}
}

func TestCORSAllowsForeignOriginOnTokenByteGET(t *testing.T) {
	srv := newTestServerWithMediaFile(t, "media-alpha", []byte("beamfall file bytes"))
	body := []byte(`{"mediaID":"media-alpha","capabilityProfile":{"client":"beamfall-test","containers":["mp4"],"directCodecs":["h264"]}}`)
	infoReq := newJSONRequest(http.MethodPost, "/api/playback-info", bytes.NewReader(body))
	authorizeViewer(infoReq)
	infoRec := httptest.NewRecorder()
	srv.ServeHTTP(infoRec, infoReq)
	if infoRec.Code != http.StatusOK {
		t.Fatalf("playback-info status = %d, want %d: %s", infoRec.Code, http.StatusOK, infoRec.Body.String())
	}
	var resp playback.InfoResponse
	if err := json.Unmarshal(infoRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode playbackInfo: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, resp.Streams[0].URL, nil)
	req.Host = "beamfall.test"
	req.Header.Set("Origin", "https://www.gstatic.com")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("media status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if rec.Body.String() != "beamfall file bytes" {
		t.Fatalf("media body = %q, want file bytes", rec.Body.String())
	}
}

func TestCORSExemptsOnlyOrdinaryAPIContentByteGETRoutes(t *testing.T) {
	srv := newTestServer(t)
	for _, route := range srv.Routes() {
		if route.Method != http.MethodGet || route.RevealClass != reveal.ContentBytes || !strings.HasPrefix(route.Pattern, "/api/") {
			continue
		}
		req := httptest.NewRequest(http.MethodGet, route.Pattern+"token", nil)
		want := route.Pattern != "/api/pack/key/"
		if got := tokenByteGETPath(req); got != want {
			t.Fatalf("tokenByteGETPath(%q) = %v, want %v", req.URL.Path, got, want)
		}
	}
}

func TestCORSAllowsSameHostOriginAndPreflight(t *testing.T) {
	srv := newTestServer(t)
	req := newJSONRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":"query { serverInfo { name } }"}`)))
	req.Host = "beamfall.test"
	req.Header.Set("Origin", "https://beamfall.test")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://beamfall.test" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/api/graphql", nil)
	preflight.Host = "beamfall.test"
	preflight.Header.Set("Origin", "https://beamfall.test")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	pre := httptest.NewRecorder()
	srv.ServeHTTP(pre, preflight)
	if pre.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want %d", pre.Code, http.StatusNoContent)
	}
	if got := pre.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, auth.CSRFHeader) || !strings.Contains(got, "Content-Type") || !strings.Contains(got, "Authorization") {
		t.Fatalf("Access-Control-Allow-Headers = %q", got)
	}
}

func TestJSONPostRequiresJSONContentType(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":"query { serverInfo { name } }"}`)))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnsupportedMediaType)
	}
}

// TestJSONEndpointsRejectOversizedBody asserts the request-body size caps: a
// body past the cap on a JSON control endpoint (/api/enroll) and on the GraphQL
// endpoint (/api/graphql) is rejected with 413 by http.MaxBytesReader before the
// handler decodes — bounding an unauthenticated memory-exhaustion DoS.
func TestJSONEndpointsRejectOversizedBody(t *testing.T) {
	srv := newTestServer(t)

	// One byte past the 1 MiB control cap, wrapped as valid-looking JSON so a
	// 413 can only come from the size cap, not a parse error.
	enrollBody := append([]byte(`{"deviceName":"`), bytes.Repeat([]byte("a"), maxControlBody+1)...)
	enrollBody = append(enrollBody, []byte(`"}`)...)
	enrollReq := newJSONRequest(http.MethodPost, "/api/enroll", bytes.NewReader(enrollBody))
	enrollRec := httptest.NewRecorder()
	srv.ServeHTTP(enrollRec, enrollReq)
	if enrollRec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("/api/enroll oversized status = %d, want %d", enrollRec.Code, http.StatusRequestEntityTooLarge)
	}

	graphBody := append([]byte(`{"query":"`), bytes.Repeat([]byte("a"), 256<<10+1)...)
	graphBody = append(graphBody, []byte(`"}`)...)
	graphReq := newJSONRequest(http.MethodPost, "/api/graphql", bytes.NewReader(graphBody))
	graphRec := httptest.NewRecorder()
	srv.ServeHTTP(graphRec, graphReq)
	if graphRec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("/api/graphql oversized status = %d, want %d", graphRec.Code, http.StatusRequestEntityTooLarge)
	}
}

// TestPlaybackInfoRequestPreservesTier pins the HTTP leg of the ADR-0156 §4
// contract: the optional model-tier field survives the exact decode the
// /api/playback-info handler performs (json.Decoder into playback.InfoRequest),
// and a request that omits it decodes to the empty tier rather than failing.
// Dropping the wire tag on CapabilityProfile.Tier reds this test.
func TestPlaybackInfoRequestPreservesTier(t *testing.T) {
	const body = `{"mediaID":"media-alpha","capabilityProfile":{"client":"beamfall-roku","tier":"current","containers":["mp4"],"directCodecs":["h264"]}}`

	var req playback.InfoRequest
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&req); err != nil {
		t.Fatalf("decode playback-info request: %v", err)
	}
	if req.CapabilityProfile.Client != "beamfall-roku" {
		t.Fatalf("Client = %q, want beamfall-roku", req.CapabilityProfile.Client)
	}
	if req.CapabilityProfile.Tier != "current" {
		t.Fatalf("Tier = %q, want current (optional tier must survive the request decode)", req.CapabilityProfile.Tier)
	}

	const untiered = `{"mediaID":"media-alpha","capabilityProfile":{"client":"beamfall-roku","containers":["mp4"],"directCodecs":["h264"]}}`
	var legacy playback.InfoRequest
	if err := json.NewDecoder(strings.NewReader(untiered)).Decode(&legacy); err != nil {
		t.Fatalf("decode untiered playback-info request: %v", err)
	}
	if legacy.CapabilityProfile.Tier != "" {
		t.Fatalf("Tier = %q, want empty for a request that omits it", legacy.CapabilityProfile.Tier)
	}
}

func TestPlaybackInfoRoute(t *testing.T) {
	srv := newTestServerWithMediaFile(t, "media-alpha", []byte("beamfall file bytes"))
	body := []byte(`{"mediaID":"media-alpha","capabilityProfile":{"client":"beamfall-test","tier":"current","containers":["mp4"],"directCodecs":["h264"]}}`)
	req := newJSONRequest(http.MethodPost, "/api/playback-info", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp playback.InfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode playbackInfo: %v", err)
	}
	if resp.MediaID != "media-alpha" {
		t.Fatalf("MediaID = %q, want media-alpha", resp.MediaID)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play", resp.Decision)
	}
	// Direct choice plus the manual-rendition HLS pin.
	if len(resp.Streams) != 2 || resp.Streams[0].Kind != "direct-play" {
		t.Fatalf("Streams = %#v, want direct-play first of two", resp.Streams)
	}
	req = httptest.NewRequest(http.MethodGet, resp.Streams[0].URL, nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("media status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "beamfall file bytes" {
		t.Fatalf("media body = %q, want file bytes", rec.Body.String())
	}
}

func TestPreviewInfoRouteIssuesOnlyAssetToken(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	path := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(path, []byte("beamfall file bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), "media-alpha", path); err != nil {
		t.Fatal(err)
	}
	srv.playback.SetAssets(testAssetLocator{preview: true})

	req := newJSONRequest(http.MethodPost, "/api/preview-info", bytes.NewReader([]byte(`{"mediaID":"media-alpha"}`)))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp playback.PreviewInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode previewInfo: %v", err)
	}
	if resp.MediaID != "media-alpha" {
		t.Fatalf("MediaID = %q, want media-alpha", resp.MediaID)
	}
	if resp.Assets == nil || !strings.HasSuffix(resp.Assets.PreviewClip, "/preview.mp4") {
		t.Fatalf("Assets = %+v, want preview clip URL", resp.Assets)
	}
	token := strings.TrimSuffix(strings.TrimPrefix(resp.Assets.PreviewClip, "/api/media/assets/"), "/preview.mp4")
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(0, 0).UTC() })
	if _, err := tokens.UnverifiedGrant(token, streamtoken.TargetMediaAssets); err != nil {
		t.Fatalf("preview token did not verify for the assets target: %v", err)
	}
	if _, err := tokens.UnverifiedGrant(token, streamtoken.TargetMediaDirect); err == nil {
		t.Fatal("preview token verified for the direct target")
	}
	if _, err := tokens.UnverifiedGrant(token, streamtoken.TargetMediaHLS); err == nil {
		t.Fatal("preview token verified for the HLS target")
	}
	active, err := st.ActiveSessions(context.Background(), time.Unix(0, 0).UTC(), store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("ActiveSessions returned error: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("ActiveSessions = %+v, want none for preview-info", active)
	}
}

func TestPreviewInfoRouteDoesNotRevealRestrictedMedia(t *testing.T) {
	srv := newTestServerWithMediaFile(t, "media-archive", []byte("restricted bytes"))
	srv.playback.SetAssets(testAssetLocator{preview: true})
	req := newJSONRequest(http.MethodPost, "/api/preview-info", bytes.NewReader([]byte(`{"mediaID":"media-archive"}`)))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp playback.PreviewInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode previewInfo: %v", err)
	}
	if resp.MediaID != "" || resp.Assets != nil {
		t.Fatalf("previewInfo leaked restricted media: %+v", resp)
	}
}

func TestPreviewInfoRouteAllowsRestrictedMediaWhenRevealed(t *testing.T) {
	srv := newTestServerWithMediaFile(t, "media-archive", []byte("restricted bytes"))
	srv.playback.SetAssets(testAssetLocator{preview: true})
	req := newJSONRequest(http.MethodPost, "/api/preview-info", bytes.NewReader([]byte(`{"mediaID":"media-archive"}`)))
	authorizeRevealed(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp playback.PreviewInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode previewInfo: %v", err)
	}
	if resp.MediaID != "media-archive" || resp.Assets == nil || !strings.HasSuffix(resp.Assets.PreviewClip, "/preview.mp4") {
		t.Fatalf("previewInfo = %+v, want revealed preview asset", resp)
	}
}

func TestPlaybackInfoRouteDoesNotRevealRestrictedMedia(t *testing.T) {
	srv := newTestServer(t)
	body := []byte(`{"mediaID":"media-archive","capabilityProfile":{"client":"beamfall-test"}}`)
	req := newJSONRequest(http.MethodPost, "/api/playback-info", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp playback.InfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode playbackInfo: %v", err)
	}
	if resp.Decision != "unavailable" {
		t.Fatalf("Decision = %q, want unavailable", resp.Decision)
	}
	if len(resp.Streams) != 0 {
		t.Fatalf("len(Streams) = %d, want 0", len(resp.Streams))
	}
}

func TestPlaybackInfoRouteAllowsRestrictedMediaWhenRevealed(t *testing.T) {
	srv := newTestServerWithMediaFile(t, "media-archive", []byte("restricted bytes"))
	body := []byte(`{"mediaID":"media-archive","capabilityProfile":{"client":"beamfall-test","containers":["mp4"],"directCodecs":["h264"]}}`)
	req := newJSONRequest(http.MethodPost, "/api/playback-info", bytes.NewReader(body))
	authorizeRevealed(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp playback.InfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode playbackInfo: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play", resp.Decision)
	}
}

func TestBeamHandoffOfferClaimRoute(t *testing.T) {
	srv := newTestServer(t)
	offerReq := newJSONRequest(http.MethodPost, "/api/beam/offers", bytes.NewReader([]byte(`{"mediaID":"media-alpha","positionSeconds":42,"targetDeviceID":"device-mac"}`)))
	authorizeViewer(offerReq)
	offerRec := httptest.NewRecorder()
	srv.ServeHTTP(offerRec, offerReq)
	if offerRec.Code != http.StatusCreated {
		t.Fatalf("offer status = %d, want %d: %s", offerRec.Code, http.StatusCreated, offerRec.Body.String())
	}
	var offer struct {
		HandoffToken    string `json:"handoffToken"`
		MediaID         string `json:"mediaID"`
		PositionSeconds int    `json:"positionSeconds"`
	}
	if err := json.Unmarshal(offerRec.Body.Bytes(), &offer); err != nil {
		t.Fatalf("decode offer: %v", err)
	}
	if offer.HandoffToken == "" || offer.MediaID != "media-alpha" || offer.PositionSeconds != 42 {
		t.Fatalf("offer = %#v, want token media-alpha at 42", offer)
	}

	claimReq := newJSONRequest(http.MethodPost, "/api/beam/claims", bytes.NewReader([]byte(`{"handoffToken":"`+offer.HandoffToken+`"}`)))
	authorizeManager(claimReq)
	claimRec := httptest.NewRecorder()
	srv.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim status = %d, want %d: %s", claimRec.Code, http.StatusOK, claimRec.Body.String())
	}
	var claim struct {
		MediaID         string `json:"mediaID"`
		PositionSeconds int    `json:"positionSeconds"`
		SourceSessionID string `json:"sourceSessionID"`
	}
	if err := json.Unmarshal(claimRec.Body.Bytes(), &claim); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	if claim.MediaID != "media-alpha" || claim.PositionSeconds != 42 || claim.SourceSessionID != "session-default" {
		t.Fatalf("claim = %#v, want media-alpha at 42 from session-default", claim)
	}

	replayReq := newJSONRequest(http.MethodPost, "/api/beam/claims", bytes.NewReader([]byte(`{"handoffToken":"`+offer.HandoffToken+`"}`)))
	authorizeManager(replayReq)
	replayRec := httptest.NewRecorder()
	srv.ServeHTTP(replayRec, replayReq)
	if replayRec.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d, want %d: %s", replayRec.Code, http.StatusUnauthorized, replayRec.Body.String())
	}
}

func TestBeamHandoffClaimInvalidAfterSourceRelock(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	offerReq := newJSONRequest(http.MethodPost, "/api/beam/offers", bytes.NewReader([]byte(`{"mediaID":"media-archive","positionSeconds":9,"targetDeviceID":"device-mac"}`)))
	authorizeRevealed(offerReq)
	offerRec := httptest.NewRecorder()
	srv.ServeHTTP(offerRec, offerReq)
	if offerRec.Code != http.StatusCreated {
		t.Fatalf("offer status = %d, want %d: %s", offerRec.Code, http.StatusCreated, offerRec.Body.String())
	}
	var offer struct {
		HandoffToken string `json:"handoffToken"`
	}
	if err := json.Unmarshal(offerRec.Body.Bytes(), &offer); err != nil {
		t.Fatalf("decode offer: %v", err)
	}
	if err := st.SetSessionRevealed(context.Background(), "session-revealed", false); err != nil {
		t.Fatalf("SetSessionRevealed returned error: %v", err)
	}

	claimReq := newJSONRequest(http.MethodPost, "/api/beam/claims", bytes.NewReader([]byte(`{"handoffToken":"`+offer.HandoffToken+`"}`)))
	authorizeManager(claimReq)
	claimRec := httptest.NewRecorder()
	srv.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusUnauthorized {
		t.Fatalf("claim status = %d, want %d: %s", claimRec.Code, http.StatusUnauthorized, claimRec.Body.String())
	}
	if bytes.Contains(claimRec.Body.Bytes(), []byte("media-archive")) {
		t.Fatalf("relocked claim leaked media id in body: %s", claimRec.Body.String())
	}
}

func TestCreatePairingCodeRequiresManagerSession(t *testing.T) {
	srv := newTestServer(t)
	req := newJSONRequest(http.MethodPost, "/api/pairing-codes", bytes.NewReader([]byte(`{}`)))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestPairingCodeEnrollmentCreatesUsableSession(t *testing.T) {
	srv := newTestServer(t)
	createReq := newJSONRequest(http.MethodPost, "/api/pairing-codes", bytes.NewReader([]byte(`{"deviceRole":"viewer"}`)))
	createReq.Header.Set("X-Beamfall-Session", "session-manager")
	createReq.Header.Set("X-Beamfall-Device", "device-mac")
	createReq.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
	createRec := httptest.NewRecorder()
	srv.ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d: %s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	var codeResp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &codeResp); err != nil {
		t.Fatalf("decode pairing code: %v", err)
	}
	if codeResp.Code == "" {
		t.Fatal("pairing code is empty")
	}

	enrollBody, err := json.Marshal(map[string]string{
		"code":       codeResp.Code,
		"deviceName": "Living Room",
	})
	if err != nil {
		t.Fatal(err)
	}
	enrollReq := newJSONRequest(http.MethodPost, "/api/enroll", bytes.NewReader(enrollBody))
	enrollRec := httptest.NewRecorder()
	srv.ServeHTTP(enrollRec, enrollReq)

	if enrollRec.Code != http.StatusCreated {
		t.Fatalf("enroll status = %d, want %d: %s", enrollRec.Code, http.StatusCreated, enrollRec.Body.String())
	}
	var enrollResp struct {
		DeviceID         string `json:"deviceID"`
		DeviceSecret     string `json:"deviceSecret"`
		SessionID        string `json:"sessionID"`
		SessionExpiresAt string `json:"sessionExpiresAt"`
	}
	if err := json.Unmarshal(enrollRec.Body.Bytes(), &enrollResp); err != nil {
		t.Fatalf("decode enrollment: %v", err)
	}
	if enrollResp.SessionID == "" {
		t.Fatal("SessionID is empty")
	}
	if enrollResp.DeviceID == "" {
		t.Fatal("DeviceID is empty")
	}
	if enrollResp.DeviceSecret == "" {
		t.Fatal("DeviceSecret is empty")
	}
	if enrollResp.SessionExpiresAt != "1970-01-31T00:00:00Z" {
		t.Fatalf("SessionExpiresAt = %q, want 1970-01-31T00:00:00Z", enrollResp.SessionExpiresAt)
	}

	manifestReq := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	manifestReq.Header.Set("X-Beamfall-Session", enrollResp.SessionID)
	manifestReq.Header.Set("X-Beamfall-Device", enrollResp.DeviceID)
	manifestReq.Header.Set("X-Beamfall-Device-Secret", enrollResp.DeviceSecret)
	manifestRec := httptest.NewRecorder()
	srv.ServeHTTP(manifestRec, manifestReq)
	if manifestRec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d, want %d: %s", manifestRec.Code, http.StatusOK, manifestRec.Body.String())
	}

	wrongSecretReq := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	wrongSecretReq.Header.Set("X-Beamfall-Session", enrollResp.SessionID)
	wrongSecretReq.Header.Set("X-Beamfall-Device", enrollResp.DeviceID)
	wrongSecretReq.Header.Set("X-Beamfall-Device-Secret", "wrong-secret")
	wrongSecretRec := httptest.NewRecorder()
	srv.ServeHTTP(wrongSecretRec, wrongSecretReq)
	if wrongSecretRec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-secret status = %d, want %d", wrongSecretRec.Code, http.StatusUnauthorized)
	}
}

func TestRelayedLoopbackEnrollmentIsRemote(t *testing.T) {
	srv := newTestServer(t)
	code := mintPairingCode(t, srv, "viewer")
	body, err := json.Marshal(map[string]string{"code": code, "deviceName": "Living Room"})
	if err != nil {
		t.Fatal(err)
	}

	relayed := newJSONRequest(http.MethodPost, "/api/enroll", bytes.NewReader(body))
	relayed = relayed.WithContext(ContextWithRelayedRequest(relayed.Context()))
	relayedRec := httptest.NewRecorder()
	srv.ServeHTTP(relayedRec, relayed)
	if relayedRec.Code != http.StatusUnauthorized {
		t.Fatalf("relayed enroll status = %d, want %d", relayedRec.Code, http.StatusUnauthorized)
	}

	lan := newJSONRequest(http.MethodPost, "/api/enroll", bytes.NewReader(body))
	lanRec := httptest.NewRecorder()
	srv.ServeHTTP(lanRec, lan)
	if lanRec.Code != http.StatusCreated {
		t.Fatalf("loopback enroll status = %d, want %d: %s", lanRec.Code, http.StatusCreated, lanRec.Body.String())
	}
}

func TestRelayedLoopbackWebEnrollmentIsRemote(t *testing.T) {
	srv := newTestServer(t)
	code := mintPairingCode(t, srv, "viewer")
	body, err := json.Marshal(map[string]string{"code": code, "deviceName": "Web Browser"})
	if err != nil {
		t.Fatal(err)
	}

	relayed := newJSONRequest(http.MethodPost, "/api/web/enroll", bytes.NewReader(body))
	relayed.TLS = &tls.ConnectionState{}
	relayed.Header.Set(auth.CSRFHeader, "1")
	relayed = relayed.WithContext(ContextWithRelayedRequest(relayed.Context()))
	relayedRec := httptest.NewRecorder()
	srv.ServeHTTP(relayedRec, relayed)
	if relayedRec.Code != http.StatusUnauthorized {
		t.Fatalf("relayed web enroll status = %d, want %d", relayedRec.Code, http.StatusUnauthorized)
	}

	lan := newJSONRequest(http.MethodPost, "/api/web/enroll", bytes.NewReader(body))
	lan.RemoteAddr = "127.0.0.1:5000"
	lan.Header.Set(auth.CSRFHeader, "1")
	lanRec := httptest.NewRecorder()
	srv.ServeHTTP(lanRec, lan)
	if lanRec.Code != http.StatusCreated {
		t.Fatalf("loopback web enroll status = %d, want %d: %s", lanRec.Code, http.StatusCreated, lanRec.Body.String())
	}
}

func TestRemoteAccessWarnsOnPlainHTTPFromNonLAN(t *testing.T) {
	srv, _ := newTestServerWithStore(t)
	query := []byte(`{"query":"query { remoteAccess { plainHTTPPublicWarning } }"}`)
	warn := func(remoteAddr string) bool {
		t.Helper()
		req := newJSONRequest(http.MethodPost, "/api/graphql", bytes.NewReader(query))
		req.RemoteAddr = remoteAddr
		authorizeManager(req)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("remoteAccess status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Data struct {
				RemoteAccess struct {
					PlainHTTPPublicWarning bool `json:"plainHTTPPublicWarning"`
				} `json:"remoteAccess"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode remoteAccess: %v", err)
		}
		return resp.Data.RemoteAccess.PlainHTTPPublicWarning
	}

	if !warn("203.0.113.10:5000") {
		t.Fatal("plain-HTTP public source warning = false, want true")
	}
	if warn("192.168.1.20:5000") {
		t.Fatal("plain-HTTP LAN source warning = true, want false")
	}
}

func TestEnrollRejectsReusedPairingCode(t *testing.T) {
	srv := newTestServer(t)
	createReq := newJSONRequest(http.MethodPost, "/api/pairing-codes", bytes.NewReader([]byte(`{}`)))
	authorizeManager(createReq)
	createRec := httptest.NewRecorder()
	srv.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d: %s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	var codeResp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &codeResp); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"code": codeResp.Code, "deviceName": "TV"})
	if err != nil {
		t.Fatal(err)
	}
	firstReq := newJSONRequest(http.MethodPost, "/api/enroll", bytes.NewReader(body))
	firstRec := httptest.NewRecorder()
	srv.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("first enroll status = %d, want %d", firstRec.Code, http.StatusCreated)
	}
	secondReq := newJSONRequest(http.MethodPost, "/api/enroll", bytes.NewReader(body))
	secondRec := httptest.NewRecorder()
	srv.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusUnauthorized {
		t.Fatalf("second enroll status = %d, want %d", secondRec.Code, http.StatusUnauthorized)
	}
}

func TestEnrollRejectsPasswordWithoutPairingCode(t *testing.T) {
	srv := newTestServer(t)
	body := []byte(`{"adminPassword":"secret","deviceName":"TV"}`)
	req := newJSONRequest(http.MethodPost, "/api/enroll", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestEnrollThrottlesRepeatedFailedPairingAttempts(t *testing.T) {
	srv := newTestServer(t)
	for i := 0; i < 5; i++ {
		req := newJSONRequest(http.MethodPost, "/api/enroll", bytes.NewReader([]byte(`{"code":"bad-code","deviceName":"TV"}`)))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d", i+1, rec.Code, http.StatusUnauthorized)
		}
	}

	req := newJSONRequest(http.MethodPost, "/api/enroll", bytes.NewReader([]byte(`{"code":"BAD CODE","deviceName":"TV"}`)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
}

// Evidence — SECAUDIT-20260817-ENROLL-LOCKOUT-DOS-1 (2026-08-20, red-first):
// with the vulnerable server-wide lane still in place this test failed as
//
//	server_test.go: attempt 25 status = 429, want 401: {"error":"pairing attempts throttled"}
//
// (command, SHA, exit codes, timing: full record above the sibling test
// TestEnrollDistributedFailuresDoNotDenyUnrelatedValidCode in
// internal/pairing/pairing_test.go). Green after the fix in the same run.
func TestEnrollDistributedFailuresDoNotDenyValidCodeOnEitherFlow(t *testing.T) {
	srv := newTestServer(t)
	// Distributed unauthenticated failures: distinct wrong codes, each from a
	// distinct IPv6 /64 source prefix. No server-wide lane may accumulate, so
	// every attempt answers 401, never 429.
	for i := 0; i < 40; i++ {
		body := []byte(fmt.Sprintf(`{"code":"BAD%04d","deviceName":"TV"}`, i))
		req := newJSONRequest(http.MethodPost, "/api/enroll", bytes.NewReader(body))
		req.RemoteAddr = fmt.Sprintf("[2001:db8:%x::1]:5000", i)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d: %s", i, rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	}

	// A valid fresh code from an uninvolved source still redeems on the
	// native flow...
	nativeBody, err := json.Marshal(map[string]string{"code": mintPairingCode(t, srv, "viewer"), "deviceName": "TV"})
	if err != nil {
		t.Fatal(err)
	}
	nativeReq := newJSONRequest(http.MethodPost, "/api/enroll", bytes.NewReader(nativeBody))
	nativeRec := httptest.NewRecorder()
	srv.ServeHTTP(nativeRec, nativeReq)
	if nativeRec.Code != http.StatusCreated {
		t.Fatalf("native enroll status = %d, want %d: %s", nativeRec.Code, http.StatusCreated, nativeRec.Body.String())
	}

	// ...and the browser flow agrees.
	webBody, err := json.Marshal(map[string]string{"code": mintPairingCode(t, srv, "viewer"), "deviceName": "Web Browser"})
	if err != nil {
		t.Fatal(err)
	}
	webReq := newJSONRequest(http.MethodPost, "/api/web/enroll", bytes.NewReader(webBody))
	webReq.Header.Set(auth.CSRFHeader, "1")
	webRec := httptest.NewRecorder()
	srv.ServeHTTP(webRec, webReq)
	if webRec.Code != http.StatusCreated {
		t.Fatalf("web enroll status = %d, want %d: %s", webRec.Code, http.StatusCreated, webRec.Body.String())
	}
}

func TestWebShellSecurityHeaders(t *testing.T) {
	srv := newTestServer(t)
	paths := []string{"/web/", "/web/index.html", "/web/some/client/route"}
	wantHeaders := map[string]string{
		"X-Frame-Options":        "SAMEORIGIN",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
		"Content-Security-Policy": "default-src 'self'; style-src 'self' 'unsafe-inline'; " +
			"worker-src 'self' blob:; media-src 'self' blob:; object-src 'none'; frame-ancestors 'self'",
	}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "127.0.0.1:5000"
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		for header, want := range wantHeaders {
			if got := rec.Header().Get(header); got != want {
				t.Errorf("path %s: header %s = %q, want %q", path, header, got, want)
			}
		}
		// The CSP must keep the XSS backstop intact: no inline SCRIPT allowance.
		// Scripts inherit default-src 'self'; only style-src relaxes to inline.
		csp := rec.Header().Get("Content-Security-Policy")
		if strings.Contains(csp, "script-src") {
			t.Errorf("path %s: CSP declares a script-src directive (must inherit default-src 'self'): %q", path, csp)
		}
	}
}

func TestWebLogoutTwiceClearsCookiesAfterSessionNotFound(t *testing.T) {
	srv, revoker := newTestServerWithSessionRevocationErrors(t, nil, store.ValidationError("session not found"))
	cookies, _ := enrollWebBrowser(t, srv)

	first := logoutWebBrowser(srv, cookies)
	if first.Code != http.StatusOK {
		t.Fatalf("first logout status = %d, want %d: %s", first.Code, http.StatusOK, first.Body.String())
	}
	assertCredentialCookiesCleared(t, first)

	second := logoutWebBrowser(srv, cookies)
	if second.Code != http.StatusOK {
		t.Fatalf("second logout status = %d, want %d: %s", second.Code, http.StatusOK, second.Body.String())
	}
	assertCredentialCookiesCleared(t, second)
	if revoker.calls != 2 {
		t.Fatalf("TerminateSession calls = %d, want 2", revoker.calls)
	}
}

func TestWebLogoutNonNotFoundRevocationErrorFailsClosed(t *testing.T) {
	srv, revoker := newTestServerWithSessionRevocationErrors(t, store.ValidationError("session ID is required"))
	cookies, _ := enrollWebBrowser(t, srv)

	rec := logoutWebBrowser(srv, cookies)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("logout status = %d, want %d: %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if got := len(rec.Result().Cookies()); got != 0 {
		t.Fatalf("failed logout cleared %d cookies, want 0", got)
	}
	if revoker.calls != 1 {
		t.Fatalf("TerminateSession calls = %d, want 1", revoker.calls)
	}
}

func newTestServerWithSessionRevocationErrors(t *testing.T, errs ...error) (*Server, *sequencedSessionRevokerJobStats) {
	t.Helper()
	base, st := newTestServerWithStore(t)
	revoker := &sequencedSessionRevokerJobStats{Store: st, errors: errs}
	srv := New(base.manifest, base.playback, base.beam, base.pairing, base.graph, base.media, base.auth, revoker)
	return srv, revoker
}

func assertCredentialCookiesCleared(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	cleared := map[string]bool{}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Value == "" && cookie.Expires.Before(time.Now()) {
			cleared[cookie.Name] = true
		}
	}
	for _, name := range []string{auth.SessionCookie, auth.DeviceCookie, auth.DeviceSecretCookie} {
		if !cleared[name] {
			t.Fatalf("cookie %q not cleared; cookies = %#v", name, rec.Result().Cookies())
		}
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	srv, _ := newTestServerWithStore(t)
	return srv
}

func newTestServerWithMediaFile(t *testing.T, mediaID store.MediaID, body []byte) *Server {
	t.Helper()
	st := storetest.Open(t)
	path := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), mediaID, path); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewServiceWithClock(st, func() time.Time { return time.Unix(0, 0).UTC() })
	manifests := manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, nil)
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(0, 0).UTC() })
	tokens.SetRecorder(st)
	srv := New(
		manifests,
		playback.NewService(
			st,
			tokens,
			func() time.Time { return time.Unix(0, 0).UTC() },
		),
		beam.NewService(st, authService, func() time.Time { return time.Unix(0, 0).UTC() }),
		pairing.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }),
		testServerGraphHandler{Handler: graph.NewHandler(manifests, authService, st, nil)},
		mediahttp.NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil)),
		authService,
		st,
	)
	srv.SetEvents(st)
	srv.SetV1Store(st)
	srv.SetV1ScanEnqueuer(testV1ScanEnqueuer(st))
	return srv
}

func authorizeViewer(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-default")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

func addViewerCookies(req *http.Request) {
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "session-default"})
	req.AddCookie(&http.Cookie{Name: auth.DeviceCookie, Value: "device-tv"})
	req.AddCookie(&http.Cookie{Name: auth.DeviceSecretCookie, Value: "dev-tv-secret"})
}

func authorizeRevealed(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-revealed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

func authorizeManager(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-manager")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
}

type testAssetLocator struct {
	trickplay bool
	preview   bool
}

func (t testAssetLocator) TrickplayAsset(string) (string, bool) {
	if !t.trickplay {
		return "", false
	}
	return "trickplay.json", true
}

func (t testAssetLocator) PreviewClipAsset(string) (string, bool) {
	if !t.preview {
		return "", false
	}
	return "preview.mp4", true
}

func (t testAssetLocator) LoudnessAsset(string) (string, bool) { return "", false }

func (t testAssetLocator) LoudnessForFingerprint(string) (playback.LoudnessData, bool) {
	return playback.LoudnessData{}, false
}

func (t testAssetLocator) WaveformAsset(string) (string, bool)   { return "", false }
func (t testAssetLocator) BeatGridAsset(string) (string, bool)   { return "", false }
func (t testAssetLocator) SyncLanesAsset(string) (string, bool)  { return "", false }
func (t testAssetLocator) SilenceMapAsset(string) (string, bool) { return "", false }

func TestManifestServiceAcceptsContext(t *testing.T) {
	st := storetest.Open(t)
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.NewService(st, nil, nil).Document(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
}

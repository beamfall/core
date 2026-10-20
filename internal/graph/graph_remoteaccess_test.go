package graph

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store/storetest"
)

// stubRemoteAccess is a test RemoteAccessProvider with toggleable presence.
type stubRemoteAccess struct {
	direct    *DirectHTTPSStatus
	relay     *RelayStatus
	directOK  bool
	relayOK   bool
	endpoints []RemoteEndpoint
}

func (s stubRemoteAccess) DirectHTTPS(context.Context) (DirectHTTPSStatus, bool) {
	if !s.directOK || s.direct == nil {
		return DirectHTTPSStatus{}, false
	}
	return *s.direct, true
}

func (s stubRemoteAccess) Relay(context.Context) (RelayStatus, bool) {
	if !s.relayOK || s.relay == nil {
		return RelayStatus{}, false
	}
	return *s.relay, true
}

func (s stubRemoteAccess) Endpoints(context.Context) []RemoteEndpoint { return s.endpoints }

// TestRemoteAccessAdminGated proves the Admin-Session gate on the
// remoteAccess Console field (ADR-0007 §4 server settings).
//
// feature:direct-https-acme
// feature:self-hosted-relay
func TestRemoteAccessAdminGated(t *testing.T) {
	handler := newTestHandler(t)

	// A session without an open Admin Session is denied.
	_, errs := post(t, handler, `{"query":"query { remoteAccess { directHTTPS { domain } } }"}`, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("non-admin remoteAccess errors = %#v, want admin-session gate", errs)
	}
}

// TestConnectionEndpointsAuthenticatedDeviceVector pins the producer-owned
// endpoint-discovery response and proves a non-manager enrolled device can
// refresh it without gaining access to manager-only remote-access status.
//
// feature:dual-url-endpoints
func TestConnectionEndpointsAuthenticatedDeviceVector(t *testing.T) {
	handler := newTestHandler(t)
	handler.SetRemoteAccess(stubRemoteAccess{
		endpoints: []RemoteEndpoint{
			{Kind: "lan", URL: "http://192.168.1.20:8800"},
			{Kind: "direct", URL: "https://home.example.com"},
			{Kind: "overlay", URL: "https://100.96.0.5:8800"},
			{Kind: "self-hosted-relay", URL: "https://relay.example.com"},
			{Kind: "hosted", URL: "https://abc123.relay.beamfall.com"},
		},
	})

	data, errs := post(t, handler,
		`{"query":"query { connectionEndpoints { version endpoints { kind url } } }"}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("viewer connectionEndpoints errors = %#v", errs)
	}
	vector, err := os.ReadFile("testdata/connection-endpoints-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := bytes.TrimSpace(data["connectionEndpoints"]), bytes.TrimSpace(vector); !bytes.Equal(got, want) {
		t.Fatalf("connectionEndpoints vector mismatch\n got: %s\nwant: %s", got, want)
	}

	_, errs = post(t, handler,
		`{"query":"query { remoteAccess { relay { endpoint connectionState } } }"}`,
		authorizeViewer)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
		t.Fatalf("viewer remoteAccess errors = %#v, want admin-session gate", errs)
	}
}

func TestConnectionEndpointsRequiresAuthentication(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(
		`{"query":"query { connectionEndpoints { version endpoints { kind url } } }"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestConnectionEndpointsWithoutProviderIsVersionedEmptySet(t *testing.T) {
	handler := newTestHandler(t)
	data, errs := post(t, handler,
		`{"query":"query { connectionEndpoints { version endpoints { kind url } } }"}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("viewer connectionEndpoints errors = %#v", errs)
	}
	if got, want := string(data["connectionEndpoints"]), `{"version":"1.0.0","endpoints":[]}`; got != want {
		t.Fatalf("connectionEndpoints = %s, want %s", got, want)
	}
}

// TestRemoteAccessHonestlyDisabledWithoutProvider proves a LAN-only server
// (no provider wired) reports both paths absent rather than faking them.
//
// feature:direct-https-acme
// feature:self-hosted-relay
func TestRemoteAccessHonestlyDisabledWithoutProvider(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	data, errs := post(t, handler, `{"query":"query { remoteAccess { directHTTPS { enabled } relay { enabled } } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("manager remoteAccess errors = %#v", errs)
	}
	var info RemoteAccessInfo
	mustUnmarshal(t, data["remoteAccess"], &info)
	if info.DirectHTTPS != nil {
		t.Fatalf("directHTTPS present with no provider: %#v", info.DirectHTTPS)
	}
	if info.Relay != nil {
		t.Fatalf("relay present with no provider: %#v", info.Relay)
	}
}

// TestRemoteAccessReportsConfiguredPaths proves the resolver surfaces each
// configured path's honest status and sets Enabled.
//
// feature:direct-https-acme
// feature:self-hosted-relay
func TestRemoteAccessReportsConfiguredPaths(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	handler.SetRemoteAccess(stubRemoteAccess{
		directOK: true,
		direct:   &DirectHTTPSStatus{Domain: "media.example.com", CertState: "pending"},
		relayOK:  true,
		relay:    &RelayStatus{Endpoint: "relay.example.com:7443", PublicHost: "media.example.com", ConnectionState: "connecting"},
	})

	data, errs := post(t, handler,
		`{"query":"query { remoteAccess { directHTTPS { enabled domain certState } relay { enabled endpoint publicHost connectionState } } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("manager remoteAccess errors = %#v", errs)
	}
	var info RemoteAccessInfo
	mustUnmarshal(t, data["remoteAccess"], &info)
	if info.DirectHTTPS == nil || !info.DirectHTTPS.Enabled {
		t.Fatalf("directHTTPS missing/disabled: %#v", info.DirectHTTPS)
	}
	if info.DirectHTTPS.Domain != "media.example.com" || info.DirectHTTPS.CertState != "pending" {
		t.Fatalf("directHTTPS status = %#v", info.DirectHTTPS)
	}
	if info.Relay == nil || !info.Relay.Enabled {
		t.Fatalf("relay missing/disabled: %#v", info.Relay)
	}
	if info.Relay.ConnectionState != "connecting" || info.Relay.Endpoint != "relay.example.com:7443" {
		t.Fatalf("relay status = %#v", info.Relay)
	}
}

// TestRemoteAccessOmitsDisabledPath proves that when only one path is
// configured, the other is omitted (never faked).
//
// feature:self-hosted-relay
func TestRemoteAccessOmitsDisabledPath(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	handler.SetRemoteAccess(stubRemoteAccess{
		relayOK: true,
		relay:   &RelayStatus{Endpoint: "relay.example.com:7443", ConnectionState: "disconnected"},
	})
	data, errs := post(t, handler, `{"query":"query { remoteAccess { directHTTPS { enabled } relay { connectionState } } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("errors = %#v", errs)
	}
	var info RemoteAccessInfo
	mustUnmarshal(t, data["remoteAccess"], &info)
	if info.DirectHTTPS != nil {
		t.Fatalf("directHTTPS present though unconfigured: %#v", info.DirectHTTPS)
	}
	if info.Relay == nil || info.Relay.ConnectionState != "disconnected" {
		t.Fatalf("relay status = %#v", info.Relay)
	}
}

// TestRemoteAccessSurfacesOrderedEndpoints proves the ordered dual-URL endpoint
// set flows through the resolver to the wire in canonical path
// order (LAN → direct → overlay → self-hosted-relay → hosted; relay kinds last,
// SEC-10), data only.
//
// feature:dual-url-endpoints
func TestRemoteAccessSurfacesOrderedEndpoints(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	handler.SetRemoteAccess(stubRemoteAccess{
		endpoints: []RemoteEndpoint{
			{Kind: "lan", URL: "http://192.168.1.20:8800"},
			{Kind: "direct", URL: "https://home.example.com"},
			{Kind: "overlay", URL: "https://100.96.0.5:8800"},
			{Kind: "self-hosted-relay", URL: "https://relay.example.com"},
			{Kind: "hosted", URL: "https://abc123.relay.beamfall.com"},
		},
	})
	data, errs := post(t, handler, `{"query":"query { remoteAccess { endpoints { kind url } } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("errors = %#v", errs)
	}
	var info RemoteAccessInfo
	mustUnmarshal(t, data["remoteAccess"], &info)
	wantKinds := []string{"lan", "direct", "overlay", "self-hosted-relay", "hosted"}
	if len(info.Endpoints) != len(wantKinds) {
		t.Fatalf("endpoints = %#v, want %d in order %v", info.Endpoints, len(wantKinds), wantKinds)
	}
	for i, k := range wantKinds {
		if info.Endpoints[i].Kind != k {
			t.Fatalf("endpoint[%d].kind = %q, want %q (order %v)", i, info.Endpoints[i].Kind, k, wantKinds)
		}
	}
	if info.Endpoints[0].URL != "http://192.168.1.20:8800" {
		t.Fatalf("LAN url = %q", info.Endpoints[0].URL)
	}
}

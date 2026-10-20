package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTestFaultsDisabledByDefault(t *testing.T) {
	srv := newTestServer(t)
	req := newJSONRequest(http.MethodPost, "/api/test/faults", bytes.NewReader([]byte(`{"method":"GET","path":"/health","status":503}`)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTestFaultsInjectAndClearPerEndpoint(t *testing.T) {
	srv := newTestServer(t)
	srv.SetTestFaults(true)

	setFault(t, srv, `{"method":"GET","path":"/health","status":429,"body":{"error":"pin locked"}}`)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "pin locked") {
		t.Fatalf("faulted /health = %d %q, want 429 pin locked", rec.Code, rec.Body.String())
	}

	setFault(t, srv, `{"method":"GET","path":"/health","status":401,"body":{"error":"token expired"}}`)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "token expired") {
		t.Fatalf("faulted /health = %d %q, want 401 token expired", rec.Code, rec.Body.String())
	}

	setFault(t, srv, `{"method":"GET","path":"/health","clear":true}`)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	// newTestServer wires no readiness provider, so the bare /health handler is live-but-not-ready.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("cleared /health = %d, want bare handler status %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestTestFaultsDelayAndEmptyPayload(t *testing.T) {
	srv := newTestServer(t)
	srv.SetTestFaults(true)
	setFault(t, srv, `{"method":"GET","path":"/health","rule":{"status":200,"delayMS":10,"emptyJSON":true}}`)

	start := time.Now()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Fatalf("fault delay = %s, want at least 10ms", elapsed)
	}
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Fatalf("faulted /health = %d %q, want 200 {}", rec.Code, rec.Body.String())
	}
}

func TestTestFaultsCloseDropsRealConnection(t *testing.T) {
	srv := newTestServer(t)
	srv.SetTestFaults(true)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	setFaultURL(t, ts.URL, `{"method":"GET","path":"/health","rule":{"close":true}}`)
	resp, err := http.Get(ts.URL + "/health")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("GET /health succeeded with status %d, want connection error", resp.StatusCode)
	}
}

func TestTestFaultsRejectsRelayedLoopbackRequest(t *testing.T) {
	srv := newTestServer(t)
	srv.SetTestFaults(true)

	// The relay backend listener serves requests from 127.0.0.1, so a bare
	// loopback check on RemoteAddr would wrongly treat relayed remote traffic
	// as loopback-only. Marking the request as relayed must still reject it.
	req := newJSONRequest(http.MethodPost, "/api/test/faults", bytes.NewReader([]byte(`{"method":"GET","path":"/health","status":503}`)))
	req = req.WithContext(ContextWithRelayedRequest(req.Context()))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("relayed request status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// TestTestFaultControlRefusesEveryRelayedClient pins requestIsLoopback's two
// host-side proximity signals on the surface that still gates on them. /metrics
// used to be that surface, and its table was where these directions were
// asserted; GCR-012-H moved /metrics onto the scrape credential and stopped
// consulting proximity at all, which would have left the signals with no test
// able to fail. The assertions live here now, unchanged in direction: a request
// this process can recognise as relayed is refused on a signal the remote
// client cannot set in its own favour — the operator's declared known-proxies
// allowlist, or a hop header the relaying proxy added — while a client that
// really is on this host keeps the control.
func TestTestFaultControlRefusesEveryRelayedClient(t *testing.T) {
	tests := []struct {
		name          string
		remoteAddr    string
		header        http.Header
		declaredProxy bool
		relayed       bool
		want          int
	}{
		{
			name:       "client on this host is served",
			remoteAddr: "127.0.0.1:5000",
			want:       http.StatusOK,
		},
		{
			name:       "blank hop header does not deny a client on this host",
			remoteAddr: "127.0.0.1:5000",
			header:     http.Header{"X-Forwarded-For": {""}},
			want:       http.StatusOK,
		},
		{
			name:       "undeclared same-host proxy is refused on its forwarded-for hop",
			remoteAddr: "127.0.0.1:5000",
			header:     http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			want:       http.StatusForbidden,
		},
		{
			name:       "undeclared same-host proxy is refused on its via hop",
			remoteAddr: "127.0.0.1:5000",
			header:     http.Header{"Via": {"1.1 gateway"}},
			want:       http.StatusForbidden,
		},
		{
			name:          "declared same-host proxy is refused with no hop header at all",
			remoteAddr:    "127.0.0.1:5000",
			declaredProxy: true,
			want:          http.StatusForbidden,
		},
		{
			name:          "declared same-host proxy is refused for the client it resolves",
			remoteAddr:    "127.0.0.1:5000",
			header:        http.Header{"X-Forwarded-For": {"203.0.113.7"}},
			declaredProxy: true,
			want:          http.StatusForbidden,
		},
		{
			name:          "declared same-host proxy is refused on a repeated forwarded-for header",
			remoteAddr:    "127.0.0.1:5000",
			header:        http.Header{"X-Forwarded-For": {"203.0.113.7", "198.51.100.9"}},
			declaredProxy: true,
			want:          http.StatusForbidden,
		},
		{
			name:          "declared same-host proxy is refused on an unparseable forwarded-for value",
			remoteAddr:    "127.0.0.1:5000",
			header:        http.Header{"X-Forwarded-For": {"not-an-address"}},
			declaredProxy: true,
			want:          http.StatusForbidden,
		},
		{
			name:       "off-host peer is refused",
			remoteAddr: "203.0.113.7:5000",
			want:       http.StatusForbidden,
		},
		{
			name:       "relay-fronted loopback peer is refused",
			remoteAddr: "127.0.0.1:5000",
			relayed:    true,
			want:       http.StatusForbidden,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			srv.SetTestFaults(true)
			if tt.declaredProxy {
				proxies, err := ParseKnownProxies("127.0.0.0/8", false)
				if err != nil {
					t.Fatal(err)
				}
				srv.knownProxies = proxies
			}
			req := newJSONRequest(http.MethodPost, "/api/test/faults", bytes.NewReader([]byte(`{"method":"GET","path":"/health","status":503}`)))
			req.RemoteAddr = tt.remoteAddr
			for name, values := range tt.header {
				for _, value := range values {
					req.Header.Add(name, value)
				}
			}
			if tt.relayed {
				req = req.WithContext(ContextWithRelayedRequest(req.Context()))
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body %q", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func setFault(t *testing.T, srv *Server, body string) {
	t.Helper()
	req := newJSONRequest(http.MethodPost, "/api/test/faults", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("set fault status = %d, body = %q", rec.Code, rec.Body.String())
	}
}

func setFaultURL(t *testing.T, baseURL, body string) {
	t.Helper()
	resp, err := http.Post(baseURL+"/api/test/faults", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("set fault: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set fault status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
)

func postSetup(srv *Server, body string, csrf bool) *httptest.ResponseRecorder {
	req := newJSONRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	if csrf {
		req.Header.Set(auth.CSRFHeader, "1")
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestWebSetupRedeemsTokenOnceAndSetsCredentialCookies(t *testing.T) {
	srv := newTestServer(t)
	srv.pairing.SetBootstrapToken("setup-tok-1")

	rec := postSetup(srv, `{"token":"setup-tok-1","deviceName":"Setup Browser","profileName":"Owner"}`, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode setup response: %v", err)
	}
	if body["deviceID"] == "" || body["profileID"] == "" || body["deviceRole"] != "manager" {
		t.Fatalf("setup body = %#v, want a manager device and owner profile", body)
	}
	if _, err := time.Parse(time.RFC3339, body["sessionExpiresAt"]); err != nil {
		t.Fatalf("sessionExpiresAt = %q, want RFC3339: %v", body["sessionExpiresAt"], err)
	}
	if _, leaked := body["deviceSecret"]; leaked {
		t.Fatal("setup response body leaks the device secret")
	}
	cookies := map[string]*http.Cookie{}
	for _, cookie := range rec.Result().Cookies() {
		cookies[cookie.Name] = cookie
	}
	for _, name := range []string{auth.SessionCookie, auth.DeviceCookie, auth.DeviceSecretCookie} {
		cookie := cookies[name]
		if cookie == nil || cookie.Value == "" || !cookie.HttpOnly || cookie.Expires.IsZero() {
			t.Fatalf("cookie %q = %+v, want a populated HttpOnly cookie with an expiry", name, cookie)
		}
	}
	if cookies[auth.DeviceCookie].Value != body["deviceID"] {
		t.Fatalf("device cookie = %q, want deviceID %q", cookies[auth.DeviceCookie].Value, body["deviceID"])
	}
	if srv.pairing.SetupRequired() {
		t.Fatal("setup token still installed after a successful redemption")
	}

	// The token is single-use: a replay is refused as invalid.
	replay := postSetup(srv, `{"token":"setup-tok-1","deviceName":"Setup Browser"}`, true)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replayed setup status = %d, want 401: %s", replay.Code, replay.Body.String())
	}
}

func TestWebSetupRejectsInvalidRequestsWithoutConsumingToken(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		csrf     bool
		want     int
		wantBody string
	}{
		{name: "missing CSRF", body: `{"token":"setup-tok-1","deviceName":"B"}`, want: http.StatusForbidden, wantBody: "missing CSRF header"},
		{name: "malformed JSON", body: `{"token":`, csrf: true, want: http.StatusBadRequest},
		{name: "missing device name", body: `{"token":"setup-tok-1"}`, csrf: true, want: http.StatusBadRequest, wantBody: "invalid setup request"},
		{name: "wrong token", body: `{"token":"not-the-token","deviceName":"B"}`, csrf: true, want: http.StatusUnauthorized, wantBody: "setup token invalid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			srv.pairing.SetBootstrapToken("setup-tok-1")
			rec := postSetup(srv, tc.body, tc.csrf)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("body = %q, want it to contain %q", rec.Body.String(), tc.wantBody)
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Fatalf("rejected setup set cookies: %v", rec.Result().Cookies())
			}
			if !srv.pairing.SetupRequired() {
				t.Fatal("rejected setup consumed the one-time token")
			}
		})
	}
}

func TestWebSetupThrottlesRepeatedFailures(t *testing.T) {
	srv := newTestServer(t)
	srv.pairing.SetBootstrapToken("setup-tok-1")
	for attempt := 0; attempt < 50; attempt++ {
		rec := postSetup(srv, `{"token":"wrong-token","deviceName":"B"}`, true)
		switch rec.Code {
		case http.StatusUnauthorized:
			continue
		case http.StatusTooManyRequests:
			if !strings.Contains(rec.Body.String(), "setup attempts throttled") {
				t.Fatalf("throttled body = %q", rec.Body.String())
			}
			// Throttling holds even for the correct token.
			valid := postSetup(srv, `{"token":"setup-tok-1","deviceName":"B"}`, true)
			if valid.Code != http.StatusTooManyRequests {
				t.Fatalf("valid token while throttled status = %d, want 429", valid.Code)
			}
			if !srv.pairing.SetupRequired() {
				t.Fatal("throttled setup consumed the token")
			}
			return
		default:
			t.Fatalf("attempt %d status = %d, want 401 or 429: %s", attempt, rec.Code, rec.Body.String())
		}
	}
	t.Fatal("setup never throttled repeated invalid tokens")
}

func postSetupFrom(srv *Server, remoteAddr, body string, header map[string]string) *httptest.ResponseRecorder {
	req := newJSONRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	req.Header.Set(auth.CSRFHeader, "1")
	req.RemoteAddr = remoteAddr
	req.Host = "192.168.4.10:8800"
	for name, value := range header {
		if name == "Host" {
			req.Host = value
			continue
		}
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// TestWebSetupLANClaimWithoutToken covers CORE-1105: while setup is pending a
// LAN browser completes setup with no token, and setup cannot run again after.
func TestWebSetupLANClaimWithoutToken(t *testing.T) {
	for _, addr := range []string{"192.168.4.20:51000", "127.0.0.1:51000", "[fe80::1%en0]:51000"} {
		t.Run(addr, func(t *testing.T) {
			srv := newTestServer(t)
			srv.pairing.SetBootstrapToken("setup-tok-1")
			rec := postSetupFrom(srv, addr, `{"deviceName":"Setup Browser","profileName":"Owner"}`, nil)
			if rec.Code != http.StatusCreated {
				t.Fatalf("LAN setup status = %d, want 201: %s", rec.Code, rec.Body.String())
			}
			if srv.pairing.SetupRequired() {
				t.Fatal("setup still pending after a LAN claim")
			}
			again := postSetupFrom(srv, addr, `{"deviceName":"Second Browser"}`, nil)
			if again.Code != http.StatusUnauthorized {
				t.Fatalf("LAN setup after completion status = %d, want 401: %s", again.Code, again.Body.String())
			}
		})
	}
}

func TestWebSetupLANClaimRefusals(t *testing.T) {
	tests := []struct {
		name   string
		addr   string
		body   string
		header map[string]string
	}{
		{name: "public source without token", addr: "203.0.113.9:51000", body: `{"deviceName":"B"}`},
		{name: "LAN source forwarded through an undeclared proxy", addr: "192.168.4.20:51000", body: `{"deviceName":"B"}`, header: map[string]string{"X-Forwarded-For": "203.0.113.9"}},
		{name: "LAN source with a wrong token", addr: "192.168.4.20:51000", body: `{"token":"not-the-token","deviceName":"B"}`},
		{name: "LAN source addressing a rebindable public name", addr: "192.168.4.20:51000", body: `{"deviceName":"B"}`, header: map[string]string{"Host": "attacker.example:8800"}},
		{name: "loopback source addressing a rebindable public name", addr: "127.0.0.1:51000", body: `{"deviceName":"B"}`, header: map[string]string{"Host": "rebind.attacker.example"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			srv.pairing.SetBootstrapToken("setup-tok-1")
			rec := postSetupFrom(srv, tc.addr, tc.body, tc.header)
			if rec.Code == http.StatusCreated || rec.Code < 400 {
				t.Fatalf("status = %d, want a refusal: %s", rec.Code, rec.Body.String())
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Fatalf("refused setup set cookies: %v", rec.Result().Cookies())
			}
			if !srv.pairing.SetupRequired() {
				t.Fatal("refused setup consumed the one-time token")
			}
		})
	}
}

func TestSetupHostResistsRebinding(t *testing.T) {
	for host, want := range map[string]bool{
		"192.168.4.10:8800":    true,
		"192.168.4.10":         true,
		"[fe80::1%25en0]:8800": true,
		"[::1]:8800":           true,
		"localhost:8800":       true,
		"LOCALHOST.":           true,
		"app.localhost":        true,
		"tower.local:8800":     true,
		"attacker.example":     false,
		"attacker.example:80":  false,
		"local.example.com":    false,
		"localhost.example":    false,
		"":                     false,
	} {
		if got := setupHostResistsRebinding(host); got != want {
			t.Errorf("setupHostResistsRebinding(%q) = %v, want %v", host, got, want)
		}
	}
}

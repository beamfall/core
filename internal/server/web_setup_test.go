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
	srv.pairing.SetBootstrapToken("first-run-token")

	rec := postSetup(srv, `{"token":"first-run-token","deviceName":"Setup Browser","profileName":"Owner"}`, true)
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
	replay := postSetup(srv, `{"token":"first-run-token","deviceName":"Setup Browser"}`, true)
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
		{name: "missing CSRF", body: `{"token":"first-run-token","deviceName":"B"}`, want: http.StatusForbidden, wantBody: "missing CSRF header"},
		{name: "malformed JSON", body: `{"token":`, csrf: true, want: http.StatusBadRequest},
		{name: "missing device name", body: `{"token":"first-run-token"}`, csrf: true, want: http.StatusBadRequest, wantBody: "invalid setup request"},
		{name: "wrong token", body: `{"token":"not-the-token","deviceName":"B"}`, csrf: true, want: http.StatusUnauthorized, wantBody: "setup token invalid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			srv.pairing.SetBootstrapToken("first-run-token")
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
	srv.pairing.SetBootstrapToken("first-run-token")
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
			valid := postSetup(srv, `{"token":"first-run-token","deviceName":"B"}`, true)
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

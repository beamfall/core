package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Home/Strict sign-in security (CORE-1110).

func probeSecurityMode(t *testing.T, srv *Server) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/setup", nil)
	req.RemoteAddr = "203.0.113.9:51000"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var body struct {
		SecurityMode string `json:"securityMode"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("setup probe = %d %s", rec.Code, rec.Body.String())
	}
	return body.SecurityMode
}

func TestWebSetupStoresSecurityMode(t *testing.T) {
	for _, tc := range []struct{ field, want string }{
		{``, "home"},
		{`,"securityMode":"home"`, "home"},
		{`,"securityMode":"strict"`, "strict"},
	} {
		t.Run(tc.want+tc.field, func(t *testing.T) {
			srv := newTestServer(t)
			srv.pairing.SetBootstrapToken("setup-tok")
			rec := postSetup(srv, `{"token":"setup-tok","deviceName":"Setup Browser"`+tc.field+`}`, true)
			if rec.Code != http.StatusCreated {
				t.Fatalf("setup status = %d: %s", rec.Code, rec.Body.String())
			}
			if got := probeSecurityMode(t, srv); got != tc.want {
				t.Fatalf("securityMode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWebSetupRejectsUnknownSecurityMode(t *testing.T) {
	srv := newTestServer(t)
	srv.pairing.SetBootstrapToken("setup-tok")
	if rec := postSetup(srv, `{"token":"setup-tok","deviceName":"B","securityMode":"open"}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("setup status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestWebAdminSignInStrictModeRefusesUniformly(t *testing.T) {
	srv, st := newAdminSignInServer(t, "correct horse")
	if _, err := st.SetSignInSecurityMode(context.Background(), store.SignInSecurityStrict, time.Unix(0, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	right := postAdminSignIn(srv, "192.168.4.20:51000", lanHost, `{"password":"correct horse","deviceName":"L"}`)
	wrong := postAdminSignIn(srv, "192.168.4.21:51000", lanHost, `{"password":"battery staple","deviceName":"L"}`)
	if right.Code != http.StatusUnauthorized || right.Body.String() != wrong.Body.String() || wrong.Code != http.StatusUnauthorized {
		t.Fatalf("strict right password = %d %s; wrong = %d %s; want identical 401s", right.Code, right.Body.String(), wrong.Code, wrong.Body.String())
	}
	if len(right.Result().Cookies()) != 0 {
		t.Fatalf("strict refusal set cookies: %v", right.Result().Cookies())
	}

	// Renewal from the device credential still works in Strict mode.
	enrollLapsedBrowser(t, st, "device-lapsed", "session-lapsed")
	renew := postRenew(srv, "192.168.4.20:51000", map[string]string{
		auth.DeviceCookie: "device-lapsed", auth.DeviceSecretCookie: recoverySecret, auth.SessionCookie: "session-lapsed",
	})
	if renew.Code != http.StatusOK {
		t.Fatalf("renew in strict mode = %d: %s", renew.Code, renew.Body.String())
	}
}

func postSignInSecurity(srv *Server, body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
	req := newJSONRequest(http.MethodPost, "/api/web/signin-security", strings.NewReader(body))
	req.Header.Set(auth.CSRFHeader, "1")
	if authorize != nil {
		authorize(req)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestWebSignInSecuritySwitchWithPassword(t *testing.T) {
	srv, st := newAdminSignInServer(t, "correct horse")
	rec := postSignInSecurity(srv, `{"mode":"strict","adminPassword":"correct horse"}`, authorizeManager)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"securityMode":"strict"`) {
		t.Fatalf("switch = %d %s, want 200 strict", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "correct horse") {
		t.Fatalf("switch response echoes the password: %s", rec.Body.String())
	}
	if got := probeSecurityMode(t, srv); got != "strict" {
		t.Fatalf("probe after switch = %q, want strict", got)
	}
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Action == store.ActivitySignInSecurityMode {
			found = entry.TargetID == "home->strict" && entry.SessionID == "session-manager"
		}
	}
	if !found {
		t.Fatalf("no home->strict audit entry in %+v", entries)
	}
	// Strict now refuses the admin-password sign-in path.
	if signIn := postAdminSignIn(srv, "192.168.4.20:51000", lanHost, `{"password":"correct horse","deviceName":"L"}`); signIn.Code != http.StatusUnauthorized {
		t.Fatalf("admin sign-in after switch = %d, want 401", signIn.Code)
	}
}

func TestWebSignInSecuritySwitchRefusals(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		authorize func(*http.Request)
		want      int
	}{
		{name: "wrong password", body: `{"mode":"strict","adminPassword":"battery staple"}`, authorize: authorizeManager, want: http.StatusUnauthorized},
		{name: "missing password", body: `{"mode":"strict"}`, authorize: authorizeManager, want: http.StatusUnauthorized},
		{name: "viewer device", body: `{"mode":"strict","adminPassword":"correct horse"}`, authorize: authorizeViewer, want: http.StatusForbidden},
		{name: "no session", body: `{"mode":"strict","adminPassword":"correct horse"}`, want: http.StatusUnauthorized},
		{name: "unknown mode", body: `{"mode":"open","adminPassword":"correct horse"}`, authorize: authorizeManager, want: http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newAdminSignInServer(t, "correct horse")
			if rec := postSignInSecurity(srv, tc.body, tc.authorize); rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if got := probeSecurityMode(t, srv); got != "home" {
				t.Fatalf("mode after refusal = %q, want home", got)
			}
		})
	}
}

func TestWebSignInSecuritySwitchThrottlesWrongPasswords(t *testing.T) {
	srv, _ := newAdminSignInServer(t, "correct horse")
	for attempt := 1; attempt <= 20; attempt++ {
		rec := postSignInSecurity(srv, `{"mode":"strict","adminPassword":"wrong"}`, authorizeManager)
		switch rec.Code {
		case http.StatusUnauthorized:
			continue
		case http.StatusTooManyRequests:
			ok := postSignInSecurity(srv, `{"mode":"strict","adminPassword":"correct horse"}`, authorizeManager)
			if ok.Code != http.StatusTooManyRequests {
				t.Fatalf("correct password while throttled = %d, want 429", ok.Code)
			}
			return
		default:
			t.Fatalf("attempt %d status = %d: %s", attempt, rec.Code, rec.Body.String())
		}
	}
	t.Fatal("wrong passwords were never throttled")
}

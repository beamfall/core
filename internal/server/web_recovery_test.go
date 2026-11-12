package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Sign-in recovery (CORE-1106): session renewal and admin-password sign-in.

const recoverySecret = "browser-secret"

// enrollLapsedBrowser enrolls a manager browser whose session expired an hour
// before the test server's pinned clock.
func enrollLapsedBrowser(t *testing.T, st *store.Store, deviceID, sessionID string) {
	t.Helper()
	if err := st.EnrollDevice(context.Background(),
		store.Device{ID: store.DeviceID(deviceID), Name: "Browser", Role: store.DeviceRoleManager, SecretHash: store.HashDeviceSecret(recoverySecret)},
		store.Session{ID: store.SessionID(sessionID), DeviceID: store.DeviceID(deviceID), ProfileID: "profile-default", ExpiresAt: time.Unix(-3600, 0).UTC()},
	); err != nil {
		t.Fatal(err)
	}
}

func postRenew(srv *Server, remoteAddr string, cookies map[string]string) *httptest.ResponseRecorder {
	req := newJSONRequest(http.MethodPost, "/api/web/session/renew", nil)
	req.Header.Set(auth.CSRFHeader, "1")
	req.RemoteAddr = remoteAddr
	for name, value := range cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value}) //nolint:gosec // test request cookie
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func responseCookies(rec *httptest.ResponseRecorder) map[string]*http.Cookie {
	out := map[string]*http.Cookie{}
	for _, cookie := range rec.Result().Cookies() {
		out[cookie.Name] = cookie
	}
	return out
}

// assertSignInCookies checks the three credential cookies: the session cookie
// carries the session expiry, the device cookies about 400 days.
func assertSignInCookies(t *testing.T, rec *httptest.ResponseRecorder, wantSessionExpiry time.Time) map[string]*http.Cookie {
	t.Helper()
	cookies := responseCookies(rec)
	session := cookies[auth.SessionCookie]
	if session == nil || session.Value == "" {
		t.Fatalf("no session cookie: %v", rec.Result().Cookies())
	}
	if !session.Expires.Equal(wantSessionExpiry) {
		t.Fatalf("session cookie expires %v, want %v", session.Expires, wantSessionExpiry)
	}
	for _, name := range []string{auth.DeviceCookie, auth.DeviceSecretCookie} {
		cookie := cookies[name]
		if cookie == nil || cookie.Value == "" {
			t.Fatalf("no %s cookie: %v", name, rec.Result().Cookies())
		}
		left := time.Until(cookie.Expires)
		if left < 399*24*time.Hour || left > 401*24*time.Hour {
			t.Fatalf("%s cookie expires in %v, want about 400 days", name, left)
		}
	}
	return cookies
}

func TestWebSessionRenewReplacesLapsedSession(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	enrollLapsedBrowser(t, st, "device-browser", "session-lapsed")

	rec := postRenew(srv, "127.0.0.1:5000", map[string]string{
		auth.SessionCookie: "session-lapsed", auth.DeviceCookie: "device-browser", auth.DeviceSecretCookie: recoverySecret,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("renew status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	wantExpiry := time.Unix(0, 0).UTC().Add(30 * 24 * time.Hour)
	cookies := assertSignInCookies(t, rec, wantExpiry)
	if cookies[auth.DeviceCookie].Value != "device-browser" || cookies[auth.DeviceSecretCookie].Value != recoverySecret {
		t.Fatalf("device cookies changed: %v", rec.Result().Cookies())
	}
	if strings.Contains(rec.Body.String(), cookies[auth.SessionCookie].Value) {
		t.Fatalf("renew body leaks the session ID: %s", rec.Body.String())
	}
	if _, err := st.Session(context.Background(), "session-lapsed"); err == nil {
		t.Fatal("lapsed session survived renewal")
	}
	renewed, err := st.Session(context.Background(), store.SessionID(cookies[auth.SessionCookie].Value))
	if err != nil || renewed.DeviceID != "device-browser" || renewed.ProfileID != "profile-default" {
		t.Fatalf("renewed session = %+v, %v", renewed, err)
	}
}

func TestWebSessionRenewRefusals(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	enrollLapsedBrowser(t, st, "device-browser", "session-lapsed")
	enrollLapsedBrowser(t, st, "device-gone", "session-gone")
	if err := st.RevokeDevice(context.Background(), "device-gone"); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		addr    string
		cookies map[string]string
		want    int
	}{
		{name: "wrong secret", addr: "127.0.0.1:5000", cookies: map[string]string{auth.DeviceCookie: "device-browser", auth.DeviceSecretCookie: "wrong"}, want: http.StatusUnauthorized},
		{name: "revoked device", addr: "127.0.0.1:5000", cookies: map[string]string{auth.DeviceCookie: "device-gone", auth.DeviceSecretCookie: recoverySecret}, want: http.StatusUnauthorized},
		{name: "missing cookies", addr: "127.0.0.1:5000", cookies: nil, want: http.StatusUnauthorized},
		{name: "public plain HTTP", addr: "203.0.113.5:4000", cookies: map[string]string{auth.DeviceCookie: "device-browser", auth.DeviceSecretCookie: recoverySecret}, want: http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := postRenew(srv, tc.addr, tc.cookies)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if _, ok := responseCookies(rec)[auth.SessionCookie]; ok {
				t.Fatalf("refused renewal set a session cookie: %v", rec.Result().Cookies())
			}
		})
	}
	if _, err := st.Session(context.Background(), "session-lapsed"); err != nil {
		t.Fatalf("a refused renewal removed the old session: %v", err)
	}
}

func TestWebSessionRenewRequiresCSRFHeader(t *testing.T) {
	srv := newTestServer(t)
	req := newJSONRequest(http.MethodPost, "/api/web/session/renew", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("renew without CSRF status = %d, want 403", rec.Code)
	}
}

func TestWebEnrollCookieExpiries(t *testing.T) {
	srv := newTestServer(t)
	cookies, _ := enrollWebBrowser(t, srv)
	byName := map[string]*http.Cookie{}
	for _, cookie := range cookies {
		byName[cookie.Name] = cookie
	}
	if want := time.Unix(0, 0).UTC().Add(30 * 24 * time.Hour); !byName[auth.SessionCookie].Expires.Equal(want) {
		t.Fatalf("session cookie expires %v, want %v", byName[auth.SessionCookie].Expires, want)
	}
	for _, name := range []string{auth.DeviceCookie, auth.DeviceSecretCookie} {
		if left := time.Until(byName[name].Expires); left < 399*24*time.Hour {
			t.Fatalf("%s cookie expires in %v, want about 400 days", name, left)
		}
	}
}

func postAdminSignIn(srv *Server, remoteAddr, host, body string) *httptest.ResponseRecorder {
	req := newJSONRequest(http.MethodPost, "/api/web/admin-signin", strings.NewReader(body))
	req.Header.Set(auth.CSRFHeader, "1")
	req.RemoteAddr = remoteAddr
	req.Host = host
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func newAdminSignInServer(t *testing.T, password string) (*Server, *store.Store) {
	t.Helper()
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if password != "" {
		if err := st.SetAdminPassword(ctx, password, time.Unix(0, 0).UTC()); err != nil {
			t.Fatal(err)
		}
	}
	return srv, st
}

const lanHost = "192.168.4.10:8800"

func TestWebAdminSignInFromLANEnrollsManager(t *testing.T) {
	srv, st := newAdminSignInServer(t, "correct horse")
	rec := postAdminSignIn(srv, "192.168.4.20:51000", lanHost, `{"password":"correct horse","deviceName":"Laptop"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin sign-in status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	cookies := assertSignInCookies(t, rec, time.Unix(0, 0).UTC().Add(30*24*time.Hour))
	if strings.Contains(rec.Body.String(), recoverySecret) || strings.Contains(rec.Body.String(), cookies[auth.DeviceSecretCookie].Value) {
		t.Fatalf("sign-in body leaks the device secret: %s", rec.Body.String())
	}
	session, err := st.Session(context.Background(), store.SessionID(cookies[auth.SessionCookie].Value))
	if err != nil || session.ProfileID != "profile-default" {
		t.Fatalf("signed-in session = %+v, %v", session, err)
	}
	device, err := st.VerifyDeviceSecret(context.Background(), session.DeviceID, cookies[auth.DeviceSecretCookie].Value)
	if err != nil || device.Role != store.DeviceRoleManager || device.Name != "Laptop" {
		t.Fatalf("signed-in device = %+v, %v", device, err)
	}
	if !strings.Contains(rec.Body.String(), `"deviceRole":"manager"`) {
		t.Fatalf("sign-in body = %s", rec.Body.String())
	}
}

func TestWebAdminSignInRefusals(t *testing.T) {
	tests := []struct {
		name     string
		password string // stored admin password; empty means none set
		addr     string
		host     string
		body     string
		want     int
		wantBody string
	}{
		{name: "public plain HTTP", password: "correct horse", addr: "203.0.113.5:4000", host: lanHost, body: `{"password":"correct horse","deviceName":"L"}`, want: http.StatusForbidden},
		{name: "LAN source addressing a rebindable public name", password: "correct horse", addr: "192.168.4.20:51000", host: "attacker.example", body: `{"password":"correct horse","deviceName":"L"}`, want: http.StatusForbidden},
		{name: "loopback source addressing a rebindable public name", password: "correct horse", addr: "127.0.0.1:51000", host: "attacker.example:8800", body: `{"password":"correct horse","deviceName":"L"}`, want: http.StatusForbidden},
		{name: "wrong password", password: "correct horse", addr: "192.168.4.20:51000", host: lanHost, body: `{"password":"battery staple","deviceName":"L"}`, want: http.StatusUnauthorized, wantBody: "sign-in failed"},
		{name: "no password set", addr: "192.168.4.20:51000", host: lanHost, body: `{"password":"anything","deviceName":"L"}`, want: http.StatusUnauthorized, wantBody: "sign-in failed"},
		{name: "missing device name", password: "correct horse", addr: "192.168.4.20:51000", host: lanHost, body: `{"password":"correct horse"}`, want: http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newAdminSignInServer(t, tc.password)
			rec := postAdminSignIn(srv, tc.addr, tc.host, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tc.wantBody)
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Fatalf("refused sign-in set cookies: %v", rec.Result().Cookies())
			}
		})
	}
}

func TestWebAdminSignInThrottlesRepeatedFailures(t *testing.T) {
	srv, _ := newAdminSignInServer(t, "correct horse")
	for attempt := 1; attempt <= 20; attempt++ {
		rec := postAdminSignIn(srv, "192.168.4.20:51000", lanHost, `{"password":"wrong","deviceName":"L"}`)
		switch rec.Code {
		case http.StatusUnauthorized:
			continue
		case http.StatusTooManyRequests:
			// The correct password is refused while the source is throttled.
			ok := postAdminSignIn(srv, "192.168.4.20:51000", lanHost, `{"password":"correct horse","deviceName":"L"}`)
			if ok.Code != http.StatusTooManyRequests {
				t.Fatalf("correct password while throttled status = %d, want 429", ok.Code)
			}
			return
		default:
			t.Fatalf("attempt %d status = %d: %s", attempt, rec.Code, rec.Body.String())
		}
	}
	t.Fatal("admin sign-in never throttled repeated failures")
}

package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// The canonical /web/ surface (post-cutover): the bundle root and any
// client-routed deep link both serve the SPA shell (index.html);
// BrowserRouter resolves the route on the client.
func TestWebServesReactShell(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{
		"/web/",
		"/web/search",
		"/web/profiles",
		"/web/mydata",
		"/web/watch/media-alpha",
		"/web/setup",
		"/web/enroll",
		"/web/promote",
		"/web/manage",
		"/web/manage/libraries",
		"/web/console",
		"/web/console/libraries",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", path, rec.Code, http.StatusOK)
		}
		if !strings.Contains(rec.Body.String(), `<div id="root">`) {
			t.Fatalf("GET %s did not serve the React shell (no #root mount)", path)
		}
	}
}

func TestWebRouteBoundaries(t *testing.T) {
	srv := newTestServer(t)

	root := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	srv.ServeHTTP(rootRec, root)
	if rootRec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("GET / status = %d, want %d", rootRec.Code, http.StatusTemporaryRedirect)
	}
	if got, want := rootRec.Header().Get("Location"), "/web/"; got != want {
		t.Fatalf("GET / Location = %q, want %q", got, want)
	}

	noSlash := httptest.NewRequest(http.MethodGet, "/web", nil)
	noSlashRec := httptest.NewRecorder()
	srv.ServeHTTP(noSlashRec, noSlash)
	if noSlashRec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("GET /web status = %d, want %d", noSlashRec.Code, http.StatusTemporaryRedirect)
	}
	if got, want := noSlashRec.Header().Get("Location"), "/web/"; got != want {
		t.Fatalf("GET /web Location = %q, want %q", got, want)
	}

	assetReq := httptest.NewRequest(http.MethodGet, "/web/favicon.svg", nil)
	assetRec := httptest.NewRecorder()
	srv.ServeHTTP(assetRec, assetReq)
	if assetRec.Code != http.StatusOK {
		t.Fatalf("GET /web/favicon.svg status = %d, want %d", assetRec.Code, http.StatusOK)
	}
	if strings.Contains(assetRec.Body.String(), `<div id="root">`) {
		t.Fatal("GET /web/favicon.svg served the SPA shell, want static asset")
	}

	missingAsset := httptest.NewRequest(http.MethodGet, "/web/assets/nope.js", nil)
	missingAssetRec := httptest.NewRecorder()
	srv.ServeHTTP(missingAssetRec, missingAsset)
	if missingAssetRec.Code != http.StatusNotFound {
		t.Fatalf("GET /web/assets/nope.js status = %d, want %d", missingAssetRec.Code, http.StatusNotFound)
	}
	if strings.Contains(missingAssetRec.Body.String(), `<div id="root">`) {
		t.Fatal("GET /web/assets/nope.js served the SPA shell, want static miss")
	}

	for _, path := range []string{
		"/web/watch/media.alpha",
		"/web/entity/entity.kind.value",
		"/web/n/node.alpha",
		"/web/seeall/source.alpha",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", path, rec.Code, http.StatusOK)
		}
		if !strings.Contains(rec.Body.String(), `<div id="root">`) {
			t.Fatalf("GET %s did not serve the React shell for dotted SPA deep link", path)
		}
	}

	for _, tt := range []struct {
		path string
		want int
	}{
		{path: "/api/not-a-route", want: http.StatusNotFound},
		{path: "/v1/not-a-route", want: http.StatusNotFound},
		{path: "/web-legacy/", want: http.StatusNotFound},
		{path: "/web-legacy/not-a-route", want: http.StatusNotFound},
		{path: "/web-legacy/app.js", want: http.StatusNotFound},
		{path: "/health/not-a-route", want: http.StatusNotFound},
		{path: "/metrics/not-a-route", want: http.StatusNotFound},
	} {
		req := httptest.NewRequest(http.MethodGet, tt.path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Fatalf("GET %s status = %d, want %d", tt.path, rec.Code, tt.want)
		}
		if rec.Header().Get("Location") != "" {
			t.Fatalf("GET %s redirected to %q, want no redirect", tt.path, rec.Header().Get("Location"))
		}
		if strings.Contains(rec.Body.String(), `<div id="root">`) {
			t.Fatalf("GET %s served the SPA shell outside /web/", tt.path)
		}
	}
}

func TestWebManifestServesManifestContentType(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/web/manifest.webmanifest", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Header().Get("Content-Type"), "application/manifest+json"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	if !strings.Contains(rec.Body.String(), `"name"`) {
		t.Fatalf("manifest body missing name field: %q", rec.Body.String())
	}
}

func TestWebEnrollSetsCookiesAndCookieAuthRoundTrip(t *testing.T) {
	srv := newTestServer(t)
	cookies, body := enrollWebBrowser(t, srv)

	byName := map[string]*http.Cookie{}
	for _, cookie := range cookies {
		byName[cookie.Name] = cookie
	}
	for _, name := range []string{auth.SessionCookie, auth.DeviceCookie, auth.DeviceSecretCookie} {
		cookie := byName[name]
		if cookie == nil {
			t.Fatalf("cookie %q not set", name)
		}
		if cookie.Value == "" {
			t.Fatalf("cookie %q is empty", name)
		}
		// enrollWebBrowser uses a plain-HTTP request (r.TLS == nil), so Secure
		// must be false here (H21): a plain-HTTP LAN browser would never receive
		// a Secure cookie. HttpOnly and SameSite=Strict stay unconditional.
		if !cookie.HttpOnly || cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
			t.Fatalf("cookie %q attributes = HttpOnly:%v Secure:%v SameSite:%v, want HttpOnly !Secure Strict (plain HTTP)",
				name, cookie.HttpOnly, cookie.Secure, cookie.SameSite)
		}
	}
	// The device secret must never enter JS-readable space: cookie only.
	if strings.Contains(body, byName[auth.DeviceSecretCookie].Value) {
		t.Fatal("enroll response body leaks the device secret")
	}

	manifestReq := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	for _, cookie := range cookies {
		manifestReq.AddCookie(cookie)
	}
	manifestRec := httptest.NewRecorder()
	srv.ServeHTTP(manifestRec, manifestReq)
	if manifestRec.Code != http.StatusOK {
		t.Fatalf("cookie manifest status = %d, want %d: %s", manifestRec.Code, http.StatusOK, manifestRec.Body.String())
	}
	if !strings.Contains(manifestRec.Body.String(), "\"navigation\"") {
		t.Fatalf("cookie manifest body = %q, want a manifest document", manifestRec.Body.String())
	}
}

// TestWebCredentialCookieSecureTracksTLS pins H21: the Secure flag must reflect
// whether the request arrived over an effectively-HTTPS connection (r.TLS set),
// so a plain-HTTP LAN browser can receive (and round-trip) the cookie, while a
// direct-TLS request still gets Secure. HttpOnly and SameSite=Strict are
// unconditional in both cases.
func TestWebCredentialCookieSecureTracksTLS(t *testing.T) {
	cookieFor := func(r *http.Request) *http.Cookie {
		t.Helper()
		rec := httptest.NewRecorder()
		setWebCredentialCookie(rec, r, auth.SessionCookie, "session-value", time.Time{})
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("set %d cookies, want 1", len(cookies))
		}
		return cookies[0]
	}

	plain := newJSONRequest(http.MethodPost, "/api/web/enroll", nil)
	if plain.TLS != nil {
		t.Fatal("plain request unexpectedly has TLS state")
	}
	plainCookie := cookieFor(plain)
	if plainCookie.Secure {
		t.Fatalf("plain-HTTP cookie Secure = true, want false")
	}
	if !plainCookie.HttpOnly || plainCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("plain-HTTP cookie HttpOnly:%v SameSite:%v, want HttpOnly Strict",
			plainCookie.HttpOnly, plainCookie.SameSite)
	}

	tlsReq := newJSONRequest(http.MethodPost, "/api/web/enroll", nil)
	tlsReq.TLS = &tls.ConnectionState{} // marks the request as arriving over TLS
	tlsCookie := cookieFor(tlsReq)
	if !tlsCookie.Secure {
		t.Fatalf("TLS cookie Secure = false, want true")
	}
	if !tlsCookie.HttpOnly || tlsCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("TLS cookie HttpOnly:%v SameSite:%v, want HttpOnly Strict",
			tlsCookie.HttpOnly, tlsCookie.SameSite)
	}
}

func TestWebSetupRejectsPublicPlainHTTPBeforeConsumingToken(t *testing.T) {
	srv := newTestServer(t)
	srv.pairing.SetBootstrapToken("first-run-token")
	body := bytes.NewReader([]byte(`{"token":"first-run-token","deviceName":"Browser"}`))
	req := newJSONRequest(http.MethodPost, "/api/setup", body)
	req.RemoteAddr = "203.0.113.7:5000"
	req.Header.Set(auth.CSRFHeader, "1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("public plaintext setup status = %d, want 403", rec.Code)
	}
	if !srv.pairing.SetupRequired() {
		t.Fatal("setup token was consumed by rejected public plaintext request")
	}
}

func TestWebEnrollRequiresCSRFHeader(t *testing.T) {
	srv := newTestServer(t)
	req := newJSONRequest(http.MethodPost, "/api/web/enroll", bytes.NewReader([]byte(`{"code":"ABCDEFGH","deviceName":"Web Browser"}`)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestCookieAuthenticatedMutationRequiresCSRFHeader(t *testing.T) {
	srv := newTestServer(t)
	cookies, _ := enrollWebBrowser(t, srv)
	body := []byte(`{"mediaID":"media-alpha","capabilityProfile":{"client":"beamfall-web","containers":["mp4"],"directCodecs":["h264","hevc"]}}`)

	// A cross-origin-style request carries the cookies (the browser attaches
	// them) but cannot set the custom header — it must be rejected.
	crossReq := newJSONRequest(http.MethodPost, "/api/playback-info", bytes.NewReader(body))
	for _, cookie := range cookies {
		crossReq.AddCookie(cookie)
	}
	crossRec := httptest.NewRecorder()
	srv.ServeHTTP(crossRec, crossReq)
	if crossRec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin-style status = %d, want %d", crossRec.Code, http.StatusForbidden)
	}

	sameReq := newJSONRequest(http.MethodPost, "/api/playback-info", bytes.NewReader(body))
	for _, cookie := range cookies {
		sameReq.AddCookie(cookie)
	}
	sameReq.Header.Set(auth.CSRFHeader, "1")
	sameRec := httptest.NewRecorder()
	srv.ServeHTTP(sameRec, sameReq)
	if sameRec.Code != http.StatusOK {
		t.Fatalf("same-origin status = %d, want %d: %s", sameRec.Code, http.StatusOK, sameRec.Body.String())
	}
}

// mintPairingCode mints a pairing code for the given role as the manager device.
func mintPairingCode(t *testing.T, srv *Server, role string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"deviceRole": role})
	if err != nil {
		t.Fatal(err)
	}
	createReq := newJSONRequest(http.MethodPost, "/api/pairing-codes", bytes.NewReader(body))
	authorizeManager(createReq)
	createRec := httptest.NewRecorder()
	srv.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("pairing code status = %d, want %d: %s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	var codeResp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &codeResp); err != nil {
		t.Fatalf("decode pairing code: %v", err)
	}
	return codeResp.Code
}

// enrollWebBrowser mints a pairing code as the manager and redeems it through
// the web enroll route, returning the credential cookies and response body.
func enrollWebBrowser(t *testing.T, srv *Server) ([]*http.Cookie, string) {
	t.Helper()
	enrollBody, err := json.Marshal(map[string]string{"code": mintPairingCode(t, srv, "viewer"), "deviceName": "Web Browser"})
	if err != nil {
		t.Fatal(err)
	}
	enrollReq := newJSONRequest(http.MethodPost, "/api/web/enroll", bytes.NewReader(enrollBody))
	enrollReq.RemoteAddr = "127.0.0.1:5000"
	enrollReq.Header.Set(auth.CSRFHeader, "1")
	enrollRec := httptest.NewRecorder()
	srv.ServeHTTP(enrollRec, enrollReq)
	if enrollRec.Code != http.StatusCreated {
		t.Fatalf("web enroll status = %d, want %d: %s", enrollRec.Code, http.StatusCreated, enrollRec.Body.String())
	}
	return enrollRec.Result().Cookies(), enrollRec.Body.String()
}

// promoteWebBrowser posts a promotion request with the browser's cookies.
func promoteWebBrowser(t *testing.T, srv *Server, cookies []*http.Cookie, code string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		t.Fatal(err)
	}
	req := newJSONRequest(http.MethodPost, "/api/web/promote", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:5000"
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	req.Header.Set(auth.CSRFHeader, "1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestWebPromoteUpgradesBrowserToManager(t *testing.T) {
	srv := newTestServer(t)
	cookies, _ := enrollWebBrowser(t, srv)

	// The viewer browser cannot mint pairing codes before promotion.
	mintReq := newJSONRequest(http.MethodPost, "/api/pairing-codes", bytes.NewReader([]byte(`{"deviceRole":"viewer"}`)))
	for _, cookie := range cookies {
		mintReq.AddCookie(cookie)
	}
	mintReq.Header.Set(auth.CSRFHeader, "1")
	mintRec := httptest.NewRecorder()
	srv.ServeHTTP(mintRec, mintReq)
	if mintRec.Code != http.StatusForbidden {
		t.Fatalf("pre-promotion mint status = %d, want %d: %s", mintRec.Code, http.StatusForbidden, mintRec.Body.String())
	}

	rec := promoteWebBrowser(t, srv, cookies, mintPairingCode(t, srv, "manager"))
	if rec.Code != http.StatusOK {
		t.Fatalf("promote status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		DeviceID   string `json:"deviceID"`
		DeviceRole string `json:"deviceRole"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode promote response: %v", err)
	}
	if resp.DeviceRole != "manager" {
		t.Fatalf("deviceRole = %q, want manager", resp.DeviceRole)
	}
	rotated := rec.Result().Cookies()
	if len(rotated) != 3 {
		t.Fatalf("promote set %d cookies, want 3", len(rotated))
	}

	oldReq := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	for _, cookie := range cookies {
		oldReq.AddCookie(cookie)
	}
	oldRec := httptest.NewRecorder()
	srv.ServeHTTP(oldRec, oldReq)
	if oldRec.Code != http.StatusUnauthorized {
		t.Fatalf("old credential status = %d, want %d: %s", oldRec.Code, http.StatusUnauthorized, oldRec.Body.String())
	}

	newReq := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	for _, cookie := range rotated {
		newReq.AddCookie(cookie)
	}
	newRec := httptest.NewRecorder()
	srv.ServeHTTP(newRec, newReq)
	if newRec.Code != http.StatusOK {
		t.Fatalf("rotated credential status = %d, want %d: %s", newRec.Code, http.StatusOK, newRec.Body.String())
	}

	// Promotion grants manager role, but minting still requires a fresh Admin Session.
	mintReq = newJSONRequest(http.MethodPost, "/api/pairing-codes", bytes.NewReader([]byte(`{"deviceRole":"viewer"}`)))
	for _, cookie := range rotated {
		mintReq.AddCookie(cookie)
	}
	mintReq.Header.Set(auth.CSRFHeader, "1")
	mintRec = httptest.NewRecorder()
	srv.ServeHTTP(mintRec, mintReq)
	if mintRec.Code != http.StatusForbidden {
		t.Fatalf("post-promotion mint status = %d, want %d: %s", mintRec.Code, http.StatusForbidden, mintRec.Body.String())
	}
}

func TestWebPromoteRejectsViewerRoleCode(t *testing.T) {
	srv := newTestServer(t)
	cookies, _ := enrollWebBrowser(t, srv)
	code := mintPairingCode(t, srv, "viewer")

	rec := promoteWebBrowser(t, srv, cookies, code)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("promote status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	// The viewer code was not consumed: it still enrolls a viewer device.
	enrollBody, err := json.Marshal(map[string]string{"code": code, "deviceName": "Other Browser"})
	if err != nil {
		t.Fatal(err)
	}
	enrollReq := newJSONRequest(http.MethodPost, "/api/web/enroll", bytes.NewReader(enrollBody))
	enrollReq.RemoteAddr = "127.0.0.1:5000"
	enrollReq.Header.Set(auth.CSRFHeader, "1")
	enrollRec := httptest.NewRecorder()
	srv.ServeHTTP(enrollRec, enrollReq)
	if enrollRec.Code != http.StatusCreated {
		t.Fatalf("enroll-after-rejected-promote status = %d, want %d: %s", enrollRec.Code, http.StatusCreated, enrollRec.Body.String())
	}
}

func TestWebPromoteRejectsUnknownCode(t *testing.T) {
	srv := newTestServer(t)
	cookies, _ := enrollWebBrowser(t, srv)

	rec := promoteWebBrowser(t, srv, cookies, "NOPE9999")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("promote status = %d, want %d: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestWebPromoteRequiresCSRFHeader(t *testing.T) {
	srv := newTestServer(t)
	cookies, _ := enrollWebBrowser(t, srv)

	req := newJSONRequest(http.MethodPost, "/api/web/promote", bytes.NewReader([]byte(`{"code":"ABCDEFGH"}`)))
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestWebPromoteRequiresEnrolledSession(t *testing.T) {
	srv := newTestServer(t)
	code := mintPairingCode(t, srv, "manager")

	body, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		t.Fatal(err)
	}
	req := newJSONRequest(http.MethodPost, "/api/web/promote", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set(auth.CSRFHeader, "1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// logoutWebBrowser posts the logout route with the given cookies and the CSRF
// header, returning the recorder.
func logoutWebBrowser(srv *Server, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := newJSONRequest(http.MethodPost, "/api/web/logout", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	req.Header.Set(auth.CSRFHeader, "1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// replayCookies issues a cookie-authenticated GET /api/manifest and returns the
// status code — the probe for whether a credential still authorizes.
func replayCookies(srv *Server, cookies []*http.Cookie) int {
	req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec.Code
}

func TestWebLogoutClearsCredentialCookies(t *testing.T) {
	srv := newTestServer(t)

	req := newJSONRequest(http.MethodPost, "/api/web/logout", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("logout without CSRF header status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	cookies, _ := enrollWebBrowser(t, srv)
	logoutRec := logoutWebBrowser(srv, cookies)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout status = %d: %s", logoutRec.Code, logoutRec.Body.String())
	}
	cleared := map[string]bool{}
	for _, cookie := range logoutRec.Result().Cookies() {
		if cookie.Value == "" && cookie.Expires.Before(time.Now()) {
			cleared[cookie.Name] = true
		}
	}
	for _, name := range []string{auth.SessionCookie, auth.DeviceCookie, auth.DeviceSecretCookie} {
		if !cleared[name] {
			t.Fatalf("cookie %q not cleared; cookies = %#v", name, logoutRec.Result().Cookies())
		}
	}
}

// Logout is a session mutation, so it must authenticate: an anonymous caller
// has no session to revoke and must not receive a success.
func TestWebLogoutRequiresSession(t *testing.T) {
	srv := newTestServer(t)
	rec := logoutWebBrowser(srv, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous logout status = %d, want %d: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

// Logout must revoke the calling session server-side — clearing cookies alone
// leaves a captured credential valid for the session's remaining server-side
// lifetime. Exactly the calling session dies: another browser's session
// survives, and the revoked session's outstanding Stream-Token ledger activity
// is terminated with it.
func TestWebLogoutRevokesCallingSessionServerSide(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	cookiesA, bodyA := enrollWebBrowser(t, srv)
	cookiesB, _ := enrollWebBrowser(t, srv)

	var enrolledA struct {
		DeviceID  string `json:"deviceID"`
		ProfileID string `json:"profileID"`
	}
	if err := json.Unmarshal([]byte(bodyA), &enrolledA); err != nil {
		t.Fatalf("decode enroll response: %v", err)
	}
	var sessionA string
	for _, cookie := range cookiesA {
		if cookie.Name == auth.SessionCookie {
			sessionA = cookie.Value
		}
	}
	if sessionA == "" {
		t.Fatal("enroll set no session cookie")
	}

	// A live playback for session A: one outstanding media Stream Token.
	now := time.Unix(0, 0).UTC()
	err := st.RecordStreamToken(context.Background(), store.SessionID(sessionA), store.ProfileID(enrolledA.ProfileID),
		store.DeviceID(enrolledA.DeviceID), "media-direct", "media-alpha", false, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("RecordStreamToken: %v", err)
	}
	if count, err := st.ActiveSessionCount(context.Background(), now); err != nil || count != 1 {
		t.Fatalf("pre-logout ActiveSessionCount = %d, %v, want 1", count, err)
	}

	// Pre-logout, the credential authorizes (the replay-capture premise).
	if code := replayCookies(srv, cookiesA); code != http.StatusOK {
		t.Fatalf("pre-logout replay status = %d, want %d", code, http.StatusOK)
	}

	if rec := logoutWebBrowser(srv, cookiesA); rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d: %s", rec.Code, rec.Body.String())
	}

	// The replayed pre-logout credential no longer authorizes.
	if code := replayCookies(srv, cookiesA); code != http.StatusUnauthorized {
		t.Fatalf("post-logout replay status = %d, want %d", code, http.StatusUnauthorized)
	}
	// The other browser's session survives.
	if code := replayCookies(srv, cookiesB); code != http.StatusOK {
		t.Fatalf("other session replay status = %d, want %d", code, http.StatusOK)
	}
	// The revoked session's ledger activity is terminated with it.
	if count, err := st.ActiveSessionCount(context.Background(), now); err != nil || count != 0 {
		t.Fatalf("post-logout ActiveSessionCount = %d, %v, want 0", count, err)
	}
}

// failingRevocationJobStats keeps the wired JobStatsProvider but refuses
// session revocation, standing in for a store whose delete failed.
type failingRevocationJobStats struct{ JobStatsProvider }

func (failingRevocationJobStats) TerminateSession(context.Context, store.SessionID) error {
	return errors.New("revocation refused")
}

// revokerlessJobStats satisfies only JobStatsProvider: a wiring with no
// revocation capability at all.
type revokerlessJobStats struct{ JobStatsProvider }

// Logout fails closed: when revocation fails (or the wiring cannot revoke at
// all), the handler must not report success and must not clear the cookies —
// the credential is still alive server-side, and pretending otherwise would
// leave the user believing a captured credential is dead.
func TestWebLogoutFailedRevocationFailsClosed(t *testing.T) {
	srv := newTestServer(t)
	cookies, _ := enrollWebBrowser(t, srv)
	wired := srv.jobStats

	for name, replacement := range map[string]JobStatsProvider{
		"revocation error":     failingRevocationJobStats{JobStatsProvider: wired},
		"revocation unwirable": revokerlessJobStats{JobStatsProvider: wired},
	} {
		srv.jobStats = replacement
		rec := logoutWebBrowser(srv, cookies)
		srv.jobStats = wired
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: logout status = %d, want %d: %s", name, rec.Code, http.StatusInternalServerError, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "logged-out") {
			t.Fatalf("%s: failed revocation reported success: %s", name, rec.Body.String())
		}
		if got := len(rec.Result().Cookies()); got != 0 {
			t.Fatalf("%s: failed revocation cleared %d cookies, want 0", name, got)
		}
		// The session is still alive — the caller must be told logout failed.
		if code := replayCookies(srv, cookies); code != http.StatusOK {
			t.Fatalf("%s: session after failed logout status = %d, want %d", name, code, http.StatusOK)
		}
	}
}

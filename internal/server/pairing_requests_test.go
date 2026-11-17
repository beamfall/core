package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/store"
)

// devicePairingCall sends one JSON request and returns the recorder.
func devicePairingCall(t *testing.T, srv *Server, method, path, body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if method == http.MethodGet {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = newJSONRequest(method, path, bytes.NewReader([]byte(body)))
	}
	if authorize != nil {
		authorize(req)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func wantDevicePairingStatus(t *testing.T, rec *httptest.ResponseRecorder, want int, step string) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("%s status = %d, want %d: %s", step, rec.Code, want, rec.Body.String())
	}
}

func decodeDevicePairing[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return out
}

func requestDevicePairingOverHTTP(t *testing.T, srv *Server, name string) pairing.DeviceRequestResponse {
	t.Helper()
	body, err := json.Marshal(map[string]string{"deviceName": name})
	if err != nil {
		t.Fatal(err)
	}
	rec := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", string(body), nil)
	wantDevicePairingStatus(t, rec, http.StatusCreated, "request")
	resp := decodeDevicePairing[pairing.DeviceRequestResponse](t, rec)
	if resp.Code == "" || resp.RequestSecret == "" || resp.ExpiresAt == "" || resp.PollIntervalSeconds <= 0 {
		t.Fatalf("request response = %+v", resp)
	}
	return resp
}

func secretBody(secret string) string {
	return fmt.Sprintf(`{"requestSecret":%q}`, secret)
}

func codeBody(code string) string {
	return fmt.Sprintf(`{"code":%q}`, code)
}

func TestDevicePairingRequestApprovedByAdminEnrolls(t *testing.T) {
	srv := newTestServer(t)
	req := requestDevicePairingOverHTTP(t, srv, "Living Room TV")

	poll := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(req.RequestSecret), nil)
	wantDevicePairingStatus(t, poll, http.StatusOK, "poll before approval")
	if got := decodeDevicePairing[pairing.DevicePollResponse](t, poll); got.Status != pairing.DeviceRequestPending || got.Enrollment != nil {
		t.Fatalf("poll before approval = %+v", got)
	}

	// Pending list, approve and deny need an administrator.
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodGet, "/api/pairing-requests", "", nil), http.StatusUnauthorized, "anonymous list")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodGet, "/api/pairing-requests", "", authorizeViewer), http.StatusForbidden, "viewer list")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(req.Code), nil), http.StatusUnauthorized, "anonymous approve")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(req.Code), authorizeViewer), http.StatusForbidden, "viewer approve")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/deny", codeBody(req.Code), authorizeViewer), http.StatusForbidden, "viewer deny")

	list := devicePairingCall(t, srv, http.MethodGet, "/api/pairing-requests", "", authorizeManager)
	wantDevicePairingStatus(t, list, http.StatusOK, "admin list")
	pending := decodeDevicePairing[pairing.PendingDeviceRequests](t, list)
	if len(pending.Requests) != 1 || pending.Requests[0].Code != req.Code || pending.Requests[0].DeviceName != "Living Room TV" {
		t.Fatalf("pending = %+v", pending)
	}
	if strings.Contains(list.Body.String(), req.RequestSecret) {
		t.Fatal("pending list leaked the request secret")
	}

	approve := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(req.Code), authorizeManager)
	wantDevicePairingStatus(t, approve, http.StatusOK, "approve")
	if got := decodeDevicePairing[pairing.DeviceRequestDecisionResponse](t, approve); got.Status != pairing.DeviceRequestApproved || got.DeviceRole != "viewer" {
		t.Fatalf("approve = %+v", got)
	}

	// The display code alone collects nothing.
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(req.Code), nil), http.StatusUnauthorized, "poll with the display code")

	poll = devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(req.RequestSecret), nil)
	wantDevicePairingStatus(t, poll, http.StatusOK, "poll after approval")
	got := decodeDevicePairing[pairing.DevicePollResponse](t, poll)
	if got.Status != pairing.DeviceRequestApproved || got.Enrollment == nil || got.Enrollment.DeviceSecret == "" {
		t.Fatalf("poll after approval = %+v", got)
	}
	manifestReq := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	manifestReq.Header.Set("X-Beamfall-Session", got.Enrollment.SessionID)
	manifestReq.Header.Set("X-Beamfall-Device", got.Enrollment.DeviceID)
	manifestReq.Header.Set("X-Beamfall-Device-Secret", got.Enrollment.DeviceSecret)
	manifestRec := httptest.NewRecorder()
	srv.ServeHTTP(manifestRec, manifestReq)
	wantDevicePairingStatus(t, manifestRec, http.StatusOK, "manifest with the delivered credential")

	// Spent: the secret, the code and the approval do not work again.
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(req.RequestSecret), nil), http.StatusUnauthorized, "reused secret")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(req.Code), authorizeManager), http.StatusNotFound, "re-approve")
	enroll := devicePairingCall(t, srv, http.MethodPost, "/api/enroll", fmt.Sprintf(`{"code":%q,"deviceName":"Thief"}`, req.Code), nil)
	wantDevicePairingStatus(t, enroll, http.StatusUnauthorized, "/api/enroll with a request code")
}

func TestDevicePairingRequestDenyAndRegenerate(t *testing.T) {
	srv := newTestServer(t)
	denied := requestDevicePairingOverHTTP(t, srv, "Kitchen Tablet")
	deny := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/deny", codeBody(denied.Code), authorizeManager)
	wantDevicePairingStatus(t, deny, http.StatusOK, "deny")
	if got := decodeDevicePairing[pairing.DeviceRequestDecisionResponse](t, deny); got.Status != pairing.DeviceRequestDenied {
		t.Fatalf("deny = %+v", got)
	}
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(denied.Code), authorizeManager), http.StatusNotFound, "approve after deny")
	poll := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(denied.RequestSecret), nil)
	wantDevicePairingStatus(t, poll, http.StatusOK, "poll after deny")
	if got := decodeDevicePairing[pairing.DevicePollResponse](t, poll); got.Status != pairing.DeviceRequestDenied || got.Enrollment != nil {
		t.Fatalf("poll after deny = %+v", got)
	}

	old := requestDevicePairingOverHTTP(t, srv, "Bedroom TV")
	regen := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/regenerate", secretBody(old.RequestSecret), nil)
	wantDevicePairingStatus(t, regen, http.StatusCreated, "regenerate")
	fresh := decodeDevicePairing[pairing.DeviceRequestResponse](t, regen)
	if fresh.Code == old.Code || fresh.RequestSecret == old.RequestSecret {
		t.Fatalf("regenerate kept the old code or secret: %+v", fresh)
	}
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(old.Code), authorizeManager), http.StatusNotFound, "approve a regenerated-away code")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(old.RequestSecret), nil), http.StatusUnauthorized, "poll a regenerated-away secret")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(fresh.Code), authorizeManager), http.StatusOK, "approve the fresh code")
}

func TestDevicePairingRequestExpiryAndValidation(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	now := time.Unix(0, 0).UTC()
	srv.pairing = pairing.NewService(st, func() time.Time { return now })

	req := requestDevicePairingOverHTTP(t, srv, "Den TV")
	now = now.Add(11 * time.Minute)
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(req.Code), authorizeManager), http.StatusNotFound, "approve an expired code")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(req.RequestSecret), nil), http.StatusUnauthorized, "poll an expired request")

	for _, body := range []string{`{"deviceName":""}`, `{"deviceName":"TV\u0007"}`, `{"deviceName":"TV\u202e"}`, fmt.Sprintf(`{"deviceName":%q}`, strings.Repeat("x", 65))} {
		wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", body, nil), http.StatusBadRequest, "request "+body)
	}
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", `{"code":""}`, authorizeManager), http.StatusBadRequest, "approve without a code")
}

func TestDevicePairingRequestPollIsThrottled(t *testing.T) {
	srv := newTestServer(t)
	var last *httptest.ResponseRecorder
	for i := range 8 {
		last = devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(fmt.Sprintf("guess-%d", i)), nil)
		if last.Code == http.StatusTooManyRequests {
			break
		}
		wantDevicePairingStatus(t, last, http.StatusUnauthorized, "guess")
	}
	wantDevicePairingStatus(t, last, http.StatusTooManyRequests, "guessing past the throttle")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"TV"}`, nil), http.StatusTooManyRequests, "request from a throttled source")
}

func TestDevicePairingRequestViewerSwitchRefusesWithInvalidCode(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	pending := requestDevicePairingOverHTTP(t, srv, "Den TV")
	disabled := true
	if _, err := st.SetConsoleConfig(context.Background(), store.ConsoleConfigPatch{ViewerPairingCodesDisabled: &disabled}, time.Unix(0, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	refused := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"TV"}`, nil)
	wantDevicePairingStatus(t, refused, http.StatusUnauthorized, "viewer request with the switch on")
	if !strings.Contains(refused.Body.String(), "invalid pairing code") {
		t.Fatalf("refusal body = %s", refused.Body.String())
	}
	approve := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(pending.Code), authorizeManager)
	wantDevicePairingStatus(t, approve, http.StatusNotFound, "viewer approval with the switch on")
	if !strings.Contains(approve.Body.String(), "invalid pairing code") {
		t.Fatalf("approval refusal body = %s", approve.Body.String())
	}
}

func TestDevicePairingRequestShowsDeviceIdentityToAdmin(t *testing.T) {
	srv := newTestServer(t)
	// lan is derived from the connection; a client-supplied value is ignored.
	rec := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"Office Mac","platform":"macos","requestedRole":"manager","lan":false}`, nil)
	wantDevicePairingStatus(t, rec, http.StatusCreated, "request with identity")
	req := decodeDevicePairing[pairing.DeviceRequestResponse](t, rec)

	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"TV","requestedRole":"admin"}`, nil), http.StatusUnauthorized, "request the admin role")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"TV","platform":"tv\u0007os"}`, nil), http.StatusBadRequest, "control character in platform")

	list := devicePairingCall(t, srv, http.MethodGet, "/api/pairing-requests", "", authorizeManager)
	wantDevicePairingStatus(t, list, http.StatusOK, "list")
	var raw struct {
		Requests []map[string]any `json:"requests"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &raw); err != nil || len(raw.Requests) != 1 {
		t.Fatalf("list = %s, %v", list.Body.String(), err)
	}
	got := raw.Requests[0]
	if got["code"] != req.Code || got["deviceName"] != "Office Mac" || got["platform"] != "macos" || got["requestedRole"] != "manager" || got["lan"] != true {
		t.Fatalf("pending entry = %v", got)
	}

	// An approval that names no role grants Viewer, whatever the device
	// asked for; manager needs the admin to name it.
	approve := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(req.Code), authorizeManager)
	wantDevicePairingStatus(t, approve, http.StatusOK, "approve")
	if decision := decodeDevicePairing[pairing.DeviceRequestDecisionResponse](t, approve); decision.DeviceRole != "viewer" {
		t.Fatalf("approval without a role = %+v, want viewer", decision)
	}
	poll := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(req.RequestSecret), nil)
	wantDevicePairingStatus(t, poll, http.StatusOK, "collect the default approval")
	if got := decodeDevicePairing[pairing.DevicePollResponse](t, poll); got.Enrollment == nil || got.Enrollment.DeviceRole != "viewer" {
		t.Fatalf("default approval enrolled %+v, want a viewer device", got.Enrollment)
	}

	rec = devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"Office Mac","requestedRole":"manager"}`, nil)
	wantDevicePairingStatus(t, rec, http.StatusCreated, "second manager request")
	req = decodeDevicePairing[pairing.DeviceRequestResponse](t, rec)
	approve = devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", fmt.Sprintf(`{"code":%q,"deviceRole":"manager"}`, req.Code), authorizeManager)
	wantDevicePairingStatus(t, approve, http.StatusOK, "approve naming manager")
	if decision := decodeDevicePairing[pairing.DeviceRequestDecisionResponse](t, approve); decision.DeviceRole != "manager" {
		t.Fatalf("approval naming manager = %+v", decision)
	}
}

// browserCall marks a call the web app sends: every web request carries the
// CSRF header (CORE-2070).
func browserCall(req *http.Request) { req.Header.Set(auth.CSRFHeader, "1") }

// publicPlainHTTP makes a call arrive from off the LAN over plain HTTP.
func publicPlainHTTP(req *http.Request) { req.RemoteAddr = "203.0.113.7:5000" }

func browserRequestDevicePairing(t *testing.T, srv *Server) pairing.DeviceRequestResponse {
	t.Helper()
	rec := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"Chrome on macOS","platform":"web"}`, browserCall)
	wantDevicePairingStatus(t, rec, http.StatusCreated, "browser request")
	return decodeDevicePairing[pairing.DeviceRequestResponse](t, rec)
}

// wantBrowserEnrollment asserts a collected browser request: the credential is
// in HttpOnly cookies and nowhere in the page-readable body.
func wantBrowserEnrollment(t *testing.T, srv *Server, poll *httptest.ResponseRecorder) {
	t.Helper()
	wantDevicePairingStatus(t, poll, http.StatusOK, "browser poll after approval")
	got := decodeDevicePairing[pairing.DevicePollResponse](t, poll)
	if got.Status != pairing.DeviceRequestApproved || got.Enrollment == nil || got.Enrollment.DeviceID == "" {
		t.Fatalf("browser poll = %+v", got)
	}
	if got.Enrollment.DeviceSecret != "" || got.Enrollment.SessionID != "" {
		t.Fatalf("browser poll body carries the credential: %+v", got.Enrollment)
	}
	byName := map[string]*http.Cookie{}
	for _, cookie := range poll.Result().Cookies() {
		byName[cookie.Name] = cookie
	}
	for _, name := range []string{auth.SessionCookie, auth.DeviceCookie, auth.DeviceSecretCookie} {
		cookie := byName[name]
		if cookie == nil || cookie.Value == "" {
			t.Fatalf("cookie %q not set", name)
		}
		if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
			t.Fatalf("cookie %q HttpOnly:%v SameSite:%v, want HttpOnly Strict", name, cookie.HttpOnly, cookie.SameSite)
		}
	}
	if strings.Contains(poll.Body.String(), byName[auth.DeviceSecretCookie].Value) || strings.Contains(poll.Body.String(), byName[auth.SessionCookie].Value) {
		t.Fatal("browser poll body leaks the credential")
	}

	manifestReq := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	for _, cookie := range poll.Result().Cookies() {
		manifestReq.AddCookie(cookie)
	}
	manifestRec := httptest.NewRecorder()
	srv.ServeHTTP(manifestRec, manifestReq)
	wantDevicePairingStatus(t, manifestRec, http.StatusOK, "manifest with the poll cookies")
}

// A request made by the web app is a browser request for its whole life: page
// script that polls without the CSRF header still gets cookies, never the
// credential in JSON.
func TestDevicePairingBrowserBindingIsFixedAtRequest(t *testing.T) {
	srv := newTestServer(t)
	req := browserRequestDevicePairing(t, srv)
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(req.Code), authorizeManager), http.StatusOK, "approve")
	wantBrowserEnrollment(t, srv, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(req.RequestSecret), nil))

	// The binding survives a regenerate, with or without the header.
	req = browserRequestDevicePairing(t, srv)
	regen := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/regenerate", secretBody(req.RequestSecret), nil)
	wantDevicePairingStatus(t, regen, http.StatusCreated, "regenerate a browser request")
	fresh := decodeDevicePairing[pairing.DeviceRequestResponse](t, regen)
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(fresh.Code), authorizeManager), http.StatusOK, "approve the fresh code")
	wantBrowserEnrollment(t, srv, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(fresh.RequestSecret), nil))

	// A poll that carries the header is a browser's too, whoever made the request.
	native := requestDevicePairingOverHTTP(t, srv, "Living Room TV")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(native.Code), authorizeManager), http.StatusOK, "approve")
	wantBrowserEnrollment(t, srv, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(native.RequestSecret), browserCall))
}

// Request, regenerate and poll share one rule for a browser: no credential
// flow over plain HTTP from off the LAN.
func TestDevicePairingBrowserCallsRefusePublicPlainHTTP(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	enabled := true
	if _, err := st.SetConsoleConfig(context.Background(), store.ConsoleConfigPatch{RemoteEnrollmentEnabled: &enabled}, time.Unix(0, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	publicBrowser := func(r *http.Request) { browserCall(r); publicPlainHTTP(r) }

	refused := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"Chrome on macOS"}`, publicBrowser)
	wantDevicePairingStatus(t, refused, http.StatusForbidden, "public plain-HTTP browser request")
	if list := decodeDevicePairing[pairing.PendingDeviceRequests](t, devicePairingCall(t, srv, http.MethodGet, "/api/pairing-requests", "", authorizeManager)); len(list.Requests) != 0 {
		t.Fatalf("refused browser request left an entry: %+v", list)
	}

	req := browserRequestDevicePairing(t, srv)
	// With the header, and without it for a request the server knows is a browser's.
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/regenerate", secretBody(req.RequestSecret), publicBrowser), http.StatusForbidden, "public plain-HTTP browser regenerate")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/regenerate", secretBody(req.RequestSecret), publicPlainHTTP), http.StatusForbidden, "public plain-HTTP regenerate of a browser request")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(req.Code), authorizeManager), http.StatusOK, "approve: the refused regenerates left the code alone")

	for _, authorize := range []func(*http.Request){publicBrowser, publicPlainHTTP} {
		public := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(req.RequestSecret), authorize)
		wantDevicePairingStatus(t, public, http.StatusForbidden, "public plain-HTTP poll of a browser request")
		if len(public.Result().Cookies()) != 0 || strings.Contains(public.Body.String(), "deviceSecret") {
			t.Fatalf("refused browser poll delivered something: %s", public.Body.String())
		}
	}
	// The approval was not spent, and it is still delivered only as cookies.
	wantBrowserEnrollment(t, srv, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(req.RequestSecret), nil))

	// A native request is not bound to the browser rule.
	rec := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"Phone"}`, publicPlainHTTP)
	wantDevicePairingStatus(t, rec, http.StatusCreated, "public native request with remote enrollment on")
	native := decodeDevicePairing[pairing.DeviceRequestResponse](t, rec)
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(native.Code), authorizeManager), http.StatusOK, "approve")
	poll := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(native.RequestSecret), publicPlainHTTP)
	wantDevicePairingStatus(t, poll, http.StatusOK, "native poll")
	if got := decodeDevicePairing[pairing.DevicePollResponse](t, poll); got.Enrollment == nil || got.Enrollment.DeviceSecret == "" || got.Enrollment.SessionID == "" {
		t.Fatalf("native poll = %+v", got)
	}
	if len(poll.Result().Cookies()) != 0 {
		t.Fatal("native poll set cookies")
	}
}

func TestDevicePairingRequestCancel(t *testing.T) {
	srv := newTestServer(t)
	const cancelPath = "/api/pairing-requests/cancel"
	req := requestDevicePairingOverHTTP(t, srv, "Living Room TV")
	cancel := devicePairingCall(t, srv, http.MethodPost, cancelPath, secretBody(req.RequestSecret), nil)
	wantDevicePairingStatus(t, cancel, http.StatusNoContent, "cancel")
	if list := decodeDevicePairing[pairing.PendingDeviceRequests](t, devicePairingCall(t, srv, http.MethodGet, "/api/pairing-requests", "", authorizeManager)); len(list.Requests) != 0 {
		t.Fatalf("cancelled request still pending: %+v", list)
	}
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(req.Code), authorizeManager), http.StatusNotFound, "approve a cancelled code")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(req.RequestSecret), nil), http.StatusUnauthorized, "poll a cancelled request")

	// The display code cancels nothing: only the holder of the secret can.
	other := requestDevicePairingOverHTTP(t, srv, "Kitchen Tablet")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, cancelPath, secretBody(other.Code), nil), http.StatusNoContent, "cancel with the display code")
	if list := decodeDevicePairing[pairing.PendingDeviceRequests](t, devicePairingCall(t, srv, http.MethodGet, "/api/pairing-requests", "", authorizeManager)); len(list.Requests) != 1 {
		t.Fatalf("the display code cancelled a request: %+v", list)
	}

	// An approval the device no longer wants is withdrawn before it enrolls anything.
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(other.Code), authorizeManager), http.StatusOK, "approve")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, cancelPath, secretBody(other.RequestSecret), nil), http.StatusNoContent, "cancel an approved request")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(other.RequestSecret), nil), http.StatusUnauthorized, "poll a cancelled approval")

	// Cancelling is idempotent and never a failed guess: repeats and unknown
	// secrets answer the same and spend no throttle attempts.
	for i := range 8 {
		wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, cancelPath, secretBody(req.RequestSecret), nil), http.StatusNoContent, fmt.Sprintf("repeat cancel %d", i))
	}
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, cancelPath, `{"requestSecret":""}`, nil), http.StatusBadRequest, "cancel without a secret")

	// A cancelled request frees its place under the per-source cap.
	held := make([]pairing.DeviceRequestResponse, 0, 3)
	for range 3 {
		held = append(held, requestDevicePairingOverHTTP(t, srv, "TV"))
	}
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"TV"}`, nil), http.StatusTooManyRequests, "request over the per-source cap")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, cancelPath, secretBody(held[0].RequestSecret), nil), http.StatusNoContent, "cancel one of the held requests")
	requestDevicePairingOverHTTP(t, srv, "TV")
}

// The lifetime is also given as a duration on the server's clock, so a device
// with a wrong clock does not expire the code early or late.
func TestDevicePairingRequestReportsServerRelativeExpiry(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	now := time.Unix(0, 0).UTC()
	srv.pairing = pairing.NewService(st, func() time.Time { return now })
	rec := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"Den TV"}`, nil)
	wantDevicePairingStatus(t, rec, http.StatusCreated, "request")
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created["expiresInSeconds"] != float64(600) {
		t.Fatalf("request expiresInSeconds = %v, want 600", created["expiresInSeconds"])
	}
	now = now.Add(90 * time.Second)
	poll := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(created["requestSecret"].(string)), nil)
	wantDevicePairingStatus(t, poll, http.StatusOK, "poll")
	var pending map[string]any
	if err := json.Unmarshal(poll.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	if pending["expiresInSeconds"] != float64(510) {
		t.Fatalf("poll expiresInSeconds = %v, want 510", pending["expiresInSeconds"])
	}
	regen := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/regenerate", secretBody(created["requestSecret"].(string)), nil)
	wantDevicePairingStatus(t, regen, http.StatusCreated, "regenerate")
	if got := decodeDevicePairing[pairing.DeviceRequestResponse](t, regen); got.ExpiresInSeconds != 600 {
		t.Fatalf("regenerate expiresInSeconds = %d, want 600", got.ExpiresInSeconds)
	}
}

// A device polling a secret the server no longer knows (after a restart, say)
// must not lock its address out of typing a pairing code.
func TestDevicePairingStalePollsDoNotThrottleCodeEnrollment(t *testing.T) {
	srv := newTestServer(t)
	for range 8 {
		devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody("stale-secret"), nil)
	}
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody("stale-secret"), nil), http.StatusTooManyRequests, "stale polls on their own lane")
	enroll := devicePairingCall(t, srv, http.MethodPost, "/api/enroll", `{"code":"N0SVCHCD","deviceName":"TV"}`, nil)
	wantDevicePairingStatus(t, enroll, http.StatusUnauthorized, "code enrollment after stale pairing polls")
}

type fixedSPKIPin string

func (p fixedSPKIPin) ServerSPKIPin(context.Context) (string, error) { return string(p), nil }

// A request that carries forwarding evidence but did not come through a
// declared proxy is not a LAN request, however private the address it arrived
// from: the admin sees lan false and the enrollment carries no server key pin.
func TestDevicePairingForwardedRequestIsNotLAN(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	srv.pairing.SetSPKIProvider(fixedSPKIPin("sha256-test-pin"))
	forwarded := func(r *http.Request) {
		r.RemoteAddr = "172.20.0.3:4000"
		r.Header.Set("X-Forwarded-For", "198.51.100.9")
	}
	collect := func(req pairing.DeviceRequestResponse, authorize func(*http.Request)) pairing.EnrollResponse {
		t.Helper()
		wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(req.Code), authorizeManager), http.StatusOK, "approve")
		poll := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(req.RequestSecret), authorize)
		wantDevicePairingStatus(t, poll, http.StatusOK, "collect")
		got := decodeDevicePairing[pairing.DevicePollResponse](t, poll)
		if got.Enrollment == nil {
			t.Fatalf("collect = %+v", got)
		}
		return *got.Enrollment
	}

	// With remote enrollment off, the forwarded request is refused like any off-LAN one.
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"TV"}`, forwarded), http.StatusUnauthorized, "forwarded request with remote enrollment off")

	// A direct LAN request and poll are pinned.
	if got := collect(requestDevicePairingOverHTTP(t, srv, "Direct TV"), nil); got.ServerKeyPin != "sha256-test-pin" {
		t.Fatalf("direct LAN enrollment pin = %q", got.ServerKeyPin)
	}
	// A LAN request collected through an undeclared proxy is refused by the remote-enrollment gate.
	lan := requestDevicePairingOverHTTP(t, srv, "Direct TV")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(lan.Code), authorizeManager), http.StatusOK, "approve")
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(lan.RequestSecret), forwarded), http.StatusUnauthorized, "forwarded collection with remote enrollment off")

	enabled := true
	if _, err := st.SetConsoleConfig(context.Background(), store.ConsoleConfigPatch{RemoteEnrollmentEnabled: &enabled}, time.Unix(0, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	rec := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"Proxied TV"}`, forwarded)
	wantDevicePairingStatus(t, rec, http.StatusCreated, "forwarded request with remote enrollment on")
	proxied := decodeDevicePairing[pairing.DeviceRequestResponse](t, rec)
	list := decodeDevicePairing[pairing.PendingDeviceRequests](t, devicePairingCall(t, srv, http.MethodGet, "/api/pairing-requests", "", authorizeManager))
	if len(list.Requests) != 1 || list.Requests[0].Code != proxied.Code || list.Requests[0].LAN {
		t.Fatalf("pending = %+v, want the forwarded request with lan false", list)
	}
	if got := collect(proxied, nil); got.ServerKeyPin != "" {
		t.Fatalf("forwarded request collected on the LAN got pin %q", got.ServerKeyPin)
	}
	// And the other leg: a LAN request collected through the proxy gets no pin either.
	if got := collect(requestDevicePairingOverHTTP(t, srv, "Direct TV"), forwarded); got.ServerKeyPin != "" {
		t.Fatalf("LAN request collected through a proxy got pin %q", got.ServerKeyPin)
	}

	// Through a declared proxy the forwarded client address decides.
	proxies, err := ParseKnownProxies("172.20.0.0/16", false)
	if err != nil {
		t.Fatal(err)
	}
	srv.knownProxies = proxies
	rec = devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"Public via proxy"}`, forwarded)
	wantDevicePairingStatus(t, rec, http.StatusCreated, "public client through a declared proxy")
	if got := collect(decodeDevicePairing[pairing.DeviceRequestResponse](t, rec), nil); got.ServerKeyPin != "" {
		t.Fatalf("public client through a declared proxy got pin %q", got.ServerKeyPin)
	}
	lanViaProxy := func(r *http.Request) {
		r.RemoteAddr = "172.20.0.3:4000"
		r.Header.Set("X-Forwarded-For", "192.168.1.44")
	}
	rec = devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests", `{"deviceName":"LAN via proxy"}`, lanViaProxy)
	wantDevicePairingStatus(t, rec, http.StatusCreated, "LAN client through a declared proxy")
	if got := collect(decodeDevicePairing[pairing.DeviceRequestResponse](t, rec), lanViaProxy); got.ServerKeyPin != "sha256-test-pin" {
		t.Fatalf("LAN client through a declared proxy pin = %q", got.ServerKeyPin)
	}
}

// A cookie-authenticated approval needs the CSRF header: a cross-site form
// post riding the admin's cookies must not approve a device.
func TestDevicePairingCookieApproveNeedsTheCSRFHeader(t *testing.T) {
	srv := newTestServer(t)
	req := requestDevicePairingOverHTTP(t, srv, "Living Room TV")
	cookiesOnly := func(r *http.Request) {
		authorizeManagerCookies(r)
		r.Header.Del(auth.CSRFHeader)
	}
	for _, path := range []string{"/api/pairing-requests/approve", "/api/pairing-requests/deny"} {
		wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, path, codeBody(req.Code), cookiesOnly), http.StatusForbidden, path+" with cookies and no CSRF header")
	}
	poll := devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/poll", secretBody(req.RequestSecret), nil)
	wantDevicePairingStatus(t, poll, http.StatusOK, "poll")
	if got := decodeDevicePairing[pairing.DevicePollResponse](t, poll); got.Status != pairing.DeviceRequestPending {
		t.Fatalf("request after refused cookie decisions = %+v, want pending", got)
	}
	wantDevicePairingStatus(t, devicePairingCall(t, srv, http.MethodPost, "/api/pairing-requests/approve", codeBody(req.Code), authorizeManagerCookies), http.StatusOK, "cookie approve with the CSRF header")
}

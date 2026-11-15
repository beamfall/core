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

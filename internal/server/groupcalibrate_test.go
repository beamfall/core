package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGroupCalibrateLANOnly is the AV-4 LAN-only acceptance (d):
// the four-timestamp calibration endpoint serves a LAN caller and
// REFUSES a non-LAN (relay/remote) caller, so the Group Conductor is never
// reachable over the remote path. A LAN request returns the server-recorded
// t2/t3 (with t2 <= t3, the receive-then-transmit bracket) plus the echoed t1; a
// non-LAN source IP is rejected 403 before any timestamp work. requestIsLAN reads
// the real connection address (RemoteAddr), so the gate cannot be spoofed by a
// header.
//
// feature:ambient-group-conductor
func TestGroupCalibrateLANOnly(t *testing.T) {
	srv := newTestServer(t)

	body := func() *bytes.Reader {
		b, _ := json.Marshal(map[string]any{"t1MS": int64(1000)})
		return bytes.NewReader(b)
	}

	// LAN caller (loopback source) is served.
	lan := newJSONRequest(http.MethodPost, "/api/group/calibrate", body())
	lan.RemoteAddr = "127.0.0.1:5000"
	authorizeViewer(lan)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, lan)
	if rec.Code != http.StatusOK {
		t.Fatalf("LAN calibrate status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var resp groupCalibrateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode calibrate response: %v", err)
	}
	if resp.T1MS != 1000 {
		t.Errorf("echoed t1 = %d, want 1000", resp.T1MS)
	}
	if resp.T2MS > resp.T3MS {
		t.Errorf("t2 (%d) > t3 (%d): receive must not be after transmit", resp.T2MS, resp.T3MS)
	}

	// IPv6 link-local connection addresses carry an interface zone. They are
	// still LAN sources and must pass the same boundary gate.
	linkLocal := newJSONRequest(http.MethodPost, "/api/group/calibrate", body())
	linkLocal.RemoteAddr = "[fe80::1%en0]:5000"
	authorizeViewer(linkLocal)
	linkLocalRec := httptest.NewRecorder()
	srv.ServeHTTP(linkLocalRec, linkLocal)
	if linkLocalRec.Code != http.StatusOK {
		t.Fatalf("IPv6 link-local calibrate status = %d, want 200 (body %s)", linkLocalRec.Code, linkLocalRec.Body.String())
	}

	// Non-LAN caller (public TEST-NET source, httptest default) is refused 403 —
	// the conductor is never exposed over the relay/remote path.
	remote := newJSONRequest(http.MethodPost, "/api/group/calibrate", body())
	remote.RemoteAddr = "203.0.113.7:5000" // TEST-NET-3, a public address
	authorizeViewer(remote)
	remoteRec := httptest.NewRecorder()
	srv.ServeHTTP(remoteRec, remote)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("non-LAN calibrate status = %d, want 403", remoteRec.Code)
	}

	relayed := newJSONRequest(http.MethodPost, "/api/group/calibrate", body())
	relayed.RemoteAddr = "127.0.0.1:5000"
	relayed = relayed.WithContext(ContextWithRelayedRequest(relayed.Context()))
	authorizeViewer(relayed)
	relayedRec := httptest.NewRecorder()
	srv.ServeHTTP(relayedRec, relayed)
	if relayedRec.Code != http.StatusForbidden {
		t.Fatalf("relayed loopback calibrate status = %d, want 403", relayedRec.Code)
	}
}

// TestGroupCalibrateRequiresSession confirms the calibration endpoint is
// session-authenticated (ADR-0035 §8: a calibrating device must resolve a
// Profile-bound scope — the Group Conductor is strictly same-Profile). An
// unauthenticated LAN request is rejected 401.
//
// feature:ambient-group-conductor
func TestGroupCalibrateRequiresSession(t *testing.T) {
	srv := newTestServer(t)
	b, _ := json.Marshal(map[string]any{"t1MS": int64(0)})
	req := newJSONRequest(http.MethodPost, "/api/group/calibrate", bytes.NewReader(b))
	req.RemoteAddr = "127.0.0.1:5000" // LAN, so the 401 is the AUTH gate, not the LAN gate
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated calibrate status = %d, want 401", rec.Code)
	}
}

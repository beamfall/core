package server

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/auth"
)

// scrapeLine returns the /metrics line that starts with name, or "".
func scrapeLine(t *testing.T, srv *Server, name string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authorizedScrape(t, srv))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d: %s", rec.Code, rec.Body.String())
	}
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(line, name+" ") {
			return line
		}
	}
	return ""
}

func TestWrongDeviceSecretProducesAuthRefusedRecord(t *testing.T) {
	srv := newTestServer(t)
	const secretCanary = "canary-device-secret-3e1b" //nolint:gosec // a leak canary, not a credential
	logs := captureJSONLogs(t)
	// The audit record must bypass the level filter: raise the default
	// logger's level above Warn and still expect the record.
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelError + 4})))

	req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	req.Header.Set("X-Beamfall-Session", "session-default")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", secretCanary)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}

	records := logRecords(t, logs, "auth refused")
	if len(records) != 1 {
		t.Fatalf("auth refused records = %d, want 1: %s", len(records), logs.String())
	}
	want := map[string]any{
		"operation":  "credential",
		"outcome":    "refused",
		"reason":     "device_secret_invalid",
		"credential": "session",
		"method":     http.MethodGet,
		"route":      "GET /api/manifest",
		"status":     float64(http.StatusUnauthorized),
		"requestID":  rec.Header().Get(requestIDHeader),
	}
	for key, value := range want {
		if records[0][key] != value {
			t.Fatalf("auth refused %s = %v, want %v: %v", key, records[0][key], value, records[0])
		}
	}
	for _, leaked := range []string{secretCanary, "session-default", "device-tv"} {
		if strings.Contains(logs.String(), leaked) {
			t.Fatalf("logs leaked %q: %s", leaked, logs.String())
		}
	}

	line := scrapeLine(t, srv, `beamfall_auth_refusals_total{reason="device_secret_invalid"}`)
	if line != `beamfall_auth_refusals_total{reason="device_secret_invalid"} 1` {
		t.Fatalf("refusal counter line = %q, want a count of 1", line)
	}
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, authorizedScrape(t, srv))
	if strings.Contains(rec.Body.String(), secretCanary) {
		t.Fatalf("/metrics leaked the device secret:\n%s", rec.Body.String())
	}
}

func TestWrongPairingCodeProducesAuthRefusedRecord(t *testing.T) {
	srv := newTestServer(t)
	const codeCanary = "CANARY-CODE-8D2F"
	logs := captureJSONLogs(t)

	req := newJSONRequest(http.MethodPost, "/api/enroll", bytes.NewReader([]byte(`{"code":"`+codeCanary+`","deviceName":"TV"}`)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}

	records := logRecords(t, logs, "auth refused")
	if len(records) != 1 || records[0]["operation"] != "enroll" || records[0]["reason"] != "pairing_code_invalid" || records[0]["credential"] != "anonymous" {
		t.Fatalf("auth refused records = %v, want one enroll/pairing_code_invalid", records)
	}
	if strings.Contains(logs.String(), codeCanary) {
		t.Fatalf("logs leaked the pairing code: %s", logs.String())
	}
	if line := scrapeLine(t, srv, `beamfall_auth_refusals_total{reason="pairing_code_invalid"}`); !strings.HasSuffix(line, " 1") {
		t.Fatalf("refusal counter line = %q, want a count of 1", line)
	}
}

func TestAnonymousRequestProducesNoAuthRefusedRecord(t *testing.T) {
	srv := newTestServer(t)
	logs := captureJSONLogs(t)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/manifest", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if records := logRecords(t, logs, "auth refused"); len(records) != 0 {
		t.Fatalf("anonymous request produced auth refused records: %v", records)
	}
}

func TestScopeRefusalIsRecordedOnlyWhenTheResponseRefuses(t *testing.T) {
	srv := newTestServer(t)
	logs := captureJSONLogs(t)
	newRequest := func(decisive bool) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req = req.WithContext(auth.WithRefusalNote(context.Background()))
		auth.NoteRefusal(req.Context(), "credential", auth.RefusalSessionExpired, decisive)
		return req
	}

	srv.recordAuthRefusal(newRequest(false), "req-1", http.StatusOK)
	if records := logRecords(t, logs, "auth refused"); len(records) != 0 {
		t.Fatalf("tolerated scope refusal produced records: %v", records)
	}
	srv.recordAuthRefusal(newRequest(false), "req-2", http.StatusForbidden)
	srv.recordAuthRefusal(newRequest(true), "req-3", http.StatusOK)
	records := logRecords(t, logs, "auth refused")
	if len(records) != 2 || records[0]["requestID"] != "req-2" || records[1]["requestID"] != "req-3" {
		t.Fatalf("records = %v, want the 403 and the decisive refusal", records)
	}

	// A reason outside the closed vocabulary is never counted or logged.
	logs.Reset()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req = req.WithContext(auth.WithRefusalNote(context.Background()))
	auth.NoteRefusal(req.Context(), "credential", auth.RefusalReason("free-form text"), true)
	srv.recordAuthRefusal(req, "req-4", http.StatusUnauthorized)
	if records := logRecords(t, logs, "auth refused"); len(records) != 0 {
		t.Fatalf("unknown reason produced records: %v", records)
	}
}

func TestAuthRefusalMetricsReportEveryReason(t *testing.T) {
	var metrics authRefusalMetrics
	var body strings.Builder
	metrics.writePrometheus(&body)
	lines := strings.Split(strings.TrimSpace(body.String()), "\n")
	if len(lines) != len(auth.RefusalReasons) {
		t.Fatalf("refusal lines = %d, want %d:\n%s", len(lines), len(auth.RefusalReasons), body.String())
	}
	for i, reason := range auth.RefusalReasons {
		if want := `beamfall_auth_refusals_total{reason="` + string(reason) + `"} 0`; lines[i] != want {
			t.Fatalf("line %d = %q, want %q", i, lines[i], want)
		}
	}
}

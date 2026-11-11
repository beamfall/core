package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// captureJSONLogs routes the default logger into a JSON buffer for the test.
func captureJSONLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &logs
}

// logRecords decodes every JSON log record whose msg equals message.
func logRecords(t *testing.T, logs *bytes.Buffer, message string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if record["msg"] == message {
			records = append(records, record)
		}
	}
	return records
}

// withoutAccessLines drops the per-request access lines from text-handler log
// output, for tests that pin the other records a request produces.
func withoutAccessLines(logs string) string {
	var kept []string
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, `msg="http request"`) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func TestAccessLineCarriesRequestIDRouteStatusLatencyAndPrincipal(t *testing.T) {
	srv := newTestServer(t)
	logs := captureJSONLogs(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	records := logRecords(t, logs, "http request")
	if len(records) != 1 {
		t.Fatalf("access records = %d, want 1: %s", len(records), logs.String())
	}
	record := records[0]
	want := map[string]any{
		"requestID": rec.Header().Get(requestIDHeader),
		"method":    http.MethodGet,
		"route":     "GET /health",
		"path":      "/health",
		"status":    float64(rec.Code),
		"principal": "anonymous",
	}
	for key, value := range want {
		if record[key] != value {
			t.Fatalf("access %s = %v, want %v: %v", key, record[key], value, record)
		}
	}
	if record["requestID"] == "" {
		t.Fatal("access record carries no request ID")
	}
	if _, ok := record["latency_ms"].(float64); !ok {
		t.Fatalf("access record latency_ms = %v, want a number", record["latency_ms"])
	}
}

func TestAccessLineNeverCarriesCredentialValues(t *testing.T) {
	srv := newTestServer(t)
	logs := captureJSONLogs(t)
	const (
		tokenCanary = "canary-stream-token-7f3a"
		queryCanary = "canary-query-secret-91c2"
		keyCanary   = "bfk_canary-scoped-key-55d0"
	)

	cases := []struct {
		name          string
		req           *http.Request
		wantPath      string
		wantPrincipal string
	}{
		{
			name:          "stream token route",
			req:           httptest.NewRequest(http.MethodGet, "/api/media/direct/"+tokenCanary+"/file.mp4?api_key="+queryCanary, nil),
			wantPath:      "/api/media/direct/[redacted]/file.mp4",
			wantPrincipal: principalStreamToken,
		},
		{
			name: "scoped key",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/v1/libraries?token="+queryCanary, nil)
				req.Header.Set("Authorization", "Bearer "+keyCanary)
				return req
			}(),
			wantPath:      "/v1/libraries",
			wantPrincipal: "scoped_key",
		},
		{
			name: "session headers",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
				authorizeViewer(req)
				return req
			}(),
			wantPath:      "/api/manifest",
			wantPrincipal: "session",
		},
	}
	for _, tc := range cases {
		logs.Reset()
		srv.ServeHTTP(httptest.NewRecorder(), tc.req)
		records := logRecords(t, logs, "http request")
		if len(records) != 1 {
			t.Fatalf("%s: access records = %d, want 1: %s", tc.name, len(records), logs.String())
		}
		if records[0]["path"] != tc.wantPath || records[0]["principal"] != tc.wantPrincipal {
			t.Fatalf("%s: access path/principal = %v/%v, want %s/%s", tc.name, records[0]["path"], records[0]["principal"], tc.wantPath, tc.wantPrincipal)
		}
		for _, secret := range []string{tokenCanary, queryCanary, keyCanary, "dev-tv-secret", "session-default"} {
			if strings.Contains(logs.String(), secret) {
				t.Fatalf("%s: logs leaked %q: %s", tc.name, secret, logs.String())
			}
		}
	}
}

func TestRequestRefusedBeforeRoutingCarriesRequestID(t *testing.T) {
	srv := newTestServer(t)
	logs := captureJSONLogs(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/no-such-route", nil)
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	reqID := rec.Header().Get(requestIDHeader)
	if reqID == "" {
		t.Fatal("pre-routing refusal carries no X-Beamfall-Request-Id")
	}
	records := logRecords(t, logs, "http request")
	if len(records) != 1 || records[0]["requestID"] != reqID || records[0]["route"] != "unmatched" || records[0]["status"] != float64(http.StatusNotFound) {
		t.Fatalf("access records = %v, want one unmatched 404 with request ID %s", records, reqID)
	}
}

func TestMetricsCountRequestsByStatusClass(t *testing.T) {
	srv := newTestServer(t)

	for _, path := range []string{"/health", "/api/no-such-route"} {
		srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authorizedScrape(t, srv))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d: %s", rec.Code, rec.Body.String())
	}

	values := map[string]float64{}
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if !strings.HasPrefix(line, "beamfall_http_") {
			continue
		}
		name, value, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("malformed metric line %q", line)
		}
		if strings.Contains(name, "path") || strings.Contains(name, "route") || strings.Contains(name, "principal") {
			t.Fatalf("request metric carries an unbounded label: %q", line)
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			t.Fatalf("metric %q value: %v", line, err)
		}
		values[name] = parsed
	}
	for _, class := range statusClasses {
		if _, ok := values[`beamfall_http_requests_total{status_class="`+class+`"}`]; !ok {
			t.Fatalf("scrape missing status class %s:\n%s", class, rec.Body.String())
		}
	}
	if values[`beamfall_http_requests_total{status_class="2xx"}`] < 1 || values[`beamfall_http_requests_total{status_class="4xx"}`] < 1 {
		t.Fatalf("2xx/4xx not counted:\n%s", rec.Body.String())
	}
	if values["beamfall_http_request_duration_seconds_count"] < 2 {
		t.Fatalf("duration count = %v, want >= 2", values["beamfall_http_request_duration_seconds_count"])
	}
	if _, ok := values["beamfall_http_request_duration_seconds_sum"]; !ok {
		t.Fatalf("scrape missing duration sum:\n%s", rec.Body.String())
	}
}

func TestTrackedResponseWriterReportsFinalStatus(t *testing.T) {
	cases := []struct {
		name  string
		write func(http.ResponseWriter)
		want  int
	}{
		{"no write", func(http.ResponseWriter) {}, http.StatusOK},
		{"body only", func(w http.ResponseWriter) { _, _ = w.Write([]byte("x")) }, http.StatusOK},
		{"informational then final", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusEarlyHints)
			w.WriteHeader(http.StatusNotFound)
		}, http.StatusNotFound},
		{"superfluous second status", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusTeapot)
			w.WriteHeader(http.StatusOK)
		}, http.StatusTeapot},
	}
	for _, tc := range cases {
		tracked, wrapped := trackResponseWriter(httptest.NewRecorder())
		tc.write(wrapped)
		if got := tracked.statusCode(); got != tc.want {
			t.Fatalf("%s: status = %d, want %d", tc.name, got, tc.want)
		}
	}
}

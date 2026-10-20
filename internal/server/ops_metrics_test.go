package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/beam"
	"github.com/beamfall/core/internal/governor"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/observability"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// newTestServerWithMetrics builds a server with the ops observability sources
// wired: the store backs readiness + active sessions + library
// size, the governor backs active transcodes. It hands back the store and
// governor so a test can create live tokens and start transcodes.
func newTestServerWithMetrics(t *testing.T) (*Server, *store.Store, *governor.Governor) {
	t.Helper()
	st := storetest.Open(t)
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	authService := auth.NewServiceWithClock(st, clock)
	manifests := manifest.NewService(st, clock, nil)
	tokens := streamtoken.NewService([]byte("test-token-key"), clock)
	gov := governor.New()
	srv := New(
		manifests,
		playback.NewService(st, tokens, clock),
		beam.NewService(st, authService, clock),
		pairing.NewService(st, clock),
		testServerGraphHandler{Handler: graph.NewHandler(manifests, authService, st, nil)},
		mediahttp.NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil)),
		authService,
		st,
	)
	srv.SetEvents(st)
	srv.SetMetricsSources(st, st, gov, st)
	srv.SetJobQueueSource(st)
	return srv, st, gov
}

// scrapeMetrics parses a Prometheus text-exposition body into name→value,
// keying labelled series by their full "name{labels}" head. It rejects a line
// whose value is not a number, so a malformed scrape fails the test rather than
// silently passing.
func scrapeMetrics(t *testing.T, body string) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sep := strings.LastIndex(line, " ")
		if sep < 0 {
			t.Fatalf("metrics line %q has no value separator", line)
		}
		name := line[:sep]
		value, err := strconv.ParseFloat(strings.TrimSpace(line[sep+1:]), 64)
		if err != nil {
			t.Fatalf("metrics line %q value not numeric: %v", line, err)
		}
		out[name] = value
	}
	return out
}

// TestMetricsExposesOpsGauges asserts a /metrics scrape parses and carries the
// three ADR-0013 §6 ops gauges. With two live sessions (one of them
// transcoding), one started transcode, and the two-Media development seed, the
// gauges read the expected values.
func TestMetricsExposesOpsGauges(t *testing.T) {
	srv, st, gov := newTestServerWithMetrics(t)
	ctx := context.Background()
	future := time.Now().UTC().Add(time.Hour)

	// Two distinct live playback sessions.
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false, future); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordStreamToken(ctx, "session-manager", "profile-default", "device-mac", "media-hls", "media-alpha", false, future); err != nil {
		t.Fatal(err)
	}
	// One active realtime transcode in the governor.
	gov.TranscodeStarted()

	req := authorizedScrape(t, srv)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	scrape := scrapeMetrics(t, rec.Body.String())
	for _, name := range []string{"beamfall_sessions_active", "beamfall_transcode_active", "beamfall_library_media_total"} {
		if _, ok := scrape[name]; !ok {
			t.Fatalf("scrape missing gauge %q; body:\n%s", name, rec.Body.String())
		}
	}
	if scrape["beamfall_sessions_active"] != 2 {
		t.Fatalf("beamfall_sessions_active = %v, want 2", scrape["beamfall_sessions_active"])
	}
	if scrape["beamfall_transcode_active"] != 1 {
		t.Fatalf("beamfall_transcode_active = %v, want 1", scrape["beamfall_transcode_active"])
	}
	if scrape["beamfall_library_media_total"] != 2 {
		t.Fatalf("beamfall_library_media_total = %v, want 2 (seed)", scrape["beamfall_library_media_total"])
	}
}

// TestMetricsExposesJobQueueSLIs asserts the ADR-0146 §8 job-queue SLIs: a
// waiting job makes the queue-depth gauge non-zero and the oldest-age gauge
// report a positive age derived from the job's server-clock enqueue time.
func TestMetricsExposesJobQueueSLIs(t *testing.T) {
	srv, st, _ := newTestServerWithMetrics(t)
	ctx := context.Background()

	if _, err := st.EnqueueJob(ctx, store.Job{
		ID:        "job-queue-sli",
		Kind:      "scan",
		Class:     store.JobClassBackground,
		DedupeKey: "queue-sli",
		CreatedAt: time.Now().UTC().Add(-90 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	req := authorizedScrape(t, srv)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	scrape := scrapeMetrics(t, rec.Body.String())
	if scrape["beamfall_jobs_queue_depth"] < 1 {
		t.Fatalf("beamfall_jobs_queue_depth = %v, want >= 1; body:\n%s", scrape["beamfall_jobs_queue_depth"], rec.Body.String())
	}
	age, ok := scrape["beamfall_jobs_queue_oldest_age_ms"]
	if !ok {
		t.Fatalf("scrape missing beamfall_jobs_queue_oldest_age_ms; body:\n%s", rec.Body.String())
	}
	if age <= 0 {
		t.Fatalf("beamfall_jobs_queue_oldest_age_ms = %v, want > 0 (job enqueued 90s ago)", age)
	}
}

func TestMetricsKeepsOTelLayerDefaultOff(t *testing.T) {
	srv, _, _ := newTestServerWithMetrics(t)
	srv.SetObservability(observability.New(2))

	req := authorizedScrape(t, srv)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if strings.Contains(rec.Body.String(), "beamfall_observability_") {
		t.Fatalf("disabled OTel metrics leaked into scrape:\n%s", rec.Body.String())
	}
}

func TestMetricsAppendsEnabledOTelScrape(t *testing.T) {
	srv, _, _ := newTestServerWithMetrics(t)
	controller := observability.New(4)
	if err := controller.SetEnabled(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	srv.SetObservability(controller)

	req := authorizedScrape(t, srv)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "beamfall_sessions_active") {
		t.Fatalf("coarse ADR-0013 metrics missing:\n%s", body)
	}
	if !strings.Contains(body, "beamfall_observability_sessions_active") {
		t.Fatalf("enabled OTel metrics missing:\n%s", body)
	}
	if got := controller.Snapshot(); len(got) == 0 {
		t.Fatal("enabled scrape recorded no ring samples")
	}
}

// decodeHealth unmarshals a /health body into a generic map for field asserts.
func decodeHealth(t *testing.T, body string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode /health body %q: %v", body, err)
	}
	return doc
}

// readinessProbe lets a /health test drive the ready/not-ready split without
// closing the real store.
type readinessProbe struct{ err error }

func (p readinessProbe) Ready(context.Context) error { return p.err }

type panickingObservability struct{}

func (panickingObservability) RecordGauge(context.Context, string, int64, map[string]string) error {
	return nil
}

func (panickingObservability) PrometheusText(*http.Request) string {
	panic("metrics source failed")
}

func TestMetricsPanicBeforeResponseCommitReturnsJSONError(t *testing.T) {
	srv := newTestServer(t)
	srv.SetObservability(panickingObservability{})
	req := authorizedScrape(t, srv)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"internal server error"}` {
		t.Fatalf("body = %q, want JSON internal error", got)
	}
}

// TestHealthDistinguishesReadyFromLive asserts /health splits liveness from
// readiness: a reachable store is live and ready (200); a store
// reporting not-ready is still live but not ready (503).
func TestHealthDistinguishesReadyFromLive(t *testing.T) {
	srv, _, _ := newTestServerWithMetrics(t)
	srv.SetBuildMetadata("sha-test", "acceptance", store.AcceptanceSeedVersion)
	srv.SetCapabilities([]string{"conversion", "rename"})

	// Ready store: live + ready, 200.
	readyReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	readyRec := httptest.NewRecorder()
	srv.ServeHTTP(readyRec, readyReq)
	if readyRec.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d: %s", readyRec.Code, http.StatusOK, readyRec.Body.String())
	}
	ready := decodeHealth(t, readyRec.Body.String())
	if ready["live"] != true {
		t.Fatalf("ready body live = %v, want true", ready["live"])
	}
	if ready["ready"] != true {
		t.Fatalf("ready body ready = %v, want true", ready["ready"])
	}
	if ready["buildSHA"] != "sha-test" {
		t.Fatalf("ready body buildSHA = %v, want sha-test", ready["buildSHA"])
	}
	if ready["seedProfile"] != "acceptance" {
		t.Fatalf("ready body seedProfile = %v, want acceptance", ready["seedProfile"])
	}
	if ready["seedVersion"] != store.AcceptanceSeedVersion {
		t.Fatalf("ready body seedVersion = %v, want %s", ready["seedVersion"], store.AcceptanceSeedVersion)
	}
	if got := fmt.Sprint(ready["capabilities"]); !strings.Contains(got, "conversion") || !strings.Contains(got, "rename") {
		t.Fatalf("ready body capabilities = %v, want conversion and rename", ready["capabilities"])
	}

	// Not-ready store: still live, but not ready, 503.
	srv.SetMetricsSources(readinessProbe{err: context.DeadlineExceeded}, nil, nil, nil)
	notReadyReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	notReadyRec := httptest.NewRecorder()
	srv.ServeHTTP(notReadyRec, notReadyReq)
	if notReadyRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d, want %d", notReadyRec.Code, http.StatusServiceUnavailable)
	}
	notReady := decodeHealth(t, notReadyRec.Body.String())
	if notReady["live"] != true {
		t.Fatalf("not-ready body live = %v, want true (process is up)", notReady["live"])
	}
	if notReady["ready"] != false {
		t.Fatalf("not-ready body ready = %v, want false", notReady["ready"])
	}
}

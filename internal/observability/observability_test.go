package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestControllerDefaultOffAndRuntimeToggle(t *testing.T) {
	controller := New(2)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	if controller.Enabled() {
		t.Fatal("default Enabled = true, want false")
	}
	if text := controller.PrometheusText(req); text != "" {
		t.Fatalf("disabled PrometheusText = %q, want empty", text)
	}
	if err := controller.RecordGauge(t.Context(), "beamfall_observability_sessions_active", 3, nil); err != nil {
		t.Fatal(err)
	}
	if got := controller.Snapshot(); len(got) != 0 {
		t.Fatalf("disabled ring len = %d, want 0", len(got))
	}

	if err := controller.SetEnabled(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if err := controller.RecordGauge(t.Context(), "beamfall_observability_sessions_active", 3, nil); err != nil {
		t.Fatal(err)
	}
	text := controller.PrometheusText(req)
	if !strings.Contains(text, "beamfall_observability_sessions_active") {
		t.Fatalf("enabled scrape missing OTel gauge:\n%s", text)
	}
	if got := controller.Snapshot(); len(got) != 1 {
		t.Fatalf("enabled ring len = %d, want 1", len(got))
	}

	if err := controller.SetEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if controller.Enabled() {
		t.Fatal("after disable Enabled = true, want false")
	}
	if got := controller.Snapshot(); len(got) != 0 {
		t.Fatalf("disabled ring len = %d, want purged", len(got))
	}
}

func TestRingBufferDropsOldest(t *testing.T) {
	controller := New(2)
	if err := controller.SetEnabled(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 3; i++ {
		if err := controller.RecordGauge(t.Context(), "beamfall_observability_jobs", i, map[string]string{"class": "background"}); err != nil {
			t.Fatal(err)
		}
	}
	got := controller.Snapshot()
	if len(got) != 2 {
		t.Fatalf("ring len = %d, want 2", len(got))
	}
	if got[0].Value != 2 || got[1].Value != 3 {
		t.Fatalf("ring values = [%d %d], want [2 3]", got[0].Value, got[1].Value)
	}
}

// TestDecision9Denylist is the runtime backstop for ADR-0146 Decision 9: it
// proves RecordGauge rejects sensitive label keys/values, which covers the
// dynamic label values a static scan cannot see. The ADR-required machine
// enforcement over instrumentation call sites is the static AST scan in
// internal/conformance (TestObservabilityInstrumentationHonorsDecision9Denylist),
// which shares this same denylist via observability.ForbiddenLabelKey/Value.
func TestDecision9Denylist(t *testing.T) {
	controller := New(2)
	for _, labels := range []map[string]string{
		{"media_title": "anything"},
		{"file_path": "anything"},
		{"library_id": "lib"},
		{"profileID": "profile"},
		{"account.id": "account"},
		{"client_ip": "127.0.0.1"},
		{"status": "Bearer secret"},
		{"status": "/api/media/direct/token"},
	} {
		err := controller.RecordGauge(t.Context(), "beamfall_observability_jobs", 1, labels)
		if !errors.Is(err, ErrSensitiveAttribute) {
			t.Fatalf("RecordGauge(%v) error = %v, want ErrSensitiveAttribute", labels, err)
		}
	}
	if err := controller.RecordGauge(t.Context(), "beamfall_observability_jobs", 1, map[string]string{"class": "background", "status": "queued"}); err != nil {
		t.Fatalf("safe labels rejected: %v", err)
	}
}

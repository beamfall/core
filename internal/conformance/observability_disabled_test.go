package conformance

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/beamfall/core/internal/observability"
)

// TestObservabilityDisabledBindsNothing is ADR-0146 Consequences (a): with the
// opt-in OTel metrics layer disabled, no exporter goroutine runs and no
// additional endpoint is bound. The core layer never opens its own socket — it
// exposes metrics through the pre-existing ADR-0013 §6 /metrics handler — so
// "no additional endpoint" is proven by the controller surfacing no scrape body
// while disabled, and "no exporter goroutine" by goroutines settling back to
// baseline after a toggle cycle (Decision 3: inert by default, purged on
// disable; Decision 10: never a lingering cost).
func TestObservabilityDisabledBindsNothing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	controller := observability.New(observability.DefaultRingSize)

	// Default-off: inert from construction — nothing enabled, nothing bound.
	if controller.Enabled() {
		t.Fatal("freshly constructed controller Enabled = true, want false (default-off)")
	}
	if body := controller.PrometheusText(req); body != "" {
		t.Fatalf("disabled controller bound a scrape endpoint: %q, want empty", body)
	}

	baseline := goroutineFloor()

	// One full enable → record → disable cycle. Enabling stands up the exporter
	// and its Prometheus handler; disabling must tear both down.
	if err := controller.SetEnabled(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if err := controller.RecordGauge(t.Context(), "beamfall_observability_jobs", 1, map[string]string{"class": "background"}); err != nil {
		t.Fatal(err)
	}
	if body := controller.PrometheusText(req); body == "" {
		t.Fatal("enabled controller served no scrape body, want the OTel exposition")
	}

	if err := controller.SetEnabled(t.Context(), false); err != nil {
		t.Fatal(err)
	}

	// (endpoint) No additional endpoint is bound while disabled: the handler is
	// released, so the controller contributes nothing to a scrape.
	if body := controller.PrometheusText(req); body != "" {
		t.Fatalf("controller still served a scrape body after disable: %q, want empty", body)
	}
	// (ring) The local ring buffer is purged on disable (Decision 4).
	if got := controller.Snapshot(); len(got) != 0 {
		t.Fatalf("ring not purged on disable: len = %d, want 0", len(got))
	}
	// (goroutine) No exporter goroutine lingers: the count settles back to
	// baseline once Shutdown has reaped anything the exporter started.
	if settled := settleGoroutines(baseline); settled > baseline {
		t.Fatalf("goroutines did not settle after disable: got %d, baseline %d (exporter goroutine leaked)", settled, baseline)
	}
}

// goroutineFloor returns a stable goroutine count after letting the scheduler
// quiesce, so the baseline is not inflated by in-flight runtime goroutines.
func goroutineFloor() int {
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	return runtime.NumGoroutine()
}

// settleGoroutines polls for the goroutine count to fall back to at most the
// baseline, returning the lowest count it observes within the window. Polling
// (rather than a single read) absorbs the small delay between Shutdown
// returning and a reaped goroutine actually exiting.
func settleGoroutines(baseline int) int {
	lowest := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		runtime.GC()
		n := runtime.NumGoroutine()
		if n < lowest {
			lowest = n
		}
		if lowest <= baseline {
			return lowest
		}
		time.Sleep(10 * time.Millisecond)
	}
	return lowest
}

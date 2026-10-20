package mediahttp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

const (
	performanceScaleDirectStreams      = 16
	performanceScaleDirectBytes        = 256 << 10
	performanceScaleDirectFloor        = 3 * time.Second
	performanceScaleDirectAllocCeiling = 32 << 20
)

// TestPerformanceScaleConcurrentDirectPlayFloor drives N simultaneous reads
// through the signed-token direct byte route and fails on latency, correctness,
// or allocation collapse.
func TestPerformanceScaleConcurrentDirectPlayFloor(t *testing.T) {
	if os.Getenv("BEAMFALL_PERF") != "1" {
		t.Skip("set BEAMFALL_PERF=1 (= make bench) to run performance floors")
	}
	body := make([]byte, performanceScaleDirectBytes)
	for i := range body {
		body[i] = byte(i)
	}
	handler, token := newTestHandlerWithBytes(t, "session-default", "media-alpha", body)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	errCh := make(chan string, performanceScaleDirectStreams)
	var wg sync.WaitGroup
	for range performanceScaleDirectStreams {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			handler.Direct(rec, httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil))
			if rec.Code != http.StatusOK || rec.Body.Len() != len(body) {
				errCh <- rec.Result().Status
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)
	close(errCh)
	for failure := range errCh {
		t.Errorf("direct stream failed: %s", failure)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("direct play: streams=%d bytes/stream=%d elapsed=%s allocated=%dMiB", performanceScaleDirectStreams, len(body), elapsed.Round(time.Millisecond), allocated>>20)
	if elapsed > performanceScaleDirectFloor {
		t.Errorf("%d direct streams took %s, floor %s", performanceScaleDirectStreams, elapsed, performanceScaleDirectFloor)
	}
	if allocated > performanceScaleDirectAllocCeiling {
		t.Errorf("direct streams allocated %d bytes, ceiling %d", allocated, performanceScaleDirectAllocCeiling)
	}
}

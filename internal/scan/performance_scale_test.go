package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store/storetest"
)

const (
	performanceScaleScanFiles        = 1_000
	performanceScaleScanFloor        = 20 * time.Second
	performanceScaleScanAllocCeiling = 192 << 20
)

// TestPerformanceScaleScanProjectionFloor gates the real bounded scan and
// catalog projection path. Fixture creation is excluded from both measurements.
func TestPerformanceScaleScanProjectionFloor(t *testing.T) {
	if os.Getenv("BEAMFALL_PERF") != "1" {
		t.Skip("set BEAMFALL_PERF=1 (= make bench) to run performance floors")
	}
	root := t.TempDir()
	settled := time.Now().Add(-2 * stabilityWindow)
	for i := range performanceScaleScanFiles {
		path := filepath.Join(root, fmt.Sprintf("group-%02d", i%20), fmt.Sprintf("media-%04d.mp4", i))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(fmt.Sprintf("fixture-%04d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, settled, settled); err != nil {
			t.Fatal(err)
		}
	}

	st := storetest.Open(t)
	prober := &atomicProber{probe: ffmpeg.Probe{Container: "mp4", VideoCodec: "h264", HasVideo: true, DurationSeconds: 60}}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	result, err := ScanWithWorkerGovernor(context.Background(), st, prober, nil, nil, nil, nil, nil, nil, fixedGovernor(4), Request{
		LibraryID: "library-local",
		Root:      root,
	})
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("scan/projection: %v", err)
	}
	if result.MediaImported != performanceScaleScanFiles {
		t.Fatalf("imported %d media, want %d", result.MediaImported, performanceScaleScanFiles)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("scan/projection: files=%d elapsed=%s allocated=%dMiB", performanceScaleScanFiles, elapsed.Round(time.Millisecond), allocated>>20)
	if elapsed > performanceScaleScanFloor {
		t.Errorf("scan/projection %s exceeds %s floor", elapsed, performanceScaleScanFloor)
	}
	if allocated > performanceScaleScanAllocCeiling {
		t.Errorf("scan/projection allocated %d bytes, ceiling %d", allocated, performanceScaleScanAllocCeiling)
	}
}

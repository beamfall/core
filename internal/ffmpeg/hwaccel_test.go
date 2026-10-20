package ffmpeg

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// stubHWAccelRunner records every encoder it was asked to probe and returns a
// canned result per encoder: an encoder explicitly mapped to nil succeeds, and
// any encoder absent from the map fails — modeling hardware the box lacks. This
// is the injected seam standing in for a real ffmpeg run, so the probe is
// exercised with no real hardware.
type stubHWAccelRunner struct {
	results  map[string]error
	attempts []string
}

// runStrict fails any encoder not explicitly listed, so a test asserts exactly
// which candidates were declared working.
func (s *stubHWAccelRunner) runStrict(_ context.Context, encoder string) error {
	s.attempts = append(s.attempts, encoder)
	if err, ok := s.results[encoder]; ok {
		return err
	}
	return errors.New("encoder not available")
}

func statusByAccel(result HWAccelResult) map[HWAccel]HWAccelStatus {
	out := map[HWAccel]HWAccelStatus{}
	for _, probe := range result.Results {
		out[probe.Accelerator] = probe.Status
	}
	return out
}

func TestProbeHardwareAccelLinuxReportsOnlyWorking(t *testing.T) {
	// QSV works, VAAPI fails, NVENC works → only QSV and NVENC are reported.
	stub := &stubHWAccelRunner{results: map[string]error{
		"h264_qsv":   nil,
		"h264_nvenc": nil,
		// h264_vaapi intentionally absent → runStrict fails it.
	}}
	got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:     stub.runStrict,
		GOOS:       "linux",
		DRIPresent: func() bool { return true },
	})
	want := []HWAccel{HWAccelQSV, HWAccelNVENC}
	if !reflect.DeepEqual(got.Working, want) {
		t.Fatalf("Working = %v, want %v", got.Working, want)
	}
	statuses := statusByAccel(got)
	if statuses[HWAccelQSV] != HWAccelWorking || statuses[HWAccelNVENC] != HWAccelWorking {
		t.Fatalf("working statuses = %#v, want QSV/NVENC working", statuses)
	}
	if statuses[HWAccelVAAPI] != HWAccelUnavailable {
		t.Fatalf("VAAPI status = %q, want unavailable", statuses[HWAccelVAAPI])
	}
	if got.SoftwareOnly() {
		t.Fatalf("SoftwareOnly() = true, want false (two accelerators worked)")
	}
	if !got.Supports(HWAccelQSV) || !got.Supports(HWAccelNVENC) {
		t.Fatalf("Supports missed a working accelerator: %+v", got)
	}
	if got.Supports(HWAccelVAAPI) {
		t.Fatalf("Supports(VAAPI) = true, but its probe failed")
	}
	// VAAPI must have been attempted (DRI present) even though it failed.
	wantAttempts := []string{"h264_qsv", "h264_vaapi", "h264_nvenc"}
	if !reflect.DeepEqual(stub.attempts, wantAttempts) {
		t.Fatalf("attempts = %v, want %v", stub.attempts, wantAttempts)
	}
}

func TestSelectHWAccelPrefersVerifiedConfiguredAccelerator(t *testing.T) {
	result := HWAccelResult{
		Results: []HWAccelProbe{
			{Accelerator: HWAccelQSV, Status: HWAccelWorking},
			{Accelerator: HWAccelNVENC, Status: HWAccelWorking},
		},
		Working: []HWAccel{HWAccelQSV, HWAccelNVENC},
	}
	if got := SelectHWAccel(result, HWAccelNVENC); got != HWAccelNVENC {
		t.Fatalf("SelectHWAccel = %q, want configured NVENC", got)
	}
}

func TestSelectHWAccelFallsBackToFirstVerifiedAccelerator(t *testing.T) {
	result := HWAccelResult{
		Results: []HWAccelProbe{
			{Accelerator: HWAccelQSV, Status: HWAccelWorking},
			{Accelerator: HWAccelNVENC, Status: HWAccelWorking},
		},
		Working: []HWAccel{HWAccelQSV, HWAccelNVENC},
	}
	if got := SelectHWAccel(result, HWAccelVAAPI); got != HWAccelQSV {
		t.Fatalf("SelectHWAccel = %q, want first verified QSV", got)
	}
}

func TestTranscodeCapForHWAccelDerivesFromProbeResult(t *testing.T) {
	if got := TranscodeCapForHWAccel(HWAccelResult{}); got != softwareTranscodeCap {
		t.Fatalf("software cap = %d, want %d", got, softwareTranscodeCap)
	}
	result := HWAccelResult{
		Results: []HWAccelProbe{{Accelerator: HWAccelVideoToolbox, Status: HWAccelWorking}},
		Working: []HWAccel{HWAccelVideoToolbox},
	}
	if got := TranscodeCapForHWAccel(result); got != hardwareTranscodeCap {
		t.Fatalf("hardware cap = %d, want %d", got, hardwareTranscodeCap)
	}
}

func TestProbeHardwareAccelDegradesWhenDRIAbsent(t *testing.T) {
	// /dev/dri absent: QSV and VAAPI must not even be attempted; NVENC still is.
	stub := &stubHWAccelRunner{results: map[string]error{
		"h264_nvenc": nil,
	}}
	got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:     stub.runStrict,
		GOOS:       "linux",
		DRIPresent: func() bool { return false },
	})
	if want := []HWAccel{HWAccelNVENC}; !reflect.DeepEqual(got.Working, want) {
		t.Fatalf("Working = %v, want %v", got.Working, want)
	}
	if want := []string{"h264_nvenc"}; !reflect.DeepEqual(stub.attempts, want) {
		t.Fatalf("attempts = %v, want %v (QSV/VAAPI must be skipped without /dev/dri)", stub.attempts, want)
	}
	statuses := statusByAccel(got)
	if statuses[HWAccelQSV] != HWAccelUnavailable || statuses[HWAccelVAAPI] != HWAccelUnavailable {
		t.Fatalf("DRI-gated statuses = %#v, want QSV/VAAPI unavailable", statuses)
	}
}

func TestProbeHardwareAccelConfiguredButFailed(t *testing.T) {
	// Every probe fails: software-only, no error, with the configured
	// accelerator distinguished from merely unavailable accelerators.
	stub := &stubHWAccelRunner{results: map[string]error{}}
	got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:               stub.runStrict,
		GOOS:                 "linux",
		DRIPresent:           func() bool { return true },
		ConfiguredPreference: HWAccelVAAPI,
	})
	if !got.SoftwareOnly() {
		t.Fatalf("SoftwareOnly() = false, want true (no accelerator worked): %+v", got)
	}
	if len(got.Working) != 0 {
		t.Fatalf("Working = %v, want empty", got.Working)
	}
	// All three Linux candidates were still test-driven.
	if want := []string{"h264_qsv", "h264_vaapi", "h264_nvenc"}; !reflect.DeepEqual(stub.attempts, want) {
		t.Fatalf("attempts = %v, want %v", stub.attempts, want)
	}
	statuses := statusByAccel(got)
	if statuses[HWAccelVAAPI] != HWAccelConfiguredFailed {
		t.Fatalf("configured VAAPI status = %q, want configured-but-failed", statuses[HWAccelVAAPI])
	}
	if statuses[HWAccelQSV] != HWAccelUnavailable || statuses[HWAccelNVENC] != HWAccelUnavailable {
		t.Fatalf("unconfigured failed statuses = %#v, want unavailable", statuses)
	}
}

func TestProbeHardwareAccelOutputIsSanitized(t *testing.T) {
	runner := func(context.Context, string) error {
		return errors.New("ffmpeg failed on /Users/alice/private/movie.mkv")
	}
	got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:               runner,
		GOOS:                 "darwin",
		DRIPresent:           func() bool { return false },
		ConfiguredPreference: HWAccelVideoToolbox,
	})
	for _, probe := range got.Results {
		if strings.Contains(string(probe.Status), "/Users/") || strings.Contains(string(probe.Accelerator), "/Users/") {
			t.Fatalf("probe leaked path: %#v", probe)
		}
	}
}

func TestProbeHardwareAccelDarwinProbesVideoToolbox(t *testing.T) {
	stub := &stubHWAccelRunner{results: map[string]error{
		"h264_videotoolbox": nil,
	}}
	got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner: stub.runStrict,
		GOOS:   "darwin",
		// DRIPresent should be irrelevant on darwin; make it true to prove QSV
		// and VAAPI are not probed regardless.
		DRIPresent: func() bool { return true },
	})
	if want := []HWAccel{HWAccelVideoToolbox}; !reflect.DeepEqual(got.Working, want) {
		t.Fatalf("Working = %v, want %v", got.Working, want)
	}
	if want := []string{"h264_videotoolbox"}; !reflect.DeepEqual(stub.attempts, want) {
		t.Fatalf("attempts = %v, want %v", stub.attempts, want)
	}
}

func TestProbeHardwareAccelUnknownPlatformProbesNothing(t *testing.T) {
	stub := &stubHWAccelRunner{results: map[string]error{}}
	got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:     stub.runStrict,
		GOOS:       "windows",
		DRIPresent: func() bool { return true },
	})
	if !got.SoftwareOnly() || len(stub.attempts) != 0 {
		t.Fatalf("expected no probes on unknown platform; Working=%v attempts=%v", got.Working, stub.attempts)
	}
	for accel, status := range statusByAccel(got) {
		if status != HWAccelUnavailable {
			t.Fatalf("%s status = %q, want unavailable on unknown platform", accel, status)
		}
	}
}

func TestProbeHardwareAccelStopsWhenContextCancelled(t *testing.T) {
	// A cancelled parent context halts the loop before any further candidate is
	// attempted (startup probe must not run past a shutdown signal).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stub := &stubHWAccelRunner{results: map[string]error{"h264_qsv": nil}}
	got := ProbeHardwareAccel(ctx, HWAccelOptions{
		Runner:     stub.runStrict,
		GOOS:       "linux",
		DRIPresent: func() bool { return true },
	})
	if len(stub.attempts) != 0 {
		t.Fatalf("attempts = %v, want none (context already cancelled)", stub.attempts)
	}
	if !got.SoftwareOnly() {
		t.Fatalf("SoftwareOnly() = false, want true under cancelled context")
	}
}

func TestProbeHardwareAccelAppliesPerProbeTimeout(t *testing.T) {
	// The runner sees a deadline within PerProbeTimeout of now — proof each
	// candidate is time-boxed (ADR-0013 §4 ~5s probe), not run unbounded.
	var sawDeadline bool
	runner := func(ctx context.Context, _ string) error {
		if dl, ok := ctx.Deadline(); ok {
			sawDeadline = true
			if d := time.Until(dl); d <= 0 || d > 100*time.Millisecond {
				t.Errorf("per-probe deadline = %v, want within (0, 100ms]", d)
			}
		} else {
			t.Errorf("probe ctx had no deadline; PerProbeTimeout not applied")
		}
		return nil
	}
	ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:          runner,
		GOOS:            "darwin",
		DRIPresent:      func() bool { return false },
		PerProbeTimeout: 80 * time.Millisecond,
	})
	if !sawDeadline {
		t.Fatalf("runner was never invoked")
	}
}

func TestProbeHardwareAccelTotalTimeoutBoundsWholeProbe(t *testing.T) {
	attempts := 0
	runner := func(ctx context.Context, _ string) error {
		attempts++
		<-ctx.Done()
		return ctx.Err()
	}
	start := time.Now()
	got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:               runner,
		GOOS:                 "linux",
		DRIPresent:           func() bool { return true },
		ConfiguredPreference: HWAccelQSV,
		PerProbeTimeout:      time.Second,
		TotalTimeout:         25 * time.Millisecond,
	})
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("probe elapsed %v, want bounded by TotalTimeout", elapsed)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 before total timeout halts later probes", attempts)
	}
	statuses := statusByAccel(got)
	if statuses[HWAccelQSV] != HWAccelConfiguredFailed {
		t.Fatalf("timed-out configured QSV status = %q, want configured-but-failed", statuses[HWAccelQSV])
	}
}

func TestHWAccelProbeArgsUploadsSoftwareFramesForVAAPI(t *testing.T) {
	vaapiArgs := strings.Join(hwAccelProbeArgs("h264_vaapi"), " ")
	for _, want := range []string{"-vaapi_device", "format=nv12,hwupload", "-c:v h264_vaapi"} {
		if !strings.Contains(vaapiArgs, want) {
			t.Fatalf("VAAPI probe args = %q, want %q", vaapiArgs, want)
		}
	}

	nvencArgs := strings.Join(hwAccelProbeArgs("h264_nvenc"), " ")
	for _, forbidden := range []string{"-vaapi_device", "hwupload"} {
		if strings.Contains(nvencArgs, forbidden) {
			t.Fatalf("NVENC probe args = %q, must not contain %q", nvencArgs, forbidden)
		}
	}
}

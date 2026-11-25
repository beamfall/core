package ffmpeg

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubHWAccelRunner records every encode it was asked to probe and returns a
// canned result per encode: an encode explicitly mapped to nil succeeds, and
// any encode absent from the map fails — modeling hardware the box lacks. An
// encode is keyed by its encoder name, with ":low_power" appended for a
// low-power encode (CORE-2094). This
// is the injected seam standing in for a real ffmpeg run, so the probe is
// exercised with no real hardware.
type stubHWAccelRunner struct {
	results  map[string]error
	mu       sync.Mutex
	attempts []string
}

// runStrict fails any encoder not explicitly listed, so a test asserts exactly
// which candidates were declared working.
func (s *stubHWAccelRunner) runStrict(_ context.Context, encode HWAccelEncode) error {
	key := encode.Encoder
	if encode.LowPower {
		key += lowPowerSuffix
	}
	s.mu.Lock()
	s.attempts = append(s.attempts, key)
	s.mu.Unlock()
	if err, ok := s.results[key]; ok {
		return err
	}
	return errors.New("encoder not available")
}

const lowPowerSuffix = ":low_power"

// assertAttempts checks which encodes were probed. Candidates probe
// concurrently, so the comparison is order-free across accelerators; within one
// accelerator every follow-up encode must come after its H.264 encode
// (CORE-2088), and a low-power H.264 retry after the normal one (CORE-2094).
func (s *stubHWAccelRunner) assertAttempts(t *testing.T, want ...string) {
	t.Helper()
	s.mu.Lock()
	got := append([]string(nil), s.attempts...)
	s.mu.Unlock()
	sortedGot, sortedWant := slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))
	if !slices.Equal(sortedGot, sortedWant) {
		t.Fatalf("attempts = %v, want %v (any order)", got, want)
	}
	for i, attempt := range got {
		encoder, lowPower := strings.CutSuffix(attempt, lowPowerSuffix)
		codec, accel, _ := strings.Cut(encoder, "_")
		if codec == "h264" && !lowPower {
			continue
		}
		if first := slices.Index(got, "h264_"+accel); first < 0 || first > i {
			t.Fatalf("attempts = %v: %s probed before h264_%s", got, attempt, accel)
		}
	}
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
	// NVENC's HEVC encoder works too; QSV's does not.
	stub := &stubHWAccelRunner{results: map[string]error{
		"h264_qsv":   nil,
		"h264_nvenc": nil,
		"hevc_nvenc": nil,
		// h264_vaapi and hevc_qsv intentionally absent → runStrict fails them.
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
	if !got.SupportsHEVC(HWAccelNVENC) || got.SupportsHEVC(HWAccelQSV) || got.SupportsHEVC(HWAccelVAAPI) {
		t.Fatalf("SupportsHEVC = qsv:%v vaapi:%v nvenc:%v, want only nvenc",
			got.SupportsHEVC(HWAccelQSV), got.SupportsHEVC(HWAccelVAAPI), got.SupportsHEVC(HWAccelNVENC))
	}
	// VAAPI must have been attempted (DRI present) even though it failed; HEVC
	// is probed only behind a passing H.264 probe.
	// A failed VAAPI encode is retried in low-power mode; AV1 follows HEVC.
	stub.assertAttempts(t, "h264_qsv", "hevc_qsv", "av1_qsv", "h264_vaapi", "h264_vaapi:low_power",
		"h264_nvenc", "hevc_nvenc", "av1_nvenc")
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

func TestTranscodeCapForDerivesFromHostAndBackend(t *testing.T) {
	cases := []struct {
		accel HWAccel
		cpus  int
		want  int
	}{
		// Software: one session per four cores, clamped to 1..8.
		{"", 1, 1},
		{"", 4, 1},
		{"", 8, 2},
		{"", 16, 4},
		{"", 64, softwareTranscodeCapMax},
		// Hardware: one per core, at least two, at most the backend limit.
		{HWAccelQSV, 1, hardwareTranscodeCapMin},
		{HWAccelQSV, 4, 4},
		{HWAccelQSV, 32, integratedGPUTranscodeCap},
		{HWAccelVAAPI, 32, integratedGPUTranscodeCap},
		{HWAccelNVENC, 6, 6},
		{HWAccelNVENC, 32, nvencSessionLimit},
		{HWAccelVideoToolbox, 10, videoToolboxTranscodeCap},
		// An accelerator outside the vocabulary is software.
		{HWAccel("bogus"), 16, 4},
	}
	for _, tc := range cases {
		if got := transcodeCapFor(tc.accel, tc.cpus); got != tc.want {
			t.Errorf("transcodeCapFor(%q, %d) = %d, want %d", tc.accel, tc.cpus, got, tc.want)
		}
	}
	if got := DefaultTranscodeCap(""); got < 1 || got > softwareTranscodeCapMax {
		t.Fatalf("DefaultTranscodeCap(software) = %d, want within 1..%d", got, softwareTranscodeCapMax)
	}
	if nvencSessionLimit != 8 || integratedGPUTranscodeCap != 6 || videoToolboxTranscodeCap != 4 {
		t.Fatalf("backend limits drifted from the documented spec (openspec hardware-transcode)")
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
	stub.assertAttempts(t, "h264_nvenc", "hevc_nvenc", "av1_nvenc")
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
	// All three Linux candidates were still test-driven, the Intel ones in
	// low-power mode too.
	stub.assertAttempts(t, "h264_qsv", "h264_qsv:low_power", "h264_vaapi", "h264_vaapi:low_power", "h264_nvenc")
	statuses := statusByAccel(got)
	if statuses[HWAccelVAAPI] != HWAccelConfiguredFailed {
		t.Fatalf("configured VAAPI status = %q, want configured-but-failed", statuses[HWAccelVAAPI])
	}
	if statuses[HWAccelQSV] != HWAccelUnavailable || statuses[HWAccelNVENC] != HWAccelUnavailable {
		t.Fatalf("unconfigured failed statuses = %#v, want unavailable", statuses)
	}
}

func TestProbeHardwareAccelOutputIsSanitized(t *testing.T) {
	runner := func(context.Context, HWAccelEncode) error {
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
	// VideoToolbox has no AV1 encoder and no low-power retry.
	stub.assertAttempts(t, "h264_videotoolbox", "hevc_videotoolbox")
}

func TestProbeHardwareAccelUnknownPlatformProbesNothing(t *testing.T) {
	stub := &stubHWAccelRunner{results: map[string]error{}}
	got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:     stub.runStrict,
		GOOS:       "freebsd",
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
	runner := func(ctx context.Context, _ HWAccelEncode) error {
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
	var attempts atomic.Int32
	runner := func(ctx context.Context, _ HWAccelEncode) error {
		attempts.Add(1)
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
	// Every Linux candidate starts its H.264 encode at once; the total
	// timeout ends them all, and neither a low-power retry nor an HEVC encode
	// follows once the probe as a whole is out of time.
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3 (one H.264 encode per candidate)", got)
	}
	statuses := statusByAccel(got)
	if statuses[HWAccelQSV] != HWAccelConfiguredFailed {
		t.Fatalf("timed-out configured QSV status = %q, want configured-but-failed", statuses[HWAccelQSV])
	}
}

func TestHWAccelProbeArgsUploadsSoftwareFramesForVAAPI(t *testing.T) {
	vaapiArgs := strings.Join(hwAccelProbeArgs(HWAccelEncode{Encoder: "h264_vaapi"}), " ")
	for _, want := range []string{"-vaapi_device", "format=nv12,hwupload", "-c:v h264_vaapi"} {
		if !strings.Contains(vaapiArgs, want) {
			t.Fatalf("VAAPI probe args = %q, want %q", vaapiArgs, want)
		}
	}

	nvencArgs := strings.Join(hwAccelProbeArgs(HWAccelEncode{Encoder: "h264_nvenc"}), " ")
	for _, forbidden := range []string{"-vaapi_device", "hwupload"} {
		if strings.Contains(nvencArgs, forbidden) {
			t.Fatalf("NVENC probe args = %q, must not contain %q", nvencArgs, forbidden)
		}
	}
}

func TestProbeHardwareAccelH264PassHEVCFailIsNotHEVCCapable(t *testing.T) {
	// The CORE-2088 case: a GPU whose H.264 encoder works but whose HEVC
	// encoder does not stays a working accelerator, but HEVC is not verified,
	// so the runner neither advertises nor selects HEVC output.
	stub := &stubHWAccelRunner{results: map[string]error{"h264_nvenc": nil}}
	got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:     stub.runStrict,
		GOOS:       "linux",
		DRIPresent: func() bool { return false },
	})
	if !got.Supports(HWAccelNVENC) {
		t.Fatalf("Supports(NVENC) = false, want true (H.264 probe passed)")
	}
	if got.SupportsHEVC(HWAccelNVENC) {
		t.Fatalf("SupportsHEVC(NVENC) = true, but the hevc_nvenc probe failed")
	}
	stub.assertAttempts(t, "h264_nvenc", "hevc_nvenc", "av1_nvenc")

	selected := SelectHWAccel(got, "")
	runner := New()
	runner.SetHardwareAccel(selected, got.SupportsHEVC(selected))
	if !runner.SupportsTranscodeCodec("h264") {
		t.Fatalf("SupportsTranscodeCodec(h264) = false, want true")
	}
	if runner.SupportsTranscodeCodec("hevc") {
		t.Fatalf("SupportsTranscodeCodec(hevc) = true, but HEVC was not verified")
	}
	runner.SetHardwareAccel(selected, true)
	if !runner.SupportsTranscodeCodec("hevc") {
		t.Fatalf("SupportsTranscodeCodec(hevc) = false with a verified HEVC encoder")
	}
	runner.SetHardwareAccel("", true)
	if runner.SupportsTranscodeCodec("hevc") {
		t.Fatalf("SupportsTranscodeCodec(hevc) = true with software transcode")
	}
}

func TestRenditionUnverifiedHEVCTakesTheSoftwarePath(t *testing.T) {
	// VAAPI is the sharp case: its upload filters hand hardware frames to the
	// encoder, so an unverified HEVC rendition must drop the accelerator as a
	// whole, not just swap the encoder name.
	runner := New()
	runner.SetHardwareAccel(HWAccelVAAPI, false)
	hevc := Rendition{Height: 720, VideoCodec: "hevc"}
	if got := runner.renditionHWAccel(hevc, EncodePolicy{}); got != "" {
		t.Fatalf("renditionHWAccel(unverified hevc) = %q, want software", got)
	}
	sw := hevc.withHWAccel(runner.renditionHWAccel(hevc, EncodePolicy{}))
	if args := strings.Join(sw.args(), " "); strings.Contains(args, "hwupload") || strings.Contains(args, "vaapi") {
		t.Fatalf("unverified HEVC args = %q, want no VAAPI upload", args)
	}
	if got := sw.videoArgs(); len(got) < 2 || got[1] != "libx264" {
		t.Fatalf("videoArgs = %v, want the libx264 fallback", got)
	}
	if got := runner.renditionHWAccel(Rendition{Height: 720}, EncodePolicy{}); got != HWAccelVAAPI {
		t.Fatalf("renditionHWAccel(h264) = %q, want vaapi", got)
	}
	runner.SetHardwareAccel(HWAccelVAAPI, true)
	if got := runner.renditionHWAccel(hevc, EncodePolicy{}); got != HWAccelVAAPI {
		t.Fatalf("renditionHWAccel(verified hevc) = %q, want vaapi", got)
	}
}

func TestHWAccelProbeArgsUploadsSoftwareFramesForHEVCVAAPI(t *testing.T) {
	args := strings.Join(hwAccelProbeArgs(HWAccelEncode{Encoder: "hevc_vaapi"}), " ")
	for _, want := range []string{"-vaapi_device", "format=nv12,hwupload", "-c:v hevc_vaapi"} {
		if !strings.Contains(args, want) {
			t.Fatalf("HEVC VAAPI probe args = %q, want %q", args, want)
		}
	}
}

func TestProbeHardwareAccelHungCandidateDoesNotStarveOthers(t *testing.T) {
	// CORE-2090: QSV wedges until its own probe deadline. VAAPI and NVENC still
	// get their full chains, HEVC included, and the report keeps vocabulary
	// order whichever probe finishes first.
	working := map[string]bool{"h264_vaapi": true, "hevc_vaapi": true, "h264_nvenc": true, "hevc_nvenc": true}
	runner := func(ctx context.Context, encode HWAccelEncode) error {
		encoder := encode.Encoder
		if encoder == "h264_qsv" {
			<-ctx.Done()
			return ctx.Err()
		}
		if working[encoder] {
			return nil
		}
		return errors.New("encoder not available")
	}
	start := time.Now()
	got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:          runner,
		GOOS:            "linux",
		DRIPresent:      func() bool { return true },
		PerProbeTimeout: 100 * time.Millisecond,
	})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("probe elapsed %v, want bounded by one candidate chain", elapsed)
	}
	if want := []HWAccel{HWAccelVAAPI, HWAccelNVENC}; !reflect.DeepEqual(got.Working, want) {
		t.Fatalf("Working = %v, want %v", got.Working, want)
	}
	if !got.SupportsHEVC(HWAccelVAAPI) || !got.SupportsHEVC(HWAccelNVENC) {
		t.Fatalf("HEVC not verified behind a hung QSV probe: %+v", got.Results)
	}
	statuses := statusByAccel(got)
	if statuses[HWAccelQSV] != HWAccelUnavailable {
		t.Fatalf("hung QSV status = %q, want unavailable", statuses[HWAccelQSV])
	}
	var order []HWAccel
	for _, probe := range got.Results {
		order = append(order, probe.Accelerator)
	}
	if !reflect.DeepEqual(order, allHWAccels) {
		t.Fatalf("report order = %v, want %v", order, allHWAccels)
	}
}

func TestProbeHardwareAccelVerifiesAV1BehindH264(t *testing.T) {
	// CORE-2094 normal pass: QSV's H.264 encode works first time, so no
	// low-power retry runs and AV1 is verified behind it.
	stub := &stubHWAccelRunner{results: map[string]error{"h264_qsv": nil, "av1_qsv": nil}}
	got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:     stub.runStrict,
		GOOS:       "linux",
		DRIPresent: func() bool { return true },
	})
	want := HardwareEncode{Accel: HWAccelQSV, AV1: true}
	if encode := got.HardwareEncode(HWAccelQSV); encode != want {
		t.Fatalf("HardwareEncode(QSV) = %+v, want %+v", encode, want)
	}
	if encode := got.HardwareEncode(HWAccelVAAPI); encode != (HardwareEncode{}) {
		t.Fatalf("HardwareEncode(failed VAAPI) = %+v, want software", encode)
	}
	stub.assertAttempts(t, "h264_qsv", "hevc_qsv", "av1_qsv",
		"h264_vaapi", "h264_vaapi:low_power", "h264_nvenc")
}

func TestProbeHardwareAccelFallsBackToLowPower(t *testing.T) {
	// CORE-2094: the normal VAAPI encode fails, the -low_power 1 retry passes.
	// The accelerator is working in low-power mode and its follow-up codecs
	// are probed in low-power mode too.
	stub := &stubHWAccelRunner{results: map[string]error{
		"h264_vaapi:low_power": nil,
		"hevc_vaapi:low_power": nil,
		"av1_vaapi:low_power":  nil,
	}}
	got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:     stub.runStrict,
		GOOS:       "linux",
		DRIPresent: func() bool { return true },
	})
	if want := []HWAccel{HWAccelVAAPI}; !reflect.DeepEqual(got.Working, want) {
		t.Fatalf("Working = %v, want %v", got.Working, want)
	}
	want := HardwareEncode{Accel: HWAccelVAAPI, HEVC: true, AV1: true, LowPower: true}
	if encode := got.HardwareEncode(HWAccelVAAPI); encode != want {
		t.Fatalf("HardwareEncode(VAAPI) = %+v, want %+v", encode, want)
	}
	stub.assertAttempts(t, "h264_qsv", "h264_qsv:low_power",
		"h264_vaapi", "h264_vaapi:low_power", "hevc_vaapi:low_power", "av1_vaapi:low_power",
		"h264_nvenc")
}

func TestProbeHardwareAccelLowPowerRetryAlsoFails(t *testing.T) {
	// CORE-2094: both the normal and the low-power encode fail, so the
	// configured accelerator is configured-but-failed and nothing follows.
	stub := &stubHWAccelRunner{results: map[string]error{}}
	got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:               stub.runStrict,
		GOOS:                 "linux",
		DRIPresent:           func() bool { return false },
		ConfiguredPreference: HWAccelNVENC,
	})
	if !got.SoftwareOnly() {
		t.Fatalf("SoftwareOnly() = false, want true: %+v", got)
	}
	// NVENC has no low-power mode, so it is not retried.
	stub.assertAttempts(t, "h264_nvenc")

	stub = &stubHWAccelRunner{results: map[string]error{}}
	got = ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:               stub.runStrict,
		GOOS:                 "linux",
		DRIPresent:           func() bool { return true },
		ConfiguredPreference: HWAccelQSV,
	})
	if statuses := statusByAccel(got); statuses[HWAccelQSV] != HWAccelConfiguredFailed {
		t.Fatalf("QSV status = %q, want configured-but-failed", statuses[HWAccelQSV])
	}
	for _, probe := range got.Results {
		if probe.LowPower || probe.HEVC || probe.AV1 {
			t.Fatalf("failed probe reports capabilities: %+v", probe)
		}
	}
	stub.assertAttempts(t, "h264_qsv", "h264_qsv:low_power", "h264_vaapi", "h264_vaapi:low_power", "h264_nvenc")
}

func TestHWAccelProbeArgsLowPowerAndAV1(t *testing.T) {
	low := strings.Join(hwAccelProbeArgs(HWAccelEncode{Encoder: "h264_qsv", LowPower: true}), " ")
	if !strings.Contains(low, "-c:v h264_qsv -low_power 1 -preset veryfast -forced_idr 1 -b:v 1000k -maxrate 1001k -bufsize 2000k -f null -") {
		t.Fatalf("low-power QSV probe args = %q, want -low_power 1 and the production rate control after the encoder", low)
	}
	// The probe uses the production rate control, so a low-power encoder that
	// cannot do VBR (no HuC firmware) fails the probe instead of every encode.
	for encoder, want := range map[string]string{
		"hevc_vaapi": "-c:v hevc_vaapi -rc_mode VBR -b:v 1000k -maxrate 1000k -bufsize 2000k -f null -",
		"av1_nvenc":  "-c:v av1_nvenc -preset p4 -forced-idr 1 -spatial-aq 1 -rc vbr -cq 23 -b:v 0 -maxrate 1000k -bufsize 2000k -f null -",
		"h264_amf":   "-c:v h264_amf -quality speed -forced_idr 1 -vbaq 1 -rc vbr_peak -b:v 1000k -maxrate 1000k -bufsize 2000k -f null -",
	} {
		if got := strings.Join(hwAccelProbeArgs(HWAccelEncode{Encoder: encoder}), " "); !strings.Contains(got, want) {
			t.Fatalf("%s probe args = %q, want %q", encoder, got, want)
		}
	}
	if normal := strings.Join(hwAccelProbeArgs(HWAccelEncode{Encoder: "h264_qsv"}), " "); strings.Contains(normal, "-low_power") {
		t.Fatalf("normal QSV probe args = %q, must not ask for low power", normal)
	}
	av1 := strings.Join(hwAccelProbeArgs(HWAccelEncode{Encoder: "av1_vaapi", LowPower: true}), " ")
	for _, want := range []string{"-vaapi_device", "format=nv12,hwupload", "-c:v av1_vaapi -low_power 1"} {
		if !strings.Contains(av1, want) {
			t.Fatalf("AV1 VAAPI probe args = %q, want %q", av1, want)
		}
	}
	// The source is 4:2:0 and wide enough for AMF's HEVC and AV1 minimums.
	if got := strings.Join(hwAccelProbeArgs(HWAccelEncode{Encoder: "av1_nvenc"}), " "); !strings.Contains(got, "size=256x256:rate=10,format=nv12") {
		t.Fatalf("AV1 NVENC probe args = %q, want a 256x256 NV12 source", got)
	}
	// NVENC has no low-power mode; the flag is never passed to it.
	nvenc := strings.Join(hwAccelProbeArgs(HWAccelEncode{Encoder: "av1_nvenc", LowPower: true}), " ")
	if strings.Contains(nvenc, "-low_power") || !strings.Contains(nvenc, "-c:v av1_nvenc") {
		t.Fatalf("AV1 NVENC probe args = %q, want av1_nvenc without -low_power", nvenc)
	}
}

func TestRenditionHardwareEncodeLowPowerAndAV1(t *testing.T) {
	runner := New()
	runner.SetHardwareEncode(HardwareEncode{Accel: HWAccelQSV, LowPower: true})
	h264 := runner.hardwareRendition(Rendition{Height: 720})
	if got, want := h264.videoArgs(), []string{"-c:v", "h264_qsv", "-low_power", "1"}; !slices.Equal(got, want) {
		t.Fatalf("low-power QSV videoArgs = %v, want %v", got, want)
	}
	// AV1 was not verified: the rendition drops the accelerator and its
	// low-power flag together.
	av1 := runner.hardwareRendition(Rendition{Height: 720, VideoCodec: "av1"})
	if av1.hwAccel != "" || av1.hwLowPower {
		t.Fatalf("unverified AV1 rendition = accel %q lowPower %v, want software", av1.hwAccel, av1.hwLowPower)
	}
	if got := av1.videoArgs(); got[1] != "libx264" {
		t.Fatalf("unverified AV1 videoArgs = %v, want the software fallback", got)
	}
	if runner.SupportsTranscodeCodec("av1") {
		t.Fatalf("SupportsTranscodeCodec(av1) = true, but AV1 was not verified")
	}

	for accel, encoder := range map[HWAccel]string{HWAccelQSV: "av1_qsv", HWAccelVAAPI: "av1_vaapi", HWAccelNVENC: "av1_nvenc"} {
		runner.SetHardwareEncode(HardwareEncode{Accel: accel, AV1: true})
		if !runner.SupportsTranscodeCodec("av1") {
			t.Fatalf("%s: SupportsTranscodeCodec(av1) = false with a verified AV1 encoder", accel)
		}
		got := runner.hardwareRendition(Rendition{Height: 720, VideoCodec: "av1"}).videoArgs()
		if want := []string{"-c:v", encoder}; !slices.Equal(got, want) {
			t.Fatalf("%s AV1 videoArgs = %v, want %v", accel, got, want)
		}
	}
	runner.SetHardwareEncode(HardwareEncode{Accel: HWAccelVideoToolbox, AV1: true})
	if runner.SupportsTranscodeCodec("av1") {
		t.Fatal("SupportsTranscodeCodec(av1) = true on VideoToolbox, which has no AV1 encoder")
	}

	// Low-power mode is ignored on an accelerator that has none.
	runner.SetHardwareEncode(HardwareEncode{Accel: HWAccelNVENC, LowPower: true})
	if got := runner.hardwareRendition(Rendition{Height: 720}).videoArgs(); slices.Contains(got, "-low_power") {
		t.Fatalf("NVENC videoArgs = %v, must not carry -low_power", got)
	}
}

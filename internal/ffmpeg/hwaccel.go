package ffmpeg

// Hardware-acceleration probe: the server test-drives each
// candidate accelerator with a short, time-boxed trivial encode and reports
// only the ones that ACTUALLY worked, instead of presenting a faith-based
// dropdown. The NAS-majority Intel path (QSV/VAAPI via /dev/dri) is the reason
// this is launch-blocking: an operator must be able to trust that the
// accelerator the server claims is real on their box, not merely compiled into
// the FFmpeg build.
//
// This file does NOT spawn FFmpeg itself — the command runner is injected (the
// HWAccelRunner seam) so the probe is testable with a stub that records which
// encodes were attempted and returns canned success/failure, with no real
// hardware required. The default runner (DefaultHWAccelRunner) wires the seam
// to a real time-boxed ffmpeg invocation.

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// HWAccel identifies a hardware video encoder Beamfall may use for transcode.
// The set is closed and named by mechanism, not vendor product line.
type HWAccel string

const (
	// HWAccelQSV is Intel Quick Sync Video (Linux, via /dev/dri).
	HWAccelQSV HWAccel = "qsv"
	// HWAccelVAAPI is the VA-API path (Linux, via /dev/dri) — the generic Intel
	// (and AMD) GPU encode path that the NAS majority relies on.
	HWAccelVAAPI HWAccel = "vaapi"
	// HWAccelNVENC is NVIDIA's NVENC (Linux/Windows, via the NVIDIA runtime).
	HWAccelNVENC HWAccel = "nvenc"
	// HWAccelVideoToolbox is Apple's VideoToolbox encoder (macOS hosts).
	HWAccelVideoToolbox HWAccel = "videotoolbox"
)

var allHWAccels = []HWAccel{HWAccelQSV, HWAccelVAAPI, HWAccelNVENC, HWAccelVideoToolbox}

// HWAccelFromString resolves s against the closed accelerator vocabulary.
func HWAccelFromString(s string) (HWAccel, bool) {
	for _, a := range allHWAccels {
		if string(a) == s {
			return a, true
		}
	}
	return "", false
}

// HWAccelStatus describes what the probe actually proved for one accelerator.
type HWAccelStatus string

const (
	HWAccelUnavailable      HWAccelStatus = "unavailable"
	HWAccelConfiguredFailed HWAccelStatus = "configured-but-failed"
	HWAccelWorking          HWAccelStatus = "working"
)

// HWAccelProbe is the sanitized probe result for one accelerator. It never
// includes FFmpeg stderr or local paths.
type HWAccelProbe struct {
	Accelerator HWAccel
	// Status is the H.264 probe verdict, which decides whether the accelerator
	// is usable at all.
	Status HWAccelStatus
	// HEVC reports that the accelerator's HEVC encoder also passed its own
	// trivial encode. It is probed only after the H.264 encode works: a GPU
	// that encodes H.264 does not necessarily encode HEVC (older NVENC, QSV
	// before Skylake, Macs without an HEVC media engine), so HEVC is never
	// inferred from the H.264 verdict (CORE-2088, ADR-0013 §4).
	HEVC bool
}

// hwAccelEncoder maps an accelerator to the ffmpeg encoder name its trivial
// probe encode targets. Probing the encoder is what verifies the full path
// (driver + device + ffmpeg build), which a mere `-hwaccels` listing does not.
var hwAccelEncoder = map[HWAccel]string{
	HWAccelQSV:          "h264_qsv",
	HWAccelVAAPI:        "h264_vaapi",
	HWAccelNVENC:        "h264_nvenc",
	HWAccelVideoToolbox: "h264_videotoolbox",
}

var hwAccelHEVCEncoder = map[HWAccel]string{
	HWAccelQSV:          "hevc_qsv",
	HWAccelVAAPI:        "hevc_vaapi",
	HWAccelNVENC:        "hevc_nvenc",
	HWAccelVideoToolbox: "hevc_videotoolbox",
}

func hwAccelVideoEncoder(accel HWAccel, codec string) string {
	if codec == "hevc" {
		return hwAccelHEVCEncoder[accel]
	}
	return hwAccelEncoder[accel]
}

// HWAccelRunner runs one trivial probe encode for the named ffmpeg encoder and
// returns nil only if the encode succeeded. The injected seam: the real
// implementation execs a time-boxed ffmpeg; tests pass a stub that returns
// canned results and records which encoders were attempted, so the probe is
// exercised without real hardware. The probe calls it concurrently for
// different accelerators, so it must be safe for concurrent use.
type HWAccelRunner func(ctx context.Context, encoder string) error

// HWAccelOptions configures a probe run. The zero value is a valid production
// probe: a real time-boxed ffmpeg runner, the host's real GOOS, and a real
// /dev/dri check. Tests override the fields to drive every branch.
type HWAccelOptions struct {
	// Runner test-drives one encoder. nil uses DefaultHWAccelRunner.
	Runner HWAccelRunner
	// GOOS overrides the platform the candidate set is chosen for. "" uses
	// runtime.GOOS. Tests set it to exercise the macOS vs Linux split without
	// the host's real platform leaking in.
	GOOS string
	// DRIPresent reports whether /dev/dri exists (the Intel passthrough that
	// QSV and VAAPI require). nil uses the real filesystem check. When it
	// reports false, QSV and VAAPI are skipped entirely — not even attempted —
	// so a box without GPU passthrough degrades cleanly to NVENC/software.
	DRIPresent func() bool
	// PerProbeTimeout time-boxes each candidate's encode (ADR-0013 §4: a
	// ~5-second probe). Zero uses defaultHWAccelProbeTimeout.
	PerProbeTimeout time.Duration
	// TotalTimeout bounds the entire hardware probe run by construction.
	// Candidates probe concurrently, so zero uses one candidate chain's worth
	// of per-probe budgets (hwAccelProbeChainLength × PerProbeTimeout): the
	// default startup probe costs about that much however many accelerators
	// are attempted.
	TotalTimeout time.Duration
	// ConfiguredPreference is the operator-selected accelerator, when the stored
	// preference exactly matches the closed vocabulary. Failed probes for this
	// accelerator report configured-but-failed instead of unavailable.
	ConfiguredPreference HWAccel
}

// defaultHWAccelProbeTimeout is the startup probe budget (ADR-0013 §4: the
// "5-second probe transcode"). A hung or absent encoder is abandoned here and
// reported as not-working rather than blocking startup.
const defaultHWAccelProbeTimeout = 5 * time.Second

// HWAccelResult is what the probe verified: the accelerators whose trivial
// encode actually succeeded, in closed-vocabulary order. Software transcode is always
// available and is never listed here (it is the implicit fallback).
type HWAccelResult struct {
	// Results has one entry per known accelerator, in closed-vocabulary order.
	Results []HWAccelProbe
	// Working lists the accelerators whose probe encode succeeded, in
	// closed-vocabulary order. Empty means software-only — a normal, non-error
	// outcome on a box with no usable hardware encoder.
	Working []HWAccel
}

const (
	softwareTranscodeCap = 1
	hardwareTranscodeCap = 4
)

// Supports reports whether the probe verified the given accelerator works.
func (r HWAccelResult) Supports(a HWAccel) bool {
	for _, probe := range r.Results {
		if probe.Accelerator == a && probe.Status == HWAccelWorking {
			return true
		}
	}
	return false
}

// SupportsHEVC reports whether the probe verified the given accelerator's HEVC
// encoder works.
func (r HWAccelResult) SupportsHEVC(a HWAccel) bool {
	for _, probe := range r.Results {
		if probe.Accelerator == a && probe.Status == HWAccelWorking && probe.HEVC {
			return true
		}
	}
	return false
}

// SoftwareOnly reports whether no hardware accelerator was verified working, so
// the server will fall back to software (libx264) transcode. Not an error —
// software is always available.
func (r HWAccelResult) SoftwareOnly() bool {
	return len(r.Working) == 0
}

// SelectHWAccel picks the hardware encoder realtime/background HLS should use.
// The configured preference wins only when the probe verified it; otherwise the
// first verified accelerator is used, with software as the empty fallback.
func SelectHWAccel(result HWAccelResult, preferred HWAccel) HWAccel {
	if preferred != "" && result.Supports(preferred) {
		return preferred
	}
	for _, accel := range result.Working {
		if _, ok := hwAccelEncoder[accel]; ok {
			return accel
		}
	}
	return ""
}

// TranscodeCapForHWAccel derives realtime admission capacity from the verified
// encoder path: software gets one slot; hardware keeps the existing four-slot
// policy.
func TranscodeCapForHWAccel(result HWAccelResult) int {
	if SelectHWAccel(result, "") == "" {
		return softwareTranscodeCap
	}
	return hardwareTranscodeCap
}

// hwAccelCandidates returns the accelerators worth probing on goos, gated by
// /dev/dri presence for the Intel passthrough path. The order is the report
// order; it does not imply a selection preference.
func hwAccelCandidates(goos string, driPresent bool) []HWAccel {
	switch goos {
	case "darwin":
		return []HWAccel{HWAccelVideoToolbox}
	case "linux":
		var c []HWAccel
		if driPresent {
			// QSV and VAAPI both ride /dev/dri; without it neither device node
			// exists, so attempting them only wastes a probe and logs a scary
			// failure. Skip them entirely (graceful degradation).
			c = append(c, HWAccelQSV, HWAccelVAAPI)
		}
		// NVENC uses the NVIDIA runtime, not /dev/dri, so it is always a
		// candidate on Linux; its probe encode is what tells us whether a GPU
		// is actually present.
		c = append(c, HWAccelNVENC)
		return c
	default:
		// Other platforms (e.g. Windows) advertise no first-class accelerator
		// in v1: probe nothing, fall back to software.
		return nil
	}
}

// ProbeHardwareAccel test-drives each platform-appropriate accelerator with a
// short, time-boxed trivial encode and returns the ones that worked (ADR-0013
// §4). It never returns an error: a candidate that fails (no device, no driver,
// timeout) is simply omitted, and an empty result means software-only — both
// normal outcomes.
//
// Candidates are probed concurrently, each on its own chain of time-boxed
// encodes, so one hung accelerator cannot starve the others: a QSV probe that
// wedges until its PerProbeTimeout leaves VAAPI and NVENC their full budgets
// (CORE-2090). Within a candidate the encodes stay sequential, because HEVC is
// only worth probing once H.264 passed (CORE-2088). The context and
// TotalTimeout bound the whole run, and the report keeps closed-vocabulary
// order whatever order the probes finish in.
func ProbeHardwareAccel(ctx context.Context, opts HWAccelOptions) HWAccelResult {
	timeout := opts.PerProbeTimeout
	if timeout <= 0 {
		timeout = defaultHWAccelProbeTimeout
	}
	totalTimeout := opts.TotalTimeout
	if totalTimeout <= 0 {
		totalTimeout = hwAccelProbeChainLength * timeout
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	driPresent := opts.DRIPresent
	if driPresent == nil {
		driPresent = devDRIPresent
	}
	runner := opts.Runner
	if runner == nil {
		runner = DefaultHWAccelRunner
	}

	candidates := hwAccelCandidates(goos, driPresent())
	probes := make([]HWAccelProbe, len(allHWAccels))
	var wg sync.WaitGroup
	for i, candidate := range allHWAccels {
		probes[i] = HWAccelProbe{Accelerator: candidate, Status: HWAccelUnavailable}
		if !slices.Contains(candidates, candidate) || ctx.Err() != nil {
			continue
		}
		wg.Go(func() {
			probes[i] = probeHWAccelCandidate(ctx, runner, candidate, timeout, opts.ConfiguredPreference == candidate)
		})
	}
	wg.Wait()

	result := HWAccelResult{Results: probes}
	for _, probe := range probes {
		if probe.Status == HWAccelWorking {
			result.Working = append(result.Working, probe.Accelerator)
		}
	}
	return result
}

// hwAccelProbeChainLength is the most encodes one candidate's chain runs in
// sequence (H.264, then HEVC). Candidates run concurrently, so the default
// TotalTimeout is one chain's worth of per-probe budgets: enough for the
// slowest candidate to finish, and still a fixed bound on boot.
const hwAccelProbeChainLength = 2

// probeHWAccelCandidate runs one accelerator's chain: the H.264 encode decides
// whether the accelerator works at all, and only then is its HEVC encoder
// probed.
func probeHWAccelCandidate(ctx context.Context, runner HWAccelRunner, candidate HWAccel, timeout time.Duration, configured bool) HWAccelProbe {
	probe := HWAccelProbe{Accelerator: candidate, Status: HWAccelUnavailable}
	if err := probeOneHWAccel(ctx, runner, hwAccelEncoder[candidate], timeout); err != nil {
		if configured {
			probe.Status = HWAccelConfiguredFailed
		}
		return probe
	}
	probe.Status = HWAccelWorking
	probe.HEVC = ctx.Err() == nil && probeOneHWAccel(ctx, runner, hwAccelHEVCEncoder[candidate], timeout) == nil
	return probe
}

func probeOneHWAccel(ctx context.Context, runner HWAccelRunner, encoder string, timeout time.Duration) error {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return runner(probeCtx, encoder)
}

func init() {
	for _, encoders := range []map[HWAccel]string{hwAccelEncoder, hwAccelHEVCEncoder} {
		for accel, encoder := range encoders {
			if !slices.Contains(allHWAccels, accel) {
				panic("ffmpeg: hwAccelEncoder contains accelerator outside closed vocabulary")
			}
			if encoder == "" {
				panic("ffmpeg: hwAccelEncoder contains empty encoder")
			}
		}
	}
}

// devDRIPresent reports whether /dev/dri exists — the render/card device tree
// QSV and VAAPI need. Absent on a host without GPU passthrough (and on
// non-Linux), in which case the Intel accelerators are not even attempted.
func devDRIPresent() bool {
	info, err := os.Stat("/dev/dri")
	return err == nil && info.IsDir()
}

// DefaultHWAccelRunner is the production HWAccelRunner: it runs a trivial
// time-boxed ffmpeg encode against the given encoder over a synthetic 1-frame
// source (testsrc → null muxer) so success means the full driver+device+build
// path works, with no real media file. ctx (already time-boxed by the probe)
// bounds the run. A missing ffmpeg binary or any non-zero exit reports the
// accelerator as not working.
func DefaultHWAccelRunner(ctx context.Context, encoder string) error {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return err
	}
	args := hwAccelProbeArgs(encoder)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	// Under the bounded supervisor (tree kill + reap on the probe's time-box)
	// with output discarded: the probe's verdict is the exit status alone.
	return supervise(ctx, cmd, io.Discard, io.Discard)
}

func hwAccelProbeArgs(encoder string) []string {
	// A 1-frame synthetic source encoded to the null muxer: the cheapest way to
	// force ffmpeg to initialize the hardware encoder. -v error keeps the probe
	// quiet; a real failure still returns a non-zero exit.
	args := []string{
		"-v", "error",
		"-f", "lavfi",
		"-i", "testsrc=duration=0.1:size=128x128:rate=10",
		"-frames:v", "1",
	}
	if encoder == hwAccelEncoder[HWAccelVAAPI] || encoder == hwAccelHEVCEncoder[HWAccelVAAPI] {
		args = []string{
			"-v", "error",
			"-vaapi_device", vaapiDevicePath(),
			"-f", "lavfi",
			"-i", "testsrc=duration=0.1:size=128x128:rate=10",
			"-frames:v", "1",
			"-vf", "format=nv12,hwupload",
		}
	}
	args = append(args,
		"-c:v", encoder,
		"-f", "null",
		"-",
	)
	return args
}

func vaapiDevicePath() string {
	entries, err := os.ReadDir("/dev/dri")
	if err != nil {
		return "/dev/dri/renderD128"
	}
	renderNodes := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "renderD") {
			renderNodes = append(renderNodes, filepath.Join("/dev/dri", entry.Name()))
		}
	}
	if len(renderNodes) == 0 {
		return "/dev/dri/renderD128"
	}
	slices.Sort(renderNodes)
	return renderNodes[0]
}

package ffmpeg

// Tone-map capability probe (ADR-0075 §3, ticket FFMPEG-TONEMAP-BT2390-INVALID-1).
//
// Beamfall trusts a host for HDR→SDR tone-mapping only after the FFmpeg binary
// this process actually resolves has EXECUTED Beamfall's own tone-map chain over
// a synthetic BT.2020/PQ source and returned a frame whose luma proves a real
// dynamic-range transform happened. Presence of a filter in `-filters`, a
// version string, an operator setting, and a previous boot's result are each
// individually and jointly insufficient — that is exactly how the shipped recipe
// came to name `tonemap=bt2390`, an option the software `tonemap` filter has
// never accepted, without anything noticing.
//
// Like the hardware-accel probe, the command runner is injected (the
// ToneMapRunner seam) so every branch is testable with a stub and no real
// FFmpeg. No untrusted input ever reaches this code: the source is synthetic
// lavfi, so a hostile media file cannot steer the probe. There is no persistent
// cache — a cache key that cannot enumerate its own invalidators (binary path,
// build configuration, in-place package upgrade) is a latent wrong answer, and
// at ~14 ms per render there is nothing to amortize.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Refusal reasons for a video re-encode Beamfall will not attempt. They are the
// wire-visible `reason` playbackInfo reports, so the ffmpeg refusal and the
// playback refusal can never drift apart.
const (
	// ToneMapRefusalDoViP5 is Dolby Vision profile 5: a single-layer ICtCp
	// stream with no BT.2020 base layer to tone-map at all.
	ToneMapRefusalDoViP5 = "dolby-vision-p5-transcode-unavailable"
	// ToneMapRefusalUnverified is an HDR source on a build that has not proven
	// it can run the tone-map chain. Refusing is the safe direction: the only
	// alternatives are an FFmpeg that dies at filter-graph parse (the shipped
	// bug) or a washed-out, wrong-rendition encode.
	ToneMapRefusalUnverified = "hdr-tone-map-unverified"
)

// ToneMapStatus is what the probe actually proved about the resolved binary.
type ToneMapStatus string

const (
	ToneMapUnsupported ToneMapStatus = "unsupported"
	ToneMapWorking     ToneMapStatus = "working"
)

// ToneMapFailure is the closed classification vocabulary for a failed probe. It
// never carries FFmpeg stderr or a local path (the HWAccelProbe discipline), so
// it is safe to surface in the Console and in a support bundle.
type ToneMapFailure string

const (
	ToneMapFailureNone ToneMapFailure = ""
	// ToneMapFailureBinaryMissing — no ffmpeg on PATH.
	ToneMapFailureBinaryMissing ToneMapFailure = "binary-missing"
	// ToneMapFailureChainUnrunnable — the build rejected the chain: a missing
	// filter or an option value it does not accept. This is the class the
	// shipped `tonemap=bt2390` recipe fell into on every platform.
	ToneMapFailureChainUnrunnable ToneMapFailure = "chain-unrunnable"
	// ToneMapFailureTimeout — a wedged binary or driver; the tree was killed.
	ToneMapFailureTimeout ToneMapFailure = "timeout"
	// ToneMapFailureNoFrame — ran, but did not produce one whole probe frame.
	ToneMapFailureNoFrame ToneMapFailure = "no-frame"
	// ToneMapFailureInconclusive — the untone-mapped baseline render itself
	// failed, so the harness proves nothing. Never a PASS.
	ToneMapFailureInconclusive ToneMapFailure = "inconclusive"
	// ToneMapFailureNoTransform — the chain ran but left the highlight where it
	// found it: an identity/pass-through. This IS the washed-out failure, and
	// catching it is why the probe is differential rather than exit-status-only.
	ToneMapFailureNoTransform ToneMapFailure = "no-transform"
	// ToneMapFailureCrushed — the chain ran but drove the highlight into
	// darkness: a wrong-direction transform, a correctness failure.
	ToneMapFailureCrushed ToneMapFailure = "crushed"
	// ToneMapFailureUnavailable — the backend's device is absent. It is
	// DETECTED by a pre-check before any render, never inferred from a failed
	// one, because the error classifier below can only produce binary-missing,
	// timeout, and chain-unrunnable — so an absent device would otherwise be
	// reported as a broken chain. The
	// spelling deliberately REUSES the existing HWAccelUnavailable value rather
	// than minting a second synonym for the same condition.
	ToneMapFailureUnavailable = ToneMapFailure(HWAccelUnavailable)
)

// TransferClass is the closed vocabulary of source transfer classes that need
// an HDR→SDR transform. Certification is per (backend, transfer class): a
// hardware backend proven on PQ has proven nothing about HLG (ADR-0075
// Amendment 2026-07-26 D3).
type TransferClass string

const (
	// TransferClassPQ is PQ/HDR10 (smpte2084).
	TransferClassPQ TransferClass = "pq"
	// TransferClassHLG is Hybrid Log-Gamma (arib-std-b67).
	TransferClassHLG TransferClass = "hlg"
	// TransferClassDoVi78 is Dolby Vision profiles 7 and 8, whose tone-map path
	// is the software chain over the HDR10-compatible PQ base layer.
	TransferClassDoVi78 TransferClass = "dovi78"
)

// ToneMapTransferClasses is the closed vocabulary in declaration order.
var ToneMapTransferClasses = []TransferClass{TransferClassPQ, TransferClassHLG, TransferClassDoVi78}

// TransferClassFor classifies a source's transfer characteristics exactly as
// Rendition.needsToneMap and VideoSignal.NeedsToneMap do — Dolby Vision 7/8
// first, then the transfer characteristic — so the certification key can never
// disagree with the runtime predicate about which class a source is in.
func TransferClassFor(colorTransfer string, doViProfile int) (TransferClass, bool) {
	switch {
	case doViProfile == 7 || doViProfile == 8:
		return TransferClassDoVi78, true
	case colorTransfer == "smpte2084":
		return TransferClassPQ, true
	case colorTransfer == "arib-std-b67":
		return TransferClassHLG, true
	default:
		return "", false
	}
}

// HardwareCertifiableClass reports whether a hardware row may exist for this
// class at all. Dolby Vision 7/8 cannot be synthesised by lavfi — the
// dual-layer/RPU shape is not expressible as a filter-graph source — so under
// the DOVI_78_FIXTURE fail-closed default it has NO hardware row and continues
// to tone-map through the software chain on its PQ base layer, exactly as it
// does today. This gates the hardware-GA claim only; it never causes a runtime
// refusal.
func HardwareCertifiableClass(class TransferClass) bool {
	return class == TransferClassPQ || class == TransferClassHLG
}

// PassthroughControlFailedAsRequired is the only value a hardware evidence row
// may record for its pass-through control:
// the control is mandatory for EVERY hardware row regardless of threshold
// origin, and it must FAIL. There is deliberately no "not-applicable".
const PassthroughControlFailedAsRequired = "failed-as-required" //nolint:gosec // ADR-0075 D7 verdict spelling; not a credential.

// ToneMapProbe is the sanitized verdict for one (backend, transfer class) pair
// on the resolved FFmpeg binary. It carries the measurements the evidence row
// records (D6) and never carries FFmpeg stderr, a media path, or a device
// serial, so it stays support-bundle-safe.
type ToneMapProbe struct {
	Status  ToneMapStatus
	Failure ToneMapFailure
	// Backend is the accelerator probed; "" is the software chain.
	Backend HWAccel
	// Class is the transfer class the fixture exercised.
	Class TransferClass
	// BaselineHighlight is the differential baseline's highlight-band mean. For
	// a hardware probe that is the pass-through control leg — the same device
	// topology minus the tone-map filter — which is what makes the pair
	// symmetric by construction (D2).
	BaselineHighlight float64
	// ToneMappedHighlight is the candidate chain's highlight-band mean.
	ToneMappedHighlight float64
	// Thresholds are the floors the verdict was taken against.
	Thresholds ToneMapThresholds
	// PassthroughControl is PassthroughControlFailedAsRequired on a hardware
	// probe whose control leg failed the floors as it must, and "" on a
	// software probe (software rows are not hardware rows).
	PassthroughControl string
}

// ToneMapRender is one probe render: the device initialisation and the filter
// legs to insert between the synthetic HDR source and the gray readback. A
// hardware chain needs pre-input arguments, which a filters-only seam could not
// carry, so the seam names the whole render.
type ToneMapRender struct {
	// InitArgs are emitted before -i.
	InitArgs []string
	// Filters are inserted between the source and the readback. Empty renders
	// the untone-mapped baseline.
	Filters []string
	// Source is the synthetic lavfi source chain for this transfer class.
	Source string
}

// ToneMapRunner renders one probe frame, returning the raw single-plane gray
// frame. The injected seam — a stub drives every branch of ProbeToneMap with no
// real FFmpeg and no real hardware.
type ToneMapRunner func(ctx context.Context, render ToneMapRender) ([]byte, error)

// ToneMapOptions configures a probe run. The zero value is a valid production
// software probe against the real resolved binary over the PQ fixture.
type ToneMapOptions struct {
	// Runner renders one probe frame. nil uses DefaultToneMapRunner.
	Runner ToneMapRunner
	// Backend is the accelerator to certify. "" probes the software chain.
	Backend HWAccel
	// Class is the transfer class to probe. "" means TransferClassPQ.
	Class TransferClass
	// Thresholds overrides the floors with a calibrated set (D2). nil inherits
	// the absolute software constants, which is only valid for a symmetric
	// pair — and the probe builds one by construction.
	Thresholds *ToneMapThresholds
	// GOOS overrides the platform the device pre-check is taken for. "" uses
	// runtime.GOOS.
	GOOS string
	// DevicePresent overrides the D7 device pre-check. nil uses the real one.
	DevicePresent func(backend HWAccel, goos string) bool
	// PerRenderTimeout time-boxes each render. Zero uses the default.
	PerRenderTimeout time.Duration
	// TotalTimeout bounds the whole probe by construction. Zero uses the default.
	TotalTimeout time.Duration
}

const (
	// toneMapProbeInput is a 16x16 synthetic frame: the work is finite by
	// construction, independent of any timeout.
	toneMapProbeInput = "nullsrc=s=16x16:r=1:d=0.1"
	// toneMapProbeSource tags that frame as BT.2020/PQ and gives it BOTH halves
	// of what the chain transforms:
	//
	//   - a luminance ramp across X, codeword 300 → 726, which in limited-range
	//     10-bit PQ is ~2.5 → ~1000 cd/m². The top end is an order of magnitude
	//     above the 100-nit SDR reference, where a tone-mapped and an
	//     untone-mapped rendering diverge maximally; the rest is real midtones,
	//     so the frame has contrast to preserve rather than one flat codeword.
	//   - saturated, hue-rotating chroma across Y (cb/cr swept ±350 around
	//     neutral), so `desat` and the BT.2020→BT.709 primaries conversion do
	//     real work. A chroma-neutral patch makes both of them no-ops: measured
	//     on the pinned build, the previous flat cb=512:cr=512 source rendered
	//     BYTE-IDENTICAL (mean 254.703) under desat=0, desat=4, p=bt2020 and
	//     m=bt2020, so nothing here had ever observed the colour half of the
	//     chain working. With this source those four diverge (100.273, 103.809,
	//     121.152, 102.473).
	//
	// The expressions deliberately contain no ':' or ',', so they need no
	// FFmpeg-level quoting inside the filter graph.
	toneMapProbeSource = "format=yuv420p10le," +
		"geq=lum=300+426*X/W:cb=512+350*(Y/H-0.5):cr=512-350*(Y/H-0.5)," +
		"setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc"
	// toneMapProbeSourceHLG is the HLG half of the launch fixture set: the same
	// synthetic in-graph source built the same way, tagged arib-std-b67 instead
	// of smpte2084. Both fixtures are
	// synthetic — no media file is committed or downloaded.
	toneMapProbeSourceHLG = "format=yuv420p10le," +
		"geq=lum=300+426*X/W:cb=512+350*(Y/H-0.5):cr=512-350*(Y/H-0.5)," +
		"setparams=color_primaries=bt2020:color_trc=arib-std-b67:colorspace=bt2020nc"
	// toneMapProbeReadback ends BOTH renders identically, so any range/primary
	// handling in the readback cancels out and no build-specific golden value
	// is needed — only the difference between the two renders is read.
	toneMapProbeReadback = "format=gray"
	// toneMapProbeWidth/toneMapProbeHeight are the probe frame's dimensions.
	toneMapProbeWidth  = 16
	toneMapProbeHeight = 16
	// toneMapProbeFrameBytes is one 16x16 8-bit gray plane.
	toneMapProbeFrameBytes = toneMapProbeWidth * toneMapProbeHeight
	// toneMapHighlightColumns is the width of the highlight band: the rightmost
	// quarter of the ramp, i.e. its ~1000-nit end. Both floors below read that
	// band rather than the whole frame, because with a ramp the midtones dilute
	// exactly the signal being measured.
	toneMapHighlightColumns = 4
	// toneMapMinHighlightDrop is how far (of 255) the tone-map must pull the
	// 1000-nit highlight band DOWN from the untone-mapped baseline. Compressing
	// a 1000-nit highlight into a 100-nit SDR reference is a fall, not the rise
	// the old flat-patch source measured — that rise was an artifact of a
	// limited→full range expansion on a single codeword, not of tone-mapping.
	// Measured on the pinned jellyfin-ffmpeg 7.1.4-3 build: baseline highlight
	// band 173.125, tone-mapped 122.859, so the real drop is 50.3 and the
	// margin over this floor is ~1.6x. A pass-through (tonemap=none) drops it
	// only 2.0, i.e. 16x BELOW the floor, so the two are cleanly separated.
	toneMapMinHighlightDrop = 32
	// toneMapMinHighlight is the floor the tone-mapped highlight band must still
	// clear, so a chain that runs but drives the picture into darkness is not
	// mistaken for working. Measured 122.859 on the pinned build, a ~1.9x margin.
	toneMapMinHighlight = 64

	defaultToneMapRenderTimeout = 5 * time.Second
	defaultToneMapProbeTimeout  = 12 * time.Second
	// defaultToneMapHardwareProbeTimeout covers a hardware probe's THREE legs
	// (candidate, pass-through control, plain baseline) plus device
	// initialisation, which the two-leg software budget does not.
	defaultToneMapHardwareProbeTimeout = 20 * time.Second
)

// ThresholdOrigin records how a row's floors were derived (ADR-0075 Amendment
// 2026-07-26 D6).
type ThresholdOrigin string

const (
	// ThresholdOriginInherited means the absolute software constants were used
	// unchanged. Valid only for a symmetric pair.
	ThresholdOriginInherited ThresholdOrigin = "inherited"
	// ThresholdOriginCalibrated means the floors were derived from a software
	// reference leg under D2's arithmetic.
	ThresholdOriginCalibrated ThresholdOrigin = "calibrated"
)

// CalibrationMarginFloor is the lower bound on CALIBRATION_MARGIN. It matters
// independently of the max(…) clamp below: it stops a future calibration from
// drifting the RELATIVE margin toward zero on a host where the software
// reference measures high.
const CalibrationMarginFloor = 0.5

// ToneMapThresholds are the floors a probe verdict is taken against.
type ToneMapThresholds struct {
	MinHighlightDrop float64
	MinHighlight     float64
	Origin           ThresholdOrigin
}

// InheritedToneMapThresholds are the absolute software constants. They are an
// inviolable lower bound on BOTH dimensions: calibration may only ever raise a
// floor, never lower one.
func InheritedToneMapThresholds() ToneMapThresholds {
	return ToneMapThresholds{
		MinHighlightDrop: toneMapMinHighlightDrop,
		MinHighlight:     toneMapMinHighlight,
		Origin:           ThresholdOriginInherited,
	}
}

// CalibrateToneMapThresholds derives a backend's floors from the SOFTWARE
// REFERENCE leg — never from the candidate under test, which is the hole this
// rule closes: a crushing backend measuring highlight 30 would otherwise set
// its own floor to 15 and self-certify.
//
//	min_highlight_drop = max( 32, m × (baseline_highlight − software_ref_highlight) )
//	min_highlight      = max( 64, m × software_ref_highlight )
//
// The run is valid only if the software reference leg ITSELF passes the
// absolute thresholds on this host and fixture. Without that anchor a degraded
// local FFmpeg — and on darwin the reference is unpinned, resolved through
// exec.LookPath — would scale every derived floor down together and quietly
// certify a bad backend against a bad reference.
func CalibrateToneMapThresholds(baselineHighlight, softwareRefHighlight, margin float64) (ToneMapThresholds, error) {
	if margin < CalibrationMarginFloor {
		return ToneMapThresholds{}, fmt.Errorf("ffmpeg: calibration margin %g is below the floor %g", margin, CalibrationMarginFloor)
	}
	referenceDrop := baselineHighlight - softwareRefHighlight
	if referenceDrop < toneMapMinHighlightDrop {
		return ToneMapThresholds{}, fmt.Errorf(
			"ffmpeg: software reference leg dropped the highlight only %.3f (need >= %d): the reference is not "+
				"trustworthy, so every floor derived from it would be scaled down with it", referenceDrop, toneMapMinHighlightDrop)
	}
	if softwareRefHighlight < toneMapMinHighlight {
		return ToneMapThresholds{}, fmt.Errorf(
			"ffmpeg: software reference leg landed the highlight at %.3f (need >= %d): the reference itself fails "+
				"the absolute thresholds, so this calibration run is invalid", softwareRefHighlight, toneMapMinHighlight)
	}
	return ToneMapThresholds{
		MinHighlightDrop: math.Max(toneMapMinHighlightDrop, margin*referenceDrop),
		MinHighlight:     math.Max(toneMapMinHighlight, margin*softwareRefHighlight),
		Origin:           ThresholdOriginCalibrated,
	}, nil
}

// ProbeToneMap renders Beamfall's production tone-map chain and an untone-mapped
// baseline over the same synthetic PQ source and returns what that proved. It
// never returns an error: every failure resolves to ToneMapUnsupported with a
// classification, because "not yet proven" and "proven not to work" are both
// "do not use". There is no branch that resolves to "assume supported".
func ProbeToneMap(ctx context.Context, opts ToneMapOptions) ToneMapProbe {
	run, ready := newToneMapProbeRun(opts)
	if !ready.working() {
		return ready
	}
	ctx, cancel := context.WithTimeout(ctx, run.total)
	defer cancel()
	if run.backend == "" {
		return run.probeSoftware(ctx)
	}
	return run.probeHardware(ctx)
}

// toneMapProbeRun is one probe's resolved configuration: every default applied
// once, so no branch below re-derives one.
type toneMapProbeRun struct {
	runner     ToneMapRunner
	backend    HWAccel
	class      TransferClass
	source     string
	thresholds ToneMapThresholds
	perRender  time.Duration
	total      time.Duration
}

// newToneMapProbeRun resolves the run, or returns the fail-closed verdict for a
// request that can never produce evidence: an unknown transfer class, a class
// with no synthesisable fixture, an absent device, or an unknown backend.
func newToneMapProbeRun(opts ToneMapOptions) (toneMapProbeRun, ToneMapProbe) {
	class := opts.Class
	if class == "" {
		class = TransferClassPQ
	}
	run := toneMapProbeRun{
		runner:     opts.Runner,
		backend:    opts.Backend,
		class:      class,
		thresholds: InheritedToneMapThresholds(),
		perRender:  opts.PerRenderTimeout,
		total:      opts.TotalTimeout,
	}
	if run.runner == nil {
		run.runner = DefaultToneMapRunner
	}
	if opts.Thresholds != nil {
		run.thresholds = *opts.Thresholds
	}
	if run.perRender <= 0 {
		run.perRender = defaultToneMapRenderTimeout
	}
	if run.total <= 0 {
		run.total = defaultToneMapProbeTimeout
		if run.backend != "" {
			run.total = defaultToneMapHardwareProbeTimeout
		}
	}
	source, synthesisable := toneMapProbeSourceFor(class)
	if !synthesisable {
		// No fixture means no measurement, so the harness proves nothing.
		return run, run.unsupported(ToneMapFailureInconclusive)
	}
	run.source = source
	if run.backend == "" {
		return run, run.working()
	}
	if !toneMapDevicePresent(run.backend, opts.GOOS, opts.DevicePresent) {
		return run, run.unsupported(ToneMapFailureUnavailable)
	}
	if len(BuildToneMapChain(ToneMapChainRequest{Backend: run.backend, Download: true}).Filters) == 0 {
		return run, run.unsupported(ToneMapFailureChainUnrunnable)
	}
	return run, run.working()
}

// toneMapProbeSourceFor selects the synthetic in-graph fixture for a class.
func toneMapProbeSourceFor(class TransferClass) (string, bool) {
	switch class {
	case TransferClassPQ:
		return toneMapProbeSource, true
	case TransferClassHLG:
		return toneMapProbeSourceHLG, true
	default:
		// Dolby Vision 7/8 cannot be synthesised by lavfi (DOVI_78_FIXTURE).
		return "", false
	}
}

// probeSoftware is the original two-leg differential, unchanged in behaviour:
// the production software chain against an untone-mapped baseline. Its verdict
// is a SOFTWARE row and may never be recorded, counted, or displayed as a
// hardware row (ADR-0075 Amendment 2026-07-26 D1b).
func (run toneMapProbeRun) probeSoftware(ctx context.Context) ToneMapProbe {
	chain := BuildToneMapChain(ToneMapChainRequest{})
	toneMapped, failure := run.render(ctx, chain)
	if failure != ToneMapFailureNone {
		return run.unsupported(failure)
	}
	baseline, failure := run.render(ctx, ToneMapChain{})
	if failure != ToneMapFailureNone {
		// The harness itself is unsound, so the tone-mapped render proves
		// nothing. Reported distinctly, decided identically.
		return run.unsupported(ToneMapFailureInconclusive)
	}
	return run.verdict(highlightSample(baseline), highlightSample(toneMapped), "")
}

// probeHardware is the three-leg differential D2 requires. The candidate is
// measured against the PASS-THROUGH CONTROL — the same device-init, upload and
// download topology with only the tone-map filter removed — which is what makes
// the pair symmetric by construction and the inherited constants valid. The
// control is then itself measured against a plain readback and MUST fail the
// floors: that is what proves the drop came from the tone-map filter rather
// than from the device round-trip around it.
func (run toneMapProbeRun) probeHardware(ctx context.Context) ToneMapProbe {
	chain := BuildToneMapChain(ToneMapChainRequest{Backend: run.backend, Download: true})
	candidate, failure := run.render(ctx, chain)
	if failure != ToneMapFailureNone {
		return run.unsupported(failure)
	}
	control, failure := run.render(ctx, toneMapChainWithoutTransform(chain))
	if failure != ToneMapFailureNone {
		return run.unsupported(ToneMapFailureInconclusive)
	}
	plain, failure := run.render(ctx, ToneMapChain{})
	if failure != ToneMapFailureNone {
		return run.unsupported(ToneMapFailureInconclusive)
	}
	if run.passes(highlightSample(plain), highlightSample(control)) {
		// The topology alone clears the floors, so a no-op tone-map would
		// certify. The run is void, not a pass.
		return run.unsupported(ToneMapFailureInconclusive)
	}
	return run.verdict(highlightSample(control), highlightSample(candidate), PassthroughControlFailedAsRequired)
}

// ToneMapCalibration is one calibration run's measurements and the floors
// derived from them. `SoftwareRefHighlight` is the number a calibrated evidence
// row records; an inherited row records null there, because it measures no
// software reference leg at all.
type ToneMapCalibration struct {
	BaselineHighlight    float64
	SoftwareRefHighlight float64
	Margin               float64
	Thresholds           ToneMapThresholds
}

// RunToneMapCalibration measures the two reference legs D2 names — the
// untone-mapped baseline and the SOFTWARE tone-mapped reference — over this
// class's fixture on the calibration host, and derives the candidate's floors
// from them. The candidate's own measurement never enters the arithmetic.
//
// The run fails rather than returning loose floors when the software reference
// does not itself pass the absolute thresholds, so a degraded local FFmpeg
// cannot scale every derived floor down with it.
func RunToneMapCalibration(ctx context.Context, opts ToneMapOptions, margin float64) (ToneMapCalibration, error) {
	opts.Backend = ""
	run, ready := newToneMapProbeRun(opts)
	if !ready.working() {
		return ToneMapCalibration{}, fmt.Errorf("ffmpeg: calibration harness unusable: %s", ready.Failure)
	}
	ctx, cancel := context.WithTimeout(ctx, run.total)
	defer cancel()

	baseline, failure := run.render(ctx, ToneMapChain{})
	if failure != ToneMapFailureNone {
		return ToneMapCalibration{}, fmt.Errorf("ffmpeg: calibration baseline leg failed: %s", failure)
	}
	reference, failure := run.render(ctx, BuildToneMapChain(ToneMapChainRequest{}))
	if failure != ToneMapFailureNone {
		return ToneMapCalibration{}, fmt.Errorf("ffmpeg: calibration software reference leg failed: %s", failure)
	}
	calibration := ToneMapCalibration{
		BaselineHighlight:    highlightSample(baseline),
		SoftwareRefHighlight: highlightSample(reference),
		Margin:               margin,
	}
	thresholds, err := CalibrateToneMapThresholds(calibration.BaselineHighlight, calibration.SoftwareRefHighlight, margin)
	if err != nil {
		return ToneMapCalibration{}, err
	}
	calibration.Thresholds = thresholds
	return calibration, nil
}

// render runs one leg and returns its frame, or the closed-vocabulary failure.
func (run toneMapProbeRun) render(ctx context.Context, chain ToneMapChain) ([]byte, ToneMapFailure) {
	renderCtx, cancel := context.WithTimeout(ctx, run.perRender)
	defer cancel()
	frame, err := run.runner(renderCtx, ToneMapRender{
		InitArgs: chain.InitArgs,
		Filters:  chain.Filters,
		Source:   run.source,
	})
	if err != nil {
		return nil, classifyToneMapRenderError(err)
	}
	if len(frame) != toneMapProbeFrameBytes {
		return nil, ToneMapFailureNoFrame
	}
	return frame, ToneMapFailureNone
}

// passes reports whether a tone-mapped measurement clears both floors.
func (run toneMapProbeRun) passes(baselineHighlight, toneMappedHighlight float64) bool {
	if baselineHighlight-toneMappedHighlight < run.thresholds.MinHighlightDrop {
		return false
	}
	return toneMappedHighlight >= run.thresholds.MinHighlight
}

func (run toneMapProbeRun) verdict(baselineHighlight, toneMappedHighlight float64, control string) ToneMapProbe {
	probe := run.working()
	probe.BaselineHighlight = baselineHighlight
	probe.ToneMappedHighlight = toneMappedHighlight
	probe.PassthroughControl = control
	switch {
	case baselineHighlight-toneMappedHighlight < run.thresholds.MinHighlightDrop:
		probe.Status = ToneMapUnsupported
		probe.Failure = ToneMapFailureNoTransform
	case toneMappedHighlight < run.thresholds.MinHighlight:
		probe.Status = ToneMapUnsupported
		probe.Failure = ToneMapFailureCrushed
	}
	return probe
}

func (run toneMapProbeRun) working() ToneMapProbe {
	return ToneMapProbe{
		Status:     ToneMapWorking,
		Backend:    run.backend,
		Class:      run.class,
		Thresholds: run.thresholds,
	}
}

func (run toneMapProbeRun) unsupported(failure ToneMapFailure) ToneMapProbe {
	probe := run.working()
	probe.Status = ToneMapUnsupported
	probe.Failure = failure
	return probe
}

func (p ToneMapProbe) working() bool {
	return p.Status == ToneMapWorking
}

func classifyToneMapRenderError(err error) ToneMapFailure {
	if errors.Is(err, exec.ErrNotFound) {
		return ToneMapFailureBinaryMissing
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ToneMapFailureTimeout
	}
	return ToneMapFailureChainUnrunnable
}

// meanSample is the arithmetic mean over the whole plane. It is the frame-wide
// summary the execution test reports; the verdict itself reads the highlight
// band, where the transform being checked actually happens.
func meanSample(frame []byte) float64 {
	var total int
	for _, sample := range frame {
		total += int(sample)
	}
	return float64(total) / float64(len(frame))
}

// highlightSample is the arithmetic mean over the highlight band — the
// rightmost toneMapHighlightColumns of every row, i.e. the ~1000-nit end of the
// source ramp. Averaging the whole frame instead would dilute that signal with
// midtones the tone-map barely moves. It is still a whole-band mean, so a
// partially corrupt frame trips the floors just as a wholly wrong one does.
func highlightSample(frame []byte) float64 {
	var total, count int
	for row := 0; row < toneMapProbeHeight; row++ {
		for column := toneMapProbeWidth - toneMapHighlightColumns; column < toneMapProbeWidth; column++ {
			total += int(frame[row*toneMapProbeWidth+column])
			count++
		}
	}
	return float64(total) / float64(count)
}

// DefaultToneMapRunner is the production ToneMapRunner: one bounded, supervised,
// synthetic-source render whose single frame lands on stdout. It carries the
// least-privilege protocol allowlist and -nostdin like every other spawn, and
// runs under the bounded supervisor so a wedged child's tree is killed and
// reaped rather than orphaned.
func DefaultToneMapRunner(ctx context.Context, render ToneMapRender) ([]byte, error) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, err
	}
	// #nosec G204 -- path is resolved from PATH; the argv is this package's own
	// synthetic source plus the production filter chain, never operator input.
	cmd := exec.CommandContext(ctx, ffmpegPath, toneMapProbeArgs(render)...)
	frame := &boundedBuffer{limit: captureLimitBytes}
	if err := supervise(ctx, cmd, frame, io.Discard); err != nil {
		return nil, err
	}
	if frame.overflowed() {
		return nil, ErrOutputOverflow
	}
	return frame.bytes(), nil
}

// toneMapProbeArgs builds the render argv as a []string — never a shell string,
// which would reintroduce quoting and argv-injection hazards in a filter graph
// full of `=` and `:`. The chain's device-initialisation arguments lead the
// argv, because -init_hw_device must precede the input.
func toneMapProbeArgs(render ToneMapRender) []string {
	source := render.Source
	if source == "" {
		source = toneMapProbeSource
	}
	graph := make([]string, 0, len(render.Filters)+2)
	graph = append(graph, source)
	graph = append(graph, render.Filters...)
	graph = append(graph, toneMapProbeReadback)

	args := []string{"-hide_banner", "-nostdin", "-v", "error"}
	args = append(args, render.InitArgs...)
	args = append(args, "-f", "lavfi")
	args = append(args, inputProtocolArgs()...)
	args = append(args,
		"-i", toneMapProbeInput,
		"-frames:v", "1",
		"-vf", strings.Join(graph, ","),
		"-f", "rawvideo",
		"-",
	)
	return args
}

// toneMapDevicePresent is the D7 pre-check: an absent device is DETECTED before
// any render, following the pattern the encoder probe already uses — the
// existing /dev/dri check for the VAAPI/QSV family, the darwin-only rule for
// VideoToolbox, and the backend's own runtime presence for CUDA/NVENC.
func toneMapDevicePresent(backend HWAccel, goos string, override func(HWAccel, string) bool) bool {
	if goos == "" {
		goos = runtime.GOOS
	}
	if override != nil {
		return override(backend, goos)
	}
	switch backend {
	case HWAccelVideoToolbox:
		return goos == "darwin"
	case HWAccelQSV, HWAccelVAAPI:
		return goos == "linux" && devDRIPresent()
	case HWAccelNVENC:
		return goos == "linux" && nvidiaDevicePresent()
	default:
		return false
	}
}

// nvidiaDevicePresent reports whether the NVIDIA runtime's control device is
// present — the CUDA/NVENC equivalent of the /dev/dri check.
func nvidiaDevicePresent() bool {
	_, err := os.Stat("/dev/nvidiactl")
	return err == nil
}

// FFmpegIdentity is the resolved binary's identity (ADR-0075 Amendment
// 2026-07-26 D5). On darwin — where ffmpeg.lock carries no artifact and the
// binary is resolved through exec.LookPath — this IS the pin: sha256 of the
// binary plus the version banner it reports. It is populable today with no
// packaging change (DARWIN_FFMPEG_PIN).
type FFmpegIdentity struct {
	Path    string
	SHA256  string
	Version string
}

// String is the row value and the verdict-cache key component.
func (i FFmpegIdentity) String() string {
	return i.SHA256 + " " + i.Version
}

// resolvedIdentity memoizes the identity against a cheap stat, so the sha256 is
// recomputed only when the resolved binary actually changes underneath a
// running server — the BYO-FFmpeg swap D4 requires to invalidate a verdict.
var resolvedIdentity struct {
	sync.Mutex
	stat     string
	identity FFmpegIdentity
	err      error
}

// ResolveFFmpegIdentity returns the identity of the FFmpeg binary this process
// resolves right now.
//
//io:ignore — binary provenance read for the capability cache key, not a server I/O journey.
func ResolveFFmpegIdentity(ctx context.Context) (FFmpegIdentity, error) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return FFmpegIdentity{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return FFmpegIdentity{}, err
	}
	stat := fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano())

	resolvedIdentity.Lock()
	defer resolvedIdentity.Unlock()
	if resolvedIdentity.stat == stat {
		return resolvedIdentity.identity, resolvedIdentity.err
	}
	resolvedIdentity.stat = stat
	resolvedIdentity.identity, resolvedIdentity.err = readFFmpegIdentity(ctx, path)
	return resolvedIdentity.identity, resolvedIdentity.err
}

func readFFmpegIdentity(ctx context.Context, path string) (FFmpegIdentity, error) {
	// #nosec G304 -- path is the binary exec.LookPath just resolved.
	file, err := os.Open(path)
	if err != nil {
		return FFmpegIdentity{}, err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return FFmpegIdentity{}, err
	}
	version, err := ffmpegVersionBanner(ctx, path)
	if err != nil {
		return FFmpegIdentity{}, err
	}
	return FFmpegIdentity{Path: path, SHA256: hex.EncodeToString(digest.Sum(nil)), Version: version}, nil
}

func ffmpegVersionBanner(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultToneMapRenderTimeout)
	defer cancel()
	// #nosec G204 -- path is the resolved binary and the argument is fixed.
	cmd := exec.CommandContext(ctx, path, "-hide_banner", "-version")
	banner := &boundedBuffer{limit: captureLimitBytes}
	if err := supervise(ctx, cmd, banner, io.Discard); err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(string(banner.bytes()), "\n")
	return strings.TrimSpace(first), nil
}

// toneMapVerdictKey is D4's liveness key: a verdict is live only while
// (backend, transfer class, resolved FFmpeg binary identity) matches the
// current runtime. The sync.Once this replaced cached one verdict for the
// process lifetime with no binary identity in the key, so a BYO-FFmpeg swap
// under a running server kept serving a stale verdict.
type toneMapVerdictKey struct {
	backend  HWAccel
	class    TransferClass
	identity string
}

type toneMapVerdictEntry struct {
	once    sync.Once
	verdict ToneMapProbe
}

var (
	toneMapVerdictMu sync.Mutex
	toneMapVerdicts  = map[toneMapVerdictKey]*toneMapVerdictEntry{}
)

// ToneMapCapabilityFor returns this runtime's verdict for one (backend,
// transfer class) pair, probing on first use and keeping the result in memory
// only. There is no persistent runtime cache, so a stale on-disk verdict can
// never authorise a rendition; the committed
// evidence file is release-gate input, never a runtime input.
func ToneMapCapabilityFor(backend HWAccel, class TransferClass) ToneMapProbe {
	entry := toneMapVerdictEntryFor(toneMapVerdictKey{
		backend:  backend,
		class:    class,
		identity: currentFFmpegIdentityKey(),
	})
	entry.once.Do(func() {
		entry.verdict = ProbeToneMap(context.Background(), ToneMapOptions{Backend: backend, Class: class})
	})
	return entry.verdict
}

func toneMapVerdictEntryFor(key toneMapVerdictKey) *toneMapVerdictEntry {
	toneMapVerdictMu.Lock()
	defer toneMapVerdictMu.Unlock()
	entry, cached := toneMapVerdicts[key]
	if !cached {
		entry = &toneMapVerdictEntry{}
		toneMapVerdicts[key] = entry
	}
	return entry
}

// currentFFmpegIdentityKey never fails the caller: an unresolvable binary keys
// its own bucket, and the probe that runs under it fails closed anyway.
func currentFFmpegIdentityKey() string {
	identity, err := ResolveFFmpegIdentity(context.Background())
	if err != nil {
		return "unresolved"
	}
	return identity.String()
}

// ToneMapCapability returns this process's SOFTWARE tone-map verdict over the
// PQ fixture — the verdict that governs every class's refusal semantics,
// unchanged from what ships today.
func ToneMapCapability() ToneMapProbe {
	return ToneMapCapabilityFor("", TransferClassPQ)
}

// ToneMapBackendPreference is HW_BACKEND_PREFERENCE_ORDER: the preferred
// ordering among eligible hardware backends, aligned to the existing candidate
// order that hwAccelCandidates lists and SelectHWAccel walks. It never promotes
// software above an eligible hardware backend.
func ToneMapBackendPreference(goos string) []HWAccel {
	switch goos {
	case "darwin":
		return []HWAccel{HWAccelVideoToolbox}
	case "linux":
		return []HWAccel{HWAccelQSV, HWAccelVAAPI, HWAccelNVENC}
	default:
		return nil
	}
}

// toneMapHardwareEligible reports whether a backend is a VERIFIED hardware
// tone-map path for a class. It is a variable so a test can state the
// certification it means instead of probing the developer's host — the same
// injection discipline as HWAccelRunner.
var toneMapHardwareEligible = func(backend HWAccel, class TransferClass) bool {
	return ToneMapCapabilityFor(backend, class).Status == ToneMapWorking
}

// SelectToneMapBackend takes the FIRST eligible backend in the declared order,
// so verified hardware strictly precedes software (ADR-0075 Amendment
// 2026-07-26 D4). "" means no hardware backend is eligible for this class and
// the software chain is used — a fallback, never a refusal. A backend with any
// unsupported verdict is skipped rather than attempted, so a failed backend
// disables only itself.
func SelectToneMapBackend(class TransferClass, goos string) HWAccel {
	if !HardwareCertifiableClass(class) {
		return ""
	}
	for _, backend := range ToneMapBackendPreference(goos) {
		if toneMapHardwareEligible(backend, class) {
			return backend
		}
	}
	return ""
}

// selectRenditionToneMapBackend resolves the hardware tone-map backend for one
// rendition at spawn time. It is a variable so a test can pin the selection
// rather than probing whatever hardware the host happens to have.
var selectRenditionToneMapBackend = func(r Rendition) HWAccel {
	if r.CopyVideo || r.ToneMapUnavailable() || !r.needsToneMap() {
		return ""
	}
	class, classified := TransferClassFor(r.ColorTransfer, r.DoViProfile)
	if !classified {
		return ""
	}
	return SelectToneMapBackend(class, runtime.GOOS)
}

// ToneMapSupported reports whether the resolved FFmpeg binary has been PROVEN
// able to run hdrToSDRFilters(). Every caller that would otherwise emit the
// chain consults this; a false verdict refuses the rendition rather than
// spawning a command that cannot run.
//
//io:ignore — in-memory capability check after a one-shot probe, not a server I/O journey.
func ToneMapSupported() bool {
	return ToneMapCapability().Status == ToneMapWorking
}

// toneMapSupported is the capability gate Rendition.ToneMapRefusal consults. It
// is a variable so a test can state the build it means (conforming or not)
// rather than inheriting whichever FFmpeg happens to sit on the developer's
// PATH — the same injection discipline as HWAccelRunner.
var toneMapSupported = ToneMapSupported

package ffmpeg

// Hardware HDR tone-mapping: chain authoring, probe topology, calibration
// arithmetic, verdict liveness, and selection.
//
// Every test here runs with a stubbed runner and an injected device pre-check,
// so the whole surface is exercised on any host with no real hardware and no
// real FFmpeg. That is deliberate: the amendment's two RED-by-design items are
// the unassigned approver and the two-host physical floor, and neither may
// become a reason for this machinery to go untested.

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
)

// alwaysPresent is the injected device pre-check for a host that has the device.
func alwaysPresent(HWAccel, string) bool { return true }

// TestHardwareToneMapChainsAreStagedOnDevice pins the D1a topology: device
// initialisation before the input, an upload before the tone-map, a real
// hardware tone-map filter, and a download back to system memory.
func TestHardwareToneMapChainsAreStagedOnDevice(t *testing.T) {
	for _, backend := range allHWAccels {
		t.Run(string(backend), func(t *testing.T) {
			chain := BuildToneMapChain(ToneMapChainRequest{Backend: backend, DevicePath: "/dev/dri/renderD128", Download: true})
			if !chain.Hardware() {
				t.Fatalf("chain for %s is not marked hardware", backend)
			}
			if !slices.Contains(chain.InitArgs, "-filter_hw_device") && !slices.Contains(chain.InitArgs, "-init_hw_device") {
				t.Fatalf("%s chain initialises no device: %v", backend, chain.InitArgs)
			}
			upload := slices.Index(chain.Filters, "hwupload")
			download := slices.Index(chain.Filters, "hwdownload")
			toneMap := slices.IndexFunc(chain.Filters, func(leg string) bool {
				return strings.HasPrefix(leg, "tonemap_")
			})
			switch {
			case upload < 0:
				t.Fatalf("%s chain never uploads to the device: %v", backend, chain.Filters)
			case toneMap < 0:
				t.Fatalf("%s chain has no hardware tone-map filter: %v", backend, chain.Filters)
			case upload > toneMap:
				t.Fatalf("%s chain tone-maps before uploading: %v", backend, chain.Filters)
			case download < 0 || download < toneMap:
				t.Fatalf("%s chain does not download after the tone-map: %v", backend, chain.Filters)
			}
			// The software filter may never appear in a chain labelled hardware.
			for _, leg := range chain.Filters {
				if strings.HasPrefix(leg, "tonemapx") || leg == "tonemap" || strings.HasPrefix(leg, "tonemap=") {
					t.Fatalf("%s chain names the SOFTWARE tone-mapper %q under a hardware label", backend, leg)
				}
			}
		})
	}
}

// TestHardwareToneMapChainStaysOnDeviceWhenAsked proves the no-download form
// exists, so a backend whose encoder consumes device frames is expressible.
func TestHardwareToneMapChainStaysOnDeviceWhenAsked(t *testing.T) {
	chain := BuildToneMapChain(ToneMapChainRequest{Backend: HWAccelVAAPI, Download: false})
	if slices.Contains(chain.Filters, "hwdownload") {
		t.Fatalf("chain downloaded despite Download=false: %v", chain.Filters)
	}
	if !slices.Contains(chain.Filters, "hwupload") {
		t.Fatalf("chain did not upload: %v", chain.Filters)
	}
}

// TestUnknownToneMapBackendBuildsNothing is the fail-closed branch: an
// unrecognised accelerator must NOT quietly fall back to the software chain
// under a hardware label.
func TestUnknownToneMapBackendBuildsNothing(t *testing.T) {
	chain := BuildToneMapChain(ToneMapChainRequest{Backend: HWAccel("wishful")})
	if len(chain.Filters) != 0 || len(chain.InitArgs) != 0 {
		t.Fatalf("unknown backend produced a chain: %+v", chain)
	}
}

// TestSoftwareToneMapChainIsUnchanged is the no-regression control: the chain
// that ships today must be byte-identical after the restructure.
func TestSoftwareToneMapChainIsUnchanged(t *testing.T) {
	chain := BuildToneMapChain(ToneMapChainRequest{})
	if chain.Hardware() {
		t.Fatal("the software chain reports itself as hardware")
	}
	if len(chain.InitArgs) != 0 {
		t.Fatalf("the software chain initialises a device: %v", chain.InitArgs)
	}
	if !slices.Equal(chain.Filters, hdrToSDRFilters()) {
		t.Fatalf("software chain = %v, want %v", chain.Filters, hdrToSDRFilters())
	}
}

// TestHardwareProbeRendersTheProductionChainAndItsControl pins the anti-drift
// property for hardware: the probe hands the runner exactly what
// BuildToneMapChain returns, plus that same topology with only the tone-map
// filter removed, plus a plain readback.
func TestHardwareProbeRendersTheProductionChainAndItsControl(t *testing.T) {
	var handed []ToneMapRender
	probe := ProbeToneMap(context.Background(), ToneMapOptions{
		Backend:       HWAccelVAAPI,
		Class:         TransferClassPQ,
		GOOS:          "linux",
		DevicePresent: alwaysPresent,
		Runner: func(_ context.Context, render ToneMapRender) ([]byte, error) {
			handed = append(handed, render)
			return hardwareProbeFrame(render), nil
		},
	})
	if probe.Status != ToneMapWorking {
		t.Fatalf("probe = %+v, want working", probe)
	}
	if probe.PassthroughControl != PassthroughControlFailedAsRequired {
		t.Fatalf("passthrough control = %q, want %q", probe.PassthroughControl, PassthroughControlFailedAsRequired)
	}
	if len(handed) != 3 {
		t.Fatalf("renders = %d, want 3 (candidate, pass-through control, plain baseline)", len(handed))
	}

	want := BuildToneMapChain(ToneMapChainRequest{Backend: HWAccelVAAPI, Download: true})
	if !slices.Equal(handed[0].Filters, want.Filters) || !slices.Equal(handed[0].InitArgs, want.InitArgs) {
		t.Fatalf("candidate render = %+v, want the production chain %+v", handed[0], want)
	}
	control := toneMapChainWithoutTransform(want)
	if !slices.Equal(handed[1].Filters, control.Filters) || !slices.Equal(handed[1].InitArgs, control.InitArgs) {
		t.Fatalf("control render = %+v, want the same topology minus the tone-map %+v", handed[1], control)
	}
	// Symmetry by construction: the control differs from the candidate by
	// exactly the tone-map filter and nothing else.
	if len(want.Filters)-len(control.Filters) != 1 {
		t.Fatalf("control topology differs from the candidate by %d legs, want exactly 1",
			len(want.Filters)-len(control.Filters))
	}
	if len(handed[2].Filters) != 0 {
		t.Fatalf("plain baseline carried filters %v, want none", handed[2].Filters)
	}
	if probe.BaselineHighlight != highlightSampleOf(160) {
		t.Fatalf("differential baseline = %v, want the CONTROL leg's measurement", probe.BaselineHighlight)
	}
}

// TestHardwareProbeVoidsWhenThePassthroughControlPasses is the failure D2 names
// explicitly: if the device round-trip alone clears both floors, a NO-OP
// hardware chain would certify. The run must be void, never a pass.
func TestHardwareProbeVoidsWhenThePassthroughControlPasses(t *testing.T) {
	probe := ProbeToneMap(context.Background(), ToneMapOptions{
		Backend:       HWAccelNVENC,
		GOOS:          "linux",
		DevicePresent: alwaysPresent,
		Runner: func(_ context.Context, render ToneMapRender) ([]byte, error) {
			switch {
			case slices.ContainsFunc(render.Filters, func(leg string) bool { return strings.HasPrefix(leg, "tonemap_") }):
				return flatFrame(toneMapProbeFrameBytes, 100), nil
			case len(render.Filters) > 0:
				// The control: the round-trip alone drops 173 -> 120, which
				// clears both floors without any tone-map having happened.
				return flatFrame(toneMapProbeFrameBytes, 120), nil
			default:
				return flatFrame(toneMapProbeFrameBytes, 173), nil
			}
		},
	})
	if probe.Status != ToneMapUnsupported || probe.Failure != ToneMapFailureInconclusive {
		t.Fatalf("probe = %+v, want unsupported/inconclusive: a topology that passes on its own certifies "+
			"a no-op tone-map", probe)
	}
}

// TestHardwareProbeDetectsAnAbsentDevice pins D7: `unavailable` is DETECTED by
// a pre-check before any render, never inferred from a failed one.
func TestHardwareProbeDetectsAnAbsentDevice(t *testing.T) {
	rendered := false
	probe := ProbeToneMap(context.Background(), ToneMapOptions{
		Backend:       HWAccelVAAPI,
		GOOS:          "linux",
		DevicePresent: func(HWAccel, string) bool { return false },
		Runner: func(context.Context, ToneMapRender) ([]byte, error) {
			rendered = true
			return nil, errors.New("should never run")
		},
	})
	if probe.Failure != ToneMapFailureUnavailable {
		t.Fatalf("probe failure = %q, want %q", probe.Failure, ToneMapFailureUnavailable)
	}
	if rendered {
		t.Fatal("the probe rendered despite an absent device; the pre-check must short-circuit")
	}
	if string(ToneMapFailureUnavailable) != string(HWAccelUnavailable) {
		t.Fatalf("unavailable spelling drifted: %q vs %q", ToneMapFailureUnavailable, HWAccelUnavailable)
	}
}

// TestToneMapDevicePreCheckFollowsTheEncoderProbePattern pins the real
// pre-checks against the platform rules the encoder probe already uses.
func TestToneMapDevicePreCheckFollowsTheEncoderProbePattern(t *testing.T) {
	if toneMapDevicePresent(HWAccelVideoToolbox, "linux", nil) {
		t.Fatal("VideoToolbox reported present on linux")
	}
	if toneMapDevicePresent(HWAccelVAAPI, "darwin", nil) {
		t.Fatal("VAAPI reported present on darwin")
	}
	if toneMapDevicePresent(HWAccelNVENC, "darwin", nil) {
		t.Fatal("NVENC reported present on darwin")
	}
	if toneMapDevicePresent(HWAccel("wishful"), "linux", nil) {
		t.Fatal("an unknown backend reported a present device")
	}
}

// TestProbeFixturesCoverTheLaunchTransferClasses pins D3's fixture set: the
// existing synthetic PQ source plus an HLG variant, both in-graph, and NO
// fixture for Dolby Vision 7/8 — which therefore gets no row at all rather than
// a fabricated pass.
func TestProbeFixturesCoverTheLaunchTransferClasses(t *testing.T) {
	pq, ok := toneMapProbeSourceFor(TransferClassPQ)
	if !ok || !strings.Contains(pq, "color_trc=smpte2084") {
		t.Fatalf("PQ fixture = %q", pq)
	}
	hlg, ok := toneMapProbeSourceFor(TransferClassHLG)
	if !ok || !strings.Contains(hlg, "color_trc=arib-std-b67") {
		t.Fatalf("HLG fixture = %q", hlg)
	}
	if strings.Contains(hlg, "smpte2084") {
		t.Fatal("the HLG fixture still tags itself PQ, so an HLG row would certify the PQ path")
	}
	if _, ok := toneMapProbeSourceFor(TransferClassDoVi78); ok {
		t.Fatal("a Dolby Vision 7/8 fixture appeared; it cannot be synthesised by lavfi (DOVI_78_FIXTURE)")
	}

	rendered := false
	probe := ProbeToneMap(context.Background(), ToneMapOptions{
		Backend:       HWAccelVideoToolbox,
		Class:         TransferClassDoVi78,
		GOOS:          "darwin",
		DevicePresent: alwaysPresent,
		Runner: func(context.Context, ToneMapRender) ([]byte, error) {
			rendered = true
			return flatFrame(toneMapProbeFrameBytes, 100), nil
		},
	})
	if probe.Status != ToneMapUnsupported || probe.Failure != ToneMapFailureInconclusive {
		t.Fatalf("DoVi 7/8 hardware probe = %+v, want unsupported/inconclusive", probe)
	}
	if rendered {
		t.Fatal("the DoVi 7/8 probe rendered something despite having no fixture")
	}
}

// TestProbeUsesTheClassFixtureItWasAskedFor guards against an HLG row that
// silently measured the PQ source.
func TestProbeUsesTheClassFixtureItWasAskedFor(t *testing.T) {
	var sources []string
	ProbeToneMap(context.Background(), ToneMapOptions{
		Class: TransferClassHLG,
		Runner: func(_ context.Context, render ToneMapRender) ([]byte, error) {
			sources = append(sources, render.Source)
			return workingToneMapFrame(render), nil
		},
	})
	for _, source := range sources {
		if !strings.Contains(source, "arib-std-b67") {
			t.Fatalf("HLG probe rendered source %q", source)
		}
	}
}

// TestCalibrationClampsToTheAbsoluteConstants is the ADR's own worked example.
// Against the values measured on the pinned build, a bare m=0.5 yields floors
// BELOW both constants — calibration would be looser than the uncalibrated
// regime it specialises. The max(…) is what forbids that, on both dimensions.
func TestCalibrationClampsToTheAbsoluteConstants(t *testing.T) {
	thresholds, err := CalibrateToneMapThresholds(173.125, 122.859, CalibrationMarginFloor)
	if err != nil {
		t.Fatalf("calibration: %v", err)
	}
	if thresholds.Origin != ThresholdOriginCalibrated {
		t.Fatalf("origin = %q", thresholds.Origin)
	}
	// Unclamped these would be 25.133 and 61.430.
	if thresholds.MinHighlightDrop != toneMapMinHighlightDrop {
		t.Fatalf("min_highlight_drop = %v, want the constant %d (unclamped it is %.3f)",
			thresholds.MinHighlightDrop, toneMapMinHighlightDrop, CalibrationMarginFloor*(173.125-122.859))
	}
	if thresholds.MinHighlight != toneMapMinHighlight {
		t.Fatalf("min_highlight = %v, want the constant %d (unclamped it is %.3f)",
			thresholds.MinHighlight, toneMapMinHighlight, CalibrationMarginFloor*122.859)
	}
}

// TestCalibrationOnlyEverRaisesAFloor: a reference measuring high must tighten,
// never loosen.
func TestCalibrationOnlyEverRaisesAFloor(t *testing.T) {
	thresholds, err := CalibrateToneMapThresholds(400, 200, 1.0)
	if err != nil {
		t.Fatalf("calibration: %v", err)
	}
	if thresholds.MinHighlightDrop != 200 || thresholds.MinHighlight != 200 {
		t.Fatalf("thresholds = %+v, want floors raised to the reference-derived values", thresholds)
	}
	if thresholds.MinHighlightDrop < toneMapMinHighlightDrop || thresholds.MinHighlight < toneMapMinHighlight {
		t.Fatal("a calibrated floor landed below an absolute constant")
	}
}

// TestCalibrationRejectsAnUntrustworthyReference is the anchor rule. Without
// it, a degraded local FFmpeg scales every derived floor down with it and
// quietly certifies a bad backend against a bad reference.
func TestCalibrationRejectsAnUntrustworthyReference(t *testing.T) {
	tests := []struct {
		name                        string
		baseline, reference, margin float64
	}{
		{"reference barely moves the highlight", 130, 120, CalibrationMarginFloor},
		{"reference crushes the highlight", 173.125, 40, CalibrationMarginFloor},
		{"margin below the floor", 173.125, 122.859, 0.4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CalibrateToneMapThresholds(tc.baseline, tc.reference, tc.margin); err == nil {
				t.Fatal("calibration succeeded on an invalid run")
			}
		})
	}
}

// TestCalibrationNeverReadsTheCandidate is the load-bearing rule: a crushing
// backend must not be able to set its own floor. The arithmetic takes only the
// baseline and the SOFTWARE reference, so identical reference legs must yield
// identical floors no matter what the candidate measured.
func TestCalibrationNeverReadsTheCandidate(t *testing.T) {
	honest, err := CalibrateToneMapThresholds(400, 200, 1.0)
	if err != nil {
		t.Fatalf("calibration: %v", err)
	}
	// The "crushing backend measuring highlight 30" from the amendment: its own
	// measurement is simply not an input, so the floor it must clear is
	// unchanged — and 30 is far below it.
	crushing, err := CalibrateToneMapThresholds(400, 200, 1.0)
	if err != nil {
		t.Fatalf("calibration: %v", err)
	}
	if honest != crushing {
		t.Fatalf("floors differ (%+v vs %+v) although the reference legs are identical", honest, crushing)
	}
	if crushing.MinHighlight <= 30 {
		t.Fatalf("min_highlight = %v would admit a candidate crushed to 30", crushing.MinHighlight)
	}
}

// TestCalibratedThresholdsGovernTheVerdict proves the probe actually applies a
// calibrated floor rather than silently using the constants.
func TestCalibratedThresholdsGovernTheVerdict(t *testing.T) {
	tight := ToneMapThresholds{MinHighlightDrop: 32, MinHighlight: 150, Origin: ThresholdOriginCalibrated}
	probe := ProbeToneMap(context.Background(), ToneMapOptions{
		Backend:       HWAccelNVENC,
		GOOS:          "linux",
		DevicePresent: alwaysPresent,
		Thresholds:    &tight,
		Runner:        func(_ context.Context, render ToneMapRender) ([]byte, error) { return hardwareProbeFrame(render), nil },
	})
	// The candidate lands at 100, which clears the inherited floor of 64 but
	// not the calibrated 150.
	if probe.Status != ToneMapUnsupported || probe.Failure != ToneMapFailureCrushed {
		t.Fatalf("probe = %+v, want unsupported/crushed under the calibrated floor", probe)
	}
	if probe.Thresholds != tight {
		t.Fatalf("probe recorded thresholds %+v, want %+v", probe.Thresholds, tight)
	}
}

// TestRunToneMapCalibrationMeasuresTheSoftwareReferenceLeg pins the calibration
// driver's two legs: it renders the plain baseline and the SOFTWARE chain, and
// never the candidate backend.
func TestRunToneMapCalibrationMeasuresTheSoftwareReferenceLeg(t *testing.T) {
	var handed []ToneMapRender
	calibration, err := RunToneMapCalibration(context.Background(), ToneMapOptions{
		Backend: HWAccelVAAPI, // must be ignored
		Runner: func(_ context.Context, render ToneMapRender) ([]byte, error) {
			handed = append(handed, render)
			return workingToneMapFrame(render), nil
		},
	}, CalibrationMarginFloor)
	if err != nil {
		t.Fatalf("calibration run: %v", err)
	}
	if len(handed) != 2 {
		t.Fatalf("renders = %d, want 2 (baseline + software reference)", len(handed))
	}
	if len(handed[0].Filters) != 0 {
		t.Fatalf("first leg carried filters %v, want the plain baseline", handed[0].Filters)
	}
	if !slices.Equal(handed[1].Filters, hdrToSDRFilters()) {
		t.Fatalf("reference leg = %v, want the software chain %v", handed[1].Filters, hdrToSDRFilters())
	}
	for _, render := range handed {
		if len(render.InitArgs) != 0 {
			t.Fatalf("a calibration leg initialised a device: %v", render.InitArgs)
		}
	}
	if math.Abs(calibration.SoftwareRefHighlight-123) > 0.001 {
		t.Fatalf("software reference = %v, want 123", calibration.SoftwareRefHighlight)
	}
}

// TestToneMapVerdictsAreKeyedForLiveness pins D4: the key is (backend,
// transfer class, resolved binary identity), so a BYO-FFmpeg swap or a
// different class can never be served a stale verdict.
func TestToneMapVerdictsAreKeyedForLiveness(t *testing.T) {
	base := toneMapVerdictKey{backend: HWAccelVAAPI, class: TransferClassPQ, identity: "sha-a"}
	distinct := []toneMapVerdictKey{
		{backend: HWAccelQSV, class: TransferClassPQ, identity: "sha-a"},
		{backend: HWAccelVAAPI, class: TransferClassHLG, identity: "sha-a"},
		{backend: HWAccelVAAPI, class: TransferClassPQ, identity: "sha-b"},
	}
	entry := toneMapVerdictEntryFor(base)
	if entry != toneMapVerdictEntryFor(base) {
		t.Fatal("the same key produced two cache entries, so a verdict would be re-probed forever")
	}
	for _, key := range distinct {
		if toneMapVerdictEntryFor(key) == entry {
			t.Fatalf("key %+v shares a cache entry with %+v; a stale verdict would be served", key, base)
		}
	}
}

// TestSelectToneMapBackendPrefersVerifiedHardware pins D4's selection: the
// first ELIGIBLE backend in the declared order wins, an unsupported backend is
// skipped rather than attempted, and software is the empty fallback.
func TestSelectToneMapBackendPrefersVerifiedHardware(t *testing.T) {
	if got := ToneMapBackendPreference("linux"); !slices.Equal(got, []HWAccel{HWAccelQSV, HWAccelVAAPI, HWAccelNVENC}) {
		t.Fatalf("linux preference order = %v", got)
	}
	if got := ToneMapBackendPreference("darwin"); !slices.Equal(got, []HWAccel{HWAccelVideoToolbox}) {
		t.Fatalf("darwin preference order = %v", got)
	}

	withToneMapEligibility(t, func(backend HWAccel, _ TransferClass) bool { return backend == HWAccelVAAPI })
	if got := SelectToneMapBackend(TransferClassPQ, "linux"); got != HWAccelVAAPI {
		t.Fatalf("selected %q, want vaapi — QSV is ineligible and must be skipped, not attempted", got)
	}
	// Dolby Vision 7/8 has no hardware row, so it falls back to SOFTWARE — not
	// to a refusal.
	if got := SelectToneMapBackend(TransferClassDoVi78, "linux"); got != "" {
		t.Fatalf("DoVi 7/8 selected hardware %q; it has no fixture and therefore no hardware row", got)
	}

	withToneMapEligibility(t, func(HWAccel, TransferClass) bool { return false })
	if got := SelectToneMapBackend(TransferClassPQ, "linux"); got != "" {
		t.Fatalf("selected %q with nothing eligible, want the software fallback", got)
	}

	withToneMapEligibility(t, func(HWAccel, TransferClass) bool { return true })
	if got := SelectToneMapBackend(TransferClassPQ, "windows"); got != "" {
		t.Fatalf("selected %q on a platform that advertises no accelerator", got)
	}
}

// TestTransferClassMatchesTheRuntimePredicate: the certification key and the
// runtime tone-map predicate must never disagree about which class a source is
// in, or a class could be certified while another is transcoded.
func TestTransferClassMatchesTheRuntimePredicate(t *testing.T) {
	tests := []struct {
		transfer string
		doVi     int
		want     TransferClass
	}{
		{"smpte2084", 0, TransferClassPQ},
		{"arib-std-b67", 0, TransferClassHLG},
		{"smpte2084", 8, TransferClassDoVi78},
		{"", 7, TransferClassDoVi78},
		{"bt709", 0, ""},
	}
	for _, tc := range tests {
		class, classified := TransferClassFor(tc.transfer, tc.doVi)
		rendition := Rendition{ColorTransfer: tc.transfer, DoViProfile: tc.doVi}
		if classified != rendition.needsToneMap() {
			t.Fatalf("TransferClassFor(%q,%d) classified=%v but needsToneMap=%v",
				tc.transfer, tc.doVi, classified, rendition.needsToneMap())
		}
		if class != tc.want {
			t.Fatalf("TransferClassFor(%q,%d) = %q, want %q", tc.transfer, tc.doVi, class, tc.want)
		}
	}
	if HardwareCertifiableClass(TransferClassDoVi78) {
		t.Fatal("DoVi 7/8 reported hardware-certifiable; it has no synthesisable fixture")
	}
	for _, class := range []TransferClass{TransferClassPQ, TransferClassHLG} {
		if !HardwareCertifiableClass(class) {
			t.Fatalf("%s reported not hardware-certifiable; it is in the launch fixture set", class)
		}
	}
}

// TestRenditionEmitsTheHardwareChainInOrder pins the composed graph: the
// tone-map chain leads, the software filters follow it in system memory, and
// the device initialisation reaches the argv before -i.
func TestRenditionEmitsTheHardwareChainInOrder(t *testing.T) {
	rendition := Rendition{Height: 720, ColorTransfer: "smpte2084"}.
		withHWAccel(HWAccelVAAPI).
		withToneMapAccel(HWAccelVAAPI)

	filters := rendition.videoFilters()
	toneMap := slices.IndexFunc(filters, func(leg string) bool { return strings.HasPrefix(leg, "tonemap_") })
	scale := slices.IndexFunc(filters, func(leg string) bool { return strings.HasPrefix(leg, "scale=") })
	if toneMap < 0 || scale < 0 || toneMap > scale {
		t.Fatalf("hardware tone-map does not lead the graph: %v", filters)
	}
	if slices.Index(filters, "hwdownload") > scale {
		t.Fatalf("a software scale runs on device frames: %v", filters)
	}
	if !slices.Contains(filters, "hwupload") {
		t.Fatalf("graph never uploads: %v", filters)
	}

	init := rendition.inputArgs()
	if !slices.Contains(init, "-init_hw_device") {
		t.Fatalf("input args carry no device initialisation: %v", init)
	}
	if !slices.Contains(init, "-vaapi_device") {
		t.Fatalf("the encoder lost its own VAAPI device: %v", init)
	}
}

// TestSoftwareRenditionArgsAreUnchanged is the shipped-behaviour control: a
// rendition with no certified hardware backend must emit exactly what it emits
// today, on both the plain and the burn-in path.
func TestSoftwareRenditionArgsAreUnchanged(t *testing.T) {
	rendition := Rendition{Height: 720, ColorTransfer: "smpte2084"}
	want := append(append([]string{}, hdrToSDRFilters()...), "scale=-2:min(ih\\,720)", "format=yuv420p")
	if got := rendition.videoFilters(); !slices.Equal(got, want) {
		t.Fatalf("software videoFilters = %v, want %v", got, want)
	}
	if len(rendition.inputArgs()) != 0 {
		t.Fatalf("software rendition emits input args %v", rendition.inputArgs())
	}

	burn := rendition.WithBurnSubtitle(2).burnInArgs(2)
	graph := burn[1]
	if !strings.Contains(graph, hdrToSDRFilters()[0]) {
		t.Fatalf("burn-in graph lost the software tone-map: %s", graph)
	}
	if !strings.Contains(graph, "format=yuv420p") {
		t.Fatalf("burn-in graph lost the encoder ingest format: %s", graph)
	}
}

// TestSDRRenditionEmitsNoToneMapChain is the negative control: nothing about
// this work may put a tone-map on a source that does not need one.
func TestSDRRenditionEmitsNoToneMapChain(t *testing.T) {
	for _, rendition := range []Rendition{
		{Height: 720},
		{Height: 720, ColorTransfer: "smpte2084", CopyVideo: true},
		{Height: 720, DoViProfile: 5},
	} {
		if got := rendition.toneMapChain(); len(got.Filters) != 0 || len(got.InitArgs) != 0 {
			t.Fatalf("rendition %+v emitted a tone-map chain %+v", rendition, got)
		}
	}
}

// TestToneMapChainFilterNamesCoversEveryLeg guards the manifest-coverage input:
// a name this misses is a filter the manifest is never asked about.
func TestToneMapChainFilterNamesCoversEveryLeg(t *testing.T) {
	names := ToneMapChainFilterNames(HWAccelNVENC)
	for _, want := range []string{"format", "hwupload", "tonemap_cuda", "hwdownload"} {
		if !slices.Contains(names, want) {
			t.Fatalf("filter names %v omit %q", names, want)
		}
	}
}

// TestToneMapProbeArgsPlaceDeviceInitBeforeInput: -init_hw_device after -i is
// silently ignored by FFmpeg, which would leave the filter with no device.
func TestToneMapProbeArgsPlaceDeviceInitBeforeInput(t *testing.T) {
	chain := BuildToneMapChain(ToneMapChainRequest{Backend: HWAccelNVENC, Download: true})
	args := toneMapProbeArgs(ToneMapRender{InitArgs: chain.InitArgs, Filters: chain.Filters, Source: toneMapProbeSource})
	initIndex := slices.Index(args, "-init_hw_device")
	inputIndex := slices.Index(args, "-i")
	if initIndex < 0 {
		t.Fatalf("probe argv carries no device initialisation: %v", args)
	}
	if initIndex > inputIndex {
		t.Fatalf("device initialisation lands after -i: %v", args)
	}
}

// withToneMapEligibility states the certification a test means rather than
// probing whatever hardware the host happens to have.
func withToneMapEligibility(t *testing.T, eligible func(HWAccel, TransferClass) bool) {
	t.Helper()
	previous := toneMapHardwareEligible
	toneMapHardwareEligible = eligible
	t.Cleanup(func() { toneMapHardwareEligible = previous })
}

// hardwareProbeFrame mimics a working hardware leg: the plain readback measures
// 173, the device round-trip alone leaves it at 160 (a 13-point drop, well
// under the floor, so the control fails as required), and the tone-map lands it
// at 100.
func hardwareProbeFrame(render ToneMapRender) []byte {
	switch {
	case slices.ContainsFunc(render.Filters, func(leg string) bool { return strings.HasPrefix(leg, "tonemap_") }):
		return flatFrame(toneMapProbeFrameBytes, 100)
	case len(render.Filters) > 0:
		return flatFrame(toneMapProbeFrameBytes, 160)
	default:
		return flatFrame(toneMapProbeFrameBytes, 173)
	}
}

func highlightSampleOf(sample byte) float64 {
	return highlightSample(flatFrame(toneMapProbeFrameBytes, sample))
}

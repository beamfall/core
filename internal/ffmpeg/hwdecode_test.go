package ffmpeg

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func deviceDecodeRendition(accel HWAccel) Rendition {
	return Rendition{
		Height:              1080,
		VideoCodec:          "h264",
		ColorTransfer:       "smpte2084",
		ColorPrimaries:      "bt2020",
		SourceVideoCodec:    "hevc",
		SourceVideoBitDepth: 10,
	}.withHWAccel(accel).withToneMapAccel(accel)
}

// TestDeviceDecodeChainKeepsEveryFrameOnTheDevice pins the argv a verified
// rendition emits: the hardware decoder before the input, one device chain, and
// no system-memory scale, upload or download.
func TestDeviceDecodeChainKeepsEveryFrameOnTheDevice(t *testing.T) {
	node := vaapiDevicePath()
	for accel, target := range map[HWAccel]string{HWAccelQSV: "qsv", HWAccelVAAPI: "vaapi"} {
		rendition := deviceDecodeRendition(accel).withDeviceDecode(true)

		wantInput := []string{"-init_hw_device", "vaapi=va:" + node}
		if accel == HWAccelQSV {
			wantInput = append(wantInput, "-init_hw_device", "qsv=qs@va")
		}
		wantInput = append(wantInput,
			"-init_hw_device", "opencl=ocl@va", "-filter_hw_device", "ocl",
			"-hwaccel", "vaapi", "-hwaccel_device", "va", "-hwaccel_output_format", "vaapi")
		if got := rendition.inputArgs(); !slices.Equal(got, wantInput) {
			t.Fatalf("%s inputArgs = %v, want %v", accel, got, wantInput)
		}

		wantFilters := []string{
			`scale_vaapi=w=-2:h=min(ih\,1080):format=p010`,
			"hwmap=derive_device=opencl",
			toneMapOpenCLFilter,
			"hwmap=derive_device=" + target + ":reverse=1",
			"format=" + target,
		}
		if got := rendition.videoFilters(); !slices.Equal(got, wantFilters) {
			t.Fatalf("%s videoFilters = %v, want %v", accel, got, wantFilters)
		}
	}
}

// TestDeviceDecodeLeavesIneligibleRenditionsUnchanged: a rendition the chain
// cannot serve emits exactly the argv it did before, even when the host's
// verdict is on.
func TestDeviceDecodeLeavesIneligibleRenditionsUnchanged(t *testing.T) {
	cases := map[string]func(Rendition) Rendition{
		"h264 source":    func(r Rendition) Rendition { r.SourceVideoCodec = "h264"; return r },
		"unknown codec":  func(r Rendition) Rendition { r.SourceVideoCodec = ""; return r },
		"8-bit source":   func(r Rendition) Rendition { r.SourceVideoBitDepth = 8; return r },
		"dolby vision 7": func(r Rendition) Rendition { r.DoViProfile = 7; return r },
		"dolby vision 5": func(r Rendition) Rendition { r.DoViProfile = 5; r.ColorTransfer = ""; return r },
		"dolby vision 8 over an sdr base": func(r Rendition) Rendition {
			r.DoViProfile = 8
			r.ColorTransfer = "bt709"
			return r
		},
		"sdr source":      func(r Rendition) Rendition { r.ColorTransfer = "bt709"; return r },
		"burned subtitle": func(r Rendition) Rendition { return r.WithBurnSubtitle(2) },
		"interlaced":      func(r Rendition) Rendition { r.FieldOrder = "tt"; return r },
		"anamorphic":      func(r Rendition) Rendition { r.SampleAspectRatio = "4:3"; return r },
		"nvenc encoder":   func(r Rendition) Rendition { return r.withHWAccel(HWAccelNVENC).withToneMapAccel(HWAccelNVENC) },
		"software encode": func(r Rendition) Rendition { return r.withHWAccel("").withToneMapAccel("") },
	}
	for name, shape := range cases {
		before := shape(deviceDecodeRendition(HWAccelQSV))
		after := before.withDeviceDecode(true)
		// withDeviceDecode moves the tone-map onto the encoder's backend, which
		// these cases already use, so any difference is the device chain.
		if before.deviceDecodeEligible() {
			t.Fatalf("%s: eligible, want ineligible", name)
		}
		if !slices.Equal(before.inputArgs(), after.inputArgs()) || !slices.Equal(before.videoFilters(), after.videoFilters()) {
			t.Fatalf("%s: argv changed\n before %v %v\n after  %v %v", name,
				before.inputArgs(), before.videoFilters(), after.inputArgs(), after.videoFilters())
		}
	}
}

// TestDeviceDecodeIsOffUntilVerified: without the verdict the rendition keeps
// the software decode, and clearing the accelerators turns the chain off.
func TestDeviceDecodeIsOffUntilVerified(t *testing.T) {
	rendition := deviceDecodeRendition(HWAccelQSV)
	if slices.Contains(rendition.inputArgs(), "-hwaccel") {
		t.Fatalf("unverified inputArgs = %v, want no hardware decode", rendition.inputArgs())
	}
	if rendition.withDeviceDecode(false).usesDeviceDecode() {
		t.Fatal("withDeviceDecode(false) uses the device chain")
	}
	cleared := rendition.withDeviceDecode(true).withHWAccel("").withToneMapAccel("")
	if cleared.usesDeviceDecode() || slices.Contains(cleared.inputArgs(), "-hwaccel") {
		t.Fatalf("cleared accelerators still emit the device chain: %v", cleared.inputArgs())
	}
}

// TestSelectRenditionDeviceDecodeNeverProbesIneligibleRenditions keeps the
// probe off the spawn path of every rendition it could not serve.
func TestSelectRenditionDeviceDecodeNeverProbesIneligibleRenditions(t *testing.T) {
	sdr := deviceDecodeRendition(HWAccelQSV)
	sdr.ColorTransfer = "bt709"
	if selectRenditionDeviceDecode(sdr) {
		t.Fatal("SDR rendition selected the device chain")
	}
}

// TestDeviceDecodeServesADolbyVisionProfile8BaseLayer: a profile 8 source is
// classed by its base layer, so it takes the same chain and the same verdict
// as a plain PQ or HLG source.
func TestDeviceDecodeServesADolbyVisionProfile8BaseLayer(t *testing.T) {
	plain := deviceDecodeRendition(HWAccelQSV).withDeviceDecode(true)
	for transfer, want := range map[string]TransferClass{"smpte2084": TransferClassPQ, "arib-std-b67": TransferClassHLG} {
		dv := deviceDecodeRendition(HWAccelQSV)
		dv.DoViProfile = 8
		dv.ColorTransfer = transfer
		if class, ok := dv.deviceDecodeClass(); !ok || class != want {
			t.Fatalf("%s base: class %q ok=%v, want %q", transfer, class, ok, want)
		}
		dv = dv.withDeviceDecode(true)
		if !dv.usesDeviceDecode() {
			t.Fatalf("%s base: device chain not used", transfer)
		}
		if !slices.Equal(dv.inputArgs(), plain.inputArgs()) || !slices.Equal(dv.videoFilters(), plain.videoFilters()) {
			t.Fatalf("%s base: argv differs from the plain source\n %v %v", transfer, dv.inputArgs(), dv.videoFilters())
		}
	}
}

// deviceDecodeFakeRunner answers the sample encode and the three legs.
type deviceDecodeFakeRunner struct {
	calls     [][]string
	sampleErr error
	candidate byte
	control   byte
	failLeg   string
}

func (f *deviceDecodeFakeRunner) run(_ context.Context, args []string) ([]byte, error) {
	f.calls = append(f.calls, args)
	graph := args[slices.Index(args, "-vf")+1]
	switch {
	case slices.Contains(args, "libx265"):
		return []byte("hevc-sample"), f.sampleErr
	case strings.Contains(graph, "tonemap_opencl"):
		if f.failLeg == "candidate" {
			return nil, errors.New("exit status 1")
		}
		return flatFrame(toneMapProbeFrameBytes, f.candidate), nil
	case strings.Contains(graph, "scale_vaapi"):
		if f.failLeg == "control" {
			return nil, errors.New("exit status 1")
		}
		return flatFrame(toneMapProbeFrameBytes, f.control), nil
	default:
		return flatFrame(toneMapProbeFrameBytes, 176), nil
	}
}

func probeDeviceDecodeWith(fake *deviceDecodeFakeRunner, class TransferClass) ToneMapProbe {
	return ProbeDeviceDecode(context.Background(), DeviceDecodeOptions{
		ToneMapOptions: ToneMapOptions{
			Backend:       HWAccelQSV,
			Class:         class,
			GOOS:          "linux",
			DevicePresent: alwaysPresent,
		},
		Run: fake.run,
	})
}

// TestDeviceDecodeProbeRunsTheProductionChainAgainstADecodedControl pins the
// four commands and the verdict's baseline.
func TestDeviceDecodeProbeRunsTheProductionChainAgainstADecodedControl(t *testing.T) {
	fake := &deviceDecodeFakeRunner{candidate: 116, control: 176}
	probe := probeDeviceDecodeWith(fake, TransferClassPQ)
	if probe.Status != ToneMapWorking || probe.PassthroughControl != PassthroughControlFailedAsRequired {
		t.Fatalf("probe = %+v, want working with a failed control", probe)
	}
	if probe.BaselineHighlight != highlightSampleOf(176) || probe.ToneMappedHighlight != highlightSampleOf(116) {
		t.Fatalf("probe measured %v -> %v, want control 176 -> candidate 116", probe.BaselineHighlight, probe.ToneMappedHighlight)
	}
	if len(fake.calls) != 4 {
		t.Fatalf("commands = %d, want 4 (sample, candidate, control, plain)", len(fake.calls))
	}

	sample := fake.calls[0]
	if !slices.Contains(sample, "smpte2084") || !slices.Contains(sample, "nullsrc=s=128x128:r=1:d=0.1") {
		t.Fatalf("sample argv = %v, want a 128x128 PQ encode", sample)
	}

	want := BuildToneMapChain(ToneMapChainRequest{Backend: HWAccelQSV, DeviceDecode: true, Download: true, Height: deviceDecodeProbeHeight})
	candidate := fake.calls[1]
	input := slices.Index(candidate, "-i")
	for _, arg := range append(slices.Clone(want.InitArgs), want.InputArgs...) {
		if at := slices.Index(candidate, arg); at < 0 || at > input {
			t.Fatalf("candidate argv = %v, want %q before -i", candidate, arg)
		}
	}
	wantGraph := strings.Join(want.Filters, ",") + ",scale=16:16,format=gray"
	if got := candidate[slices.Index(candidate, "-vf")+1]; got != wantGraph {
		t.Fatalf("candidate graph = %q, want %q", got, wantGraph)
	}
	if got, wantControl := fake.calls[2][slices.Index(fake.calls[2], "-vf")+1],
		want.Filters[0]+",hwdownload,format=p010,scale=16:16,format=gray"; got != wantControl {
		t.Fatalf("control graph = %q, want %q", got, wantControl)
	}
	plain := fake.calls[3]
	if slices.Contains(plain, "-hwaccel") || plain[slices.Index(plain, "-vf")+1] != "scale=16:16,format=gray" {
		t.Fatalf("plain argv = %v, want a software decode and the readback only", plain)
	}
	if _, err := os.Stat(candidate[input+1]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sample file %s left behind (err %v)", candidate[input+1], err)
	}
}

func TestDeviceDecodeProbeTagsAnHLGSample(t *testing.T) {
	fake := &deviceDecodeFakeRunner{candidate: 116, control: 176}
	if probe := probeDeviceDecodeWith(fake, TransferClassHLG); probe.Status != ToneMapWorking {
		t.Fatalf("probe = %+v, want working", probe)
	}
	if !slices.Contains(fake.calls[0], "arib-std-b67") {
		t.Fatalf("sample argv = %v, want an HLG transfer", fake.calls[0])
	}
}

// TestDeviceDecodeProbeFailsClosed covers every way the probe can end without
// proof.
func TestDeviceDecodeProbeFailsClosed(t *testing.T) {
	cases := map[string]struct {
		fake *deviceDecodeFakeRunner
		want ToneMapFailure
	}{
		"tone-map is a no-op":      {&deviceDecodeFakeRunner{candidate: 170, control: 176}, ToneMapFailureNoTransform},
		"tone-map crushes":         {&deviceDecodeFakeRunner{candidate: 10, control: 176}, ToneMapFailureCrushed},
		"chain does not run":       {&deviceDecodeFakeRunner{candidate: 116, control: 176, failLeg: "candidate"}, ToneMapFailureChainUnrunnable},
		"control does not run":     {&deviceDecodeFakeRunner{candidate: 116, control: 176, failLeg: "control"}, ToneMapFailureInconclusive},
		"decode alone darkens":     {&deviceDecodeFakeRunner{candidate: 80, control: 110}, ToneMapFailureInconclusive},
		"sample cannot be encoded": {&deviceDecodeFakeRunner{sampleErr: errors.New("exit status 1")}, ToneMapFailureInconclusive},
	}
	for name, tc := range cases {
		probe := probeDeviceDecodeWith(tc.fake, TransferClassPQ)
		if probe.Status != ToneMapUnsupported || probe.Failure != tc.want {
			t.Fatalf("%s: probe = %+v, want unsupported/%s", name, probe, tc.want)
		}
	}

	absent := ProbeDeviceDecode(context.Background(), DeviceDecodeOptions{ToneMapOptions: ToneMapOptions{
		Backend: HWAccelQSV, GOOS: "linux", DevicePresent: func(HWAccel, string) bool { return false },
	}})
	if absent.Failure != ToneMapFailureUnavailable {
		t.Fatalf("absent device probe = %+v, want unavailable", absent)
	}
	nvenc := ProbeDeviceDecode(context.Background(), DeviceDecodeOptions{ToneMapOptions: ToneMapOptions{
		Backend: HWAccelNVENC, GOOS: "linux", DevicePresent: alwaysPresent,
	}})
	if nvenc.Failure != ToneMapFailureChainUnrunnable {
		t.Fatalf("NVENC probe = %+v, want chain-unrunnable (no device-decode chain)", nvenc)
	}
}

// TestUploadChainControlDownloadsWhatWasUploaded: without the tone-map the
// device holds the uploaded format, and hwdownload returns no other.
func TestUploadChainControlDownloadsWhatWasUploaded(t *testing.T) {
	chain := BuildToneMapChain(ToneMapChainRequest{Backend: HWAccelVAAPI, Download: true})
	control := toneMapChainWithoutTransform(chain)
	spec := hwToneMapSpecs[HWAccelVAAPI]
	if got, want := control.Filters[len(control.Filters)-1], "format="+spec.uploadFormat; got != want {
		t.Fatalf("control ends in %q, want %q: %v", got, want, control.Filters)
	}
}

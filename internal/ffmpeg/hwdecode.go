package ffmpeg

// Device-decode probe (CORE-2156, ADR-0075 Amendment 2026-07-26).
//
// The device-decode chain keeps decode, scale and tone-map on the GPU. A host
// may use it only after this process's FFmpeg has run that exact chain over an
// encoded HDR sample and returned a frame that proves the tone-map happened.
// The tone-map probe cannot stand in for it: its source is a lavfi frame, and
// a lavfi frame never passes through a hardware decoder.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// deviceDecodeSampleSize is the edge of the encoded sample and
	// deviceDecodeProbeHeight the height the chain scales it to. A hardware
	// HEVC decoder refuses the tone-map probe's 16x16 frame, and 64 is the
	// smallest size the decoders tested here accept. The sample is twice the
	// target so the device scaler has to resize: at equal sizes scale_vaapi
	// passes frames through and a host with a broken scaler would certify.
	deviceDecodeSampleSize  = 128
	deviceDecodeProbeHeight = 64

	defaultDeviceDecodeRenderTimeout = 8 * time.Second
	// Four renders: the sample encode and three legs.
	defaultDeviceDecodeProbeTimeout = 40 * time.Second
)

// DeviceDecodeRunner runs one FFmpeg command of the probe and returns its
// stdout. The argv is complete; the runner only resolves and supervises the
// binary.
type DeviceDecodeRunner func(ctx context.Context, args []string) ([]byte, error)

// confinedDeviceDecodeRunner is the production DeviceDecodeRunner for one
// probe: every command runs under conf, the confinement of a transcode on the
// probed backend with the probe's private sample directory as its only job
// path.
func confinedDeviceDecodeRunner(conf confinement) DeviceDecodeRunner {
	return func(ctx context.Context, args []string) ([]byte, error) {
		ffmpegPath, err := exec.LookPath("ffmpeg")
		if err != nil {
			return nil, err
		}
		return runProbeFFmpeg(ctx, ffmpegPath, conf, args)
	}
}

// DeviceDecodeOptions configures one device-decode probe. The embedded
// ToneMapOptions carry the backend, class, floors, device pre-check and
// timeouts; its Runner is unused.
type DeviceDecodeOptions struct {
	ToneMapOptions
	// Run executes each command. nil runs FFmpeg confined to the sample's
	// directory, with the GPU grant for a QSV or VA-API backend.
	Run DeviceDecodeRunner
}

// ProbeDeviceDecode certifies the device-decode chain for one backend and
// transfer class. It encodes a small HDR HEVC sample, then renders three legs
// from it: the production chain, the same device decode and scale with no
// tone-map (the control), and a plain software decode. The verdict compares
// the candidate against the control, so a chain that decodes and scales but
// does not tone-map fails. Like ProbeToneMap it never returns an error and has
// no branch that resolves to "assume supported".
func ProbeDeviceDecode(ctx context.Context, opts DeviceDecodeOptions) ToneMapProbe {
	if opts.PerRenderTimeout <= 0 {
		opts.PerRenderTimeout = defaultDeviceDecodeRenderTimeout
	}
	if opts.TotalTimeout <= 0 {
		opts.TotalTimeout = defaultDeviceDecodeProbeTimeout
	}
	run, ready := newToneMapProbeRun(opts.ToneMapOptions)
	if !ready.working() {
		return ready
	}
	candidate := BuildToneMapChain(ToneMapChainRequest{
		Backend:      run.backend,
		DeviceDecode: true,
		Download:     true,
		Height:       deviceDecodeProbeHeight,
	})
	if !candidate.DeviceDecode {
		return run.unsupported(ToneMapFailureChainUnrunnable)
	}
	// The sample lives in a private directory, the one path a confined leg may
	// read, so no other file in the temporary directory is exposed to it.
	sampleDir, err := os.MkdirTemp("", "beamfall-device-decode-*")
	if err != nil {
		return run.unsupported(ToneMapFailureInconclusive)
	}
	defer func() { _ = os.RemoveAll(sampleDir) }()
	runner := opts.Run
	if runner == nil {
		runner = confinedDeviceDecodeRunner(confinement{ReadOnly: []string{sampleDir}, DRI: driAccel(run.backend)})
	}
	ctx, cancel := context.WithTimeout(ctx, run.total)
	defer cancel()

	probe := deviceDecodeProbe{run: run, exec: runner}
	sample, failure := probe.writeSample(ctx, sampleDir)
	if failure != ToneMapFailureNone {
		// Without a sample nothing was measured, whatever the cause.
		return run.unsupported(failure)
	}

	toneMapped, failure := probe.render(ctx, sample, candidate)
	if failure != ToneMapFailureNone {
		return run.unsupported(failure)
	}
	control, failure := probe.render(ctx, sample, deviceDecodeControlChain(candidate))
	if failure != ToneMapFailureNone {
		return run.unsupported(ToneMapFailureInconclusive)
	}
	plain, failure := probe.render(ctx, sample, ToneMapChain{})
	if failure != ToneMapFailureNone {
		return run.unsupported(ToneMapFailureInconclusive)
	}
	if run.passes(highlightSample(plain), highlightSample(control)) {
		// Decode and scale alone clear the floors, so a no-op tone-map would
		// certify. The run is void, not a pass.
		return run.unsupported(ToneMapFailureInconclusive)
	}
	return run.verdict(highlightSample(control), highlightSample(toneMapped), PassthroughControlFailedAsRequired)
}

// deviceDecodeControlChain is the candidate's device decode and scale with the
// tone-map and the map back to the encoder removed: the frame is downloaded in
// the format the scale produced.
func deviceDecodeControlChain(candidate ToneMapChain) ToneMapChain {
	control := candidate
	control.Filters = []string{candidate.Filters[0], "hwdownload", "format=p010"}
	return control
}

type deviceDecodeProbe struct {
	run  toneMapProbeRun
	exec DeviceDecodeRunner
}

// writeSample encodes the class's fixture as HEVC Main10 and stores it in
// dir, so the legs read it under the same file-only protocol allowlist and
// directory confinement as a real transcode.
func (p deviceDecodeProbe) writeSample(ctx context.Context, dir string) (string, ToneMapFailure) {
	renderCtx, cancel := context.WithTimeout(ctx, p.run.perRender)
	defer cancel()
	encoded, err := p.exec(renderCtx, deviceDecodeSampleArgs(p.run.source, p.run.class))
	if err != nil {
		if failure := classifyToneMapRenderError(err); failure != ToneMapFailureChainUnrunnable {
			return "", failure
		}
		return "", ToneMapFailureInconclusive
	}
	if len(encoded) == 0 {
		return "", ToneMapFailureInconclusive
	}
	sample := filepath.Join(dir, "sample.hevc")
	if err := os.WriteFile(sample, encoded, 0o600); err != nil {
		return "", ToneMapFailureInconclusive
	}
	return sample, ToneMapFailureNone
}

// render runs one leg over the sample and returns its 16x16 gray frame.
func (p deviceDecodeProbe) render(ctx context.Context, sample string, chain ToneMapChain) ([]byte, ToneMapFailure) {
	renderCtx, cancel := context.WithTimeout(ctx, p.run.perRender)
	defer cancel()
	frame, err := p.exec(renderCtx, deviceDecodeLegArgs(sample, chain))
	if err != nil {
		return nil, classifyToneMapRenderError(err)
	}
	if len(frame) != toneMapProbeFrameBytes {
		return nil, ToneMapFailureNoFrame
	}
	return frame, ToneMapFailureNone
}

// deviceDecodeSampleArgs encodes one lossless frame of the fixture, tagged
// with the class's transfer so the decoder and the tone-map read it as HDR.
func deviceDecodeSampleArgs(source string, class TransferClass) []string {
	transfer := "smpte2084"
	if class == TransferClassHLG {
		transfer = "arib-std-b67"
	}
	args := []string{"-hide_banner", "-nostdin", "-v", "error", "-f", "lavfi"}
	args = append(args, inputProtocolArgs()...)
	return append(args,
		"-i", fmt.Sprintf("nullsrc=s=%[1]dx%[1]d:r=1:d=0.1", deviceDecodeSampleSize),
		"-frames:v", "1",
		"-vf", source,
		"-c:v", "libx265",
		"-x265-params", "log-level=error:lossless=1",
		"-color_primaries", "bt2020",
		"-color_trc", transfer,
		"-colorspace", "bt2020nc",
		"-f", "hevc",
		"-",
	)
}

// deviceDecodeLegArgs renders one leg: the chain's device and input options,
// the sample, then the chain's filters and the readback to the probe frame.
func deviceDecodeLegArgs(sample string, chain ToneMapChain) []string {
	graph := append([]string(nil), chain.Filters...)
	graph = append(graph, fmt.Sprintf("scale=%d:%d", toneMapProbeWidth, toneMapProbeHeight), toneMapProbeReadback)

	args := []string{"-hide_banner", "-nostdin", "-v", "error"}
	args = append(args, chain.InitArgs...)
	args = append(args, chain.InputArgs...)
	args = append(args, "-f", "hevc")
	args = append(args, inputProtocolArgs()...)
	return append(args,
		"-i", sample,
		"-frames:v", "1",
		"-vf", strings.Join(graph, ","),
		"-f", "rawvideo",
		"-",
	)
}

var (
	deviceDecodeVerdictMu sync.Mutex
	deviceDecodeVerdicts  = map[toneMapVerdictKey]*toneMapVerdictEntry{}
)

// DeviceDecodeCapabilityFor returns this runtime's device-decode verdict for
// one (backend, transfer class) pair, probing on first use. It is keyed and
// kept exactly like ToneMapCapabilityFor: in memory only, per FFmpeg identity.
func DeviceDecodeCapabilityFor(backend HWAccel, class TransferClass) ToneMapProbe {
	key := toneMapVerdictKey{backend: backend, class: class, identity: currentFFmpegIdentityKey()}
	deviceDecodeVerdictMu.Lock()
	entry, cached := deviceDecodeVerdicts[key]
	if !cached {
		entry = &toneMapVerdictEntry{}
		deviceDecodeVerdicts[key] = entry
	}
	deviceDecodeVerdictMu.Unlock()
	entry.once.Do(func() {
		entry.verdict = ProbeDeviceDecode(context.Background(), DeviceDecodeOptions{
			ToneMapOptions: ToneMapOptions{Backend: backend, Class: class},
		})
	})
	return entry.verdict
}

// selectRenditionDeviceDecode decides at spawn time whether one rendition
// uses the device-decode chain. It is a variable so a test can pin the
// decision rather than probing whatever hardware the host happens to have.
var selectRenditionDeviceDecode = func(r Rendition) bool {
	if !r.deviceDecodeEligible() {
		return false
	}
	class, _ := r.deviceDecodeClass()
	return DeviceDecodeCapabilityFor(r.hwAccel, class).Status == ToneMapWorking
}

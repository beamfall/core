//go:build unix

package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// CORE-2158: a hardware job's shim invocation carries the GPU grant, and the
// probes that certify it run through the same shim with the same grant.

// fakeShim installs a stand-in for `beamfall ffmpeg-shim` that records each
// invocation's argv and answers like FFmpeg would for the probes: a sample for
// the device-decode encode, a probe-sized frame for a rawvideo render.
func fakeShim(t *testing.T) (shim string, invocations func() [][]string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "invocations")
	shim = filepath.Join(dir, "beamfall")
	script := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do printf '%%s\037' "$arg"; done >> '%[1]s'
printf '\n' >> '%[1]s'
for arg in "$@"; do
	case "$arg" in
	libx265) printf sample; exit 0 ;;
	rawvideo) head -c %[2]d /dev/zero; exit 0 ;;
	esac
done
`, log, toneMapProbeFrameBytes)
	if err := os.WriteFile(shim, []byte(script), 0o700); err != nil { // #nosec G306 -- test fixture must be executable
		t.Fatal(err)
	}
	return shim, func() [][]string {
		t.Helper()
		data, err := os.ReadFile(log) // #nosec G304 -- log is beneath a test-owned temporary directory
		if err != nil {
			t.Fatal(err)
		}
		var calls [][]string
		for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			calls = append(calls, strings.Split(strings.TrimSuffix(line, "\037"), "\037"))
		}
		return calls
	}
}

// fakeFFmpegOnPath puts an ffmpeg first on PATH for exec.LookPath; the fake
// shim never runs it, and keeps the rest of PATH for its own utilities.
func fakeFFmpegOnPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test fixture must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

// shimFlags splits one recorded shim argv into its confinement and target.
type shimFlags struct {
	dri    bool
	ro, rw []string
	target []string
}

func parseRecordedShim(t *testing.T, args []string) shimFlags {
	t.Helper()
	if len(args) == 0 || args[0] != shimSubcommand {
		t.Fatalf("not a shim invocation: %q", args)
	}
	var flags shimFlags
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--dri":
			flags.dri = true
		case "--ro":
			i++
			flags.ro = append(flags.ro, args[i])
		case "--rw":
			i++
			flags.rw = append(flags.rw, args[i])
		case "--":
			flags.target = args[i+1:]
			return flags
		default:
			t.Fatalf("unexpected shim flag %q in %q", args[i], args)
		}
	}
	t.Fatalf("shim invocation without --: %q", args)
	return flags
}

func TestHLSShimGetsDRIOnlyForQSVOrVAAPI(t *testing.T) {
	shim, invocations := fakeShim(t)
	fakeFFmpegOnPath(t)
	originalToneMap, originalDecode := selectRenditionToneMapBackend, selectRenditionDeviceDecode
	t.Cleanup(func() { selectRenditionToneMapBackend, selectRenditionDeviceDecode = originalToneMap, originalDecode })
	selectRenditionDeviceDecode = func(Rendition) bool { return false }
	src := filepath.Join(t.TempDir(), "in.mkv")
	if err := os.WriteFile(src, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		encode  HardwareEncode
		toneMap HWAccel
		wantDRI bool
	}{
		{"software", HardwareEncode{}, "", false},
		{"vaapi encode", HardwareEncode{Accel: HWAccelVAAPI}, "", true},
		{"qsv encode", HardwareEncode{Accel: HWAccelQSV}, "", true},
		{"nvenc encode", HardwareEncode{Accel: HWAccelNVENC}, "", false},
		{"software encode, vaapi tone-map", HardwareEncode{}, HWAccelVAAPI, true},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, tc := range cases {
		selectRenditionToneMapBackend = func(Rendition) HWAccel { return tc.toneMap }
		runner := New()
		runner.SetHardeningShim(shim)
		runner.SetHardwareEncode(tc.encode)
		rendition := Rendition{Height: 720}
		starts := map[string]func(dir string) (*HLSProcess, error){
			"StartHLS":           func(dir string) (*HLSProcess, error) { return runner.StartHLS(ctx, src, dir, rendition) },
			"StartBackgroundHLS": func(dir string) (*HLSProcess, error) { return runner.StartBackgroundHLS(ctx, src, dir, rendition) },
			"StartHLSSegments": func(dir string) (*HLSProcess, error) {
				return runner.StartHLSSegments(ctx, src, dir, 0, 4, rendition)
			},
		}
		for name, start := range starts {
			dir := t.TempDir()
			proc, err := start(dir)
			if err != nil {
				t.Fatalf("%s %s: %v", tc.name, name, err)
			}
			_ = proc.Wait()
			calls := invocations()
			flags := parseRecordedShim(t, calls[len(calls)-1])
			if flags.dri != tc.wantDRI {
				t.Errorf("%s %s: --dri = %v, want %v", tc.name, name, flags.dri, tc.wantDRI)
			}
			if !slices.Equal(flags.rw, []string{dir}) || !slices.Equal(flags.ro, []string{filepath.Dir(src)}) {
				t.Errorf("%s %s: ro %q rw %q, want the job paths only", tc.name, name, flags.ro, flags.rw)
			}
		}
	}
}

func TestEncoderAndToneMapProbesRunThroughTheShim(t *testing.T) {
	shim, invocations := fakeShim(t)
	ffmpegPath := fakeFFmpegOnPath(t)
	SetProbeHardeningShim(shim)
	t.Cleanup(func() { SetProbeHardeningShim("") })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	check := func(what string, wantDRI bool) {
		t.Helper()
		calls := invocations()
		flags := parseRecordedShim(t, calls[len(calls)-1])
		if flags.dri != wantDRI {
			t.Errorf("%s: --dri = %v, want %v", what, flags.dri, wantDRI)
		}
		if len(flags.ro) != 0 || len(flags.rw) != 0 {
			t.Errorf("%s: job paths ro %q rw %q, want none", what, flags.ro, flags.rw)
		}
		if len(flags.target) == 0 || flags.target[0] != ffmpegPath {
			t.Errorf("%s: target %q, want %s", what, flags.target, ffmpegPath)
		}
	}
	for _, tc := range []struct {
		encoder string
		wantDRI bool
	}{{"h264_vaapi", true}, {"h264_qsv", true}, {"hevc_vaapi", true}, {"h264_nvenc", false}, {"h264_videotoolbox", false}} {
		if err := DefaultHWAccelRunner(ctx, HWAccelEncode{Encoder: tc.encoder}); err != nil {
			t.Fatalf("encoder probe %s: %v", tc.encoder, err)
		}
		check("encoder probe "+tc.encoder, tc.wantDRI)
	}
	for _, tc := range []struct {
		backend HWAccel
		wantDRI bool
	}{{HWAccelVAAPI, true}, {HWAccelQSV, true}, {"", false}} {
		frame, err := DefaultToneMapRunner(ctx, ToneMapRender{Source: toneMapProbeSource, Backend: tc.backend})
		if err != nil || len(frame) != toneMapProbeFrameBytes {
			t.Fatalf("tone-map render %q = %d bytes, %v", tc.backend, len(frame), err)
		}
		check("tone-map render "+string(tc.backend), tc.wantDRI)
	}
}

// TestToneMapProbeRendersCarryItsBackend proves every render of one probe asks
// for the backend's confinement, the baseline included.
func TestToneMapProbeRendersCarryItsBackend(t *testing.T) {
	var backends []HWAccel
	ProbeToneMap(context.Background(), ToneMapOptions{
		Backend:       HWAccelVAAPI,
		GOOS:          "linux",
		DevicePresent: func(HWAccel, string) bool { return true },
		Runner: func(_ context.Context, render ToneMapRender) ([]byte, error) {
			backends = append(backends, render.Backend)
			return make([]byte, toneMapProbeFrameBytes), nil
		},
	})
	if len(backends) == 0 {
		t.Fatal("the probe rendered nothing")
	}
	for i, backend := range backends {
		if backend != HWAccelVAAPI {
			t.Errorf("render %d backend = %q, want %q", i, backend, HWAccelVAAPI)
		}
	}
}

// TestDeviceDecodeProbeIsConfinedToItsSample proves the default device-decode
// runner wraps every command in the shim with the GPU grant and one read-only
// path, a private directory holding the sample, removed after the probe.
func TestDeviceDecodeProbeIsConfinedToItsSample(t *testing.T) {
	shim, invocations := fakeShim(t)
	fakeFFmpegOnPath(t)
	SetProbeHardeningShim(shim)
	t.Cleanup(func() { SetProbeHardeningShim("") })

	ProbeDeviceDecode(context.Background(), DeviceDecodeOptions{ToneMapOptions: ToneMapOptions{
		Backend:       HWAccelVAAPI,
		Class:         TransferClassPQ,
		GOOS:          "linux",
		DevicePresent: func(HWAccel, string) bool { return true },
	}})
	calls := invocations()
	if len(calls) != 4 {
		t.Fatalf("recorded %d commands, want the sample encode and three legs: %q", len(calls), calls)
	}
	var sampleDir string
	for i, call := range calls {
		flags := parseRecordedShim(t, call)
		if !flags.dri || len(flags.ro) != 1 || len(flags.rw) != 0 {
			t.Fatalf("command %d confinement dri=%v ro=%q rw=%q, want --dri and one read-only dir", i, flags.dri, flags.ro, flags.rw)
		}
		if sampleDir == "" {
			sampleDir = flags.ro[0]
		} else if flags.ro[0] != sampleDir {
			t.Fatalf("command %d read-only dir %q, want %q", i, flags.ro[0], sampleDir)
		}
		if i == 0 {
			continue
		}
		input := flags.target[slices.Index(flags.target, "-i")+1]
		if filepath.Dir(input) != sampleDir {
			t.Errorf("leg %d reads %q, outside its grant %q", i, input, sampleDir)
		}
	}
	if _, err := os.Stat(sampleDir); !os.IsNotExist(err) {
		t.Errorf("sample directory %q survives the probe: %v", sampleDir, err)
	}
}

func TestProbeCommandExecsDirectlyWithoutAShim(t *testing.T) {
	SetProbeHardeningShim("")
	cmd := probeCommand(context.Background(), "/usr/bin/ffmpeg", confinement{DRI: true}, []string{"-version"})
	if !slices.Equal(cmd.Args, []string{"/usr/bin/ffmpeg", "-version"}) {
		t.Fatalf("probe without a shim = %q, want a direct exec", cmd.Args)
	}
}

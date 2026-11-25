//go:build unix

package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// CORE-2452: the Console encode policy reaches every launch path, including
// tone-map and device-decode selection, the encrypted and composed encodes,
// and a session's pinned snapshot.

// shimTarget is the FFmpeg argv of one recorded shim invocation.
func shimTarget(t *testing.T, call []string) []string {
	t.Helper()
	i := slices.Index(call, "--")
	if i < 0 || i+1 >= len(call) {
		t.Fatalf("shim invocation without --: %q", call)
	}
	return call[i+2:] // skip the resolved ffmpeg path
}

func lastShimCall(t *testing.T, invocations func() [][]string) []string {
	t.Helper()
	calls := invocations()
	if len(calls) == 0 {
		t.Fatal("no shim invocation recorded")
	}
	return calls[len(calls)-1]
}

// withHardwareToneMap pins a verified CUDA tone-map backend and device decode,
// as on an NVIDIA host whose probes certified both.
func withHardwareToneMap(t *testing.T) {
	t.Helper()
	originalToneMap, originalDecode := selectRenditionToneMapBackend, selectRenditionDeviceDecode
	t.Cleanup(func() { selectRenditionToneMapBackend, selectRenditionDeviceDecode = originalToneMap, originalDecode })
	selectRenditionToneMapBackend = func(context.Context, Rendition) HWAccel { return HWAccelNVENC }
	selectRenditionDeviceDecode = func(_ context.Context, r Rendition) bool { return r.hwAccel != "" }
}

// TestSoftwareOnlyPolicyKeepsHDRToneMapOffTheDevice pins Codex's finding on
// CORE-2452: with hardware off, an HDR conversion on a host with a verified
// hardware tone-map neither tone-maps nor decodes on the device, and gets no
// GPU grant, on every HLS launch path that encodes video. (The keyframe
// path carries copied video only, so it never tone-maps.)
func TestSoftwareOnlyPolicyKeepsHDRToneMapOffTheDevice(t *testing.T) {
	withToneMapSupport(t, true)
	withHardwareToneMap(t)
	shim, invocations := fakeShim(t)
	fakeFFmpegOnPath(t)
	src := filepath.Join(t.TempDir(), "in.mkv")
	if err := os.WriteFile(src, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	hdr := Rendition{Height: 1080, VideoCodec: "h264", VideoKbps: 6000, ColorTransfer: "smpte2084", ColorPrimaries: "bt2020"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, tc := range []struct {
		name       string
		policy     EncodePolicy
		wantNVIDIA bool
	}{
		{"hardware on", EncodePolicy{}, true},
		{"hardware off", EncodePolicy{SoftwareOnly: true}, false},
	} {
		runner := New()
		runner.SetHardeningShim(shim)
		runner.SetHardwareEncode(HardwareEncode{Accel: HWAccelNVENC})
		runner.SetEncodePolicy(tc.policy)

		prepared := runner.launchRendition(context.Background(), hdr)
		if gotHW := prepared.toneMapAccel != "" || prepared.deviceDecode; gotHW != tc.wantNVIDIA {
			t.Errorf("%s: tone-map %q device decode %v, want hardware %v", tc.name, prepared.toneMapAccel, prepared.deviceDecode, tc.wantNVIDIA)
		}

		starts := map[string]func(dir string) (*HLSProcess, error){
			"StartHLS":           func(dir string) (*HLSProcess, error) { return runner.StartHLS(ctx, src, dir, hdr) },
			"StartBackgroundHLS": func(dir string) (*HLSProcess, error) { return runner.StartBackgroundHLS(ctx, src, dir, hdr) },
			"StartHLSSegments": func(dir string) (*HLSProcess, error) {
				return runner.StartHLSSegments(ctx, src, dir, 0, 4, hdr)
			},
		}
		for name, start := range starts {
			proc, err := start(t.TempDir())
			if err != nil {
				t.Fatalf("%s %s: %v", tc.name, name, err)
			}
			_ = proc.Wait()
			call := lastShimCall(t, invocations)
			if got := slices.Contains(call, "--nvidia"); got != tc.wantNVIDIA {
				t.Errorf("%s %s: --nvidia = %v, want %v (%q)", tc.name, name, got, tc.wantNVIDIA, call)
			}
			target := shimTarget(t, call)
			if !tc.wantNVIDIA {
				for _, hw := range []string{"-hwaccel", "h264_nvenc", "-init_hw_device"} {
					if slices.Contains(target, hw) {
						t.Errorf("%s %s: argv carries %s: %q", tc.name, name, hw, target)
					}
				}
			}
		}
	}
}

// TestPinnedEncodePolicyOutlivesConsoleChange pins the session snapshot: a
// rendition pinned to the policy its session started under keeps that
// encoder, whatever the runner's policy is now.
func TestPinnedEncodePolicyOutlivesConsoleChange(t *testing.T) {
	runner := New()
	runner.SetHardwareEncode(HardwareEncode{Accel: HWAccelNVENC, HEVC: true})
	hevc := Rendition{Height: 2160, VideoCodec: "hevc", VideoKbps: 16000}.WithEncodePolicy(runner.EncodePolicy())

	runner.SetEncodePolicy(EncodePolicy{SoftwareOnly: true, H264Only: true, Quality: QualityLow})
	if args := policyArgs(runner, hevc); !containsArgPair(args, "-c:v", "hevc_nvenc") || !containsArgPair(args, "-cq", "23") || !containsArgPair(args, "-b:v", "0") || !containsArgPair(args, "-maxrate", "16000k") {
		t.Fatalf("pinned session argv after the Console change = %v, want hevc_nvenc at the balanced target", args)
	}
	unpinned := Rendition{Height: 1080, VideoCodec: "h264", VideoKbps: 6000}
	if args := policyArgs(runner, unpinned); !containsArgPair(args, "-c:v", "libx264") || !containsArgPair(args, "-crf", "28") {
		t.Fatalf("new session argv = %v, want libx264 at CRF 28", args)
	}
}

// TestQualityReachesEncryptedAndComposedEncodes pins Codex's finding on
// CORE-2452: the software-only encrypted and composed-picture encodes take the
// Console quality through their entry points, and pinned snapshots win.
func TestQualityReachesEncryptedAndComposedEncodes(t *testing.T) {
	shim, invocations := fakeShim(t)
	fakeFFmpegOnPath(t)
	dir := t.TempDir()
	audio := filepath.Join(dir, "song.mp3")
	graphics := filepath.Join(dir, "song.cdg")
	for _, path := range []string{audio, graphics} {
		if err := os.WriteFile(path, []byte("media"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	picture := ComposedPicture{GraphicsPath: graphics}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, tc := range []struct {
		quality string
		pinned  *EncodePolicy
		wantCRF string
	}{
		{QualityLow, nil, "28"},
		{QualityHigh, nil, "20"},
		{"", nil, "23"},
		{QualityLow, &EncodePolicy{Quality: QualityHigh}, "20"},
	} {
		runner := New()
		runner.SetHardeningShim(shim)
		runner.SetHardwareEncode(HardwareEncode{Accel: HWAccelNVENC})
		runner.SetEncodePolicy(EncodePolicy{Quality: tc.quality})
		video := Rendition{Height: 720, VideoCodec: "h264", VideoKbps: 3000, AudioCodec: "aac", AudioChannels: 2}
		if tc.pinned != nil {
			video = video.WithEncodePolicy(*tc.pinned)
		}
		starts := map[string]func() (*HLSProcess, error){
			"StartEncryptedHLSSegments": func() (*HLSProcess, error) {
				return runner.startEncryptedHLSSegments(ctx, "linux", testEncryptedEndpoint(), 0, 4, video)
			},
			"StartHLSSegments composed": func() (*HLSProcess, error) {
				return runner.StartHLSSegments(ctx, audio, t.TempDir(), 0, 4, video.WithComposedPicture(picture))
			},
			"StartBackgroundComposedHLS": func() (*HLSProcess, error) {
				return runner.StartBackgroundComposedHLS(ctx, audio, t.TempDir(), picture, video)
			},
		}
		for name, start := range starts {
			proc, err := start()
			if err != nil {
				t.Fatalf("%s quality %q: %v", name, tc.quality, err)
			}
			_ = proc.Wait()
			target := shimTarget(t, lastShimCall(t, invocations))
			if !containsArgPair(target, "-c:v", "libx264") || !containsArgPair(target, "-crf", tc.wantCRF) {
				t.Errorf("%s quality %q pinned %v: argv %q, want libx264 at CRF %s", name, tc.quality, tc.pinned, target, tc.wantCRF)
			}
		}
	}
}

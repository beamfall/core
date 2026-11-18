//go:build unix

package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestSpriteSheetsDecodeKeyframesOnHardwareWithSoftwareFallback proves a
// trickplay pass decodes keyframes only on one thread, on the verified VA-API
// or QSV device with the --dri grant, retries in software when the hardware
// pass fails and then decodes every frame when keyframe skipping fails, and
// stays in software for other accelerators and the software-only policy
// (CORE-2570).
func TestSpriteSheetsDecodeKeyframesOnHardwareWithSoftwareFallback(t *testing.T) {
	fakeFFmpegOnPath(t)
	src := filepath.Join(t.TempDir(), "in.mkv")
	if err := os.WriteFile(src, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "sheet-%04d.jpg")

	cases := []struct {
		name         string
		encode       HardwareEncode
		softwareOnly bool
		failOn       string            // a flag whose runs fail, "" for none
		want         []spriteSheetPass // per run, in order
	}{
		{"software", HardwareEncode{}, false, "", []spriteSheetPass{{keyframesOnly: true}}},
		{"vaapi", HardwareEncode{Accel: HWAccelVAAPI}, false, "", []spriteSheetPass{{HWAccelVAAPI, true}}},
		{"qsv", HardwareEncode{Accel: HWAccelQSV}, false, "", []spriteSheetPass{{HWAccelQSV, true}}},
		{"vaapi fails, software retry", HardwareEncode{Accel: HWAccelVAAPI}, false, "-hwaccel",
			[]spriteSheetPass{{HWAccelVAAPI, true}, {keyframesOnly: true}}},
		{"keyframe skip fails, every frame", HardwareEncode{Accel: HWAccelVAAPI}, false, "-skip_frame",
			[]spriteSheetPass{{HWAccelVAAPI, true}, {keyframesOnly: true}, {}}},
		{"nvenc stays software", HardwareEncode{Accel: HWAccelNVENC}, false, "", []spriteSheetPass{{keyframesOnly: true}}},
		{"software-only policy", HardwareEncode{Accel: HWAccelVAAPI}, true, "", []spriteSheetPass{{keyframesOnly: true}}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, tc := range cases {
		shim, invocations := fakeSpriteShim(t, tc.failOn)
		runner := New()
		runner.SetHardeningShim(shim)
		runner.SetHardwareEncode(tc.encode)
		runner.SetEncodePolicy(EncodePolicy{SoftwareOnly: tc.softwareOnly})
		if err := runner.SpriteSheets(ctx, src, 10, 320, 10, 10, dst); err != nil {
			t.Fatalf("%s: SpriteSheets: %v", tc.name, err)
		}
		calls := invocations()
		if len(calls) != len(tc.want) {
			t.Fatalf("%s: %d ffmpeg runs, want %d: %q", tc.name, len(calls), len(tc.want), calls)
		}
		for i, pass := range tc.want {
			hardware := pass.accel != ""
			flags := parseRecordedShim(t, calls[i])
			input := flags.target[:slices.Index(flags.target, "-i")]
			if j := slices.Index(input, "-threads"); j < 0 || input[j+1] != "1" {
				t.Errorf("%s run %d: decode is not single-threaded: %q", tc.name, i, input)
			}
			j := slices.Index(input, "-skip_frame")
			if keyframes := j >= 0 && input[j+1] == "nokey"; keyframes != pass.keyframesOnly {
				t.Errorf("%s run %d: keyframes only = %v, want %v: %q", tc.name, i, keyframes, pass.keyframesOnly, input)
			}
			if got := slices.Contains(input, "-hwaccel"); got != hardware {
				t.Errorf("%s run %d: hardware decode = %v, want %v: %q", tc.name, i, got, hardware, input)
			}
			if flags.dri != hardware || flags.nvidia {
				t.Errorf("%s run %d: grant dri=%v nvidia=%v, want dri=%v", tc.name, i, flags.dri, flags.nvidia, hardware)
			}
			if !slices.Contains(flags.target, "fps=1/10,scale=320:-2,tile=10x10") {
				t.Errorf("%s run %d: sheet filter changed: %q", tc.name, i, flags.target)
			}
		}
	}
}

// fakeSpriteShim records each shim argv and fails any run that carries the
// failOn flag.
func fakeSpriteShim(t *testing.T, failOn string) (string, func() [][]string) {
	t.Helper()
	shim, invocations := fakeShim(t)
	if failOn == "" {
		return shim, invocations
	}
	data, err := os.ReadFile(shim) // #nosec G304 -- shim is beneath a test-owned temporary directory
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Replace(string(data), "for arg in \"$@\"; do\n\tcase", "for arg in \"$@\"; do\n\tcase \"$arg\" in "+failOn+") exit 1 ;; esac\n\tcase", 1)
	if err := os.WriteFile(shim, []byte(script), 0o700); err != nil { // #nosec G306,G703 -- executable fixture path comes from fakeShim in a test-owned temporary directory
		t.Fatal(err)
	}
	return shim, invocations
}

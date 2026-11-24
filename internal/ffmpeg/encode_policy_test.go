package ffmpeg

import (
	"slices"
	"testing"
)

// policyArgs is the HLS argv a new session gets from runner for rendition.
func policyArgs(runner *Runner, rendition Rendition) []string {
	return hlsArgs("/src/movie.mkv", "/tmp/hls", runner.hardwareRendition(rendition))
}

// TestEncodePolicySoftwareOnlyForcesLibx264 pins CORE-2452: with the Console
// hardware switch off, a new session on a host with a verified encoder
// encodes in software, and turning it back on restores the encoder.
func TestEncodePolicySoftwareOnlyForcesLibx264(t *testing.T) {
	runner := New()
	runner.SetHardwareEncode(HardwareEncode{Accel: HWAccelNVENC, HEVC: true})
	rendition := Rendition{Height: 1080, VideoCodec: "h264", VideoKbps: 6000}

	if args := policyArgs(runner, rendition); !containsArgPair(args, "-c:v", "h264_nvenc") {
		t.Fatalf("default policy argv = %v, want h264_nvenc", args)
	}

	runner.SetEncodePolicy(EncodePolicy{SoftwareOnly: true})
	args := policyArgs(runner, rendition)
	if !containsArgPair(args, "-c:v", "libx264") || slices.Contains(args, "h264_nvenc") {
		t.Fatalf("software-only argv = %v, want libx264 and no h264_nvenc", args)
	}
	for _, flag := range []string{"-b:v", "-rc", "-forced-idr", "-hwaccel"} {
		if slices.Contains(args, flag) {
			t.Fatalf("software-only argv = %v, carries hardware flag %s", args, flag)
		}
	}
	for _, codec := range []string{"hevc", "av1"} {
		if runner.SupportsTranscodeCodec(codec) {
			t.Fatalf("software-only SupportsTranscodeCodec(%s) = true", codec)
		}
	}
	if !runner.SupportsTranscodeCodec("h264") {
		t.Fatal("software-only SupportsTranscodeCodec(h264) = false")
	}

	runner.SetEncodePolicy(EncodePolicy{})
	if args := policyArgs(runner, rendition); !containsArgPair(args, "-c:v", "h264_nvenc") {
		t.Fatalf("argv after hardware back on = %v, want h264_nvenc", args)
	}
}

// TestEncodePolicyH264OnlyWithholdsHEVC pins the Console codec setting: H.264
// only withholds the HEVC output the probe verified.
func TestEncodePolicyH264OnlyWithholdsHEVC(t *testing.T) {
	runner := New()
	runner.SetHardwareEncode(HardwareEncode{Accel: HWAccelQSV, HEVC: true, AV1: true})
	if !runner.SupportsTranscodeCodec("hevc") {
		t.Fatal("default SupportsTranscodeCodec(hevc) = false with a verified HEVC encoder")
	}
	runner.SetEncodePolicy(EncodePolicy{H264Only: true})
	for _, codec := range []string{"hevc", "av1"} {
		if runner.SupportsTranscodeCodec(codec) {
			t.Fatalf("H.264-only SupportsTranscodeCodec(%s) = true", codec)
		}
	}
	// H.264 sessions keep the hardware encoder.
	if args := policyArgs(runner, Rendition{Height: 720, VideoCodec: "h264"}); !containsArgPair(args, "-c:v", "h264_qsv") {
		t.Fatalf("H.264-only argv = %v, want h264_qsv", args)
	}
}

// TestEncodePolicyQualityArgs pins the encoder argv each Console quality gives
// a 4000 kb/s rung on every encoder path. Empty and balanced keep the
// defaults.
func TestEncodePolicyQualityArgs(t *testing.T) {
	tests := []struct {
		name    string
		accel   HWAccel
		quality string
		want    []string
	}{
		{"software default", "", "", []string{"-crf", "23", "-maxrate", "4000k"}},
		{"software balanced", "", QualityBalanced, []string{"-crf", "23", "-maxrate", "4000k"}},
		{"software low", "", QualityLow, []string{"-crf", "28", "-maxrate", "4000k"}},
		{"software high", "", QualityHigh, []string{"-crf", "20", "-maxrate", "4000k"}},
		{"nvenc balanced", HWAccelNVENC, QualityBalanced, []string{"-preset", "p4", "-cq", "23", "-b:v", "0", "-maxrate", "4000k"}},
		{"nvenc low", HWAccelNVENC, QualityLow, []string{"-preset", "p4", "-cq", "28", "-b:v", "0", "-maxrate", "4000k"}},
		{"nvenc high", HWAccelNVENC, QualityHigh, []string{"-preset", "p6", "-cq", "20", "-b:v", "0", "-maxrate", "4000k"}},
		{"qsv balanced", HWAccelQSV, "", []string{"-preset", "veryfast", "-b:v", "4000k", "-maxrate", "4001k"}},
		{"qsv low", HWAccelQSV, QualityLow, []string{"-preset", "veryfast", "-b:v", "3000k", "-maxrate", "4000k"}},
		{"qsv high", HWAccelQSV, QualityHigh, []string{"-preset", "medium", "-b:v", "4000k", "-maxrate", "4001k"}},
		{"amf low", HWAccelAMF, QualityLow, []string{"-quality", "speed", "-b:v", "3000k", "-maxrate", "4000k"}},
		{"amf high", HWAccelAMF, QualityHigh, []string{"-quality", "balanced", "-b:v", "4000k", "-maxrate", "4000k"}},
		{"vaapi low", HWAccelVAAPI, QualityLow, []string{"-b:v", "3000k", "-maxrate", "4000k"}},
		{"vaapi high", HWAccelVAAPI, QualityHigh, []string{"-b:v", "4000k", "-maxrate", "4000k"}},
		{"videotoolbox high", HWAccelVideoToolbox, QualityHigh, []string{"-b:v", "4000k", "-maxrate", "4000k"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := New()
			runner.SetHardwareEncode(HardwareEncode{Accel: tc.accel})
			runner.SetEncodePolicy(EncodePolicy{Quality: tc.quality})
			args := policyArgs(runner, Rendition{Height: 720, VideoCodec: "h264", VideoKbps: 4000})
			for i := 0; i < len(tc.want); i += 2 {
				if !containsArgPair(args, tc.want[i], tc.want[i+1]) {
					t.Fatalf("argv = %v, want %s %s", args, tc.want[i], tc.want[i+1])
				}
			}
		})
	}
}

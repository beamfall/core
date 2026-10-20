package ffmpeg

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const sampleProbeJSON = `{
    "streams": [
        {"codec_type": "video", "codec_name": "h264", "width": 1280, "height": 720},
        {"codec_type": "audio", "codec_name": "aac"}
    ],
    "format": {
        "format_name": "mov,mp4,m4a,3gp,3g2,mj2",
        "duration": "10.005333"
    }
}`

func TestParseProbe(t *testing.T) {
	probe, err := parseProbe([]byte(sampleProbeJSON), "/media/Test Pattern.mp4")
	if err != nil {
		t.Fatalf("parseProbe returned error: %v", err)
	}
	if probe.VideoCodec != "h264" {
		t.Fatalf("VideoCodec = %q, want h264", probe.VideoCodec)
	}
	if probe.Container != "mp4" {
		t.Fatalf("Container = %q, want mp4", probe.Container)
	}
	if probe.DurationSeconds != 10 {
		t.Fatalf("DurationSeconds = %d, want 10", probe.DurationSeconds)
	}
	if probe.VideoWidth != 1280 || probe.VideoHeight != 720 {
		t.Fatalf("frame size = %dx%d, want 1280x720", probe.VideoWidth, probe.VideoHeight)
	}
}

func TestNumericParsingBoundaries(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		unit string
		min  float64
		max  float64
		ok   bool
	}{
		{name: "lower boundary", raw: "-120 LUFS", unit: "LUFS", min: -120, max: 20, ok: true},
		{name: "upper boundary", raw: "20 LUFS", unit: "LUFS", min: -120, max: 20, ok: true},
		{name: "below lower", raw: "-120.0001 LUFS", unit: "LUFS", min: -120, max: 20},
		{name: "above upper", raw: "20.0001 LUFS", unit: "LUFS", min: -120, max: 20},
		{name: "missing unit", raw: "-14.7", unit: "LUFS", min: -120, max: 20},
		{name: "wrong unit", raw: "-14.7 dB", unit: "LUFS", min: -120, max: 20},
		{name: "trailing field", raw: "-14.7 LUFS ignored", unit: "LUFS", min: -120, max: 20},
		{name: "locale decimal", raw: "-14,7 LUFS", unit: "LUFS", min: -120, max: 20},
		{name: "nan", raw: "NaN LUFS", unit: "LUFS", min: -120, max: 20},
		{name: "infinity", raw: "+Inf LUFS", unit: "LUFS", min: -120, max: 20},
		{name: "overflow", raw: "1e309 LUFS", unit: "LUFS", min: -120, max: 20},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseBoundedNumber(tc.raw, tc.unit, tc.min, tc.max)
			if ok != tc.ok {
				t.Fatalf("parseBoundedNumber(%q, %q) ok = %v, want %v", tc.raw, tc.unit, ok, tc.ok)
			}
		})
	}
	for _, raw := range []string{"1.5", "9223372036854775808", "12 ignored"} {
		if _, ok := parseBoundedInteger(raw, -30720, 30720); ok {
			t.Fatalf("parseBoundedInteger(%q) accepted an ambiguous or overflowing value", raw)
		}
	}
	if _, ok := parseProbeTimestamp("Inf"); ok {
		t.Fatal("parseProbeTimestamp accepted infinity")
	}
	if stats, ok := parseEBUR128Summary("I: -14.7 dB\n"); ok || stats != (loudnessStats{}) {
		t.Fatalf("parseEBUR128Summary accepted the wrong unit: %+v, ok=%v", stats, ok)
	}
}

func TestParseProbeAttachedPicture(t *testing.T) {
	probe, err := parseProbe([]byte(`{
		"streams": [
			{"index": 2, "codec_type": "audio", "codec_name": "aac"},
			{"index": 7, "codec_type": "video", "codec_name": "mjpeg", "disposition": {"attached_pic": 1}}
		],
		"format": {"format_name": "mov,mp4,m4a,3gp,3g2,mj2", "duration": "10"}
	}`), "/media/audio.m4a")
	if err != nil {
		t.Fatal(err)
	}
	if !probe.Streams[1].AttachedPicture || probe.embeddedArtworkStream != 7 {
		t.Fatalf("attached picture = %v, selected stream = %d", probe.Streams[1].AttachedPicture, probe.embeddedArtworkStream)
	}
}

// io:ffmpeg-extract-embedded-artwork
func TestExtractEmbeddedArtworkUsesAttachedStreamAndCleansFailure(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		// #nosec G302 -- this is a test-owned executable fixture in a private temp directory.
		if err := os.Chmod(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ffprobe := filepath.Join(binDir, "ffprobe")
	probeScript := `#!/bin/sh
printf '%s\n' '{"streams":[{"index":2,"codec_type":"audio","codec_name":"aac"},{"index":7,"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}],"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"10"}}'
`
	writeExecutable(ffprobe, probeScript)
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	writeFFmpeg := func(body string) {
		t.Helper()
		writeExecutable(ffmpegPath, "#!/bin/sh\n"+body)
	}
	writeFFmpeg(`printf '%s\n' "$@" > "$BEAMFALL_TEST_FFMPEG_ARGS"
last=
for arg in "$@"; do last=$arg; done
printf jpeg > "$last"
`)
	t.Setenv("PATH", binDir)
	argsPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("BEAMFALL_TEST_FFMPEG_ARGS", argsPath)
	src := filepath.Join(t.TempDir(), "audio.m4a")
	if err := os.WriteFile(src, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "poster.jpg")
	runner := New()
	found, err := runner.ExtractEmbeddedArtwork(context.Background(), src, 1280, dst)
	if err != nil || !found {
		t.Fatalf("ExtractEmbeddedArtwork = (%v, %v)", found, err)
	}
	// #nosec G304 -- argsPath is constructed beneath a test-owned temporary directory.
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "-map\n0:7\n") {
		t.Fatalf("ffmpeg args did not select attached stream 7:\n%s", args)
	}

	writeFFmpeg(`last=
for arg in "$@"; do last=$arg; done
printf partial > "$last"
exit 9
`)
	failedDst := filepath.Join(t.TempDir(), "failed.jpg")
	found, err = runner.ExtractEmbeddedArtwork(context.Background(), src, 1280, failedDst)
	if err == nil || found {
		t.Fatalf("failed extraction = (%v, %v)", found, err)
	}
	leftovers, err := filepath.Glob(failedDst + ".*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("failed extraction left temp files: %v", leftovers)
	}
}

func TestParseProbeStreamInventory(t *testing.T) {
	tests := []struct {
		name        string
		fixturePath string
		mediaPath   string
		wantStreams []ProbeStream
		wantFonts   []FontAttachment
	}{
		{
			name:        "dv hdr10plus pgs multichannel font",
			fixturePath: "testdata/stream_inventory_dv_hdr10plus_pgs_71_font.ffprobe.json",
			mediaPath:   "/media/Feature.mkv",
			wantStreams: []ProbeStream{
				{
					Type:           "video",
					Codec:          "hevc",
					Profile:        "Main 10",
					Level:          153,
					Language:       "eng",
					Default:        true,
					BitDepth:       10,
					ColorTransfer:  "smpte2084",
					ColorPrimaries: "bt2020",
					HDR10Plus:      true,
					DoViProfile:    7,
					DoViBLCompatID: 6,
				},
				{
					Index:         1,
					Type:          "audio",
					Codec:         "truehd",
					Language:      "eng",
					Default:       true,
					Channels:      8,
					ChannelLayout: "7.1",
				},
				{
					Index:    2,
					Type:     "subtitle",
					Codec:    "hdmv_pgs_subtitle",
					Language: "spa",
					Forced:   true,
				},
				{
					Index: 3,
					Type:  "attachment",
					Codec: "ttf",
				},
			},
			wantFonts: []FontAttachment{
				{Filename: "Arial.ttf", MimeType: "application/x-truetype-font"},
			},
		},
		{
			name:        "subtitle dispositions",
			fixturePath: "testdata/stream_inventory_subtitle_dispositions.ffprobe.json",
			mediaPath:   "/media/Feature.mkv",
			wantStreams: []ProbeStream{
				{
					Type:  "video",
					Codec: "h264",
				},
				{
					Type:            "subtitle",
					Codec:           "subrip",
					Language:        "eng",
					Default:         true,
					HearingImpaired: true,
					VisualImpaired:  true,
					Commentary:      true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(tt.fixturePath)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			probe, err := parseProbe(data, tt.mediaPath)
			if err != nil {
				t.Fatalf("parseProbe returned error: %v", err)
			}
			if !reflect.DeepEqual(probe.Streams, tt.wantStreams) {
				t.Fatalf("Streams = %+v, want %+v", probe.Streams, tt.wantStreams)
			}
			if !reflect.DeepEqual(probe.FontAttachments, tt.wantFonts) {
				t.Fatalf("FontAttachments = %+v, want %+v", probe.FontAttachments, tt.wantFonts)
			}
		})
	}
}

func TestParseProbeVideoShapeAndCadence(t *testing.T) {
	tests := []struct {
		name string
		json string
		want ProbeStream
	}{
		{
			name: "anamorphic 480p 16:9",
			json: `{
				"streams": [{
					"codec_type": "video", "codec_name": "h264", "width": 720, "height": 480,
					"sample_aspect_ratio": "32:27", "display_aspect_ratio": "16:9",
					"field_order": "progressive", "r_frame_rate": "30000/1001", "avg_frame_rate": "30000/1001"
				}],
				"format": {"format_name": "matroska,webm", "duration": "60.0"}
			}`,
			want: ProbeStream{
				Type: "video", Codec: "h264",
				SampleAspectRatio: "32:27", DisplayAspectRatio: "16:9",
				FieldOrder: "progressive", FrameRate: "30000/1001", AverageFrameRate: "30000/1001",
			},
		},
		{
			name: "interlaced",
			json: `{
				"streams": [{
					"codec_type": "video", "codec_name": "mpeg2video", "width": 720, "height": 480,
					"sample_aspect_ratio": "1:1", "display_aspect_ratio": "3:2",
					"field_order": "tt", "r_frame_rate": "30000/1001", "avg_frame_rate": "30000/1001"
				}],
				"format": {"format_name": "mpeg", "duration": "60.0"}
			}`,
			want: ProbeStream{
				Type: "video", Codec: "mpeg2video",
				SampleAspectRatio: "1:1", DisplayAspectRatio: "3:2",
				FieldOrder: "tt", FrameRate: "30000/1001", AverageFrameRate: "30000/1001",
			},
		},
		{
			name: "vfr",
			json: `{
				"streams": [{
					"codec_type": "video", "codec_name": "h264", "width": 1280, "height": 720,
					"sample_aspect_ratio": "1:1", "display_aspect_ratio": "16:9",
					"field_order": "progressive", "r_frame_rate": "30000/1001", "avg_frame_rate": "24000/1001"
				}],
				"format": {"format_name": "mov,mp4,m4a,3gp,3g2,mj2", "duration": "60.0"}
			}`,
			want: ProbeStream{
				Type: "video", Codec: "h264",
				SampleAspectRatio: "1:1", DisplayAspectRatio: "16:9",
				FieldOrder: "progressive", FrameRate: "30000/1001", AverageFrameRate: "24000/1001",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe, err := parseProbe([]byte(tt.json), "/media/source.mkv")
			if err != nil {
				t.Fatalf("parseProbe returned error: %v", err)
			}
			got := probe.Streams[0]
			if got.Type != tt.want.Type || got.Codec != tt.want.Codec ||
				got.SampleAspectRatio != tt.want.SampleAspectRatio ||
				got.DisplayAspectRatio != tt.want.DisplayAspectRatio ||
				got.FieldOrder != tt.want.FieldOrder ||
				got.FrameRate != tt.want.FrameRate ||
				got.AverageFrameRate != tt.want.AverageFrameRate {
				t.Fatalf("stream metadata = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseProbeChapters(t *testing.T) {
	data := []byte(`{
		"streams": [{"codec_type": "audio", "codec_name": "aac"}],
		"chapters": [
			{"start_time": "0.000000", "end_time": "64.400000", "tags": {"title": "Opening"}},
			{"start_time": "64.400000", "end_time": "128.900000", "tags": {"TITLE": "The Door"}}
		],
		"format": {
			"format_name": "mov,mp4,m4a,3gp,3g2,mj2",
			"duration": "128.900000"
		}
	}`)
	probe, err := parseProbe(data, "/media/Novel.m4b")
	if err != nil {
		t.Fatalf("parseProbe returned error: %v", err)
	}
	if len(probe.Chapters) != 2 {
		t.Fatalf("len(Chapters) = %d, want 2", len(probe.Chapters))
	}
	if probe.Chapters[0] != (ProbeChapter{StartSeconds: 0, EndSeconds: 64.4, Title: "Opening"}) {
		t.Fatalf("chapter 0 = %+v, want Opening 0-64.4", probe.Chapters[0])
	}
	if probe.Chapters[1] != (ProbeChapter{StartSeconds: 64.4, EndSeconds: 128.9, Title: "The Door"}) {
		t.Fatalf("chapter 1 = %+v, want The Door 64.4-128.9", probe.Chapters[1])
	}
}

func TestRenditionArgsCapScaleAndBitrateWithoutUpscaling(t *testing.T) {
	if got := (Rendition{}).args(); len(got) != 0 {
		t.Fatalf("zero rendition args = %v, want none (source size)", got)
	}
	got := Rendition{Height: 720, VideoKbps: 4000}.args()
	want := []string{"-vf", "scale=-2:min(ih\\,720)", "-maxrate", "4000k", "-bufsize", "8000k"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestRenditionToneMapArgsForHDRSources(t *testing.T) {
	// The tone-map head is jellyfin-ffmpeg's tonemapx filter, NOT the software
	// `tonemap` filter: `bt2390` is not one of that filter's modes, so the
	// previous zscale+tonemap recipe died at filter-option parse on every build
	//. TestProductionToneMapChainRunsOrIsRefused
	// is what proves this string is runnable; this test only pins its shape.
	wantFilter := "tonemapx=tonemap=bt2390:desat=0:peak=1000:t=bt709:m=bt709:p=bt709:range=tv,scale=-2:min(ih\\,720),format=yuv420p"
	tests := []struct {
		name string
		r    Rendition
		want []string
	}{
		{"hdr10 pq", Rendition{Height: 720, ColorTransfer: "smpte2084"}, []string{"-vf", wantFilter}},
		{"hlg", Rendition{Height: 720, ColorTransfer: "arib-std-b67"}, []string{"-vf", wantFilter}},
		{"dolby vision profile 8 base layer", Rendition{Height: 720, DoViProfile: 8, DoViBLCompatID: 6}, []string{"-vf", wantFilter}},
		{"sdr", Rendition{Height: 720}, []string{"-vf", "scale=-2:min(ih\\,720)"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.args(); !equalArgs(got, tc.want) {
				t.Fatalf("args = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRenditionProbeDrivenVideoFilters(t *testing.T) {
	anamorphic, err := parseProbe([]byte(`{
		"streams": [{
			"codec_type": "video", "codec_name": "h264", "width": 720, "height": 480,
			"sample_aspect_ratio": "32:27", "field_order": "progressive"
		}],
		"format": {"format_name": "matroska,webm", "duration": "60.0"}
	}`), "/media/anamorphic.mkv")
	if err != nil {
		t.Fatalf("parse anamorphic probe: %v", err)
	}
	interlaced, err := parseProbe([]byte(`{
		"streams": [{
			"codec_type": "video", "codec_name": "mpeg2video", "width": 720, "height": 480,
			"sample_aspect_ratio": "1:1", "field_order": "tt"
		}],
		"format": {"format_name": "matroska,webm", "duration": "60.0"}
	}`), "/media/interlaced.mkv")
	if err != nil {
		t.Fatalf("parse interlaced probe: %v", err)
	}
	progressive, err := parseProbe([]byte(`{
		"streams": [{
			"codec_type": "video", "codec_name": "h264", "width": 1280, "height": 720,
			"sample_aspect_ratio": "1:1", "field_order": "progressive"
		}],
		"format": {"format_name": "matroska,webm", "duration": "60.0"}
	}`), "/media/progressive.mkv")
	if err != nil {
		t.Fatalf("parse progressive probe: %v", err)
	}

	tests := []struct {
		name string
		s    ProbeStream
		want []string
	}{
		{"anamorphic", anamorphic.Streams[0], []string{"-vf", "scale=-2:min(ih\\,480),setsar=1"}},
		{"interlaced", interlaced.Streams[0], []string{"-vf", "yadif=deint=interlaced,scale=-2:min(ih\\,480)"}},
		{"progressive square", progressive.Streams[0], []string{"-vf", "scale=-2:min(ih\\,480)"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := Rendition{Height: 480, SampleAspectRatio: tc.s.SampleAspectRatio, FieldOrder: tc.s.FieldOrder}
			if got := r.args(); !equalArgs(got, tc.want) {
				t.Fatalf("args = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRenditionRefusesDolbyVisionProfile5(t *testing.T) {
	if !((Rendition{DoViProfile: 5}).ToneMapUnavailable()) {
		t.Fatal("Dolby Vision profile 5 must refuse full-video transcode")
	}
	if (Rendition{DoViProfile: 5, CopyVideo: true}).ToneMapUnavailable() {
		t.Fatal("Dolby Vision profile 5 audio-only transcode copies video and should not be refused")
	}
	if (Rendition{DoViProfile: 8}).ToneMapUnavailable() {
		t.Fatal("Dolby Vision profile 8 should tone-map the HDR-compatible base layer")
	}
}

// TestRenditionAudioCopyArgs asserts the audio-only-transcode rendition copies
// the video stream (`-c:v copy`, no re-encode) and emits no scale/bitrate args
// (a copied stream cannot be rescaled or capped). The audio still transcodes —
// the StartHLS arg builder appends `-c:a aac` unconditionally — so the
// rendition only owns the video side of the decision.
func TestRenditionAudioCopyArgs(t *testing.T) {
	r := Rendition{CopyVideo: true, Height: 1080, VideoKbps: 8000}
	if got, want := r.videoArgs(), []string{"-c:v", "copy"}; !equalArgs(got, want) {
		t.Fatalf("videoArgs = %v, want %v", got, want)
	}
	if got := r.args(); len(got) != 0 {
		t.Fatalf("audio-copy args = %v, want none (no rescale/cap on a copied stream)", got)
	}
}

func TestRenditionAudioTargetArgs(t *testing.T) {
	gain := -5.5
	tests := []struct {
		name string
		r    Rendition
		want []string
	}{
		{
			name: "eac3 5.1",
			r:    Rendition{AudioCodec: "eac3", AudioChannels: 6},
			want: []string{"-c:a", "eac3", "-ac", "6"},
		},
		{
			name: "passthrough copy",
			r:    Rendition{AudioCodec: "copy", AudioChannels: 8, AudioGainDB: &gain},
			want: []string{"-c:a", "copy"},
		},
		{
			name: "aac stereo downmix with loudness gain",
			r:    Rendition{AudioCodec: "aac", AudioChannels: 2, AudioGainDB: &gain},
			want: []string{"-c:a", "aac", "-ac", "2", "-af", "aresample=matrix_encoding=dplii,volume=-5.5dB"},
		},
		{
			name: "legacy default is aac stereo downmix",
			r:    Rendition{},
			want: []string{"-c:a", "aac", "-ac", "2", "-af", "aresample=matrix_encoding=dplii"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.audioArgs(); !equalArgs(got, tc.want) {
				t.Fatalf("audioArgs = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRenditionEncodeVideoArgs guards the re-encode path's video selection: the
// libx264 block moved out of StartHLS into videoArgs(), so it must still emit
// the same encoder parameters.
func TestRenditionEncodeVideoArgs(t *testing.T) {
	want := []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p"}
	if got := (Rendition{Height: 720, VideoKbps: 4000}).videoArgs(); !equalArgs(got, want) {
		t.Fatalf("videoArgs = %v, want %v", got, want)
	}
}

func TestStartHLSLinearReencodeForcesKeyframes(t *testing.T) {
	args := hlsArgs("/src/movie.mkv", "/tmp/hls", Rendition{})
	if !containsArgPair(args, "-force_key_frames", "expr:gte(t,n_forced*4)") {
		t.Fatalf("linear HLS args missing forced keyframes: %v", args)
	}
	copyArgs := hlsArgs("/src/movie.mkv", "/tmp/hls", Rendition{CopyVideo: true})
	if containsArg(copyArgs, "-force_key_frames") {
		t.Fatalf("audio-copy linear HLS args forced keyframes on copied video: %v", copyArgs)
	}
}

func TestHLSArgsMapSelectedAudioStream(t *testing.T) {
	selected := 2
	plain := hlsArgs("/src/movie.mkv", "/tmp/hls", Rendition{AudioStreamIndex: &selected})
	if !containsArgSequence(plain, "-map", "0:v:0", "-map", "0:2") {
		t.Fatalf("plain HLS argv = %v, want [-map 0:v:0 -map 0:2]", plain)
	}

	burn := hlsArgs("/src/movie.mkv", "/tmp/hls", Rendition{AudioStreamIndex: &selected}.WithBurnSubtitle(3))
	if !containsArgSequence(burn, "-map", "[vsub]", "-map", "0:2") {
		t.Fatalf("burn-in HLS argv = %v, want [-map [vsub] -map 0:2]", burn)
	}

	legacy := hlsArgs("/src/movie.mkv", "/tmp/hls", Rendition{})
	if containsArg(legacy, "-map") {
		t.Fatalf("nil audio selection changed legacy HLS argv: %v", legacy)
	}
}

func TestRenditionHardwareVideoArgs(t *testing.T) {
	tests := []struct {
		name  string
		accel HWAccel
		want  []string
	}{
		{"qsv", HWAccelQSV, []string{"-c:v", "h264_qsv"}},
		{"vaapi", HWAccelVAAPI, []string{"-c:v", "h264_vaapi"}},
		{"nvenc", HWAccelNVENC, []string{"-c:v", "h264_nvenc"}},
		{"videotoolbox", HWAccelVideoToolbox, []string{"-c:v", "h264_videotoolbox"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Rendition{hwAccel: tc.accel}).videoArgs(); !equalArgs(got, tc.want) {
				t.Fatalf("videoArgs = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRenditionHEVCHardwareVideoArgs(t *testing.T) {
	tests := []struct {
		name  string
		accel HWAccel
		want  []string
	}{
		{"qsv", HWAccelQSV, []string{"-c:v", "hevc_qsv"}},
		{"vaapi", HWAccelVAAPI, []string{"-c:v", "hevc_vaapi"}},
		{"nvenc", HWAccelNVENC, []string{"-c:v", "hevc_nvenc"}},
		{"videotoolbox", HWAccelVideoToolbox, []string{"-c:v", "hevc_videotoolbox"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Rendition{VideoCodec: "hevc", hwAccel: tc.accel}).videoArgs(); !equalArgs(got, tc.want) {
				t.Fatalf("videoArgs = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRenditionHardwareUploadFilters(t *testing.T) {
	qsv := Rendition{Height: 720, hwAccel: HWAccelQSV}.args()
	if want := []string{"-vf", "scale=-2:min(ih\\,720),format=nv12"}; !equalArgs(qsv, want) {
		t.Fatalf("QSV args = %v, want %v", qsv, want)
	}
	vaapi := Rendition{Height: 720, hwAccel: HWAccelVAAPI}
	if want := []string{"-vaapi_device", vaapiDevicePath()}; !equalArgs(vaapi.inputArgs(), want) {
		t.Fatalf("VAAPI inputArgs = %v, want %v", vaapi.inputArgs(), want)
	}
	if got, want := vaapi.args(), []string{"-vf", "scale=-2:min(ih\\,720),format=nv12,hwupload"}; !equalArgs(got, want) {
		t.Fatalf("VAAPI args = %v, want %v", got, want)
	}
}

func equalArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func containsArgPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func containsArgSequence(args []string, want ...string) bool {
	for i := 0; i+len(want) <= len(args); i++ {
		if equalArgs(args[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

func TestParseProbeRejectsNonMedia(t *testing.T) {
	if _, err := parseProbe([]byte(`{"format":{"format_name":"tty"},"streams":[]}`), "/media/notes.txt"); err == nil {
		t.Fatal("parseProbe accepted a probe with no video stream or duration")
	}
}

// TestCheckPathRejectsOptionLookingPaths ensures that paths beginning with "-"
// are rejected before being forwarded to ffmpeg/ffprobe, preventing
// option-injection attacks.
func TestCheckPathRejectsOptionLookingPaths(t *testing.T) {
	bad := []string{
		"-",
		"-input",
		"--some-flag",
		"-rf",
	}
	for _, p := range bad {
		if err := checkPath(p); err == nil {
			t.Errorf("checkPath(%q) returned nil, want error", p)
		}
	}
	good := []string{
		"/media/file.mp4",
		"./relative/path.mkv",
		"normal.avi",
		"/tmp/output dir/clip.m4s",
	}
	for _, p := range good {
		if err := checkPath(p); err != nil {
			t.Errorf("checkPath(%q) returned %v, want nil", p, err)
		}
	}
}

func TestContainerFrom(t *testing.T) {
	cases := []struct {
		formatName string
		path       string
		want       string
	}{
		{"mov,mp4,m4a,3gp,3g2,mj2", "a.mp4", "mp4"},
		{"mov,mp4,m4a,3gp,3g2,mj2", "a.m4v", "m4v"},
		{"mov,mp4,m4a,3gp,3g2,mj2", "a.mov", "mov"},
		{"matroska,webm", "a.mkv", "matroska"},
		{"matroska,webm", "a.webm", "webm"},
		{"avi", "a.avi", "avi"},
	}
	for _, c := range cases {
		if got := containerFrom(c.formatName, c.path); got != c.want {
			t.Errorf("containerFrom(%q, %q) = %q, want %q", c.formatName, c.path, got, c.want)
		}
	}
}

// TestParseWaveformAmplitudes pins the astats ametadata parse + dBFS→linear
// conversion: one amplitude per RMS_level line, "-inf" silence is
// 0, surrounding ametadata header lines are ignored, and a level above 0 dBFS
// clamps to 1.
func TestParseWaveformAmplitudes(t *testing.T) {
	out := `frame:0    pts:0       pts_time:0
lavfi.astats.Overall.RMS_level=0.000000
frame:1    pts:1024    pts_time:0.064
lavfi.astats.Overall.RMS_level=-20.000000
frame:2    pts:2048    pts_time:0.128
lavfi.astats.Overall.RMS_level=-inf
frame:3    pts:3072    pts_time:0.192
lavfi.astats.Overall.RMS_level=3.000000
`
	got := parseWaveformAmplitudes(out)
	want := []float64{1, 0.1, 0, 1} // 0 dB→1, -20 dB→0.1, -inf→0, +3 dB clamps to 1
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 0.0001 {
			t.Fatalf("amplitudes = %v, want %v", got, want)
		}
	}
	if len(parseWaveformAmplitudes("no rms lines here\nframe:0")) != 0 {
		t.Fatal("parseWaveformAmplitudes found amplitudes in output with no RMS_level lines")
	}
}

// TestParseSilenceSpans pins the silencedetect stderr parse (ADR-0052 §2.4(a)):
// start/end pairs become {start,end} spans, the trailing "| silence_duration"
// field on the end line is stripped, the "[silencedetect @ …]" log prefix is
// ignored, and a dangling start with no matching end (silence to EOF) is dropped
// rather than guessed.
func TestParseSilenceSpans(t *testing.T) {
	out := `[silencedetect @ 0x55a0] silence_start: 12.34
[silencedetect @ 0x55a0] silence_end: 15.6 | silence_duration: 3.26
size=N/A time=00:00:20.00 bitrate=N/A speed= 40x
[silencedetect @ 0x55a0] silence_start: 60.0
[silencedetect @ 0x55a0] silence_end: 63.2 | silence_duration: 3.2
[silencedetect @ 0x55a0] silence_start: 90.5
`
	got := parseSilenceSpans(out)
	want := [][2]float64{
		{12.34, 15.6},
		{60, 63.2},
	}
	if len(got) != len(want) {
		t.Fatalf("spans = %+v, want %+v", got, want)
	}
	for i := range want {
		if math.Abs(got[i][0]-want[i][0]) > 0.0001 ||
			math.Abs(got[i][1]-want[i][1]) > 0.0001 {
			t.Fatalf("spans = %+v, want %+v", got, want)
		}
	}
	// No silencedetect output at all → no spans (analysed, nothing quiet).
	if got := parseSilenceSpans("size=N/A time=00:00:20.00\nframe:0"); len(got) != 0 {
		t.Fatalf("parseSilenceSpans found %+v in output with no events, want none", got)
	}
	// A malformed start token must not pair with the next end line.
	if got := parseSilenceSpans("silence_start: notanumber\nsilence_end: 5.0"); len(got) != 0 {
		t.Fatalf("parseSilenceSpans paired a malformed start, got %+v", got)
	}
}

// TestParseEBUR128Summary pins the ebur128 stderr summary parse (ADR-0098 area
// 3): the I:/LRA:/Peak: data lines yield integrated loudness, range, and true
// peak; the headings and threshold lines are ignored; output with no integrated
// line reports ok=false (a real measurement is marked by the I: line).
func TestParseEBUR128Summary(t *testing.T) {
	out := `[Parsed_ebur128_0 @ 0x55a0] Summary:

  Integrated loudness:
    I:         -14.7 LUFS
    Threshold: -24.9 LUFS

  Loudness range:
    LRA:         6.2 LU
    Threshold: -34.8 LUFS
    LRA low:   -18.1 LUFS
    LRA high:  -11.9 LUFS

  True peak:
    Peak:       -1.1 dBFS
`
	stats, ok := parseEBUR128Summary(out)
	if !ok {
		t.Fatal("parseEBUR128Summary reported no summary, want a measurement")
	}
	if math.Abs(stats.IntegratedLUFS-(-14.7)) > 1e-9 {
		t.Fatalf("integrated = %v, want -14.7", stats.IntegratedLUFS)
	}
	if math.Abs(stats.LoudnessRangeLU-6.2) > 1e-9 {
		t.Fatalf("range = %v, want 6.2", stats.LoudnessRangeLU)
	}
	if math.Abs(stats.TruePeakDBTP-(-1.1)) > 1e-9 {
		t.Fatalf("truePeak = %v, want -1.1", stats.TruePeakDBTP)
	}
	if _, ok := parseEBUR128Summary("no summary here\nframe:0"); ok {
		t.Fatal("parseEBUR128Summary found a measurement in output with no I: line")
	}
}

// TestLoudnessFromTags pins the trusted-tag recovery (ADR-0098 area 3): a
// ReplayGain track-gain string and an R128 Q7.8 integer both recover an
// integrated loudness as reference−gain with FromTags=true; absent/garbage tags
// report no measurement so the runner falls through to ebur128 analysis.
func TestLoudnessFromTags(t *testing.T) {
	// ReplayGain: -18 reference, "-6.94 dB" gain → integrated = -18 - (-6.94) = -11.06.
	rg, ok := loudnessFromTags(map[string]string{"replaygain_track_gain": "-6.94 dB"})
	if !ok || !rg.FromTags || math.Abs(rg.IntegratedLUFS-(-11.06)) > 1e-9 {
		t.Fatalf("replaygain recovery = (%+v, %v), want integrated -11.06 fromTags", rg, ok)
	}
	// R128: -23 reference, gain 1280/256 = +5 dB → integrated = -23 - 5 = -28.
	r128, ok := loudnessFromTags(map[string]string{"r128_track_gain": "1280"})
	if !ok || !r128.FromTags || math.Abs(r128.IntegratedLUFS-(-28)) > 1e-9 {
		t.Fatalf("r128 recovery = (%+v, %v), want integrated -28 fromTags", r128, ok)
	}
	// No usable tag → no measurement (the runner then runs an ebur128 pass).
	if _, ok := loudnessFromTags(map[string]string{"title": "Song"}); ok {
		t.Fatal("loudnessFromTags recovered a measurement from tags with no gain")
	}
	if _, ok := loudnessFromTags(map[string]string{"replaygain_track_gain": "garbage"}); ok {
		t.Fatal("loudnessFromTags recovered a measurement from an unparseable gain")
	}
}

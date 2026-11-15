package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPipeRenditionArgsEncodeSoftwareFMP4ToStdout(t *testing.T) {
	audio := 1
	rendition := Rendition{Height: 720, VideoKbps: 3000, AudioCodec: "aac", AudioChannels: 2, AudioStreamIndex: &audio}
	args := pipeRenditionArgs(testPipeURL, 10*time.Second, rendition, 4)
	i := slices.Index(args, "-i")
	if i < 0 || args[i+1] != testPipeURL || slices.Index(args[i+1:], "-i") >= 0 {
		t.Fatalf("args lack their one input -i %s: %v", testPipeURL, args)
	}
	input := args[:i]
	for _, flag := range []string{"-nostdin", "-xerror"} {
		if !slices.Contains(input, flag) {
			t.Errorf("input options lack %s: %v", flag, input)
		}
	}
	for _, pair := range [][2]string{
		{"-protocol_whitelist", "http,tcp"},
		{"-max_redirects", "0"},
		{"-rw_timeout", "10000000"},
		{"-format_whitelist", pipeFormatAllowlist},
	} {
		j := slices.Index(input, pair[0])
		if j < 0 || input[j+1] != pair[1] {
			t.Errorf("input options lack %s %s: %v", pair[0], pair[1], input)
		}
	}
	output := args[i+2:]
	for _, pair := range [][2]string{
		{"-c:v", "libx264"},
		{"-force_key_frames", "expr:gte(t,n_forced*4)"},
		{"-c:a", "aac"},
	} {
		j := slices.Index(output, pair[0])
		if j < 0 || output[j+1] != pair[1] {
			t.Errorf("output options lack %s %s: %v", pair[0], pair[1], output)
		}
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-map 0:v:0 -map 0:1") {
		t.Errorf("args do not map the selected audio stream: %v", args)
	}
	if !strings.HasSuffix(joined, "-movflags frag_keyframe+empty_moov+default_base_moof -f mp4 pipe:1") {
		t.Errorf("args do not end in fragmented MP4 to pipe:1: %v", args)
	}
	for _, banned := range []string{"file", "faststart", "hls", "vaapi", "videotoolbox", "-progress"} {
		if strings.Contains(joined, banned) {
			t.Errorf("args contain %q: %v", banned, args)
		}
	}
	if slices.Contains(args, "-y") {
		t.Errorf("args overwrite a file output: %v", args)
	}
	if got := pipeRenditionArgs(testPipeURL, time.Second, rendition, 6); !slices.Contains(got, "expr:gte(t,n_forced*6)") {
		t.Errorf("keyframe cadence does not follow the segment length: %v", got)
	}
}

func TestRenditionToPipeRefusesUnsupportedRenditions(t *testing.T) {
	ctx := context.Background()
	local := PipeInput{URL: testPipeURL}
	plain := Rendition{Height: 720, VideoKbps: 3000, AudioCodec: "aac", AudioChannels: 2}
	var out bytes.Buffer
	cases := map[string]func() error{
		"remote": func() error {
			return New().RenditionToPipe(ctx, PipeInput{URL: "http://192.168.1.10:8080/token/source"}, plain, 4, &out)
		},
		"file":       func() error { return New().RenditionToPipe(ctx, PipeInput{URL: "file:///etc/hosts"}, plain, 4, &out) },
		"nil writer": func() error { return New().RenditionToPipe(ctx, local, plain, 4, nil) },
		"no cadence": func() error { return New().RenditionToPipe(ctx, local, plain, 0, &out) },
		"audio only": func() error { r := plain; r.AudioOnly = true; return New().RenditionToPipe(ctx, local, r, 4, &out) },
		"copy video": func() error { r := plain; r.CopyVideo = true; return New().RenditionToPipe(ctx, local, r, 4, &out) },
		"burn-in": func() error {
			r := plain
			r.burnSubtitleIndex = 1
			return New().RenditionToPipe(ctx, local, r, 4, &out)
		},
		"hw encode":   func() error { return New().RenditionToPipe(ctx, local, plain.withHWAccel(HWAccelVAAPI), 4, &out) },
		"dovi p5 hdr": func() error { r := plain; r.DoViProfile = 5; return New().RenditionToPipe(ctx, local, r, 4, &out) },
	}
	for name, run := range cases {
		if err := run(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	r := plain
	r.DoViProfile = 5
	if err := New().RenditionToPipe(ctx, local, r, 4, &out); !errors.Is(err, ErrToneMapRefused) {
		t.Errorf("dovi p5 err = %v, want ErrToneMapRefused", err)
	}
	if out.Len() != 0 {
		t.Errorf("refusals wrote %d bytes", out.Len())
	}
}

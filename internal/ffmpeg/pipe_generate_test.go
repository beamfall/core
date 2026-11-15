package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestPipeGenerateArgsScopeInputAndWriteNoFile checks every generated-asset
// variant reads only the one confined loopback input and names no output file.
func TestPipeGenerateArgsScopeInputAndWriteNoFile(t *testing.T) {
	const timeout = 10 * time.Second
	for name, tc := range map[string]struct {
		args   []string
		suffix string
	}{
		"sprite sheets": {pipeSpriteSheetArgs(testPipeURL, timeout, 10, 160, 10, 10), "-c:v mjpeg -f image2pipe pipe:1"},
		"frame grab":    {pipeFrameGrabArgs(testPipeURL, timeout, 12.5, 320), "-c:v mjpeg -f image2pipe pipe:1"},
		"sample clip":   {pipeSampleClipArgs(testPipeURL, timeout, []float64{1, 5}, 2, 320, 15), "-movflags frag_keyframe+empty_moov+default_base_moof -f mp4 pipe:1"},
		"audio analysis": {
			pipeAudioAnalysisArgs(testPipeURL, timeout, pipeAnalysisLogArgs, "ebur128=peak=true:framelog=quiet"),
			"-map 0:a:0 -af ebur128=peak=true:framelog=quiet -f null -",
		},
	} {
		i := slices.Index(tc.args, "-i")
		if i < 0 || tc.args[i+1] != testPipeURL || slices.Index(tc.args[i+1:], "-i") >= 0 {
			t.Fatalf("%s: args lack their one input -i %s: %v", name, testPipeURL, tc.args)
		}
		input := tc.args[:i]
		for _, pair := range [][2]string{
			{"-protocol_whitelist", "http,tcp"},
			{"-max_redirects", "0"},
			{"-rw_timeout", "10000000"},
			{"-format_whitelist", pipeFormatAllowlist},
		} {
			j := slices.Index(input, pair[0])
			if j < 0 || input[j+1] != pair[1] {
				t.Errorf("%s: input options lack %s %s: %v", name, pair[0], pair[1], input)
			}
		}
		if !slices.Contains(input, "-nostdin") {
			t.Errorf("%s: args read stdin: %v", name, tc.args)
		}
		joined := strings.Join(tc.args, " ")
		if !strings.HasSuffix(joined, tc.suffix) {
			t.Errorf("%s: args do not end in %q: %v", name, tc.suffix, tc.args)
		}
		if strings.Contains(joined, "file") || strings.Contains(joined, "faststart") || slices.Contains(tc.args, "-y") {
			t.Errorf("%s: args name a file output: %v", name, tc.args)
		}
	}
	if args := pipeFrameGrabArgs(testPipeURL, timeout, 12.5, 320); slices.Index(args, "-ss") > slices.Index(args, "-i") {
		t.Errorf("frame grab seeks after opening the input: %v", args)
	}
}

func TestPipeGenerateRefusesBadArguments(t *testing.T) {
	var out bytes.Buffer
	ctx := context.Background()
	remote := PipeInput{URL: "http://192.168.1.10:8080/token/source"}
	local := PipeInput{URL: testPipeURL}
	for name, call := range map[string]func() error{
		"sheets remote":   func() error { return New().SpriteSheetsToPipe(ctx, remote, 10, 160, 10, 10, &out) },
		"sheets nil":      func() error { return New().SpriteSheetsToPipe(ctx, local, 10, 160, 10, 10, nil) },
		"grab file":       func() error { return New().FrameGrabToPipe(ctx, PipeInput{URL: "file:///etc/hosts"}, 1, 320, &out) },
		"grab nil":        func() error { return New().FrameGrabToPipe(ctx, local, 1, 320, nil) },
		"clip no offsets": func() error { return New().SampleClipToPipe(ctx, local, nil, 2, 320, 15, &out) },
		"clip remote":     func() error { return New().SampleClipToPipe(ctx, remote, []float64{1}, 2, 320, 15, &out) },
		"waveform remote": func() error { _, err := New().WaveformAmplitudesFromPipe(ctx, remote); return err },
		"silence remote":  func() error { _, err := New().SilenceSpansFromPipe(ctx, remote); return err },
		"loudness remote": func() error { _, _, _, err := New().LoudnessMeasureFromPipe(ctx, remote); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if out.Len() != 0 {
		t.Fatalf("refused runs wrote %d bytes", out.Len())
	}
}

func testJPEG(t *testing.T, w, h int, shade uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = shade + uint8(i%7)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSplitJPEGStreamSeparatesConcatenatedImages(t *testing.T) {
	images := [][]byte{testJPEG(t, 32, 24, 10), testJPEG(t, 16, 16, 90), testJPEG(t, 48, 8, 200)}
	got, err := SplitJPEGStream(bytes.Join(images, nil), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(images) {
		t.Fatalf("split %d images, want %d", len(got), len(images))
	}
	for i := range images {
		if !bytes.Equal(got[i], images[i]) {
			t.Fatalf("image %d differs after the split", i)
		}
		if _, err := jpeg.Decode(bytes.NewReader(got[i])); err != nil {
			t.Fatalf("image %d does not decode: %v", i, err)
		}
	}
	if got, err := SplitJPEGStream(nil, 3); err != nil || len(got) != 0 {
		t.Fatalf("empty stream = %d images, %v", len(got), err)
	}
}

func TestSplitJPEGStreamRefusesMalformedOrExcessStreams(t *testing.T) {
	one := testJPEG(t, 16, 16, 40)
	two := append(append([]byte(nil), one...), one...)
	for name, tc := range map[string]struct {
		data []byte
		max  int
	}{
		"too many":        {two, 1},
		"truncated":       {one[:len(one)-1], 2},
		"trailing bytes":  {append(append([]byte(nil), one...), 0x00, 0x01), 2},
		"not a jpeg":      {[]byte("plain text, not an image"), 2},
		"bare markers":    {[]byte{0xFF, 0xD8, 0xFF, 0xD9}, 2},
		"no image budget": {one, 0},
	} {
		if _, err := SplitJPEGStream(tc.data, tc.max); !errors.Is(err, ErrJPEGStream) {
			t.Errorf("%s: err = %v, want ErrJPEGStream", name, err)
		}
	}
}

package cdgrender

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// track is a minimal synthetic graphics stream: a colour table, a screen
// fill, a row of tiles, then idle packs to the given length.
func track(seconds int) []byte {
	pack := func(instruction byte, data ...byte) []byte {
		p := make([]byte, 24)
		p[0], p[1] = 0x09, instruction
		copy(p[4:20], data)
		return p
	}
	var out []byte
	out = append(out, pack(30, 0x00, 0x04, 0x3E, 0x30)...) // dark blue, amber
	out = append(out, pack(1, 0, 0)...)                    // fill with colour 0
	for col := byte(10); col < 40; col++ {
		tile := []byte{0, 1, 6, col}
		for range 12 {
			tile = append(tile, 0x2A)
		}
		out = append(out, pack(6, tile...)...)
	}
	for len(out) < seconds*300*24 {
		out = append(out, make([]byte, 24)...)
	}
	return out
}

// capped keeps the first n bytes, then fails every write as a closed pipe
// would once the encoder has gone.
type capped struct {
	buf bytes.Buffer
	n   int
}

var errGone = errors.New("reader gone")

func (c *capped) Write(p []byte) (int, error) {
	room := c.n - c.buf.Len()
	if room <= 0 {
		return 0, errGone
	}
	if len(p) > room {
		c.buf.Write(p[:room])
		return room, errGone
	}
	return c.buf.Write(p)
}

func TestOptionsArgs(t *testing.T) {
	got := Options{Width: 1280, Height: 720, FPS: 30, Start: 37.5, Tempo: 1.25}.args()
	want := []string{"oscillux-cdg-render", "--width", "1280", "--height", "720", "--fps", "30", "--start", "37.5", "--tempo", "1.25"}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestRenderWritesFramesUntilTheReaderGoes(t *testing.T) {
	const w, h, frames = 96, 64, 5
	out := &capped{n: w * h * 4 * frames}
	err := Render(context.Background(), bytes.NewReader(track(2)), out, Options{Width: w, Height: h, FPS: 30, Tempo: 1})
	if err != nil {
		t.Fatalf("Render = %v, want nil once the reader has gone", err)
	}
	if out.buf.Len() != out.n {
		t.Fatalf("wrote %d bytes, want %d", out.buf.Len(), out.n)
	}
	frame := out.buf.Bytes()[(frames-1)*w*h*4:]
	colours := map[[3]byte]bool{}
	for i := 0; i < len(frame); i += 4 {
		if frame[i+3] != 255 {
			t.Fatalf("pixel %d alpha = %d, want opaque", i/4, frame[i+3])
		}
		colours[[3]byte(frame[i:i+3])] = true
	}
	if len(colours) < 8 {
		t.Fatalf("last frame has %d colours, want a drawn picture", len(colours))
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	render := func() []byte {
		out := &capped{n: 48 * 32 * 4 * 3}
		if err := Render(context.Background(), bytes.NewReader(track(1)), out, Options{Width: 48, Height: 32, FPS: 30, Start: 0.5, Tempo: 1}); err != nil {
			t.Fatal(err)
		}
		return out.buf.Bytes()
	}
	if !bytes.Equal(render(), render()) {
		t.Fatal("two renders of the same stream and options differ")
	}
}

func TestRenderReportsRefusedOptions(t *testing.T) {
	err := Render(context.Background(), bytes.NewReader(track(1)), &capped{n: 1 << 20}, Options{Width: 0, Height: 64, FPS: 30, Tempo: 1})
	if err == nil || !strings.Contains(err.Error(), "presenter exited") {
		t.Fatalf("Render = %v, want the presenter's refusal", err)
	}
}

func TestRenderStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		// An endless sink: only the cancel can stop this render.
		done <- Render(ctx, bytes.NewReader(track(600)), io.Discard, Options{Width: 320, Height: 240, FPS: 30, Tempo: 1})
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Render = %v, want nil after cancel", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Render did not stop after the context ended")
	}
}

// A closed output reaches the module as EBADF, which Rust's stdout treats as
// a successful write; the render must still stop.
func TestRenderStopsWhenItsOutputIsClosed(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	_ = w.Close()
	done := make(chan error, 1)
	go func() {
		done <- Render(context.Background(), bytes.NewReader(track(1)), w, Options{Width: 64, Height: 48, FPS: 30, Tempo: 1})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Render = %v, want nil once its output is closed", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Render kept drawing into a closed output")
	}
}

// lyricTrack is shaped like a karaoke song at its busiest: a backdrop, lyric
// lines in a sung and an unsung colour with an outline, rewritten as XOR tiles
// on every pack so the window changes every frame, across twelve inked rows.
func lyricTrack(seconds int) []byte {
	pack := func(instruction byte, data ...byte) []byte {
		p := make([]byte, 24)
		p[0], p[1] = 0x09, instruction
		copy(p[4:20], data)
		return p
	}
	var out []byte
	out = append(out, pack(30, 0x00, 0x04, 0x3F, 0x3F, 0x3C, 0x0F, 0x00, 0x00)...) // navy, white, amber, black
	out = append(out, pack(1, 0, 0)...)
	colours := [][2]byte{{1, 3}, {2, 3}, {3, 1}}
	for i := 0; len(out) < seconds*300*24; i++ {
		c := colours[i%len(colours)]
		tile := []byte{c[0], c[1], byte(4 + i/44%12), byte(3 + i%44)}
		for r := range 12 {
			tile = append(tile, []byte{0x1E, 0x21, 0x3F, 0x21, 0x21, 0x00}[(i+r)%6])
		}
		out = append(out, pack(38, tile...)...)
	}
	return out
}

// frameCounter discards frames and fails once it has taken n bytes, as a
// closed encoder pipe would.
type frameCounter struct{ n int }

func (c *frameCounter) Write(p []byte) (int, error) {
	if c.n <= 0 {
		return 0, errGone
	}
	c.n -= len(p)
	return len(p), nil
}

// TestRenderRunsWellAboveRealTime holds the live composed path's budget
// (CORE-3260): a live HLS segment is served only once its frames are drawn,
// so the presenter must draw 960x540 frames at least twice as fast as they
// play, through the same runtime and streams production uses. It counts the
// process's CPU time, not wall time, so a loaded machine does not fail it. A runtime
// option that slowed every guest loop (WithCloseOnContextDone) once cut this
// to under real time and every other live segment timed out.
func TestRenderRunsWellAboveRealTime(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("timing floor: skipped under -short and -race")
	}
	const w, h, fps, frames = 960, 540, 30, 120
	stream := lyricTrack(30)
	if _, _, err := compile(); err != nil {
		t.Fatal(err)
	}
	if _, ok := processCPU(); !ok {
		t.Skip("timing floor: no process CPU clock on this platform")
	}
	best := time.Duration(1<<63 - 1)
	for range 3 {
		start, _ := processCPU()
		err := Render(context.Background(), bytes.NewReader(stream), &frameCounter{n: frames * w * h * 4}, Options{Width: w, Height: h, FPS: fps, Start: 10, Tempo: 1})
		if err != nil {
			t.Fatal(err)
		}
		end, _ := processCPU()
		best = min(best, end-start)
	}
	got := float64(frames) / best.Seconds()
	t.Logf("best of 3: %d frames at %dx%d in %v of CPU (%.0f fps)", frames, w, h, best, got)
	if got < 2*fps {
		t.Fatalf("presenter draws %.0f fps at %dx%d, want at least %d (twice real time)", got, w, h, 2*fps)
	}
}

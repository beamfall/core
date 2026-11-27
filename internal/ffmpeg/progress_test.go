//go:build unix

package ffmpeg

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestProgressWriterReportsOnlyForwardOutputTime(t *testing.T) {
	advances := 0
	w := &progressWriter{advance: func() { advances++ }}
	// Split across writes, with the N/A ffmpeg reports before the first frame,
	// a repeat and a regression that must not count as progress.
	for _, chunk := range []string{
		"frame=0\nout_time_us=N/A\nprogress=continue\nout_ti",
		"me_us=1000000\nprogress=continue\n",
		"out_time_us=1000000\nout_time_us=500000\n",
		"out_time_us=2000000\nprogress=end\n",
	} {
		if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if advances != 2 {
		t.Fatalf("advances = %d, want 2 (1s then 2s)", advances)
	}
}

func TestProgressWriterBoundsAnUnterminatedLine(t *testing.T) {
	w := &progressWriter{}
	big := make([]byte, 64<<10)
	for i := range big {
		big[i] = 'x'
	}
	if _, err := w.Write(big); err != nil {
		t.Fatal(err)
	}
	if len(w.line) > hlsProgressLineBytes {
		t.Fatalf("kept %d bytes of a partial line, cap %d", len(w.line), hlsProgressLineBytes)
	}
}

func TestHLSArgsRequestProgressOnStdout(t *testing.T) {
	if !requestsProgress(hlsArgs("/src/movie.mkv", "/out", Rendition{Height: 720, VideoKbps: 4000})) {
		t.Fatal("linear HLS argv does not ask for -progress pipe:1")
	}
	if requestsProgress([]string{"-v", "error", "-progress"}) {
		t.Fatal("a dangling -progress counted as a progress stream")
	}
}

// TestHLSProcessProgressAdvancesAndKillIsNotAStop proves a child that asks for
// -progress pipe:1 moves ProgressedAt as its output time advances, and that
// Kill ends it without marking the exit as requested.
func TestHLSProcessProgressAdvancesAndKillIsNotAStop(t *testing.T) {
	script := `printf 'out_time_us=N/A\nprogress=continue\n'; sleep 0.2; printf 'out_time_us=1000000\nprogress=continue\n'; exec sleep 30`
	cmd := exec.CommandContext(context.Background(), "sh", "-c", script, "sh", "-progress", "pipe:1") // #nosec G204 -- fixed test script
	p, err := startHLSProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	if !p.ReportsProgress() {
		t.Fatal("ReportsProgress = false for an argv with -progress pipe:1")
	}
	started := p.ProgressedAt()
	deadline := time.Now().Add(5 * time.Second)
	for !p.ProgressedAt().After(started) {
		if time.Now().After(deadline) {
			t.Fatal("ProgressedAt never advanced past the start time")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The stall watchdog subtracts ProgressedAt from now, so it must carry a
	// monotonic clock reading; without one a wall-clock jump fakes a stall.
	if at := p.ProgressedAt(); !strings.Contains(at.String(), " m=") {
		t.Fatalf("ProgressedAt %v has no monotonic clock reading", at)
	}

	p.Kill()
	if err := p.Wait(); err == nil {
		t.Fatal("Wait after Kill returned nil, want the signal exit")
	}
	if p.StopRequested() {
		t.Fatal("Kill marked the exit as requested")
	}
}

func TestHLSProcessWithoutProgressArgsDoesNotReport(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "exit 0")
	p, err := startHLSProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	_ = p.Wait()
	if p.ReportsProgress() {
		t.Fatal("ReportsProgress = true without -progress pipe:1")
	}
}

func TestProgressWriterParsesOutTimeAndSpeed(t *testing.T) {
	var got []ConvertProgress
	w := &progressWriter{onProgress: func(p ConvertProgress) { got = append(got, p) }}
	chunks := []string{
		"frame=0\nout_time_us=N/A\nspeed=N/A\nprogress=continue\n",
		"out_time_us=1000000\nspeed= 14.1x\nprogress=co",
		"ntinue\nout_time_us=500000\nspeed=14.1x\nprogress=continue\n",
		"out_time_us=2500000\nspeed=N/A\nprogress=continue\n",
		"out_time_us=4000000\nspeed=18.5X\nprogress=end\n",
		"out_time_us=5000000\nspeed=20.25x",
	}
	for _, chunk := range chunks {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	w.flush()

	want := []ConvertProgress{
		{OutTime: 1 * time.Second, Speed: 14.1},
		{OutTime: 2500 * time.Millisecond, Speed: 0},
		{OutTime: 4 * time.Second, Speed: 18.5},
		{OutTime: 5 * time.Second, Speed: 20.25},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d progress events %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseProgressSpeedBoundaries(t *testing.T) {
	for raw, want := range map[string]float64{
		"14.1x":   14.1,
		" 40x":    40,
		"0.75X":   0.75,
		"0x":      0,
		"1.5e+01": 15,
	} {
		got, ok := parseProgressSpeed(raw)
		if !ok || got != want {
			t.Fatalf("parseProgressSpeed(%q) = %v, %v; want %v, true", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "N/A", "n/a", "-1.5x", "NaNx", "Infx", "bad"} {
		if got, ok := parseProgressSpeed(raw); ok {
			t.Fatalf("parseProgressSpeed(%q) = %v, true; want false", raw, got)
		}
	}
}

func TestRunDetailWithProgressStreamsAndPreservesStderr(t *testing.T) {
	var got []ConvertProgress
	script := `printf 'out_time_us=1500000\nspeed=12.4x\nprogress=continue\nout_time_us=3000000\nspeed=15.0x\nprogress=end\n'`
	cmd := exec.CommandContext(context.Background(), "sh", "-c", script) // #nosec G204 -- fixed test script
	detail, err := New().runDetailWithProgress(context.Background(), cmd, func(p ConvertProgress) {
		got = append(got, p)
	})
	if err != nil || detail != "" {
		t.Fatalf("runDetailWithProgress = %q, %v; want clean success", detail, err)
	}
	if len(got) != 2 || got[0] != (ConvertProgress{OutTime: 1500 * time.Millisecond, Speed: 12.4}) ||
		got[1] != (ConvertProgress{OutTime: 3 * time.Second, Speed: 15}) {
		t.Fatalf("progress events = %+v", got)
	}

	failCmd := exec.CommandContext(context.Background(), "sh", "-c", `echo "boom" >&2; exit 2`) // #nosec G204 -- fixed test script
	detail, err = New().runDetailWithProgress(context.Background(), failCmd, func(ConvertProgress) {})
	if err == nil || detail != "boom" {
		t.Fatalf("failed runDetailWithProgress = %q, %v; want boom + err", detail, err)
	}
}

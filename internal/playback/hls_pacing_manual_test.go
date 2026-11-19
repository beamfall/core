package playback

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
)

func TestPacingMacOSReceipt(t *testing.T) {
	src := os.Getenv("CORE0022_MEDIA")
	if runtime.GOOS != "darwin" || src == "" {
		t.Skip("requires macOS and CORE0022_MEDIA")
	}
	duration, err := strconv.Atoi(os.Getenv("CORE0022_DURATION"))
	if err != nil || duration < 120 || src == "" {
		t.Fatal("set CORE0022_MEDIA and CORE0022_DURATION (whole seconds, at least 120)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	runner := ffmpeg.New()
	runner.SetHardwareAccel(ffmpeg.HWAccelVideoToolbox, false)
	s := NewSessionsInDir(runner, t.TempDir(), nil)
	t.Cleanup(s.Close)
	key := SessionKey{SessionID: "receipt", MediaID: "receipt"}
	artifact := func(name string) string {
		t.Helper()
		path, err := s.FilePath(ctx, key, src, duration, 1920, 1080, false, "1080p/"+name)
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	segment := func(n int) string { return artifact(fmt.Sprintf("seg%05d.m4s", n)) }
	first := segment(0)
	init := artifact("init.mp4")
	dir := filepath.Dir(first)
	session := registeredSession(t, s, key)
	observe := func(playhead int, stopped bool) (string, int) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			out, err := exec.CommandContext(ctx, "/bin/ps", "-axww", "-o", "pid=,stat=,command=").Output()
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range strings.Split(string(out), "\n") {
				if !strings.Contains(row, filepath.Join(filepath.Dir(dir), ".encoder-")) || !strings.HasSuffix(row, "/live.m3u8") {
					continue
				}
				fields := strings.Fields(row)
				if len(fields) < 3 || strings.HasPrefix(fields[1], "T") != stopped {
					continue
				}
				// #nosec G204 -- PID read from ps for the session encoder.
				status, err := exec.CommandContext(ctx, "/bin/ps", "-p", fields[0], "-o", "pid=,stat=,comm=").Output()
				state := strings.Fields(string(status))
				if err != nil || len(state) < 3 || filepath.Base(state[2]) != "ffmpeg" ||
					strings.HasPrefix(state[1], "T") != stopped {
					continue
				}
				session.publicationMu.Lock()
				frontier := scanFrontier(dir, 0)
				staged, err := filepath.Glob(filepath.Join(filepath.Dir(dir), ".encoder-*", "seg*.m4s"))
				completed := 0
				for _, path := range staged {
					if fileReady(path) {
						completed++
					}
				}
				session.publicationMu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				fmt.Printf("playhead=%ds segment=%d frontier=%d on_disk_ahead=%d unpublished_completed=%d\nps: %s\n",
					playhead*4, playhead, frontier, frontier-playhead, completed, strings.TrimSpace(string(status)))
				if stopped && frontier-playhead+completed > encodeAheadSegments {
					t.Fatalf("published and unpublished completed files exceed the lead")
				}
				if stopped && frontier-playhead < encodeAheadSegments {
					continue // SIGSTOP precedes the last cache rename
				}
				return fields[0], frontier
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("encoder never reached stopped=%v at %ds: dir=%s frontier=%d", stopped, playhead*4, dir, scanFrontier(dir, 0))
		return "", 0
	}
	// #nosec G204 G702 -- operator-supplied local fixture for the opt-in test.
	source, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height,r_frame_rate:format=duration", "-of", "default=nw=1", src).CombinedOutput()
	if err != nil {
		t.Fatalf("source probe: %v\n%s", err, source)
	}
	fmt.Printf("source probe:\n%s", source)
	cat := func(w io.Writer, path string) {
		t.Helper()
		f, err := os.Open(path) // #nosec G304 -- test-owned playback cache
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := io.Copy(w, f); err != nil {
			t.Fatal(err)
		}
	}
	var sample bytes.Buffer
	cat(&sample, init)
	cat(&sample, first)
	probe := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height", "-of", "csv=p=0", "-i", "pipe:0")
	probe.Stdin = &sample
	got, err := probe.CombinedOutput()
	fmt.Printf("transcode probe: %s", got)
	if err != nil || strings.TrimSpace(string(got)) != "h264,1920,1080" {
		t.Fatalf("expected 1080p H.264: %v", err)
	}
	pid, initial := observe(0, true)
	if initial != 8 {
		t.Fatalf("initial lead is %d, want 8", initial)
	}

	player := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-nostdin", "-v", "error",
		"-progress", "pipe:1", "-nostats", "-f", "mp4", "-i", "pipe:0", "-map", "0:v:0", "-f", "null", "-")
	var progress bytes.Buffer
	player.Stdout = &progress
	player.Stderr = os.Stderr
	input, err := player.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := player.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = input.Close()
			_ = player.Process.Kill()
			_ = player.Wait()
		}
	}()
	began := time.Now()
	cat(input, init)
	cat(input, first)
	for n := 1; n <= 15; n++ {
		time.Sleep(time.Until(began.Add(time.Duration(n*4) * time.Second)))
		cat(input, segment(n))
	}
	fmt.Printf("wall-clock playback: %.3fs\n", time.Since(began).Seconds())
	pausedPID, frontier := observe(15, true)
	if pausedPID != pid || frontier-15 != 8 {
		t.Fatalf("after 60s: pid=%s (initial %s), lead=%d", pausedPID, pid, frontier-15)
	}
	fmt.Println("seek: 60s -> 80s (+20s)")
	cat(input, segment(20))
	activePID, _ := observe(20, false)
	if activePID != pid {
		t.Fatalf("seek changed PID from %s to %s", pid, activePID)
	}
	pausedPID, frontier = observe(20, true)
	if pausedPID != pid || frontier-20 != 8 {
		t.Fatalf("after seek: pid=%s (initial %s), lead=%d", pausedPID, pid, frontier-20)
	}
	_ = input.Close()
	err = player.Wait()
	waited = true
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(progress.String(), "progress=end") {
		lines := strings.Split(strings.TrimSpace(progress.String()), "\n")
		fmt.Println("decoder final progress:")
		fmt.Println(strings.Join(lines[len(lines)-12:], "\n"))
	} else {
		t.Fatal("headless playback did not finish decoding")
	}
}

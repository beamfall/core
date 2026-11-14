//go:build unix

package playback

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// hungEncoder stands in for an ffmpeg that reports one output position on its
// progress pipe and then hangs without exiting.
func hungEncoder() []string {
	return []string{"sh", "-c", `printf 'out_time_us=1000000\nprogress=continue\n'; exec sleep 60`, "sh", "-progress", "pipe:1"}
}

func stallSessions(t *testing.T, transcoder Transcoder) *Sessions {
	t.Helper()
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	sessions.stallTimeout = 100 * time.Millisecond
	sessions.waitWindow = 5 * time.Second
	t.Cleanup(sessions.Close)
	return sessions
}

// TestStalledEncoderIsRestartedInsideRequest proves a request waiting on an
// encoder whose output stopped advancing kills it and restarts it at the
// requested segment instead of waiting on it for as long as clients retry.
func TestStalledEncoderIsRestartedInsideRequest(t *testing.T) {
	t.Parallel()
	transcoder := &scriptedTranscoder{}
	transcoder.start = func(n int, dir string, startSegment int) []string {
		if n == 0 {
			return hungEncoder()
		}
		writeArtifact(t, dir, fmt.Sprintf("seg%05d.m4s", startSegment), mediaSegment())
		return []string{"sleep", "60"}
	}
	sessions := stallSessions(t, transcoder)
	key := SessionKey{SessionID: "session-stall", MediaID: "media-stall"}

	begin := time.Now()
	path, err := filePath720(sessions, key, "720p/seg00005.m4s")
	if err != nil {
		t.Fatalf("segment after stall: %v", err)
	}
	if filepath.Base(path) != "seg00005.m4s" {
		t.Fatalf("path = %q, want seg00005.m4s", path)
	}
	if elapsed := time.Since(begin); elapsed >= sessions.waitWindow {
		t.Fatalf("stall recovery took %v, want it inside the %v wait", elapsed, sessions.waitWindow)
	}
	transcoder.mu.Lock()
	starts := append([]int(nil), transcoder.starts...)
	transcoder.mu.Unlock()
	if len(starts) != 2 || starts[1] != 5 {
		t.Fatalf("starts = %v, want the stalled start then a restart at segment 5", starts)
	}
}

// TestEncoderThatKeepsStallingFailsFast proves stall recovery shares the crash
// bound: every restart also hangs, so the request fails with the
// encoder-failing error once its restarts are spent.
func TestEncoderThatKeepsStallingFailsFast(t *testing.T) {
	t.Parallel()
	transcoder := &scriptedTranscoder{}
	transcoder.start = func(int, string, int) []string { return hungEncoder() }
	sessions := stallSessions(t, transcoder)
	key := SessionKey{SessionID: "session-hung", MediaID: "media-hung"}

	begin := time.Now()
	_, err := filePath720(sessions, key, "720p/seg00000.m4s")
	if !errors.Is(err, errEncoderFailing) {
		t.Fatalf("err = %v, want errEncoderFailing", err)
	}
	if got, want := transcoder.startCount(), 1+maxRequestEncoderRestarts; got != want {
		t.Fatalf("starts = %d, want %d", got, want)
	}
	if elapsed := time.Since(begin); elapsed >= sessions.waitWindow {
		t.Fatalf("failing request took %v, want it to fail before the %v wait expires", elapsed, sessions.waitWindow)
	}
}

// TestStalledEncoderWithoutWaitersIsLeftAlone proves the watchdog only acts
// while a request waits: an encoder whose output has not moved, with nobody
// waiting on it, keeps running.
func TestStalledEncoderWithoutWaitersIsLeftAlone(t *testing.T) {
	t.Parallel()
	transcoder := &scriptedTranscoder{cmds: make(chan *exec.Cmd, 1)}
	transcoder.start = func(_ int, dir string, _ int) []string {
		writeArtifact(t, dir, "init.mp4", []byte("init"))
		return hungEncoder()
	}
	sessions := stallSessions(t, transcoder)
	key := SessionKey{SessionID: "session-idle", MediaID: "media-idle"}

	if _, err := filePath720(sessions, key, "720p/init.mp4"); err != nil {
		t.Fatalf("init.mp4: %v", err)
	}
	cmd := <-transcoder.cmds
	time.Sleep(10 * sessions.stallTimeout)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("encoder with no waiters was killed: %v", err)
	}
	if got := transcoder.startCount(); got != 1 {
		t.Fatalf("starts = %d, want 1", got)
	}
}

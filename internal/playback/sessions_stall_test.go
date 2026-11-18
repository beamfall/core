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

// TestEncoderThatNeverProgressesFailsTheSession proves CORE-2379: an encoder
// that hangs on every start used to cycle forever (stall, kill, restart) as
// the sliding restart budget refilled, answering retryable errors while the
// player spun. After encoderMaxFailedRuns runs in a row with no segment the
// session latches failed: later requests get ErrTranscodeFailed at once and
// start no further encoder.
func TestEncoderThatNeverProgressesFailsTheSession(t *testing.T) {
	t.Parallel()
	transcoder := &scriptedTranscoder{}
	transcoder.start = func(int, string, int) []string { return hungEncoder() }
	sessions := stallSessions(t, transcoder)
	key := SessionKey{SessionID: "session-never", MediaID: "media-never"}

	var err error
	for request := 0; request < encoderMaxFailedRuns && !errors.Is(err, ErrTranscodeFailed); request++ {
		_, err = filePath720(sessions, key, "720p/seg00000.m4s")
		if err == nil {
			t.Fatalf("request %d succeeded on an encoder that never writes", request)
		}
	}
	if !errors.Is(err, ErrTranscodeFailed) {
		t.Fatalf("err = %v, want ErrTranscodeFailed once %d runs failed", err, encoderMaxFailedRuns)
	}
	if got := transcoder.startCount(); got != encoderMaxFailedRuns {
		t.Fatalf("starts = %d, want %d", got, encoderMaxFailedRuns)
	}
	begin := time.Now()
	if _, err := filePath720(sessions, key, "720p/seg00003.m4s"); !errors.Is(err, ErrTranscodeFailed) {
		t.Fatalf("request after failure: err = %v, want ErrTranscodeFailed", err)
	}
	if elapsed := time.Since(begin); elapsed >= sessions.stallTimeout {
		t.Fatalf("failed session took %v to answer, want it immediate", elapsed)
	}
	if got := transcoder.startCount(); got != encoderMaxFailedRuns {
		t.Fatalf("starts after failure = %d, want no new start (%d)", got, encoderMaxFailedRuns)
	}
}

// TestStallAfterOutputDoesNotCountTowardFailure proves only runs that wrote
// nothing count: an encoder that writes its first segment and then hangs is
// restarted under each next request and never fails the session.
func TestStallAfterOutputDoesNotCountTowardFailure(t *testing.T) {
	t.Parallel()
	staged := filepath.Join(t.TempDir(), "segment.m4s")
	writeArtifact(t, filepath.Dir(staged), filepath.Base(staged), mediaSegment())
	transcoder := &scriptedTranscoder{}
	transcoder.start = func(_ int, dir string, startSegment int) []string {
		// The segment lands after launch, as ffmpeg's would, then the
		// encoder hangs with its output position frozen.
		script := fmt.Sprintf(`printf 'out_time_us=1000000\nprogress=continue\n'; cp %q %q; exec sleep 60`,
			staged, filepath.Join(dir, fmt.Sprintf("seg%05d.m4s", startSegment)))
		return []string{"sh", "-c", script, "sh", "-progress", "pipe:1"}
	}
	sessions := stallSessions(t, transcoder)
	key := SessionKey{SessionID: "session-slow", MediaID: "media-slow"}

	// Each request asks for the segment after the one the last run wrote, so
	// it waits on a hung run that did produce output, which stalls and is
	// restarted at the requested segment.
	for segment := 0; segment <= encoderMaxFailedRuns; segment++ {
		_, err := filePath720(sessions, key, fmt.Sprintf("720p/seg%05d.m4s", segment))
		if errors.Is(err, ErrTranscodeFailed) {
			t.Fatalf("segment %d: session failed although every run wrote a segment", segment)
		}
	}
	if got := transcoder.startCount(); got < encoderMaxFailedRuns {
		t.Fatalf("starts = %d, want at least %d stall restarts exercised", got, encoderMaxFailedRuns)
	}
}

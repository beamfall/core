//go:build unix

package playback

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/encderiv"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/governor"
)

type sourceReadingTranscoder struct {
	fakeEncryptedTranscoder
	sourceURL chan string
}

func (f *sourceReadingTranscoder) StartEncryptedHLSSegments(ctx context.Context, endpoint ffmpeg.EncryptedLiveEndpoint, start, seconds int, rendition ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	if _, err := f.fakeEncryptedTranscoder.StartEncryptedHLSSegments(ctx, endpoint, start, seconds, rendition); err != nil {
		return nil, err
	}
	proc, err := ffmpeg.StartHLSCommand(exec.CommandContext(ctx, "sleep", "60"))
	if err == nil {
		f.sourceURL <- endpoint.Source
	}
	return proc, err
}

type blockedLiveSource struct {
	nopReadSeekCloser
	entered chan struct{}
	release <-chan struct{}
}

func (s *blockedLiveSource) Read(p []byte) (int, error) {
	close(s.entered)
	<-s.release
	return s.nopReadSeekCloser.Read(p)
}

func TestSessionsEncoderCapacityReleasesBeforeSourceDrain(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		stop        string
		replacement bool
		duringStop  bool
	}{
		{name: "terminate", stop: "terminate"},
		{name: "preempt", stop: "preempt"},
		{name: "idle reap", stop: "idle reap"},
		{name: "close", stop: "close"},
		{name: "process wait", stop: "process wait"},
		{name: "terminate replacement while stopping", stop: "terminate", replacement: true, duringStop: true},
		{name: "terminate replacement after stop", stop: "terminate", replacement: true},
		{name: "close replacement after stop", stop: "close", replacement: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			transcoder := &sourceReadingTranscoder{sourceURL: make(chan string, 1)}
			sessions := NewSessions(transcoder, t.TempDir(), nil)
			g := governor.New()
			sessions.SetGovernor(g)
			sessions.SetTranscodeCap(1)
			t.Cleanup(sessions.Close)
			entered, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			t.Cleanup(unblock)
			stopEntered, stopRelease := make(chan struct{}), make(chan struct{})
			unblockStop := sync.OnceFunc(func() { close(stopRelease) })
			t.Cleanup(unblockStop)
			var stoppedProc *ffmpeg.HLSProcess
			if tc.duringStop {
				sessions.stopEncoder = func(proc *ffmpeg.HLSProcess) {
					if proc == stoppedProc {
						close(stopEntered)
						<-stopRelease
					}
					proc.Stop()
				}
			}

			secure := testEncryptedSource(nil)
			secure.Size = 1
			secure.Open = func() (io.ReadSeekCloser, error) {
				return &blockedLiveSource{nopReadSeekCloser{bytes.NewReader([]byte{1})}, entered, release}, nil
			}
			key := SessionKey{SessionID: "source-drain", MediaID: "media-a"}
			other := SessionKey{SessionID: "source-drain-other", MediaID: "media-b"}
			path, err := encryptedFilePath(sessions, key, secure, 120, -1, false, "720p/seg00000.m4s")
			if err != nil {
				t.Fatal(err)
			}
			session := registeredSession(t, sessions, key)
			session.mu.Lock()
			run := session.run
			stoppedProc = run.proc
			session.mu.Unlock()
			if got := g.ActiveTranscodes(); got != 1 || !run.proc.Running() {
				t.Fatalf("first start: ActiveTranscodes = %d, process started = %v", got, run.proc.Running())
			}

			reader, err := sessions.OpenLiveArtifact(key, path)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			var buf [1]byte
			if _, err := io.ReadFull(reader, buf[:]); err != nil {
				t.Fatalf("read before revocation: %v", err)
			}
			clientDone := make(chan struct{})
			go func() {
				defer close(clientDone)
				client := &http.Client{Timeout: 5 * time.Second}
				resp, err := client.Get(<-transcoder.sourceURL)
				if err == nil {
					_ = resp.Body.Close()
				}
			}()
			select {
			case <-entered:
			case <-clientDone:
				t.Fatal("source request ended before reaching the blocked Read")
			case <-time.After(5 * time.Second):
				t.Fatal("source request never reached Read")
			}

			var replacementDone chan error
			if tc.replacement {
				replacementDone = make(chan error, 1)
				go func() {
					_, err := encryptedFilePath(sessions, key, secure, 120, -1, false, "480p/seg00000.m4s")
					replacementDone <- err
				}()
				if tc.duringStop {
					select {
					case <-stopEntered:
					case <-time.After(5 * time.Second):
						t.Fatal("replacement never reached Stop")
					}
				} else {
					waitSessionCondition(t, "replacement to stop the encoder", func() bool {
						sessions.mu.Lock()
						defer sessions.mu.Unlock()
						return run.stopped
					})
				}
				if sessions.CanAdmit(other) || g.ActiveTranscodes() != 1 {
					t.Fatal("attached replacement lost its admission")
				}
			}

			stopDone := make(chan error, 1)
			terminate := func() error {
				if n := sessions.TerminateSession(key.SessionID); n != 1 {
					return fmt.Errorf("TerminateSession = %d, want 1", n)
				}
				return nil
			}
			startOther := func(target *Sessions, priority Priority) error {
				_, err := target.FilePathShaped(context.Background(), other, "", 120, 1280, 720, false, false,
					ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{Codec: "aac", Channels: 2}, nil,
					StreamShape{Encrypted: testEncryptedSource(nil)}, priority, "720p/seg00000.m4s")
				return err
			}
			target := sessions
			switch tc.stop {
			case "terminate":
				if tc.replacement {
					finishesWhileBlocked(t, "TerminateSession during replacement", terminate)
				} else {
					go func() { stopDone <- terminate() }()
				}
			case "preempt":
				finishesWhileBlocked(t, "foreground start that preempts the first encoder", func() error {
					return startOther(sessions, PriorityForeground)
				})
			case "idle reap":
				sessions.mu.Lock()
				session.lastAccess = sessions.now().Add(-2 * sessionIdleTimeout)
				sessions.mu.Unlock()
				go func() {
					sessions.reap()
					stopDone <- nil
				}()
			case "close":
				go func() {
					sessions.Close()
					stopDone <- nil
				}()
				// Shutdown seals this manager. Check its released slot below
				// and start the next session under the same governor.
				target = NewSessions(transcoder, t.TempDir(), nil)
				target.SetGovernor(g)
				target.SetTranscodeCap(1)
				t.Cleanup(target.Close)
			case "process wait":
				run.proc.Kill()
			}

			if tc.stop != "process wait" {
				waitSessionCondition(t, "artifact reader to revoke while the source is blocked", func() bool {
					n, err := reader.ReadAt(buf[:], 0)
					return n == 0 && errors.Is(err, encderiv.ErrLiveSessionClosed)
				})
			}
			if tc.duringStop {
				if sessions.CanAdmit(other) || g.ActiveTranscodes() != 1 {
					t.Fatal("detached run released admission before encoder exit")
				}
				unblockStop()
			}
			if tc.stop != "preempt" {
				waitSessionCondition(t, "encoder slot and lease to release while the source is blocked", func() bool {
					sessions.mu.Lock()
					defer sessions.mu.Unlock()
					return sessions.liveCount() == 0 && g.ActiveTranscodes() == 0
				})
				finishesWhileBlocked(t, "second encoder start", func() error {
					return startOther(target, PriorityNormal)
				})
			}
			if got := g.ActiveTranscodes(); got != 1 {
				t.Fatalf("ActiveTranscodes after second start = %d, want 1", got)
			}
			if got := transcoder.encryptedStarts(); got != 2 {
				t.Fatalf("encrypted launches before the source drained = %d, want 2", got)
			}
			if _, err := os.Stat(session.dir); err != nil {
				t.Fatalf("first directory removed before the source drained: %v", err)
			}
			select {
			case err := <-stopDone:
				t.Fatalf("teardown returned before the source drained: %v", err)
			case err := <-replacementDone:
				t.Fatalf("replacement returned before the source drained: %v", err)
			default:
			}

			unblock()
			switch {
			case tc.replacement:
				select {
				case err := <-replacementDone:
					if !errors.Is(err, ErrSessionEvicted) {
						t.Fatalf("replacement: got %v, want ErrSessionEvicted", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("replacement did not finish after the source drained")
				}
			case tc.stop == "process wait":
				if err := terminate(); err != nil {
					t.Fatal(err)
				}
			}
			if tc.stop == "close" || tc.stop == "idle reap" || tc.stop == "terminate" && !tc.replacement {
				select {
				case err := <-stopDone:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("teardown did not finish after the source drained")
				}
			}
			waitSessionCondition(t, "directory removal after the source drained", func() bool {
				_, err := os.Stat(session.dir)
				return errors.Is(err, os.ErrNotExist)
			})
			<-clientDone
		})
	}
}

// scriptedTranscoder runs a real child process per start (so the reaper and
// exit watch run exactly as for ffmpeg) and lets each test script what every
// start writes and how the process ends.
type scriptedTranscoder struct {
	mu     sync.Mutex
	starts []int
	// start writes the start's artifacts and returns the command standing in
	// for ffmpeg; n is the zero-based start ordinal.
	start func(n int, dir string, startSegment int) []string
	cmds  chan *exec.Cmd
}

func (f *scriptedTranscoder) StartHLS(ctx context.Context, src, dir string, rendition ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	return f.StartHLSSegments(ctx, src, dir, 0, segmentSeconds, rendition)
}

func (f *scriptedTranscoder) StartHLSSegments(ctx context.Context, _, dir string, startSegment, _ int, _ ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	f.mu.Lock()
	n := len(f.starts)
	f.starts = append(f.starts, startSegment)
	f.mu.Unlock()
	argv := f.start(n, dir, startSegment)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 -- test-scripted argv
	proc, err := ffmpeg.StartHLSCommand(cmd)
	if err != nil {
		return nil, err
	}
	if f.cmds != nil {
		f.cmds <- cmd
	}
	return proc, nil
}

func (f *scriptedTranscoder) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts)
}

func writeArtifact(t *testing.T, dir, name string, body []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
		t.Errorf("write %s: %v", name, err)
	}
}

// TestEncoderCrashMidWaitRestartsInsideRequest proves a request waiting on a
// segment notices its encoder dying at once and restarts it at the requested
// segment, succeeding well inside the latency budget instead of waiting it out.
func TestEncoderCrashMidWaitRestartsInsideRequest(t *testing.T) {
	t.Parallel()
	transcoder := &scriptedTranscoder{cmds: make(chan *exec.Cmd, 4)}
	transcoder.start = func(n int, dir string, startSegment int) []string {
		writeArtifact(t, dir, "init.mp4", []byte("init"))
		if n == 0 {
			return []string{"sleep", "60"} // hangs until the test kills it
		}
		writeArtifact(t, dir, fmt.Sprintf("seg%05d.m4s", startSegment), mediaSegment())
		return []string{"sleep", "60"}
	}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-crash", MediaID: "media-crash"}

	type result struct {
		path string
		err  error
	}
	done := make(chan result, 1)
	begin := time.Now()
	go func() {
		path, err := filePath720(sessions, key, "720p/seg00005.m4s")
		done <- result{path, err}
	}()
	first := <-transcoder.cmds
	if err := first.Process.Kill(); err != nil {
		t.Fatalf("kill encoder: %v", err)
	}
	var got result
	select {
	case got = <-done:
	case <-time.After(TranscodeStartLatencyBudget):
		t.Fatal("request waited out its budget after the encoder died")
	}
	if got.err != nil {
		t.Fatalf("segment after crash: %v", got.err)
	}
	if filepath.Base(got.path) != "seg00005.m4s" {
		t.Fatalf("path = %q, want seg00005.m4s", got.path)
	}
	if elapsed := time.Since(begin); elapsed > 3*time.Second {
		t.Fatalf("crash recovery took %v, want well under the %v budget", elapsed, TranscodeStartLatencyBudget)
	}
	transcoder.mu.Lock()
	starts := append([]int(nil), transcoder.starts...)
	transcoder.mu.Unlock()
	if len(starts) != 2 || starts[1] != 5 {
		t.Fatalf("starts = %v, want the crashed start then a restart at segment 5", starts)
	}
}

// TestEncoderCrashBudgetFailsFast proves crash recovery is bounded: a source
// that kills FFmpeg every time is retried at most twice per request and within
// the session budget, then fails fast without another spawn. The first request
// stays on the retryable 503 path; once encoderMaxFailedRuns runs in a row
// produced nothing, the session is failed for good (CORE-2379).
func TestEncoderCrashBudgetFailsFast(t *testing.T) {
	t.Parallel()
	transcoder := &scriptedTranscoder{}
	transcoder.start = func(_ int, _ string, _ int) []string {
		return []string{"sh", "-c", "echo 'Invalid data found when processing input' >&2; exit 1"}
	}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-doomed", MediaID: "media-doomed"}

	begin := time.Now()
	for i, want := range []struct {
		starts int
		err    error
	}{
		{1 + maxRequestEncoderRestarts, errEncoderFailing}, // first request: start + its per-request restarts
		{encoderRestartBudget + 1, ErrTranscodeFailed},     // second: its start is the last run that may fail
		{encoderRestartBudget + 1, ErrTranscodeFailed},     // third: failed session, refused before spawning
	} {
		_, err := filePath720(sessions, key, "720p/seg00000.m4s")
		if !errors.Is(err, want.err) {
			t.Fatalf("request %d err = %v, want %v", i, err, want.err)
		}
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrSessionEvicted) {
			t.Fatalf("request %d err = %v, want an encoder failure", i, err)
		}
		if got := transcoder.startCount(); got != want.starts {
			t.Fatalf("after request %d starts = %d, want %d", i, got, want.starts)
		}
	}
	if elapsed := time.Since(begin); elapsed > 3*time.Second {
		t.Fatalf("failing requests took %v, want fail-fast well under the latency budget", elapsed)
	}
}

// TestTruncatedSourceRefusesSegmentsPastItsEnd reproduces CORE-1343: the
// source ends after its first segment although its probed duration is 120s.
// The real segment serves; every later segment fails as not-found without
// spawning an encoder that would write an empty, zero-duration segment.
func TestTruncatedSourceRefusesSegmentsPastItsEnd(t *testing.T) {
	t.Parallel()
	transcoder := &scriptedTranscoder{}
	transcoder.start = func(_ int, dir string, startSegment int) []string {
		writeArtifact(t, dir, "init.mp4", []byte("init"))
		if startSegment == 0 {
			writeArtifact(t, dir, "seg00000.m4s", mediaSegment())
		} else {
			writeArtifact(t, dir, fmt.Sprintf("seg%05d.m4s", startSegment), emptySegment())
		}
		return []string{"sh", "-c", "echo 'File ended prematurely' >&2; exit 0"}
	}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-short", MediaID: "media-short"}

	if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("real first segment: %v", err)
	}
	for _, name := range []string{"720p/seg00001.m4s", "720p/seg00002.m4s", "720p/seg00020.m4s"} {
		path, err := filePath720(sessions, key, name)
		if !errors.Is(err, ErrSourceEnded) || !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s = (%q, %v), want ErrSourceEnded wrapping ErrNotFound", name, path, err)
		}
	}
	if got := transcoder.startCount(); got != 1 {
		t.Fatalf("starts = %d, want 1 (no encoder spawned past the source end)", got)
	}
}

// TestSeekPastTruncatedEndNeverServesEmptySegment covers the far-seek path: a
// fresh session seeks straight past the real end, FFmpeg writes a styp-only
// segment, and the request must refuse it rather than serve it as playable.
func TestSeekPastTruncatedEndNeverServesEmptySegment(t *testing.T) {
	t.Parallel()
	transcoder := &scriptedTranscoder{}
	transcoder.start = func(_ int, dir string, startSegment int) []string {
		writeArtifact(t, dir, "init.mp4", []byte("init"))
		writeArtifact(t, dir, fmt.Sprintf("seg%05d.m4s", startSegment), emptySegment())
		return []string{"sleep", "60"} // still running: the content check alone must catch it
	}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-seek", MediaID: "media-seek"}

	if _, err := filePath720(sessions, key, "720p/seg00010.m4s"); !errors.Is(err, ErrSourceEnded) {
		t.Fatalf("empty segment err = %v, want ErrSourceEnded", err)
	}
	if _, err := filePath720(sessions, key, "720p/seg00011.m4s"); !errors.Is(err, ErrSourceEnded) {
		t.Fatalf("segment past the known end err = %v, want ErrSourceEnded", err)
	}
	if got := transcoder.startCount(); got != 1 {
		t.Fatalf("starts = %d, want 1", got)
	}
}

// TestShortLastSegmentStillServes proves a source that really ends inside its
// last declared segment is not mistaken for a truncated one.
func TestShortLastSegmentStillServes(t *testing.T) {
	t.Parallel()
	transcoder := &scriptedTranscoder{}
	transcoder.start = func(_ int, dir string, startSegment int) []string {
		writeArtifact(t, dir, "init.mp4", []byte("init"))
		for i := startSegment; i < 3; i++ { // 10s: segments of 4s, 4s and a short 2s
			writeArtifact(t, dir, fmt.Sprintf("seg%05d.m4s", i), mediaSegment())
		}
		return []string{"true"}
	}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-ten", MediaID: "media-ten"}
	for _, name := range []string{"720p/seg00000.m4s", "720p/seg00002.m4s"} {
		if _, err := sessions.FilePath(context.Background(), key, "/src/ten.mkv", 10, 1280, 720, false, name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Let the clean exit be reaped, then the short last segment must still serve.
	deadline := time.Now().Add(2 * time.Second)
	session := registeredSession(t, sessions, key)
	for {
		session.mu.Lock()
		reaped := session.proc == nil
		known := session.sourceEndKnown
		session.mu.Unlock()
		if known {
			t.Fatal("a complete encode recorded a premature source end")
		}
		if reaped || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := sessions.FilePath(context.Background(), key, "/src/ten.mkv", 10, 1280, 720, false, "720p/seg00002.m4s"); err != nil {
		t.Fatalf("short last segment after reap: %v", err)
	}
}

func TestSegmentLacksMedia(t *testing.T) {
	t.Parallel()
	large := make([]byte, 16)
	binary.BigEndian.PutUint32(large, 1)
	copy(large[4:], "styp")
	binary.BigEndian.PutUint64(large[8:], 16)
	toEnd := []byte{0, 0, 0, 0, 'm', 'd', 'a', 't', 'x'}
	cases := []struct {
		name string
		body []byte
		want bool
	}{
		{"styp only", emptySegment(), true},
		{"empty mdat", append(emptySegment(), mp4Box("mdat", nil)...), true},
		{"64-bit styp only", large, true},
		{"media", mediaSegment(), false},
		{"mdat to end of file", append(emptySegment(), toEnd...), false},
		{"unparseable", []byte("m4s"), false},
		{"truncated box", emptySegment()[:20], false},
		{"oversized box", mp4Box("styp", make([]byte, 4))[:10], false},
	}
	dir := t.TempDir()
	for i, tc := range cases {
		path := filepath.Join(dir, fmt.Sprintf("case%d.m4s", i))
		if err := os.WriteFile(path, tc.body, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := segmentLacksMedia(path); got != tc.want {
			t.Errorf("%s: segmentLacksMedia = %v, want %v", tc.name, got, tc.want)
		}
	}
	if segmentLacksMedia(filepath.Join(dir, "missing.m4s")) {
		t.Error("a missing file must not read as an empty segment")
	}
}

func TestSessionsReplacementAtCapacityOneKeepsLease(t *testing.T) {
	t.Parallel()
	g := governor.New()
	transcoder := &scriptedTranscoder{start: func(_ int, dir string, segment int) []string {
		writeArtifact(t, dir, fmt.Sprintf("seg%05d.m4s", segment), mediaSegment())
		return []string{"sleep", "60"}
	}}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	sessions.SetGovernor(g)
	sessions.SetTranscodeCap(1)
	defer sessions.Close()
	slow := newSlowStop(sessions)
	defer slow.unblock()
	sessions.stopEncoder = func(proc *ffmpeg.HLSProcess) {
		slow.stop(proc)
		proc.Stop()
	}
	key := SessionKey{SessionID: "session-c", MediaID: "media-c"}
	if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("first start: %v", err)
	}
	session := registeredSession(t, sessions, key)
	sessions.mu.Lock()
	lease := session.run.lease
	sessions.mu.Unlock()
	slow.armed.Store(true)
	replaced := make(chan error, 1)
	go func() {
		_, err := filePath720(sessions, key, "480p/seg00000.m4s")
		replaced <- err
	}()
	slow.waitEntered(t)
	other := SessionKey{SessionID: "session-other", MediaID: "media-other"}
	if sessions.CanAdmit(other) || g.ActiveTranscodes() != 1 {
		t.Fatal("replacement did not retain its slot and sole lease during Stop")
	}
	slow.unblock()
	select {
	case err := <-replaced:
		if err != nil {
			t.Fatalf("rendition replacement: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacement did not finish")
	}
	sessions.mu.Lock()
	replacementLease := session.run.lease
	sessions.mu.Unlock()
	if lease == nil || lease != replacementLease || g.ActiveTranscodes() != 1 {
		t.Fatal("replacement did not keep the original transcode lease")
	}
	sessions.Close()
	if got := g.ActiveTranscodes(); got != 0 {
		t.Fatalf("ActiveTranscodes after Close = %d, want 0", got)
	}
}

func TestSessionsCloseDuringSpawn(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	launcher := &scriptedTranscoder{cmds: make(chan *exec.Cmd, 1)}
	launcher.start = func(_ int, dir string, _ int) []string {
		writeArtifact(t, dir, "seg00000.m4s", mediaSegment())
		return []string{"sleep", "60"}
	}
	g := governor.New()
	sessions := NewSessions(&pausedSpawn{scriptedTranscoder: launcher, release: release}, t.TempDir(), nil)
	sessions.SetGovernor(g)
	defer sessions.Close()
	defer unblock()
	key := SessionKey{SessionID: "session-closing-mid-spawn", MediaID: "media-alpha"}

	fetched := make(chan error, 1)
	go func() {
		_, err := filePath720(sessions, key, "720p/seg00000.m4s")
		fetched <- err
	}()
	var cmd *exec.Cmd
	select {
	case cmd = <-launcher.cmds:
	case err := <-fetched:
		t.Fatalf("spawn did not start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("spawn did not reach the latch")
	}
	session := registeredSession(t, sessions, key)
	closed := make(chan struct{})
	go func() {
		sessions.Close()
		close(closed)
	}()
	waitSessionCondition(t, "shutdown detached the spawning session", session.detached.Load)
	select {
	case <-closed:
		t.Fatal("Close returned before the spawning encoder was handed back")
	case <-time.After(50 * time.Millisecond):
	}
	if got := g.ActiveTranscodes(); got != 1 {
		t.Fatalf("ActiveTranscodes while spawn is held = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(session.dir, "720p", "seg00000.m4s")); err != nil {
		t.Fatalf("spawn still owns its directory: %v", err)
	}
	unblock()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not drain the spawn")
	}
	if cmd.ProcessState == nil {
		t.Fatal("Close left the spawned child unreaped")
	}
	if got := g.ActiveTranscodes(); got != 0 {
		t.Fatalf("ActiveTranscodes after Close = %d, want 0", got)
	}
	if _, err := os.Stat(session.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session dir after Close: %v", err)
	}
	if err := <-fetched; !errors.Is(err, ErrSessionEvicted) {
		t.Fatalf("fetch err = %v, want ErrSessionEvicted", err)
	}
}

type pausedSpawn struct {
	*scriptedTranscoder
	release <-chan struct{}
}

func (p *pausedSpawn) StartHLSSegments(ctx context.Context, src, dir string, startSegment, segmentSeconds int, rendition ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	proc, err := p.scriptedTranscoder.StartHLSSegments(ctx, src, dir, startSegment, segmentSeconds, rendition)
	if err == nil {
		<-p.release
	}
	return proc, err
}

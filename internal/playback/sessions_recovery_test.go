//go:build unix

package playback

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
)

// mp4Box encodes one ISO-BMFF box with a 32-bit size.
func mp4Box(typ string, payload []byte) []byte {
	out := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(out, uint32(8+len(payload))) // #nosec G115 -- test payloads are a few bytes
	copy(out[4:], typ)
	return append(out, payload...)
}

// emptySegment is what ffmpeg's HLS muxer writes when it starts past the end
// of a truncated input: a lone 24-byte styp box (#EXTINF:0.000000).
func emptySegment() []byte {
	return mp4Box("styp", []byte("msdh\x00\x00\x00\x00msdhmsix"))
}

// mediaSegment is a minimal playable-shaped segment: styp, moof, mdat.
func mediaSegment() []byte {
	out := emptySegment()
	out = append(out, mp4Box("moof", make([]byte, 8))...)
	return append(out, mp4Box("mdat", []byte("media-bytes"))...)
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
// the session budget, then fails fast without another spawn.
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
	for i, wantStarts := range []int{
		1 + maxRequestEncoderRestarts, // first request: start + its per-request restarts
		encoderRestartBudget + 1,      // second: its start spends the last budget slot
		encoderRestartBudget + 1,      // third: budget spent, refused before spawning
	} {
		_, err := filePath720(sessions, key, "720p/seg00000.m4s")
		if !errors.Is(err, errEncoderFailing) {
			t.Fatalf("request %d err = %v, want errEncoderFailing (the 503 path)", i, err)
		}
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrSessionEvicted) {
			t.Fatalf("request %d err = %v must stay on the retryable 503 path", i, err)
		}
		if got := transcoder.startCount(); got != wantStarts {
			t.Fatalf("after request %d starts = %d, want %d", i, got, wantStarts)
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
	for {
		sessions.mu.Lock()
		reaped := sessions.sessions[key].proc == nil
		known := sessions.sessions[key].sourceEndKnown
		sessions.mu.Unlock()
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

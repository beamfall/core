package playback

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/ffmpeg/ffmpegtest"
	"github.com/beamfall/core/internal/store"
)

// keyframeDriftTolerance is how far a segment's first video PTS may sit from
// the playlist's start for it: the playlist's millisecond rounding plus the
// microsecond the restart point is rounded up by.
const keyframeDriftTolerance = 0.001

var (
	_ keyframeTranscoder = (*ffmpeg.Runner)(nil)
	_ KeyframeStore      = (*store.Store)(nil)
)

func TestKeyframeTimelineLayout(t *testing.T) {
	t.Parallel()
	timeline := newKeyframeTimeline(ffmpeg.KeyframeIndex{
		// The first keyframe is not at 0 (it opens segment 0 all the same),
		// one sits within the playlist's millisecond of another, and one lies
		// past the end.
		Keyframes: []time.Duration{
			40 * time.Millisecond,
			7 * time.Second,
			7*time.Second + 300*time.Microsecond,
			14*time.Second + 12*time.Microsecond,
			21 * time.Second,
		},
		Duration: 20*time.Second + 480*time.Millisecond,
	})
	if timeline == nil {
		t.Fatal("no timeline")
	}
	wantStarts := []time.Duration{0, 7 * time.Second, 14*time.Second + 12*time.Microsecond}
	if !slices.Equal(timeline.starts, wantStarts) {
		t.Fatalf("starts = %v, want %v", timeline.starts, wantStarts)
	}
	playlist := timeline.playlist()
	for _, want := range []string{
		"#EXT-X-TARGETDURATION:7\n",
		"#EXTINF:7.000,\nseg00000.m4s\n",
		"#EXTINF:7.000,\nseg00001.m4s\n",
		"#EXTINF:6.480,\nseg00002.m4s\n#EXT-X-ENDLIST\n",
	} {
		if !strings.Contains(playlist, want) {
			t.Fatalf("playlist lacks %q:\n%s", want, playlist)
		}
	}
	for at, want := range map[time.Duration]int{0: 0, 6999 * time.Millisecond: 0, 7 * time.Second: 1, 13 * time.Second: 1, 20 * time.Second: 2} {
		if got := timeline.segmentAt(at); got != want {
			t.Errorf("segmentAt(%v) = %d, want %d", at, got, want)
		}
	}
	if timeline.segmentStart(3) != timeline.end {
		t.Fatalf("segmentStart past the last = %v, want the end", timeline.segmentStart(3))
	}
	if newKeyframeTimeline(ffmpeg.KeyframeIndex{Duration: time.Second}) != nil {
		t.Fatal("timeline built without keyframes")
	}
}

// TestAudioCopyPlaylistFollowsSourceKeyframes is HLS-AUDIOCOPY-KEYFRAME-
// TIMELINE-1's first criterion: for a 7s-GOP source the audio-copy playlist
// lists 7s segments, not the uniform 10s grid, and its total matches the
// ffprobe duration within one frame.
func TestAudioCopyPlaylistFollowsSourceKeyframes(t *testing.T) {
	t.Parallel()
	ffmpegtest.Require(t)
	const duration = 60
	for _, name := range []string{"gop7.mp4", "gop7.mkv"} {
		src := makeGOPSource(t, name, 7, 25, duration)
		sessions := newTestSessions(t, ffmpeg.New(), t.TempDir(), nil)
		defer sessions.Close()
		key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}
		playlist := readAudioCopyPlaylist(t, sessions, key, src, duration)

		index, err := ffmpeg.New().ProbeKeyframes(context.Background(), src)
		if err != nil {
			t.Fatal(err)
		}
		lengths := playlistSegmentLengths(t, playlist)
		if len(lengths) != 9 || len(index.Keyframes) != 9 {
			t.Fatalf("%s: %d segments over %d keyframes, want 9 (keyframes every 7s over 60s):\n%s", name, len(lengths), len(index.Keyframes), playlist)
		}
		var total float64
		for i, length := range lengths {
			total += length
			if i == len(lengths)-1 {
				continue
			}
			// Segment 0 runs from 0 to the second keyframe, so it also holds
			// the first keyframe's offset (AAC priming puts it a frame or so
			// past 0 on some muxers and FFmpeg releases); the rest span one GOP.
			from := 0.0
			if i > 0 {
				from = index.Keyframes[i].Seconds()
			}
			want := index.Keyframes[i+1].Seconds() - from
			if math.Abs(length-want) > keyframeDriftTolerance+0.0005 || (i > 0 && math.Abs(length-7) > keyframeDriftTolerance+0.0005) {
				t.Fatalf("%s: segment %d lasts %.3fs, want %.3f:\n%s", name, i, length, want, playlist)
			}
		}
		probed := probeFormatDuration(t, src)
		if math.Abs(total-probed) > 1.0/25 {
			t.Fatalf("%s: playlist total %.3fs, ffprobe duration %.6fs: more than a frame apart", name, total, probed)
		}
		if !strings.Contains(playlist, "#EXT-X-TARGETDURATION:7\n") {
			t.Fatalf("%s: target duration is not the longest segment:\n%s", name, playlist)
		}
	}
}

// TestAudioCopyRestartsLandOnPlaylistTimes is the second criterion: an encoder
// restarted at segment N writes media whose first video PTS is the playlist's
// cumulative start for N, and the transcoded audio starts with it.
func TestAudioCopyRestartsLandOnPlaylistTimes(t *testing.T) {
	t.Parallel()
	ffmpegtest.Require(t)
	const duration = 60
	sources := map[string]string{
		"gop7.mp4": makeGOPSource(t, "gop7.mp4", 7, 25, duration),
		"gop7.mkv": makeGOPSource(t, "gop7.mkv", 7, 25, duration),
		// Video that starts half a second after the audio: the first keyframe
		// opens segment 0 instead of getting a segment of its own.
		"late-video.mkv": makeLateVideoSource(t, "late-video.mkv", duration),
		// A second, larger video stream with a 3s GOP that FFmpeg's default
		// stream choice prefers: segments must follow the probed first one.
		"two-video.mkv": makeTwoVideoSource(t, "two-video.mkv", duration),
	}
	for name, src := range sources {
		sessions := newTestSessions(t, ffmpeg.New(), t.TempDir(), nil)
		defer sessions.Close()
		key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}
		playlist := readAudioCopyPlaylist(t, sessions, key, src, duration)
		index, err := ffmpeg.New().ProbeKeyframes(context.Background(), src)
		if err != nil {
			t.Fatal(err)
		}

		// Each request lies before the last restart's base, so each one
		// restarts the encoder at exactly that segment.
		for _, segment := range []int{8, 5, 2, 0} {
			file := fmt.Sprintf("%s/seg%05d.m4s", audioCopyRungID, segment)
			path, err := sessions.FilePath(context.Background(), key, src, duration, 64, 64, true, file)
			if err != nil {
				t.Fatalf("%s %s: %v", name, file, err)
			}
			sessions.mu.Lock()
			base := sessions.sessions[key].baseSegment
			sessions.mu.Unlock()
			if base != segment {
				t.Fatalf("%s %s: encoder base %d, want a restart at %d", name, file, base, segment)
			}
			playlistAt, err := playlistStartTime(playlist, segment)
			if err != nil {
				t.Fatal(err)
			}
			init := filepath.Join(filepath.Dir(path), initSegmentName)
			// Segment 0 starts at 0 in the playlist, but its video can only
			// start at the source's first keyframe; later segments start at
			// their keyframe on both.
			wantVideo := playlistAt
			if segment == 0 {
				wantVideo = index.Keyframes[0].Seconds()
			}
			// Segment 0's encoder starts from the head of the file, where
			// FFmpeg releases differ by up to a frame in where they place
			// the first video packet next to primed AAC audio (8.0 shifts it
			// to the audio's 23ms, 9.0 does not); restarts seek to a keyframe
			// and must land on the playlist time.
			tolerance := keyframeDriftTolerance
			if segment == 0 {
				tolerance = 1.0 / 25
			}
			videoAt := firstVideoPacketPTS(t, init, path)
			if drift := math.Abs(videoAt - wantVideo); drift > tolerance {
				t.Fatalf("%s segment %d: video starts at %.6fs, want %.6fs (playlist %.3fs)", name, segment, videoAt, wantVideo, playlistAt)
			}
			// Audio starts with the segment: at the video for a restart, and
			// no later than it (at or after the playlist's 0) for segment 0.
			audioAt := firstPacketPTS(t, init, path, "a:0")
			if audioAt > videoAt+0.05 || audioAt < playlistAt-0.05 {
				t.Fatalf("%s segment %d: audio starts at %.6fs, video at %.6fs, playlist %.3fs", name, segment, audioAt, videoAt, playlistAt)
			}
			if segment > 0 && math.Abs(audioAt-videoAt) > 0.05 {
				t.Fatalf("%s segment %d: audio starts at %.6fs, video at %.6fs", name, segment, audioAt, videoAt)
			}
		}
	}
}

// TestAudioCopyKeyframesProbedOncePerFileGeneration is the third criterion: the
// keyframe probe runs once per file generation, shared by concurrent first
// plays, reused by later sessions and by a new Sessions (a restart) through
// the store, and run again only when the file changes.
func TestAudioCopyKeyframesProbedOncePerFileGeneration(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(src, []byte("generation one"), 0o600); err != nil {
		t.Fatal(err)
	}
	transcoder := &keyframeFakeTranscoder{index: ffmpeg.KeyframeIndex{
		Keyframes: []time.Duration{0, 7 * time.Second, 14 * time.Second},
		Duration:  20 * time.Second,
	}}
	keyframes := &memoryKeyframeStore{}
	first := newTestSessions(t, transcoder, t.TempDir(), nil)
	defer first.Close()
	first.SetKeyframeStore(keyframes)

	var wg sync.WaitGroup
	playlists := make([]string, 8)
	for i := range playlists {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := SessionKey{SessionID: store.SessionID(fmt.Sprintf("viewer-%d", i)), MediaID: "media-alpha"}
			path, err := first.FilePath(context.Background(), key, src, 20, 64, 64, true, audioCopyRungID+"/index.m3u8")
			if err != nil {
				t.Error(err)
				return
			}
			body, err := os.ReadFile(path) // #nosec G304 -- path is beneath a test-owned temporary directory.
			if err != nil {
				t.Error(err)
				return
			}
			playlists[i] = string(body)
		}()
	}
	wg.Wait()
	if got := transcoder.probes.Load(); got != 1 {
		t.Fatalf("8 concurrent first plays probed %d times, want 1", got)
	}
	for _, playlist := range playlists {
		if !strings.Contains(playlist, "#EXTINF:7.000,\nseg00001.m4s\n#EXTINF:6.000,\nseg00002.m4s\n") {
			t.Fatalf("playlist does not follow the probed keyframes:\n%s", playlist)
		}
	}

	// A segment request restarts the encoder at that segment's keyframe.
	key := SessionKey{SessionID: "viewer-0", MediaID: "media-alpha"}
	if _, err := first.FilePath(context.Background(), key, src, 20, 64, 64, true, audioCopyRungID+"/seg00002.m4s"); err != nil {
		t.Fatal(err)
	}
	if got := transcoder.keyframeStartsSnapshot(); !slices.Equal(got, []time.Duration{14 * time.Second}) {
		t.Fatalf("keyframe restarts at %v, want [14s]", got)
	}

	// A new Sessions sharing the store (a server restart) does not probe.
	second := newTestSessions(t, transcoder, t.TempDir(), nil)
	defer second.Close()
	second.SetKeyframeStore(keyframes)
	readAudioCopyPlaylist(t, second, SessionKey{SessionID: "after-restart", MediaID: "media-alpha"}, src, 20)
	if got := transcoder.probes.Load(); got != 1 {
		t.Fatalf("a restarted Sessions probed again (%d probes), want the stored index", got)
	}

	// A rewritten file is a new generation and is probed again, once.
	if err := os.WriteFile(src, []byte("generation two, longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, viewer := range []string{"rewritten-a", "rewritten-b"} {
		readAudioCopyPlaylist(t, second, SessionKey{SessionID: store.SessionID(viewer), MediaID: "media-alpha"}, src, 20)
	}
	if got := transcoder.probes.Load(); got != 2 {
		t.Fatalf("rewritten file: %d probes, want 2", got)
	}
	if got := keyframes.puts.Load(); got != 2 {
		t.Fatalf("store recorded %d indexes, want one per generation (2)", got)
	}
}

func TestAudioCopyProbeFailureFailsTheRequest(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	transcoder := &keyframeFakeTranscoder{probeErr: fmt.Errorf("no video keyframes")}
	sessions := newTestSessions(t, transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}
	if _, err := sessions.FilePath(context.Background(), key, src, 20, 64, 64, true, audioCopyRungID+"/index.m3u8"); err == nil {
		t.Fatal("a failed keyframe probe served a uniform-grid playlist")
	}
	// The failure is not cached: the next request probes again.
	_, _ = sessions.FilePath(context.Background(), key, src, 20, 64, 64, true, audioCopyRungID+"/index.m3u8")
	if got := transcoder.probes.Load(); got != 2 {
		t.Fatalf("%d probes, want 2", got)
	}
}

func TestReencodeSessionsDoNotProbeKeyframes(t *testing.T) {
	t.Parallel()
	transcoder := &keyframeFakeTranscoder{}
	sessions := newTestSessions(t, transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}
	path, err := sessions.FilePath(context.Background(), key, fakeSourcePath, 120, 1280, 720, false, "720p/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path) // #nosec G304 -- path is beneath a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	if transcoder.probes.Load() != 0 || !strings.Contains(string(body), "#EXTINF:4.000,") {
		t.Fatalf("re-encode session probed keyframes or left the 4s grid:\n%s", body)
	}
}

// keyframeFakeTranscoder is fakeTranscoder plus the keyframe extension. Its
// probe counts calls and takes long enough for concurrent first plays to
// overlap.
type keyframeFakeTranscoder struct {
	fakeTranscoder
	index          ffmpeg.KeyframeIndex
	probeErr       error
	probes         atomic.Int32
	keyframeMu     sync.Mutex
	keyframeStarts []time.Duration
}

func (f *keyframeFakeTranscoder) ProbeKeyframes(context.Context, string) (ffmpeg.KeyframeIndex, error) {
	f.probes.Add(1)
	time.Sleep(20 * time.Millisecond)
	return f.index, f.probeErr
}

func (f *keyframeFakeTranscoder) StartHLSKeyframeSegments(ctx context.Context, src, dir string, startSegment int, start time.Duration, rendition ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	f.keyframeMu.Lock()
	f.keyframeStarts = append(f.keyframeStarts, start)
	f.keyframeMu.Unlock()
	return f.StartHLSSegments(ctx, src, dir, startSegment, 0, rendition)
}

func (f *keyframeFakeTranscoder) keyframeStartsSnapshot() []time.Duration {
	f.keyframeMu.Lock()
	defer f.keyframeMu.Unlock()
	return slices.Clone(f.keyframeStarts)
}

// memoryKeyframeStore is an in-memory KeyframeStore keyed by file generation.
type memoryKeyframeStore struct {
	mu   sync.Mutex
	rows map[keyframeGeneration]store.PlaybackKeyframes
	puts atomic.Int32
}

func (m *memoryKeyframeStore) PlaybackKeyframes(_ context.Context, path string, sizeBytes, modTimeNs int64) (store.PlaybackKeyframes, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[keyframeGeneration{path: path, size: sizeBytes, modTimeNs: modTimeNs}]
	return row, ok, nil
}

func (m *memoryKeyframeStore) PutPlaybackKeyframes(_ context.Context, index store.PlaybackKeyframes) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[keyframeGeneration]store.PlaybackKeyframes{}
	}
	m.rows[keyframeGeneration{path: index.Path, size: index.SizeBytes, modTimeNs: index.ModTimeNs}] = index
	m.puts.Add(1)
	return nil
}

// makeLateVideoSource is a 7s-GOP source whose video starts 0.5s after its
// audio, so its first keyframe is not at the container start.
func makeLateVideoSource(t *testing.T, name string, durationSeconds int) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), name)
	out, err := exec.Command("ffmpeg", "-v", "error", // #nosec G204 -- fixed ffmpeg arguments on a test-owned fixture.
		"-itsoffset", "0.5",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=duration=%d:size=64x64:rate=25", durationSeconds),
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:duration=%d", durationSeconds),
		"-map", "0:v", "-map", "1:a",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-g", "175", "-keyint_min", "175", "-sc_threshold", "0",
		"-c:a", "aac",
		"-y", src).CombinedOutput()
	if err != nil {
		t.Fatalf("synthesize late-video source: %v\n%s", err, out)
	}
	return src
}

// makeTwoVideoSource has a 64x64 7s-GOP first video stream and a 160x90
// 3s-GOP second one flagged default, which FFmpeg's automatic stream
// selection picks over the first (CORE-1735).
func makeTwoVideoSource(t *testing.T, name string, durationSeconds int) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), name)
	out, err := exec.Command("ffmpeg", "-v", "error", // #nosec G204 -- fixed ffmpeg arguments on a test-owned fixture.
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=duration=%d:size=64x64:rate=25", durationSeconds),
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=duration=%d:size=160x90:rate=25", durationSeconds),
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:duration=%d", durationSeconds),
		"-map", "0:v", "-map", "1:v", "-map", "2:a",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-sc_threshold", "0",
		"-g:v:0", "175", "-keyint_min:v:0", "175",
		"-g:v:1", "75", "-keyint_min:v:1", "75",
		"-disposition:v:0", "0", "-disposition:v:1", "default",
		"-c:a", "aac",
		"-y", src).CombinedOutput()
	if err != nil {
		t.Fatalf("synthesize two-video source: %v\n%s", err, out)
	}
	return src
}

func readAudioCopyPlaylist(t *testing.T, sessions *Sessions, key SessionKey, src string, duration int) string {
	t.Helper()
	path, err := sessions.FilePath(context.Background(), key, src, duration, 64, 64, true, audioCopyRungID+"/index.m3u8")
	if err != nil {
		t.Fatalf("audio-copy index: %v", err)
	}
	body, err := os.ReadFile(path) // #nosec G304 -- path is beneath a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func playlistSegmentLengths(t *testing.T, playlist string) []float64 {
	t.Helper()
	var lengths []float64
	for _, line := range strings.Split(playlist, "\n") {
		text, ok := strings.CutPrefix(line, "#EXTINF:")
		if !ok {
			continue
		}
		length, err := strconv.ParseFloat(strings.TrimSuffix(text, ","), 64)
		if err != nil {
			t.Fatal(err)
		}
		lengths = append(lengths, length)
	}
	return lengths
}

func probeFormatDuration(t *testing.T, src string) float64 {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", src).CombinedOutput() // #nosec G204 -- fixed ffprobe arguments on a test-owned fixture.
	if err != nil {
		t.Fatalf("ffprobe duration: %v\n%s", err, out)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatal(err)
	}
	return duration
}

// blockingProbeTranscoder holds ProbeKeyframes until its context ends or the
// test releases it with a usable index.
type blockingProbeTranscoder struct {
	fakeTranscoder
	started   chan struct{}
	release   chan struct{}
	cancelled atomic.Bool
}

func (f *blockingProbeTranscoder) ProbeKeyframes(ctx context.Context, _ string) (ffmpeg.KeyframeIndex, error) {
	close(f.started)
	select {
	case <-ctx.Done():
		f.cancelled.Store(true)
		return ffmpeg.KeyframeIndex{}, ctx.Err()
	case <-f.release:
		return ffmpeg.KeyframeIndex{Keyframes: []time.Duration{0, 4 * time.Second}, Duration: 8 * time.Second}, nil
	}
}

func (f *blockingProbeTranscoder) StartHLSKeyframeSegments(ctx context.Context, src, dir string, startSegment int, _ time.Duration, rendition ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	return f.StartHLSSegments(ctx, src, dir, startSegment, 0, rendition)
}

// TestCloseCancelsAndWaitsForKeyframeProbes keeps Close final (CORE-1736): a
// first audio-copy play still probing keyframes when Close runs has its probe
// cancelled and waited for, and neither it nor a later request creates a
// session or starts an encoder once Close has returned.
func TestCloseCancelsAndWaitsForKeyframeProbes(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "film.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	transcoder := &blockingProbeTranscoder{started: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(transcoder.release) }) }
	t.Cleanup(release)
	sessions := newTestSessions(t, transcoder, t.TempDir(), nil)
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	served := make(chan error, 1)
	go func() {
		_, err := sessions.FilePath(context.Background(), key, src, 8, 64, 64, true, audioCopyRungID+"/seg00000.m4s")
		served <- err
	}()
	<-transcoder.started

	closed := make(chan struct{})
	go func() {
		_ = sessions.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return while a keyframe probe was in flight")
	}
	if !transcoder.cancelled.Load() {
		t.Error("Close returned before cancelling the in-flight keyframe probe")
	}
	release() // without the fix, the probe now completes after Close
	if err := <-served; err == nil {
		t.Error("the request whose probe Close cancelled was served")
	}
	if _, err := sessions.FilePath(context.Background(), key, src, 120, 1280, 720, false, "720p/index.m3u8"); err == nil {
		t.Error("a request after Close was served")
	}

	sessions.mu.Lock()
	open := len(sessions.sessions)
	sessions.mu.Unlock()
	transcoder.mu.Lock()
	starts := len(transcoder.starts)
	transcoder.mu.Unlock()
	if open != 0 || starts != 0 {
		t.Fatalf("after Close: %d sessions, %d encoder starts; want none", open, starts)
	}
}

// TestKeyframeTimelineMergesShortGOPs is CORE-1843's layout criterion: a
// short-GOP source lists target-length playlist segments, each a run of whole
// GOPs, instead of one segment per GOP; a GOP longer than the target stands
// alone.
func TestKeyframeTimelineMergesShortGOPs(t *testing.T) {
	t.Parallel()
	seconds := func(values ...float64) []time.Duration {
		out := make([]time.Duration, len(values))
		for i, v := range values {
			out[i] = time.Duration(v * float64(time.Second))
		}
		return out
	}
	everySecond := make([]float64, 26)
	for i := range everySecond {
		everySecond[i] = float64(i)
	}
	for _, tc := range []struct {
		name      string
		keyframes []time.Duration
		duration  time.Duration
		groups    []int
		extinf    []string
	}{
		{"1s GOPs", seconds(everySecond...), 25500 * time.Millisecond, []int{0, 10, 20}, []string{"10.000", "10.000", "5.500"}},
		{"uneven GOPs", seconds(0, 4, 8, 9, 12), 20 * time.Second, []int{0, 3, 4}, []string{"9.000", "3.000", "8.000"}},
		{"long GOPs", seconds(0, 15, 17), 30 * time.Second, []int{0, 1, 2}, []string{"15.000", "2.000", "13.000"}},
	} {
		timeline := newKeyframeTimeline(ffmpeg.KeyframeIndex{Keyframes: tc.keyframes, Duration: tc.duration})
		if !slices.Equal(timeline.groups, tc.groups) {
			t.Fatalf("%s: groups = %v, want %v", tc.name, timeline.groups, tc.groups)
		}
		if timeline.playlistSegmentCount() != len(tc.groups) || timeline.segmentCount() != len(timeline.starts) {
			t.Fatalf("%s: %d playlist segments over %d GOPs", tc.name, timeline.playlistSegmentCount(), timeline.segmentCount())
		}
		playlist := timeline.playlist()
		var got []string
		for _, line := range strings.Split(playlist, "\n") {
			if text, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
				got = append(got, strings.TrimSuffix(text, ","))
			}
		}
		if !slices.Equal(got, tc.extinf) {
			t.Fatalf("%s: EXTINF %v, want %v:\n%s", tc.name, got, tc.extinf, playlist)
		}
		for segment := range tc.groups {
			first, end := timeline.encoderSegments(segment)
			wantEnd := timeline.segmentCount()
			if segment+1 < len(tc.groups) {
				wantEnd = tc.groups[segment+1]
			}
			if first != tc.groups[segment] || end != wantEnd {
				t.Fatalf("%s: segment %d carries GOPs [%d,%d), want [%d,%d)", tc.name, segment, first, end, tc.groups[segment], wantEnd)
			}
			// A player starting anywhere inside the segment asks for its
			// first GOP.
			mid := (timeline.segmentStart(first) + timeline.segmentStart(end)) / 2
			if got := timeline.segmentAt(mid); got != first {
				t.Fatalf("%s: segmentAt(%v) = %d, want %d", tc.name, mid, got, first)
			}
		}
	}
}

// TestJoinFragmentsKeepsFragmentsAndDropsIndexes checks the box surgery that
// joins merged GOP files: the first styp opens the result, every moof and
// mdat follows in order, sidx boxes go, and malformed input is refused.
func TestJoinFragmentsKeepsFragmentsAndDropsIndexes(t *testing.T) {
	t.Parallel()
	box := func(kind, payload string) []byte {
		out := make([]byte, 8, 8+len(payload))
		out[3] = byte(8 + len(payload)) // #nosec G115 -- test payloads are a few bytes.
		copy(out[4:], kind)
		return append(out, payload...)
	}
	join := func(boxes ...[]byte) []byte { return slices.Concat(boxes...) }
	sessions := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
	key := SessionKey{SessionID: "join"}
	path, err := filePath720(sessions, key, "master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := testArtifactForPath(sessions, key, path)
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Release()
	session := artifact.session
	dir := filepath.Join(filepath.Dir(path), "720p")
	partA := filepath.Join(dir, "seg00000.m4s")
	partB := filepath.Join(dir, "seg00001.m4s")
	if err := os.WriteFile(partA, join(box("styp", "a"), box("sidx", "a1"), box("sidx", "a2"), box("moof", "a"), box("mdat", "aa")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partB, join(box("styp", "b"), box("sidx", "b1"), box("moof", "b"), box("mdat", "bb")), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "joined00000.m4s")
	if err := sessions.joinArtifact(t.Context(), session, dst, []string{partA, partB}, 0); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst) // #nosec G304 -- path is beneath a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	want := join(box("styp", "a"), box("moof", "a"), box("mdat", "aa"), box("moof", "b"), box("mdat", "bb"))
	if !slices.Equal(got, want) {
		t.Fatalf("joined = %q, want %q", got, want)
	}

	broken := filepath.Join(dir, "broken.m4s")
	if err := os.WriteFile(broken, join(box("moof", "c"), []byte{0, 0, 0, 40, 'm', 'd', 'a', 't'}), 0o600); err != nil {
		t.Fatal(err)
	}
	refused := filepath.Join(dir, "joined00001.m4s")
	if err := sessions.joinArtifact(t.Context(), session, refused, []string{partA, broken}, 0); err == nil {
		t.Fatal("joined a truncated segment")
	}
	if _, err := os.Stat(refused); !os.IsNotExist(err) {
		t.Fatalf("a refused join left %s behind: %v", refused, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(session.dir, ".join-*")); len(leftovers) > 0 {
		t.Fatalf("a refused join left temporary files: %v", leftovers)
	}
}

// TestAudioCopyMergesShortGOPSegments is CORE-1843's end-to-end criterion: a
// 2s-GOP source lists about 10s segments on its keyframes, a request for a
// later segment restarts the encoder at that segment's first GOP, and the
// joined segment plays from the playlist time for the whole segment.
func TestAudioCopyMergesShortGOPSegments(t *testing.T) {
	t.Parallel()
	ffmpegtest.Require(t)
	const duration = 30
	src := makeGOPSource(t, "gop2.mp4", 2, 25, duration)
	sessions := newTestSessions(t, ffmpeg.New(), t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}
	playlist := readAudioCopyPlaylist(t, sessions, key, src, duration)
	lengths := playlistSegmentLengths(t, playlist)
	if len(lengths) != 3 {
		t.Fatalf("%d segments, want 3 merged 10s segments:\n%s", len(lengths), playlist)
	}
	for i, length := range lengths[:2] {
		if math.Abs(length-10) > 0.05 {
			t.Fatalf("segment %d lasts %.3fs, want about 10s:\n%s", i, length, playlist)
		}
	}

	file := audioCopyRungID + "/seg00002.m4s"
	path, err := sessions.FilePath(context.Background(), key, src, duration, 64, 64, true, file)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	sessions.mu.Lock()
	base := sessions.sessions[key].baseSegment
	sessions.mu.Unlock()
	if base != 10 {
		t.Fatalf("encoder base %d, want a restart at GOP 10 (20s)", base)
	}
	playlistAt, err := playlistStartTime(playlist, 2)
	if err != nil {
		t.Fatal(err)
	}
	init := filepath.Join(filepath.Dir(path), initSegmentName)
	if videoAt := firstVideoPacketPTS(t, init, path); math.Abs(videoAt-playlistAt) > keyframeDriftTolerance {
		t.Fatalf("segment 2 video starts at %.6fs, want the playlist's %.3fs", videoAt, playlistAt)
	}

	// Segment 1 joins five GOP files; its video covers the whole 10s.
	file = audioCopyRungID + "/seg00001.m4s"
	path, err = sessions.FilePath(context.Background(), key, src, duration, 64, 64, true, file)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	if filepath.Base(path) != "joined00001.m4s" {
		t.Fatalf("segment 1 served from %s, want the joined file", path)
	}
	first, last := videoPacketSpan(t, init, path)
	if math.Abs(first-10) > keyframeDriftTolerance || last < 10+lengths[1]-2.0/25 {
		t.Fatalf("segment 1 video spans %.3fs to %.3fs, want 10s to about %.3fs", first, last, 10+lengths[1])
	}
}

// videoPacketSpan is the first and last video packet PTS in init + segment.
func videoPacketSpan(t *testing.T, initPath, segmentPath string) (first, last float64) {
	t.Helper()
	joined := filepath.Join(t.TempDir(), "span.mp4")
	initBody, err := os.ReadFile(initPath) // #nosec G304 -- path is beneath a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	segmentBody, err := os.ReadFile(segmentPath) // #nosec G304 -- path is beneath a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(joined, slices.Concat(initBody, segmentBody), 0o600); err != nil { // #nosec G703 -- joined is beneath a test-owned temporary directory.
		t.Fatal(err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time", "-of", "csv=p=0", joined).CombinedOutput() // #nosec G204 -- fixed ffprobe arguments on a test-owned file.
	if err != nil {
		t.Fatalf("ffprobe packets: %v\n%s", err, out)
	}
	first, last = math.Inf(1), math.Inf(-1)
	for _, line := range strings.Fields(string(out)) {
		pts, err := strconv.ParseFloat(strings.TrimSuffix(line, ","), 64)
		if err != nil {
			continue
		}
		first, last = math.Min(first, pts), math.Max(last, pts)
	}
	if math.IsInf(first, 1) {
		t.Fatal("no video packets")
	}
	return first, last
}

// TestSlowKeyframeProbeDoesNotHoldFirstPlay: a first audio-copy play whose
// keyframe probe outlasts the first-play wait starts on the uniform grid
// instead of waiting for the probe (a 17 GB remux on a parity array took eight
// minutes, and every play failed). The session keeps that grid when the probe
// ends, so it is not torn down mid-play, and the next play uses the probed
// timeline.
func TestSlowKeyframeProbeDoesNotHoldFirstPlay(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "film.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	transcoder := &blockingProbeTranscoder{started: make(chan struct{}), release: make(chan struct{})}
	sessions := newTestSessions(t, transcoder, t.TempDir(), nil)
	sessions.keyframes.firstPlayWait = 50 * time.Millisecond
	defer sessions.Close()
	session := func(key SessionKey) *hlsSession {
		t.Helper()
		if _, err := sessions.FilePath(context.Background(), key, src, 8, 64, 64, true, audioCopyRungID+"/index.m3u8"); err != nil {
			t.Fatalf("%s: %v", key.SessionID, err)
		}
		sessions.mu.Lock()
		defer sessions.mu.Unlock()
		return sessions.sessions[key]
	}
	first := SessionKey{SessionID: "first", MediaID: "media-alpha"}

	playing := session(first)
	if playing == nil || playing.timeline != nil {
		t.Fatal("the first play waited for the keyframe probe instead of starting on the uniform grid")
	}

	close(transcoder.release)
	deadline := time.Now().Add(10 * time.Second)
	for {
		sessions.keyframes.mu.Lock()
		probed := len(sessions.keyframes.done)
		sessions.keyframes.mu.Unlock()
		if probed == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the background keyframe probe never recorded its timeline")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if again := session(first); again != playing || again.timeline != nil {
		t.Fatal("the probe finishing reshaped the session that was already playing")
	}
	if next := session(SessionKey{SessionID: "next", MediaID: "media-alpha"}); next == nil || next.timeline == nil {
		t.Fatal("the next play did not use the probed keyframe timeline")
	}
}

func TestKeyframeProbeTimeoutScalesWithFileSize(t *testing.T) {
	t.Parallel()
	if got := keyframeProbeTimeoutFor(1 << 30); got != keyframeProbeTimeout {
		t.Errorf("1 GiB: %v, want the %v floor", got, keyframeProbeTimeout)
	}
	// The Tower remux that never finished in five minutes.
	if got := keyframeProbeTimeoutFor(17_415_319_816); got < 30*time.Minute {
		t.Errorf("17.4 GB: %v, want at least 30m", got)
	}
}

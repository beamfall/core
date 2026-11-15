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
		sessions := NewSessions(ffmpeg.New(), t.TempDir(), nil)
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
		sessions := NewSessions(ffmpeg.New(), t.TempDir(), nil)
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
	first := NewSessions(transcoder, t.TempDir(), nil)
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
	second := NewSessions(transcoder, t.TempDir(), nil)
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
	sessions := NewSessions(transcoder, t.TempDir(), nil)
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
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}
	path, err := sessions.FilePath(context.Background(), key, "/src/movie.mkv", 120, 1280, 720, false, "720p/index.m3u8")
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

package playback

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/ffmpeg/ffmpegtest"
	"github.com/beamfall/core/internal/governor"
	"github.com/beamfall/core/internal/store"
)

type fakeTranscoder struct {
	mu            sync.Mutex
	starts        []string
	segmentStarts []int
	renditions    []ffmpeg.Rendition
	segmentsToLay int
}

func (f *fakeTranscoder) StartHLS(_ context.Context, src, dir string, rendition ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, src)
	f.renditions = append(f.renditions, rendition)
	if err := os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n"), 0o600); err != nil {
		return nil, err
	}
	return &ffmpeg.HLSProcess{}, nil
}

func (f *fakeTranscoder) StartHLSSegments(_ context.Context, src, dir string, startSegment, _ int, rendition ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, src)
	f.segmentStarts = append(f.segmentStarts, startSegment)
	f.renditions = append(f.renditions, rendition)
	if err := os.WriteFile(filepath.Join(dir, "init.mp4"), []byte("init"), 0o600); err != nil {
		return nil, err
	}
	count := f.segmentsToLay
	if count <= 0 {
		count = 1
	}
	for i := startSegment; i < startSegment+count; i++ {
		name := filepath.Join(dir, fmt.Sprintf("seg%05d.m4s", i))
		if err := os.WriteFile(name, []byte("m4s"), 0o600); err != nil {
			return nil, err
		}
	}
	return &ffmpeg.HLSProcess{}, nil
}

// filePath720 requests one artifact for a 120s 1280x720 source.
func filePath720(sessions *Sessions, key SessionKey, name string) (string, error) {
	return sessions.FilePath(context.Background(), key, "/src/movie.mkv", 120, 1280, 720, false, name)
}

func TestMasterPlaylistAdvertisesOnlySustainableRungs(t *testing.T) {
	t.Parallel()
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	path, err := filePath720(sessions, key, "master.m3u8")
	if err != nil {
		t.Fatalf("master.m3u8: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	master := string(data)
	for _, want := range []string{
		"#EXTM3U",
		"#EXT-X-STREAM-INF:BANDWIDTH=4256000,RESOLUTION=1280x720\n720p/index.m3u8",
		"#EXT-X-STREAM-INF:BANDWIDTH=1756000,RESOLUTION=852x480\n480p/index.m3u8",
	} {
		if !strings.Contains(master, want) {
			t.Fatalf("master playlist missing %q:\n%s", want, master)
		}
	}
	// Honest ABR: a 720p source never advertises a 1080p rung.
	if strings.Contains(master, "1080p") {
		t.Fatalf("720p source advertises 1080p:\n%s", master)
	}
}

func TestSessionHEVCLadderFromGrantCaps(t *testing.T) {
	t.Parallel()
	transcoder := &hevcCapableTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}
	caps := ladderCaps{ClientMaxHeight: 1080, HEVC: true, HEVCMaxHeight: 2160}

	path, err := sessions.FilePathPriorityWithBurnSubtitleAudioLadder(context.Background(), key, "/src/movie.mkv", 120, 3840, 2160, false, false, caps, VideoSignal{}, -1, ffmpeg.AudioProfile{}, PriorityNormal, "master.m3u8")
	if err != nil {
		t.Fatalf("master.m3u8: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	master := string(data)
	if !strings.Contains(master, "#EXT-X-STREAM-INF:BANDWIDTH=25256000,RESOLUTION=3840x2160\n2160p-hevc/index.m3u8") {
		t.Fatalf("master playlist missing HEVC 2160p rung:\n%s", master)
	}
	if !strings.Contains(master, "#EXT-X-STREAM-INF:BANDWIDTH=8256000,RESOLUTION=1920x1080\n1080p/index.m3u8") {
		t.Fatalf("master playlist missing capped H.264 1080p rung:\n%s", master)
	}

	if _, err := sessions.FilePathPriorityWithBurnSubtitleAudioLadder(context.Background(), key, "/src/movie.mkv", 120, 3840, 2160, false, false, caps, VideoSignal{}, -1, ffmpeg.AudioProfile{}, PriorityNormal, "2160p-hevc/seg00000.m4s"); err != nil {
		t.Fatalf("HEVC segment: %v", err)
	}
	if got := transcoder.renditions[0]; got.VideoCodec != "hevc" || got.Height != 2160 || got.VideoKbps != 25000 {
		t.Fatalf("rendition = %+v, want 2160p HEVC at 25000kbps", got)
	}
}

func TestMasterPlaylistUnknownDimensionsOffersSingleSourceRung(t *testing.T) {
	t.Parallel()
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	path, err := sessions.FilePath(context.Background(), key, "/src/movie.mkv", 120, 0, 0, false, "master.m3u8")
	if err != nil {
		t.Fatalf("master.m3u8: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	master := string(data)
	if !strings.Contains(master, "source/index.m3u8") {
		t.Fatalf("master playlist missing the source rung:\n%s", master)
	}
	if strings.Contains(master, "RESOLUTION=") {
		t.Fatalf("unknown dimensions must not claim a RESOLUTION:\n%s", master)
	}
	if strings.Count(master, "#EXT-X-STREAM-INF") != 1 {
		t.Fatalf("unknown dimensions advertise more than the source rung:\n%s", master)
	}
}

func TestSessionsStartOnFirstSegmentAndReuse(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	// Playlists never start an encode (lazy session model).
	if _, err := filePath720(sessions, key, "master.m3u8"); err != nil {
		t.Fatal(err)
	}
	if _, err := filePath720(sessions, key, "720p/index.m3u8"); err != nil {
		t.Fatal(err)
	}
	if len(transcoder.starts) != 0 {
		t.Fatalf("playlist fetches started %d transcodes, want 0", len(transcoder.starts))
	}

	if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("seg0: %v", err)
	}
	if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("cached seg0: %v", err)
	}
	if len(transcoder.starts) != 1 {
		t.Fatalf("transcoder started %d times, want 1 (session reuse)", len(transcoder.starts))
	}
	if got := transcoder.renditions[0]; got.Height != 720 || got.VideoKbps != 4000 {
		t.Fatalf("rendition = %+v, want 720p/4000k", got)
	}
}

func TestSessionReplacesArtifactsWhenSourceChanges(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	for _, src := range []string{"/src/old.mkv", "/src/replaced.mkv"} {
		if _, err := sessions.FilePath(context.Background(), key, src, 120, 1280, 720, false, "720p/seg00000.m4s"); err != nil {
			t.Fatalf("segment for %s: %v", src, err)
		}
	}

	if got := transcoder.starts; len(got) != 2 || got[0] != "/src/old.mkv" || got[1] != "/src/replaced.mkv" {
		t.Fatalf("transcode sources = %v, want old then replacement", got)
	}
}

func TestAudioCopySelectionServesCopyVideoRung(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	filePath := func(name string) (string, error) {
		return sessions.FilePath(context.Background(), key, "/src/movie.mkv", 120, 1280, 720, true, name)
	}

	// The master advertises only the audio-copy rung — no resolution ladder to
	// climb when the video is copied through.
	master, err := filePath("master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(master)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, audioCopyRungID+"/index.m3u8") ||
		strings.Contains(got, "720p/") {
		t.Fatalf("master advertises the wrong rungs:\n%s", got)
	}

	// A served segment of the audio-copy rung copies the video and transcodes
	// only the audio: the live rendition carries CopyVideo.
	if _, err := filePath(audioCopyRungID + "/seg00000.m4s"); err != nil {
		t.Fatalf("audio-copy seg0: %v", err)
	}
	if len(transcoder.renditions) != 1 {
		t.Fatalf("transcoder started %d times, want 1", len(transcoder.renditions))
	}
	if got := transcoder.renditions[0]; !got.CopyVideo {
		t.Fatalf("rendition = %+v, want CopyVideo=true", got)
	}
}

func TestSessionRenditionCarriesAudioProfile(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}
	gain := -4.25

	if _, err := sessions.FilePathPriorityWithBurnSubtitleAudio(
		context.Background(),
		key,
		"/src/movie.mkv",
		120,
		1280,
		720,
		false,
		false,
		VideoSignal{},
		-1,
		ffmpeg.AudioProfile{Codec: "eac3", Channels: 6, GainDB: &gain},
		PriorityNormal,
		"720p/seg00000.m4s",
	); err != nil {
		t.Fatalf("seg0: %v", err)
	}
	if len(transcoder.renditions) != 1 {
		t.Fatalf("transcoder started %d times, want 1", len(transcoder.renditions))
	}
	got := transcoder.renditions[0]
	if got.AudioCodec != "eac3" || got.AudioChannels != 6 || got.AudioGainDB == nil || *got.AudioGainDB != gain {
		t.Fatalf("audio profile = codec %q channels %d gain %v, want eac3/6/%v", got.AudioCodec, got.AudioChannels, got.AudioGainDB, gain)
	}
}

func TestSessionRenditionCarriesAudioStreamIndex(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	selected := 2

	if _, err := sessions.FilePathPriorityWithBurnSubtitleAudioLadderMap(
		context.Background(),
		SessionKey{SessionID: "session-default", MediaID: "media-alpha"},
		"/src/movie.mkv", 120, 1280, 720, false, false, ladderCaps{}, VideoSignal{}, -1,
		ffmpeg.AudioProfile{}, &selected, PriorityNormal, "720p/seg00000.m4s",
	); err != nil {
		t.Fatalf("seg0: %v", err)
	}
	if len(transcoder.renditions) != 1 {
		t.Fatalf("transcoder started %d times, want 1", len(transcoder.renditions))
	}
	got := transcoder.renditions[0].AudioStreamIndex
	if got == nil || *got != 2 {
		t.Fatalf("AudioStreamIndex = %v, want 2", got)
	}
}

func TestSessionRenditionCarriesSourceVideoSignal(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}
	sourceVideo := VideoSignal{ColorTransfer: "smpte2084", ColorPrimaries: "bt2020", DoViProfile: 8, DoViBLCompatID: 6}

	if _, err := sessions.FilePathPriority(context.Background(), key, "/src/movie.mkv", 120, 1280, 720, false, false, sourceVideo, PriorityNormal, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("FilePathPriority: %v", err)
	}
	if len(transcoder.renditions) != 1 {
		t.Fatalf("transcoder started %d times, want 1", len(transcoder.renditions))
	}
	got := transcoder.renditions[0]
	if got.ColorTransfer != sourceVideo.ColorTransfer || got.ColorPrimaries != sourceVideo.ColorPrimaries || got.DoViProfile != sourceVideo.DoViProfile || got.DoViBLCompatID != sourceVideo.DoViBLCompatID {
		t.Fatalf("rendition source signal = %+v, want %+v", got, sourceVideo)
	}
}

func TestSessionRenditionCarriesBurnSubtitleSelection(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	if _, err := sessions.FilePathPriorityWithBurnSubtitle(context.Background(), key, "/src/movie.mkv", 120, 1280, 720, false, false, VideoSignal{}, 2, PriorityNormal, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("FilePathPriorityWithBurnSubtitle: %v", err)
	}
	if len(transcoder.renditions) != 1 {
		t.Fatalf("transcoder started %d times, want 1", len(transcoder.renditions))
	}
	index, ok := transcoder.renditions[0].BurnSubtitleIndex()
	if !ok || index != 2 {
		t.Fatalf("BurnSubtitleIndex = (%d,%v), want (2,true)", index, ok)
	}
}

func TestStandardSelectionDoesNotCopyVideo(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("seg0: %v", err)
	}
	if len(transcoder.renditions) != 1 {
		t.Fatalf("transcoder started %d times, want 1", len(transcoder.renditions))
	}
	if got := transcoder.renditions[0]; got.CopyVideo {
		t.Fatalf("standard rendition = %+v, want CopyVideo=false", got)
	}
}

func TestIsAudioCopyArtifact(t *testing.T) {
	t.Parallel()
	copyNames := []string{audioCopyRungID, audioCopyRungID + "/index.m3u8", audioCopyRungID + "/seg00000.m4s"}
	for _, name := range copyNames {
		if !IsAudioCopyArtifact(name) {
			t.Fatalf("IsAudioCopyArtifact(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"master.m3u8", "720p/index.m3u8", "audio-copying/seg.m4s", ""} {
		if IsAudioCopyArtifact(name) {
			t.Fatalf("IsAudioCopyArtifact(%q) = true, want false", name)
		}
	}
}

func TestSessionsRejectUnknownFileNames(t *testing.T) {
	t.Parallel()
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	for _, name := range []string{
		"../escape.ts", "etc/passwd", "seg1.m4s", "index.m3u8", "720p/seg1.m4s",
		"720p/live.m3u8", "1080p/index.m3u8", "720p/../../escape.m4s", "",
	} {
		if _, err := filePath720(sessions, key, name); err == nil {
			t.Fatalf("FilePath accepted %q", name)
		}
	}
}

func TestSessionsCloseRemovesWorkingDirs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sessions := NewSessions(&fakeTranscoder{}, root, nil)
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}
	if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}

	sessions.Close()

	entries, err := os.ReadDir(filepath.Join(root, "transcode"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("transcode dir has %d entries after Close, want 0", len(entries))
	}
}

func TestSessionsCanUseExplicitScratchDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	scratchDir := filepath.Join(root, "scratch")
	sessions := NewSessionsInDir(&fakeTranscoder{}, scratchDir, nil)
	defer sessions.Close()

	path, err := filePath720(sessions, SessionKey{SessionID: "session-default", MediaID: "media-alpha"}, "720p/seg00000.m4s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, scratchDir+string(os.PathSeparator)) {
		t.Fatalf("artifact path = %q, want under %q", path, scratchDir)
	}
	if _, err := os.Stat(filepath.Join(root, "transcode")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default transcode dir exists with explicit scratch dir: %v", err)
	}
}

func TestKnownDurationSynthesizesVODPlaylistPerRung(t *testing.T) {
	t.Parallel()
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	path, err := sessions.FilePath(context.Background(), key, "/src/movie.mkv", 10, 1280, 720, false, "720p/index.m3u8")
	if err != nil {
		t.Fatalf("FilePath returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	playlist := string(data)
	for _, want := range []string{
		"#EXT-X-VERSION:7",
		"#EXT-X-PLAYLIST-TYPE:VOD",
		"#EXT-X-MAP:URI=\"init.mp4\"",
		"#EXT-X-ENDLIST",
		"#EXTINF:4.000,\nseg00000.m4s",
		"#EXTINF:4.000,\nseg00001.m4s",
		"#EXTINF:2.000,\nseg00002.m4s", // 10s = 4+4+2
	} {
		if !strings.Contains(playlist, want) {
			t.Fatalf("playlist missing %q:\n%s", want, playlist)
		}
	}
	if strings.Contains(playlist, "seg00003.m4s") {
		t.Fatalf("playlist lists a segment past the duration:\n%s", playlist)
	}

	// The init segment the MAP line points at is servable from the rung.
	if _, err := sessions.FilePath(context.Background(), key, "/src/movie.mkv", 10, 1280, 720, false, "720p/init.mp4"); err != nil {
		t.Fatalf("init.mp4: %v", err)
	}
}

func TestAudioCopyTenSecondGOPSeeksStayWithinOneSegment(t *testing.T) {
	t.Parallel()
	ffmpegtest.Require(t)
	src := makeTenSecondGOPSource(t, 660)
	sessions := NewSessions(ffmpeg.New(), t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	indexPath, err := sessions.FilePath(context.Background(), key, src, 660, 64, 64, true, audioCopyRungID+"/index.m3u8")
	if err != nil {
		t.Fatalf("audio-copy index: %v", err)
	}
	playlistBytes, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	playlist := string(playlistBytes)
	for _, offset := range []int{-300, -30, 30, 300} {
		target := 330 + offset
		segment := target / audioCopySegmentSeconds
		name := fmt.Sprintf("%s/seg%05d.m4s", audioCopyRungID, segment)
		path, err := sessions.FilePath(context.Background(), key, src, 660, 64, 64, true, name)
		if err != nil {
			t.Fatalf("seek %+d (%s): %v", offset, name, err)
		}
		playlistAt, err := playlistStartTime(playlist, segment)
		if err != nil {
			t.Fatalf("seek %+d playlist time: %v", offset, err)
		}
		mediaAt := firstVideoPacketPTS(t, filepath.Join(filepath.Dir(path), initSegmentName), path)
		if drift := math.Abs(mediaAt - playlistAt); drift > audioCopySegmentSeconds {
			t.Fatalf("seek %+d segment %d drift %.3fs, want <= %ds (playlist %.3f media %.3f)", offset, segment, drift, audioCopySegmentSeconds, playlistAt, mediaAt)
		}
	}
}

func TestSeekPastFrontierRestartsTranscodeAtSegment(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{segmentsToLay: 2}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	// First request starts a transcode at zero (lays seg 0 and 1).
	if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("seg0: %v", err)
	}
	// Seeking far past the frontier restarts at the requested segment.
	if _, err := filePath720(sessions, key, "720p/seg00020.m4s"); err != nil {
		t.Fatalf("seg20: %v", err)
	}
	if len(transcoder.segmentStarts) != 2 || transcoder.segmentStarts[0] != 0 || transcoder.segmentStarts[1] != 20 {
		t.Fatalf("segmentStarts = %v, want [0 20]", transcoder.segmentStarts)
	}

	// A segment already on disk never restarts anything.
	if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("cached seg0: %v", err)
	}
	if len(transcoder.segmentStarts) != 2 {
		t.Fatalf("cached segment caused restart: %v", transcoder.segmentStarts)
	}

	// A request just past the frontier waits instead of restarting (the fake
	// already wrote seg21 as part of the restart batch).
	if _, err := filePath720(sessions, key, "720p/seg00021.m4s"); err != nil {
		t.Fatalf("seg21: %v", err)
	}
	if len(transcoder.segmentStarts) != 2 {
		t.Fatalf("near-frontier request restarted: %v", transcoder.segmentStarts)
	}

	// Segments past the declared duration are rejected.
	if _, err := filePath720(sessions, key, "720p/seg00030.m4s"); err == nil {
		t.Fatal("accepted a segment past the media duration")
	}
}

// TestTranscodeLatencyBudgetsOnReferenceClip pins ADR-0155 Decision 2.2/2.3
// as numeric, executable contracts. The synthetic 64x64 clip is the stable
// reference fixture: it exercises the real FFmpeg first-segment path and a
// far-seek re-transcode without coupling CI to operator hardware throughput.
// Keep this test serial: its bounded load generator is the only intentional
// contention, so unrelated test scheduling cannot make the SLO flaky.
//
// GATE-TRANSCODE-SLO-LOAD-FALSE-RED-1: this wall-clock SLO can still be
// blown by ambient host contention outside this test's control (sibling
// `make gate` lanes from the agent fleet), not just by the code under test.
// assertLatencyBudget reports that case as an explicit SLO_INCONCLUSIVE skip
// instead of a red -- see its doc comment for exactly when that applies. The
// policy is table-proved by TestSLOQuarantineDecision; the repeatable
// induced-load procedure for proving it on a real oversubscribed host is
// testing/slo-quarantine-induced-load.md.
func TestTranscodeLatencyBudgetsOnReferenceClip(t *testing.T) {
	if os.Getenv("BEAMFALL_PERF") != "1" {
		t.Skip("set BEAMFALL_PERF=1 to run the isolated playback performance lane")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatal("ffmpeg is required by the blocking playback performance lane")
	}
	const durationSeconds = 48
	injectedLatency := injectedPlaybackLatency(t)
	stopLoad := startControlledPlaybackLoad(t)
	defer stopLoad()
	t.Logf("reference_resources duration_s=%d input=64x64@1fps output=64p gomaxprocs=%d logical_cpus=%d injected_latency=%s host_load=%q", durationSeconds, runtime.GOMAXPROCS(0), runtime.NumCPU(), injectedLatency, hostLoadSnapshot())
	src := makeTenSecondGOPSource(t, durationSeconds)
	sessions := NewSessions(ffmpeg.New(), t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "latency-reference-session", MediaID: "latency-reference-media"}

	// Sample host load BEFORE the timed operation as well as at failure: load1
	// is a one-minute average, so the at-failure sample alone can still be high
	// from contention that has already drained. Both samples must show
	// oversubscription for the quarantine to fire (sloQuarantineDecision).
	firstLoadBefore := sampleHostLoad()
	startedAt := time.Now().Add(-injectedLatency)
	firstPath, err := sessions.FilePath(context.Background(), key, src, durationSeconds, 64, 64, false, "64p/seg00000.m4s")
	assertTranscodeCompleted(t, "transcoded first segment", err, injectedLatency > 0, firstLoadBefore)
	assertLatencyBudget(t, "transcoded first-segment", time.Since(startedAt), TranscodeStartLatencyBudget, injectedLatency > 0, firstLoadBefore)

	// Freeze the first generation at segment zero so the next request is
	// deterministically a live re-transcode, even on a very fast CI host that
	// could otherwise finish this tiny fixture before the test issues its seek.
	sessions.mu.Lock()
	proc := sessions.sessions[key].proc
	sessions.mu.Unlock()
	if proc != nil {
		proc.Stop()
	}
	entries, err := os.ReadDir(filepath.Dir(firstPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if segmentName.MatchString(entry.Name()) && entry.Name() != "seg00000.m4s" {
			if err := os.Remove(filepath.Join(filepath.Dir(firstPath), entry.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}

	seekLoadBefore := sampleHostLoad()
	seekStartedAt := time.Now().Add(-injectedLatency)
	_, err = sessions.FilePath(context.Background(), key, src, durationSeconds, 64, 64, false, "64p/seg00008.m4s")
	assertTranscodeCompleted(t, "post-seek transcoded segment", err, injectedLatency > 0, seekLoadBefore)
	assertLatencyBudget(t, "post-seek re-transcode", time.Since(seekStartedAt), PostSeekTranscodeLatencyBudget, injectedLatency > 0, seekLoadBefore)
	sessions.mu.Lock()
	seekBase := sessions.sessions[key].baseSegment
	sessions.mu.Unlock()
	if seekBase != 8 {
		t.Fatalf("post-seek transcode base segment = %d, want 8", seekBase)
	}
}

func TestInjectedPlaybackLatencyRegressionReds(t *testing.T) {
	if os.Getenv("BEAMFALL_PERF") != "1" {
		t.Skip("set BEAMFALL_PERF=1 to run the isolated playback performance lane")
	}
	// #nosec G204 G702 -- os.Args[0] is this test binary, not external input.
	cmd := exec.Command(os.Args[0], "-test.run=^TestTranscodeLatencyBudgetsOnReferenceClip$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), "BEAMFALL_PLAYBACK_LATENCY_INJECT=9s")
	output, err := cmd.CombinedOutput()
	// The injected 9s delay is a deterministic, host-independent synthetic
	// regression (see injectedPlaybackLatency): both quarantine checkpoints
	// (assertTranscodeCompleted, assertLatencyBudget) recognize the injected
	// path via the same `injected` bool and never attribute it to host
	// contention, regardless of load. So the inner run must always red here
	// -- an inner SLO_INCONCLUSIVE skip is itself a failure of this
	// meta-test, not a tolerated/skippable outcome, because it would mean a
	// genuine regression could hide behind ambient contention.
	if strings.Contains(string(output), "SLO_INCONCLUSIVE") {
		t.Fatalf("injected regression must never hit host-contention quarantine (bypass regressed):\n%s", output)
	}
	if err == nil {
		t.Fatalf("injected 9s playback regression unexpectedly passed:\n%s", output)
	}
	// The inner test can red at either checkpoint: assertLatencyBudget once
	// the 9s injected elapsed exceeds the 8s budget, or -- if the real
	// FFmpeg wait itself times out under load before that comparison runs
	// -- assertTranscodeCompleted's now-unquarantined context.DeadlineExceeded
	// fatal. Both are genuine reds for the injected path; either satisfies
	// this meta-test.
	wantExceedsBudget := strings.Contains(string(output), "exceeds budget 8s")
	wantDeadlineExceeded := strings.Contains(string(output), context.DeadlineExceeded.Error())
	if !wantExceedsBudget && !wantDeadlineExceeded {
		t.Fatalf("injected regression failed for the wrong reason:\n%s", output)
	}
	t.Log("injected_latency_red duration=9s budget=8s")
}

func TestTranscodeLatencyBudgetClassification(t *testing.T) {
	sessions := &Sessions{}
	session := &hlsSession{}
	path := filepath.Join(t.TempDir(), "seg00000.m4s")
	if got := sessions.transcodeLatencyBudget(session, path); got != TranscodeStartLatencyBudget {
		t.Fatalf("first-start budget = %v, want %v", got, TranscodeStartLatencyBudget)
	}
	session.started = true
	if got := sessions.transcodeLatencyBudget(session, path); got != PostSeekTranscodeLatencyBudget {
		t.Fatalf("post-seek budget = %v, want %v", got, PostSeekTranscodeLatencyBudget)
	}
	if err := os.WriteFile(path, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sessions.transcodeLatencyBudget(session, path); got != fileWaitTimeout {
		t.Fatalf("cached-artifact budget = %v, want %v", got, fileWaitTimeout)
	}
}

// TestSLOQuarantineDecision pins the SLO_INCONCLUSIVE quarantine policy
// deterministically, so the two dangerous directions are proved by table and
// not only by observing a real busy host: the injected regression must red at
// any load, and a genuine non-injected regression must red unless the host was
// sustainedly oversubscribed across the whole measurement.
func TestSLOQuarantineDecision(t *testing.T) {
	t.Parallel()
	const numCPU = 12
	for _, tc := range []struct {
		name          string
		injected      bool
		loadBefore    float64
		loadAtFailure float64
		ok            bool
		want          bool
	}{
		// The injected synthetic regression is host-independent: it must red
		// at every load, including loads that would quarantine a real overrun.
		{name: "injected quiet host", injected: true, loadBefore: 0.1, loadAtFailure: 0.1, ok: true, want: false},
		{name: "injected sustained overload", injected: true, loadBefore: 96, loadAtFailure: 284, ok: true, want: false},
		{name: "injected load unavailable", injected: true, ok: false, want: false},

		// Non-injected: quarantine only on sustained oversubscription.
		{name: "sustained overload quarantines", injected: false, loadBefore: 165, loadAtFailure: 284, ok: true, want: true},
		{name: "barely sustained overload quarantines", injected: false, loadBefore: 12.01, loadAtFailure: 12.01, ok: true, want: true},
		{name: "quiet host reds", injected: false, loadBefore: 1.5, loadAtFailure: 2.0, ok: true, want: false},
		// The load1 staleness case this fence exists for: a drained burst
		// leaves the one-minute average high at failure, but the host was
		// already quiet when the timed operation started -- a genuine
		// regression, and it must red.
		{name: "stale average after drained burst reds", injected: false, loadBefore: 3, loadAtFailure: 96, ok: true, want: false},
		// Contention that only began before the operation and cleared by the
		// failure did not span the measurement either.
		{name: "contention cleared during measurement reds", injected: false, loadBefore: 96, loadAtFailure: 3, ok: true, want: false},

		// Boundary: fully subscribed is not oversubscribed.
		{name: "boundary equal to numCPU both samples reds", injected: false, loadBefore: numCPU, loadAtFailure: numCPU, ok: true, want: false},
		{name: "boundary equal at failure reds", injected: false, loadBefore: 50, loadAtFailure: numCPU, ok: true, want: false},
		{name: "boundary equal before reds", injected: false, loadBefore: numCPU, loadAtFailure: 50, ok: true, want: false},

		// Fail closed: unknown host load is a red, never a skip.
		{name: "load unavailable reds even at high readings", injected: false, loadBefore: 165, loadAtFailure: 284, ok: false, want: false},
		{name: "load unavailable zero readings reds", injected: false, ok: false, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sloQuarantineDecision(tc.injected, tc.loadBefore, tc.loadAtFailure, tc.ok, numCPU)
			if got != tc.want {
				t.Fatalf("sloQuarantineDecision(injected=%v, before=%v, atFailure=%v, ok=%v, numCPU=%d) = %v, want %v",
					tc.injected, tc.loadBefore, tc.loadAtFailure, tc.ok, numCPU, got, tc.want)
			}
		})
	}
}

// TestHostLoadAverageParsing pins hostLoad1's parse/classification half against
// both platform `uptime` formats and against garbage, because an
// unparseable reading must classify as ok=false, which sloQuarantineDecision
// then resolves to a red rather than a skip.
func TestHostLoadAverageParsing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		output   string
		wantLoad float64
		wantOK   bool
	}{
		{
			name:     "macos format",
			output:   "13:54  up 7 days,  5:31, 6 users, load averages: 14.84 15.51 22.30\n",
			wantLoad: 14.84,
			wantOK:   true,
		},
		{
			name:     "linux format",
			output:   " 13:54:02 up 7 days,  5:31,  6 users,  load average: 1.23, 4.56, 7.89\n",
			wantLoad: 1.23,
			wantOK:   true,
		},
		{
			name:     "high load reading",
			output:   "10:25  up 7 days,  2:03, 6 users, load averages: 284.31 165.02 96.44\n",
			wantLoad: 284.31,
			wantOK:   true,
		},
		{name: "empty output", output: "", wantOK: false},
		{name: "unrecognized format", output: "uptime: command produced nothing useful\n", wantOK: false},
		{
			name:   "load label without a decimal number",
			output: "13:54  up 7 days, load averages: n/a n/a n/a\n",
			wantOK: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			load, ok := parseLoadAverage1(tc.output)
			if ok != tc.wantOK {
				t.Fatalf("parseLoadAverage1(%q) ok = %v, want %v", tc.output, ok, tc.wantOK)
			}
			if ok && load != tc.wantLoad {
				t.Fatalf("parseLoadAverage1(%q) load = %v, want %v", tc.output, load, tc.wantLoad)
			}
			if !ok && load != 0 {
				t.Fatalf("parseLoadAverage1(%q) load = %v on failure, want 0", tc.output, load)
			}
		})
	}
}

func injectedPlaybackLatency(t *testing.T) time.Duration {
	t.Helper()
	raw := os.Getenv("BEAMFALL_PLAYBACK_LATENCY_INJECT")
	if raw == "" {
		return 0
	}
	delay, err := time.ParseDuration(raw)
	if err != nil || delay < 0 {
		t.Fatalf("BEAMFALL_PLAYBACK_LATENCY_INJECT=%q must be a non-negative duration", raw)
	}
	return delay
}

func startControlledPlaybackLoad(t *testing.T) func() {
	t.Helper()
	raw := os.Getenv("BEAMFALL_PERF_LOAD_WORKERS")
	workers, err := strconv.Atoi(raw)
	if err != nil || workers < 1 {
		t.Fatalf("BEAMFALL_PERF_LOAD_WORKERS=%q must be a positive integer", raw)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var operations atomic.Uint64
	started := time.Now()
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var completed uint64
			for {
				select {
				case <-stop:
					operations.Add(completed)
					return
				default:
				}
				for range 100_000 {
					completed++
				}
			}
		}()
	}
	t.Logf("controlled_load_start workers=%d host_load=%q", workers, hostLoadSnapshot())
	return func() {
		close(stop)
		wg.Wait()
		t.Logf("controlled_load_stop workers=%d operations=%d elapsed=%s host_load=%q", workers, operations.Load(), time.Since(started), hostLoadSnapshot())
	}
}

func hostLoadSnapshot() string {
	output, err := exec.Command("uptime").Output()
	if err != nil {
		return "unavailable: " + err.Error()
	}
	return strings.TrimSpace(string(output))
}

// loadAverageRE extracts the 1-minute load average from `uptime`, matching
// both the macOS ("load averages: 1.23 4.56 7.89") and Linux
// ("load average: 1.23, 4.56, 7.89") formats.
var loadAverageRE = regexp.MustCompile(`load averages?:\s*([0-9]+\.[0-9]+)`)

// parseLoadAverage1 is the pure half of hostLoad1: it classifies raw `uptime`
// output into a 1-minute load average, or ok=false when the format is not
// recognized. Kept separate so the parse/classification path is deterministically
// testable (TestHostLoadAverageParsing) without shelling out.
func parseLoadAverage1(uptimeOutput string) (load float64, ok bool) {
	m := loadAverageRE.FindStringSubmatch(uptimeOutput)
	if m == nil {
		return 0, false
	}
	load, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return load, true
}

// hostLoad1 returns the current 1-minute load average, or ok=false if it
// can't be determined (no `uptime`, or unrecognized output format).
func hostLoad1() (load float64, ok bool) {
	output, err := exec.Command("uptime").Output()
	if err != nil {
		return 0, false
	}
	return parseLoadAverage1(string(output))
}

// hostLoadSample is one hostLoad1 reading taken at a known point in time.
// The quarantine needs two of them (see sloQuarantineDecision).
type hostLoadSample struct {
	load float64
	ok   bool
}

func sampleHostLoad() hostLoadSample {
	load, ok := hostLoad1()
	return hostLoadSample{load: load, ok: ok}
}

// sloQuarantineDecision is the whole quarantine policy, as a pure function so
// it can be proved by table test (TestSLOQuarantineDecision) rather than only
// by observing a real busy host. It returns true only for an outcome that is
// genuinely inconclusive rather than a red.
//
// Three fences, all of which must hold:
//
//   - injected == false. The deterministic synthetic regression
//     (injectedPlaybackLatency) costs no host resources, so it is never
//     attributable to contention and must always red at every checkpoint.
//   - ok == true. Both load samples were actually readable. When the host load
//     is unknown we fail CLOSED to a red: an unreadable `uptime` must never
//     become a licence to skip an SLO failure.
//   - loadBefore > numCPU AND loadAtFailure > numCPU. load1 is a one-minute
//     *average*, so a single sample taken at the moment of failure is stale by
//     construction: a burst that has already drained (the gate's own 8-way
//     parallel batch peaks near load1=96 on a 12-core host, tools/gate/gate.sh)
//     can leave load1 above NumCPU on a host that is quiet again by the time
//     the timed operation runs, which would quarantine a GENUINE regression.
//     Requiring the host to be oversubscribed both BEFORE the timed operation
//     starts and AT the failure narrows the window to sustained oversubscription
//     that actually spans the measurement. Boundary load == numCPU is fully
//     subscribed, not oversubscribed, and does not quarantine.
//
// The dangerous direction is over-quarantine (hiding a real regression), so
// every ambiguous case above resolves to "red", never "skip".
func sloQuarantineDecision(injected bool, loadBefore, loadAtFailure float64, ok bool, numCPU int) bool {
	if injected || !ok {
		return false
	}
	cores := float64(numCPU)
	return loadBefore > cores && loadAtFailure > cores
}

// assertTranscodeCompleted handles the other half of the
// GATE-TRANSCODE-SLO-LOAD-FALSE-RED-1 exposure: under severe contention, the
// production wait can hit its own context.WithTimeout(ctx, waitBudget)
// deadline (sessions.go) before FFmpeg finishes, so FilePath returns a bare
// context.DeadlineExceeded instead of a slow-but-successful result -- the
// elapsed-vs-budget comparison in assertLatencyBudget below is never
// reached. The synthetic backdating in TestInjectedPlaybackLatencyRegressionReds
// never touches the real wait clock, so on the non-injected path a
// deadline-exceeded error here is always about actual host timing, not an
// injected delay, and may be quarantined the same way assertLatencyBudget
// quarantines it.
//
// injected mirrors assertLatencyBudget's parameter of the same name: when
// this call belongs to the injected-regression path (see
// injectedPlaybackLatency), the bypass never applies here either, so a
// context.DeadlineExceeded still reds instead of being swallowed as
// SLO_INCONCLUSIVE. Without that, a genuine regression exercised through the
// injected path could hit this checkpoint under contention and get skipped
// before assertLatencyBudget ever runs -- defeating the guarantee that the
// injected path always reds regardless of host load. Any other error is a
// real failure and always reds.
//
// before is the load sample taken just before the timed operation started;
// sloQuarantineDecision requires it and the at-failure sample to agree, so a
// one-minute average left high by already-drained contention cannot quarantine
// a genuine regression measured on a now-quiet host.
func assertTranscodeCompleted(t *testing.T, label string, err error, injected bool, before hostLoadSample) {
	t.Helper()
	if err == nil {
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		atFailure := sampleHostLoad()
		if sloQuarantineDecision(injected, before.load, atFailure.load, before.ok && atFailure.ok, runtime.NumCPU()) {
			t.Skipf("SLO_INCONCLUSIVE reason=host_contention step=%q err=%v load1_before=%.2f load1_at_failure=%.2f logical_cpus=%d host_load=%q", label, err, before.load, atFailure.load, runtime.NumCPU(), hostLoadSnapshot())
		}
	}
	t.Fatalf("%s: %v", label, err)
}

// assertLatencyBudget is the GATE-TRANSCODE-SLO-LOAD-FALSE-RED-1 fix: the
// real-encoder wall-clock SLO tracked by ADR-0155 Decision 2.2/2.3 must
// still red a genuine transcode regression, but must not misattribute
// ambient host contention (sibling `make gate` lanes from the agent fleet)
// to the code under test.
//
// injected marks a deterministic, host-independent synthetic delay applied
// by TestInjectedPlaybackLatencyRegressionReds (see injectedPlaybackLatency):
// it costs no host resources, so any overrun it causes is attributable to
// the code path being exercised, never to contention, and must always red.
//
// Otherwise, an overrun is only converted to an explicit, logged,
// non-silent inconclusive skip -- never a silent pass -- when the host is
// objectively oversubscribed for the whole span of the measurement (runnable
// load exceeding available logical cores both before the timed operation
// started and at the failure; see sloQuarantineDecision). A fast host never
// reaches this check at all (the early return above), so a genuine regression
// measured on an uncontended host still reds exactly as before.
func assertLatencyBudget(t *testing.T, label string, elapsed, budget time.Duration, injected bool, before hostLoadSample) {
	t.Helper()
	if elapsed <= budget {
		return
	}
	atFailure := sampleHostLoad()
	if sloQuarantineDecision(injected, before.load, atFailure.load, before.ok && atFailure.ok, runtime.NumCPU()) {
		t.Skipf("SLO_INCONCLUSIVE reason=host_contention step=%q elapsed=%s budget=%s load1_before=%.2f load1_at_failure=%.2f logical_cpus=%d host_load=%q", label, elapsed, budget, before.load, atFailure.load, runtime.NumCPU(), hostLoadSnapshot())
	}
	t.Fatalf("%s latency %v exceeds budget %v", label, elapsed, budget)
}

func TestRungSwitchReplacesProcessAtRequestedSegment(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{segmentsToLay: 2}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-default", MediaID: "media-alpha"}

	if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("720p seg0: %v", err)
	}
	// Mid-stream quality switch: same playback session, new rendition stream
	// resuming at the playhead segment (ADR-0010 §8 restart-at-a-new-bitrate).
	if _, err := filePath720(sessions, key, "480p/seg00010.m4s"); err != nil {
		t.Fatalf("480p seg10: %v", err)
	}
	if len(transcoder.renditions) != 2 {
		t.Fatalf("starts = %d, want 2 (switch replaces, never runs a sibling)", len(transcoder.renditions))
	}
	if got := transcoder.renditions[1]; got.Height != 480 || got.VideoKbps != 1500 {
		t.Fatalf("switched rendition = %+v, want 480p/1500k", got)
	}
	if transcoder.segmentStarts[1] != 10 {
		t.Fatalf("switch started at segment %d, want 10", transcoder.segmentStarts[1])
	}
	// One live transcode per session throughout.
	if got := len(sessions.ActiveKeys()); got != 1 {
		t.Fatalf("ActiveKeys = %d, want 1", got)
	}

	// Old-rung segments already on disk stay servable without a restart.
	if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("old rung cached seg: %v", err)
	}
	if len(transcoder.renditions) != 2 {
		t.Fatalf("cached old-rung segment restarted the encoder: %d starts", len(transcoder.renditions))
	}
}

func TestTranscodeCapRefusesWhenAllSlotsAreActive(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	sessions.SetTranscodeCap(2)

	keyA := SessionKey{SessionID: "session-a", MediaID: "media-alpha"}
	keyB := SessionKey{SessionID: "session-b", MediaID: "media-alpha"}
	keyC := SessionKey{SessionID: "session-c", MediaID: "media-alpha"}
	for _, key := range []SessionKey{keyA, keyB} {
		if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
			t.Fatal(err)
		}
	}
	if sessions.CanAdmit(keyC) {
		t.Fatal("CanAdmit = true at capacity with no idle session")
	}
	// Both sessions fetched just now (actively streaming): never preempted —
	// the new session is refused instead (ADR-0010 §6: no mid-stream failure).
	if _, err := filePath720(sessions, keyC, "720p/seg00000.m4s"); !errors.Is(err, ErrTranscodeCapacity) {
		t.Fatalf("err = %v, want ErrTranscodeCapacity", err)
	}
	// The admitted sessions keep their slots.
	if got := len(sessions.ActiveKeys()); got != 2 {
		t.Fatalf("ActiveKeys = %d, want 2", got)
	}
}

const (
	performanceScaleTranscodes      = 4
	performanceScaleTranscodeFloor  = 3 * time.Second
	performanceScaleTranscodeMemory = 32 << 20
)

// TestPerformanceScaleConcurrentTranscodeFloor proves the configured M-way
// admission floor starts and serves every session without serialization or
// memory collapse. The fake transcoder keeps this deterministic and hardware-
// independent while exercising the production session/admission/file path.
func TestPerformanceScaleConcurrentTranscodeFloor(t *testing.T) {
	if os.Getenv("BEAMFALL_PERF") != "1" {
		t.Skip("set BEAMFALL_PERF=1 (= make bench) to run performance floors")
	}
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	sessions.SetTranscodeCap(performanceScaleTranscodes)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	errCh := make(chan error, performanceScaleTranscodes)
	var wg sync.WaitGroup
	for i := range performanceScaleTranscodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := SessionKey{SessionID: store.SessionID(fmt.Sprintf("perf-session-%d", i)), MediaID: "media-alpha"}
			_, err := filePath720(sessions, key, "720p/seg00000.m4s")
			errCh <- err
		}()
	}
	wg.Wait()
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Errorf("concurrent transcode: %v", err)
		}
	}
	if got := len(sessions.ActiveKeys()); got != performanceScaleTranscodes {
		t.Errorf("active transcodes = %d, want %d", got, performanceScaleTranscodes)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("transcode: sessions=%d elapsed=%s allocated=%dMiB", performanceScaleTranscodes, elapsed.Round(time.Millisecond), allocated>>20)
	if elapsed > performanceScaleTranscodeFloor {
		t.Errorf("%d concurrent transcodes took %s, floor %s", performanceScaleTranscodes, elapsed, performanceScaleTranscodeFloor)
	}
	if allocated > performanceScaleTranscodeMemory {
		t.Errorf("concurrent transcodes allocated %d bytes, ceiling %d", allocated, performanceScaleTranscodeMemory)
	}
}

// filePath720Priority requests one artifact for a 120s 1280x720 source at an
// explicit admission rank.
func filePath720Priority(sessions *Sessions, key SessionKey, priority Priority, name string) (string, error) {
	return sessions.FilePathPriority(context.Background(), key, "/src/movie.mkv", 120, 1280, 720, false, false, VideoSignal{}, priority, name)
}

// filePath720Degraded requests one artifact for a 120s 1280x720 source as a
// capacity-degraded Watch-Party session.
func filePath720Degraded(sessions *Sessions, key SessionKey, name string) (string, error) {
	return sessions.FilePathPriority(context.Background(), key, "/src/movie.mkv", 120, 1280, 720, false, true, VideoSignal{}, PriorityNormal, name)
}

// TestDegradedSessionAdmitsOverCapAndServesLowestRungOnly proves the PANEL-16
// degrade: a Watch-Party member at a full cap with no preemptable victim is
// admitted over the cap (ADR-0010 §6 "drop to this quality") and its master
// advertises only the single lowest rung (also honoring §8: one rendition).
func TestDegradedSessionAdmitsOverCapAndServesLowestRungOnly(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	sessions.SetTranscodeCap(2)

	keyA := SessionKey{SessionID: "session-a", MediaID: "media-alpha"}
	keyB := SessionKey{SessionID: "session-b", MediaID: "media-alpha"}
	keyC := SessionKey{SessionID: "session-c", MediaID: "media-alpha"}
	keyD := SessionKey{SessionID: "session-d", MediaID: "media-alpha"}
	for _, key := range []SessionKey{keyA, keyB} {
		if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
			t.Fatal(err)
		}
	}
	// A normal start is refused at the full cap (both peers actively streaming).
	if _, err := filePath720(sessions, keyD, "720p/seg00000.m4s"); !errors.Is(err, ErrTranscodeCapacity) {
		t.Fatalf("normal start err = %v, want ErrTranscodeCapacity", err)
	}
	// The degraded member's master advertises only the lowest rung (480p), never
	// the 720p the source could otherwise back. The session is created degraded on
	// its first (master) fetch, exactly as the degrade grant drives in production.
	master, err := filePath720Degraded(sessions, keyC, "master.m3u8")
	if err != nil {
		t.Fatalf("degraded master: %v", err)
	}
	data, err := os.ReadFile(master)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, "480p/index.m3u8") || strings.Contains(got, "720p") {
		t.Fatalf("degraded master is not lowest-rung-only:\n%s", got)
	}
	// And the segment fetch admits the encode over the cap rather than refusing.
	if _, err := filePath720Degraded(sessions, keyC, "480p/seg00000.m4s"); err != nil {
		t.Fatalf("degraded segment over cap: %v", err)
	}
	if got := len(sessions.ActiveKeys()); got != 3 {
		t.Fatalf("ActiveKeys = %d, want 3 (degraded admitted over cap of 2)", got)
	}
}

// TestDegradedAdmissionPrefersPreemptionBeforeOverCap proves over-cap admission
// is the last resort: when an idle session can be reaped for the slot, the
// degraded start takes it instead of over-subscribing the cap.
func TestDegradedAdmissionPrefersPreemptionBeforeOverCap(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := &now
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), func() time.Time { return *clock })
	defer sessions.Close()
	sessions.SetTranscodeCap(1)

	idle := SessionKey{SessionID: "session-idle", MediaID: "media-alpha"}
	if _, err := filePath720(sessions, idle, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	// Let the first session go idle past the preempt window.
	*clock = now.Add(preemptIdleAfter + time.Second)

	newcomer := SessionKey{SessionID: "session-new", MediaID: "media-alpha"}
	if _, err := filePath720Degraded(sessions, newcomer, "480p/seg00000.m4s"); err != nil {
		t.Fatalf("degraded start: %v", err)
	}
	// The idle session was preempted, so the cap of 1 is respected — not exceeded.
	if got := len(sessions.ActiveKeys()); got != 1 {
		t.Fatalf("ActiveKeys = %d, want 1 (idle preempted, not over-subscribed)", got)
	}
}

func TestHigherPriorityPreemptsActiveLowerPrioritySession(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), clock)
	defer sessions.Close()
	sessions.SetTranscodeCap(1)

	normal := SessionKey{SessionID: "session-normal", MediaID: "media-alpha"}
	fg := SessionKey{SessionID: "session-foreground", MediaID: "media-alpha"}

	// A normal stream takes the only slot and is actively streaming (just
	// fetched), so the idle rule cannot touch it.
	if _, err := filePath720Priority(sessions, normal, PriorityNormal, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}

	// A normal newcomer is refused (active equal-rank peer is protected).
	if sessions.CanAdmitPriority(fg, PriorityNormal) {
		t.Fatal("CanAdmitPriority(normal) = true against an active equal-rank stream")
	}
	// A foreground newcomer outranks the active normal stream and may preempt it
	// (ADR-0010 §6: the lower-priority stream is the one that drops/queues).
	if !sessions.CanAdmitPriority(fg, PriorityForeground) {
		t.Fatal("CanAdmitPriority(foreground) = false against an active lower-rank stream")
	}
	if _, err := filePath720Priority(sessions, fg, PriorityForeground, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("foreground preempting start: %v", err)
	}
	keys := sessions.ActiveKeys()
	if len(keys) != 1 || keys[0] != fg {
		t.Fatalf("ActiveKeys = %v, want [%v] (foreground evicted the normal stream)", keys, fg)
	}
}

func TestNormalPriorityNeverPreemptsActiveForeground(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), clock)
	defer sessions.Close()
	sessions.SetTranscodeCap(1)

	fg := SessionKey{SessionID: "session-foreground", MediaID: "media-alpha"}
	normal := SessionKey{SessionID: "session-normal", MediaID: "media-alpha"}

	if _, err := filePath720Priority(sessions, fg, PriorityForeground, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	// A normal newcomer cannot evict an actively-streaming foreground stream.
	if sessions.CanAdmitPriority(normal, PriorityNormal) {
		t.Fatal("CanAdmitPriority(normal) = true against an active foreground stream")
	}
	if _, err := filePath720Priority(sessions, normal, PriorityNormal, "720p/seg00000.m4s"); !errors.Is(err, ErrTranscodeCapacity) {
		t.Fatalf("err = %v, want ErrTranscodeCapacity", err)
	}
	// But once the foreground stream goes idle past the window, the idle rule
	// still lets the normal newcomer take the slot — preemption stacks on top of
	// the idle rule, it does not replace it.
	now = now.Add(preemptIdleAfter + time.Second)
	if _, err := filePath720Priority(sessions, normal, PriorityNormal, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("normal start after idle window: %v", err)
	}
	keys := sessions.ActiveKeys()
	if len(keys) != 1 || keys[0] != normal {
		t.Fatalf("ActiveKeys = %v, want [%v]", keys, normal)
	}
}

func TestSessionPriorityRaisesNeverLowers(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-a", MediaID: "media-alpha"}

	// Start normal, then re-request the same (on-disk) segment at foreground:
	// standing is raised without forcing an encode restart.
	if _, err := filePath720Priority(sessions, key, PriorityNormal, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	if _, err := filePath720Priority(sessions, key, PriorityForeground, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	if got := sessions.sessions[key].priority; got != PriorityForeground {
		t.Fatalf("priority = %d after raise, want %d", got, PriorityForeground)
	}
	// A later normal request must not lower the earned foreground standing.
	if _, err := filePath720Priority(sessions, key, PriorityNormal, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	if got := sessions.sessions[key].priority; got != PriorityForeground {
		t.Fatalf("priority = %d after a normal re-request, want it kept at %d", got, PriorityForeground)
	}
}

func TestTranscodeCapPreemptsIdleSessionForNewOne(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	transcoder := &fakeTranscoder{}
	sessions := NewSessions(transcoder, t.TempDir(), clock)
	defer sessions.Close()
	sessions.SetTranscodeCap(1)

	keyIdle := SessionKey{SessionID: "session-idle", MediaID: "media-alpha"}
	keyNew := SessionKey{SessionID: "session-new", MediaID: "media-alpha"}
	if _, err := filePath720(sessions, keyIdle, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}

	// Still actively streaming: the newcomer is refused.
	if sessions.CanAdmit(keyNew) {
		t.Fatal("CanAdmit = true while the only slot is actively streaming")
	}

	// After the idle window the paused session is preempted; it resumes
	// transparently on its next fetch.
	now = now.Add(preemptIdleAfter + time.Second)
	if !sessions.CanAdmit(keyNew) {
		t.Fatal("CanAdmit = false with a preemptable idle session")
	}
	if _, err := filePath720(sessions, keyNew, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("preempting start: %v", err)
	}
	keys := sessions.ActiveKeys()
	if len(keys) != 1 || keys[0] != keyNew {
		t.Fatalf("ActiveKeys = %v, want [%v]", keys, keyNew)
	}
}

// TestWaitForFileWakesOnFileCreation drives the event path: the file is absent
// when waitForFile starts, then appears with content a moment later, and the
// wait returns nil well within the deadline (not after a fixed poll quantum).
func TestWaitForFileWakesOnFileCreation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "seg00000.m4s")
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, []byte("m4s"), 0o600)
	}()
	start := time.Now()
	if err := waitForFile(context.Background(), path); err != nil {
		t.Fatalf("waitForFile: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= safetyPollInterval {
		t.Fatalf("waited %v (>= safety interval %v): event path did not fire", elapsed, safetyPollInterval)
	}
}

// TestWaitForFileIgnoresZeroByteFile holds the exact readiness contract: a
// created-but-empty file (ffmpeg mid-write) is not ready; only a non-empty body
// satisfies the wait.
func TestWaitForFileIgnoresZeroByteFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "seg00000.m4s")
	if err := os.WriteFile(path, nil, 0o600); err != nil { // zero bytes: not ready
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, []byte("m4s"), 0o600)
	}()
	if err := waitForFile(context.Background(), path); err != nil {
		t.Fatalf("waitForFile: %v", err)
	}
}

// TestWaitForFileFastPathWhenPresent returns immediately when the file already
// exists with content, never arming a watch.
func TestWaitForFileFastPathWhenPresent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "seg00000.m4s")
	if err := os.WriteFile(path, []byte("m4s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := waitForFile(context.Background(), path); err != nil {
		t.Fatalf("waitForFile: %v", err)
	}
}

// TestWaitForFileHonorsContextCancellation returns ctx.Err() when the caller
// cancels before the file appears — the wait must not block past cancellation.
func TestWaitForFileHonorsContextCancellation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "seg00000.m4s")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	if err := waitForFile(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForFile err = %v, want context.Canceled", err)
	}
}

// TestPollForFileTimesOut keeps the fallback's deadline contract: when the file
// never appears, the slow path returns the "stream not ready" sentinel.
func TestPollForFileTimesOut(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "never.m4s")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := pollForFile(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pollForFile err = %v, want context.DeadlineExceeded", err)
	}
}

// TestScanFrontierReadsHighestSegmentOffLock confirms the off-lock frontier scan
// returns the highest produced segment at or above base, and base-1 when the
// directory is empty or missing — the same answer the old under-lock ReadDir gave.
func TestScanFrontierReadsHighestSegmentOffLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if got := scanFrontier(dir, 5); got != 4 {
		t.Fatalf("empty dir frontier = %d, want 4 (base-1)", got)
	}
	if got := scanFrontier(filepath.Join(dir, "missing"), 5); got != 4 {
		t.Fatalf("missing dir frontier = %d, want 4 (base-1)", got)
	}
	for _, seg := range []int{3, 5, 6, 7} { // seg3 is below base, ignored
		name := filepath.Join(dir, fmt.Sprintf("seg%05d.m4s", seg))
		if err := os.WriteFile(name, []byte("m4s"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "init.mp4"), []byte("x"), 0o600); err != nil {
		t.Fatal(err) // non-segment file must not be counted
	}
	if got := scanFrontier(dir, 5); got != 7 {
		t.Fatalf("frontier = %d, want 7 (highest at/above base)", got)
	}
}

func makeTenSecondGOPSource(t *testing.T, durationSeconds int) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "ten-second-gop.mp4")
	cmd := exec.Command("ffmpeg",
		"-v", "error",
		"-f", "lavfi",
		"-i", fmt.Sprintf("testsrc=duration=%d:size=64x64:rate=1", durationSeconds),
		"-f", "lavfi",
		"-i", fmt.Sprintf("sine=frequency=440:duration=%d", durationSeconds),
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-g", strconv.Itoa(audioCopySegmentSeconds),
		"-keyint_min", strconv.Itoa(audioCopySegmentSeconds),
		"-sc_threshold", "0",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-shortest",
		"-y", src,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthesize 10s-GOP source: %v\n%s", err, out)
	}
	return src
}

func playlistStartTime(playlist string, segment int) (float64, error) {
	var current float64
	lines := strings.Split(playlist, "\n")
	for i := 0; i+1 < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "#EXTINF:") {
			continue
		}
		durationText := strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ",")
		duration, err := strconv.ParseFloat(durationText, 64)
		if err != nil {
			return 0, err
		}
		if strings.TrimSpace(lines[i+1]) == fmt.Sprintf("seg%05d.m4s", segment) {
			return current, nil
		}
		current += duration
	}
	return 0, fmt.Errorf("segment %d not listed", segment)
}

func firstVideoPacketPTS(t *testing.T, initPath, segmentPath string) float64 {
	t.Helper()
	combined := filepath.Join(t.TempDir(), "segment.mp4")
	initBytes, err := os.ReadFile(initPath)
	if err != nil {
		t.Fatal(err)
	}
	segmentBytes, err := os.ReadFile(segmentPath)
	if err != nil {
		t.Fatal(err)
	}
	body := append(initBytes, segmentBytes...)
	if err := os.WriteFile(combined, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "packet=pts_time",
		"-of", "csv=p=0",
		combined,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe segment: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		for _, field := range strings.Split(strings.TrimSpace(line), ",") {
			if pts, err := strconv.ParseFloat(field, 64); err == nil {
				return pts
			}
		}
	}
	t.Fatalf("ffprobe segment produced no video packet PTS:\n%s", out)
	return 0
}

// failStartTranscoder fails every process launch, exercising the governor
// transcode-lease rollback path in ensureRung.
type failStartTranscoder struct{}

func (failStartTranscoder) StartHLS(context.Context, string, string, ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	return nil, errors.New("ffmpeg launch failed")
}

func (failStartTranscoder) StartHLSSegments(context.Context, string, string, int, int, ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	return nil, errors.New("ffmpeg launch failed")
}

// TestTranscodeLeaseHeldWhileLiveAndReleasedOnClose proves a session holds one
// governor realtime-transcode lease while it has a live process — preempting
// background generation — and releases it on teardown.
func TestTranscodeLeaseHeldWhileLiveAndReleasedOnClose(t *testing.T) {
	g := governor.New()
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	sessions.SetGovernor(g)
	key := SessionKey{SessionID: "session-a", MediaID: "media-a"}

	if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("start transcode: %v", err)
	}
	if got := g.ActiveTranscodes(); got != 1 {
		t.Fatalf("ActiveTranscodes = %d, want 1 while a session is live", got)
	}
	if g.GenerationAllowed() {
		t.Fatal("generation allowed while a live transcode holds a lease")
	}

	sessions.Close()
	if got := g.ActiveTranscodes(); got != 0 {
		t.Fatalf("ActiveTranscodes after Close = %d, want 0", got)
	}
	if !g.GenerationAllowed() {
		t.Fatal("generation still blocked after every transcode was torn down")
	}
}

// TestTranscodeLeaseRolledBackOnLaunchFailure proves an FFmpeg launch failure
// releases the transcode lease acquired before the launch, so a failed start
// never leaves a phantom transcode blocking generation forever.
func TestTranscodeLeaseRolledBackOnLaunchFailure(t *testing.T) {
	g := governor.New()
	sessions := NewSessions(failStartTranscoder{}, t.TempDir(), nil)
	sessions.SetGovernor(g)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-b", MediaID: "media-b"}

	if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err == nil {
		t.Fatal("expected a launch failure")
	}
	if got := g.ActiveTranscodes(); got != 0 {
		t.Fatalf("ActiveTranscodes after a launch failure = %d, want 0 (lease not rolled back)", got)
	}
	if !g.GenerationAllowed() {
		t.Fatal("generation blocked after a failed transcode launch — lease leaked")
	}
}

// TestTranscodeLeaseHeldAcrossRenditionReplacement proves an in-place process
// replacement (rung switch / far seek) carries the single transcode lease across
// the swap rather than acquiring a second one: the session is one transcode unit
// throughout, and count returns to zero on teardown.
func TestTranscodeLeaseHeldAcrossRenditionReplacement(t *testing.T) {
	g := governor.New()
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	sessions.SetGovernor(g)
	key := SessionKey{SessionID: "session-c", MediaID: "media-c"}

	if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if got := g.ActiveTranscodes(); got != 1 {
		t.Fatalf("ActiveTranscodes after first start = %d, want 1", got)
	}
	// A different rung replaces the running process in place.
	if _, err := filePath720(sessions, key, "480p/seg00000.m4s"); err != nil {
		t.Fatalf("rendition replacement: %v", err)
	}
	if got := g.ActiveTranscodes(); got != 1 {
		t.Fatalf("ActiveTranscodes after replacement = %d, want 1 (a replacement must not double-count)", got)
	}

	sessions.Close()
	if got := g.ActiveTranscodes(); got != 0 {
		t.Fatalf("ActiveTranscodes after Close = %d, want 0", got)
	}
}

// startCount reports how many transcode launches the fake has been asked for.
// Concurrent tests read it while other goroutines are still starting sessions.
func (f *fakeTranscoder) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts)
}

// registeredSession returns the live *hlsSession for key. Tests use it to keep a
// pointer that eviction will make stale, which is exactly what an in-flight
// request holds across ensureRung's unlock windows.
func registeredSession(t *testing.T, sessions *Sessions, key SessionKey) *hlsSession {
	t.Helper()
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	session, ok := sessions.sessions[key]
	if !ok {
		t.Fatal("no session registered for key")
	}
	return session
}

// TestEnsureRungRefusesEvictedSession proves a request holding a *hlsSession
// pointer across an unlock window cannot resurrect a session the server has
// already torn down. TerminateSession is ADR-0012 §7's device-revocation path,
// so a session that reaches ensureRung after eviction must fail, never spawn a
// new FFmpeg: such a process is absent from s.sessions, so Close/reap/
// PurgeOrphanedTranscodeDirs would never stop it (the revoked device would keep
// streaming past shutdown) and it would run outside the governor's transcode
// lease, undercounting the concurrency cap for the life of the server.
func TestEnsureRungRefusesEvictedSession(t *testing.T) {
	t.Parallel()

	// Eviction before the session has ever spawned: the stale pointer must not
	// take the first-start path (admit + lease + launch).
	t.Run("before first spawn", func(t *testing.T) {
		t.Parallel()
		g := governor.New()
		transcoder := &fakeTranscoder{}
		sessions := NewSessions(transcoder, t.TempDir(), nil)
		sessions.SetGovernor(g)
		defer sessions.Close()
		key := SessionKey{SessionID: "session-evicted-cold", MediaID: "media-alpha"}

		// master.m3u8 registers the session without starting a transcode.
		if _, err := filePath720(sessions, key, "master.m3u8"); err != nil {
			t.Fatalf("master.m3u8: %v", err)
		}
		stale := registeredSession(t, sessions, key)
		rung, ok := stale.rung("720p")
		if !ok {
			t.Fatal("720p rung missing from session ladder")
		}
		rungDir := filepath.Join(stale.dir, rung.ID)
		if got := sessions.TerminateSession(key.SessionID); got != 1 {
			t.Fatalf("TerminateSession = %d, want 1", got)
		}
		// Put the rung dir back that eviction removed. The defect is that a spawn
		// is attempted at all; restoring the dir lets that spawn succeed instead of
		// dying on a missing path, so the lease it leaks is observable too.
		if err := os.MkdirAll(rungDir, 0o700); err != nil {
			t.Fatalf("recreate rung dir: %v", err)
		}

		err := sessions.ensureRung(context.Background(), key, stale, rung, 0, filepath.Join(rungDir, "seg00000.m4s"))
		if !errors.Is(err, ErrSessionEvicted) {
			t.Fatalf("ensureRung err = %v, want ErrSessionEvicted", err)
		}
		if got := transcoder.startCount(); got != 0 {
			t.Fatalf("transcode starts = %d, want 0 (spawned FFmpeg for an evicted session)", got)
		}
		if got := g.ActiveTranscodes(); got != 0 {
			t.Fatalf("ActiveTranscodes = %d, want 0 (lease leaked to an evicted session)", got)
		}
	})

	// Eviction of a session that holds a live process: the stale pointer must not
	// take the replacement path either. This is the window the frontier scan
	// opens — removeLocked releases the lease, so a replacement here would run
	// unleased forever.
	t.Run("while holding a live process", func(t *testing.T) {
		t.Parallel()
		g := governor.New()
		transcoder := &fakeTranscoder{}
		sessions := NewSessions(transcoder, t.TempDir(), nil)
		sessions.SetGovernor(g)
		defer sessions.Close()
		key := SessionKey{SessionID: "session-evicted-live", MediaID: "media-alpha"}

		if _, err := filePath720(sessions, key, "720p/seg00000.m4s"); err != nil {
			t.Fatalf("first start: %v", err)
		}
		stale := registeredSession(t, sessions, key)
		rung, ok := stale.rung("720p")
		if !ok {
			t.Fatal("720p rung missing from session ladder")
		}
		rungDir := filepath.Join(stale.dir, rung.ID)
		if got := sessions.TerminateSession(key.SessionID); got != 1 {
			t.Fatalf("TerminateSession = %d, want 1", got)
		}
		if err := os.MkdirAll(rungDir, 0o700); err != nil {
			t.Fatalf("recreate rung dir: %v", err)
		}
		started := transcoder.startCount()

		// Segment 12 is past the frontier plus lookahead, so this is the far-seek
		// request that drops the lock to rescan the frontier.
		err := sessions.ensureRung(context.Background(), key, stale, rung, 12, filepath.Join(rungDir, "seg00012.m4s"))
		if !errors.Is(err, ErrSessionEvicted) {
			t.Fatalf("ensureRung err = %v, want ErrSessionEvicted", err)
		}
		if got := transcoder.startCount(); got != started {
			t.Fatalf("transcode starts = %d, want %d (replaced the process of an evicted session)", got, started)
		}
		if got := g.ActiveTranscodes(); got != 0 {
			t.Fatalf("ActiveTranscodes = %d, want 0 after eviction", got)
		}
	})

	// The same eviction driven concurrently through the real request path, so the
	// off-lock frontier scan actually races TerminateSession. The standing
	// invariant is that the governor's lease count always equals the number of
	// sessions holding a live process: a transcode spawned for an evicted session
	// is counted by neither, and one spawned unleased breaks the equality.
	t.Run("concurrent with the off-lock frontier scan", func(t *testing.T) {
		t.Parallel()
		g := governor.New()
		sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
		sessions.SetGovernor(g)
		key := SessionKey{SessionID: "session-evicted-race", MediaID: "media-alpha"}

		const iterations = 300
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// seg0 starts the transcode; the far seek to seg12 forces the frontier
				// cache miss that drops s.mu. Both may legitimately fail once the
				// session is evicted mid-flight.
				_, _ = filePath720(sessions, key, "720p/seg00000.m4s")
				_, _ = filePath720(sessions, key, "720p/seg00012.m4s")
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				sessions.TerminateSession(key.SessionID)
			}
		}()
		wg.Wait()

		sessions.mu.Lock()
		live := sessions.liveCount()
		sessions.mu.Unlock()
		if got := g.ActiveTranscodes(); got != live {
			t.Fatalf("ActiveTranscodes = %d, want %d (one lease per live process)", got, live)
		}
		sessions.Close()
		if got := g.ActiveTranscodes(); got != 0 {
			t.Fatalf("ActiveTranscodes after Close = %d, want 0", got)
		}
	})
}

// TestTranscodeLeaseReleasedOnIdleReap proves the idle-reap teardown path
// releases the transcode lease, so a reaped session stops preempting generation.
func TestTranscodeLeaseReleasedOnIdleReap(t *testing.T) {
	clock := time.Unix(1000, 0).UTC()
	var mu sync.Mutex
	now := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	}
	g := governor.New()
	sessions := NewSessionsInDir(&fakeTranscoder{}, t.TempDir(), now)
	sessions.SetGovernor(g)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-d", MediaID: "media-d"}

	if _, err := sessions.FilePath(context.Background(), key, "/src/movie.mkv", 120, 1280, 720, false, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("start transcode: %v", err)
	}
	if got := g.ActiveTranscodes(); got != 1 {
		t.Fatalf("ActiveTranscodes = %d, want 1", got)
	}

	// Advance past the idle timeout and reap.
	mu.Lock()
	clock = clock.Add(sessionIdleTimeout + time.Minute)
	mu.Unlock()
	sessions.reap()

	if got := g.ActiveTranscodes(); got != 0 {
		t.Fatalf("ActiveTranscodes after idle reap = %d, want 0 (lease not released on reap)", got)
	}
	if !g.GenerationAllowed() {
		t.Fatal("generation still blocked after the live session was reaped")
	}
}

// TestNewSessionsInDirMintsDistinctDirsAcrossRestarts proves the boot nonce in
// the session directory name keeps two
// Sessions instances rooted at the same directory from ever minting the same
// session directory, even though each instance's nextID counter independently
// starts at zero and mints "session 1" first. Without it, a restarted server's
// first new session would reuse the previous run's directory name and could
// serve a client that run's leftover segments of different media.
func TestNewSessionsInDirMintsDistinctDirsAcrossRestarts(t *testing.T) {
	t.Parallel()
	scratchDir := filepath.Join(t.TempDir(), "scratch")
	key := SessionKey{SessionID: "session-a", MediaID: "media-a"}

	first := NewSessionsInDir(&fakeTranscoder{}, scratchDir, nil)
	firstPlaylist, err := filePath720(first, key, "master.m3u8")
	if err != nil {
		t.Fatalf("first instance master.m3u8: %v", err)
	}

	// A second instance rooted at the same directory simulates a restart:
	// nextID resets to zero in memory, but the two instances must never mint
	// the same directory name.
	second := NewSessionsInDir(&fakeTranscoder{}, scratchDir, nil)
	defer second.Close()
	secondPlaylist, err := filePath720(second, key, "master.m3u8")
	if err != nil {
		t.Fatalf("second instance master.m3u8: %v", err)
	}

	firstDir, secondDir := filepath.Dir(firstPlaylist), filepath.Dir(secondPlaylist)
	if firstDir == secondDir {
		t.Fatalf("both instances minted session dir %q", secondDir)
	}
}

// TestNewSessionsInDirPurgesLeftoverSessionDir proves construction removes a
// session directory a previous instance left on disk:
// a crash or plain process restart
// drops the in-memory session map before the boot reconciler's
// PurgeOrphanedTranscodeDirs sweep would otherwise run — or forever, if the
// reconciler never runs (no Encrypted-Library media configured) — so without
// this the leftover segments and playlist would stay servable to a new
// session.
func TestNewSessionsInDirPurgesLeftoverSessionDir(t *testing.T) {
	t.Parallel()
	scratchDir := filepath.Join(t.TempDir(), "scratch")
	key := SessionKey{SessionID: "session-a", MediaID: "media-a"}

	previous := NewSessionsInDir(&fakeTranscoder{}, scratchDir, nil)
	leftoverPlaylist, err := filePath720(previous, key, "master.m3u8")
	if err != nil {
		t.Fatalf("previous instance master.m3u8: %v", err)
	}
	leftoverDir := filepath.Dir(leftoverPlaylist)
	if _, err := os.Stat(leftoverDir); err != nil {
		t.Fatalf("leftover dir missing before restart: %v", err)
	}
	// A pre-nonce build named its dirs session-<N>; an upgrade must purge those too.
	legacyDir := filepath.Join(scratchDir, "session-3")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("seed legacy session dir: %v", err)
	}

	restarted := NewSessionsInDir(&fakeTranscoder{}, scratchDir, nil)
	defer restarted.Close()

	for _, dir := range []string{leftoverDir, legacyDir} {
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("leftover session dir %q still present after restart construction: %v", dir, err)
		}
	}
}

// TestNewSessionsInDirPurgeNeverRemovesLiveSessions proves
// PurgeOrphanedTranscodeDirs — including the sweep NewSessionsInDir now runs
// at construction — only ever removes directories no live session owns: a
// session started within the current instance must survive even though its
// directory shares the session-<nonce>-<N> shape the purge is confined to.
func TestNewSessionsInDirPurgeNeverRemovesLiveSessions(t *testing.T) {
	t.Parallel()
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()

	playlist, err := filePath720(sessions, SessionKey{SessionID: "session-a", MediaID: "media-a"}, "master.m3u8")
	if err != nil {
		t.Fatalf("master.m3u8: %v", err)
	}
	liveDir := filepath.Dir(playlist)

	if err := sessions.PurgeOrphanedTranscodeDirs(); err != nil {
		t.Fatalf("PurgeOrphanedTranscodeDirs: %v", err)
	}

	if _, err := os.Stat(liveDir); err != nil {
		t.Fatalf("live session dir removed by purge: %v", err)
	}
}

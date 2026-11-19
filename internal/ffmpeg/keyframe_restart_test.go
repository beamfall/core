package ffmpeg

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestKeyframeSegmentArgsStartCopyAtTheSeek pins -copypriorss 0 on the
// copied-video keyframe path (CORE-2618): without it the GOP before a restart
// keyframe fills the restart's first segment.
func TestKeyframeSegmentArgsStartCopyAtTheSeek(t *testing.T) {
	args := keyframeSegmentArgs("/src/film.mkv", "/tmp/hls", 12, 24024*time.Millisecond, Rendition{CopyVideo: true, AudioCodec: "eac3", AudioChannels: 6})
	if !containsArgPair(args, "-copypriorss", "0") {
		t.Fatalf("keyframe segment argv = %v, want -copypriorss 0", args)
	}
	input := -1
	for i, arg := range args {
		if arg == "-i" {
			input = i
		}
		if arg == "-copypriorss" && (input < 0 || i < input) {
			t.Fatalf("-copypriorss is an output option, found before -i in %v", args)
		}
	}
}

// TestKeyframeRestartFirstSegmentCarriesItsGOP runs the real command line for
// an audio-copy restart (copied H.264 with B-frames, audio transcoded to 5.1
// E-AC-3) at a mid-file keyframe and checks the restart's first segment holds
// media starting at that keyframe and its init.mp4 is readable. Before
// CORE-2618 FFmpeg wrote an init without its dec3 box and a first segment
// with no media, and the server read the empty segment as the source's end.
// The MP4 case puts the keyframe between two microseconds (24000/1001 fps in a
// 1/10000000 timescale), so its rounded-up index time lies just after it.
func TestKeyframeRestartFirstSegmentCarriesItsGOP(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	for _, tc := range []struct {
		name      string
		file      string
		rate      string
		gop       int
		extra     []string
		keyframe  time.Duration // the restart keyframe's exact time
		indexTime time.Duration // that time as the keyframe index holds it
	}{
		// keyframes every 2 s: 0, 2, 4, 6, 8 s.
		{name: "mkv whole-millisecond keyframes", file: "src.mkv", rate: "24", gop: 48, keyframe: 4 * time.Second, indexTime: 4 * time.Second},
		// keyframe 2 is frame 100: 100*1001/24000 s = 4.1708333... s.
		{name: "mp4 fractional keyframe", file: "src.mp4", rate: "24000/1001", gop: 50, extra: []string{"-video_track_timescale", "10000000"},
			keyframe: 4170833333 * time.Nanosecond, indexTime: 4170834 * time.Microsecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireRestartFirstSegment(t, tc.file, tc.rate, tc.gop, tc.extra, tc.keyframe, tc.indexTime)
		})
	}
}

func requireRestartFirstSegment(t *testing.T, file, rate string, gop int, extra []string, keyframe, indexTime time.Duration) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, file)
	// 10 s with a fixed GOP and B-frames, as Blu-ray and web releases carry.
	encode := []string{"-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=" + rate,
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "10", "-c:v", "libx264", "-preset", "ultrafast", "-bf", "2",
		"-x264-params", "keyint=" + strconv.Itoa(gop) + ":min-keyint=" + strconv.Itoa(gop) + ":scenecut=0",
		"-c:a", "aac", "-ac", "2"}
	encode = append(append(encode, extra...), "-y", src)
	if out, err := exec.Command("ffmpeg", encode...).CombinedOutput(); err != nil { // #nosec G204 -- test-owned encoder and temp paths
		t.Skipf("libx264 fixture unavailable: %v: %s", err, out)
	}
	out := filepath.Join(dir, "audio-copy")
	if err := os.Mkdir(out, 0o750); err != nil {
		t.Fatal(err)
	}
	rendition := Rendition{CopyVideo: true, AudioCodec: "eac3", AudioChannels: 6}
	cmd := exec.Command("ffmpeg", keyframeSegmentArgs(src, out, 2, indexTime, rendition)...) // #nosec G204 -- argv under test, temp paths
	if output, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(output), "eac3") && strings.Contains(string(output), "Unknown encoder") {
			t.Skipf("eac3 encoder unavailable: %s", output)
		}
		t.Fatalf("ffmpeg: %v: %s", err, output)
	}

	init, err := os.ReadFile(filepath.Join(out, "init.mp4")) // #nosec G304 -- output under this test's t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(out, "seg00002.m4s")) // #nosec G304 -- output under this test's t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	joined := filepath.Join(dir, "first.mp4")
	if err := os.WriteFile(joined, append(init, first...), 0o600); err != nil { // #nosec G703 -- path under this test's t.TempDir()
		t.Fatal(err)
	}
	probe := exec.Command("ffprobe", "-v", "error", "-show_entries", "packet=stream_index,pts_time", "-of", "csv=p=0", joined) // #nosec G204 -- test-owned temp path
	var stderr bytes.Buffer
	probe.Stderr = &stderr
	packets, err := probe.Output()
	if err != nil || stderr.Len() > 0 {
		t.Fatalf("init.mp4 + first restart segment unreadable (init %d bytes, segment %d bytes): %v: %s", len(init), len(first), err, stderr.String())
	}
	firstPTS := map[string]float64{}
	for line := range strings.Lines(string(packets)) {
		fields := strings.Split(strings.TrimSpace(line), ",")
		if len(fields) < 2 {
			continue
		}
		if _, seen := firstPTS[fields[0]]; seen {
			continue
		}
		pts, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			t.Fatalf("packet line %q: %v", line, err)
		}
		firstPTS[fields[0]] = pts
	}
	video, ok := firstPTS["0"]
	if !ok {
		t.Fatalf("first restart segment carries no video packets: %s", packets)
	}
	if _, ok := firstPTS["1"]; !ok {
		t.Fatalf("first restart segment carries no audio packets: %s", packets)
	}
	if diff := video - keyframe.Seconds(); diff < -0.001 || diff > 0.001 {
		t.Fatalf("first restart segment starts at %.6fs, want the restart keyframe at %.6fs", video, keyframe.Seconds())
	}
}

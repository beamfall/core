package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestAudioOnlyRenditionNeverMapsVideo drives every file-to-HLS command line
// with an audio-only rendition whose audio stream is selected: the linear
// encode, the fixed-length segments and the keyframe segments. None may map
// the first video stream or carry a video encode, since an audio-class source
// has no video stream, or only embedded cover art (CORE-1565).
func TestAudioOnlyRenditionNeverMapsVideo(t *testing.T) {
	selected := 2
	rendition := Rendition{AudioOnly: true, AudioCodec: "aac", AudioChannels: 2, AudioStreamIndex: &selected}
	for name, args := range map[string][]string{
		"linear":            hlsArgs("/lib/song.m4a", "/d", rendition),
		"segments":          hlsSegmentArgs("/lib/song.m4a", "/d", 2, 4, rendition),
		"keyframe segments": keyframeSegmentArgs("/lib/song.m4a", "/d", 2, 8*time.Second, rendition),
	} {
		t.Run(name, func(t *testing.T) {
			if !containsArgPair(args, "-map", "0:2") {
				t.Fatalf("argv = %v, want -map 0:2 for the selected audio stream", args)
			}
			for _, video := range []string{"0:v:0", "-c:v", "-vf", "-b:v", "-force_key_frames"} {
				if containsArg(args, video) {
					t.Fatalf("audio-only argv carries video argument %q: %v", video, args)
				}
			}
		})
	}
}

// TestAudioOnlyRenditionRealEncodeWithSelectedTrack encodes audio-class
// fixtures with two audio tracks, with and without embedded cover art, through
// the linear and segmented HLS paths with the second track selected. The
// output must be a single AAC audio stream with no video, and it must last as
// long as the selected (shorter) track, which proves the selection was mapped
// (CORE-1565).
func TestAudioOnlyRenditionRealEncodeWithSelectedTrack(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	for _, fixture := range []struct {
		name  string
		cover bool
	}{
		{"no picture", false},
		{"cover art", true},
	} {
		src := filepath.Join(root, strings.ReplaceAll(fixture.name, " ", "-")+".m4a")
		synthesizeTwoTrackAudio(t, src, fixture.cover)
		for _, path := range []string{"linear", "segments"} {
			t.Run(fixture.name+"/"+path, func(t *testing.T) {
				dir := filepath.Join(root, strings.ReplaceAll(fixture.name, " ", "-")+"-"+path)
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				selected := 1
				rendition := Rendition{AudioOnly: true, AudioCodec: "aac", AudioChannels: 2, AudioStreamIndex: &selected}
				var (
					proc *HLSProcess
					err  error
				)
				if path == "linear" {
					proc, err = New().StartHLS(context.Background(), src, dir, rendition)
				} else {
					proc, err = New().StartHLSSegments(context.Background(), src, dir, 0, 2, rendition)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := proc.Wait(); err != nil {
					t.Fatalf("encode: %v", err)
				}
				playlist := "live.m3u8"
				if path == "linear" {
					playlist = "index.m3u8"
				}
				probe := probeHLS(t, filepath.Join(dir, playlist))
				if strings.Contains(probe, "codec_type=video") || strings.Count(probe, "\nstream|") != 1 || !strings.Contains(probe, "codec_name=aac|codec_type=audio") {
					t.Fatalf("want one AAC audio stream and no video:\n%s", probe)
				}
				_, durationText, _ := strings.Cut(probe, "duration=")
				duration, err := strconv.ParseFloat(strings.TrimSpace(strings.Split(durationText, "\n")[0]), 64)
				if err != nil {
					t.Fatalf("duration in %q: %v", probe, err)
				}
				if duration < 3.5 || duration > 4.5 {
					t.Fatalf("output lasts %.2fs, want about 4s (the selected track, not the 8s default)", duration)
				}
			})
		}
	}
}

// synthesizeTwoTrackAudio writes an M4A with an 8-second first audio track
// (stream 0) and a 4-second second one (stream 1), optionally with embedded
// cover art, which the MP4 muxer writes as stream 2. The art is added in a
// second, stream-copy pass: limiting the picture to one frame in the encoding
// pass would end the whole file after that frame.
func synthesizeTwoTrackAudio(t *testing.T, path string, cover bool) {
	t.Helper()
	run := func(what string, args ...string) {
		t.Helper()
		if out, err := exec.Command("ffmpeg", append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil { // #nosec G204 -- test fixture, temp paths and fixed args
			t.Fatalf("synthesize %s: %v\n%s", what, err, out)
		}
	}
	audio := path
	if cover {
		audio = path + ".audio.m4a"
	}
	run("two-track audio",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=8",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=4",
		"-map", "0:a", "-map", "1:a", "-c:a", "aac", "-y", audio,
	)
	if cover {
		art := path + ".cover.jpg"
		run("cover art", "-f", "lavfi", "-i", "color=c=red:s=64x64", "-frames:v", "1", "-c:v", "mjpeg", "-y", art)
		run("covered audio", "-i", audio, "-i", art, "-map", "0:a", "-map", "1:v", "-c", "copy", "-disposition:v:0", "attached_pic", "-y", path)
	}
	if cover {
		out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=index,codec_type", "-of", "compact", path).CombinedOutput() // #nosec G204 -- test fixture, temp paths and fixed args
		if err != nil || !strings.Contains(string(out), "index=1|codec_type=audio") || !strings.Contains(string(out), "index=2|codec_type=video") {
			t.Fatalf("fixture streams: %v\n%s", err, out)
		}
	}
}

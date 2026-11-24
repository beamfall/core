package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestAudioAdjustFilters(t *testing.T) {
	for _, tc := range []struct {
		adjust AudioAdjust
		want   string
	}{
		{AudioAdjust{}, ""},
		{AudioAdjust{Tempo: 1}, ""},
		// Tempo alone: the pitch is untouched.
		{AudioAdjust{Tempo: 0.9}, "atempo=0.9"},
		{AudioAdjust{Tempo: 1.25}, "atempo=1.25"},
		// Pitch alone: the rate change moves pitch and speed, atempo restores
		// the recorded speed.
		{AudioAdjust{PitchSemitones: 2}, "aresample=48000,asetrate=53878,aresample=48000,atempo=0.890902"},
		{AudioAdjust{PitchSemitones: -3}, "aresample=48000,asetrate=40363,aresample=48000,atempo=1.189208"},
		// Both, including the extremes of the accepted ranges, stay inside the
		// 0.5..2 one atempo instance takes.
		{AudioAdjust{PitchSemitones: 2, Tempo: 0.8}, "aresample=48000,asetrate=53878,aresample=48000,atempo=0.712721"},
		{AudioAdjust{PitchSemitones: MaxPitchShiftSemitones, Tempo: MinTempo}, "aresample=48000,asetrate=67882,aresample=48000,atempo=0.530332"},
		{AudioAdjust{PitchSemitones: MinPitchShiftSemitones, Tempo: MaxTempo}, "aresample=48000,asetrate=33941,aresample=48000,atempo=1.767773"},
	} {
		if got := strings.Join(tc.adjust.Filters(), ","); got != tc.want {
			t.Errorf("%+v filters = %q, want %q", tc.adjust, got, tc.want)
		}
	}
}

func TestAudioAdjustTimeline(t *testing.T) {
	adjust := AudioAdjust{Tempo: 0.8}
	if got := adjust.SourceSeconds(40); got != 32 {
		t.Fatalf("output 40s at tempo 0.8 plays source %vs, want 32", got)
	}
	if got := adjust.OutputSeconds(200); got != 250 {
		t.Fatalf("200s of source at tempo 0.8 lasts %ds, want 250", got)
	}
	if got := (AudioAdjust{Tempo: 1.25}).OutputSeconds(201); got != 161 {
		t.Fatalf("201s at tempo 1.25 lasts %ds, want 161 (rounded up)", got)
	}
	if got := adjust.OutputPosition(41); got != 51 {
		t.Fatalf("source 41s at tempo 0.8 plays at output %ds, want 51 (rounded down)", got)
	}
	if got := (AudioAdjust{PitchSemitones: 3}).OutputSeconds(200); got != 200 {
		t.Fatalf("a pitch shift alone keeps the duration: %d", got)
	}
}

func TestAudioArgsAdjustLeadsFilterChain(t *testing.T) {
	gain := -3.5
	args := Rendition{AudioCodec: "aac", AudioChannels: 2, AudioGainDB: &gain}.WithAudioAdjust(AudioAdjust{Tempo: 1.1}).audioArgs()
	if got, want := argAfter(args, "-af"), "atempo=1.1,volume=-3.5dB"; got != want {
		t.Fatalf("-af = %q, want %q", got, want)
	}
	// An adjusted stream cannot be stream-copied.
	args = Rendition{AudioCodec: "copy"}.WithAudioAdjust(AudioAdjust{PitchSemitones: 1}).audioArgs()
	if argAfter(args, "-c:a") != "aac" || !strings.HasPrefix(argAfter(args, "-af"), "aresample=48000,asetrate=") {
		t.Fatalf("adjusted copy must re-encode with the adjustment: %q", args)
	}
	if args := (Rendition{AudioCodec: "copy"}).audioArgs(); !slices.Equal(args, []string{"-c:a", "copy"}) {
		t.Fatalf("unadjusted copy changed: %q", args)
	}
}

func TestHLSSegmentArgsAudioOnlyAdjusted(t *testing.T) {
	rendition := Rendition{AudioOnly: true, AudioCodec: "aac", AudioChannels: 2}.WithAudioAdjust(AudioAdjust{PitchSemitones: -2, Tempo: 0.8})
	args := hlsSegmentArgs("/lib/song.flac", "/scratch/s/audio", 10, 4, rendition)
	// Output segment 10 of 4s starts at output 40s, which plays source 32s.
	if !containsPair(args, "-ss", "32") || !containsPair(args, "-output_ts_offset", "40") || !containsPair(args, "-start_number", "10") {
		t.Fatalf("segment 10 must seek the source to 32s and stamp output 40s: %q", args)
	}
	if !containsPair(args, "-map", "0:a:0") || slices.Contains(args, "-c:v") || slices.Contains(args, "-force_key_frames") || slices.Contains(args, "-vf") {
		t.Fatalf("audio-only encode must map only the audio: %q", args)
	}
	if !strings.HasPrefix(argAfter(args, "-af"), "aresample=48000,asetrate=42763,aresample=48000,atempo=") {
		t.Fatalf("-af = %q", argAfter(args, "-af"))
	}
	index := 3
	rendition.AudioStreamIndex = &index
	if args := hlsSegmentArgs("/lib/song.flac", "/d", 0, 4, rendition); !containsPair(args, "-map", "0:3") || !containsPair(args, "-ss", "0") {
		t.Fatalf("selected audio stream must be mapped: %q", args)
	}
	// A fractional source position keeps millisecond precision.
	if args := hlsSegmentArgs("/lib/song.flac", "/d", 1, 4, Rendition{AudioOnly: true}.WithAudioAdjust(AudioAdjust{Tempo: 0.9})); !containsPair(args, "-ss", "3.6") {
		t.Fatalf("segment 1 at tempo 0.9 must seek to 3.6s: %q", args)
	}
}

func TestHLSSegmentArgsUnadjustedUnchanged(t *testing.T) {
	args := hlsSegmentArgs("/lib/film.mkv", "/d", 3, 4, Rendition{Height: 720, VideoKbps: 3000})
	if !containsPair(args, "-ss", "12") || !containsPair(args, "-force_key_frames", "expr:gte(t,n_forced*4)") || !containsPair(args, "-c:v", "libx264") || slices.Contains(args, "-map") {
		t.Fatalf("unadjusted video encode changed: %q", args)
	}
}

func TestStartHLSSegmentsRefusesAdjustedSourceVideo(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "film.mkv")
	if err := os.WriteFile(src, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rendition := Rendition{Height: 720}.WithAudioAdjust(AudioAdjust{Tempo: 0.9})
	if _, err := New().StartHLSSegments(context.Background(), src, dir, 0, 4, rendition); err == nil || !strings.Contains(err.Error(), "audio-only or a composed picture") {
		t.Fatalf("an adjusted encode of the source video must be refused, got %v", err)
	}
}

func TestComposedSegmentArgsTempoGraphics(t *testing.T) {
	picture := ComposedPicture{GraphicsPath: "/lib/song.cdg"}
	rendition := Rendition{VideoKbps: 1500}.WithAudioAdjust(AudioAdjust{Tempo: 0.8})
	args := composedSegmentArgs("/lib/song.mp3", "/scratch/s/composed", picture, 10, 4, rendition)
	// The presenter draws source time itself (startComposedProcess), so the
	// rawvideo picture needs no retiming filters.
	if got, want := argAfter(args, "-vf"), "format=yuv420p"; got != want {
		t.Fatalf("-vf = %q\nwant %q", got, want)
	}
	if !containsPair(args, "-ss", "32") || !containsPair(args, "-output_ts_offset", "40") || argAfter(args, "-af") != "atempo=0.8" {
		t.Fatalf("audio must seek source 32s, play at tempo 0.8 and land at output 40s: %q", args)
	}
	args = composedSegmentArgs("/lib/song.mp3", "/d", picture, 0, 4, rendition)
	if got := argAfter(args, "-vf"); got != "format=yuv420p" || slices.Contains(args, "-ss") {
		t.Fatalf("from the start: -vf = %q in %q", got, args)
	}
}

func TestComposedSegmentArgsTempoTimedText(t *testing.T) {
	picture := ComposedPicture{TimedTextPath: "/scratch/s/composed/picture.ass"}
	rendition := Rendition{}.WithAudioAdjust(AudioAdjust{Tempo: 1.25, PitchSemitones: 1})
	args := composedSegmentArgs("/lib/song.ogg", "/scratch/s/composed", picture, 2, 4, rendition)
	// Output 8s plays source 10s: libass renders the text due at source time
	// and the frames are then stamped back onto the output clock.
	if got, want := argAfter(args, "-vf"), "setpts=PTS*1.25+10/TB,ass=filename=/scratch/s/composed/picture.ass,setpts=(PTS-STARTPTS)/1.25,format=yuv420p"; got != want {
		t.Fatalf("-vf = %q\nwant %q", got, want)
	}
	if !containsPair(args, "-ss", "10") || !containsPair(args, "-output_ts_offset", "8") {
		t.Fatalf("audio seek must be source 10s, output offset 8s: %q", args)
	}
}

func requireAdjustCapableFFmpeg(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	filters, _ := exec.Command("ffmpeg", "-hide_banner", "-filters").Output()
	if !strings.Contains(string(filters), " asetrate ") || !strings.Contains(string(filters), " atempo ") {
		t.Skip("ffmpeg lacks the asetrate or atempo filter")
	}
}

func TestStartHLSSegmentsAudioOnlyAdjustedRealEncode(t *testing.T) {
	requireAdjustCapableFFmpeg(t)
	root := t.TempDir()
	src := filepath.Join(root, "song.mp3")
	synthesizeAudio(t, src) // 6s
	for name, adjust := range map[string]AudioAdjust{
		"tempo":       {Tempo: 0.75},
		"pitch":       {PitchSemitones: -6},
		"pitch+tempo": {PitchSemitones: 4, Tempo: 1.25},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(root, strings.ReplaceAll(name, "+", "-"))
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			rendition := Rendition{AudioOnly: true, AudioCodec: "aac", AudioChannels: 2}.WithAudioAdjust(adjust)
			proc, err := New().StartHLSSegments(context.Background(), src, dir, 0, 2, rendition)
			if err != nil {
				t.Fatal(err)
			}
			if err := proc.Wait(); err != nil {
				t.Fatalf("encode: %v", err)
			}
			probe := probeHLS(t, filepath.Join(dir, "live.m3u8"))
			if strings.Contains(probe, "codec_type=video") || !strings.Contains(probe, "codec_name=aac|codec_type=audio") {
				t.Fatalf("want one AAC audio stream and no video:\n%s", probe)
			}
			_, durationText, _ := strings.Cut(probe, "duration=")
			duration, err := strconv.ParseFloat(strings.TrimSpace(strings.Split(durationText, "\n")[0]), 64)
			if err != nil {
				t.Fatalf("duration in %q: %v", probe, err)
			}
			if want := float64(adjust.OutputSeconds(6)); duration < want-0.5 || duration > want+0.5 {
				t.Fatalf("output lasts %.2fs, want about %.0fs (6s at tempo %v)", duration, want, adjust.tempo())
			}
		})
	}
}

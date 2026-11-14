package ffmpeg

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Synthetic listings in the shape `ffmpeg -filters` and `ffmpeg -encoders`
// print: a legend, a dashed separator, then a flags column and a name.
const (
	syntheticFilterListing = `Filters:
  T.. = Timeline support
  ... = Slice threading
 ------
 ... color             |->V       Provide a uniformly colored input.
 T.C scale             V->V       Scale the input video size.
 ... pad               V->V       Pad the input video.
 ... fps               V->V       Force constant framerate.
 ... tpad              V->V       Temporarily pad video frames.
 ... trim              V->V       Pick one continuous section from the input.
 ... setpts            V->V       Set PTS for the output video frame.
 ... format            V->V       Convert the input video to one of the specified pixel formats.
 ... ass               V->V       Render ASS subtitles.
 ... aresample         A->A       Resample audio data.
 ... asetrate          A->A       Change the sample rate without altering the data.
 ... atempo            A->A       Adjust audio tempo.
 ... volume            A->A       Change input volume.
`
	syntheticEncoderListing = `Encoders:
 V..... = Video
 ------
 V....D libx264              libx264 H.264 / AVC
 A....D aac                  AAC (Advanced Audio Coding)
`
)

func withoutLine(listing, name string) string {
	var kept []string
	for _, line := range strings.Split(listing, "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[1] == name {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func TestParseComponentList(t *testing.T) {
	filters := parseComponentList(syntheticFilterListing)
	for _, name := range []string{"color", "scale", "ass", "format"} {
		if !filters[name] {
			t.Errorf("filters missing %q", name)
		}
	}
	// Legend rows sit above the separator and are not components.
	if filters["="] || filters["Timeline"] || len(filters) != 13 {
		t.Errorf("filters = %v, want the thirteen listed names only", filters)
	}
	if parsed := parseComponentList("no separator here\n ... ass V->V x\n"); len(parsed) != 0 {
		t.Errorf("listing without a separator parsed %v, want nothing", parsed)
	}
}

func TestMissingForNamesWhatTheArgvNeeds(t *testing.T) {
	full := capabilities{Filters: parseComponentList(syntheticFilterListing), Encoders: parseComponentList(syntheticEncoderListing)}
	noLibass := capabilities{Filters: parseComponentList(withoutLine(syntheticFilterListing, "ass")), Encoders: full.Encoders}
	noAAC := capabilities{Filters: full.Filters, Encoders: parseComponentList(withoutLine(syntheticEncoderListing, "aac"))}
	noATempo := capabilities{Filters: parseComponentList(withoutLine(syntheticFilterListing, "atempo")), Encoders: full.Encoders}
	stereo := Rendition{Height: 720, AudioCodec: "aac", AudioChannels: 2}
	slowed := stereo.WithAudioAdjust(AudioAdjust{Tempo: 0.8})
	cases := []struct {
		name      string
		caps      capabilities
		timedText bool
		rendition Rendition
		want      []string
	}{
		{"full build, timed text", full, true, slowed, nil},
		{"full build, graphics", full, false, slowed, nil},
		{"no libass, timed text", noLibass, true, stereo, []string{"filter ass"}},
		{"no libass, graphics", noLibass, false, stereo, nil},
		{"no AAC encoder", noAAC, false, stereo, []string{"encoder aac"}},
		{"no atempo, unadjusted", noATempo, false, stereo, nil},
		{"no atempo, slowed", noATempo, true, slowed, []string{"filter atempo"}},
		{"nothing listed", capabilities{}, true, stereo, []string{"filter color", "filter ass", "filter format", "filter aresample", "encoder libx264", "encoder aac"}},
	}
	for _, tc := range cases {
		if got := tc.caps.missingFor(composedRecipeArgv(tc.timedText, tc.rendition)[0]); !slices.Equal(got, tc.want) {
			t.Errorf("%s: missing = %v, want %v", tc.name, got, tc.want)
		}
	}
	shifted := Rendition{AudioCodec: "aac", AudioChannels: 2}.WithAudioAdjust(AudioAdjust{PitchSemitones: 2})
	noASetRate := capabilities{Filters: parseComponentList(withoutLine(syntheticFilterListing, "asetrate")), Encoders: full.Encoders}
	if got := noASetRate.missingFor(shifted.audioArgs()); !slices.Equal(got, []string{"filter asetrate"}) {
		t.Errorf("pitch shift without asetrate: missing = %v", got)
	}
	if got := full.missingFor(shifted.audioArgs()); len(got) != 0 {
		t.Errorf("pitch shift on a full build: missing = %v", got)
	}
}

func TestProbeCapabilitiesUsesTheRunner(t *testing.T) {
	var flags []string
	caps, err := probeCapabilities(context.Background(), func(_ context.Context, flag string) ([]byte, error) {
		flags = append(flags, flag)
		if flag == "-filters" {
			return []byte(withoutLine(syntheticFilterListing, "ass")), nil
		}
		return []byte(syntheticEncoderListing), nil
	})
	if err != nil || !slices.Equal(flags, []string{"-filters", "-encoders"}) {
		t.Fatalf("probe err %v flags %v", err, flags)
	}
	stereo := Rendition{AudioCodec: "aac", AudioChannels: 2}
	if !slices.Equal(caps.missingFor(composedRecipeArgv(true, stereo)[0]), []string{"filter ass"}) || len(caps.missingFor(composedRecipeArgv(false, stereo)[0])) != 0 {
		t.Fatalf("caps = %+v, want a build without libass", caps)
	}
	failure := errors.New("listing failed")
	if _, err := probeCapabilities(context.Background(), func(context.Context, string) ([]byte, error) { return nil, failure }); !errors.Is(err, failure) {
		t.Fatalf("err = %v, want the runner failure", err)
	}
}

func TestComposedCanvas(t *testing.T) {
	cases := []struct{ height, wantWidth, wantHeight int }{
		{0, 1280, 720}, {720, 1280, 720}, {1080, 1280, 720}, {480, 854, 480}, {360, 640, 360}, {361, 640, 360},
	}
	for _, tc := range cases {
		if w, h := ComposedCanvas(tc.height); w != tc.wantWidth || h != tc.wantHeight {
			t.Errorf("ComposedCanvas(%d) = %dx%d, want %dx%d", tc.height, w, h, tc.wantWidth, tc.wantHeight)
		}
	}
}

func TestComposedEncodeArgsScaleToTheRenditionHeight(t *testing.T) {
	graphics := composedEncodeArgs("/lib/song.mp3", ComposedPicture{GraphicsPath: "/c/song.cdg"}, Rendition{Height: 360}, 0, 4)
	vf := argAfter(graphics, "-vf")
	if !strings.Contains(vf, "scale=500:360:flags=neighbor") || !strings.Contains(vf, "pad=640:360:") {
		t.Errorf("graphics -vf = %q, want the 360p canvas", vf)
	}
	text := composedEncodeArgs("/lib/song.mp3", ComposedPicture{TimedTextPath: "/c/song.ass"}, Rendition{Height: 360}, 0, 4)
	if !slices.Contains(text, "color=c=black:s=640x360:r=30") {
		t.Errorf("timed-text args = %v, want a 640x360 canvas", text)
	}
}

// TestComposedRecipeRequirementsCoverEveryBranch proves the check covers every
// filter and encoder the emitted argv contains: over both picture kinds, every
// start-offset branch, an unadjusted, slowed, pitch-shifted and gain-adjusted
// audio side, and an AAC and adjusted-copy audio codec, a build listing all of
// them passes, and a build missing any one name found in the argv (read here by
// a plain token scan, independent of argvRequirements) fails.
func TestComposedRecipeRequirementsCoverEveryBranch(t *testing.T) {
	gain := -3.0
	base := Rendition{Height: 360, VideoKbps: 600, AudioChannels: 2}
	renditions := map[string]Rendition{
		"aac":           withCodec(base, "aac"),
		"aac slowed":    withCodec(base, "aac").WithAudioAdjust(AudioAdjust{Tempo: 0.8}),
		"aac shifted":   withCodec(base, "aac").WithAudioAdjust(AudioAdjust{PitchSemitones: -2, Tempo: 1.1}),
		"copy shifted":  withCodec(base, "copy").WithAudioAdjust(AudioAdjust{PitchSemitones: 1}),
		"aac with gain": func() Rendition { r := withCodec(base, "aac"); r.AudioGainDB = &gain; return r }(),
		"surround eac3": func() Rendition { r := withCodec(base, "eac3"); r.AudioChannels = 6; return r }(),
	}
	wantSeen := map[string]bool{"atempo": false, "asetrate": false, "aresample": false, "volume": false, "ass": false, "tpad": false, "trim": false}
	for name, rendition := range renditions {
		for _, timedText := range []bool{false, true} {
			for _, args := range composedRecipeArgv(timedText, rendition) {
				filters, encoders := tokenScan(args)
				for _, f := range filters {
					if _, ok := wantSeen[f]; ok {
						wantSeen[f] = true
					}
				}
				all := capabilities{Filters: setOf(filters), Encoders: setOf(encoders)}
				if missing := all.missingFor(args); len(missing) != 0 {
					t.Errorf("%s timedText=%v: build listing every argv name still misses %v", name, timedText, missing)
				}
				for _, drop := range filters {
					c := capabilities{Filters: setOf(filters), Encoders: all.Encoders}
					delete(c.Filters, drop)
					if !slices.Contains(c.missingFor(args), "filter "+drop) {
						t.Errorf("%s timedText=%v: a build without filter %q passes", name, timedText, drop)
					}
				}
				for _, drop := range encoders {
					c := capabilities{Filters: all.Filters, Encoders: setOf(encoders)}
					delete(c.Encoders, drop)
					if !slices.Contains(c.missingFor(args), "encoder "+drop) {
						t.Errorf("%s timedText=%v: a build without encoder %q passes", name, timedText, drop)
					}
				}
			}
		}
	}
	for f, seen := range wantSeen {
		if !seen {
			t.Errorf("no case emitted filter %q, so the coverage above is not exercising it", f)
		}
	}
}

func withCodec(r Rendition, codec string) Rendition {
	r.AudioCodec = codec
	return r
}

func setOf(names []string) map[string]bool {
	set := map[string]bool{}
	for _, name := range names {
		set[name] = true
	}
	return set
}

// tokenScan reads filter and encoder names from an argv by plain token
// matching: each comma-separated chain element (an escaped comma is not a
// separator) starts with its filter name.
func tokenScan(args []string) (filters, encoders []string) {
	name := regexp.MustCompile(`^[a-z0-9_]+`)
	for i := 0; i+1 < len(args); i++ {
		switch {
		case args[i] == "-vf" || args[i] == "-af" || (args[i] == "-i" && i > 0 && args[i-1] == "lavfi"):
			for _, element := range strings.Split(strings.ReplaceAll(args[i+1], `\,`, ""), ",") {
				filters = append(filters, name.FindString(element))
			}
		case (args[i] == "-c:v" || args[i] == "-c:a") && args[i+1] != "copy":
			encoders = append(encoders, args[i+1])
		}
	}
	return filters, encoders
}

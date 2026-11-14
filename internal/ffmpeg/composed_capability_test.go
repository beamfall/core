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
	if filters["="] || filters["Timeline"] || len(filters) != 9 {
		t.Errorf("filters = %v, want the nine listed names only", filters)
	}
	if parsed := parseComponentList("no separator here\n ... ass V->V x\n"); len(parsed) != 0 {
		t.Errorf("listing without a separator parsed %v, want nothing", parsed)
	}
}

func TestMissingForComposed(t *testing.T) {
	full := capabilities{Filters: parseComponentList(syntheticFilterListing), Encoders: parseComponentList(syntheticEncoderListing)}
	noLibass := capabilities{Filters: parseComponentList(withoutLine(syntheticFilterListing, "ass")), Encoders: full.Encoders}
	noX264 := capabilities{Filters: full.Filters, Encoders: parseComponentList(withoutLine(syntheticEncoderListing, "libx264"))}
	cases := []struct {
		name      string
		caps      capabilities
		timedText bool
		want      []string
	}{
		{"full build, timed text", full, true, nil},
		{"full build, graphics", full, false, nil},
		{"no libass, timed text", noLibass, true, []string{"filter ass"}},
		{"no libass, graphics", noLibass, false, nil},
		{"no libx264, graphics", noX264, false, []string{"encoder libx264"}},
		{"nothing listed", capabilities{}, true, []string{"filter color", "filter setpts", "filter ass", "filter format", "encoder libx264"}},
	}
	for _, tc := range cases {
		if got := tc.caps.missingForComposed(tc.timedText); !slices.Equal(got, tc.want) {
			t.Errorf("%s: missing = %v, want %v", tc.name, got, tc.want)
		}
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
	if !slices.Equal(caps.missingForComposed(true), []string{"filter ass"}) || len(caps.missingForComposed(false)) != 0 {
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

// TestComposedRecipeRequirementsCoverEveryBranch holds the capability lists to
// the recipe: every filter composedEncodeArgs can emit for a picture kind, over
// every start-offset and tempo branch, and its video encoder, is one the
// capability check requires.
func TestComposedRecipeRequirementsCoverEveryBranch(t *testing.T) {
	filterName := regexp.MustCompile(`^[a-z0-9_]+`)
	kinds := map[bool]ComposedPicture{
		false: {GraphicsPath: "/c/song.cdg"},
		true:  {TimedTextPath: "/c/song.ass"},
	}
	for timedText, picture := range kinds {
		required := composedGraphicsFilters
		if timedText {
			required = composedTimedTextFilters
		}
		for _, tempo := range []float64{0, 0.8} {
			for _, start := range []int{0, 8} {
				rendition := Rendition{Height: 720}
				if tempo != 0 {
					rendition = rendition.WithAudioAdjust(AudioAdjust{Tempo: tempo})
				}
				args := composedEncodeArgs("/lib/song.mp3", picture, rendition, start, 4)
				var used []string
				for _, filter := range splitFilterChain(argAfter(args, "-vf")) {
					used = append(used, filterName.FindString(filter))
				}
				if i := slices.Index(args, "lavfi"); i >= 0 {
					used = append(used, filterName.FindString(args[i+2]))
				}
				for _, name := range used {
					if !slices.Contains(required, name) {
						t.Errorf("timedText=%v tempo=%v start=%d: filter %q is not in the required list %v", timedText, tempo, start, name, required)
					}
				}
				if encoder := argAfter(args, "-c:v"); !slices.Contains(composedEncoders, encoder) {
					t.Errorf("encoder %q is not in the required list %v", encoder, composedEncoders)
				}
			}
		}
	}
}

// splitFilterChain splits a -vf chain on the commas between filters, leaving
// backslash-escaped commas inside option values alone.
func splitFilterChain(chain string) []string {
	var out []string
	var current strings.Builder
	escaped := false
	for _, r := range chain {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == ',':
			out = append(out, current.String())
			current.Reset()
			continue
		}
		current.WriteRune(r)
	}
	return append(out, current.String())
}

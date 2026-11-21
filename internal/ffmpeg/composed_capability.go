package ffmpeg

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

// Composed-picture capability: a live composed stream is offered only when the
// resolved FFmpeg build has every filter and encoder the composition command
// names. A build without libass, for one, has no `ass` filter, so a timed-text
// picture would fail at filter-graph parse and turn a song that plays as audio
// into a failing stream; a build without the AAC encoder or the pitch and tempo
// filters fails the same way on the audio side.
//
// The requirements are read off the argv the encode would run
// (argvRequirements over composedEncodeArgs and Rendition.audioArgs), so they
// cannot drift from the recipe. Unlike the tone-map probe, listing is enough:
// the recipe's filters take fixed options Beamfall controls, so a filter that
// is present parses them. The listing is kept in memory per resolved binary
// identity, like the tone-map verdict, so a BYO-FFmpeg swap under a running
// server is noticed.

// capabilities is the set of filters and encoders an FFmpeg build lists.
type capabilities struct {
	Filters  map[string]bool
	Encoders map[string]bool
}

// missingFor names the filters and encoders args runs that the build lacks, in
// argv order. Empty means the build can run the command.
func (c capabilities) missingFor(args []string) []string {
	filters, encoders := argvRequirements(args)
	var missing []string
	for _, name := range filters {
		if !c.Filters[name] {
			missing = append(missing, "filter "+name)
		}
	}
	for _, name := range encoders {
		if !c.Encoders[name] {
			missing = append(missing, "encoder "+name)
		}
	}
	return missing
}

// argvRequirements reads the filters and encoders an FFmpeg argv names: every
// filter of each -vf and -af chain, the source filter of a lavfi input, and
// each -c:v and -c:a encoder other than copy. Each name appears once.
func argvRequirements(args []string) (filters, encoders []string) {
	add := func(list []string, name string) []string {
		if name == "" || slices.Contains(list, name) {
			return list
		}
		return append(list, name)
	}
	for i := 0; i+1 < len(args); i++ {
		value := args[i+1]
		switch args[i] {
		case "-vf", "-af":
			for _, filter := range splitFilterChain(value) {
				filters = add(filters, filterName(filter))
			}
		case "-f":
			if value == "lavfi" && i+3 < len(args) && args[i+2] == "-i" {
				filters = add(filters, filterName(args[i+3]))
			}
		case "-c:v", "-c:a":
			if value != "copy" {
				encoders = add(encoders, value)
			}
		}
	}
	return filters, encoders
}

// filterName is the name of one filter in a chain: everything before its
// options.
func filterName(filter string) string {
	name, _, _ := strings.Cut(filter, "=")
	return strings.TrimSpace(name)
}

// splitFilterChain splits a filter chain on the commas between filters,
// leaving backslash-escaped commas inside option values (escapeFilterValue)
// alone.
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

// composedRecipeArgv is every command a live composed session of this picture
// kind and rendition can run: the encode from the start and from a later
// segment (a seek), which take different filter branches. The picture paths are
// placeholders; only the names the argv carries matter.
func composedRecipeArgv(timedText bool, rendition Rendition) [][]string {
	picture := ComposedPicture{GraphicsPath: "/composed/picture.cdg"}
	if timedText {
		picture = ComposedPicture{TimedTextPath: "/composed/picture.ass"}
	}
	const segmentSeconds = 4
	return [][]string{
		composedEncodeArgs("/composed/audio", picture, rendition, 0, segmentSeconds),
		composedEncodeArgs("/composed/audio", picture, rendition, segmentSeconds, segmentSeconds),
	}
}

// parseComponentList reads the names out of `ffmpeg -filters` or `ffmpeg
// -encoders` output: after the "Filters:" or "Encoders:" header, every row is a
// flags column followed by the component name. Legend rows ("T.. = Timeline
// support") and the dashed separator `-encoders` prints are skipped; `-filters`
// prints no separator, so the separator cannot mark where the rows begin.
func parseComponentList(output string) map[string]bool {
	names := map[string]bool{}
	listing := false
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if !listing {
			listing = len(fields) == 1 && strings.HasSuffix(fields[0], ":")
			continue
		}
		if len(fields) >= 2 && fields[1] != "=" {
			names[fields[1]] = true
		}
	}
	return names
}

// componentListRunner returns the output of `ffmpeg -hide_banner <flag>` for
// flag -filters or -encoders. It is the probe's seam, like ToneMapRunner.
type componentListRunner func(ctx context.Context, flag string) ([]byte, error)

// defaultComponentListRunner is the production componentListRunner: one
// bounded, supervised listing of the resolved binary.
func defaultComponentListRunner(ctx context.Context, flag string) ([]byte, error) {
	if flag != "-filters" && flag != "-encoders" {
		return nil, errors.New("ffmpeg: component listing takes -filters or -encoders")
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, componentListTimeout)
	defer cancel()
	// #nosec G204 -- path is resolved from PATH and the flag is one of two
	// fixed listing flags checked above.
	cmd := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-nostdin", flag)
	out := &boundedBuffer{limit: captureLimitBytes}
	if err := supervise(ctx, cmd, out, io.Discard); err != nil {
		return nil, err
	}
	if out.overflowed() {
		return nil, ErrOutputOverflow
	}
	return out.bytes(), nil
}

const componentListTimeout = 10 * time.Second

// probeCapabilities lists the build's filters and encoders through run.
func probeCapabilities(ctx context.Context, run componentListRunner) (capabilities, error) {
	if run == nil {
		run = defaultComponentListRunner
	}
	filters, err := run(ctx, "-filters")
	if err != nil {
		return capabilities{}, err
	}
	encoders, err := run(ctx, "-encoders")
	if err != nil {
		return capabilities{}, err
	}
	return capabilities{Filters: parseComponentList(string(filters)), Encoders: parseComponentList(string(encoders))}, nil
}

type capabilitiesEntry = sharedResolution[capabilities]

var (
	capabilitiesMu      sync.Mutex
	capabilitiesByBuild = map[string]*capabilitiesEntry{}
)

// capabilitiesForCurrentBuild lists the resolved binary's filters and encoders
// on first use and keeps the answer in memory, keyed by the binary's identity.
// A failed listing is kept too (and fails closed in the exported checks): an
// unresolvable binary keys its own bucket, and a changed binary a new one.
func capabilitiesForCurrentBuild(ctx context.Context) (capabilities, error) {
	key, err := currentFFmpegIdentityKey(ctx)
	if err != nil {
		return capabilities{}, err
	}
	capabilitiesMu.Lock()
	entry, ok := capabilitiesByBuild[key]
	if !ok {
		entry = &capabilitiesEntry{}
		capabilitiesByBuild[key] = entry
	}
	capabilitiesMu.Unlock()
	return entry.wait(ctx, func() (capabilities, error) {
		return probeCapabilities(context.Background(), nil)
	})
}

// ComposedRecipeSupported reports whether the resolved FFmpeg build has every
// filter and encoder a live composed encode of a graphics picture (timedText
// false) or a timed-text picture (timedText true) runs with this rendition's
// shape, audio and adjustment. A build whose components cannot be listed
// cannot.
//
//io:ignore — in-memory capability check after a one-shot listing, not a server I/O journey.
func ComposedRecipeSupported(timedText bool, rendition Rendition) bool {
	return ComposedRecipeSupportedContext(context.Background(), timedText, rendition)
}

func ComposedRecipeSupportedContext(ctx context.Context, timedText bool, rendition Rendition) bool {
	caps, err := capabilitiesForCurrentBuild(ctx)
	if err != nil {
		return false
	}
	for _, args := range composedRecipeArgv(timedText, rendition) {
		if len(caps.missingFor(args)) > 0 {
			return false
		}
	}
	return true
}

// AudioAdjustSupported reports whether the resolved FFmpeg build has every
// audio filter and the encoder the audio encode of this rendition runs, its
// pitch and tempo adjustment included. A build whose components cannot be
// listed cannot.
//
//io:ignore — in-memory capability check after a one-shot listing, not a server I/O journey.
func AudioAdjustSupported(rendition Rendition) bool {
	return AudioAdjustSupportedContext(context.Background(), rendition)
}

func AudioAdjustSupportedContext(ctx context.Context, rendition Rendition) bool {
	caps, err := capabilitiesForCurrentBuild(ctx)
	return err == nil && len(caps.missingFor(rendition.audioArgs())) == 0
}

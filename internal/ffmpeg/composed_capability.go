package ffmpeg

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Composed-picture capability: a live composed stream is offered only when the
// resolved FFmpeg build has every filter and encoder the composition recipe
// (composedEncodeArgs) names. A build without libass, for one, has no `ass`
// filter, so a timed-text picture would fail at filter-graph parse and turn a
// song that plays as audio into a failing stream.
//
// Unlike the tone-map probe, listing is enough here: the recipe's filters take
// fixed options Beamfall controls, so a filter that is present parses them. The
// answer is kept in memory per resolved binary identity, like the tone-map
// verdict, so a BYO-FFmpeg swap under a running server is noticed.

// composedGraphicsFilters, composedTimedTextFilters and composedEncoders are
// what composedEncodeArgs emits for each picture kind, over every start-offset
// and tempo branch (TestComposedRecipeRequirementsCoverEveryBranch holds the
// lists to the recipe).
var (
	composedGraphicsFilters  = []string{"scale", "pad", "fps", "tpad", "trim", "setpts", "format"}
	composedTimedTextFilters = []string{"color", "setpts", "ass", "format"}
	composedEncoders         = []string{"libx264"}
)

// capabilities is the set of filters and encoders an FFmpeg build lists.
type capabilities struct {
	Filters  map[string]bool
	Encoders map[string]bool
}

// missingForComposed names the filters and encoders the composition recipe
// for one picture kind needs that the build lacks, in recipe order. Empty means
// the build can compose it.
func (c capabilities) missingForComposed(timedText bool) []string {
	filters := composedGraphicsFilters
	if timedText {
		filters = composedTimedTextFilters
	}
	var missing []string
	for _, name := range filters {
		if !c.Filters[name] {
			missing = append(missing, "filter "+name)
		}
	}
	for _, name := range composedEncoders {
		if !c.Encoders[name] {
			missing = append(missing, "encoder "+name)
		}
	}
	return missing
}

// parseComponentList reads the names out of `ffmpeg -filters` or `ffmpeg
// -encoders` output: every line after the legend's dashed separator is a flags
// column followed by the component name.
func parseComponentList(output string) map[string]bool {
	names := map[string]bool{}
	listing := false
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if !listing {
			listing = len(fields) == 1 && strings.Trim(fields[0], "-") == "" && strings.HasPrefix(fields[0], "--")
			continue
		}
		if len(fields) >= 2 {
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

type capabilitiesEntry struct {
	once sync.Once
	caps capabilities
	err  error
}

var (
	capabilitiesMu      sync.Mutex
	capabilitiesByBuild = map[string]*capabilitiesEntry{}
)

// capabilitiesForCurrentBuild lists the resolved binary's filters and encoders
// on first use and keeps the answer in memory, keyed by the binary's identity.
// A failed listing is kept too (and fails closed in missingForComposed): an
// unresolvable binary keys its own bucket, and a changed binary a new one.
func capabilitiesForCurrentBuild() (capabilities, error) {
	key := currentFFmpegIdentityKey()
	capabilitiesMu.Lock()
	entry, ok := capabilitiesByBuild[key]
	if !ok {
		entry = &capabilitiesEntry{}
		capabilitiesByBuild[key] = entry
	}
	capabilitiesMu.Unlock()
	entry.once.Do(func() {
		entry.caps, entry.err = probeCapabilities(context.Background(), nil)
	})
	return entry.caps, entry.err
}

// ComposedRecipeSupported reports whether the resolved FFmpeg build can run the
// composition recipe for a graphics picture (timedText false) or a timed-text
// picture (timedText true). A build whose components cannot be listed cannot.
//
//io:ignore — in-memory capability check after a one-shot listing, not a server I/O journey.
func ComposedRecipeSupported(timedText bool) bool {
	caps, err := capabilitiesForCurrentBuild()
	return err == nil && len(caps.missingForComposed(timedText)) == 0
}

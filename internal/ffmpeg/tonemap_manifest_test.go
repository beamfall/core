package ffmpeg

// Static conformance of the production tone-map recipe against the PINNED
// FFmpeg build, with no FFmpeg binary involved.
//
// TestProductionToneMapChainRunsOrIsRefused executes the chain, which is the
// stronger check — but it can only run where a conforming build exists. On a
// macOS/BYO developer host (stock Homebrew ffmpeg, no tonemapx, no zscale) the
// probe correctly fails, Beamfall correctly refuses HDR, the two agree, and the
// test logs and returns. That is the designed behaviour, so it must keep
// passing there; the consequence is that on those hosts the execution test
// cannot notice the recipe rotting. Reverting hdrToSDRFilters() to the shipped
// `zscale=t=linear:npl=100,tonemap=bt2390,zscale=…` bug leaves it GREEN.
//
// This test closes that hole. It parses the chain hdrToSDRFilters() actually
// emits and asserts every filter name, option key, and option value it uses is
// one the pinned build accepts, reading that from a checked-in verbatim record
// of `ffmpeg -h filter=<name>` on the pin itself
// (testdata/ffmpeg-pin-filters.txt, which documents its own regeneration). It
// therefore REDs on every host — conforming or not — the moment the recipe
// names something the shipped build cannot parse. `tonemap=bt2390` is exactly
// that: the pinned software `tonemap` filter enumerates 0..6
// (none|linear|gamma|clip|reinhard|hable|mobius) and has never had a bt2390
// member, while `tonemapx` enumerates 0..7 and does.
//
// This is not a cache of probe results (ADR-0075 §5 forbids that, and
// ToneMapCapability's in-process sync.Once remains the only memoization). It is
// a static record of a specific immutable artifact — the deb whose URL and
// SHA-256 are pinned in ffmpeg.lock — and it changes only when that pin does.
// The two tests are complementary: this one proves the recipe is well-formed
// for the build Beamfall ships, the execution test proves it actually
// transforms the picture.

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const pinnedFilterManifestPath = "testdata/ffmpeg-pin-filters.txt"

// filterOption is one AVOption of one filter as the pinned build reports it.
type filterOption struct {
	// kind is the declared type, e.g. "<int>" or "<double>".
	kind string
	// values holds every accepted spelling of an enumerated option — both the
	// symbolic names and their numeric equivalents, since FFmpeg accepts
	// either. Empty means the option is free-form (a string, or a bare
	// numeric with no enum members).
	values map[string]bool
}

// pinnedFilter is one filter's option set, in the pinned build's own
// declaration order. Order matters: FFmpeg binds a positional argument
// (`tonemap=bt2390`) to the next option in that order, so the shipped bug is
// only reproduced faithfully if the manifest preserves it.
type pinnedFilter struct {
	order   []string
	options map[string]filterOption
}

// TestProductionToneMapChainIsAcceptedByThePinnedBuild is the host-independent
// half of the tone-map coverage: it needs no ffmpeg on PATH, so it reds on a
// developer laptop just as it would in the shipped image.
//
// The SOFTWARE chain must be accepted by every recorded build. It is the
// fallback every transfer class lands on when no hardware backend is eligible,
// so a platform that cannot run it has no tone-map path at all.
func TestProductionToneMapChainIsAcceptedByThePinnedBuild(t *testing.T) {
	manifest := loadPinnedFilterManifest(t)

	chain := hdrToSDRFilters()
	if len(chain) == 0 {
		t.Fatal("hdrToSDRFilters() is empty; this test would verify nothing")
	}

	for _, platform := range sortedKeys(manifest) {
		for _, leg := range chain {
			name, _ := splitFilterLeg(t, leg)
			if _, known := manifest[platform][name]; !known {
				t.Fatalf("tone-map chain leg %q names filter %q, which %s does not cover for %s. "+
					"Either that build has no such filter — in which case every HDR re-encode dies at "+
					"filter-graph parse, the FFMPEG-TONEMAP-BT2390-INVALID-1 bug — or the recipe legitimately "+
					"grew a new filter and the manifest must be regenerated to cover it.",
					leg, name, pinnedFilterManifestPath, platform)
			}
			assertFilterLegAccepted(t, manifest[platform], platform, leg)
		}
	}
}

// TestHardwareToneMapChainsMatchTheRecordedBuilds is the D1a §5 half. A
// hardware backend is certifiable on a platform only where every filter its
// chain names is recorded there; where it is recorded, every option key and
// value the chain sets must be one that build accepts, so a hardware recipe
// cannot rot the way `tonemap=bt2390` did.
//
// An UNRECORDED backend is not a failure here — it is the fail-closed outcome
// the amendment requires: no verified spelling means no certification, the
// release gate sees no approved row, and the GA hardware claim stays RED.
func TestHardwareToneMapChainsMatchTheRecordedBuilds(t *testing.T) {
	manifest := loadPinnedFilterManifest(t)
	covered := 0

	for _, backend := range allHWAccels {
		chain := BuildToneMapChain(ToneMapChainRequest{Backend: backend, DevicePath: "/dev/dri/renderD128", Download: true})
		if len(chain.Filters) == 0 {
			t.Fatalf("backend %q builds no chain at all; D1a requires a real hardware chain per certified backend", backend)
		}
		for _, platform := range sortedKeys(manifest) {
			missing := missingChainFilters(t, manifest[platform], chain)
			if len(missing) > 0 {
				t.Logf("backend %s is NOT certifiable on %s: %s records no %s",
					backend, platform, pinnedFilterManifestPath, strings.Join(missing, ", "))
				continue
			}
			covered++
			for _, leg := range chain.Filters {
				assertFilterLegAccepted(t, manifest[platform], platform, leg)
			}
		}
	}

	if covered == 0 {
		t.Fatalf("no hardware tone-map chain is covered by any platform section in %s, so this test asserts "+
			"nothing about hardware spelling at all", pinnedFilterManifestPath)
	}
}

// missingChainFilters names the chain's filters the platform does not record.
func missingChainFilters(t *testing.T, platform map[string]pinnedFilter, chain ToneMapChain) []string {
	t.Helper()
	var missing []string
	for _, leg := range chain.Filters {
		name, _ := splitFilterLeg(t, leg)
		if _, known := platform[name]; !known {
			missing = append(missing, name)
		}
	}
	return missing
}

// assertFilterLegAccepted checks one chain leg's every option key and value
// against what that build declares.
func assertFilterLegAccepted(t *testing.T, platform map[string]pinnedFilter, platformName, leg string) {
	t.Helper()
	name, args := splitFilterLeg(t, leg)
	filter, known := platform[name]
	if !known {
		return
	}
	for position, arg := range args {
		key, value := bindFilterArg(t, filter, leg, arg, position)
		option, accepted := filter.options[key]
		if !accepted {
			t.Fatalf("tone-map chain leg %q sets option %q, but %s's %q filter has no such option "+
				"(it has: %s). FFmpeg rejects the whole filter graph for this.",
				leg, key, platformName, name, strings.Join(sortedKeys(filter.options), " "))
		}
		assertOptionValueAccepted(t, leg, name, key, value, option)
	}
}

// bindFilterArg resolves one filter argument to the option it actually sets,
// following FFmpeg's rule that a bare value binds to the option at that
// position in the filter's declaration order. `tonemap=bt2390` is exactly this
// case: it sets the software tonemap filter's first option, `tonemap`.
func bindFilterArg(t *testing.T, filter pinnedFilter, leg, arg string, position int) (string, string) {
	t.Helper()
	if key, value, named := strings.Cut(arg, "="); named {
		return key, value
	}
	if position >= len(filter.order) {
		t.Fatalf("tone-map chain leg %q passes %q at position %d, but the pinned build's filter declares "+
			"only %d options", leg, arg, position, len(filter.order))
	}
	return filter.order[position], arg
}

// assertOptionValueAccepted checks one option value against what the pinned
// build enumerates for it. An enumerated option must name a real member; a
// numeric option with no members must at least be a number.
func assertOptionValueAccepted(t *testing.T, leg, filter, key, value string, option filterOption) {
	t.Helper()
	if len(option.values) > 0 {
		if !option.values[value] {
			t.Fatalf("tone-map chain leg %q sets %s=%s, but the pinned build's %q filter accepts only "+
				"%s for that option. FFmpeg answers `Unable to parse option value` and the re-encode dies "+
				"at filter-graph parse — this is precisely how `tonemap=bt2390` shipped.",
				leg, key, value, filter, strings.Join(sortedKeys(option.values), "|"))
		}
		return
	}
	if option.kind != "<int>" && option.kind != "<double>" && option.kind != "<float>" {
		return
	}
	if _, err := strconv.ParseFloat(value, 64); err != nil {
		t.Fatalf("tone-map chain leg %q sets %s=%s, but the pinned build declares %s.%s as %s: %v",
			leg, key, value, filter, key, option.kind, err)
	}
}

// splitFilterLeg splits one chain leg into its filter name and its `key=value`
// arguments, using FFmpeg's own `name=a=1:b=2` filter syntax.
func splitFilterLeg(t *testing.T, leg string) (string, []string) {
	t.Helper()
	name, rest, hasArgs := strings.Cut(leg, "=")
	if name == "" {
		t.Fatalf("tone-map chain leg %q has no filter name", leg)
	}
	if !hasArgs {
		return name, nil
	}
	return name, strings.Split(rest, ":")
}

// loadPinnedFilterManifest parses the checked-in `ffmpeg -h filter=<name>`
// output of the pinned build into filter -> option -> accepted values.
//
// The help format is fixed-indentation: an AVOption line carries exactly three
// leading spaces, and each enumerated value of the preceding option carries
// exactly five. Only lines after that filter's `<name> AVOptions:` header count,
// so the input/output and threading preamble is never mistaken for an option.
// The result is keyed platform -> filter -> option -> accepted values. The
// platform dimension is what lets the file record that a build simply does not
// have a filter, which is the difference between "this backend is uncertifiable
// here" and "this recipe is broken".
func loadPinnedFilterManifest(t *testing.T) map[string]map[string]pinnedFilter {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(pinnedFilterManifestPath))
	if err != nil {
		t.Fatalf("read pinned filter manifest: %v", err)
	}

	manifest := make(map[string]map[string]pinnedFilter)
	var platform, name string
	var filter pinnedFilter
	var lastOption string
	inOptions := false

	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case strings.HasPrefix(line, "#"), strings.HasPrefix(line, "=== HELP_PLATFORM_PIN "):
			continue
		case strings.HasPrefix(line, "=== HELP_PLATFORM_BEGIN "):
			platform = strings.TrimSpace(strings.TrimPrefix(line, "=== HELP_PLATFORM_BEGIN "))
			manifest[platform] = map[string]pinnedFilter{}
			continue
		case strings.HasPrefix(line, "=== HELP_PLATFORM_END "):
			platform = ""
			continue
		case platform == "":
			continue
		case strings.HasPrefix(line, "=== HELP_FILTER_BEGIN "):
			name = strings.TrimSpace(strings.TrimPrefix(line, "=== HELP_FILTER_BEGIN "))
			filter = pinnedFilter{options: map[string]filterOption{}}
			inOptions = false
			lastOption = ""
			continue
		case strings.HasPrefix(line, "=== HELP_FILTER_END "):
			if name != "" {
				manifest[platform][name] = filter
			}
			name = ""
			continue
		case name == "":
			continue
		case strings.HasSuffix(line, " AVOptions:"):
			inOptions = true
			continue
		case !inOptions:
			continue
		}

		fields := strings.Fields(line)
		switch {
		case isIndented(line, 3) && len(fields) >= 2:
			lastOption = fields[0]
			filter.order = append(filter.order, lastOption)
			filter.options[lastOption] = filterOption{kind: fields[1], values: map[string]bool{}}
		case isIndented(line, 5) && len(fields) >= 2 && lastOption != "":
			// Both spellings: FFmpeg accepts the symbolic name and the number.
			filter.options[lastOption].values[fields[0]] = true
			filter.options[lastOption].values[fields[1]] = true
		}
	}

	if len(manifest) == 0 {
		t.Fatalf("%s parsed to zero platforms; the manifest or this parser is broken, and a manifest that "+
			"covers nothing would vacuously accept any recipe", pinnedFilterManifestPath)
	}
	for _, name := range sortedKeys(manifest) {
		if len(manifest[name]) == 0 {
			t.Fatalf("%s records platform %s with zero filters", pinnedFilterManifestPath, name)
		}
	}
	return manifest
}

// isIndented reports whether line begins with exactly n spaces followed by a
// non-space, which is how FFmpeg's help output distinguishes an option from one
// of its enumerated values.
func isIndented(line string, n int) bool {
	if len(line) <= n {
		return false
	}
	return strings.Count(line[:n], " ") == n && line[n] != ' '
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// TestPinnedFilterManifestRecordsThePinnedBuild guards the manifest itself: a
// truncated or mis-parsed file would make the check above vacuous, passing any
// recipe at all. It pins the two facts the tone-map recipe turns on — tonemapx
// HAS a bt2390 mode and the software tonemap filter does NOT — so a manifest
// regenerated against the wrong build cannot quietly disarm the test.
func TestPinnedFilterManifestRecordsThePinnedBuild(t *testing.T) {
	manifest := loadPinnedFilterManifest(t)

	for _, platform := range sortedKeys(manifest) {
		filters := manifest[platform]

		tonemapx, ok := filters["tonemapx"]
		if !ok {
			t.Fatalf("%s does not cover tonemapx for %s, the filter the production recipe uses",
				pinnedFilterManifestPath, platform)
		}
		if !tonemapx.options["tonemap"].values["bt2390"] {
			t.Fatalf("manifest says %s's tonemapx has no bt2390 mode (got %v); "+
				"either the manifest is broken or the pin moved out from under the recipe",
				platform, sortedKeys(tonemapx.options["tonemap"].values))
		}

		software, ok := filters["tonemap"]
		if !ok {
			t.Fatalf("%s does not cover the software tonemap filter for %s, so it could not catch a recipe "+
				"that regressed to it", pinnedFilterManifestPath, platform)
		}
		if software.options["tonemap"].values["bt2390"] {
			t.Fatalf("manifest claims %s's software tonemap filter accepts bt2390. It never has — that "+
				"belief IS the FFMPEG-TONEMAP-BT2390-INVALID-1 bug, and a manifest asserting it would "+
				"re-arm the trap", platform)
		}
		// Positional binding is what makes `tonemap=bt2390` a *value* error
		// rather than a syntax curiosity, so the recorded declaration order
		// must put the mode option first.
		if len(software.order) == 0 || software.order[0] != "tonemap" {
			t.Fatalf("manifest records %s's software tonemap filter options as %v; the mode option must be "+
				"first or a positional `tonemap=bt2390` would not be checked against it", platform, software.order)
		}
	}
}

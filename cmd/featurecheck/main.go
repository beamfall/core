// Command featurecheck is the feature-coverage ledger. It cross-references the
// feature registry (testing/features.yaml) against the `feature:<id>` markers
// declared by test sources across the repos, and reports the (feature x target)
// matrix: which features are proven on which devices/surfaces.
//
// It mirrors the completeness philosophy of internal/conformance TestArch
// (no route escapes the reveal registry) and TestEveryRouteRegistered — here,
// no feature escapes the test matrix. v1 is report-only; `-strict` exits
// non-zero when an applicable cell whose target root is present has no proof.
// `-strict-areas` applies the same rule only to comma-separated feature areas.
//
//	go run ./cmd/featurecheck                              # report
//	go run ./cmd/featurecheck -strict                      # global gate
//	go run ./cmd/featurecheck -strict-areas console,workflow # area gate
//
// Markers: put `feature:<id>` in a test (Go: a comment; Swift/Kotlin/JS: a
// comment or the test name). A marker found in a repo credits every target that
// repo serves (intersected with the feature's `applies`). Device-specific
// markers can refine this later; v1 credits at repo granularity.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const registryPath = "testing/features.yaml"

type feature struct {
	id      string
	area    string
	summary string
	adr     []string
	applies []string
	status  string
}

type targetTally struct {
	Applicable   int
	Proven       int
	Missing      int
	Unassessable int
}

type ratchetFile struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Targets       map[string]ratchetTarget `json:"targets"`
}

type ratchetTarget struct {
	MinProven int `json:"minProven"`
}

// scanJob maps a source root + file extension to the targets a marker there
// credits. A root that does not exist on disk makes its targets unassessable.
type scanJob struct {
	dir     string
	ext     string
	targets []string
}

var scanJobs = []scanJob{
	{dir: "internal", ext: "_test.go", targets: []string{"server"}},
	{dir: "internal/web/app", ext: ".test.ts", targets: []string{"web"}},
	{dir: "internal/web/app", ext: ".test.tsx", targets: []string{"web"}},
	{dir: "internal/web/app", ext: ".spec.ts", targets: []string{"web"}},
	{dir: "internal/web/app", ext: ".spec.tsx", targets: []string{"web"}},
	{dir: "../beamfall-apple", ext: ".swift", targets: []string{"tvos", "iphone", "ipad", "macos"}},
	{dir: "../beamfall-android", ext: ".kt", targets: []string{"androidtv", "androidmobile"}},
	{dir: "../beamfall-windows", ext: ".cs", targets: []string{"windows"}},
}

var markerRE = regexp.MustCompile(`feature:([A-Za-z0-9_-]+)`)

var featureStatuses = map[string]bool{
	"planned": true,
	"partial": true,
	"shipped": true,
}

func main() {
	strict := flag.Bool("strict", false, "exit non-zero if any applicable, assessable cell has no proof")
	strictAreas := flag.String("strict-areas", "", "comma-separated feature areas whose applicable, assessable cells must have proof")
	ratchetPath := flag.String("ratchet", "", "JSON file of per-target minimum proven counts")
	flag.Parse()

	os.Exit(run(runOptions{
		strict:      *strict,
		strictAreas: *strictAreas,
		ratchetPath: *ratchetPath,
	}, os.Stdout, os.Stderr))
}

type runOptions struct {
	strict      bool
	strictAreas string
	ratchetPath string
}

func run(options runOptions, stdout, stderr io.Writer) int {
	targets, features, err := parseRegistry(registryPath)
	if err != nil {
		fmt.Fprintln(stderr, "featurecheck:", err)
		return 2
	}
	enforcedAreas, enforcedAreaNames, err := parseStrictAreas(options.strictAreas, features)
	if err != nil {
		fmt.Fprintln(stderr, "featurecheck:", err)
		return 2
	}

	// covered[target] = set of feature ids proven for that target.
	covered := map[string]map[string]bool{}
	assessable := map[string]bool{}
	for _, job := range scanJobs {
		if _, statErr := os.Stat(job.dir); statErr != nil {
			continue // root absent — its targets stay unassessable
		}
		ids := scanMarkers(job.dir, job.ext)
		for _, tgt := range job.targets {
			assessable[tgt] = true
			if covered[tgt] == nil {
				covered[tgt] = map[string]bool{}
			}
			for id := range ids {
				covered[tgt][id] = true
			}
		}
	}

	// Tally.
	var applicable, ok, miss, unassessable int
	byTarget := map[string]targetTally{}
	var missing []string
	var proven []string
	var strictAreaMissing []string
	for _, f := range features {
		for _, tgt := range f.applies {
			applicable++
			tally := byTarget[tgt]
			tally.Applicable++
			switch {
			case !assessable[tgt]:
				unassessable++
				tally.Unassessable++
			case covered[tgt][f.id]:
				ok++
				tally.Proven++
				proven = append(proven, f.id+" @ "+tgt)
			default:
				miss++
				tally.Missing++
				missing = append(missing, f.id+" @ "+tgt)
				if enforcedAreas[f.area] {
					strictAreaMissing = append(strictAreaMissing, f.id+" @ "+tgt)
				}
			}
			byTarget[tgt] = tally
		}
	}

	sort.Strings(proven)
	sort.Strings(missing)
	sort.Strings(strictAreaMissing)

	fmt.Fprintf(stdout, "feature-coverage ledger — %d features x %d targets\n", len(features), len(targets))
	fmt.Fprintf(stdout, "targets present: %s\n", presentList(targets, assessable))
	fmt.Fprintf(stdout, "applicable cells: %d | proven: %d | missing: %d | unassessable (target root absent): %d\n\n",
		applicable, ok, miss, unassessable)
	fmt.Fprintln(stdout, "PER-TARGET:")
	for _, tgt := range targets {
		tally := byTarget[tgt]
		state := ""
		if !assessable[tgt] {
			state = " (unassessable)"
		}
		fmt.Fprintf(stdout, "  %s%s: applicable=%d proven=%d missing=%d unassessable=%d\n",
			tgt, state, tally.Applicable, tally.Proven, tally.Missing, tally.Unassessable)
	}
	fmt.Fprintln(stdout)

	if len(proven) > 0 {
		fmt.Fprintf(stdout, "PROVEN (%d):\n", len(proven))
		for _, p := range proven {
			fmt.Fprintf(stdout, "  ok  %s\n", p)
		}
		fmt.Fprintln(stdout)
	}
	fmt.Fprintf(stdout, "MISSING (%d applicable, assessable cells with no proof):\n", len(missing))
	for _, m := range missing {
		fmt.Fprintf(stdout, "  --  %s\n", m)
	}

	if options.strict && miss > 0 {
		fmt.Fprintf(stderr, "\nfeaturecheck: %d uncovered cell(s) — strict mode failing\n", miss)
		return 1
	}
	failed := false
	if options.ratchetPath != "" {
		if err := checkRatchet(options.ratchetPath, targets, assessable, byTarget, stdout); err != nil {
			fmt.Fprintln(stderr, "\nfeaturecheck:", err)
			failed = true
		}
	}
	if len(strictAreaMissing) > 0 {
		fmt.Fprintf(stderr, "\nfeaturecheck: %d uncovered cell(s) in strict area(s) %s — strict area mode failing:\n  %s\n",
			len(strictAreaMissing), strings.Join(enforcedAreaNames, ", "), strings.Join(strictAreaMissing, "\n  "))
		failed = true
	}
	if failed {
		return 1
	}
	return 0
}

func parseStrictAreas(value string, features []feature) (map[string]bool, []string, error) {
	valid := map[string]bool{}
	for _, f := range features {
		if f.area != "" {
			valid[f.area] = true
		}
	}
	var validNames []string
	for area := range valid {
		validNames = append(validNames, area)
	}
	sort.Strings(validNames)

	enforced := map[string]bool{}
	var unknown []string
	for _, raw := range strings.Split(value, ",") {
		area := strings.TrimSpace(raw)
		if area == "" || enforced[area] {
			continue
		}
		if !valid[area] {
			unknown = append(unknown, area)
			continue
		}
		enforced[area] = true
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, nil, fmt.Errorf("unknown strict area(s) %q; valid areas: %s",
			strings.Join(unknown, ", "), strings.Join(validNames, ", "))
	}

	var enforcedNames []string
	for area := range enforced {
		enforcedNames = append(enforcedNames, area)
	}
	sort.Strings(enforcedNames)
	return enforced, enforcedNames, nil
}

func checkRatchet(path string, targets []string, assessable map[string]bool, byTarget map[string]targetTally, stdout io.Writer) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading ratchet: %w", err)
	}
	var ratchet ratchetFile
	if err := json.Unmarshal(data, &ratchet); err != nil {
		return fmt.Errorf("parsing ratchet: %w", err)
	}
	if ratchet.SchemaVersion != 1 {
		return fmt.Errorf("ratchet schemaVersion must be 1, got %d", ratchet.SchemaVersion)
	}
	if len(ratchet.Targets) == 0 {
		return fmt.Errorf("ratchet has no targets")
	}

	knownTargets := map[string]bool{}
	for _, tgt := range targets {
		knownTargets[tgt] = true
	}
	var failures []string
	for _, tgt := range targets {
		expectation, ok := ratchet.Targets[tgt]
		if !ok {
			continue
		}
		if expectation.MinProven < 0 {
			failures = append(failures, fmt.Sprintf("%s minProven is negative", tgt))
			continue
		}
		if !assessable[tgt] {
			fmt.Fprintf(stdout, "RATCHET skip %s: target root absent\n", tgt)
			continue
		}
		proven := byTarget[tgt].Proven
		if proven < expectation.MinProven {
			failures = append(failures, fmt.Sprintf("%s proven=%d below ratchet minProven=%d", tgt, proven, expectation.MinProven))
			continue
		}
		fmt.Fprintf(stdout, "RATCHET ok %s: proven=%d minProven=%d\n", tgt, proven, expectation.MinProven)
	}
	for tgt, expectation := range ratchet.Targets {
		if !knownTargets[tgt] {
			failures = append(failures, fmt.Sprintf("%s is not a registry target", tgt))
			continue
		}
		if expectation.MinProven < 0 {
			failures = append(failures, fmt.Sprintf("%s minProven is negative", tgt))
			continue
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return fmt.Errorf("coverage ratchet failed:\n  %s", strings.Join(failures, "\n  "))
	}
	return nil
}

// presentList renders the targets, marking absent (unassessable) ones.
func presentList(targets []string, assessable map[string]bool) string {
	var parts []string
	for _, t := range targets {
		if assessable[t] {
			parts = append(parts, t)
		} else {
			parts = append(parts, t+"(absent)")
		}
	}
	return strings.Join(parts, ", ")
}

// scanMarkers walks dir and returns the set of feature ids referenced by a
// `feature:<id>` marker in any file whose name ends with ext.
func scanMarkers(dir, ext string) map[string]bool {
	ids := map[string]bool{}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ext) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, m := range markerRE.FindAllSubmatch(data, -1) {
			ids[string(m[1])] = true
		}
		return nil
	})
	return ids
}

// parseRegistry reads the regular subset of testing/features.yaml: the
// top-level schema version and `targets: [...]` flow list, plus each feature's
// required scalar/list fields. It is intentionally minimal (no YAML dependency
// — the project is stdlib-only) and fail-closed against the scanner target
// vocabulary that makes registry cells assessable.
func parseRegistry(path string) (targets []string, features []feature, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading registry: %w", err)
	}
	schemaVersion := 0
	var cur *feature
	featureIDs := map[string]bool{}
	flush := func() error {
		if cur == nil {
			return nil
		}
		var missing []string
		if cur.id == "" {
			missing = append(missing, "id")
		}
		if cur.area == "" {
			missing = append(missing, "area")
		}
		if cur.summary == "" {
			missing = append(missing, "summary")
		}
		if len(cur.adr) == 0 {
			missing = append(missing, "adr")
		}
		if len(cur.applies) == 0 {
			missing = append(missing, "applies")
		}
		if cur.status == "" {
			missing = append(missing, "status")
		}
		if len(missing) > 0 {
			return fmt.Errorf("feature %q missing required field(s): %s", cur.id, strings.Join(missing, ", "))
		}
		if !featureStatuses[cur.status] {
			return fmt.Errorf("feature %q has invalid status %q (want planned, partial, or shipped)", cur.id, cur.status)
		}
		if featureIDs[cur.id] {
			return fmt.Errorf("duplicate feature id %q", cur.id)
		}
		if duplicate, ok := firstDuplicate(cur.applies); ok {
			return fmt.Errorf("feature %q has duplicate applies target %q", cur.id, duplicate)
		}
		featureIDs[cur.id] = true
		features = append(features, *cur)
		cur = nil
		return nil
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "schemaVersion:") {
			value := strings.TrimSpace(trimmed[len("schemaVersion:"):])
			parsed, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				return nil, nil, fmt.Errorf("registry schemaVersion must be 1, got %q", value)
			}
			schemaVersion = parsed
			continue
		}
		if targets == nil && strings.HasPrefix(trimmed, "targets:") {
			targets = parseFlowList(trimmed[len("targets:"):])
			continue
		}
		if strings.HasPrefix(trimmed, "- id:") {
			if ferr := flush(); ferr != nil {
				return nil, nil, ferr
			}
			cur = &feature{id: strings.TrimSpace(trimmed[len("- id:"):])}
			continue
		}
		if cur != nil && strings.HasPrefix(trimmed, "area:") {
			cur.area = strings.TrimSpace(trimmed[len("area:"):])
			continue
		}
		if cur != nil && strings.HasPrefix(trimmed, "summary:") {
			cur.summary = strings.TrimSpace(trimmed[len("summary:"):])
			continue
		}
		if cur != nil && strings.HasPrefix(trimmed, "adr:") {
			cur.adr = parseFlowList(trimmed[len("adr:"):])
			continue
		}
		if cur != nil && strings.HasPrefix(trimmed, "applies:") {
			cur.applies = parseFlowList(trimmed[len("applies:"):])
			continue
		}
		if cur != nil && strings.HasPrefix(trimmed, "status:") {
			cur.status = strings.TrimSpace(trimmed[len("status:"):])
		}
	}
	if ferr := flush(); ferr != nil {
		return nil, nil, ferr
	}
	if schemaVersion != 1 {
		return nil, nil, fmt.Errorf("registry schemaVersion must be 1, got %d", schemaVersion)
	}
	if len(targets) == 0 {
		return nil, nil, fmt.Errorf("registry has no top-level targets: list")
	}
	if duplicate, ok := firstDuplicate(targets); ok {
		return nil, nil, fmt.Errorf("registry has duplicate top-level target %q", duplicate)
	}
	registryTargets := stringSet(targets)
	scannerTargets := map[string]bool{}
	for _, job := range scanJobs {
		for _, target := range job.targets {
			scannerTargets[target] = true
		}
	}
	if missing, unknown := setDifference(scannerTargets, registryTargets), setDifference(registryTargets, scannerTargets); len(missing) > 0 || len(unknown) > 0 {
		return nil, nil, fmt.Errorf("registry targets do not match scanner targets (missing: %s; unknown: %s)", strings.Join(missing, ", "), strings.Join(unknown, ", "))
	}
	for _, feature := range features {
		for _, target := range feature.applies {
			if !registryTargets[target] {
				return nil, nil, fmt.Errorf("feature %q applies to unknown target %q", feature.id, target)
			}
		}
	}
	if len(features) == 0 {
		return nil, nil, fmt.Errorf("registry has no features")
	}
	return targets, features, nil
}

func firstDuplicate(values []string) (string, bool) {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return value, true
		}
		seen[value] = true
	}
	return "", false
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func setDifference(left, right map[string]bool) []string {
	var difference []string
	for value := range left {
		if !right[value] {
			difference = append(difference, value)
		}
	}
	sort.Strings(difference)
	return difference
}

// parseFlowList parses a single-line YAML flow list: `[a, b, c]` -> ["a","b","c"].
func parseFlowList(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

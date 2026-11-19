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
//
// Surface decisions (ADR-0237): every (feature x surface) cell, where a surface
// is any target but `server`, is supported (in `applies` and proven), planned
// (in `applies`, not yet proven), unsupported (an `unsupported:` reason) or
// undecided (none of these). A feature may also name the ticket that tracks a
// planned cell (`tickets:`) and the release a surface ships it from
// (`release:`). The ratchet caps undecided and unticketed cells per surface.
//
//	go run ./cmd/featurecheck -release 1.0                 # release feature gate
//	go run ./cmd/featurecheck -gaps                        # unticketed planned cells
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

// serverTarget is the core itself. It is a proof target but not a surface:
// surface decisions (unsupported, tickets, release) never name it.
const serverTarget = "server"

// decisionBlocks are the per-feature `surface: value` block fields.
var decisionBlocks = []string{"unsupported", "tickets", "release"}

var (
	ticketRE        = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)
	releaseRE       = regexp.MustCompile(`^[0-9]+\.[0-9]+(\.[0-9]+)?$`)
	ownerDecisionRE = regexp.MustCompile(`\(owner [0-9]{4}-[0-9]{2}-[0-9]{2}\)`)
)

type feature struct {
	id      string
	area    string
	summary string
	adr     []string
	applies []string
	status  string
	// unsupported, tickets and release map a surface to its reason, ticket ID
	// or first release. Nil when the block is absent.
	unsupported map[string]string
	tickets     map[string]string
	release     map[string]string
}

func (f *feature) block(name string) *map[string]string {
	switch name {
	case "unsupported":
		return &f.unsupported
	case "tickets":
		return &f.tickets
	default:
		return &f.release
	}
}

// decisionTally counts one surface's cells by decision state. Unticketed
// counts planned cells on an assessable surface that name no ticket.
type decisionTally struct {
	Supported   int
	Planned     int
	Unsupported int
	Undecided   int
	Unticketed  int
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
	MinProven     int  `json:"minProven"`
	MaxUndecided  *int `json:"maxUndecided,omitempty"`
	MaxUnticketed *int `json:"maxUnticketed,omitempty"`
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
	ratchetPath := flag.String("ratchet", "", "JSON file of per-target minimum proven and maximum undecided/unticketed counts")
	release := flag.String("release", "", "exit non-zero unless every cell with release <= this version (X.Y) is proven")
	gaps := flag.Bool("gaps", false, "list planned, unproven cells with no ticket, by surface and area")
	flag.Parse()

	os.Exit(run(runOptions{
		strict:      *strict,
		strictAreas: *strictAreas,
		ratchetPath: *ratchetPath,
		release:     *release,
		gaps:        *gaps,
	}, os.Stdout, os.Stderr))
}

type runOptions struct {
	strict      bool
	strictAreas string
	ratchetPath string
	release     string
	gaps        bool
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
	if options.release != "" && !releaseRE.MatchString(options.release) {
		fmt.Fprintf(stderr, "featurecheck: -release %q is not a version (want X.Y or X.Y.Z)\n", options.release)
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

	decisions, gaps := tallyDecisions(targets, features, assessable, covered)
	var releaseFailures []string
	if options.release != "" {
		releaseFailures = checkRelease(options.release, features, assessable, covered)
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
	fmt.Fprintln(stdout, "PER-SURFACE DECISIONS:")
	for _, tgt := range targets {
		if tgt == serverTarget {
			continue
		}
		d := decisions[tgt]
		state := ""
		if !assessable[tgt] {
			state = " (unassessable)"
		}
		fmt.Fprintf(stdout, "  %s%s: supported=%d planned=%d unsupported=%d undecided=%d unticketed=%d\n",
			tgt, state, d.Supported, d.Planned, d.Unsupported, d.Undecided, d.Unticketed)
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
	if options.gaps {
		printGaps(stdout, targets, gaps)
	}

	if options.strict && miss > 0 {
		fmt.Fprintf(stderr, "\nfeaturecheck: %d uncovered cell(s) — strict mode failing\n", miss)
		return 1
	}
	failed := false
	if options.ratchetPath != "" {
		if err := checkRatchet(options.ratchetPath, targets, assessable, byTarget, decisions, stdout); err != nil {
			fmt.Fprintln(stderr, "\nfeaturecheck:", err)
			failed = true
		}
	}
	if len(strictAreaMissing) > 0 {
		fmt.Fprintf(stderr, "\nfeaturecheck: %d uncovered cell(s) in strict area(s) %s — strict area mode failing:\n  %s\n",
			len(strictAreaMissing), strings.Join(enforcedAreaNames, ", "), strings.Join(strictAreaMissing, "\n  "))
		failed = true
	}
	if len(releaseFailures) > 0 {
		fmt.Fprintf(stderr, "\nfeaturecheck: %d cell(s) targeted at release <= %s are not proven — release gate failing:\n  %s\n",
			len(releaseFailures), options.release, strings.Join(releaseFailures, "\n  "))
		failed = true
	}
	if failed {
		return 1
	}
	return 0
}

// tallyDecisions classifies every (feature x surface) cell and collects the
// unticketed planned cells as gaps[surface][area] = feature ids.
func tallyDecisions(targets []string, features []feature, assessable map[string]bool, covered map[string]map[string]bool) (map[string]decisionTally, map[string]map[string][]string) {
	decisions := map[string]decisionTally{}
	gaps := map[string]map[string][]string{}
	for _, f := range features {
		applies := stringSet(f.applies)
		for _, tgt := range targets {
			if tgt == serverTarget {
				continue
			}
			d := decisions[tgt]
			switch {
			case applies[tgt] && assessable[tgt] && covered[tgt][f.id]:
				d.Supported++
			case applies[tgt]:
				d.Planned++
				if assessable[tgt] && f.tickets[tgt] == "" {
					d.Unticketed++
					if gaps[tgt] == nil {
						gaps[tgt] = map[string][]string{}
					}
					gaps[tgt][f.area] = append(gaps[tgt][f.area], f.id)
				}
			case f.unsupported[tgt] != "":
				d.Unsupported++
			default:
				d.Undecided++
			}
			decisions[tgt] = d
		}
	}
	return decisions, gaps
}

// checkRelease returns every cell whose release is at or before the given
// version but is not proven. An absent target root cannot prove anything, so
// its cells fail: the release gate is fail-closed.
func checkRelease(release string, features []feature, assessable map[string]bool, covered map[string]map[string]bool) []string {
	var failures []string
	for _, f := range features {
		for surface, first := range f.release {
			if compareVersions(first, release) > 0 {
				continue
			}
			switch {
			case !assessable[surface]:
				failures = append(failures, fmt.Sprintf("%s @ %s (release %s; target root absent)", f.id, surface, first))
			case !covered[surface][f.id]:
				failures = append(failures, fmt.Sprintf("%s @ %s (release %s)", f.id, surface, first))
			}
		}
	}
	sort.Strings(failures)
	return failures
}

func printGaps(stdout io.Writer, targets []string, gaps map[string]map[string][]string) {
	total := 0
	for _, areas := range gaps {
		for _, ids := range areas {
			total += len(ids)
		}
	}
	fmt.Fprintf(stdout, "\nGAPS (%d planned, unproven cell(s) with no ticket, by surface/area):\n", total)
	for _, tgt := range targets {
		var areas []string
		for area := range gaps[tgt] {
			areas = append(areas, area)
		}
		sort.Strings(areas)
		for _, area := range areas {
			ids := gaps[tgt][area]
			sort.Strings(ids)
			fmt.Fprintf(stdout, "  %s/%s (%d): %s\n", tgt, area, len(ids), strings.Join(ids, ", "))
		}
	}
}

// compareVersions compares two dotted versions already validated by
// releaseRE; a missing component counts as zero.
func compareVersions(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(left) || i < len(right); i++ {
		var l, r int
		if i < len(left) {
			l, _ = strconv.Atoi(left[i])
		}
		if i < len(right) {
			r, _ = strconv.Atoi(right[i])
		}
		if l != r {
			if l < r {
				return -1
			}
			return 1
		}
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

func checkRatchet(path string, targets []string, assessable map[string]bool, byTarget map[string]targetTally, decisions map[string]decisionTally, stdout io.Writer) error {
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
		if invalid := invalidRatchetTarget(tgt, expectation); invalid != "" {
			failures = append(failures, invalid)
			continue
		}
		if expectation.MaxUndecided != nil {
			undecided := decisions[tgt].Undecided
			if undecided > *expectation.MaxUndecided {
				failures = append(failures, fmt.Sprintf("%s undecided=%d above ratchet maxUndecided=%d", tgt, undecided, *expectation.MaxUndecided))
			} else {
				fmt.Fprintf(stdout, "RATCHET ok %s: undecided=%d maxUndecided=%d\n", tgt, undecided, *expectation.MaxUndecided)
			}
		}
		if !assessable[tgt] {
			fmt.Fprintf(stdout, "RATCHET skip %s: target root absent\n", tgt)
			continue
		}
		proven := byTarget[tgt].Proven
		if proven < expectation.MinProven {
			failures = append(failures, fmt.Sprintf("%s proven=%d below ratchet minProven=%d", tgt, proven, expectation.MinProven))
		} else {
			fmt.Fprintf(stdout, "RATCHET ok %s: proven=%d minProven=%d\n", tgt, proven, expectation.MinProven)
		}
		if expectation.MaxUnticketed != nil {
			unticketed := decisions[tgt].Unticketed
			if unticketed > *expectation.MaxUnticketed {
				failures = append(failures, fmt.Sprintf("%s unticketed=%d above ratchet maxUnticketed=%d", tgt, unticketed, *expectation.MaxUnticketed))
			} else {
				fmt.Fprintf(stdout, "RATCHET ok %s: unticketed=%d maxUnticketed=%d\n", tgt, unticketed, *expectation.MaxUnticketed)
			}
		}
	}
	for tgt := range ratchet.Targets {
		if !knownTargets[tgt] {
			failures = append(failures, fmt.Sprintf("%s is not a registry target", tgt))
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return fmt.Errorf("coverage ratchet failed:\n  %s", strings.Join(failures, "\n  "))
	}
	return nil
}

// invalidRatchetTarget reports a malformed ratchet entry, or "" when valid.
func invalidRatchetTarget(tgt string, expectation ratchetTarget) string {
	switch {
	case expectation.MinProven < 0:
		return tgt + " minProven is negative"
	case expectation.MaxUndecided != nil && *expectation.MaxUndecided < 0:
		return tgt + " maxUndecided is negative"
	case expectation.MaxUnticketed != nil && *expectation.MaxUnticketed < 0:
		return tgt + " maxUnticketed is negative"
	case tgt == serverTarget && (expectation.MaxUndecided != nil || expectation.MaxUnticketed != nil):
		return tgt + " is not a surface; maxUndecided and maxUnticketed do not apply"
	}
	return ""
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
// top-level schema version, `targets: [...]` and `fullParity: [...]` flow
// lists, plus each feature's required scalar/list fields and its optional
// `unsupported:`, `tickets:` and `release:` blocks of `surface: value` lines.
// It is intentionally minimal (no YAML dependency — the project is
// stdlib-only) and fail-closed against the scanner target vocabulary that
// makes registry cells assessable.
func parseRegistry(path string) (targets []string, features []feature, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading registry: %w", err)
	}
	schemaVersion := 0
	var cur *feature
	var fullParity []string
	block, blockIndent := "", 0
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
		for _, name := range decisionBlocks {
			if m := *cur.block(name); m != nil && len(m) == 0 {
				return fmt.Errorf("feature %q has an empty %s block", cur.id, name)
			}
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
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if block != "" {
			if indent > blockIndent {
				key, value, ok := strings.Cut(trimmed, ":")
				if !ok {
					return nil, nil, fmt.Errorf("feature %q %s entry %q is not `surface: value`", cur.id, block, trimmed)
				}
				key, value = strings.TrimSpace(key), unquote(stripComment(value))
				entries := *cur.block(block)
				if _, dup := entries[key]; dup {
					return nil, nil, fmt.Errorf("feature %q has duplicate %s surface %q", cur.id, block, key)
				}
				entries[key] = value
				continue
			}
			block = ""
		}
		if cur != nil {
			if name, inline, ok := blockStart(trimmed); ok {
				if inline {
					return nil, nil, fmt.Errorf("feature %q %s must be a block of `surface: value` lines", cur.id, name)
				}
				entries := cur.block(name)
				if *entries != nil {
					return nil, nil, fmt.Errorf("feature %q has more than one %s block", cur.id, name)
				}
				*entries = map[string]string{}
				block, blockIndent = name, indent
				continue
			}
		}
		if indent == 0 && strings.HasPrefix(trimmed, "fullParity:") {
			if fullParity != nil {
				return nil, nil, fmt.Errorf("registry has more than one fullParity list")
			}
			value := stripComment(trimmed[len("fullParity:"):])
			if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
				return nil, nil, fmt.Errorf("registry fullParity must be a one-line flow list `[surface, ...]`, got %q", value)
			}
			if fullParity = parseFlowList(value); fullParity == nil {
				fullParity = []string{}
			}
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
	if err := validateDecisions(registryTargets, fullParity, features); err != nil {
		return nil, nil, err
	}
	return targets, features, nil
}

// blockStart reports whether a trimmed line opens a decision block, and
// whether it wrongly carries an inline value.
func blockStart(trimmed string) (name string, inline, ok bool) {
	for _, name := range decisionBlocks {
		if rest, found := strings.CutPrefix(trimmed, name+":"); found {
			return name, stripComment(rest) != "", true
		}
	}
	return "", false, false
}

// validateDecisions enforces the ADR-0237 surface-decision rules: blocks name
// surfaces only; an unsupported surface has a reason and is not in applies; a
// ticket or release names a surface in applies; values are well formed; and an
// exclusion on a full-parity surface records the owner's dated decision.
func validateDecisions(registryTargets map[string]bool, fullParity []string, features []feature) error {
	if duplicate, ok := firstDuplicate(fullParity); ok {
		return fmt.Errorf("registry has duplicate fullParity surface %q", duplicate)
	}
	parity := stringSet(fullParity)
	for _, surface := range fullParity {
		if !registryTargets[surface] || surface == serverTarget {
			return fmt.Errorf("fullParity names %q, which is not a surface", surface)
		}
	}
	for _, f := range features {
		applies := stringSet(f.applies)
		for _, name := range decisionBlocks {
			for _, surface := range sortedKeys(*f.block(name)) {
				value := (*f.block(name))[surface]
				if !registryTargets[surface] || surface == serverTarget {
					return fmt.Errorf("feature %q %s names %q, which is not a surface", f.id, name, surface)
				}
				switch name {
				case "unsupported":
					if applies[surface] {
						return fmt.Errorf("feature %q is both in applies and unsupported for %q", f.id, surface)
					}
					if value == "" {
						return fmt.Errorf("feature %q unsupported %q has no reason", f.id, surface)
					}
					if parity[surface] && !ownerDecisionRE.MatchString(value) {
						return fmt.Errorf("feature %q unsupported %q needs an owner decision: %q is a full-parity surface, so the reason must carry (owner YYYY-MM-DD)", f.id, surface, surface)
					}
				case "tickets":
					if !applies[surface] {
						return fmt.Errorf("feature %q has a ticket for %q, which is not in applies", f.id, surface)
					}
					if !ticketRE.MatchString(value) {
						return fmt.Errorf("feature %q ticket %q for %q is not a ticket ID (want PREFIX-NUMBER)", f.id, value, surface)
					}
				case "release":
					if !applies[surface] {
						return fmt.Errorf("feature %q has a release for %q, which is not in applies", f.id, surface)
					}
					if !releaseRE.MatchString(value) {
						return fmt.Errorf("feature %q release %q for %q is not a version (want X.Y or X.Y.Z)", f.id, value, surface)
					}
				}
			}
		}
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// stripComment trims value and drops a trailing YAML comment: a `#` at the
// start or after whitespace, outside a leading quoted scalar.
func stripComment(value string) string {
	value = strings.TrimSpace(value)
	start := 0
	if len(value) > 0 && (value[0] == '"' || value[0] == '\'') {
		if end := strings.IndexByte(value[1:], value[0]); end >= 0 {
			start = end + 2
		}
	}
	for i := start; i < len(value); i++ {
		if value[i] == '#' && (i == 0 || value[i-1] == ' ' || value[i-1] == '\t') {
			return strings.TrimSpace(value[:i])
		}
	}
	return value
}

// unquote strips one pair of matching surrounding quotes.
func unquote(value string) string {
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}
	return value
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

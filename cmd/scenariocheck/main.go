// Command scenariocheck reconciles testing/scenarios.yaml against scenario:<id>
// markers in tests and scripts. It is the journey axis beside featurecheck
// (feature breadth) and ioe2e (I/O breadth).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const registryPath = "testing/scenarios.yaml"

type scenario struct {
	id            string
	tier          string
	applies       []string
	blocked       string
	releaseWaiver releaseWaiver
}

type releaseWaiver struct {
	ticket        string
	owner         string
	expiresAt     string
	evidencePath  string
	humanAccepted bool
}

type tally struct {
	Applicable   int
	Proven       int
	Missing      int
	Blocked      int
	Unassessable int
}

type scanJob struct {
	dir      string
	suffixes []string
	targets  []string
}

var scanJobs = []scanJob{
	{dir: "cmd", suffixes: []string{"_test.go"}, targets: []string{"server"}},
	{dir: "internal", suffixes: []string{"_test.go"}, targets: []string{"server"}},
	{dir: "script", suffixes: []string{".sh", ".py"}, targets: []string{"server"}},
	{dir: "internal/web/app", suffixes: []string{".test.ts", ".test.tsx", ".spec.ts", ".spec.tsx"}, targets: []string{"web"}},
	{dir: "../beamfall-apple", suffixes: []string{".swift"}, targets: []string{"tvos", "iphone", "ipad", "macos"}},
	{dir: "../beamfall-android", suffixes: []string{".kt"}, targets: []string{"androidtv", "androidmobile"}},
	{dir: "../beamfall-windows", suffixes: []string{".cs"}, targets: []string{"windows"}},
}

var markerRE = regexp.MustCompile(`scenario:([A-Za-z0-9_-]+)`)

func main() {
	strict := flag.Bool("strict", false, "exit non-zero if any unblocked applicable, assessable scenario cell has no proof")
	release := flag.Bool("release", false, "exit non-zero if any release-tier scenario is missing or blocked without a valid releaseWaiver")
	flag.Parse()

	targets, scenarios, err := parseRegistry(registryPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scenariocheck:", err)
		os.Exit(2)
	}

	covered := map[string]map[string]bool{}
	assessable := map[string]bool{}
	for _, job := range scanJobs {
		if _, statErr := os.Stat(job.dir); statErr != nil {
			continue
		}
		ids := scanMarkers(job)
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

	var proven, missing, blocked []string
	var releaseFailures []string
	byTarget := map[string]tally{}
	for _, s := range scenarios {
		for _, tgt := range s.applies {
			t := byTarget[tgt]
			t.Applicable++
			switch {
			case !assessable[tgt]:
				t.Unassessable++
				if *release && s.tier == "release" {
					if err := validReleaseWaiver(s.releaseWaiver); err != nil {
						releaseFailures = append(releaseFailures, s.id+" @ "+tgt+" unassessable without valid releaseWaiver: "+err.Error())
					}
				}
			case covered[tgt][s.id]:
				t.Proven++
				proven = append(proven, s.id+" @ "+tgt)
			case s.blocked != "":
				t.Blocked++
				blocked = append(blocked, s.id+" @ "+tgt+" - "+s.blocked)
				if *release && s.tier == "release" {
					if err := validReleaseWaiver(s.releaseWaiver); err != nil {
						releaseFailures = append(releaseFailures, s.id+" @ "+tgt+" blocked without valid releaseWaiver: "+err.Error())
					}
				}
			default:
				t.Missing++
				missing = append(missing, s.id+" @ "+tgt)
				if *release && s.tier == "release" {
					releaseFailures = append(releaseFailures, s.id+" @ "+tgt+" missing release proof")
				}
			}
			byTarget[tgt] = t
		}
	}

	sort.Strings(proven)
	sort.Strings(missing)
	sort.Strings(blocked)

	fmt.Printf("scenario ledger - %d scenarios x %d targets\n", len(scenarios), len(targets))
	fmt.Printf("targets present: %s\n\n", presentList(targets, assessable))
	fmt.Println("PER-TARGET:")
	for _, tgt := range targets {
		t := byTarget[tgt]
		state := ""
		if !assessable[tgt] {
			state = " (unassessable)"
		}
		fmt.Printf("  %s%s: applicable=%d proven=%d missing=%d blocked=%d unassessable=%d\n",
			tgt, state, t.Applicable, t.Proven, t.Missing, t.Blocked, t.Unassessable)
	}
	printList("PROVEN", proven)
	printList("BLOCKED", blocked)
	printList("MISSING", missing)

	if *strict && len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "\nscenariocheck: %d uncovered scenario cell(s)\n", len(missing))
		os.Exit(1)
	}
	if *release && len(releaseFailures) > 0 {
		sort.Strings(releaseFailures)
		fmt.Fprintf(os.Stderr, "\nscenariocheck release blockers (%d):\n  %s\n", len(releaseFailures), strings.Join(releaseFailures, "\n  "))
		os.Exit(1)
	}
}

func validReleaseWaiver(w releaseWaiver) error {
	if w.ticket == "" {
		return fmt.Errorf("missing ticket")
	}
	if w.owner == "" {
		return fmt.Errorf("missing owner")
	}
	if w.evidencePath == "" {
		return fmt.Errorf("missing evidencePath")
	}
	if !w.humanAccepted {
		return fmt.Errorf("humanAccepted is false")
	}
	expires, err := time.Parse("2006-01-02", w.expiresAt)
	if err != nil {
		return fmt.Errorf("expiresAt must be YYYY-MM-DD")
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if expires.Before(today) {
		return fmt.Errorf("releaseWaiver expired")
	}
	if _, err := os.Stat(w.evidencePath); err != nil {
		return fmt.Errorf("evidencePath not readable")
	}
	return nil
}

func printList(name string, values []string) {
	fmt.Printf("\n%s (%d):\n", name, len(values))
	for _, value := range values {
		fmt.Printf("  %s\n", value)
	}
}

func scanMarkers(job scanJob) map[string]bool {
	ids := map[string]bool{}
	_ = filepath.WalkDir(job.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !hasSuffix(path, job.suffixes) {
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

func parseRegistry(path string) (targets []string, scenarios []scenario, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading registry: %w", err)
	}
	var cur *scenario
	inWaiver := false
	flush := func() error {
		if cur == nil {
			return nil
		}
		if cur.id == "" || len(cur.applies) == 0 {
			return fmt.Errorf("scenario %q missing id or applies", cur.id)
		}
		scenarios = append(scenarios, *cur)
		cur = nil
		return nil
	}
	for _, raw := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(raw, " \t"))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
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
			cur = &scenario{id: strings.TrimSpace(trimmed[len("- id:"):])}
			inWaiver = false
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "applies:"):
			cur.applies = parseFlowList(trimmed[len("applies:"):])
			inWaiver = false
		case strings.HasPrefix(trimmed, "tier:"):
			cur.tier = strings.Trim(strings.TrimSpace(trimmed[len("tier:"):]), `"`)
			inWaiver = false
		case strings.HasPrefix(trimmed, "blocked:"):
			cur.blocked = strings.Trim(strings.TrimSpace(trimmed[len("blocked:"):]), `"`)
			inWaiver = false
		case strings.HasPrefix(trimmed, "releaseWaiver:"):
			inWaiver = true
		case inWaiver && strings.HasPrefix(trimmed, "ticket:"):
			cur.releaseWaiver.ticket = strings.Trim(strings.TrimSpace(trimmed[len("ticket:"):]), `"`)
		case inWaiver && strings.HasPrefix(trimmed, "owner:"):
			cur.releaseWaiver.owner = strings.Trim(strings.TrimSpace(trimmed[len("owner:"):]), `"`)
		case inWaiver && strings.HasPrefix(trimmed, "expiresAt:"):
			cur.releaseWaiver.expiresAt = strings.Trim(strings.TrimSpace(trimmed[len("expiresAt:"):]), `"`)
		case inWaiver && strings.HasPrefix(trimmed, "evidencePath:"):
			cur.releaseWaiver.evidencePath = strings.Trim(strings.TrimSpace(trimmed[len("evidencePath:"):]), `"`)
		case inWaiver && strings.HasPrefix(trimmed, "humanAccepted:"):
			cur.releaseWaiver.humanAccepted = strings.TrimSpace(trimmed[len("humanAccepted:"):]) == "true"
		}
	}
	if ferr := flush(); ferr != nil {
		return nil, nil, ferr
	}
	if len(targets) == 0 {
		return nil, nil, fmt.Errorf("registry has no top-level targets: list")
	}
	if len(scenarios) == 0 {
		return nil, nil, fmt.Errorf("registry has no scenarios")
	}
	return targets, scenarios, nil
}

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

func parseFlowList(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.Trim(strings.TrimSpace(part), `"`); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func hasSuffix(path string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

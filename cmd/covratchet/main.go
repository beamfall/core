// Command covratchet is the per-package line-coverage floor ratchet.
// It runs `go test -coverprofile` across the module, parses per-package coverage
// from the output, and fails if any package drops below its committed floor.
//
// Usage:
//
//	go run ./cmd/covratchet                         # check against testing/coverage-floor.json
//	go run ./cmd/covratchet -check
//	go run ./cmd/covratchet -floor path/to/file.json
//	go run ./cmd/covratchet -seed                   # print a fresh floor file (measure + seed)
//
// Floor file schema (JSON):
//
//	{
//	  "schemaVersion": 1,
//	  "aggregate": 67.2,
//	  "floors": {
//	    "github.com/beamfall/core/internal/foo": 72.5
//	  }
//	}
//
// Only packages that appear in the floor file are checked. Packages not in the
// floor file are reported as informational. The -seed flag measures current
// coverage and writes a floor 3pp below each measured value — use it to
// bootstrap or refresh the floor after intentional coverage changes.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const defaultFloorPath = "testing/coverage-floor.json"
const seedHeadroom = 3.0 // pp below measured when seeding
const aggregateName = "github.com/beamfall/core"
const unlistedPackageLineThreshold = 500

var coverLineRe = regexp.MustCompile(`^ok\s+(\S+)\s+\S+\s+coverage:\s+([\d.]+)%`)
var profileLineRe = regexp.MustCompile(`^(.+):\d+\.\d+,(\d+)\.\d+\s+\d+\s+\d+$`)

type floorFile struct {
	SchemaVersion int                `json:"schemaVersion"`
	Aggregate     *float64           `json:"aggregate,omitempty"`
	Floors        map[string]float64 `json:"floors"`
}

type measurement struct {
	Aggregate float64
	Packages  map[string]float64
	Lines     map[string]int
}

func main() {
	floorPath := flag.String("floor", defaultFloorPath, "path to coverage-floor.json")
	checkFlag := flag.Bool("check", false, "check coverage floors and exit non-zero on regression")
	seed := flag.Bool("seed", false, "measure current coverage and print a seeded floor file")
	flag.Parse()
	_ = checkFlag // Checking is the default; -check is kept for roadmap/CI callers.

	measured, err := measureCoverage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "covratchet: %v\n", err)
		os.Exit(1)
	}

	if *seed {
		printSeed(measured)
		return
	}

	floors, err := loadFloors(*floorPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "covratchet: %v\n", err)
		os.Exit(1)
	}

	os.Exit(check(measured, floors))
}

type coverageRunner func(profile string) ([]byte, error)

// measureCoverage runs `go test -coverprofile` and returns per-package coverage.
func measureCoverage() (*measurement, error) {
	return measureCoverageWith(func(profile string) ([]byte, error) {
		cmd := exec.Command("go", "test", "-coverprofile="+profile, "./...")
		return cmd.CombinedOutput()
	})
}

func measureCoverageWith(run coverageRunner) (*measurement, error) {
	tmp, err := os.CreateTemp("", "covratchet-*.out")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	out, err := run(tmp.Name())
	results, parseErr := parseCoverage(out)
	if err != nil {
		return &measurement{Packages: results}, fmt.Errorf("go test -coverprofile ./... failed: %w\n%s", err, out)
	}
	aggregate, lines, err := parseCoverageProfile(tmp.Name())
	if err != nil {
		return nil, err
	}
	return &measurement{Aggregate: aggregate, Packages: results, Lines: lines}, parseErr
}

func parseCoverage(out []byte) (map[string]float64, error) {
	results := map[string]float64{}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		m := coverLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		pkg := m[1]
		pct, _ := strconv.ParseFloat(m[2], 64)
		results[pkg] = pct
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse go test output: %w", err)
	}
	return results, nil
}

func parseCoverageProfile(path string) (float64, map[string]int, error) {
	// #nosec G304 -- path is the process-created temp profile from measureCoverageWith.
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, fmt.Errorf("read coverage profile: %w", err)
	}
	defer f.Close()

	var total, covered float64
	files := map[string]int{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		match := profileLineRe.FindStringSubmatch(line)
		if match != nil {
			end, _ := strconv.Atoi(match[2])
			if end > files[match[1]] {
				files[match[1]] = end
			}
		}
		if strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return 0, nil, fmt.Errorf("parse coverage profile line %q", line)
		}
		stmts, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return 0, nil, fmt.Errorf("parse coverage statements %q: %w", fields[1], err)
		}
		count, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			return 0, nil, fmt.Errorf("parse coverage count %q: %w", fields[2], err)
		}
		total += stmts
		if count > 0 {
			covered += stmts
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, nil, fmt.Errorf("parse coverage profile: %w", err)
	}
	if total == 0 {
		return 0, nil, fmt.Errorf("coverage profile has no statements")
	}

	packages := map[string]int{}
	for file, lines := range files {
		separator := strings.LastIndex(file, "/")
		if separator < 0 {
			continue
		}
		// Coverage profile paths are module import paths. Trim only the filename.
		pkg := file[:separator]
		packages[pkg] += lines
	}
	return covered / total * 100, packages, nil
}

func loadFloors(path string) (*floorFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read floor file %s: %w", path, err)
	}
	var f floorFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse floor file: %w", err)
	}
	if f.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported schemaVersion %d (want 1)", f.SchemaVersion)
	}
	return &f, nil
}

// check compares measured coverage against floors. Returns 0 on pass, 1 on fail.
func check(measured *measurement, floors *floorFile) int {
	type result struct {
		pkg     string
		floor   float64
		actual  float64
		missing bool
		fail    bool
	}

	var results []result
	var failures int
	checks := map[string]float64{}

	if floors.Aggregate != nil {
		checks[aggregateName] = *floors.Aggregate
	}
	for pkg := range floors.Floors {
		checks[pkg] = floors.Floors[pkg]
	}

	pkgs := make([]string, 0, len(checks))
	for pkg := range checks {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)

	for _, pkg := range pkgs {
		floor := checks[pkg]
		actual, ok := measured.Packages[pkg]
		if pkg == aggregateName {
			actual, ok = measured.Aggregate, true
		}
		r := result{pkg: pkg, floor: floor}
		if !ok {
			r.missing = true
			r.fail = true
			failures++
			results = append(results, r)
			continue
		}
		r.actual = actual
		if actual < floor {
			r.fail = true
			failures++
		}
		results = append(results, r)
	}

	for _, r := range results {
		if r.missing {
			fmt.Printf("FAIL  %-70s floor=%.1f%% (not measured)\n", shortPkg(r.pkg), r.floor)
		} else if r.fail {
			fmt.Printf("FAIL  %-70s measured=%.1f%% floor=%.1f%% (Δ=%.1f%%)\n",
				shortPkg(r.pkg), r.actual, r.floor, r.actual-r.floor)
		} else {
			fmt.Printf("PASS  %-70s measured=%.1f%% floor=%.1f%%\n",
				shortPkg(r.pkg), r.actual, r.floor)
		}
	}

	unlisted := make([]string, 0)
	for pkg, lines := range measured.Lines {
		if _, listed := floors.Floors[pkg]; !listed && lines >= unlistedPackageLineThreshold {
			unlisted = append(unlisted, pkg)
		}
	}
	sort.Strings(unlisted)
	for _, pkg := range unlisted {
		fmt.Printf("INFO  %-70s source_lines=%d (unlisted; threshold=%d)\n",
			shortPkg(pkg), measured.Lines[pkg], unlistedPackageLineThreshold)
	}

	if failures > 0 {
		fmt.Fprintf(os.Stderr, "\ncovratchet: %d package(s) below coverage floor\n", failures)
		fmt.Fprintf(os.Stderr, "To raise the floor after adding tests: update testing/coverage-floor.json\n")
		fmt.Fprintf(os.Stderr, "To lower a floor intentionally: edit testing/coverage-floor.json with justification in commit message\n")
		return 1
	}
	fmt.Printf("\ncovratchet: all %d checked packages at or above floor\n", len(results))
	return 0
}

func printSeed(measured *measurement) {
	floors := make(map[string]float64, len(measured.Packages))
	for pkg, pct := range measured.Packages {
		floor := pct - seedHeadroom
		if floor < 0 {
			floor = 0
		}
		// Round to 1 decimal place
		floors[pkg] = float64(int(floor*10)) / 10
	}
	aggregate := measured.Aggregate - seedHeadroom
	if aggregate < 0 {
		aggregate = 0
	}
	aggregate = float64(int(aggregate*10)) / 10
	f := floorFile{SchemaVersion: 1, Aggregate: &aggregate, Floors: floors}
	data, _ := json.MarshalIndent(f, "", "  ")
	fmt.Println(string(data))
}

func shortPkg(pkg string) string {
	// Strip module prefix for display
	const prefix = "github.com/beamfall/core/"
	return strings.TrimPrefix(pkg, prefix)
}

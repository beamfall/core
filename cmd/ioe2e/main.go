// Command ioe2e is the I/O-coverage ledger — the I/O-axis sibling of
// cmd/featurecheck (the feature-axis tool). It is the F3 reconciliation slice
// of the I/O E2E test track: it cross-references three coverage axes and reports
// which I/O operations are proven by tests.
//
//  1. CODE-DERIVED axis — every EXPORTED method on *ffmpeg.Runner, enumerated
//     live from internal/ffmpeg via go/packages (the methods span ffmpeg.go,
//     hardening.go, and skipdetect.go, so enumeration is at PACKAGE granularity,
//     never a single-file AST parse). Derived live, never hardcoded, so renames
//     stay safe.
//  2. MARKER axis — `// io:<id>` comments in test sources (mirror of
//     featurecheck's `feature:<id>` markers). A marker credits its op as covered.
//  3. LEDGER axis — testing/io-ops.yaml, the hand-maintained egress + filesystem
//     ops that are NOT code-derivable (ffmpeg ops are never listed there).
//
// Reconciliation / fail-closed rule: every exported *Runner method must EITHER
// have a matching `// io:<id>` marker OR be excluded with a `//io:ignore`
// directive near the method (config setters use this). A method that is neither
// is a FAILURE in strict mode. Likewise every ledger op must have a marker.
//
//	go run ./cmd/ioe2e list                 # report-only union of all ops
//	go run ./cmd/ioe2e check                # gate; non-zero on any uncovered op
//	go run ./cmd/ioe2e release-check j.xml   # fail if a marked op's test didn't PASS
//
// Exit codes (F3/F4 hardening #4): 0 ok · 1 a check failed · 2 usage/IO error.
// stdout carries the report; stderr carries diagnostics. This tool MUST NOT
// import any internal/* package (F3 #5) — it reads internal/ffmpeg by PATH via
// go/packages, so there is no import cycle and the no-internal-import lint holds.
package main

import (
	_ "embed"
	"encoding/xml"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	ledgerPath    = "testing/io-ops.yaml"
	ffmpegPkgPath = "./internal/ffmpeg"
	// egressLintRoots is the production tree scanned for raw http.Client
	// constructions (the D2 silent-gap backstop). Egress lives under internal/;
	// test files are excluded (go/packages NeedSyntax loads non-test files only).
	egressLintRoots = "./internal/..."
)

// io:<id> marker in test sources; //io:ignore directive near a *Runner method.
var (
	markerRE   = regexp.MustCompile(`io:([a-z0-9-]+)`)
	ignoreRE   = regexp.MustCompile(`//\s*io:ignore`)
	ignoreIDRE = regexp.MustCompile(`//\s*io:ignore\s+([a-z0-9-]+)`)
)

// scanRoots are the source trees scanned for `// io:<id>` markers. featurecheck
// scans client repos too; the I/O ops here are all server-side, so the core
// tree is the assessable surface. A root absent on disk is simply skipped.
var scanRoots = []struct {
	dir string
	ext string
}{
	{dir: "internal", ext: "_test.go"},
	{dir: "cmd", ext: "_test.go"},
}

// op is one I/O operation on the union axis.
type op struct {
	id      string
	desc    string
	pkg     string
	kind    string // "ffmpeg" | "egress" | "fs"
	ignore  bool   // ffmpeg method excluded via //io:ignore
	blocked string // ledger op with a documented, accepted coverage gap (reason); empty = expected covered
}

type status struct {
	covered bool
	ignored bool
	blocked bool
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "list":
		os.Exit(runList())
	case "check":
		os.Exit(runCheck())
	case "egress-lint":
		os.Exit(runEgressLint())
	case "release-ids":
		os.Exit(runReleaseIDs())
	case "release-packages":
		os.Exit(runReleasePackages())
	case "release-check":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "ioe2e: release-check requires a junit.xml path")
			os.Exit(2)
		}
		os.Exit(runReleaseCheck(os.Args[2]))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ioe2e <list|check|egress-lint|release-ids|release-packages|release-check <junit.xml>>")
}

// buildUnion assembles the full op list (ffmpeg + ledger) and the marker set,
// then resolves covered/ignored status for each op. Returns the ops in stable
// order plus their statuses keyed by id.
func buildUnion() ([]op, map[string]status, error) {
	ffmpegOps, err := deriveFFmpegOps()
	if err != nil {
		return nil, nil, err
	}
	ledgerOps, err := parseLedger(ledgerPath)
	if err != nil {
		return nil, nil, err
	}
	ops := append(ffmpegOps, ledgerOps...)

	markers, ignoreIDs := scanMarkers()
	statuses := map[string]status{}
	for _, o := range ops {
		// An op is ignored if its ffmpeg method carries a //io:ignore doc comment
		// (o.ignore, set in deriveFFmpegOps) OR a test source declares
		// `//io:ignore <id>`. The latter keeps the directive scannable from test
		// sources when the method's own package cannot be edited.
		statuses[o.id] = status{covered: markers[o.id], ignored: o.ignore || ignoreIDs[o.id], blocked: o.blocked != ""}
	}
	return ops, statuses, nil
}

func runList() int {
	ops, statuses, err := buildUnion()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ioe2e:", err)
		return 2
	}

	byKind := map[string][]op{}
	for _, o := range ops {
		byKind[o.kind] = append(byKind[o.kind], o)
	}
	var covered, missing, ignored, blocked int
	for _, o := range ops {
		st := statuses[o.id]
		switch {
		case st.covered:
			covered++
		case st.ignored:
			ignored++
		case st.blocked:
			blocked++
		default:
			missing++
		}
	}

	fmt.Printf("io-coverage ledger — %d io-ops (covered=%d missing=%d ignored=%d blocked=%d)\n",
		len(ops), covered, missing, ignored, blocked)
	fmt.Printf("axes: ffmpeg=%d (code-derived) egress=%d fs=%d (ledger %s)\n\n",
		len(byKind["ffmpeg"]), len(byKind["egress"]), len(byKind["fs"]), ledgerPath)

	for _, kind := range []string{"ffmpeg", "egress", "fs"} {
		group := byKind[kind]
		if len(group) == 0 {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].id < group[j].id })
		fmt.Printf("%s (%d):\n", strings.ToUpper(kind), len(group))
		for _, o := range group {
			fmt.Printf("  %-4s io:%-34s %s\n", statusTag(statuses[o.id]), o.id, o.desc)
		}
		fmt.Println()
	}
	return 0
}

func runCheck() int {
	ops, statuses, err := buildUnion()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ioe2e:", err)
		return 2
	}
	var uncovered, blockedList []string
	for _, o := range ops {
		st := statuses[o.id]
		if st.ignored || st.covered {
			continue
		}
		if st.blocked {
			blockedList = append(blockedList, fmt.Sprintf("io:%s — %s", o.id, o.blocked))
			continue
		}
		uncovered = append(uncovered, fmt.Sprintf("io:%s (%s, %s)", o.id, o.kind, o.pkg))
	}
	sort.Strings(uncovered)
	sort.Strings(blockedList)

	// Blocked ops are a documented, accepted coverage gap — surfaced (never
	// silent) but not a failure. They keep `check` green so it can gate while
	// the residual work is tracked in lifecycle_fs_blocked_test.go.
	if len(blockedList) > 0 {
		fmt.Printf("ioe2e check: %d blocked io-op(s) (documented gaps, not failing):\n", len(blockedList))
		for _, b := range blockedList {
			fmt.Printf("  blk %s\n", b)
		}
		fmt.Println()
	}

	if len(uncovered) == 0 {
		fmt.Printf("ioe2e check: OK — all %d io-op(s) covered, ignored, or documented-blocked\n", len(ops))
		return 0
	}
	fmt.Printf("ioe2e check: FAIL — %d uncovered io-op(s):\n", len(uncovered))
	for _, u := range uncovered {
		fmt.Printf("  --  %s\n", u)
	}
	fmt.Fprintf(os.Stderr, "ioe2e: %d io-op(s) lack a `// io:<id>` marker (and are not //io:ignore)\n", len(uncovered))
	return 1
}

// releaseInventory is the committed floor of io ids that a release must prove,
// one `id<TAB>package` row per id, generated by `ioe2e release-ids`. Every other
// expected set in this tool is derived from sources that shrink when their input
// shrinks — deleting a `// io:<id>` marker would silently delete the obligation.
// A tracked file cannot shrink at run time: retiring an id takes a reviewed commit.
//
//go:embed release-ids.tsv
var releaseInventoryTSV string

// releaseMarker is one release id's marker declaration: the Test* functions
// carrying it and the repo-relative package that declares them.
type releaseMarker struct {
	testNames []string
	pkg       string
}

// parseReleaseInventory reads the embedded floor. Rows are `id<TAB>package`.
// Exactly two fields are required and a duplicate id is rejected outright — a
// second row could otherwise silently rebind an id to another package.
func parseReleaseInventory(text string) (map[string]string, error) {
	inventory := map[string]string{}
	for n, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			return nil, fmt.Errorf("release-ids.tsv line %d: want `id<TAB>package`, got %q", n+1, line)
		}
		id, pkg := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
		if id == "" || pkg == "" {
			return nil, fmt.Errorf("release-ids.tsv line %d: empty id or package", n+1)
		}
		if _, dup := inventory[id]; dup {
			return nil, fmt.Errorf("release-ids.tsv line %d: duplicate id %q", n+1, id)
		}
		inventory[id] = pkg
	}
	if len(inventory) == 0 {
		return nil, fmt.Errorf("release-ids.tsv is empty; a release must prove at least one io id")
	}
	return inventory, nil
}

// modulePath reads the module path from go.mod so JUnit suite names (which are
// full import paths) can be compared against the inventory's repo-relative ones.
func modulePath() (string, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", fmt.Errorf("reading go.mod: %w", err)
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
	}
	return "", fmt.Errorf("go.mod declares no module path")
}

func runReleaseIDs() int {
	markerTests, err := scanReleaseMarkerTests()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ioe2e:", err)
		return 2
	}
	var ids []string
	for id := range markerTests {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Printf("%s\t%s\n", id, markerTests[id].pkg)
	}
	return 0
}

// runReleasePackages prints the go test package arguments the release JUnit
// producer must run: the union of the committed inventory's packages and the
// package declaring every currently discovered marker. Deriving it here keeps
// producer scope and proof obligation from drifting apart — a marker in a new
// package is executed without an inventory edit, while the inventory stays the
// floor that cannot shrink.
func runReleasePackages() int {
	inventory, err := parseReleaseInventory(releaseInventoryTSV)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ioe2e:", err)
		return 2
	}
	markerTests, err := scanReleaseMarkerTests()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ioe2e:", err)
		return 2
	}
	for _, pkg := range releasePackages(inventory, markerTests) {
		fmt.Println(pkg)
	}
	return 0
}

func releasePackages(inventory map[string]string, markerTests map[string]releaseMarker) []string {
	seen := map[string]bool{}
	for _, pkg := range inventory {
		seen[pkg] = true
	}
	for _, marker := range markerTests {
		seen[marker.pkg] = true
	}
	var pkgs []string
	for pkg := range seen {
		pkgs = append(pkgs, "./"+pkg)
	}
	sort.Strings(pkgs)
	return pkgs
}

// runReleaseCheck reads a gotestsum/JUnit XML file and fails if any io id whose
// marker is present did not actually PASS in the run (F3 #3: "executed, not just
// marked"). This intentionally checks marker-only journey ids too, not just
// ffmpeg + ledger ops.
//
// Non-vacuity rules: an empty id set is a failure, not a
// pass; scan errors are surfaced instead of silently yielding zero markers; the
// committed inventory is a floor the derived set must cover; and a Go-declared
// marker is proven only by its own named test in its own package, so a
// same-named or id-containing test elsewhere cannot stand in for it.
func runReleaseCheck(path string) int {
	ops, statuses, err := buildUnion()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ioe2e:", err)
		return 2
	}
	cases, err := parseJUnit(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ioe2e:", err)
		return 2
	}
	markerTests, err := scanReleaseMarkerTests()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ioe2e:", err)
		return 2
	}
	inventory, err := parseReleaseInventory(releaseInventoryTSV)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ioe2e:", err)
		return 2
	}
	module, err := modulePath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ioe2e:", err)
		return 2
	}

	markers := releaseMarkerIDs(markerTests)
	_, ignoreIDs := scanMarkers()
	ids := releaseCheckIDs(ops, statuses, markers, ignoreIDs)

	if floor := checkReleaseFloor(inventory, markerTests, ids, ignoreIDs); len(floor) > 0 {
		fmt.Printf("ioe2e release-check: FAIL — %d pinned release id(s) unproven:\n", len(floor))
		for _, f := range floor {
			fmt.Printf("  --  %s\n", f)
		}
		fmt.Fprintf(os.Stderr, "ioe2e: %d pinned id(s) in cmd/ioe2e/release-ids.tsv are no longer discoverable\n", len(floor))
		return 1
	}
	if len(ids) == 0 {
		fmt.Println("ioe2e release-check: FAIL — no marked io ids discovered; a release proves nothing without them")
		fmt.Fprintln(os.Stderr, "ioe2e: zero marked io ids is a failure, not a pass")
		return 1
	}

	// For each marked, non-ignored id, find testcases that name it and require at
	// least one to have passed.
	var failures []string
	for _, id := range ids {
		var named, passed int
		for _, tc := range cases {
			if !testcaseMatchesMarker(tc, id, markerTests[id], module) {
				continue
			}
			named++
			if tc.passed() {
				passed++
			}
		}
		switch {
		case named == 0:
			failures = append(failures, fmt.Sprintf("io:%s — marked but no testcase names it in %s", id, path))
		case passed == 0:
			failures = append(failures, fmt.Sprintf("io:%s — %d testcase(s) found, none PASSED", id, named))
		}
	}
	sort.Strings(failures)

	if len(failures) == 0 {
		fmt.Printf("ioe2e release-check: OK — %d marked io id(s) executed and PASSED\n", len(ids))
		return 0
	}
	fmt.Printf("ioe2e release-check: FAIL — %d marked io id(s) not proven PASSED:\n", len(failures))
	for _, f := range failures {
		fmt.Printf("  --  %s\n", f)
	}
	fmt.Fprintf(os.Stderr, "ioe2e: %d marked io id(s) were not proven to pass\n", len(failures))
	return 1
}

// checkReleaseFloor reports every way the committed inventory is no longer
// honoured: a pinned id that has lost its marker, one retired through an
// `//io:ignore <id>` directive, one dropped from the verified set, and one whose
// marker moved to a package the inventory does not name.
func checkReleaseFloor(inventory map[string]string, markerTests map[string]releaseMarker, ids []string, ignoreIDs map[string]bool) []string {
	verified := map[string]bool{}
	for _, id := range ids {
		verified[id] = true
	}
	var problems []string
	for id, pkg := range inventory {
		marker, found := markerTests[id]
		switch {
		case !found:
			problems = append(problems, fmt.Sprintf("io:%s — pinned in release-ids.tsv but no `// io:` marker declares it", id))
		case ignoreIDs[id]:
			problems = append(problems, fmt.Sprintf("io:%s — pinned in release-ids.tsv but retired by an //io:ignore directive", id))
		case marker.pkg != pkg:
			problems = append(problems, fmt.Sprintf("io:%s — marker moved from %s to %s; update cmd/ioe2e/release-ids.tsv", id, pkg, marker.pkg))
		case !verified[id]:
			problems = append(problems, fmt.Sprintf("io:%s — pinned in release-ids.tsv but excluded from the verified set", id))
		}
	}
	sort.Strings(problems)
	return problems
}

func releaseMarkerIDs(markerTests map[string]releaseMarker) map[string]bool {
	ids := map[string]bool{}
	for id := range markerTests {
		ids[id] = true
	}
	return ids
}

func releaseCheckIDs(ops []op, statuses map[string]status, markers, ignoreIDs map[string]bool) []string {
	ignored := map[string]bool{}
	for id := range ignoreIDs {
		ignored[id] = true
	}
	for _, o := range ops {
		if statuses[o.id].ignored {
			ignored[o.id] = true
		}
	}

	var ids []string
	for id := range markers {
		if ignored[id] {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// testcaseMatchesMarker binds a testcase to an id. An id whose marker is declared
// in Go source is proven ONLY by its own declared test (or a subtest of it) in
// its own package: the JUnit merges several packages, so a same-named — or merely
// id-containing — passing test elsewhere would otherwise mask the real failure.
// The name-substring fallback survives only for ids with no Go declaration, the
// non-Go JUnit case it was written for.
func testcaseMatchesMarker(tc junitCase, id string, marker releaseMarker, module string) bool {
	if len(marker.testNames) == 0 {
		return nameMatchesOp(tc.Name, id)
	}
	if !suiteIsPackage(tc.Suite, module, marker.pkg) {
		return false
	}
	for _, testName := range marker.testNames {
		if tc.Name == testName || strings.HasPrefix(tc.Name, testName+"/") {
			return true
		}
	}
	return false
}

// suiteIsPackage compares a JUnit suite name (a full import path, emitted by
// go-junit-report's gojson parser) against a repo-relative package. Matching is
// exact: a same-named package in another module must not satisfy the obligation.
func suiteIsPackage(suite, module, pkg string) bool {
	return suite == module+"/"+pkg
}

// scanReleaseMarkerTests maps each io id to the Test* functions that declare it
// and the package declaring them. Every failure is returned rather than skipped:
// a missing scan root, an unwalkable tree, an unreadable file, or a Go file that
// will not parse used to yield silently fewer markers — and fewer markers meant
// a smaller, easier release obligation.
func scanReleaseMarkerTests() (map[string]releaseMarker, error) {
	markerTests := map[string]map[string]bool{}
	markerPkg := map[string]string{}
	for _, root := range scanRoots {
		if _, err := os.Stat(root.dir); err != nil {
			return nil, fmt.Errorf("release marker scan root %s: %w", root.dir, err)
		}
		walkErr := filepath.WalkDir(root.dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("walking %s: %w", path, err)
			}
			if d.IsDir() || !strings.HasSuffix(path, root.ext) {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return fmt.Errorf("reading %s: %w", path, readErr)
			}
			if !releaseCheckFile(data) {
				return nil
			}
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, data, parser.ParseComments)
			if parseErr != nil {
				return fmt.Errorf("parsing %s: %w", path, parseErr)
			}
			collectMarkerTests(file, filepath.ToSlash(filepath.Dir(path)), markerTests, markerPkg)
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	out := map[string]releaseMarker{}
	for id, names := range markerTests {
		marker := releaseMarker{pkg: markerPkg[id]}
		for name := range names {
			marker.testNames = append(marker.testNames, name)
		}
		sort.Strings(marker.testNames)
		out[id] = marker
	}
	return out, nil
}

// collectMarkerTests records every `// io:<id>` marker on a Test* doc comment in
// one parsed file, together with the package directory that declares it.
func collectMarkerTests(file *ast.File, pkgDir string, markerTests map[string]map[string]bool, markerPkg map[string]string) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Doc == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
			continue
		}
		for _, c := range fn.Doc.List {
			for _, m := range markerRE.FindAllStringSubmatch(c.Text, -1) {
				id := m[1]
				if id == "ignore" {
					continue
				}
				if markerTests[id] == nil {
					markerTests[id] = map[string]bool{}
				}
				markerTests[id][fn.Name.Name] = true
				markerPkg[id] = pkgDir
			}
		}
	}
}

func releaseCheckFile(data []byte) bool {
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "//go:build") {
			return strings.Contains(line, "io_e2e")
		}
		if strings.HasPrefix(line, "//") {
			continue
		}
		return true
	}
	return true
}

// ── D2 egress silent-gap linter ─────────────────────────────────────────────
//
// The ledger reconciles DECLARED egress ops (testing/io-ops.yaml) against test
// markers, but it cannot see a brand-new raw `http.Client` someone adds in a
// package that has no egress op yet — a *silent* egress gap. egress-lint closes
// that hole: it AST-scans the production internal/ tree for raw http.Client
// composite-literal constructions (the real client, not a *http.Client seam) and
// fails on any whose owning package has no egress op in the ledger and no
// `//io:ignore` directive at the site. Escape hatches: register the op in the
// ledger (the intended path for a real egress) or `//io:ignore` it (a non-egress
// client, e.g. a loopback/health probe).

// clientSite is one raw http.Client{...} construction in production code.
type clientSite struct {
	pkgPath string // full import path, e.g. github.com/beamfall/core/internal/scrape
	file    string
	line    int
	ignored bool // a //io:ignore comment sits on the literal's line or the line above
}

// shortPkg reduces a full import path to the repo-relative ledger form
// (github.com/beamfall/core/internal/scrape -> internal/scrape) so it can be
// matched against the ledger's pkg: field.
func (s clientSite) shortPkg() string {
	if i := strings.Index(s.pkgPath, "/internal/"); i >= 0 {
		return s.pkgPath[i+1:]
	}
	return s.pkgPath
}

// classifyEgressSites partitions client sites into accounted (covered by an
// egress ledger pkg, or //io:ignore'd) and silent gaps. Pure for testability.
func classifyEgressSites(sites []clientSite, egressPkgs map[string]bool) (accounted int, gaps []string) {
	for _, s := range sites {
		switch {
		case s.ignored:
			accounted++
		case egressPkgs[s.shortPkg()]:
			accounted++
		default:
			gaps = append(gaps, fmt.Sprintf("%s:%d — raw http.Client in %s has no egress op in %s and no //io:ignore",
				relPath(s.file), s.line, s.shortPkg(), ledgerPath))
		}
	}
	sort.Strings(gaps)
	return accounted, gaps
}

func runEgressLint() int {
	ledgerOps, err := parseLedger(ledgerPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ioe2e:", err)
		return 2
	}
	egressPkgs := map[string]bool{}
	for _, o := range ledgerOps {
		if o.kind == "egress" {
			egressPkgs[o.pkg] = true
		}
	}
	sites, err := scanEgressClients()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ioe2e:", err)
		return 2
	}
	accounted, gaps := classifyEgressSites(sites, egressPkgs)
	if len(gaps) == 0 {
		fmt.Printf("ioe2e egress-lint: OK — %d raw http.Client site(s), all covered by an egress ledger op or //io:ignore\n", accounted)
		return 0
	}
	fmt.Printf("ioe2e egress-lint: FAIL — %d silent egress gap(s):\n", len(gaps))
	for _, g := range gaps {
		fmt.Printf("  --  %s\n", g)
	}
	fmt.Fprintf(os.Stderr, "ioe2e: %d raw egress http.Client declaration(s) not in %s (add an egress op or //io:ignore)\n", len(gaps), ledgerPath)
	return 1
}

// scanEgressClients AST-loads the production internal/ packages and returns every
// raw `http.Client{...}` / `&http.Client{...}` composite-literal site. A *http.Client
// field, parameter, or return type is NOT a construction and is not reported — only
// the literal that actually builds a client. A site is flagged ignored when a
// //io:ignore comment is on its line or the line directly above.
func scanEgressClients() ([]clientSite, error) {
	fset := token.NewFileSet()
	cfg := &packages.Config{
		Fset: fset,
		Mode: packages.NeedName | packages.NeedSyntax | packages.NeedFiles | packages.NeedCompiledGoFiles,
	}
	pkgs, err := packages.Load(cfg, egressLintRoots)
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", egressLintRoots, err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		return nil, fmt.Errorf("load errors scanning %s (see stderr above)", egressLintRoots)
	}
	var sites []clientSite
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			ignoreLines := map[int]bool{}
			for _, cg := range file.Comments {
				for _, c := range cg.List {
					if ignoreRE.MatchString(c.Text) {
						ignoreLines[fset.Position(c.Slash).Line] = true
					}
				}
			}
			ast.Inspect(file, func(n ast.Node) bool {
				cl, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				sel, ok := cl.Type.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				x, ok := sel.X.(*ast.Ident)
				if !ok || x.Name != "http" || sel.Sel.Name != "Client" {
					return true
				}
				pos := fset.Position(cl.Pos())
				sites = append(sites, clientSite{
					pkgPath: pkg.PkgPath,
					file:    pos.Filename,
					line:    pos.Line,
					ignored: ignoreLines[pos.Line] || ignoreLines[pos.Line-1],
				})
				return true
			})
		}
	}
	return sites, nil
}

// relPath makes an absolute source path repo-relative for readable output,
// falling back to the input when the cwd cannot be determined.
func relPath(p string) string {
	wd, err := os.Getwd()
	if err != nil {
		return p
	}
	if rel, err := filepath.Rel(wd, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

func statusTag(st status) string {
	switch {
	case st.covered:
		return "ok"
	case st.ignored:
		return "ign"
	case st.blocked:
		return "blk"
	default:
		return "--"
	}
}

// deriveFFmpegOps loads internal/ffmpeg by PATH (no internal import) and returns
// one op per exported method on *ffmpeg.Runner. Loading uses NeedSyntax|NeedTypes
// |NeedDeps so the whole package (all files) is type-checked and every method is
// enumerated — a single-file AST parse would silently undercount. The op id is
// the method name lowercased into the dashed scheme (e.g. Probe -> ffmpeg-probe,
// FrameGrab -> ffmpeg-frame-grab). A method carrying a //io:ignore directive in
// its doc/line comment is marked ignore (config setters).
func deriveFFmpegOps() ([]op, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedDeps | packages.NeedFiles,
	}
	pkgs, err := packages.Load(cfg, ffmpegPkgPath)
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", ffmpegPkgPath, err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		return nil, fmt.Errorf("type errors loading %s (see stderr above)", ffmpegPkgPath)
	}
	if len(pkgs) != 1 {
		return nil, fmt.Errorf("expected exactly 1 package at %s, got %d", ffmpegPkgPath, len(pkgs))
	}
	pkg := pkgs[0]

	runner := pkg.Types.Scope().Lookup("Runner")
	if runner == nil {
		return nil, fmt.Errorf("type Runner not found in %s", ffmpegPkgPath)
	}
	named, ok := runner.Type().(*types.Named)
	if !ok {
		return nil, fmt.Errorf("Runner is not a named type")
	}

	ignored := ignoredMethods(pkg)

	var ops []op
	for i := 0; i < named.NumMethods(); i++ {
		m := named.Method(i)
		if !m.Exported() {
			continue
		}
		ops = append(ops, op{
			id:     "ffmpeg-" + dashed(m.Name()),
			desc:   "ffmpeg *Runner." + m.Name(),
			pkg:    "internal/ffmpeg",
			kind:   "ffmpeg",
			ignore: ignored[m.Name()],
		})
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("no exported *Runner methods derived from %s", ffmpegPkgPath)
	}
	return ops, nil
}

// ignoredMethods returns the set of *Runner method names carrying a //io:ignore
// directive in their doc comment or trailing line comment.
func ignoredMethods(pkg *packages.Package) map[string]bool {
	ignored := map[string]bool{}
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !isRunnerReceiver(fn.Recv) {
				continue
			}
			// Scan raw comment lines, not fn.Doc.Text(): the latter strips
			// //directive-form comments, so the idiomatic //io:ignore (no
			// space after //) would be invisible.
			if fn.Doc != nil {
				for _, c := range fn.Doc.List {
					if ignoreRE.MatchString(c.Text) {
						ignored[fn.Name.Name] = true
						break
					}
				}
			}
		}
	}
	return ignored
}

// isRunnerReceiver reports whether a receiver field list is (r *Runner) / (Runner).
func isRunnerReceiver(recv *ast.FieldList) bool {
	if recv == nil || len(recv.List) != 1 {
		return false
	}
	t := recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	id, ok := t.(*ast.Ident)
	return ok && id.Name == "Runner"
}

// dashed turns a CamelCase method name into dashed-lowercase: FrameGrab ->
// frame-grab, StartHLS -> start-hls, ExtractSubtitleToWebVTT ->
// extract-subtitle-to-web-vtt. Runs of capitals (HLS, VTT) collapse to one word.
func dashed(name string) string {
	var b strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		isUpper := r >= 'A' && r <= 'Z'
		if isUpper && i > 0 {
			prev := runes[i-1]
			prevUpper := prev >= 'A' && prev <= 'Z'
			nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
			// boundary: lower→Upper, or Upper→Upper followed by lower (HLSSeg -> hls-seg)
			if !prevUpper || nextLower {
				b.WriteByte('-')
			}
		}
		if isUpper {
			b.WriteRune(r - 'A' + 'a')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// scanMarkers walks the scan roots and returns (1) the set of io ids covered by a
// `// io:<id>` marker and (2) the set of ids excluded by a `//io:ignore <id>`
// directive, in any file whose name ends with the root's ext. The literal token
// `io:ignore` is the ignore directive, never a covering marker.
func scanMarkers() (covered, ignored map[string]bool) {
	covered, ignored = map[string]bool{}, map[string]bool{}
	for _, root := range scanRoots {
		if _, err := os.Stat(root.dir); err != nil {
			continue
		}
		_ = filepath.WalkDir(root.dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, root.ext) {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			for _, m := range ignoreIDRE.FindAllSubmatch(data, -1) {
				ignored[string(m[1])] = true
			}
			for _, m := range markerRE.FindAllSubmatch(data, -1) {
				id := string(m[1])
				if id == "ignore" {
					continue // the ignore directive, not a coverage marker
				}
				covered[id] = true
			}
			return nil
		})
	}
	return covered, ignored
}

// parseLedger reads the egress + fs ops from testing/io-ops.yaml. Stdlib-only,
// mirroring featurecheck's minimal parser: a top-level `ops:` list whose entries
// are `- id:` / `desc:` / `pkg:` / `kind:` plus an optional `blocked:` reason
// (a documented, accepted coverage gap — `check` reports it but does not fail).
// Strict: an entry missing id/kind, or a kind that is not egress|fs, is an error
// (ffmpeg ops must not appear here).
func parseLedger(path string) ([]op, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading ledger: %w", err)
	}
	var ops []op
	var cur *op
	flush := func() error {
		if cur == nil {
			return nil
		}
		if cur.id == "" || cur.kind == "" {
			return fmt.Errorf("ledger op %q missing id or kind", cur.id)
		}
		if cur.kind != "egress" && cur.kind != "fs" {
			return fmt.Errorf("ledger op %q has kind %q; only egress|fs allowed (ffmpeg ops are code-derived)", cur.id, cur.kind)
		}
		ops = append(ops, *cur)
		cur = nil
		return nil
	}
	for _, raw := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(raw, " \t"))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "- id:"):
			if ferr := flush(); ferr != nil {
				return nil, ferr
			}
			cur = &op{id: scalar(trimmed[len("- id:"):]), kind: ""}
		case cur != nil && strings.HasPrefix(trimmed, "desc:"):
			cur.desc = scalar(trimmed[len("desc:"):])
		case cur != nil && strings.HasPrefix(trimmed, "pkg:"):
			cur.pkg = scalar(trimmed[len("pkg:"):])
		case cur != nil && strings.HasPrefix(trimmed, "kind:"):
			cur.kind = scalar(trimmed[len("kind:"):])
		case cur != nil && strings.HasPrefix(trimmed, "blocked:"):
			cur.blocked = scalar(trimmed[len("blocked:"):])
		}
	}
	if ferr := flush(); ferr != nil {
		return nil, ferr
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("ledger %s has no ops", path)
	}
	return ops, nil
}

// scalar trims a YAML scalar value of surrounding space and optional quotes.
func scalar(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "\"")
	s = strings.TrimSuffix(s, "\"")
	return strings.TrimSpace(s)
}

// ── JUnit XML (gotestsum) parsing ──────────────────────────────────────────

type junitSuites struct {
	XMLName xml.Name     `xml:"testsuites"`
	Suites  []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	XMLName xml.Name    `xml:"testsuite"`
	Name    string      `xml:"name,attr"`
	Cases   []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name string `xml:"name,attr"`
	// Suite is the owning package's import path, carried down from the
	// enclosing <testsuite>. Without it a merged multi-package report cannot say
	// which package proved an id.
	Suite   string
	Failure *junitFailure `xml:"failure"`
	Error   *junitFailure `xml:"error"`
	Skipped *junitFailure `xml:"skipped"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
}

func (c junitCase) passed() bool {
	return c.Failure == nil && c.Error == nil && c.Skipped == nil
}

// parseJUnit reads a JUnit XML file. gotestsum emits either a root <testsuites>
// or a single <testsuite>; handle both. Returns the flat list of testcases.
func parseJUnit(path string) ([]junitCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading junit: %w", err)
	}
	var suites junitSuites
	if err := xml.Unmarshal(data, &suites); err == nil && len(suites.Suites) > 0 {
		var cases []junitCase
		for _, s := range suites.Suites {
			cases = append(cases, withSuite(s)...)
		}
		return cases, nil
	}
	var single junitSuite
	if err := xml.Unmarshal(data, &single); err != nil {
		return nil, fmt.Errorf("parsing junit %s: %w", path, err)
	}
	return withSuite(single), nil
}

// withSuite stamps each testcase with its enclosing suite name so package
// identity survives the flattening into a single case list.
func withSuite(suite junitSuite) []junitCase {
	cases := make([]junitCase, 0, len(suite.Cases))
	for _, tc := range suite.Cases {
		tc.Suite = suite.Name
		cases = append(cases, tc)
	}
	return cases
}

// nameMatchesOp reports whether a testcase name carries an op id. It matches an
// explicit `io:<id>` token, or the bare id with dashes or underscores (test
// names cannot contain dashes in Go identifiers, so `ffmpeg_probe` is accepted
// as a stand-in for `ffmpeg-probe`).
func nameMatchesOp(testName, id string) bool {
	lower := strings.ToLower(testName)
	if strings.Contains(lower, "io:"+id) {
		return true
	}
	if strings.Contains(lower, id) {
		return true
	}
	return strings.Contains(lower, strings.ReplaceAll(id, "-", "_"))
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestClassifyEgressSites covers the D2 silent-gap accounting: a raw http.Client
// is accepted when its package owns an egress ledger op OR the site is
// //io:ignore'd, and is reported as a silent gap otherwise.
func TestClassifyEgressSites(t *testing.T) {
	egressPkgs := map[string]bool{
		"internal/scrape":      true,
		"internal/updatecheck": true,
	}
	sites := []clientSite{
		{pkgPath: "github.com/beamfall/core/internal/scrape", file: "internal/scrape/scrape.go", line: 210},                       // documented → accounted
		{pkgPath: "github.com/beamfall/core/internal/updatecheck", file: "internal/updatecheck/updatecheck.go"},                   // documented → accounted
		{pkgPath: "github.com/beamfall/core/internal/loopbackprobe", file: "internal/loopbackprobe/p.go", line: 9, ignored: true}, // ignored → accounted
		{pkgPath: "github.com/beamfall/core/internal/newegress", file: "internal/newegress/x.go", line: 42},                       // silent gap → fail
	}

	accounted, gaps := classifyEgressSites(sites, egressPkgs)
	if accounted != 3 {
		t.Errorf("accounted = %d, want 3 (2 documented + 1 ignored)", accounted)
	}
	if len(gaps) != 1 {
		t.Fatalf("gaps = %d, want 1 silent gap; got %v", len(gaps), gaps)
	}
	if !strings.Contains(gaps[0], "internal/newegress") || !strings.Contains(gaps[0], ":42") {
		t.Errorf("gap message = %q, want it to name internal/newegress:42", gaps[0])
	}
}

// TestClassifyEgressSitesAllAccounted proves a clean tree (every site documented
// or ignored) yields zero gaps — the green path the gate relies on.
func TestClassifyEgressSitesAllAccounted(t *testing.T) {
	egressPkgs := map[string]bool{"internal/scrape": true}
	sites := []clientSite{
		{pkgPath: "github.com/beamfall/core/internal/scrape", file: "internal/scrape/scrape.go", line: 210},
		{pkgPath: "github.com/beamfall/core/internal/x", file: "internal/x/x.go", line: 5, ignored: true},
	}
	if accounted, gaps := classifyEgressSites(sites, egressPkgs); len(gaps) != 0 || accounted != 2 {
		t.Errorf("clean tree: accounted=%d gaps=%v, want accounted=2 gaps=none", accounted, gaps)
	}
}

// TestShortPkg verifies the import-path → ledger-pkg reduction.
func TestShortPkg(t *testing.T) {
	got := clientSite{pkgPath: "github.com/beamfall/core/internal/scrape"}.shortPkg()
	if got != "internal/scrape" {
		t.Errorf("shortPkg = %q, want internal/scrape", got)
	}
}

func TestReleaseCheckIDsIncludesMarkerOnlyJourneyIDs(t *testing.T) {
	ops := []op{
		{id: "fs-trash-move"},
		{id: "ffmpeg-probe"},
		{id: "ffmpeg-set-binary-path", ignore: true},
	}
	statuses := map[string]status{
		"fs-trash-move":          {covered: true},
		"ffmpeg-probe":           {covered: true},
		"ffmpeg-set-binary-path": {covered: true, ignored: true},
	}
	markers := map[string]bool{
		"audited-delete-bypass":  true,
		"ffmpeg-probe":           true,
		"ffmpeg-set-binary-path": true,
		"ignored-marker":         true,
	}
	ignoreIDs := map[string]bool{"ignored-marker": true}

	got := releaseCheckIDs(ops, statuses, markers, ignoreIDs)
	want := []string{"audited-delete-bypass", "ffmpeg-probe"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("releaseCheckIDs = %v, want %v", got, want)
	}
}

// ── RELGATE-NONVAC-4: release-check must not pass vacuously ────────────────

func writeMarkerTree(t *testing.T, dir, pkg, file, body string) {
	t.Helper()
	full := filepath.Join(dir, pkg)
	if err := os.MkdirAll(full, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, file), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const goodMarkerFile = `package sample

import "testing"

// io:sample-op
func TestSample(t *testing.T) {}
`

// withScanRoots points the release scanner at a temp tree. runReleaseCheck reads
// the ffmpeg and ledger axes before the marker scan, so damaging the real
// checkout would fail somewhere else entirely and never reach these paths.
func withScanRoots(t *testing.T, dir string) {
	t.Helper()
	saved := scanRoots
	scanRoots = []struct {
		dir string
		ext string
	}{{dir: filepath.Join(dir, "internal"), ext: "_test.go"}}
	t.Cleanup(func() { scanRoots = saved })
}

func TestScanReleaseMarkerTestsSurfacesScanFailures(t *testing.T) {
	t.Run("missing scan root", func(t *testing.T) {
		withScanRoots(t, t.TempDir())
		_, err := scanReleaseMarkerTests()
		if err == nil {
			t.Fatal("a missing scan root must be an error, not zero markers")
		}
		if !strings.Contains(err.Error(), "scan root") {
			t.Fatalf("error should name the scan root, got %v", err)
		}
	})

	t.Run("unparseable source", func(t *testing.T) {
		dir := t.TempDir()
		writeMarkerTree(t, dir, "internal/sample", "broken_test.go", "package sample\n// io:sample-op\nfunc {{{\n")
		withScanRoots(t, dir)
		_, err := scanReleaseMarkerTests()
		if err == nil {
			t.Fatal("a Go file that will not parse must be an error, not a silently skipped marker")
		}
	})

	t.Run("unreadable source", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("mode 0000 does not stop root")
		}
		dir := t.TempDir()
		writeMarkerTree(t, dir, "internal/sample", "sample_test.go", goodMarkerFile)
		if err := os.Chmod(filepath.Join(dir, "internal/sample/sample_test.go"), 0o000); err != nil {
			t.Fatal(err)
		}
		withScanRoots(t, dir)
		if _, err := scanReleaseMarkerTests(); err == nil {
			t.Fatal("an unreadable test source must be an error, not a silently skipped marker")
		}
	})

	t.Run("healthy tree", func(t *testing.T) {
		dir := t.TempDir()
		writeMarkerTree(t, dir, "internal/sample", "sample_test.go", goodMarkerFile)
		withScanRoots(t, dir)
		markers, err := scanReleaseMarkerTests()
		if err != nil {
			t.Fatalf("healthy tree: %v", err)
		}
		got := markers["sample-op"]
		if len(got.testNames) != 1 || got.testNames[0] != "TestSample" {
			t.Fatalf("testNames = %v, want [TestSample]", got.testNames)
		}
		if !strings.HasSuffix(got.pkg, "internal/sample") {
			t.Fatalf("pkg = %q, want it to name the declaring package", got.pkg)
		}
	})
}

func TestParseReleaseInventoryRejectsShrinkableFiles(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"empty", "# only comments\n"},
		{"one field", "sample-op\n"},
		{"three fields", "sample-op\tinternal/sample\textra\n"},
		{"empty package", "sample-op\t\n"},
		{"duplicate id", "sample-op\tinternal/sample\nsample-op\tinternal/other\n"},
		{"identical duplicate", "sample-op\tinternal/sample\nsample-op\tinternal/sample\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseReleaseInventory(tc.text); err == nil {
				t.Fatalf("parseReleaseInventory(%q) must fail", tc.text)
			}
		})
	}
	got, err := parseReleaseInventory("# c\nsample-op\tinternal/sample\n")
	if err != nil {
		t.Fatalf("valid inventory: %v", err)
	}
	if got["sample-op"] != "internal/sample" {
		t.Fatalf("inventory = %v", got)
	}
}

func TestCheckReleaseFloorCatchesEveryShrink(t *testing.T) {
	inventory := map[string]string{"sample-op": "internal/sample"}
	markers := map[string]releaseMarker{"sample-op": {testNames: []string{"TestSample"}, pkg: "internal/sample"}}

	if got := checkReleaseFloor(inventory, markers, []string{"sample-op"}, nil); len(got) != 0 {
		t.Fatalf("honoured floor reported problems: %v", got)
	}
	for _, tc := range []struct {
		name    string
		markers map[string]releaseMarker
		ids     []string
		ignore  map[string]bool
		want    string
	}{
		{"marker deleted", map[string]releaseMarker{}, nil, nil, "no `// io:` marker"},
		{"marker ignored", markers, []string{"sample-op"}, map[string]bool{"sample-op": true}, "//io:ignore"},
		{"marker moved", map[string]releaseMarker{"sample-op": {testNames: []string{"TestSample"}, pkg: "internal/elsewhere"}}, []string{"sample-op"}, nil, "marker moved"},
		{"dropped from verified set", markers, nil, nil, "excluded from the verified set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkReleaseFloor(inventory, tc.markers, tc.ids, tc.ignore)
			if len(got) != 1 || !strings.Contains(got[0], tc.want) {
				t.Fatalf("checkReleaseFloor = %v, want one problem containing %q", got, tc.want)
			}
		})
	}
}

func TestTestcaseMatchesMarkerRefusesCrossPackageStandIns(t *testing.T) {
	const module = "github.com/beamfall/core"
	marker := releaseMarker{testNames: []string{"TestOwner"}, pkg: "internal/owner"}
	owner := module + "/internal/owner"
	other := module + "/internal/other"

	for _, tc := range []struct {
		name string
		tc   junitCase
		want bool
	}{
		{"declared test in its own package", junitCase{Name: "TestOwner", Suite: owner}, true},
		{"subtest of the declared test", junitCase{Name: "TestOwner/case", Suite: owner}, true},
		{"same name in another package", junitCase{Name: "TestOwner", Suite: other}, false},
		{"same name with no suite at all", junitCase{Name: "TestOwner"}, false},
		{"id-containing name in another package", junitCase{Name: "TestSampleOpThing", Suite: other}, false},
		{"id-containing name in its own package", junitCase{Name: "TestSampleOpThing", Suite: owner}, false},
		{"same package name in another module", junitCase{Name: "TestOwner", Suite: "example.com/x/internal/owner"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := testcaseMatchesMarker(tc.tc, "sample-op", marker, module); got != tc.want {
				t.Fatalf("testcaseMatchesMarker = %v, want %v", got, tc.want)
			}
		})
	}

	// An id with no Go declaration keeps the substring fallback it was written for.
	undeclared := junitCase{Name: "io:sample-op smoke"}
	if !testcaseMatchesMarker(undeclared, "sample-op", releaseMarker{}, module) {
		t.Fatal("ids without a Go marker test must keep the non-Go JUnit fallback")
	}
}

func TestParseJUnitKeepsSuiteIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.xml")
	body := `<testsuites><testsuite name="github.com/beamfall/core/internal/owner">` +
		`<testcase name="TestOwner"/></testsuite>` +
		`<testsuite name="github.com/beamfall/core/internal/other">` +
		`<testcase name="TestOwner"><failure message="boom"/></testcase></testsuite></testsuites>`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cases, err := parseJUnit(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Fatalf("got %d cases, want 2", len(cases))
	}
	if cases[0].Suite != "github.com/beamfall/core/internal/owner" || cases[1].Suite != "github.com/beamfall/core/internal/other" {
		t.Fatalf("suite identity lost: %+v", cases)
	}
}

func TestReleasePackagesUnionsInventoryAndDiscoveredMarkers(t *testing.T) {
	inventory := map[string]string{"a": "internal/one", "b": "internal/two"}
	markers := map[string]releaseMarker{"b": {pkg: "internal/two"}, "c": {pkg: "internal/three"}}
	got := releasePackages(inventory, markers)
	want := []string{"./internal/one", "./internal/three", "./internal/two"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("releasePackages = %v, want %v", got, want)
	}
}

// The committed inventory must describe the checkout it ships with: a pinned id
// whose marker is gone, ignored, or relocated has to red the release check.
func TestCommittedReleaseInventoryMatchesThisCheckout(t *testing.T) {
	inventory, err := parseReleaseInventory(releaseInventoryTSV)
	if err != nil {
		t.Fatalf("cmd/ioe2e/release-ids.tsv: %v", err)
	}
	if len(inventory) == 0 {
		t.Fatal("the committed release inventory is empty")
	}
	root := repoRootForTest(t)
	t.Chdir(root)
	markers, err := scanReleaseMarkerTests()
	if err != nil {
		t.Fatalf("scanning markers: %v", err)
	}
	_, ignoreIDs := scanMarkers()
	var ids []string
	for id := range markers {
		ids = append(ids, id)
	}
	if problems := checkReleaseFloor(inventory, markers, ids, ignoreIDs); len(problems) > 0 {
		t.Fatalf("committed inventory no longer matches the tree: %v", problems)
	}
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test working directory")
		}
		dir = parent
	}
}

// runReleaseCheck itself must fail on the two inputs that used to yield a clean
// exit 0: an unscannable tree, and a tree that yields no markers at all.
func TestRunReleaseCheckRefusesVacuousTrees(t *testing.T) {
	root := repoRootForTest(t)
	junit := filepath.Join(t.TempDir(), "j.xml")
	body := `<testsuites><testsuite name="x"><testcase name="TestNothing"/></testsuite></testsuites>`
	if err := os.WriteFile(junit, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("unscannable tree exits 2", func(t *testing.T) {
		t.Chdir(root)
		withScanRoots(t, t.TempDir()) // no internal/ inside
		if got := runReleaseCheck(junit); got != 2 {
			t.Fatalf("runReleaseCheck = %d, want 2 for an unscannable tree", got)
		}
	})

	t.Run("markerless tree fails the pinned floor", func(t *testing.T) {
		empty := t.TempDir()
		if err := os.MkdirAll(filepath.Join(empty, "internal"), 0o750); err != nil {
			t.Fatal(err)
		}
		t.Chdir(root)
		withScanRoots(t, empty)
		if got := runReleaseCheck(junit); got != 1 {
			t.Fatalf("runReleaseCheck = %d, want 1 when no marker survives", got)
		}
	})
}

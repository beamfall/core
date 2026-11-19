package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRegistryRequiresCanonicalFeatureStatus(t *testing.T) {
	withScanJobs(t, []scanJob{
		{dir: "server", ext: "_test.go", targets: []string{"server"}},
		{dir: "web", ext: ".test.ts", targets: []string{"web"}},
	})
	path := writeRegistry(t, `
schemaVersion: 1
targets: [server, web]
features:
  - id: search-keyword
    area: browse
    summary: Search the library by keyword.
    adr: ["0011"]
    applies: [server]
    status: shipped
`)

	_, features, err := parseRegistry(path)
	if err != nil {
		t.Fatalf("parseRegistry() error = %v", err)
	}
	if len(features) != 1 || features[0].status != "shipped" {
		t.Fatalf("features = %#v, want shipped status", features)
	}
}

func TestParseRegistryRejectsAmbiguousFeatureStatus(t *testing.T) {
	withScanJobs(t, []scanJob{{dir: "server", ext: "_test.go", targets: []string{"server"}}})
	path := writeRegistry(t, `
schemaVersion: 1
targets: [server]
features:
  - id: search-keyword
    area: browse
    summary: Search the library by keyword.
    adr: ["0011"]
    applies: [server]
    status: tested
`)

	_, _, err := parseRegistry(path)
	if err == nil || !strings.Contains(err.Error(), `invalid status "tested"`) {
		t.Fatalf("parseRegistry() error = %v, want invalid status", err)
	}
}

func TestParseRegistryRejectsInvalidRegistryShape(t *testing.T) {
	const valid = `
schemaVersion: 1
targets: [server, web]
features:
  - id: search-keyword
    area: browse
    summary: Search the library by keyword.
    adr: ["0011"]
    applies: [server]
    status: shipped
`
	tests := []struct {
		name     string
		registry string
		want     string
	}{
		{name: "wrong schema version", registry: strings.Replace(valid, "schemaVersion: 1", "schemaVersion: 2", 1), want: "schemaVersion must be 1"},
		{name: "missing id", registry: strings.Replace(valid, "id: search-keyword", "id:", 1), want: "missing required field(s): id"},
		{name: "missing area", registry: strings.Replace(valid, "area: browse\n", "", 1), want: "missing required field(s): area"},
		{name: "missing summary", registry: strings.Replace(valid, "summary: Search the library by keyword.\n", "", 1), want: "missing required field(s): summary"},
		{name: "empty adr", registry: strings.Replace(valid, `adr: ["0011"]`, "adr: []", 1), want: "missing required field(s): adr"},
		{name: "empty applies", registry: strings.Replace(valid, "applies: [server]", "applies: []", 1), want: "missing required field(s): applies"},
		{name: "missing status", registry: strings.Replace(valid, "status: shipped\n", "", 1), want: "missing required field(s): status"},
		{name: "duplicate id", registry: valid + strings.TrimPrefix(valid[strings.Index(valid, "  - id:"):], ""), want: `duplicate feature id "search-keyword"`},
		{name: "unknown top-level target", registry: strings.Replace(valid, "targets: [server, web]", "targets: [server, web, bogus]", 1), want: "unknown: bogus"},
		{name: "unknown applies only", registry: strings.Replace(valid, "applies: [server]", "applies: [bogus]", 1), want: `applies to unknown target "bogus"`},
		{name: "duplicate top-level target", registry: strings.Replace(valid, "targets: [server, web]", "targets: [server, web, web]", 1), want: `duplicate top-level target "web"`},
		{name: "duplicate applies target", registry: strings.Replace(valid, "applies: [server]", "applies: [server, server]", 1), want: `duplicate applies target "server"`},
		{name: "invalid status", registry: strings.Replace(valid, "status: shipped", "status: tested", 1), want: `invalid status "tested"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withScanJobs(t, []scanJob{
				{dir: "server", ext: "_test.go", targets: []string{"server"}},
				{dir: "web", ext: ".test.ts", targets: []string{"web"}},
			})
			_, _, err := parseRegistry(writeRegistry(t, tt.registry))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseRegistry() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestStrictAreas(t *testing.T) {
	tests := []struct {
		name         string
		strict       bool
		strictAreas  string
		ratchet      bool
		wantCode     int
		wantStdout   []string
		wantStderr   []string
		absentStderr []string
		exactOutput  bool
	}{
		{
			name:        "unset preserves default output",
			wantCode:    0,
			exactOutput: true,
		},
		{
			name:        "comma separated areas fail for selected missing cells",
			strictAreas: " api, console ",
			wantCode:    1,
			wantStderr: []string{
				"featurecheck: 1 uncovered cell(s) in strict area(s) api, console — strict area mode failing:",
				"console-missing @ server",
			},
			absentStderr: []string{"console-unassessable @ web"},
		},
		{
			name:         "fully proven area passes despite other missing cells",
			strictAreas:  "api",
			wantCode:     0,
			absentStderr: []string{"strict area mode failing"},
		},
		{
			name:        "unknown area fails and lists valid areas",
			strictAreas: "consoel",
			wantCode:    2,
			wantStderr: []string{
				`featurecheck: unknown strict area(s) "consoel"; valid areas: api, console`,
			},
		},
		{
			name:        "ratchet and strict areas both run when both fail",
			strictAreas: "console",
			ratchet:     true,
			wantCode:    1,
			wantStderr: []string{
				"coverage ratchet failed:",
				"server proven=1 below ratchet minProven=2",
				"strict area(s) console",
				"console-missing @ server",
			},
			absentStderr: []string{"console-unassessable @ web"},
		},
		{
			name:     "global strict retains existing early failure",
			strict:   true,
			ratchet:  true,
			wantCode: 1,
			wantStderr: []string{
				"featurecheck: 1 uncovered cell(s) — strict mode failing",
			},
			absentStderr: []string{
				"coverage ratchet failed",
				"strict area mode failing",
				"console-unassessable @ web",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ratchetPath := setupFeaturecheckFixture(t)
			if !tt.ratchet {
				ratchetPath = ""
			}
			var stdout, stderr bytes.Buffer
			code := run(runOptions{
				strict:      tt.strict,
				strictAreas: tt.strictAreas,
				ratchetPath: ratchetPath,
			}, &stdout, &stderr)

			if code != tt.wantCode {
				t.Fatalf("run() code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, stdout.String(), stderr.String())
			}
			if tt.exactOutput {
				const want = `feature-coverage ledger — 3 features x 2 targets
targets present: server, web(absent)
applicable cells: 3 | proven: 1 | missing: 1 | unassessable (target root absent): 1

PER-TARGET:
  server: applicable=2 proven=1 missing=1 unassessable=0
  web (unassessable): applicable=1 proven=0 missing=0 unassessable=1

PER-SURFACE DECISIONS:
  web (unassessable): supported=0 planned=1 unsupported=0 undecided=2 unticketed=0

PROVEN (1):
  ok  api-proven @ server

MISSING (1 applicable, assessable cells with no proof):
  --  console-missing @ server
`
				if stdout.String() != want || stderr.String() != "" {
					t.Fatalf("default output changed\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
				}
			}
			for _, want := range tt.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout missing %q:\n%s", want, stdout.String())
				}
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr missing %q:\n%s", want, stderr.String())
				}
			}
			for _, absent := range tt.absentStderr {
				if strings.Contains(stderr.String(), absent) {
					t.Errorf("stderr unexpectedly contains %q:\n%s", absent, stderr.String())
				}
			}
		})
	}
}

func TestParseRegistryReadsSurfaceDecisions(t *testing.T) {
	withScanJobs(t, decisionScanJobs)
	_, features, err := parseRegistry(writeRegistry(t, decisionRegistry))
	if err != nil {
		t.Fatalf("parseRegistry() error = %v", err)
	}
	f := features[0]
	if got := f.unsupported["androidtv"]; got != "Remote-only input: joins by phone handoff. (owner 2026-11-18)" {
		t.Errorf("unsupported[androidtv] = %q", got)
	}
	if got := f.unsupported["iphone"]; got != "Covered by the iPad layout." {
		t.Errorf("unsupported[iphone] = %q", got)
	}
	if got := f.tickets["tvos"]; got != "CORE-2700" {
		t.Errorf("tickets[tvos] = %q", got)
	}
	if got, want := f.release, map[string]string{"web": "1.0", "tvos": "1.1"}; len(got) != 2 || got["web"] != want["web"] || got["tvos"] != want["tvos"] {
		t.Errorf("release = %#v, want %#v", got, want)
	}
	if f.status != "partial" {
		t.Errorf("status after blocks = %q, want partial", f.status)
	}
	if features[1].unsupported != nil || features[1].tickets != nil || features[1].release != nil {
		t.Errorf("second feature inherited blocks: %#v", features[1])
	}
}

func TestParseRegistryRejectsInvalidSurfaceDecisions(t *testing.T) {
	tests := []struct {
		name string
		from string
		to   string
		want string
	}{
		{name: "unsupported surface also applies", from: "      iphone: Covered by the iPad layout.", to: "      web: Not for web.", want: `both in applies and unsupported for "web"`},
		{name: "empty reason", from: "      iphone: Covered by the iPad layout.", to: "      iphone:", want: `unsupported "iphone" has no reason`},
		{name: "unknown surface", from: "      iphone: Covered by the iPad layout.", to: "      fridge: No screen.", want: `names "fridge", which is not a surface`},
		{name: "server is not a surface", from: "      tvos: CORE-2700", to: "      server: CORE-2700", want: `names "server", which is not a surface`},
		{name: "ticket outside applies", from: "      tvos: CORE-2700", to: "      androidtv: CORE-2700", want: `ticket for "androidtv", which is not in applies`},
		{name: "malformed ticket", from: "      tvos: CORE-2700", to: "      tvos: soon", want: `ticket "soon" for "tvos" is not a ticket ID`},
		{name: "release outside applies", from: `      tvos: "1.1"`, to: `      androidtv: "1.1"`, want: `release for "androidtv", which is not in applies`},
		{name: "malformed release", from: `      tvos: "1.1"`, to: `      tvos: next`, want: `release "next" for "tvos" is not a version`},
		{name: "duplicate surface in block", from: `      tvos: "1.1"`, to: "      tvos: \"1.1\"\n      tvos: \"1.2\"", want: `duplicate release surface "tvos"`},
		{name: "inline block value", from: "    tickets:\n      tvos: CORE-2700", to: "    tickets: {tvos: CORE-2700}", want: "tickets must be a block"},
		{name: "repeated block", from: "    tickets:\n      tvos: CORE-2700", to: "    tickets:\n      tvos: CORE-2700\n    tickets:\n      ipad: CORE-2701", want: "more than one tickets block"},
		{name: "empty block", from: "    tickets:\n      tvos: CORE-2700", to: "    tickets:", want: "empty tickets block"},
		{name: "entry without colon", from: "      tvos: CORE-2700", to: "      tvos CORE-2700", want: "is not `surface: value`"},
		{name: "full-parity exclusion without owner decision", from: "(owner 2026-11-18)", to: "(team call)", want: `"androidtv" is a full-parity surface`},
		{name: "fullParity names server", from: "fullParity: [androidtv]", to: "fullParity: [server]", want: `fullParity names "server"`},
		{name: "fullParity duplicate", from: "fullParity: [androidtv]", to: "fullParity: [androidtv, androidtv]", want: `duplicate fullParity surface "androidtv"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(decisionRegistry, tt.from) {
				t.Fatalf("fixture lacks %q", tt.from)
			}
			withScanJobs(t, decisionScanJobs)
			_, _, err := parseRegistry(writeRegistry(t, strings.Replace(decisionRegistry, tt.from, tt.to, 1)))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseRegistry() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestSurfaceDecisionsReleaseGapsAndRatchet(t *testing.T) {
	setup := func(t *testing.T, ratchet string) string {
		t.Helper()
		t.Chdir(t.TempDir())
		for _, dir := range []string{"testing", "internal", "web", "apple"} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(registryPath, []byte(strings.TrimSpace(decisionRegistry)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		// web proves both features; nothing proves them on tvOS or iPad.
		if err := os.WriteFile(filepath.Join("web", "party.test.ts"), []byte("// feature:watch-party\n// feature:search-keyword\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("testing", "ratchet.json")
		if err := os.WriteFile(path, []byte(ratchet), 0o600); err != nil {
			t.Fatal(err)
		}
		withScanJobs(t, []scanJob{
			{dir: "internal", ext: "_test.go", targets: []string{"server"}},
			{dir: "web", ext: ".test.ts", targets: []string{"web"}},
			{dir: "apple", ext: ".swift", targets: []string{"tvos", "iphone", "ipad"}},
			{dir: "absent-android", ext: ".kt", targets: []string{"androidtv"}},
		})
		return path
	}
	const tightRatchet = `{"schemaVersion":1,"targets":{"web":{"minProven":1,"maxUndecided":0,"maxUnticketed":0},"ipad":{"minProven":0,"maxUndecided":1,"maxUnticketed":1},"androidtv":{"minProven":0,"maxUndecided":1,"maxUnticketed":0}}}`
	tests := []struct {
		name       string
		options    runOptions
		ratchet    string
		wantCode   int
		wantStdout []string
		wantStderr []string
		absent     []string
	}{
		{
			name:     "decision counts per surface",
			wantCode: 0,
			wantStdout: []string{
				"  web: supported=2 planned=0 unsupported=0 undecided=0 unticketed=0",
				"  tvos: supported=0 planned=2 unsupported=0 undecided=0 unticketed=1",
				"  ipad: supported=0 planned=1 unsupported=0 undecided=1 unticketed=1",
				"  iphone: supported=0 planned=0 unsupported=1 undecided=1 unticketed=0",
				"  androidtv (unassessable): supported=0 planned=0 unsupported=1 undecided=1 unticketed=0",
			},
		},
		{
			name:     "release gate passes for a proven surface",
			options:  runOptions{release: "1.0"},
			wantCode: 0,
			absent:   []string{"release gate failing"},
		},
		{
			name:     "release gate fails for an unproven targeted surface",
			options:  runOptions{release: "1.1"},
			wantCode: 1,
			wantStderr: []string{
				"1 cell(s) targeted at release <= 1.1 are not proven — release gate failing:",
				"watch-party @ tvos (release 1.1)",
			},
		},
		{
			name:       "release compares numerically",
			options:    runOptions{release: "1.10"},
			wantCode:   1,
			wantStderr: []string{"watch-party @ tvos (release 1.1)"},
		},
		{
			name:       "malformed release flag",
			options:    runOptions{release: "one"},
			wantCode:   2,
			wantStderr: []string{`-release "one" is not a version`},
		},
		{
			name:     "gaps group unticketed planned cells",
			options:  runOptions{gaps: true},
			wantCode: 0,
			wantStdout: []string{
				"GAPS (2 planned, unproven cell(s) with no ticket, by surface/area):",
				"  tvos/browse (1): search-keyword",
				"  ipad/browse (1): search-keyword",
			},
		},
		{
			name:     "ratchet at current values passes",
			ratchet:  tightRatchet,
			wantCode: 0,
			wantStdout: []string{
				"RATCHET ok web: undecided=0 maxUndecided=0",
				"RATCHET ok ipad: unticketed=1 maxUnticketed=1",
				"RATCHET ok androidtv: undecided=1 maxUndecided=1",
				"RATCHET skip androidtv: target root absent",
			},
		},
		{
			name:     "ratchet fails when undecided or unticketed grows",
			ratchet:  strings.Replace(strings.Replace(tightRatchet, `"maxUndecided":1`, `"maxUndecided":0`, 1), `"maxUnticketed":1`, `"maxUnticketed":0`, 1),
			wantCode: 1,
			wantStderr: []string{
				"ipad undecided=1 above ratchet maxUndecided=0",
				"ipad unticketed=1 above ratchet maxUnticketed=0",
			},
		},
		{
			name:       "server takes no decision ceilings",
			ratchet:    `{"schemaVersion":1,"targets":{"server":{"minProven":0,"maxUndecided":0}}}`,
			wantCode:   1,
			wantStderr: []string{"server is not a surface"},
		},
		{
			name:       "negative ceiling",
			ratchet:    `{"schemaVersion":1,"targets":{"web":{"minProven":0,"maxUnticketed":-1}}}`,
			wantCode:   1,
			wantStderr: []string{"web maxUnticketed is negative"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ratchetPath := setup(t, tt.ratchet)
			if tt.ratchet != "" {
				tt.options.ratchetPath = ratchetPath
			}
			var stdout, stderr bytes.Buffer
			code := run(tt.options, &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("run() code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, stdout.String(), stderr.String())
			}
			for _, want := range tt.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout missing %q:\n%s", want, stdout.String())
				}
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr missing %q:\n%s", want, stderr.String())
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(stderr.String(), absent) {
					t.Errorf("stderr unexpectedly contains %q:\n%s", absent, stderr.String())
				}
			}
		})
	}
}

var decisionScanJobs = []scanJob{
	{dir: "internal", ext: "_test.go", targets: []string{"server"}},
	{dir: "web", ext: ".test.ts", targets: []string{"web"}},
	{dir: "apple", ext: ".swift", targets: []string{"tvos", "iphone", "ipad"}},
	{dir: "android", ext: ".kt", targets: []string{"androidtv"}},
}

// decisionRegistry covers every decision state: watch-party is supported on
// web, planned and ticketed on tvOS, unsupported on androidtv (a full-parity
// surface here, so with an owner decision) and on iphone, and undecided on
// ipad. search-keyword is planned and unticketed on tvOS and iPad.
const decisionRegistry = `
schemaVersion: 1
targets: [server, web, tvos, iphone, ipad, androidtv]
fullParity: [androidtv]
features:
  - id: watch-party
    area: playback
    summary: Synchronised group playback.
    adr: ["0050"]
    applies: [server, web, tvos]
    unsupported:
      androidtv: Remote-only input: joins by phone handoff. (owner 2026-11-18)
      iphone: Covered by the iPad layout.
    tickets:
      tvos: CORE-2700
    release:
      web: "1.0"
      tvos: "1.1"
    status: partial
  - id: search-keyword
    area: browse
    summary: Search the library by keyword.
    adr: ["0011"]
    applies: [server, web, tvos, ipad]
    status: shipped
`

func setupFeaturecheckFixture(t *testing.T) string {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Join("testing"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join("internal"), 0o700); err != nil {
		t.Fatal(err)
	}
	registry := `
schemaVersion: 1
targets: [server, web]
features:
  - id: console-missing
    area: console
    summary: Missing Console proof.
    adr: ["0007"]
    applies: [server]
    status: partial
  - id: api-proven
    area: api
    summary: Proven API behavior.
    adr: ["0007"]
    applies: [server]
    status: shipped
  - id: console-unassessable
    area: console
    summary: Unassessable Web proof.
    adr: ["0007"]
    applies: [web]
    status: planned
`
	if err := os.WriteFile(registryPath, []byte(strings.TrimSpace(registry)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("internal", "proof_test.go"), []byte("// feature:api-proven\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ratchetPath := filepath.Join("testing", "ratchet.json")
	ratchet := `{"schemaVersion":1,"targets":{"server":{"minProven":2}}}` + "\n"
	if err := os.WriteFile(ratchetPath, []byte(ratchet), 0o600); err != nil {
		t.Fatal(err)
	}

	withScanJobs(t, []scanJob{
		{dir: "internal", ext: "_test.go", targets: []string{"server"}},
		{dir: "missing-web-root", ext: ".test.ts", targets: []string{"web"}},
	})
	return ratchetPath
}

func withScanJobs(t *testing.T, jobs []scanJob) {
	t.Helper()
	oldScanJobs := scanJobs
	scanJobs = jobs
	t.Cleanup(func() {
		scanJobs = oldScanJobs
	})
}

func writeRegistry(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "features.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

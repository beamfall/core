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

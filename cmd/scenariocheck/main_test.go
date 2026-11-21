package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenarios.yaml")
	if err := os.WriteFile(path, []byte(`schemaVersion: 1
targets: [server, web]
scenarios:
  - id: rename-flow
    applies: [server, web]
    blocked: "not built yet"
  - id: agent-selector
    applies: [server]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	targets, scenarios, err := parseRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0] != "server" || targets[1] != "web" {
		t.Fatalf("targets = %#v", targets)
	}
	if len(scenarios) != 2 {
		t.Fatalf("len(scenarios) = %d, want 2", len(scenarios))
	}
	if scenarios[0].id != "rename-flow" || scenarios[0].blocked == "" {
		t.Fatalf("first scenario = %#v", scenarios[0])
	}
}

func TestParseRegistryRejectsMissingApplies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenarios.yaml")
	if err := os.WriteFile(path, []byte(`schemaVersion: 1
targets: [server]
scenarios:
  - id: missing-applies
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseRegistry(path); err == nil {
		t.Fatal("parseRegistry accepted scenario without applies")
	}
}

// TestParseRegistryReleaseWaiver verifies that releaseWaiver sub-fields are
// parsed from the YAML into the scenario struct.
func TestParseRegistryReleaseWaiver(t *testing.T) {
	dir := t.TempDir()
	evPath := filepath.Join(dir, "evidence.txt")
	if err := os.WriteFile(evPath, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	yaml := `schemaVersion: 1
targets: [server]
scenarios:
  - id: waiver-scenario
    applies: [server]
    tier: release
    blocked: "blocked for test"
    releaseWaiver:
      ticket: TEST-XCUT-5
      owner: ops@beamfall.io
      expiresAt: "2099-12-31"
      evidencePath: "` + evPath + `"
      humanAccepted: true
`
	path := filepath.Join(dir, "scenarios.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, scenarios, err := parseRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 1 {
		t.Fatalf("len(scenarios) = %d, want 1", len(scenarios))
	}
	w := scenarios[0].releaseWaiver
	if w.ticket != "TEST-XCUT-5" {
		t.Errorf("ticket = %q, want TEST-XCUT-5", w.ticket)
	}
	if w.owner != "ops@beamfall.io" {
		t.Errorf("owner = %q, want ops@beamfall.io", w.owner)
	}
	if w.expiresAt != "2099-12-31" {
		t.Errorf("expiresAt = %q, want 2099-12-31", w.expiresAt)
	}
	if w.evidencePath != evPath {
		t.Errorf("evidencePath = %q, want %q", w.evidencePath, evPath)
	}
	if !w.humanAccepted {
		t.Error("humanAccepted = false, want true")
	}
}

// TestValidReleaseWaiver_Valid confirms a fully-populated, unexpired waiver
// with a readable evidencePath is accepted.
func TestValidReleaseWaiver_Valid(t *testing.T) {
	evPath := filepath.Join(t.TempDir(), "ev.txt")
	if err := os.WriteFile(evPath, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().AddDate(1, 0, 0).Format("2006-01-02")
	w := releaseWaiver{
		ticket:        "TEST-XCUT-5",
		owner:         "ops@beamfall.io",
		expiresAt:     future,
		evidencePath:  evPath,
		humanAccepted: true,
	}
	if err := validReleaseWaiver(w); err != nil {
		t.Errorf("expected nil error for valid waiver, got: %v", err)
	}
}

// TestValidReleaseWaiver_MissingTicket confirms a waiver without a ticket is rejected.
func TestValidReleaseWaiver_MissingTicket(t *testing.T) {
	evPath := filepath.Join(t.TempDir(), "ev.txt")
	if err := os.WriteFile(evPath, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := releaseWaiver{
		owner:         "ops@beamfall.io",
		expiresAt:     "2099-12-31",
		evidencePath:  evPath,
		humanAccepted: true,
	}
	err := validReleaseWaiver(w)
	if err == nil {
		t.Fatal("expected error for missing ticket, got nil")
	}
	if !strings.Contains(err.Error(), "ticket") {
		t.Errorf("error %q should mention 'ticket'", err.Error())
	}
}

// TestValidReleaseWaiver_MissingOwner confirms a waiver without an owner is rejected.
func TestValidReleaseWaiver_MissingOwner(t *testing.T) {
	evPath := filepath.Join(t.TempDir(), "ev.txt")
	if err := os.WriteFile(evPath, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := releaseWaiver{
		ticket:        "TEST-XCUT-5",
		expiresAt:     "2099-12-31",
		evidencePath:  evPath,
		humanAccepted: true,
	}
	err := validReleaseWaiver(w)
	if err == nil {
		t.Fatal("expected error for missing owner, got nil")
	}
	if !strings.Contains(err.Error(), "owner") {
		t.Errorf("error %q should mention 'owner'", err.Error())
	}
}

// TestValidReleaseWaiver_MissingEvidencePath confirms a waiver without an
// evidencePath is rejected.
func TestValidReleaseWaiver_MissingEvidencePath(t *testing.T) {
	w := releaseWaiver{
		ticket:        "TEST-XCUT-5",
		owner:         "ops@beamfall.io",
		expiresAt:     "2099-12-31",
		humanAccepted: true,
	}
	err := validReleaseWaiver(w)
	if err == nil {
		t.Fatal("expected error for missing evidencePath, got nil")
	}
	if !strings.Contains(err.Error(), "evidencePath") {
		t.Errorf("error %q should mention 'evidencePath'", err.Error())
	}
}

// TestValidReleaseWaiver_HumanAcceptedFalse confirms a waiver without human
// acceptance is rejected.
func TestValidReleaseWaiver_HumanAcceptedFalse(t *testing.T) {
	evPath := filepath.Join(t.TempDir(), "ev.txt")
	if err := os.WriteFile(evPath, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := releaseWaiver{
		ticket:        "TEST-XCUT-5",
		owner:         "ops@beamfall.io",
		expiresAt:     "2099-12-31",
		evidencePath:  evPath,
		humanAccepted: false,
	}
	err := validReleaseWaiver(w)
	if err == nil {
		t.Fatal("expected error when humanAccepted=false, got nil")
	}
	if !strings.Contains(err.Error(), "humanAccepted") {
		t.Errorf("error %q should mention 'humanAccepted'", err.Error())
	}
}

// TestValidReleaseWaiver_Expired confirms a waiver whose expiresAt is in the
// past is rejected.
func TestValidReleaseWaiver_Expired(t *testing.T) {
	evPath := filepath.Join(t.TempDir(), "ev.txt")
	if err := os.WriteFile(evPath, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().AddDate(-1, 0, 0).Format("2006-01-02")
	w := releaseWaiver{
		ticket:        "TEST-XCUT-5",
		owner:         "ops@beamfall.io",
		expiresAt:     past,
		evidencePath:  evPath,
		humanAccepted: true,
	}
	err := validReleaseWaiver(w)
	if err == nil {
		t.Fatal("expected error for expired waiver, got nil")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error %q should mention 'expired'", err.Error())
	}
}

// TestValidReleaseWaiver_BadDateFormat confirms an unparseable expiresAt is rejected.
func TestValidReleaseWaiver_BadDateFormat(t *testing.T) {
	evPath := filepath.Join(t.TempDir(), "ev.txt")
	if err := os.WriteFile(evPath, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := releaseWaiver{
		ticket:        "TEST-XCUT-5",
		owner:         "ops@beamfall.io",
		expiresAt:     "31-12-2099", // wrong format
		evidencePath:  evPath,
		humanAccepted: true,
	}
	err := validReleaseWaiver(w)
	if err == nil {
		t.Fatal("expected error for bad date format, got nil")
	}
	if !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Errorf("error %q should mention 'YYYY-MM-DD'", err.Error())
	}
}

// TestValidReleaseWaiver_EvidencePathNotReadable confirms a waiver pointing to
// a non-existent evidence file is rejected.
func TestValidReleaseWaiver_EvidencePathNotReadable(t *testing.T) {
	w := releaseWaiver{
		ticket:        "TEST-XCUT-5",
		owner:         "ops@beamfall.io",
		expiresAt:     "2099-12-31",
		evidencePath:  "/nonexistent/path/to/evidence.txt",
		humanAccepted: true,
	}
	err := validReleaseWaiver(w)
	if err == nil {
		t.Fatal("expected error for unreadable evidencePath, got nil")
	}
	if !strings.Contains(err.Error(), "evidencePath") {
		t.Errorf("error %q should mention 'evidencePath'", err.Error())
	}
}

// TestReleaseCheckBlockedWithoutWaiver confirms that a blocked release-tier
// scenario with no releaseWaiver causes the release check to report a failure.
// This is the core gate-enforcement invariant.
func TestReleaseCheckBlockedWithoutWaiver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenarios.yaml")
	if err := os.WriteFile(path, []byte(`schemaVersion: 1
targets: [server]
scenarios:
  - id: unwaived-release
    applies: [server]
    tier: release
    blocked: "journey not yet built"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, scenarios, err := parseRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	// A blocked release scenario without a releaseWaiver must be invalid.
	s := scenarios[0]
	if s.tier != "release" {
		t.Fatalf("tier = %q, want release", s.tier)
	}
	waiverErr := validReleaseWaiver(s.releaseWaiver)
	if waiverErr == nil {
		t.Fatal("expected validReleaseWaiver to return an error for a zero-value waiver, got nil")
	}
}

// TestReleaseCheckBlockedWithValidWaiver confirms that a blocked release-tier
// scenario carrying a complete, unexpired releaseWaiver passes the waiver check.
func TestReleaseCheckBlockedWithValidWaiver(t *testing.T) {
	dir := t.TempDir()
	evPath := filepath.Join(dir, "evidence.txt")
	if err := os.WriteFile(evPath, []byte("human-accepted waiver evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().AddDate(1, 0, 0).Format("2006-01-02")
	yaml := `schemaVersion: 1
targets: [server]
scenarios:
  - id: waived-release
    applies: [server]
    tier: release
    blocked: "non-server targets pending"
    releaseWaiver:
      ticket: TEST-XCUT-5
      owner: ops@beamfall.io
      expiresAt: "` + future + `"
      evidencePath: "` + evPath + `"
      humanAccepted: true
`
	path := filepath.Join(dir, "scenarios.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, scenarios, err := parseRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	s := scenarios[0]
	if waiverErr := validReleaseWaiver(s.releaseWaiver); waiverErr != nil {
		t.Errorf("expected valid waiver to pass, got: %v", waiverErr)
	}
}

// TestScanRootsAcrossTheAppleSplit covers CORE-1514: the closed iPhone, iPad
// and tvOS code moves from beamfall-apple to beamfall-apple-mobile-tv. Before
// the split the old repo still credits those targets; after it they need the
// closed repo, and without it they are unassessable with a SKIP line rather
// than scored against the open half alone.
func TestScanRootsAcrossTheAppleSplit(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		preSplit, closedPresent       bool
		wantTVOS, wantMacOS, wantSkip bool
		wantClosedProof               bool
	}{
		{name: "pre-split layout", preSplit: true, wantTVOS: true, wantMacOS: true},
		{name: "split, closed repo present", closedPresent: true, wantTVOS: true, wantMacOS: true, wantClosedProof: true},
		{name: "split, closed repo absent", wantMacOS: true, wantSkip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			open := filepath.Join(root, "beamfall-apple")
			closed := filepath.Join(root, "beamfall-apple-mobile-tv")
			writeSwift(t, filepath.Join(open, "Sources", "Shared.swift"), "// scenario:open-proof")
			if tc.preSplit {
				writeSwift(t, filepath.Join(open, "Apps", "BeamfallMobile", "App.swift"), "// scenario:closed-proof")
			}
			if tc.closedPresent {
				writeSwift(t, filepath.Join(closed, "Apps", "BeamfallTV", "App.swift"), "// scenario:closed-proof")
			}
			jobs := []scanJob{
				{dir: open, suffixes: []string{".swift"}, targets: []string{"tvos", "macos"}},
				{dir: closed, suffixes: []string{".swift"}, targets: []string{"tvos"}, requiredUnless: filepath.Join(open, "Apps", "BeamfallMobile")},
			}
			var out strings.Builder
			covered, assessable := scanRoots(jobs, &out)
			if assessable["tvos"] != tc.wantTVOS || assessable["macos"] != tc.wantMacOS {
				t.Fatalf("assessable = %v, want tvos=%v macos=%v", assessable, tc.wantTVOS, tc.wantMacOS)
			}
			if skipped := strings.Contains(out.String(), "SKIP "+closed); skipped != tc.wantSkip {
				t.Fatalf("output %q: SKIP line = %v, want %v", out.String(), skipped, tc.wantSkip)
			}
			if tc.wantTVOS && !covered["tvos"]["open-proof"] {
				t.Fatal("the open repo's marker stopped crediting tvos")
			}
			if got := covered["tvos"]["closed-proof"] && !covered["macos"]["closed-proof"]; tc.wantClosedProof && !got {
				t.Fatalf("closed-repo marker coverage = %v, want tvos only", covered)
			}
		})
	}
}

func writeSwift(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

package conformance

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/playback"
)

const quirkLicensePath = "../playback/quirks/LICENSE"

// TestQuirkBaselineVocabulary closes the gap TestVocab leaves open: it skips
// every non-.go file, so the shipped quirks database — which is data, not code,
// and is authored from triage reports — would never be scanned for domain
// vocabulary (invariants 1–3). Quirk entries describe DEVICES; a domain word
// appearing in one is a contract breach wherever it lands.
func TestQuirkBaselineVocabulary(t *testing.T) {
	data, err := os.ReadFile(quirkBaselinePath)
	if err != nil {
		t.Fatalf("read %s: %v", quirkBaselinePath, err)
	}
	rel := "internal/playback/quirks/baseline.json"
	for lineNo, line := range strings.Split(strings.ToLower(string(data)), "\n") {
		if word, ok := vocabLineViolation(rel, line); ok {
			t.Fatalf("%s:%d contains banned domain vocabulary %q", rel, lineNo+1, word)
		}
	}
}

// TestQuirkBaselineIsValid runs the shipped baseline through the real validator,
// so the first evidence-backed entry added later is gate-checked the day it
// lands rather than discovered at server start (NewService panics on an invalid
// baseline, which is a build defect, not a runtime condition).
func TestQuirkBaselineIsValid(t *testing.T) {
	data, err := os.ReadFile(quirkBaselinePath)
	if err != nil {
		t.Fatalf("read %s: %v", quirkBaselinePath, err)
	}
	db, err := playback.ParseQuirkDB(data)
	if err != nil {
		t.Fatalf("shipped quirks baseline is invalid: %v", err)
	}
	if db.Version == "" {
		t.Fatal("shipped quirks baseline declares no version — diagnostics cannot tell a missing quirk from a stale one")
	}
}

// TestQuirkDatabaseIsCC0 pins ADR-0161 §5's licence boundary: compatibility
// facts about hardware ship with an AGPL server but must not become a
// proprietary moat over forks, so the database carries its own dedication and
// the loader refuses a database that drops it.
func TestQuirkDatabaseIsCC0(t *testing.T) {
	license, err := os.ReadFile(quirkLicensePath)
	if err != nil {
		t.Fatalf("read %s: %v", quirkLicensePath, err)
	}
	for _, want := range []string{"CC0-1.0", "CC0 1.0 Universal", "Public Domain Dedication"} {
		if !strings.Contains(string(license), want) {
			t.Fatalf("quirks LICENSE does not carry %q", want)
		}
	}

	data, err := os.ReadFile(quirkBaselinePath)
	if err != nil {
		t.Fatalf("read %s: %v", quirkBaselinePath, err)
	}
	var declared struct {
		License string `json:"license"`
	}
	if err := json.Unmarshal(data, &declared); err != nil {
		t.Fatalf("decode quirks baseline: %v", err)
	}
	if declared.License != "CC0-1.0" {
		t.Fatalf("quirks baseline declares license %q, want CC0-1.0", declared.License)
	}
}

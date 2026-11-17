package app

import (
	"testing"

	"github.com/beamfall/core/internal/identify"
	scanpkg "github.com/beamfall/core/internal/scan"
)

// TestScanIdentifyOutcomeNamesWhyNothingApplied: scan reports every pass that
// applied no match, so the adapter must carry identify's reason across,
// including a skip for a class the plugin has no scraper for.
func TestScanIdentifyOutcomeNamesWhyNothingApplied(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		outcome identify.AutoOutcome
		manual  bool
		want    scanpkg.IdentifyOutcome
	}{
		{"applied", identify.AutoOutcome{Applied: true, ScraperID: "s"}, false, scanpkg.IdentifyOutcome{ScraperID: "s"}},
		{"below threshold", identify.AutoOutcome{Queued: true, ScraperID: "s", Decision: identify.MatchDecision{Scored: true, Score: 0.4, Threshold: 0.8}}, false, scanpkg.IdentifyOutcome{Reason: scanpkg.IdentifyQueuedBelowThreshold, ScraperID: "s"}},
		{"no candidates", identify.AutoOutcome{Queued: true, ScraperID: "s"}, false, scanpkg.IdentifyOutcome{Reason: scanpkg.IdentifyQueuedNoCandidates, ScraperID: "s"}},
		{"manual policy", identify.AutoOutcome{Queued: true, ScraperID: "manual"}, true, scanpkg.IdentifyOutcome{Reason: scanpkg.IdentifyQueuedManual, ScraperID: "manual"}},
		{"no scraper", identify.AutoOutcome{Skipped: true, SkipReason: identify.SkipNoScraper}, false, scanpkg.IdentifyOutcome{Reason: scanpkg.IdentifySkippedNoScraper}},
		{"no bundle", identify.AutoOutcome{Skipped: true, SkipReason: identify.SkipNoBundle}, false, scanpkg.IdentifyOutcome{Reason: scanpkg.IdentifySkippedNoBundle}},
		{"no scorer", identify.AutoOutcome{Skipped: true, SkipReason: identify.SkipNoScorer}, false, scanpkg.IdentifyOutcome{Reason: scanpkg.IdentifySkippedNoScorer}},
		{"not wired", identify.AutoOutcome{Skipped: true, SkipReason: identify.SkipNotWired}, false, scanpkg.IdentifyOutcome{Reason: scanpkg.IdentifySkippedNotWired}},
		{"pinned", identify.AutoOutcome{Skipped: true, SkipReason: identify.SkipPinned}, true, scanpkg.IdentifyOutcome{Reason: scanpkg.IdentifySkippedPinned}},
	}
	for _, tc := range cases {
		if got := scanIdentifyOutcome(tc.outcome, tc.manual); got != tc.want {
			t.Errorf("%s: outcome = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

package identify

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// UnmatchedStore is the Unmatched-queue write seam;
// *store.Store satisfies it. Defined at the consumer. Below-threshold
// items are queued here instead of guessed; a confident match clears the row.
type UnmatchedStore interface {
	QueueUnmatched(ctx context.Context, item store.UnmatchedItem) error
	ClearUnmatched(ctx context.Context, mediaID store.MediaID) (bool, error)
}

// MatchPinStore is the import match-pin read seam;
// *store.Store satisfies it. AutoIdentify reads it so a pinned match — one an
// operator asserted through "Fix match" — is left alone on rescan rather than
// re-identified. The fingerprint keys the pin (the file's stable content
// identity), so the caller passes it from the scanned file it already holds.
type MatchPinStore interface {
	MatchPinned(ctx context.Context, fingerprint string) (bool, bool, error)
}

// SetAutoStores wires the scan-time auto-apply pipeline's two extra seams: the
// Unmatched queue (below-threshold parking) and the import match-pin (rescan
// respects an operator-pinned match). Nil (the default) makes AutoIdentify report
// it is not wired; the manual Search/Apply flow is unaffected either way.
func (s *Service) SetAutoStores(queue UnmatchedStore, pins MatchPinStore) {
	s.queue = queue
	s.pins = pins
}

// AutoOutcome reports what AutoIdentify did with one scanned item, so the caller
// (scan, via the composition-root adapter) can log honestly and tests can assert
// the branch taken. Exactly one of Applied/Queued/Skipped is true on success.
type AutoOutcome struct {
	// Applied is true when the top candidate cleared the plugin's threshold and
	// its lookup document was written onto the Media (the autonomous path,
	// invariant #6 — allowed because Trash is ON).
	Applied bool
	// Queued is true when no candidate cleared the threshold (or none was found):
	// an Unmatched-queue row was written and nothing was applied.
	Queued bool
	// Skipped is true when the item was not a pipeline candidate: the plugin
	// registered no scorer, the bundle declares no searchable scraper for the
	// parsed class, or the match is pinned (an operator already fixed it).
	Skipped bool
	// Decision carries the top candidate's score vs threshold when one was scored
	// (Applied or Queued-with-candidate); the zero value otherwise.
	Decision MatchDecision
}

// AutoIdentify runs the scan-time parse → identify → apply pipeline
// for one freshly scanned/changed Media item. It:
//
//  1. leaves a pinned match alone (FREEZE-3 rescan rule — an operator's "Fix
//     match" must survive rescans);
//  2. resolves the plugin's searchable scraper for the parsed class (the
//     ImpliedMediaFields["media.class"] match), skipping when none exists;
//  3. searches that scraper, scores each candidate with the plugin's registered
//     MatchScorer against the parsed guess, and keeps the best;
//  4. auto-applies the top candidate when it clears the plugin's per-plugin
//     threshold — autonomous, allowed because Trash is ON (invariant #6) — and
//     clears any stale Unmatched-queue row;
//  5. otherwise writes an Unmatched-queue row (the recoverable path) and applies
//     nothing.
//
// A plugin with no registered scorer Skips (keeps the manual-only behaviour
// ADR-0044 §2 specifies until a scorer ships). fingerprint is the scanned file's
// content fingerprint, used only for the pin read; "" skips the pin check.
func (s *Service) AutoIdentify(ctx context.Context, mediaID store.MediaID, pluginID, fingerprint string, parsed ParsedMedia) (AutoOutcome, error) {
	if mediaID == "" {
		return AutoOutcome{}, errors.New("identify: media ID is required")
	}
	if _, ok := s.scorers[pluginID]; !ok {
		return AutoOutcome{Skipped: true}, nil // no scorer → manual-only (ADR-0044 §2)
	}
	if s.queue == nil {
		return AutoOutcome{Skipped: true}, nil // pipeline not wired
	}
	// A pinned match is operator/import-authoritative — a rescan leaves it alone
	// rather than re-identify it. This is the test (c)
	// invariant: the pin survives rescans.
	if fingerprint != "" && s.pins != nil {
		pinned, found, err := s.pins.MatchPinned(ctx, fingerprint)
		if err != nil {
			return AutoOutcome{}, err
		}
		if found && pinned {
			return AutoOutcome{Skipped: true}, nil
		}
	}
	bundle, ok := s.bundles.Bundle(plugin.ID(pluginID))
	if !ok {
		return AutoOutcome{Skipped: true}, nil
	}
	scraper, ok := s.searchableScraperForClass(pluginID, bundle, parsed.Class)
	if !ok {
		return AutoOutcome{Skipped: true}, nil
	}
	candidates, err := s.Search(ctx, pluginID, scraper.ID, parsed.Title, mediaID)
	if err != nil {
		return AutoOutcome{}, err
	}
	best, decision, hasBest := s.bestCandidate(pluginID, candidates, parsed)
	if hasBest && decision.AutoApply {
		if err := s.Apply(ctx, mediaID, pluginID, scraper.ID, best.ID); err != nil {
			return AutoOutcome{}, err
		}
		// A successful auto-apply clears any stale queue row from a prior scan.
		if _, err := s.queue.ClearUnmatched(ctx, mediaID); err != nil {
			return AutoOutcome{}, err
		}
		return AutoOutcome{Applied: true, Decision: decision}, nil
	}
	// Below threshold (or no candidate): park it, never guess.
	item := store.UnmatchedItem{
		MediaID:   mediaID,
		PluginID:  pluginID,
		ScraperID: scraper.ID,
		Threshold: decision.Threshold,
	}
	if hasBest {
		item.Confidence = decision.Score
		item.CandidateID = best.ID
		item.CandidateTitle = best.Title
	}
	if err := s.queue.QueueUnmatched(ctx, item); err != nil {
		return AutoOutcome{}, err
	}
	return AutoOutcome{Queued: true, Decision: decision}, nil
}

const manualScraperID = "manual"

// QueueManual parks a scanned item in the Unmatched queue for plugins with no
// searchable catalog to score or auto-apply against. Local file/folder/sidecar
// hints remain useful context for manual matching.
func (s *Service) QueueManual(ctx context.Context, mediaID store.MediaID, pluginID, fingerprint string, parsed ParsedMedia) (AutoOutcome, error) {
	if mediaID == "" {
		return AutoOutcome{}, errors.New("identify: media ID is required")
	}
	if pluginID == "" {
		return AutoOutcome{}, errors.New("identify: plugin ID is required")
	}
	if s.queue == nil {
		return AutoOutcome{Skipped: true}, nil
	}
	if fingerprint != "" && s.pins != nil {
		pinned, found, err := s.pins.MatchPinned(ctx, fingerprint)
		if err != nil {
			return AutoOutcome{}, err
		}
		if found && pinned {
			return AutoOutcome{Skipped: true}, nil
		}
	}
	if _, ok := s.bundles.Bundle(plugin.ID(pluginID)); !ok {
		return AutoOutcome{Skipped: true}, nil
	}
	if err := s.queue.QueueUnmatched(ctx, store.UnmatchedItem{
		MediaID:        mediaID,
		PluginID:       pluginID,
		ScraperID:      manualScraperID,
		CandidateTitle: strings.TrimSpace(parsed.Title),
	}); err != nil {
		return AutoOutcome{}, err
	}
	return AutoOutcome{Queued: true}, nil
}

// bestCandidate scores every candidate with the plugin's MatchScorer and returns
// the highest-scoring one with its MatchDecision. hasBest is false when the
// candidate list is empty.
//
// Fail-closed tie rule: a top score that would auto-apply but is
// shared by two or more candidates is a genuine ambiguity unless the candidates
// carry a popularity signal that breaks the tie. Popularity is consulted only at
// this exact-score point, never as a score bonus, so it cannot lift a lower
// confidence match over a stronger title/year/ordinal match.
func (s *Service) bestCandidate(pluginID string, candidates []Candidate, parsed ParsedMedia) (Candidate, MatchDecision, bool) {
	var (
		best      Candidate
		bestDec   MatchDecision
		hasBest   bool
		tiedAtTop bool
	)
	for _, candidate := range candidates {
		decision := s.ScoreMatch(pluginID, candidate, parsed)
		switch {
		case !hasBest || decision.Score > bestDec.Score:
			best, bestDec, hasBest, tiedAtTop = candidate, decision, true, false
		case decision.Score == bestDec.Score:
			switch {
			case candidate.Popularity > best.Popularity:
				best, bestDec, tiedAtTop = candidate, decision, false
			case candidate.Popularity == best.Popularity:
				tiedAtTop = true
			}
		}
	}
	if tiedAtTop && bestDec.AutoApply {
		bestDec.AutoApply = false // ambiguous top — defer to manual (fail-closed)
	}
	return best, bestDec, hasBest
}

// searchableScraperForClass picks the bundle's scraper whose implied class equals
// the parsed class and that can run a free-text search (the ADR-0028 §4
// SearchTemplate → CandidateIDPath chain). When parsed.Class is empty (the parser
// could not classify), the sole searchable scraper is used if there is exactly
// one — an unambiguous bundle still auto-identifies; an ambiguous one defers to
// the manual flow rather than guess a class.
func (s *Service) searchableScraperForClass(pluginID string, bundle plugin.Bundle, class string) (plugin.ScraperSpec, bool) {
	class = strings.TrimSpace(class)
	var (
		soleSearchable  plugin.ScraperSpec
		searchableCount int
	)
	for _, scraper := range bundle.Scrapers {
		if scraper.SearchTemplate == "" || scraper.CandidateIDPath == "" {
			continue
		}
		if scraper.OptionalAuth && !s.credentialAvailable(scraper.AuthRef) {
			key := pluginID + "\x00" + scraper.ID
			if _, loaded := s.optionalAuthSkips.LoadOrStore(key, struct{}{}); !loaded {
				slog.Info("identify: optional-auth scraper unavailable; skipping", "plugin", pluginID, "scraper", scraper.ID)
			}
			continue
		}
		searchableCount++
		soleSearchable = scraper
		if class != "" && impliedClass(scraper) == class {
			return scraper, true
		}
	}
	if class == "" && searchableCount == 1 {
		return soleSearchable, true
	}
	return plugin.ScraperSpec{}, false
}

type credentialAvailability interface {
	CredentialAvailable(ref string) bool
}

func (s *Service) credentialAvailable(ref string) bool {
	checker, ok := s.engine.(credentialAvailability)
	return ok && checker.CredentialAvailable(ref)
}

// impliedClass reads a scraper's ImpliedMediaFields class constant, tolerating
// both the bare "class" key and the BF-24-namespaced "media.class" key the
// first-party bundles declare.
func impliedClass(scraper plugin.ScraperSpec) string {
	if v, ok := scraper.ImpliedMediaFields["media.class"]; ok {
		return v
	}
	return scraper.ImpliedMediaFields["class"]
}

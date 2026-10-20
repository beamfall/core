package identify

import (
	"math"

	sdk "github.com/beamfall/plugin-sdk"
)

// MatchScorer is a first-party compiled plugin hook:
// it scores how confidently one identification Candidate matches a file's parsed
// guess, so identification can auto-apply a high-confidence match and queue a
// low-confidence one. Like FilenameParser it is declared at its consumer
// and is domain-agnostic at this seam — Candidate and ParsedMedia are
// shape-named, and the Film & TV specifics live in the registered impl, keyed by
// plugin ID. The result is the match confidence in [0,1]; the Service clamps
// out-of-range values before comparing against the per-plugin threshold.
type MatchScorer = sdk.MatchScorer

// ParsedMedia is the parser's best guess for an unidentified file, carried into
// the scorer so it can weigh a Candidate against what the filename implied. It
// mirrors the scan-side parser output by shape (the core vocab gate bans domain
// names here just as in internal/scan): a plugin reads Sequence/Index/IndexEnd
// as its own concepts. identify cannot import internal/scan (arch §4), so the
// composition root maps scan.ParsedMedia onto this identify-local twin when it
// registers a scorer.
type ParsedMedia = sdk.ParsedMedia

// scorerBinding pairs a plugin's MatchScorer with its auto-apply threshold.
type scorerBinding struct {
	scorer    MatchScorer
	threshold float64
}

// RegisterScorer binds a plugin's MatchScorer and its auto-apply threshold,
// keyed by plugin ID, so a plugin's matches are scored by its own scorer (the
// composition root registers the Film & TV scorer under "filmtv"). The threshold
// is clamped to [0,1]. A plugin with no registered scorer keeps identification's
// existing manual-apply behaviour (ScoreMatch reports applied=false, scored=false).
func (s *Service) RegisterScorer(pluginID string, scorer MatchScorer, threshold float64) {
	if scorer == nil {
		return
	}
	if s.scorers == nil {
		s.scorers = map[string]scorerBinding{}
	}
	s.scorers[pluginID] = scorerBinding{scorer: scorer, threshold: clampThreshold(threshold)}
}

// MatchDecision is one candidate's auto-apply verdict. It is the
// seam PIPE-1 wires into scan: a Score at or above the plugin's threshold means
// the match is confident enough to apply without a human; below it, the file
// queues for manual identification. Scored is false when the plugin registered
// no scorer, in which case the caller falls back to its existing manual flow.
type MatchDecision struct {
	Score     float64 // clamped match confidence in [0,1]
	Threshold float64 // the plugin's auto-apply threshold in [0,1]
	Scored    bool    // a scorer was registered for the plugin
	AutoApply bool    // Scored && Score >= Threshold
}

// ScoreMatch scores one Candidate against a file's parsed guess using the
// plugin's registered scorer and reports whether it clears the auto-apply
// threshold. With no scorer registered for the plugin it returns a zero
// MatchDecision (Scored=false) — identification keeps its manual-apply
// behaviour, exactly as ADR-0044 §2 specifies until a scorer ships per plugin.
func (s *Service) ScoreMatch(pluginID string, candidate Candidate, parsed ParsedMedia) MatchDecision {
	binding, ok := s.scorers[pluginID]
	if !ok {
		return MatchDecision{}
	}
	score := clampScore(binding.scorer.Score(candidate, parsed))
	return MatchDecision{
		Score:     score,
		Threshold: binding.threshold,
		Scored:    true,
		AutoApply: score >= binding.threshold,
	}
}

// clampUnit pins a score or threshold to [0,1]: a scorer returning a value
// outside the contract (or an out-of-range registered threshold) is clamped
// rather than trusted, so the auto-apply comparison stays well-defined.
func clampUnit(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func clampScore(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return clampUnit(v)
}

func clampThreshold(v float64) float64 {
	if math.IsNaN(v) {
		return 1
	}
	return clampUnit(v)
}

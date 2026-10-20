package identify_test

import (
	"math"
	"testing"

	"github.com/beamfall/core/internal/identify"
)

// stubScorer returns a fixed score, ignoring its inputs — it exercises the
// Service's threshold/clamp/registration seam independently of any real scoring
// model (the Film & TV model is tested in the beamfall-plugin-filmtv module, package filmtvparse — relocated per ADR-0114/ORG-52).
type stubScorer float64

func (s stubScorer) Score(identify.Candidate, identify.ParsedMedia) float64 { return float64(s) }

func TestScoreMatchAutoApplyAboveThreshold(t *testing.T) {
	t.Parallel()
	svc := identify.NewService(nil, nil, nil)
	svc.RegisterScorer("filmtv", stubScorer(0.9), 0.75)

	got := svc.ScoreMatch("filmtv", identify.Candidate{Title: "The Matrix", Year: 1999},
		identify.ParsedMedia{Title: "The Matrix", Year: 1999})

	if !got.Scored {
		t.Fatal("expected Scored=true for a registered plugin")
	}
	if !got.AutoApply {
		t.Errorf("score %.2f >= threshold %.2f should auto-apply", got.Score, got.Threshold)
	}
	if got.Score != 0.9 || got.Threshold != 0.75 {
		t.Errorf("got score=%.2f threshold=%.2f, want 0.90/0.75", got.Score, got.Threshold)
	}
}

func TestScoreMatchQueuesBelowThreshold(t *testing.T) {
	t.Parallel()
	svc := identify.NewService(nil, nil, nil)
	svc.RegisterScorer("filmtv", stubScorer(0.4), 0.75)

	got := svc.ScoreMatch("filmtv", identify.Candidate{Title: "Unrelated"},
		identify.ParsedMedia{Title: "The Matrix"})

	if !got.Scored {
		t.Fatal("expected Scored=true for a registered plugin")
	}
	if got.AutoApply {
		t.Errorf("score %.2f < threshold %.2f should queue, not auto-apply", got.Score, got.Threshold)
	}
}

func TestScoreMatchAtThresholdAutoApplies(t *testing.T) {
	t.Parallel()
	svc := identify.NewService(nil, nil, nil)
	svc.RegisterScorer("filmtv", stubScorer(0.75), 0.75)

	if !svc.ScoreMatch("filmtv", identify.Candidate{}, identify.ParsedMedia{}).AutoApply {
		t.Error("score exactly at threshold must auto-apply (>= comparison)")
	}
}

func TestScoreMatchUnregisteredPluginKeepsManualFlow(t *testing.T) {
	t.Parallel()
	svc := identify.NewService(nil, nil, nil)
	// No scorer registered for "other".
	svc.RegisterScorer("filmtv", stubScorer(1), 0.75)

	got := svc.ScoreMatch("other", identify.Candidate{Title: "x"}, identify.ParsedMedia{Title: "x"})

	if got.Scored {
		t.Error("a plugin with no registered scorer must report Scored=false (manual flow)")
	}
	if got.AutoApply {
		t.Error("an unscored plugin must never auto-apply")
	}
}

func TestRegisterScorerClampsThresholdAndScore(t *testing.T) {
	t.Parallel()
	svc := identify.NewService(nil, nil, nil)
	// Out-of-range threshold clamps to 1; out-of-range score clamps to [0,1].
	svc.RegisterScorer("filmtv", stubScorer(5), 2)

	got := svc.ScoreMatch("filmtv", identify.Candidate{}, identify.ParsedMedia{})
	if got.Threshold != 1 {
		t.Errorf("threshold 2 should clamp to 1, got %.2f", got.Threshold)
	}
	if got.Score != 1 {
		t.Errorf("score 5 should clamp to 1, got %.2f", got.Score)
	}
	if !got.AutoApply {
		t.Error("clamped 1.0 >= clamped 1.0 should auto-apply")
	}

	svc.RegisterScorer("neg", stubScorer(-3), 0.5)
	if s := svc.ScoreMatch("neg", identify.Candidate{}, identify.ParsedMedia{}).Score; s != 0 {
		t.Errorf("negative score should clamp to 0, got %.2f", s)
	}
}

func TestRegisterScorerClampsNaNFailClosed(t *testing.T) {
	t.Parallel()
	svc := identify.NewService(nil, nil, nil)
	svc.RegisterScorer("nan-score", stubScorer(math.NaN()), 0.5)
	score := svc.ScoreMatch("nan-score", identify.Candidate{}, identify.ParsedMedia{})
	if score.Score != 0 || score.AutoApply {
		t.Fatalf("NaN score decision = %+v, want zero score without auto-apply", score)
	}

	svc.RegisterScorer("nan-threshold", stubScorer(0.5), math.NaN())
	threshold := svc.ScoreMatch("nan-threshold", identify.Candidate{}, identify.ParsedMedia{})
	if threshold.Threshold != 1 || threshold.AutoApply {
		t.Fatalf("NaN threshold decision = %+v, want threshold 1 without auto-apply", threshold)
	}
}

func TestRegisterScorerNilIgnored(t *testing.T) {
	t.Parallel()
	svc := identify.NewService(nil, nil, nil)
	svc.RegisterScorer("filmtv", nil, 0.75)
	if svc.ScoreMatch("filmtv", identify.Candidate{}, identify.ParsedMedia{}).Scored {
		t.Error("a nil scorer must not register")
	}
}

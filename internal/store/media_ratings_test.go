package store

import (
	"context"
	"testing"
)

// TestDemoSeedCarriesRatings covers CORE-2398: the demo seed gives its films a
// certification and its series a TV display rating, each with a 0-10 score,
// and the card annotation reads them back.
func TestDemoSeedCarriesRatings(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir(), Options{SeedProfile: SeedProfileDemo})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer st.Close()
	items := []SourceItem{{ID: "media-demo-sintel", Kind: "media"}, {ID: "media-demo-series-1", Kind: "media"}, {ID: "media-demo-guarded-1", Kind: "media"}}
	if err := st.AnnotateMediaClass(context.Background(), items); err != nil {
		t.Fatalf("AnnotateMediaClass: %v", err)
	}
	for i, want := range []struct {
		certification string
		score         float64
	}{{"PG-13", 7.4}, {"TV-PG", 8.1}, {"", 0}} {
		got := items[i]
		if got.Certification != want.certification {
			t.Fatalf("%s certification = %q, want %q", got.ID, got.Certification, want.certification)
		}
		if want.score == 0 {
			if got.AudienceScore != nil {
				t.Fatalf("%s audienceScore = %v, want nil", got.ID, *got.AudienceScore)
			}
			continue
		}
		if got.AudienceScore == nil || *got.AudienceScore != want.score {
			t.Fatalf("%s audienceScore = %v, want %v", got.ID, got.AudienceScore, want.score)
		}
	}
}

func TestMediaRatingFromPrecedenceAndRange(t *testing.T) {
	cert, score, _ := MediaRatingFrom([]MediaField{
		{Name: "certification", Type: EntityFieldText, Text: "R"},
		{Name: "community-rating", Type: EntityFieldNumber, Number: 11},
	})
	if cert != "R" || score != nil {
		t.Fatalf("MediaRatingFrom = %q, %v; want R and no score off the 0-10 scale", cert, score)
	}
	cert, score, _ = MediaRatingFrom([]MediaField{
		{Name: "certification-display", Type: EntityFieldText, Text: "TV-MA"},
		{Name: "certification", Type: EntityFieldText, Text: "R"},
		{Name: "community-rating", Type: EntityFieldNumber, Number: 6.4},
	})
	if cert != "TV-MA" || score == nil || *score != 6.4 {
		t.Fatalf("MediaRatingFrom = %q, %v; want TV-MA and 6.4", cert, score)
	}
}

// TestMediaRatingFromDropsZeroAndVotelessScores covers CORE-2524: a zero
// score, and a score whose stored vote count is zero, are no score; a score
// stored without any vote count is kept.
func TestMediaRatingFromDropsZeroAndVotelessScores(t *testing.T) {
	score := func(fields ...MediaField) *float64 {
		_, got, _ := MediaRatingFrom(fields)
		return got
	}
	rating := func(v float64) MediaField {
		return MediaField{Name: "community-rating", Type: EntityFieldNumber, Number: v}
	}
	votes := func(v float64) MediaField {
		return MediaField{Name: "community-rating-votes", Type: EntityFieldNumber, Number: v}
	}
	if got := score(rating(0)); got != nil {
		t.Fatalf("zero score = %v, want none", *got)
	}
	if got := score(rating(7.5), votes(0)); got != nil {
		t.Fatalf("score with zero votes = %v, want none", *got)
	}
	if got := score(rating(7.5)); got == nil || *got != 7.5 {
		t.Fatalf("score without a vote count = %v, want 7.5", got)
	}
	if got := score(rating(7.5), votes(12)); got == nil || *got != 7.5 {
		t.Fatalf("voted score = %v, want 7.5", got)
	}
}

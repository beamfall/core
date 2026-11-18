package store

import "database/sql"

// Core-conventional Media field names a client renders as a rating (CORE-2398).
// The names are core convention, as for certificationField; the values are
// plugin data. certification-display is the display string (a series TV
// rating, say) and wins over certification, the value the Profile ceiling
// ranks. community-rating is a number field holding a 0-10 audience score and
// community-rating-votes the count of votes behind it (CORE-2415).
const (
	certificationDisplayField = "certification-display"
	audienceScoreField        = "community-rating"
	audienceScoreVotesField   = "community-rating-votes"
	audienceScoreMax          = 10
)

// AudienceScoreField is the core-conventional Media field whose plugin label
// names the audience score's source on the wire (CORE-2415).
const AudienceScoreField = audienceScoreField

// mediaRatingColumns selects the display certification, the audience score and
// its vote count of the media row in scope, for queries over `media`.
const mediaRatingColumns = `COALESCE(
		(SELECT value FROM media_field_text WHERE media_id = media.id AND field = '` + certificationDisplayField + `' AND field_type = 'text' AND value <> ''),
		(SELECT value FROM media_field_text WHERE media_id = media.id AND field = '` + certificationField + `' AND field_type = 'text'),
		''),
	(SELECT value FROM media_field_real WHERE media_id = media.id AND field = '` + audienceScoreField + `'),
	(SELECT value FROM media_field_real WHERE media_id = media.id AND field = '` + audienceScoreVotesField + `')`

// MediaRatingFrom returns the display certification, the audience score and
// its vote count carried by one Media item's fields, with the same precedence
// as the browse read. A score outside 0-10 is not a score on this scale and is
// dropped; votes are kept only beside a score (see audienceScoreVotes).
func MediaRatingFrom(fields []MediaField) (certification string, score *float64, votes int) {
	var fallback string
	var raw, rawVotes sql.NullFloat64
	for _, field := range fields {
		switch {
		case field.Name == certificationDisplayField && field.Type == EntityFieldText:
			certification = field.Text
		case field.Name == certificationField && field.Type == EntityFieldText:
			fallback = field.Text
		case field.Name == audienceScoreField && field.Type == EntityFieldNumber:
			raw = sql.NullFloat64{Float64: field.Number, Valid: true}
		case field.Name == audienceScoreVotesField && field.Type == EntityFieldNumber:
			rawVotes = sql.NullFloat64{Float64: field.Number, Valid: true}
		}
	}
	if certification == "" {
		certification = fallback
	}
	score = votedAudienceScore(raw, rawVotes)
	return certification, score, audienceScoreVotes(score, rawVotes)
}

// audienceScore is a stored 0-10 audience score. Zero is no score: no
// provider rates a title 0.0, and a stored zero is a provider's placeholder
// for "unrated" (CORE-2524).
func audienceScore(raw sql.NullFloat64) *float64 {
	if !raw.Valid || raw.Float64 <= 0 || raw.Float64 > audienceScoreMax {
		return nil
	}
	score := raw.Float64
	return &score
}

// votedAudienceScore is audienceScore, dropped when a vote count is stored
// and is zero: a score nobody voted for is no score (CORE-2524). A score
// stored without any vote count is kept.
func votedAudienceScore(raw, votes sql.NullFloat64) *float64 {
	if votes.Valid && votes.Float64 < 1 {
		return nil
	}
	return audienceScore(raw)
}

// audienceScoreVotes is the vote count behind a usable score; zero when there
// is no score or no positive count.
func audienceScoreVotes(score *float64, raw sql.NullFloat64) int {
	if score == nil || !raw.Valid || raw.Float64 < 1 {
		return 0
	}
	return int(raw.Float64)
}

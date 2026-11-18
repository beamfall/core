package store

import "database/sql"

// Core-conventional Media field names a client renders as a rating (CORE-2398).
// The names are core convention, as for certificationField; the values are
// plugin data. certification-display is the display string (a series TV
// rating, say) and wins over certification, the value the Profile ceiling
// ranks. community-rating is a number field holding a 0-10 audience score.
const (
	certificationDisplayField = "certification-display"
	audienceScoreField        = "community-rating"
	audienceScoreMax          = 10
)

// mediaRatingColumns selects the display certification and the audience score
// of the media row in scope, for queries over `media`.
const mediaRatingColumns = `COALESCE(
		(SELECT value FROM media_field_text WHERE media_id = media.id AND field = '` + certificationDisplayField + `' AND field_type = 'text' AND value <> ''),
		(SELECT value FROM media_field_text WHERE media_id = media.id AND field = '` + certificationField + `' AND field_type = 'text'),
		''),
	(SELECT value FROM media_field_real WHERE media_id = media.id AND field = '` + audienceScoreField + `')`

// MediaRatingFrom returns the display certification and the audience score
// carried by one Media item's fields, with the same precedence as the browse
// read. A score outside 0-10 is not a score on this scale and is dropped.
func MediaRatingFrom(fields []MediaField) (certification string, score *float64) {
	var fallback string
	var raw sql.NullFloat64
	for _, field := range fields {
		switch {
		case field.Name == certificationDisplayField && field.Type == EntityFieldText:
			certification = field.Text
		case field.Name == certificationField && field.Type == EntityFieldText:
			fallback = field.Text
		case field.Name == audienceScoreField && field.Type == EntityFieldNumber:
			raw = sql.NullFloat64{Float64: field.Number, Valid: true}
		}
	}
	if certification == "" {
		certification = fallback
	}
	return certification, audienceScore(raw)
}

func audienceScore(raw sql.NullFloat64) *float64 {
	if !raw.Valid || raw.Float64 < 0 || raw.Float64 > audienceScoreMax {
		return nil
	}
	score := raw.Float64
	return &score
}

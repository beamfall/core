package store

import (
	"context"
	"strings"
)

// MediaCardFieldValues reads the named text and number field values of many
// Media at once, keyed by Media ID then field name: the values a card renders
// without a detail read (the plugin's card subtitle and the structural
// ordinals, CORE-1226). Text fields carry Text and number fields Number; flag,
// date and url fields are not read. Every open catalog plane is read, so an
// unsealed Encrypted Library's Media are covered. Media with none of the
// fields are absent from the result.
func (s *Store) MediaCardFieldValues(ctx context.Context, ids []MediaID, names []string) (map[MediaID]map[string]MediaField, error) {
	values := map[MediaID]map[string]MediaField{}
	if len(ids) == 0 || len(names) == 0 {
		return values, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	nameArgs := make([]any, len(names))
	for i, name := range names {
		nameArgs[i] = name
	}
	nameClause := " AND field IN (" + sqlPlaceholders(len(names)) + ")"
	for _, reader := range s.openCatalogReadersLocked() {
		for start := 0; start < len(ids); start += engagementChunkSize {
			chunk := ids[start:min(start+engagementChunkSize, len(ids))]
			args := make([]any, 0, len(chunk)+len(names))
			for _, id := range chunk {
				args = append(args, id)
			}
			args = append(args, nameArgs...)
			for _, table := range []string{"media_field_text", "media_field_real"} {
				// #nosec G202 -- the table name is one of two literals; only generated placeholders are interpolated and every value is a bound arg.
				rows, err := reader.QueryContext(ctx, `SELECT media_id, field, field_type, value FROM `+table+`
					WHERE media_id IN (`+sqlPlaceholders(len(chunk))+`)`+nameClause, args...)
				if err != nil {
					return nil, err
				}
				for rows.Next() {
					var id MediaID
					var field MediaField
					if table == "media_field_text" {
						err = rows.Scan(&id, &field.Name, &field.Type, &field.Text)
						field.Text = strings.TrimSpace(field.Text)
					} else {
						err = rows.Scan(&id, &field.Name, &field.Type, &field.Number)
					}
					if err != nil {
						_ = rows.Close()
						return nil, err
					}
					if values[id] == nil {
						values[id] = map[string]MediaField{}
					}
					values[id][field.Name] = field
				}
				if err := rows.Close(); err != nil {
					return nil, err
				}
				if err := rows.Err(); err != nil {
					return nil, err
				}
			}
		}
	}
	return values, nil
}

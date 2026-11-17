package store

import (
	"context"
	"database/sql"
)

// AnnotateMediaClass sets MediaClass on each media-kind SourceItem from its
// stored playback class, so a client can pick a class-appropriate placeholder
// for a card with no artwork (CORE-1492), and Year from its stored year, so a
// card can state it without a detail read (CORE-2269). Entity items are left
// alone; Media whose class is unknown keep an empty class and Media with no
// year keep 0. Every open catalog plane is read, so an unsealed Encrypted
// Library's Media are annotated too. Safe on any SourceItem slice.
func (s *Store) AnnotateMediaClass(ctx context.Context, items []SourceItem) error {
	var ids []MediaID
	for i := range items {
		if items[i].Kind != "entity" {
			ids = append(ids, items[i].ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	type cardFacts struct {
		class MediaClass
		year  int
	}
	facts := make(map[MediaID]cardFacts, len(ids))
	for _, reader := range s.openCatalogReadersLocked() {
		for start := 0; start < len(ids); start += engagementChunkSize {
			chunk := ids[start:min(start+engagementChunkSize, len(ids))]
			args := make([]any, len(chunk))
			for i, id := range chunk {
				args[i] = id
			}
			// #nosec G202 -- only generated placeholders are interpolated; IDs are bound args.
			rows, err := reader.QueryContext(ctx, `SELECT id, media_class, year FROM media WHERE id IN (`+sqlPlaceholders(len(chunk))+`)`, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id MediaID
				var class MediaClass
				var year sql.NullInt64
				if err := rows.Scan(&id, &class, &year); err != nil {
					_ = rows.Close()
					return err
				}
				facts[id] = cardFacts{class: class, year: int(year.Int64)}
			}
			if err := rows.Close(); err != nil {
				return err
			}
			if err := rows.Err(); err != nil {
				return err
			}
		}
	}
	for i := range items {
		if items[i].Kind == "entity" {
			continue
		}
		items[i].MediaClass = facts[items[i].ID].class
		items[i].Year = facts[items[i].ID].year
	}
	return nil
}

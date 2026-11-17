package store

import (
	"context"
	"database/sql"
)

// AnnotateMediaClass sets MediaClass on each media-kind SourceItem from its
// stored playback class, so a client can pick a class-appropriate placeholder
// for a card with no artwork (CORE-1492), and Year from its stored year, so a
// card can state it without a detail read (CORE-2269). An entity item takes
// the class its directly linked, untrashed Media share, so a card for a
// container of audio frames its art like its tracks (CORE-2362); one whose
// Media mix classes, or that links none, keeps an empty class, and entities
// never carry a year. Media whose class is unknown keep an empty class and
// Media with no year keep 0. Every open catalog plane is read, so an unsealed
// Encrypted Library's Media are annotated too. The same read sets a media-kind
// item's LibraryID. Safe on any SourceItem slice.
func (s *Store) AnnotateMediaClass(ctx context.Context, items []SourceItem) error {
	var ids, entityIDs []MediaID
	for i := range items {
		if items[i].Kind == "entity" {
			entityIDs = append(entityIDs, items[i].ID)
		} else {
			ids = append(ids, items[i].ID)
		}
	}
	if len(ids) == 0 && len(entityIDs) == 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	type cardFacts struct {
		class   MediaClass
		year    int
		library LibraryID
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
			rows, err := reader.QueryContext(ctx, `SELECT id, media_class, year, library_id FROM media WHERE id IN (`+sqlPlaceholders(len(chunk))+`)`, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id MediaID
				var class MediaClass
				var year sql.NullInt64
				var library LibraryID
				if err := rows.Scan(&id, &class, &year, &library); err != nil {
					_ = rows.Close()
					return err
				}
				facts[id] = cardFacts{class: class, year: int(year.Int64), library: library}
			}
			if err := rows.Close(); err != nil {
				return err
			}
			if err := rows.Err(); err != nil {
				return err
			}
		}
	}
	entityClasses, err := s.entityMediaClassesLocked(ctx, entityIDs)
	if err != nil {
		return err
	}
	for i := range items {
		if items[i].Kind == "entity" {
			items[i].MediaClass = entityClasses[items[i].ID]
			continue
		}
		items[i].MediaClass = facts[items[i].ID].class
		items[i].Year = facts[items[i].ID].year
		items[i].LibraryID = facts[items[i].ID].library
	}
	return nil
}

// entityMediaClassesLocked maps each Entity to the one class its directly
// linked, untrashed Media share. An Entity whose Media mix classes, or include
// one of unknown class, maps to nothing. Caller holds s.mu.
func (s *Store) entityMediaClassesLocked(ctx context.Context, ids []MediaID) (map[MediaID]MediaClass, error) {
	type span struct{ low, high MediaClass }
	spans := map[MediaID]span{}
	if len(ids) == 0 {
		return map[MediaID]MediaClass{}, nil
	}
	for _, reader := range s.openCatalogReadersLocked() {
		for start := 0; start < len(ids); start += engagementChunkSize {
			chunk := ids[start:min(start+engagementChunkSize, len(ids))]
			args := make([]any, len(chunk))
			for i, id := range chunk {
				args[i] = id
			}
			// #nosec G202 -- only generated placeholders are interpolated; IDs are bound args.
			rows, err := reader.QueryContext(ctx, `
				SELECT r.from_entity_id, MIN(m.media_class), MAX(m.media_class)
				FROM entity_relation r
				JOIN media m ON m.id = r.to_media_id
				WHERE r.from_entity_id IN (`+sqlPlaceholders(len(chunk))+`) AND m.trashed_at_ms IS NULL
				GROUP BY r.from_entity_id`, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id MediaID
				var low, high MediaClass
				if err := rows.Scan(&id, &low, &high); err != nil {
					_ = rows.Close()
					return nil, err
				}
				if seen, ok := spans[id]; ok {
					low, high = min(low, seen.low), max(high, seen.high)
				}
				spans[id] = span{low: low, high: high}
			}
			if err := rows.Close(); err != nil {
				return nil, err
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
		}
	}
	classes := make(map[MediaID]MediaClass, len(spans))
	for id, span := range spans {
		if span.low != "" && span.low == span.high {
			classes[id] = span.low
		}
	}
	return classes, nil
}

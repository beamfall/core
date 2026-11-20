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
// Media mix classes, or that links none, keeps an empty class. The scoped
// Source read sets Entity years separately. Media whose class is unknown
// keep an empty class and Media with no year keep 0. Certification,
// AudienceScore and AudienceScoreVotes come from the core-conventional rating fields (see
// MediaRatingFrom, CORE-2398, CORE-2415); Media without them keep the zero
// values. Every open catalog plane is read, so an
// unsealed Encrypted Library's Media are annotated too. The same read sets a
// media-kind item's DurationSeconds. Safe on any SourceItem slice.
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
		class         MediaClass
		year          int
		duration      int
		certification string
		score         sql.NullFloat64
		votes         sql.NullFloat64
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
			rows, err := reader.QueryContext(ctx, `SELECT id, media_class, year, duration_seconds, `+mediaRatingColumns+` FROM media WHERE id IN (`+sqlPlaceholders(len(chunk))+`)`, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id MediaID
				var class MediaClass
				var year sql.NullInt64
				var duration sql.NullInt64
				var certification string
				var score, votes sql.NullFloat64
				if err := rows.Scan(&id, &class, &year, &duration, &certification, &score, &votes); err != nil {
					_ = rows.Close()
					return err
				}
				facts[id] = cardFacts{class: class, year: int(year.Int64), duration: int(duration.Int64), certification: certification, score: score, votes: votes}
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
		items[i].DurationSeconds = facts[items[i].ID].duration
		items[i].Certification = facts[items[i].ID].certification
		items[i].AudienceScore = votedAudienceScore(facts[items[i].ID].score, facts[items[i].ID].votes)
		items[i].AudienceScoreVotes = audienceScoreVotes(items[i].AudienceScore, facts[items[i].ID].votes)
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

// annotateEntityYearsLocked sets each Entity card's year from its earliest
// dated, directly linked Media visible to this Source. The Source bounds the
// number of IDs, so each readable plane can aggregate in one query.
func (s *Store) annotateEntityYearsLocked(ctx context.Context, filter LibraryFilter, items []SourceItem) error {
	var ids []any
	for _, item := range items {
		if item.Kind == "entity" {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return err
	}
	years := make(map[MediaID]int, len(ids))
	var publicIDs []LibraryID
	for _, library := range libraries {
		if !libraryVisible(library, filter) {
			continue
		}
		if !library.Encrypted {
			publicIDs = append(publicIDs, library.ID)
			continue
		}
		db := s.encryptedDBs[library.ID]
		if db == nil || db.state != "unsealed-registered" || db.reader == nil {
			continue
		}
		var pluginID string
		if library.PluginID != nil {
			pluginID = *library.PluginID
		}
		certClause, certArgs := planeCertificationFilterSQL(pluginID, filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
		if err := readEntityYears(ctx, db.reader, ids, []LibraryID{library.ID}, certClause, certArgs, years); err != nil {
			return err
		}
	}
	if len(publicIDs) > 0 {
		certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
		if err := readEntityYears(ctx, s.catalogR, ids, publicIDs, certClause, certArgs, years); err != nil {
			return err
		}
	}
	for i := range items {
		if items[i].Kind == "entity" {
			items[i].Year = years[items[i].ID]
		}
	}
	return nil
}

func readEntityYears(ctx context.Context, reader *sql.DB, ids []any, libraryIDs []LibraryID, certClause string, certArgs []any, years map[MediaID]int) error {
	libraries, libraryArgs := libraryIDPlaceholders(libraryIDs)
	args := make([]any, 0, len(ids)+len(libraryArgs)+len(certArgs))
	args = append(args, ids...)
	args = append(args, libraryArgs...)
	args = append(args, certArgs...)
	// #nosec G202 -- only generated placeholders and closed certification SQL are interpolated.
	rows, err := reader.QueryContext(ctx, `
		SELECT r.from_entity_id, MIN(m.year)
		FROM entity_relation r
		JOIN media m ON m.id = r.to_media_id
		WHERE r.from_entity_id IN (`+sqlPlaceholders(len(ids))+`)
		  AND r.to_media_id IS NOT NULL
		  AND m.library_id IN (`+libraries+`)
		  AND m.trashed_at_ms IS NULL AND m.year > 0`+certClause+`
		GROUP BY r.from_entity_id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id MediaID
		var year int
		if err := rows.Scan(&id, &year); err != nil {
			return err
		}
		if current, ok := years[id]; !ok || year < current {
			years[id] = year
		}
	}
	return rows.Err()
}

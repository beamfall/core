package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// AnnotateMediaClass sets MediaClass on each media-kind SourceItem from its
// stored playback class, so a client can pick a class-appropriate placeholder
// for a card with no artwork (CORE-1492), and Year from its stored year, so a
// card can state it without a detail read (CORE-2269). An entity item takes
// the class its directly linked, untrashed Media share, so a card for a
// container of audio frames its art like its tracks (CORE-2362); one that
// links no Media of its own takes the class of its linked Entities' Media
// instead. One whose Media mix classes, or that reaches none, keeps an empty
// class. The scoped
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
// linked, untrashed Media share. An Entity with no Media of its own takes the
// class of the Media linked to its linked Entities instead, so an artist whose
// tracks all hang off an album still frames its card square. An Entity whose
// Media mix classes, or include one of unknown class, maps to nothing. Caller
// holds s.mu.
func (s *Store) entityMediaClassesLocked(ctx context.Context, ids []MediaID) (map[MediaID]MediaClass, error) {
	if len(ids) == 0 {
		return map[MediaID]MediaClass{}, nil
	}
	spans, err := s.entityMediaClassSpansLocked(ctx, ids, entityDirectMediaClassSQL)
	if err != nil {
		return nil, err
	}
	var bare []MediaID
	for _, id := range ids {
		if _, ok := spans[id]; !ok {
			bare = append(bare, id)
		}
	}
	if len(bare) > 0 {
		linked, err := s.entityMediaClassSpansLocked(ctx, bare, entityLinkedMediaClassSQL)
		if err != nil {
			return nil, err
		}
		maps.Copy(spans, linked)
	}
	classes := make(map[MediaID]MediaClass, len(spans))
	for id, span := range spans {
		if span.low != "" && span.low == span.high {
			classes[id] = span.low
		}
	}
	return classes, nil
}

type mediaClassSpan struct{ low, high MediaClass }

// entityDirectMediaClassSQL spans the classes of each Entity's directly linked Media.
const entityDirectMediaClassSQL = `
				SELECT r.from_entity_id, MIN(m.media_class), MAX(m.media_class)
				FROM entity_relation r
				JOIN media m ON m.id = r.to_media_id
				WHERE r.from_entity_id IN (%[1]s) AND m.trashed_at_ms IS NULL
				GROUP BY r.from_entity_id`

// entityLinkedMediaClassSQL spans the classes of the Media linked to each
// Entity's linked, untrashed Entities, following the link either way.
const entityLinkedMediaClassSQL = `
				WITH linked(item_id, other_id) AS (
					SELECT from_entity_id, to_entity_id FROM entity_relation
					WHERE from_entity_id IN (%[1]s) AND to_entity_id IS NOT NULL
					UNION
					SELECT to_entity_id, from_entity_id FROM entity_relation
					WHERE to_entity_id IN (%[1]s)
				)
				SELECT linked.item_id, MIN(m.media_class), MAX(m.media_class)
				FROM linked
				JOIN entity e ON e.id = linked.other_id AND e.trashed_at_ms IS NULL
				JOIN entity_relation r ON r.from_entity_id = linked.other_id
				JOIN media m ON m.id = r.to_media_id
				WHERE linked.other_id <> linked.item_id AND m.trashed_at_ms IS NULL
				GROUP BY linked.item_id`

// entityMediaClassSpansLocked runs a class-span query over every open catalog
// plane, widening each Entity's span across planes. Caller holds s.mu.
func (s *Store) entityMediaClassSpansLocked(ctx context.Context, ids []MediaID, query string) (map[MediaID]mediaClassSpan, error) {
	spans := map[MediaID]mediaClassSpan{}
	for _, reader := range s.openCatalogReadersLocked() {
		for start := 0; start < len(ids); start += engagementChunkSize {
			chunk := ids[start:min(start+engagementChunkSize, len(ids))]
			args := make([]any, len(chunk))
			for i, id := range chunk {
				args[i] = id
			}
			// The placeholders appear once per IN clause, so bind the IDs for each.
			bound := slices.Repeat(args, strings.Count(query, "%[1]s"))
			// #nosec G201 G202 -- only generated placeholders are interpolated; IDs are bound args.
			rows, err := reader.QueryContext(ctx, fmt.Sprintf(query, sqlPlaceholders(len(chunk))), bound...)
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
				spans[id] = mediaClassSpan{low: low, high: high}
			}
			if err := rows.Close(); err != nil {
				return nil, err
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
		}
	}
	return spans, nil
}

// annotateEntityYearsLocked sets Source card years from directly linked Media.
func (s *Store) annotateEntityYearsLocked(ctx context.Context, filter LibraryFilter, items []SourceItem) error {
	_, err := s.annotateEntityCardsLocked(ctx, filter, items, "")
	return err
}

// annotateEntityCardsLocked batches the earliest visible Media year and, when
// requested, the most common non-empty text value on the card's linked Media.
func (s *Store) annotateEntityCardsLocked(ctx context.Context, filter LibraryFilter, items []SourceItem, field string) (map[MediaID]string, error) {
	var ids []MediaID
	for _, item := range items {
		if item.Kind == "entity" {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	years := make(map[MediaID]int, len(ids))
	votes := map[MediaID]map[string]int{}
	read := func(reader *sql.DB, libraryIDs []LibraryID, certClause string, certArgs []any) error {
		return readEntityCardFacts(ctx, reader, string(encoded), field, libraryIDs, certClause, certArgs, years, votes)
	}
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
		if err := read(db.reader, []LibraryID{library.ID}, certClause, certArgs); err != nil {
			return nil, err
		}
	}
	if len(publicIDs) > 0 {
		certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
		if err := read(s.catalogR, publicIDs, certClause, certArgs); err != nil {
			return nil, err
		}
	}
	for i := range items {
		if items[i].Kind == "entity" {
			items[i].Year = years[items[i].ID]
		}
	}
	values := make(map[MediaID]string, len(votes))
	for id, counts := range votes {
		var winner string
		var best int
		for value, count := range counts {
			if count > best || (count == best && value < winner) {
				winner, best = value, count
			}
		}
		values[id] = winner
	}
	return values, nil
}

func readEntityCardFacts(ctx context.Context, reader *sql.DB, ids, field string, libraryIDs []LibraryID, certClause string, certArgs []any, years map[MediaID]int, votes map[MediaID]map[string]int) error {
	libraries, libraryArgs := libraryIDPlaceholders(libraryIDs)
	args := append([]any{field, ids}, libraryArgs...)
	args = append(args, certArgs...)
	// #nosec G202 -- only generated placeholders and closed certification SQL are interpolated.
	rows, err := reader.QueryContext(ctx, `
		SELECT r.from_entity_id, MIN(CASE WHEN m.year > 0 THEN m.year END),
		       NULLIF(f.value, ''), COUNT(DISTINCT m.id)
		FROM entity_relation r
		JOIN media m ON m.id = r.to_media_id
		LEFT JOIN media_field_text f ON f.media_id = m.id AND f.field = ? AND f.field_type = 'text'
		  AND EXISTS (SELECT 1 FROM media_file mf WHERE mf.media_id = m.id AND mf.missing_at_ms IS NULL)
		WHERE r.from_entity_id IN (SELECT value FROM json_each(?))
		  AND m.library_id IN (`+libraries+`)
		  AND m.trashed_at_ms IS NULL`+certClause+`
		GROUP BY r.from_entity_id, NULLIF(f.value, '')`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id MediaID
		var year sql.NullInt64
		var value sql.NullString
		var count int
		if err := rows.Scan(&id, &year, &value, &count); err != nil {
			return err
		}
		if current := years[id]; year.Valid && (current == 0 || int(year.Int64) < current) {
			years[id] = int(year.Int64)
		}
		if value.Valid {
			if votes[id] == nil {
				votes[id] = map[string]int{}
			}
			votes[id][value.String] += count
		}
	}
	return rows.Err()
}

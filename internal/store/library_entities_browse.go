package store

import (
	"context"
	"database/sql"
	"slices"
)

// libraryEntityCandidates reads the eligible untrashed Entities of one kind
// as browse candidates. Their other browse keys come from the Media they reach
// when a sort or filter needs them.
func libraryEntityCandidates(ctx context.Context, reader *sql.DB, query LibraryEntitiesQuery, restricted bool) ([]libraryBrowseCandidate, error) {
	statement := `SELECT id, title FROM entity e WHERE library_id = ? AND kind = ? AND trashed_at_ms IS NULL`
	args := []any{query.LibraryID, query.Kind}
	if clause, sourceArgs := entityBrowseSourceSQL(query.BrowseSources[query.Kind]); clause != "" {
		// #nosec G202 -- the predicate uses fixed SQL fragments and bound args.
		statement += ` AND ` + clause
		args = append(args, sourceArgs...)
	}
	rows, err := reader.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := []libraryBrowseCandidate{}
	for rows.Next() {
		var id EntityID
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		candidates = append(candidates, libraryBrowseCandidate{
			item: SourceItem{ID: MediaID(id), Kind: "entity", Title: title, LibraryID: query.LibraryID, Restricted: restricted},
		})
	}
	return candidates, rows.Err()
}

// libraryEntityBrowseFilters reports whether a browse narrows the listed set.
func libraryEntityBrowseFilters(query LibraryBrowseQuery) bool {
	return query.Watch != "" || len(query.EntityFilters) > 0 || query.YearFrom > 0 ||
		query.YearTo > 0 || query.Quality != "" || query.MinRating > 0
}

// libraryEntityBrowseReadsMedia reports whether a browse needs the Media each
// Entity reaches: any filter, or a sort on a key only Media carry.
func libraryEntityBrowseReadsMedia(query LibraryBrowseQuery) bool {
	switch query.Sort {
	case "", LibraryBrowseSortTitle, LibraryBrowseSortRandom:
		return libraryEntityBrowseFilters(query)
	}
	return true
}

// libraryEntityBrowseFilterLocked gives each Entity candidate the browse keys
// of the visible Media it reaches, then drops the candidates the browse
// filters reject. Reached Media are the Media linked to the Entity and to its
// child Entities; visibility is the Media browse's (certification, trash,
// extras, Mute), so an Entity is never judged by Media the caller cannot see.
// An Entity that reaches no visible Media survives only an unfiltered browse.
// The caller holds s.mu.
func (s *Store) libraryEntityBrowseFilterLocked(ctx context.Context, reader *sql.DB, filter LibraryFilter, query LibraryEntitiesQuery, candidates []libraryBrowseCandidate) ([]libraryBrowseCandidate, error) {
	browse := query.Browse
	set, err := s.libraryVisibleSetLocked(ctx, filter, browse.ProfileID)
	if err != nil {
		return nil, err
	}
	media := set.candidates
	if len(set.visible) > 0 {
		if err := s.libraryBrowseKeysLocked(ctx, set.reader, set.visible, browse, media); err != nil {
			return nil, err
		}
	}
	visible := make(map[MediaID]*libraryBrowseCandidate, len(media))
	for i := range media {
		visible[media[i].item.ID] = &media[i]
	}
	reached, err := libraryEntityReachedMedia(ctx, reader, query)
	if err != nil {
		return nil, err
	}
	keys := make(map[EntityID]*libraryEntityKeys, len(candidates))
	for entityID, ids := range reached {
		for _, mediaID := range ids {
			if m := visible[mediaID]; m != nil {
				if keys[entityID] == nil {
					keys[entityID] = &libraryEntityKeys{watched: true, tiers: map[string]bool{}}
				}
				keys[entityID].add(*m)
			}
		}
	}
	for i := range candidates {
		if k := keys[EntityID(candidates[i].item.ID)]; k != nil {
			k.apply(&candidates[i])
		}
	}
	if !libraryEntityBrowseFilters(browse) {
		return candidates, nil
	}

	candidates = slices.DeleteFunc(candidates, func(c libraryBrowseCandidate) bool {
		return keys[EntityID(c.item.ID)] == nil
	})
	for _, group := range browse.EntityFilters {
		linked, err := libraryEntityGroupMatches(ctx, reader, query.LibraryID, group)
		if err != nil {
			return nil, err
		}
		candidates = slices.DeleteFunc(candidates, func(c libraryBrowseCandidate) bool {
			id := EntityID(c.item.ID)
			if linked.entities[id] {
				return false
			}
			return !slices.ContainsFunc(reached[id], func(mediaID MediaID) bool {
				return visible[mediaID] != nil && linked.media[mediaID]
			})
		})
	}
	// Quality matches when any reached Media has the tier, so it is judged on
	// the tier set, not on the one height a candidate carries.
	rest := browse
	rest.Quality = ""
	return slices.DeleteFunc(candidates, func(c libraryBrowseCandidate) bool {
		if browse.Quality != "" && !keys[EntityID(c.item.ID)].tiers[browse.Quality] {
			return true
		}
		return !libraryBrowseMatches(c, rest)
	}), nil
}

// libraryEntityKeys accumulates one Entity's browse keys from its reached
// Media: the earliest year and release date, the latest added, the total
// duration and plays, the latest watched, the highest rating and audience
// score, whether all are watched and any is in progress, and every quality
// tier present.
type libraryEntityKeys struct {
	year        int
	rowID       int64
	duration    int
	lastWatched int64
	rating      int
	release     int64
	audience    int64
	plays       int64
	watched     bool
	inProgress  bool
	tiers       map[string]bool
}

func (k *libraryEntityKeys) add(m libraryBrowseCandidate) {
	if m.year > 0 && (k.year == 0 || m.year < k.year) {
		k.year = m.year
	}
	k.rowID = max(k.rowID, m.rowID)
	k.duration += max(m.duration, 0)
	k.lastWatched = max(k.lastWatched, m.lastWatched)
	k.rating = max(k.rating, m.rating)
	if m.release > 0 && (k.release == 0 || m.release < k.release) {
		k.release = m.release
	}
	k.audience = max(k.audience, m.audience)
	// plays carries the count plus one; the sum keeps that offset once.
	if m.plays > 0 {
		k.plays = max(k.plays, 1) + m.plays - 1
	}
	k.watched = k.watched && m.watched
	k.inProgress = k.inProgress || m.inProgress
	if m.height > 0 {
		k.tiers[QualityTierForHeight(m.height)] = true
	}
}

func (k *libraryEntityKeys) apply(c *libraryBrowseCandidate) {
	c.year = k.year
	c.rowID = k.rowID
	c.duration = k.duration
	c.lastWatched = k.lastWatched
	c.rating = k.rating
	c.release = k.release
	c.audience = k.audience
	c.plays = k.plays
	c.watched = k.watched
	c.inProgress = k.inProgress
}

// libraryEntityReachedMedia maps each untrashed Entity of the listed kind to
// the Media linked to it or to one of its child Entities: a linked Entity of a
// child kind, in either link direction, that is not also a parent kind (the
// rule libraryEntityLinks counts children by). Visibility is the caller's.
func libraryEntityReachedMedia(ctx context.Context, reader *sql.DB, query LibraryEntitiesQuery) (map[EntityID][]MediaID, error) {
	children := slices.DeleteFunc(slices.Clone(query.ChildKinds), func(kind string) bool {
		return slices.Contains(query.ParentKinds, kind)
	})
	args := []any{query.LibraryID, query.Kind}
	owners := `SELECT id, id FROM items`
	if len(children) > 0 {
		in := sqlPlaceholders(len(children))
		for range 2 {
			args = append(args, query.LibraryID)
			for _, kind := range children {
				args = append(args, kind)
			}
		}
		owners += `
			UNION
			SELECT items.id, e.id FROM items
			JOIN entity_relation r ON r.from_entity_id = items.id
			JOIN entity e ON e.id = r.to_entity_id
			WHERE e.id <> items.id AND e.library_id = ? AND e.trashed_at_ms IS NULL AND e.kind IN (` + in + `)
			UNION
			SELECT items.id, e.id FROM items
			JOIN entity_relation r ON r.to_entity_id = items.id
			JOIN entity e ON e.id = r.from_entity_id
			WHERE e.id <> items.id AND e.library_id = ? AND e.trashed_at_ms IS NULL AND e.kind IN (` + in + `)`
	}
	// #nosec G202 -- only generated placeholders are interpolated; ids and kinds are bound args.
	rows, err := reader.QueryContext(ctx, `
		WITH items(id) AS (
			SELECT id FROM entity WHERE library_id = ? AND kind = ? AND trashed_at_ms IS NULL
		),
		owners(item_id, owner_id) AS (`+owners+`)
		SELECT DISTINCT owners.item_id, r.to_media_id
		FROM owners
		JOIN entity_relation r ON r.from_entity_id = owners.owner_id
		WHERE r.to_media_id IS NOT NULL
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reached := map[EntityID][]MediaID{}
	for rows.Next() {
		var entityID EntityID
		var mediaID MediaID
		if err := rows.Scan(&entityID, &mediaID); err != nil {
			return nil, err
		}
		reached[entityID] = append(reached[entityID], mediaID)
	}
	return reached, rows.Err()
}

// libraryEntityGroupLinks is what one grouping value reaches: the Entities
// linked to it in either direction and the Media linked from it.
type libraryEntityGroupLinks struct {
	entities map[EntityID]bool
	media    map[MediaID]bool
}

// libraryEntityGroupMatches reads what the Library's untrashed grouping
// Entities of the filter's kind and title link to.
func libraryEntityGroupMatches(ctx context.Context, reader *sql.DB, libraryID LibraryID, group EntityFilter) (libraryEntityGroupLinks, error) {
	links := libraryEntityGroupLinks{entities: map[EntityID]bool{}, media: map[MediaID]bool{}}
	rows, err := reader.QueryContext(ctx, `
		WITH groups(id) AS (
			SELECT id FROM entity
			WHERE library_id = ? AND kind = ? AND title = ? AND trashed_at_ms IS NULL
		)
		SELECT 'media', r.to_media_id FROM entity_relation r JOIN groups g ON g.id = r.from_entity_id
		WHERE r.to_media_id IS NOT NULL
		UNION
		SELECT 'entity', r.to_entity_id FROM entity_relation r JOIN groups g ON g.id = r.from_entity_id
		WHERE r.to_entity_id IS NOT NULL
		UNION
		SELECT 'entity', r.from_entity_id FROM entity_relation r JOIN groups g ON g.id = r.to_entity_id
	`, libraryID, group.Kind, group.Name)
	if err != nil {
		return links, err
	}
	defer rows.Close()
	for rows.Next() {
		var side, id string
		if err := rows.Scan(&side, &id); err != nil {
			return links, err
		}
		if side == "media" {
			links.media[MediaID(id)] = true
		} else {
			links.entities[EntityID(id)] = true
		}
	}
	return links, rows.Err()
}

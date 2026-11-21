package store

import (
	"context"
	"database/sql"
	"slices"
)

// maxLibraryEntityParents caps how many parent Entities one listed Entity
// names; a card shows a short byline, not every link.
const maxLibraryEntityParents = 3

// LibraryEntitiesQuery asks for one page of a Library's Entities of one kind
// (CORE-2362). Kinds lists every kind to total, ChildKinds the kinds whose
// linked Entities count as an item's children, and ParentKinds the kinds whose
// linked Entities name an item's parents. The caller derives all three from
// the Library plugin's declarations; the store never chooses a kind itself. A
// Kind left out of Kinds is listed without being counted first, for a caller
// that already totalled it (CORE-2894).
type LibraryEntitiesQuery struct {
	LibraryID   LibraryID
	Kinds       []string
	Kind        string
	ChildKinds  []string
	ParentKinds []string
	// BrowseSources restricts each named kind to Entities matching any source.
	BrowseSources map[string][]EntityBrowseSource
	// Browse sorts and filters the listed kind the way LibraryBrowse sorts and
	// filters Media (CORE-2440); its LibraryID, FacetEntityKinds, Limit and
	// Offset are ignored. The zero value lists in title order, unfiltered.
	Browse LibraryBrowseQuery
	Limit  int
	Offset int
}

// LibraryEntityRef names one Entity linked to a listed Entity.
type LibraryEntityRef struct {
	ID    EntityID
	Title string
}

// LibraryEntity is one listed Entity with its counts. ChildCount counts the
// visible linked Entities of a child kind; MediaCount counts the distinct
// visible Media linked to the Entity or to one of those children.
type LibraryEntity struct {
	Item       SourceItem
	ChildCount int
	MediaCount int
	Parents    []LibraryEntityRef
}

// LibraryEntitiesResult is one page of a Library's Entities of one kind.
// Totals counts eligible Entities per kind. Total is the listed kind's count
// after the browse filters. Letters is where each title initial starts in the
// filtered set, for a title sort only.
type LibraryEntitiesResult struct {
	Totals  map[string]int
	Total   int
	Items   []LibraryEntity
	Letters []LibraryBrowseLetter
}

// LibraryEntities returns one page of a Library's Entities of one kind, in the
// browse order and filtered as query.Browse asks, with each kind's total. Visibility is the Library browse's: a
// Library the filter hides, or a sealed Encrypted Library, lists nothing, and
// trashed Entities are omitted. Media counts honour the certification
// ceiling, so a count never includes Media the caller cannot see.
func (s *Store) LibraryEntities(ctx context.Context, filter LibraryFilter, query LibraryEntitiesQuery) (LibraryEntitiesResult, error) {
	empty := LibraryEntitiesResult{Totals: map[string]int{}, Items: []LibraryEntity{}, Letters: []LibraryBrowseLetter{}}
	if query.LibraryID == "" {
		return empty, nil
	}
	if len(filter.AllowedIDs) == 0 && !filter.AllLibraries {
		return empty, nil
	}
	if len(filter.AllowedIDs) > 0 && !slices.Contains(filter.AllowedIDs, query.LibraryID) {
		return empty, nil
	}
	filter.AllowedIDs = []LibraryID{query.LibraryID}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return empty, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := max(query.Offset, 0)
	watch, err := s.effectiveBrowseWatchLocked(ctx, query.Browse)
	if err != nil {
		return empty, err
	}
	query.Browse.Watch = watch

	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return empty, err
	}
	var (
		found      bool
		restricted bool
		reader     = s.catalogR
		certify    = func() (string, []any) {
			return certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
		}
	)
	for _, library := range libraries {
		if library.ID != query.LibraryID || !libraryVisible(library, filter) {
			continue
		}
		if library.Encrypted {
			// A sealed Encrypted Library has no readable plane: it lists nothing.
			db := s.encryptedDBs[library.ID]
			if db == nil || db.state != "unsealed-registered" || db.reader == nil {
				continue
			}
			reader = db.reader
			pluginID := ""
			if library.PluginID != nil {
				pluginID = *library.PluginID
			}
			certify = func() (string, []any) {
				return planeCertificationFilterSQL(pluginID, filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
			}
		}
		found = true
		restricted = library.IsRestricted
	}
	if !found {
		return empty, nil
	}

	totals, err := libraryEntityTotals(ctx, reader, query)
	if err != nil {
		return empty, err
	}
	result := LibraryEntitiesResult{Totals: totals, Total: totals[query.Kind], Items: []LibraryEntity{}, Letters: []LibraryBrowseLetter{}}
	if query.Kind == "" || (slices.Contains(query.Kinds, query.Kind) && result.Total == 0) {
		return result, nil
	}
	candidates, err := libraryEntityCandidates(ctx, reader, query, restricted)
	if err != nil {
		return empty, err
	}
	candidates = filterLibraryBrowseText(candidates, query.Browse.Text)
	if candidates, err = s.dropMutedEntitiesLocked(ctx, reader, query.Browse.ProfileID, candidates); err != nil {
		return empty, err
	}
	if libraryEntityBrowseReadsMedia(query.Browse) {
		candidates, err = s.libraryEntityBrowseFilterLocked(ctx, reader, filter, query, candidates)
		if err != nil {
			return empty, err
		}
	}
	sortLibraryBrowse(candidates, query.Browse)
	if query.Browse.Sort == "" || query.Browse.Sort == LibraryBrowseSortTitle {
		result.Letters = libraryBrowseLetters(candidates)
	}
	result.Total = len(candidates)
	index := map[EntityID]int{}
	if offset < len(candidates) {
		for _, c := range candidates[offset:min(offset+limit, len(candidates))] {
			index[EntityID(c.item.ID)] = len(result.Items)
			result.Items = append(result.Items, LibraryEntity{Item: c.item})
		}
	}
	if len(result.Items) == 0 {
		return result, nil
	}
	children, err := libraryEntityLinks(ctx, reader, query, index, &result)
	if err != nil {
		return empty, err
	}
	if err := libraryEntityMediaCounts(ctx, reader, certify, query.LibraryID, index, children, &result); err != nil {
		return empty, err
	}
	return result, nil
}

// libraryEntityTotals counts the Library's eligible untrashed Entities of each kind.
func libraryEntityTotals(ctx context.Context, reader *sql.DB, query LibraryEntitiesQuery) (map[string]int, error) {
	totals := map[string]int{}
	if len(query.Kinds) == 0 {
		return totals, nil
	}
	args := make([]any, 0, 1+len(query.Kinds))
	args = append(args, query.LibraryID)
	for _, kind := range query.Kinds {
		args = append(args, kind)
	}
	var eligibility string
	for _, kind := range query.Kinds {
		if clause, sourceArgs := entityBrowseSourceSQL(query.BrowseSources[kind]); clause != "" {
			eligibility += ` AND (e.kind <> ? OR ` + clause + `)`
			args = append(args, kind)
			args = append(args, sourceArgs...)
		}
	}
	// #nosec G202 -- interpolated SQL is built from fixed fragments and placeholders.
	rows, err := reader.QueryContext(ctx, `
		SELECT kind, COUNT(*) FROM entity e
		WHERE library_id = ? AND trashed_at_ms IS NULL AND kind IN (`+sqlPlaceholders(len(query.Kinds))+`)`+eligibility+`
		GROUP BY kind
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			return nil, err
		}
		totals[kind] = count
	}
	return totals, rows.Err()
}

// libraryEntityLinks fills each listed Entity's child count and parents from
// the Entities linked to it in either direction, and returns each listed
// Entity's child Entity ids for the Media count. Parents keep relation sort
// order, then title.
func libraryEntityLinks(ctx context.Context, reader *sql.DB, query LibraryEntitiesQuery, index map[EntityID]int, result *LibraryEntitiesResult) (map[EntityID][]EntityID, error) {
	children := map[EntityID][]EntityID{}
	kinds := slices.Concat(query.ParentKinds, query.ChildKinds)
	if len(kinds) == 0 {
		return children, nil
	}
	ids := make([]any, 0, len(index))
	for i := range result.Items {
		ids = append(ids, EntityID(result.Items[i].Item.ID))
	}
	in := sqlPlaceholders(len(ids))
	args := make([]any, 0, 2*len(ids)+1+len(kinds))
	args = append(args, ids...)
	args = append(args, ids...)
	args = append(args, query.LibraryID)
	for _, kind := range kinds {
		args = append(args, kind)
	}
	// #nosec G202 -- only generated placeholders are interpolated; ids and kinds are bound args.
	rows, err := reader.QueryContext(ctx, `
		WITH linked(item_id, other_id, sort_order) AS (
			SELECT from_entity_id, to_entity_id, sort_order FROM entity_relation
			WHERE from_entity_id IN (`+in+`) AND to_entity_id IS NOT NULL
			UNION ALL
			SELECT to_entity_id, from_entity_id, sort_order FROM entity_relation
			WHERE to_entity_id IN (`+in+`)
		)
		SELECT linked.item_id, e.id, e.kind, e.title, MIN(linked.sort_order) AS link_order
		FROM linked
		JOIN entity e ON e.id = linked.other_id
		WHERE e.id <> linked.item_id AND e.library_id = ? AND e.trashed_at_ms IS NULL
			AND e.kind IN (`+sqlPlaceholders(len(kinds))+`)
		GROUP BY linked.item_id, e.id
		ORDER BY linked.item_id, (link_order IS NULL), link_order, e.title COLLATE NOCASE, e.id
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			itemID, otherID EntityID
			kind, title     string
			linkOrder       sql.NullInt64
		)
		if err := rows.Scan(&itemID, &otherID, &kind, &title, &linkOrder); err != nil {
			return nil, err
		}
		item := &result.Items[index[itemID]]
		// A kind declared both ways names a parent: the card's byline wins
		// over a count that would read the same link twice.
		if slices.Contains(query.ParentKinds, kind) {
			if len(item.Parents) < maxLibraryEntityParents {
				item.Parents = append(item.Parents, LibraryEntityRef{ID: otherID, Title: title})
			}
			continue
		}
		item.ChildCount++
		children[itemID] = append(children[itemID], otherID)
	}
	return children, rows.Err()
}

// libraryEntityMediaCounts sets each listed Entity's count of distinct visible
// Media linked to it or to one of its children.
func libraryEntityMediaCounts(ctx context.Context, reader *sql.DB, certify func() (string, []any), libraryID LibraryID, index map[EntityID]int, children map[EntityID][]EntityID, result *LibraryEntitiesResult) error {
	owners := map[EntityID][]EntityID{}
	for id := range index {
		owners[id] = append(owners[id], id)
	}
	for id, kids := range children {
		for _, kid := range kids {
			owners[kid] = append(owners[kid], id)
		}
	}
	ownerIDs := make([]any, 0, len(owners))
	for id := range owners {
		ownerIDs = append(ownerIDs, id)
	}
	certClause, certArgs := certify()
	media := map[EntityID]map[MediaID]bool{}
	for start := 0; start < len(ownerIDs); start += engagementChunkSize {
		chunk := ownerIDs[start:min(start+engagementChunkSize, len(ownerIDs))]
		args := make([]any, 0, len(chunk)+1+len(certArgs))
		args = append(args, chunk...)
		args = append(args, libraryID)
		args = append(args, certArgs...)
		rows, err := reader.QueryContext(ctx, libraryEntityMediaCountsSQL(len(chunk), certClause), args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var owner EntityID
			var mediaID MediaID
			if err := rows.Scan(&owner, &mediaID); err != nil {
				_ = rows.Close()
				return err
			}
			for _, item := range owners[owner] {
				if media[item] == nil {
					media[item] = map[MediaID]bool{}
				}
				media[item][mediaID] = true
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}
	for id, at := range index {
		result.Items[at].MediaCount = len(media[id])
	}
	return nil
}

// libraryEntityMediaCountsSQL selects each owner's visible Media for owners
// bound first, then the Library id and certClause's args. CROSS JOIN keeps r
// outermost, seeking the owners' relations by from_entity_id: with a bound id
// list SQLite otherwise walks every Media in the Library and probes its
// relations (CORE-2894).
func libraryEntityMediaCountsSQL(owners int, certClause string) string {
	// #nosec G202 -- only generated placeholders and closed certification SQL are interpolated.
	return `
		SELECT r.from_entity_id, m.id
		FROM entity_relation r
		CROSS JOIN media m ON m.id = r.to_media_id
		WHERE r.from_entity_id IN (` + sqlPlaceholders(owners) + `)
			AND m.trashed_at_ms IS NULL AND m.library_id = ?` + certClause
}

package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"slices"
	"sort"
	"strings"
)

// libraryBrowseEntityFacetCap bounds the Entity filter choices one read offers.
const libraryBrowseEntityFacetCap = 200

// Library browse sort keys. "added" orders by catalog insertion (the same
// m.rowid order the recently-added Source uses); "random" is the seeded
// permutation the random Source uses, so a shuffled set pages stably.
const (
	LibraryBrowseSortTitle       = "title"
	LibraryBrowseSortYear        = "year"
	LibraryBrowseSortAdded       = "added"
	LibraryBrowseSortDuration    = "duration"
	LibraryBrowseSortLastWatched = "last-watched"
	LibraryBrowseSortRandom      = "random"
)

// Library browse watch-state predicates, evaluated against the requesting
// Profile's playback progress.
const (
	LibraryBrowseWatchUnwatched  = "unwatched"
	LibraryBrowseWatchInProgress = "in-progress"
	LibraryBrowseWatchWatched    = "watched"
)

// LibraryBrowseQuery selects, filters, sorts and pages every Media of one
// Library the caller may see.
type LibraryBrowseQuery struct {
	LibraryID  LibraryID
	ProfileID  ProfileID
	Sort       string
	Descending bool
	// Seed keys the "random" sort; the same seed always yields the same order.
	Seed string
	// Watch is "", or one of the LibraryBrowseWatch* predicates.
	Watch string
	// EntityFilters keeps only Media related to every named Entity.
	EntityFilters []EntityFilter
	// YearFrom / YearTo bound the release year inclusively; 0 leaves a side open.
	YearFrom int
	YearTo   int
	// Quality is "" or one QualityTier* resolution bucket.
	Quality string
	// FacetEntityKinds names the Entity Kinds whose titles are reported as
	// facet values, in this order. Empty reports none.
	FacetEntityKinds []string
	Limit            int
	Offset           int
}

// LibraryBrowseFacets are the filter values present in the Library's visible
// set, before any filter is applied, so a client offers only choices that exist.
type LibraryBrowseFacets struct {
	Entities  []LibraryBrowseEntityFacet
	Decades   []int
	Qualities []string
}

// LibraryBrowseEntityFacet is the titles of one Entity Kind that the visible
// set actually reaches.
type LibraryBrowseEntityFacet struct {
	Kind   string
	Titles []string
}

// LibraryBrowseResult is one page of a Library browse plus the size of the
// whole filtered set.
type LibraryBrowseResult struct {
	Total  int
	Items  []SourceItem
	Facets LibraryBrowseFacets
}

type libraryBrowseCandidate struct {
	item        SourceItem
	rowID       int64
	year        int
	duration    int
	height      int
	watched     bool
	inProgress  bool
	lastWatched int64
}

// ValidLibraryBrowseSort reports whether sortKey is a known sort ("" is title).
func ValidLibraryBrowseSort(sortKey string) bool {
	switch sortKey {
	case "", LibraryBrowseSortTitle, LibraryBrowseSortYear, LibraryBrowseSortAdded,
		LibraryBrowseSortDuration, LibraryBrowseSortLastWatched, LibraryBrowseSortRandom:
		return true
	}
	return false
}

// ValidLibraryBrowseWatch reports whether watch is a known predicate ("" is none).
func ValidLibraryBrowseWatch(watch string) bool {
	switch watch {
	case "", LibraryBrowseWatchUnwatched, LibraryBrowseWatchInProgress, LibraryBrowseWatchWatched:
		return true
	}
	return false
}

// ValidLibraryBrowseQuality reports whether quality is a known tier ("" is none).
func ValidLibraryBrowseQuality(quality string) bool {
	return quality == "" || qualityTiers[quality]
}

// LibraryBrowse returns one page of a Library's visible Media with the total
// size of the filtered set. Visibility (reveal, allow-list, certification,
// trash, extras, per-Profile Mute) is the all-items Source's, so a browse never
// returns or counts an item a rail would hide. Watch state lives in a separate
// database from the catalog, so the candidate set is merged, filtered and
// sorted here rather than in one SQL statement.
func (s *Store) LibraryBrowse(ctx context.Context, filter LibraryFilter, query LibraryBrowseQuery) (LibraryBrowseResult, error) {
	empty := LibraryBrowseResult{Items: []SourceItem{}}
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

	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return empty, err
	}
	// AllowedIDs is the one browsed Library, so at most one is visible and its
	// catalog plane holds every row this read needs.
	var visible []LibraryID
	reader := s.catalogR
	// certify renders the certification clause for the plane being read: an
	// Encrypted Library's own catalog has no library table to join.
	certify := func() (string, []any) {
		return certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	}
	for _, library := range libraries {
		if !libraryVisible(library, filter) {
			continue
		}
		if library.Encrypted {
			// A sealed Encrypted Library has no readable plane: it browses empty.
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
		visible = append(visible, library.ID)
	}
	if len(visible) == 0 {
		return empty, nil
	}

	candidates, err := libraryBrowseCandidates(ctx, reader, certify, visible, nil)
	if err != nil {
		return empty, err
	}
	if query.ProfileID != "" {
		candidates, err = s.libraryBrowseUnmutedLocked(ctx, reader, query.ProfileID, filter, candidates)
		if err != nil {
			return empty, err
		}
	}
	result := LibraryBrowseResult{Items: []SourceItem{}}
	result.Facets = libraryBrowseFacets(candidates)
	for _, kind := range query.FacetEntityKinds {
		titles, err := libraryBrowseEntityTitles(ctx, reader, kind, visible, candidates)
		if err != nil {
			return empty, err
		}
		result.Facets.Entities = append(result.Facets.Entities, LibraryBrowseEntityFacet{Kind: kind, Titles: titles})
	}

	for i := range query.EntityFilters {
		related, err := libraryBrowseCandidates(ctx, reader, certify, visible, &query.EntityFilters[i])
		if err != nil {
			return empty, err
		}
		keep := make(map[MediaID]bool, len(related))
		for _, c := range related {
			keep[c.item.ID] = true
		}
		candidates = slices.DeleteFunc(candidates, func(c libraryBrowseCandidate) bool { return !keep[c.item.ID] })
	}
	if query.ProfileID != "" && (query.Watch != "" || query.Sort == LibraryBrowseSortLastWatched) {
		if err := s.libraryBrowseProgressLocked(ctx, query.ProfileID, candidates); err != nil {
			return empty, err
		}
	}
	candidates = slices.DeleteFunc(candidates, func(c libraryBrowseCandidate) bool {
		return !libraryBrowseMatches(c, query)
	})
	sortLibraryBrowse(candidates, query)

	result.Total = len(candidates)
	if offset < len(candidates) {
		page := candidates[offset:min(offset+limit, len(candidates))]
		result.Items = make([]SourceItem, len(page))
		for i, c := range page {
			result.Items[i] = c.item
		}
	}
	if err := s.annotateRestrictedLocked(ctx, nil, result.Items); err != nil {
		return empty, err
	}
	if !filter.PlaybackGate {
		if err := s.annotateLockedLocked(ctx, filter.Certification, result.Items, filter.GrantedMediaIDs); err != nil {
			return empty, err
		}
	}
	return result, nil
}

func libraryBrowseCandidates(ctx context.Context, reader *sql.DB, certify func() (string, []any), visible []LibraryID, entityFilter *EntityFilter) ([]libraryBrowseCandidate, error) {
	placeholders := make([]string, len(visible))
	args := make([]any, 0, len(visible))
	for i, id := range visible {
		placeholders[i] = "?"
		args = append(args, id)
	}
	entityClause, entityArgs := entityRelationFilterSQL(entityFilter, visible)
	args = append(args, entityArgs...)
	certClause, certArgs := certify()
	args = append(args, certArgs...)
	// #nosec G202 -- interpolated fragments are internally generated placeholders and closed filter SQL.
	rows, err := reader.QueryContext(ctx, `
		SELECT m.id, m.title, m.duration_seconds, COALESCE(m.year, 0), m.video_height, m.rowid
		FROM media m
		WHERE m.library_id IN (`+strings.Join(placeholders, ", ")+`)
		  AND m.trashed_at_ms IS NULL`+mediaExcludeExtrasClause+`
		  AND (NOT EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id)
		       OR EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id AND f.missing_at_ms IS NULL))`+entityClause+certClause+`
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := []libraryBrowseCandidate{}
	for rows.Next() {
		var c libraryBrowseCandidate
		if err := rows.Scan(&c.item.ID, &c.item.Title, &c.duration, &c.year, &c.height, &c.rowID); err != nil {
			return nil, err
		}
		c.item.Kind = "media"
		c.item.Summary = mediaDurationSummary(c.duration)
		candidates = append(candidates, c)
	}
	return candidates, rows.Err()
}

// libraryBrowseUnmutedLocked applies the per-Profile Mute predicate to the
// whole candidate set, before counting, so the total never counts a hidden item.
// relations is the plane the candidates were read from: an Encrypted Library
// keeps its relations in its own catalog.
func (s *Store) libraryBrowseUnmutedLocked(ctx context.Context, relations *sql.DB, profileID ProfileID, filter LibraryFilter, candidates []libraryBrowseCandidate) ([]libraryBrowseCandidate, error) {
	items := make([]SourceItem, len(candidates))
	for i, c := range candidates {
		items[i] = c.item
	}
	kept, err := s.applyMuteFilterLocked(ctx, relations, profileID, filter, items)
	if err != nil {
		return nil, err
	}
	if len(kept) == len(items) {
		return candidates, nil
	}
	keep := make(map[MediaID]bool, len(kept))
	for _, item := range kept {
		keep[item.ID] = true
	}
	return slices.DeleteFunc(candidates, func(c libraryBrowseCandidate) bool { return !keep[c.item.ID] }), nil
}

func (s *Store) libraryBrowseProgressLocked(ctx context.Context, profileID ProfileID, candidates []libraryBrowseCandidate) error {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id, watched, position_seconds, channel_context,
		       COALESCE(last_played_at_ms, updated_at_ms)
		FROM playback_progress
		WHERE profile_id = ?
	`, profileID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type progress struct {
		watched     bool
		inProgress  bool
		lastWatched int64
	}
	byMedia := map[MediaID]progress{}
	for rows.Next() {
		var id MediaID
		var watched, position, channelContext int
		var last int64
		if err := rows.Scan(&id, &watched, &position, &channelContext, &last); err != nil {
			return err
		}
		byMedia[id] = progress{
			watched:     watched != 0,
			inProgress:  watched == 0 && position >= resumeFloorSeconds && channelContext == 0,
			lastWatched: last,
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range candidates {
		if p, ok := byMedia[candidates[i].item.ID]; ok {
			candidates[i].watched = p.watched
			candidates[i].inProgress = p.inProgress
			candidates[i].lastWatched = p.lastWatched
		}
	}
	return nil
}

// libraryBrowseEntityTitles lists the Entities of one kind that at least one
// visible candidate belongs to. It is built from the candidates, never from the
// Library's whole Entity table, so a title reachable only through Media the
// caller cannot see (certification, Mute) is not offered as a filter.
func libraryBrowseEntityTitles(ctx context.Context, reader *sql.DB, kind string, visible []LibraryID, candidates []libraryBrowseCandidate) ([]string, error) {
	placeholders := make([]string, len(visible))
	args := make([]any, 0, len(visible)+1)
	args = append(args, kind)
	for i, id := range visible {
		placeholders[i] = "?"
		args = append(args, id)
	}
	// #nosec G202 -- only generated placeholders are interpolated.
	rows, err := reader.QueryContext(ctx, `
		SELECT e.title, r.to_media_id
		FROM entity e
		JOIN entity_relation r ON r.from_entity_id = e.id
		WHERE e.kind = ? AND e.library_id IN (`+strings.Join(placeholders, ", ")+`)
		  AND e.trashed_at_ms IS NULL AND r.to_media_id IS NOT NULL
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	shown := make(map[MediaID]bool, len(candidates))
	for _, c := range candidates {
		shown[c.item.ID] = true
	}
	seen := map[string]bool{}
	titles := []string{}
	for rows.Next() {
		var title string
		var mediaID MediaID
		if err := rows.Scan(&title, &mediaID); err != nil {
			return nil, err
		}
		if shown[mediaID] && !seen[title] {
			seen[title] = true
			titles = append(titles, title)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(titles, func(a, b string) int {
		if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	return titles[:min(len(titles), libraryBrowseEntityFacetCap)], nil
}

func libraryBrowseFacets(candidates []libraryBrowseCandidate) LibraryBrowseFacets {
	decades := map[int]bool{}
	tiers := map[string]bool{}
	for _, c := range candidates {
		if c.year > 0 {
			decades[c.year/10*10] = true
		}
		if c.height > 0 {
			tiers[QualityTierForHeight(c.height)] = true
		}
	}
	facets := LibraryBrowseFacets{Entities: []LibraryBrowseEntityFacet{}, Decades: make([]int, 0, len(decades)), Qualities: []string{}}
	for decade := range decades {
		facets.Decades = append(facets.Decades, decade)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(facets.Decades)))
	for _, tier := range []string{QualityTierUHD, QualityTierHD1080, QualityTierHD720, QualityTierSD} {
		if tiers[tier] {
			facets.Qualities = append(facets.Qualities, tier)
		}
	}
	return facets
}

func libraryBrowseMatches(c libraryBrowseCandidate, query LibraryBrowseQuery) bool {
	switch query.Watch {
	case LibraryBrowseWatchUnwatched:
		if c.watched {
			return false
		}
	case LibraryBrowseWatchInProgress:
		if !c.inProgress {
			return false
		}
	case LibraryBrowseWatchWatched:
		if !c.watched {
			return false
		}
	}
	if query.YearFrom > 0 && c.year < query.YearFrom {
		return false
	}
	if query.YearTo > 0 && (c.year == 0 || c.year > query.YearTo) {
		return false
	}
	// An un-probed item (height 0) has no known tier, so it matches no quality.
	if query.Quality != "" && (c.height <= 0 || QualityTierForHeight(c.height) != query.Quality) {
		return false
	}
	return true
}

// sortLibraryBrowse orders candidates by the query's key. Items missing the key
// (no year, no duration, never watched) sort last in BOTH directions so a
// descending sort does not open on a wall of blanks; every order is made total
// by a title-then-id tie-break.
func sortLibraryBrowse(candidates []libraryBrowseCandidate, query LibraryBrowseQuery) {
	if query.Sort == LibraryBrowseSortRandom {
		keys := make(map[MediaID][sha256.Size]byte, len(candidates))
		for _, c := range candidates {
			keys[c.item.ID] = sha256.Sum256([]byte(query.Seed + "\x00" + string(c.item.ID)))
		}
		sort.Slice(candidates, func(i, j int) bool {
			a, b := keys[candidates[i].item.ID], keys[candidates[j].item.ID]
			if cmp := bytes.Compare(a[:], b[:]); cmp != 0 {
				return cmp < 0
			}
			return candidates[i].item.ID < candidates[j].item.ID
		})
		return
	}
	titles := make(map[MediaID]string, len(candidates))
	for _, c := range candidates {
		titles[c.item.ID] = strings.ToLower(c.item.Title)
	}
	byTitle := func(a, b libraryBrowseCandidate) int {
		if cmp := strings.Compare(titles[a.item.ID], titles[b.item.ID]); cmp != 0 {
			return cmp
		}
		return strings.Compare(string(a.item.ID), string(b.item.ID))
	}
	var key func(libraryBrowseCandidate) int64
	switch query.Sort {
	case LibraryBrowseSortYear:
		key = func(c libraryBrowseCandidate) int64 { return int64(c.year) }
	case LibraryBrowseSortAdded:
		key = func(c libraryBrowseCandidate) int64 { return c.rowID }
	case LibraryBrowseSortDuration:
		key = func(c libraryBrowseCandidate) int64 { return int64(c.duration) }
	case LibraryBrowseSortLastWatched:
		key = func(c libraryBrowseCandidate) int64 { return c.lastWatched }
	}
	slices.SortFunc(candidates, func(a, b libraryBrowseCandidate) int {
		if key == nil {
			if query.Descending {
				return byTitle(b, a)
			}
			return byTitle(a, b)
		}
		ka, kb := key(a), key(b)
		switch {
		case ka == kb:
			return byTitle(a, b)
		case ka <= 0:
			return 1
		case kb <= 0:
			return -1
		case (ka < kb) != query.Descending:
			return -1
		}
		return 1
	})
}

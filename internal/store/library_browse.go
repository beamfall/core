package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"math"
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
	// LibraryBrowseSortRating orders by the requesting Profile's own rating.
	LibraryBrowseSortRating = "rating"
	// LibraryBrowseSortReleaseDate orders by the core-conventional
	// release-date field, else 1 January of the Media's year (CORE-2443).
	LibraryBrowseSortReleaseDate = "release-date"
	// LibraryBrowseSortAudienceScore orders by the 0-10 audience score.
	LibraryBrowseSortAudienceScore = "audience-score"
	// LibraryBrowseSortPlays orders by the requesting Profile's play count.
	LibraryBrowseSortPlays = "plays"
)

// releaseDateField is the core-conventional Media date field a client renders
// as the release date; the values are plugin data.
const releaseDateField = "release-date"

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
	// MinRating keeps only Media the requesting Profile rated at least this
	// high (1..ItemRatingMax); 0 applies no rating filter.
	MinRating int
	// Text keeps only items whose title or sort title holds every word of
	// it, ignoring case, accents and punctuation (CORE-2568); "" keeps all.
	Text string
	// FacetEntityKinds names the Entity Kinds whose titles are reported as
	// facet values, in this order. Empty reports none.
	FacetEntityKinds []string
	// FacetMembers retains membership only for category browse-unit counts.
	FacetMembers bool
	Limit        int
	Offset       int
}

// LibraryBrowseFacets are the filter values present in the Library's visible
// set, before any filter is applied, so a client offers only choices that exist.
type LibraryBrowseFacets struct {
	Entities  []LibraryBrowseEntityFacet
	Decades   []int
	Qualities []string
}

// LibraryBrowseEntityFacet is the titles of one Entity Kind that the visible
// set actually reaches, in title order. Counts is parallel to Titles: how many
// distinct visible Media reach each title.
type LibraryBrowseEntityFacet struct {
	Kind   string
	Titles []string
	Counts []int
	// Members retains visible membership for browse-unit counting at the graph boundary.
	Members [][]MediaID
}

// LibraryBrowseResult is one page of a Library browse plus the size of the
// whole filtered set.
type LibraryBrowseResult struct {
	Total  int
	Items  []SourceItem
	Facets LibraryBrowseFacets
	// Letters is where each title initial starts in the filtered set, for a
	// title sort only; any other order leaves it empty.
	Letters []LibraryBrowseLetter
}

type libraryBrowseCandidate struct {
	item        SourceItem
	rowID       int64
	year        int
	duration    int
	width       int
	height      int
	watched     bool
	inProgress  bool
	lastWatched int64
	rating      int
	// release is the release date as a YYYYMMDD number (a year alone is its
	// 1 January), audience the audience score in hundredths plus one, and
	// plays the Profile's play count plus one; 0 means the key is unknown.
	release  int64
	audience int64
	plays    int64
	// sortTitle is the admin- or provider-set sort title, "" when none is set.
	sortTitle string
}

// titleKey is the title a candidate sorts and letter-indexes by: its sort
// title when it has one, else its title (COALESCE(sort title, title)).
func (c libraryBrowseCandidate) titleKey() string {
	if c.sortTitle != "" {
		return c.sortTitle
	}
	return c.item.Title
}

// ValidLibraryBrowseSort reports whether sortKey is a known sort ("" is title).
func ValidLibraryBrowseSort(sortKey string) bool {
	switch sortKey {
	case "", LibraryBrowseSortTitle, LibraryBrowseSortYear, LibraryBrowseSortAdded,
		LibraryBrowseSortDuration, LibraryBrowseSortLastWatched, LibraryBrowseSortRandom,
		LibraryBrowseSortRating, LibraryBrowseSortReleaseDate, LibraryBrowseSortAudienceScore,
		LibraryBrowseSortPlays:
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
	empty := LibraryBrowseResult{Items: []SourceItem{}, Letters: []LibraryBrowseLetter{}}
	filter, ok := narrowLibraryFilter(filter, query.LibraryID)
	if !ok {
		return empty, nil
	}

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
	watch, err := s.effectiveBrowseWatchLocked(ctx, query)
	if err != nil {
		return empty, err
	}
	query.Watch = watch

	set, err := s.libraryVisibleSetLocked(ctx, filter, query.ProfileID)
	if err != nil || len(set.visible) == 0 {
		return empty, err
	}
	reader, visible, certify, candidates := set.reader, set.visible, set.certify, set.candidates
	result := LibraryBrowseResult{Items: []SourceItem{}, Letters: []LibraryBrowseLetter{}}
	result.Facets = libraryBrowseFacets(candidates)
	for _, kind := range query.FacetEntityKinds {
		facet, err := libraryBrowseEntityTitles(ctx, reader, kind, visible, candidates, query.FacetMembers)
		if err != nil {
			return empty, err
		}
		result.Facets.Entities = append(result.Facets.Entities, facet)
	}

	candidates = filterLibraryBrowseText(candidates, query.Text)
	for i := range query.EntityFilters {
		related, err := libraryBrowseCandidates(ctx, reader, certify, visible, &query.EntityFilters[i], nil)
		if err != nil {
			return empty, err
		}
		keep := make(map[MediaID]bool, len(related))
		for _, c := range related {
			keep[c.item.ID] = true
		}
		candidates = slices.DeleteFunc(candidates, func(c libraryBrowseCandidate) bool { return !keep[c.item.ID] })
	}
	if err := s.libraryBrowseKeysLocked(ctx, reader, visible, query, candidates); err != nil {
		return empty, err
	}
	candidates = slices.DeleteFunc(candidates, func(c libraryBrowseCandidate) bool {
		return !libraryBrowseMatches(c, query)
	})
	sortLibraryBrowse(candidates, query)
	if query.Sort == "" || query.Sort == LibraryBrowseSortTitle {
		result.Letters = libraryBrowseLetters(candidates)
	}

	result.Total = len(candidates)
	if offset < len(candidates) {
		page := candidates[offset:min(offset+limit, len(candidates))]
		result.Items = make([]SourceItem, len(page))
		for i, c := range page {
			result.Items[i] = c.item
		}
	}
	if err := s.annotateLibraryItemsLocked(ctx, reader, filter, query.ProfileID, result.Items); err != nil {
		return empty, err
	}
	return result, nil
}

// annotateLibraryItemsLocked badges one page of a Library view's items:
// restricted, muted and locked. The Mute badge is read from the browsed
// Library's own plane, the same one the Mute filter read, so an Encrypted
// Library's items are badged too. The caller holds s.mu.
func (s *Store) annotateLibraryItemsLocked(ctx context.Context, reader *sql.DB, filter LibraryFilter, profileID ProfileID, items []SourceItem) error {
	if err := s.annotateRestrictedLocked(ctx, nil, items); err != nil {
		return err
	}
	if profileID != "" {
		if err := s.annotateMutedLocked(ctx, reader, profileID, items); err != nil {
			return err
		}
	}
	if !filter.PlaybackGate {
		if err := s.annotateLockedLocked(ctx, filter.Certification, items, filter.GrantedMediaIDs); err != nil {
			return err
		}
	}
	return nil
}

func libraryBrowseCandidates(ctx context.Context, reader *sql.DB, certify func() (string, []any), visible []LibraryID, entityFilter *EntityFilter, only []MediaID) ([]libraryBrowseCandidate, error) {
	placeholders := make([]string, len(visible))
	args := make([]any, 0, len(visible))
	for i, id := range visible {
		placeholders[i] = "?"
		args = append(args, id)
	}
	entityClause, entityArgs := entityRelationFilterSQL(entityFilter, visible, true)
	args = append(args, entityArgs...)
	certClause, certArgs := certify()
	args = append(args, certArgs...)
	// only, when set, narrows the read to those Media. The library term is
	// then written +m.library_id so the planner reads those rows by primary
	// key rather than scanning the Library.
	onlyClause, libraryColumn := "", "m.library_id"
	if len(only) > 0 {
		libraryColumn = "+m.library_id"
		onlyClause = " AND m.id IN (" + sqlPlaceholders(len(only)) + ")"
		for _, id := range only {
			args = append(args, string(id))
		}
	}
	// #nosec G202 -- interpolated fragments are internally generated placeholders and closed filter SQL.
	rows, err := reader.QueryContext(ctx, `
		SELECT m.id, m.title, m.library_id, m.duration_seconds, COALESCE(m.year, 0), m.video_width, m.video_height, m.rowid
		FROM media m
		WHERE `+libraryColumn+` IN (`+strings.Join(placeholders, ", ")+`)
		  AND m.trashed_at_ms IS NULL`+mediaExcludeExtrasClause+`
		  AND (NOT EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id)
		       OR EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id AND f.missing_at_ms IS NULL))`+entityClause+certClause+onlyClause+`
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := []libraryBrowseCandidate{}
	for rows.Next() {
		var c libraryBrowseCandidate
		if err := rows.Scan(&c.item.ID, &c.item.Title, &c.item.LibraryID, &c.duration, &c.year, &c.width, &c.height, &c.rowID); err != nil {
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
		       last_played_at_ms, updated_at_ms, play_count
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
		plays       int64
	}
	byMedia := map[MediaID]progress{}
	for rows.Next() {
		var id MediaID
		var watched, position, channelContext int
		var played sql.NullInt64
		var updated, plays int64
		if err := rows.Scan(&id, &watched, &position, &channelContext, &played, &updated, &plays); err != nil {
			return err
		}
		// A real play time always counts. Without one the row's update time
		// stands in, unless nothing is watched on it: that is a cleared mark.
		var last int64
		switch {
		case played.Valid:
			last = played.Int64
		case watched != 0 || position != 0:
			last = updated
		}
		byMedia[id] = progress{
			watched:     watched != 0,
			inProgress:  watched == 0 && position >= resumeFloorSeconds && channelContext == 0,
			lastWatched: last,
			plays:       max(plays, 0),
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range candidates {
		p := byMedia[candidates[i].item.ID]
		candidates[i].watched = p.watched
		candidates[i].inProgress = p.inProgress
		candidates[i].lastWatched = p.lastWatched
		// An unplayed item has a known count of 0.
		candidates[i].plays = p.plays + 1
	}
	return nil
}

// libraryBrowseKeysLocked attaches the browse keys the query's filters and
// sort read beyond the catalog row: the Profile's progress, play counts and
// ratings, and the release date and audience score fields. visible names the
// Libraries the candidates were read from. The caller holds s.mu.
func (s *Store) libraryBrowseKeysLocked(ctx context.Context, reader *sql.DB, visible []LibraryID, query LibraryBrowseQuery, candidates []libraryBrowseCandidate) error {
	if query.ProfileID != "" && (query.Watch != "" || query.Sort == LibraryBrowseSortLastWatched || query.Sort == LibraryBrowseSortPlays) {
		if err := s.libraryBrowseProgressLocked(ctx, query.ProfileID, candidates); err != nil {
			return err
		}
	}
	if query.ProfileID != "" && (query.MinRating > 0 || query.Sort == LibraryBrowseSortRating) {
		if err := s.libraryBrowseRatingsLocked(ctx, query.ProfileID, candidates); err != nil {
			return err
		}
	}
	switch query.Sort {
	case LibraryBrowseSortReleaseDate:
		return libraryBrowseReleaseDates(ctx, reader, visible, candidates)
	case LibraryBrowseSortAudienceScore:
		return libraryBrowseAudienceScores(ctx, reader, visible, candidates)
	}
	return nil
}

// libraryBrowseFieldRows reads one Media field's values, from the given side
// table and value column, for the Media of the visible Libraries.
func libraryBrowseFieldRows(ctx context.Context, reader *sql.DB, table, column, field string, visible []LibraryID, scan func(*sql.Rows) error) error {
	args := make([]any, 0, len(visible)+1)
	args = append(args, field)
	for _, id := range visible {
		args = append(args, id)
	}
	// #nosec G202 -- table and column are closed internal names; only generated placeholders are interpolated.
	rows, err := reader.QueryContext(ctx, `
		SELECT f.media_id, f.`+column+`
		FROM `+table+` f JOIN media m ON m.id = f.media_id
		WHERE f.field = ? AND m.library_id IN (`+sqlPlaceholders(len(visible))+`)
	`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// libraryBrowseReleaseDates attaches each candidate's release date: its
// release-date field, else 1 January of its year.
func libraryBrowseReleaseDates(ctx context.Context, reader *sql.DB, visible []LibraryID, candidates []libraryBrowseCandidate) error {
	byMedia := map[MediaID]int64{}
	err := libraryBrowseFieldRows(ctx, reader, "media_field_date", "value_ms", releaseDateField, visible, func(rows *sql.Rows) error {
		var id MediaID
		var ms int64
		if err := rows.Scan(&id, &ms); err != nil {
			return err
		}
		if day := millisToTime(ms).UTC(); day.Year() > 0 {
			byMedia[id] = int64(day.Year())*10000 + int64(day.Month())*100 + int64(day.Day())
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := range candidates {
		c := &candidates[i]
		if release, ok := byMedia[c.item.ID]; ok {
			c.release = release
		} else if c.year > 0 {
			c.release = int64(c.year)*10000 + 101
		}
	}
	return nil
}

// libraryBrowseAudienceScores attaches each candidate's 0-10 audience score
// by the rule cards use: a value off that scale, a zero, or a score
// with a stored zero vote count is no score (CORE-2524).
func libraryBrowseAudienceScores(ctx context.Context, reader *sql.DB, visible []LibraryID, candidates []libraryBrowseCandidate) error {
	votes := map[MediaID]sql.NullFloat64{}
	err := libraryBrowseFieldRows(ctx, reader, "media_field_real", "value", audienceScoreVotesField, visible, func(rows *sql.Rows) error {
		var id MediaID
		var raw sql.NullFloat64
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		votes[id] = raw
		return nil
	})
	if err != nil {
		return err
	}
	byMedia := map[MediaID]int64{}
	err = libraryBrowseFieldRows(ctx, reader, "media_field_real", "value", audienceScoreField, visible, func(rows *sql.Rows) error {
		var id MediaID
		var raw sql.NullFloat64
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		if score := votedAudienceScore(raw, votes[id]); score != nil {
			byMedia[id] = int64(math.Round(*score*100)) + 1
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := range candidates {
		candidates[i].audience = byMedia[candidates[i].item.ID]
	}
	return nil
}

// libraryBrowseRatingsLocked attaches the Profile's own Media ratings.
func (s *Store) libraryBrowseRatingsLocked(ctx context.Context, profileID ProfileID, candidates []libraryBrowseCandidate) error {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT target_id, rating
		FROM item_rating
		WHERE profile_id = ? AND target_kind = ?
	`, profileID, ItemTargetMedia)
	if err != nil {
		return err
	}
	defer rows.Close()
	byMedia := map[MediaID]int{}
	for rows.Next() {
		var id MediaID
		var rating int
		if err := rows.Scan(&id, &rating); err != nil {
			return err
		}
		byMedia[id] = rating
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range candidates {
		candidates[i].rating = byMedia[candidates[i].item.ID]
	}
	return nil
}

// libraryBrowseEntityTitles lists the Entities of one kind that at least one
// visible candidate belongs to, each with how many distinct visible candidates
// reach it. It is built from the candidates, never from the Library's whole
// Entity table, so a title reachable only through Media the caller cannot see
// (certification, Mute) is not offered as a filter and never counted. Past the
// cap it keeps the most used titles (ties by title), then answers in title
// order.
func libraryBrowseEntityTitles(ctx context.Context, reader *sql.DB, kind string, visible []LibraryID, candidates []libraryBrowseCandidate, includeMembers bool) (LibraryBrowseEntityFacet, error) {
	facet := LibraryBrowseEntityFacet{Kind: kind, Titles: []string{}, Counts: []int{}}
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
		return facet, err
	}
	defer rows.Close()
	shown := make(map[MediaID]bool, len(candidates))
	for _, c := range candidates {
		shown[c.item.ID] = true
	}
	// Two Entities may share a title; a Media reaching both counts once.
	reached := map[string]map[MediaID]bool{}
	for rows.Next() {
		var title string
		var mediaID MediaID
		if err := rows.Scan(&title, &mediaID); err != nil {
			return facet, err
		}
		if !shown[mediaID] {
			continue
		}
		if reached[title] == nil {
			reached[title] = map[MediaID]bool{}
		}
		reached[title][mediaID] = true
	}
	if err := rows.Err(); err != nil {
		return facet, err
	}
	counts := make(map[string]int, len(reached))
	for title, media := range reached {
		counts[title] = len(media)
	}
	facet.Titles = topEntityTitles(counts, libraryBrowseEntityFacetCap)
	facet.Counts = make([]int, len(facet.Titles))
	if includeMembers {
		facet.Members = make([][]MediaID, len(facet.Titles))
	}
	for i, title := range facet.Titles {
		facet.Counts[i] = counts[title]
		if includeMembers {
			for id := range reached[title] {
				facet.Members[i] = append(facet.Members[i], id)
			}
		}
	}
	return facet, nil
}

// topEntityTitles keeps at most limit titles, the ones with the highest counts
// (ties by title), and answers them in title order: case-insensitive, then
// exact.
func topEntityTitles(counts map[string]int, limit int) []string {
	byTitle := func(a, b string) int {
		if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	}
	titles := make([]string, 0, len(counts))
	for title := range counts {
		titles = append(titles, title)
	}
	if len(titles) > limit {
		slices.SortFunc(titles, func(a, b string) int {
			if c := counts[b] - counts[a]; c != 0 {
				return c
			}
			return byTitle(a, b)
		})
		titles = titles[:limit]
	}
	slices.SortFunc(titles, byTitle)
	return titles
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

// filterLibraryBrowseText drops the candidates whose title and sort title do
// not each hold every word of text, folded as keyword search folds them. It
// runs on the candidates themselves, so an Entity listing matches the
// Entity's own title, not the titles of the Media it reaches.
func filterLibraryBrowseText(candidates []libraryBrowseCandidate, text string) []libraryBrowseCandidate {
	words := strings.Fields(foldSearchText(text))
	if len(words) == 0 {
		return candidates
	}
	holdsAll := func(title string) bool {
		folded := foldSearchText(title)
		for _, word := range words {
			if !strings.Contains(folded, word) {
				return false
			}
		}
		return true
	}
	return slices.DeleteFunc(candidates, func(c libraryBrowseCandidate) bool {
		return !holdsAll(c.item.Title) && (c.sortTitle == "" || !holdsAll(c.sortTitle))
	})
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
	// An unrated item (rating 0) matches no minimum.
	if query.MinRating > 0 && c.rating < query.MinRating {
		return false
	}
	return true
}

// sortLibraryBrowse orders candidates by the query's key. Items missing the key
// (no year, no duration, never watched, unrated) sort last in BOTH directions so a
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
		titles[c.item.ID] = strings.ToLower(c.titleKey())
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
	case LibraryBrowseSortRating:
		key = func(c libraryBrowseCandidate) int64 { return int64(c.rating) }
	case LibraryBrowseSortReleaseDate:
		key = func(c libraryBrowseCandidate) int64 { return c.release }
	case LibraryBrowseSortAudienceScore:
		key = func(c libraryBrowseCandidate) int64 { return c.audience }
	case LibraryBrowseSortPlays:
		key = func(c libraryBrowseCandidate) int64 { return c.plays }
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

// narrowLibraryFilter narrows a caller's Library filter to the one browsed
// Library, reporting false when the caller may not see it at all.
func narrowLibraryFilter(filter LibraryFilter, libraryID LibraryID) (LibraryFilter, bool) {
	if libraryID == "" {
		return filter, false
	}
	if len(filter.AllowedIDs) == 0 && !filter.AllLibraries {
		return filter, false
	}
	if len(filter.AllowedIDs) > 0 && !slices.Contains(filter.AllowedIDs, libraryID) {
		return filter, false
	}
	filter.AllowedIDs = []LibraryID{libraryID}
	return filter, true
}

// libraryVisibleSet is every Media of one browsed Library the caller may see,
// with the catalog plane it was read from. visible is empty when the Library is
// hidden from the caller or is a sealed Encrypted Library.
type libraryVisibleSet struct {
	reader     *sql.DB
	visible    []LibraryID
	certify    func() (string, []any)
	candidates []libraryBrowseCandidate
}

// libraryVisibleScopeLocked selects the Libraries a narrowed filter shows,
// with the plane to read them from and its certification clause, without
// reading any Media. The caller holds s.mu.
func (s *Store) libraryVisibleScopeLocked(ctx context.Context, filter LibraryFilter) (libraryVisibleSet, error) {
	set := libraryVisibleSet{reader: s.catalogR}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return libraryVisibleSet{}, err
	}
	// AllowedIDs is the one browsed Library, so at most one is visible and its
	// catalog plane holds every row this read needs.
	// certify renders the certification clause for the plane being read: an
	// Encrypted Library's own catalog has no library table to join.
	set.certify = func() (string, []any) {
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
			set.reader = db.reader
			pluginID := ""
			if library.PluginID != nil {
				pluginID = *library.PluginID
			}
			set.certify = func() (string, []any) {
				return planeCertificationFilterSQL(pluginID, filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
			}
		}
		set.visible = append(set.visible, library.ID)
	}
	return set, nil
}

// libraryVisibleSetLocked reads the visible, unmuted Media of the one Library
// a narrowed filter allows. Visibility (reveal, allow-list, certification,
// trash, extras, per-Profile Mute) is the all-items Source's, so every Library
// view built on it hides exactly what a rail would hide. The caller holds s.mu.
func (s *Store) libraryVisibleSetLocked(ctx context.Context, filter LibraryFilter, profileID ProfileID) (libraryVisibleSet, error) {
	set, err := s.libraryVisibleScopeLocked(ctx, filter)
	if err != nil || len(set.visible) == 0 {
		return set, err
	}

	candidates, err := libraryBrowseCandidates(ctx, set.reader, set.certify, set.visible, nil, nil)
	if err != nil {
		return libraryVisibleSet{}, err
	}
	// Sort titles live on the catalog plane only; an Encrypted Library's items
	// sort by their titles.
	if set.reader == s.catalogR {
		sortTitles := map[MediaID]string{}
		for _, libraryID := range set.visible {
			if err := librarySortTitles(ctx, set.reader, libraryID, sortTitles); err != nil {
				return libraryVisibleSet{}, err
			}
		}
		for i := range candidates {
			candidates[i].sortTitle = sortTitles[candidates[i].item.ID]
		}
	}
	if profileID != "" {
		candidates, err = s.libraryBrowseUnmutedLocked(ctx, set.reader, profileID, filter, candidates)
		if err != nil {
			return libraryVisibleSet{}, err
		}
	}
	set.candidates = candidates
	return set, nil
}

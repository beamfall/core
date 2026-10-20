package store

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const fuzzySearchScanLimit = 5000

type rankedSourceItem struct {
	item  SourceItem
	score int
}

type mediaSearchCandidate struct {
	id              MediaID
	title           string
	durationSeconds int
}

func (s *Store) fuzzySearchMediaLocked(ctx context.Context, filter LibraryFilter, query string, limit int) ([]SourceItem, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	var visible []LibraryID
	libraryPlugins := map[LibraryID]string{}
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visible = append(visible, library.ID)
			if library.PluginID != nil {
				libraryPlugins[library.ID] = *library.PluginID
			}
		}
	}
	if len(visible) == 0 {
		return []SourceItem{}, nil
	}
	ranked, err := s.metadataSearchMediaLocked(ctx, filter, query, libraries)
	if err != nil {
		return nil, err
	}
	placeholders := make([]string, len(visible))
	args := make([]any, 0, len(visible)+2)
	for i, id := range visible {
		placeholders[i] = "?"
		args = append(args, id)
	}
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	args = append(args, certArgs...)
	args = append(args, fuzzySearchScanLimit)
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT m.id, m.title, m.duration_seconds
		FROM media m
		WHERE m.trashed_at_ms IS NULL
		  AND m.library_id IN (`+strings.Join(placeholders, ", ")+`)`+certClause+`
		ORDER BY m.title COLLATE NOCASE, m.id
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	var candidates []mediaSearchCandidate
	for rows.Next() {
		var candidate mediaSearchCandidate
		if err := rows.Scan(&candidate.id, &candidate.title, &candidate.durationSeconds); err != nil {
			_ = rows.Close()
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	matcher := newSearchMatcher(query)
	for _, candidate := range candidates {
		score := matcher.rank(candidate.title)
		if score < 0 {
			continue
		}
		ranked = append(ranked, rankedSourceItem{score: score, item: SourceItem{
			ID:      candidate.id,
			Kind:    "media",
			Title:   candidate.title,
			Summary: mediaDurationSummary(candidate.durationSeconds),
		}})
	}
	for _, libraryID := range visible {
		db := s.encryptedDBs[libraryID]
		if db == nil || db.state != "unsealed-registered" || db.reader == nil {
			continue
		}
		planeCertClause, planeCertArgs := planeCertificationFilterSQL(libraryPlugins[libraryID], filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
		args := []any{libraryID}
		args = append(args, planeCertArgs...)
		args = append(args, fuzzySearchScanLimit)
		rows, err := db.reader.QueryContext(ctx, `
			SELECT m.id, m.title, m.duration_seconds
			FROM media m
			WHERE m.library_id = ?
			  AND m.trashed_at_ms IS NULL`+planeCertClause+`
			ORDER BY m.title COLLATE NOCASE, m.id
			LIMIT ?
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id MediaID
			var title string
			var durationSeconds int
			if err := rows.Scan(&id, &title, &durationSeconds); err != nil {
				rows.Close()
				return nil, err
			}
			score := matcher.rank(title)
			if score < 0 {
				continue
			}
			ranked = append(ranked, rankedSourceItem{score: score, item: SourceItem{
				ID:      id,
				Kind:    "media",
				Title:   title,
				Summary: mediaDurationSummary(durationSeconds),
			}})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	ranked = dedupeRankedSourceItems(ranked)
	sortRankedSourceItems(ranked)
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	items := make([]SourceItem, len(ranked))
	for i, item := range ranked {
		items[i] = item.item
	}
	return items, nil
}

func (s *Store) archiveSearchMediaLocked(ctx context.Context, filter LibraryFilter, query string, limit, offset int) ([]SourceItem, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	var visible []LibraryID
	libraryPlugins := map[LibraryID]string{}
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visible = append(visible, library.ID)
			if library.PluginID != nil {
				libraryPlugins[library.ID] = *library.PluginID
			}
		}
	}
	if len(visible) == 0 {
		return []SourceItem{}, nil
	}
	ranked, err := s.metadataSearchMediaLocked(ctx, filter, query, libraries)
	if err != nil {
		return nil, err
	}
	placeholders := make([]string, len(visible))
	args := make([]any, 0, len(visible)+2)
	for i, id := range visible {
		placeholders[i] = "?"
		args = append(args, id)
	}
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	args = append(args, certArgs...)
	args = append(args, fuzzySearchScanLimit)
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT m.id, m.title, COALESCE(m.summary, ''), m.duration_seconds
		FROM media m
		WHERE m.trashed_at_ms IS NULL
		  AND m.library_id IN (`+strings.Join(placeholders, ", ")+`)`+certClause+`
		ORDER BY m.title COLLATE NOCASE, m.id
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matcher := newSearchMatcher(query)
	for rows.Next() {
		var id MediaID
		var title, summary string
		var durationSeconds int
		if err := rows.Scan(&id, &title, &summary, &durationSeconds); err != nil {
			return nil, err
		}
		score := matcher.rank(title, summary)
		if score < 0 {
			continue
		}
		ranked = append(ranked, rankedSourceItem{score: score, item: SourceItem{
			ID:      id,
			Kind:    "media",
			Title:   title,
			Summary: mediaDurationSummary(durationSeconds),
		}})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, libraryID := range visible {
		db := s.encryptedDBs[libraryID]
		if db == nil || db.state != "unsealed-registered" || db.reader == nil {
			continue
		}
		planeCertClause, planeCertArgs := planeCertificationFilterSQL(libraryPlugins[libraryID], filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
		args := []any{libraryID}
		args = append(args, planeCertArgs...)
		args = append(args, fuzzySearchScanLimit)
		rows, err := db.reader.QueryContext(ctx, `
			SELECT m.id, m.title, COALESCE(m.summary, ''), m.duration_seconds
			FROM media m
			WHERE m.library_id = ?
			  AND m.trashed_at_ms IS NULL`+planeCertClause+`
			ORDER BY m.title COLLATE NOCASE, m.id
			LIMIT ?
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id MediaID
			var title, summary string
			var durationSeconds int
			if err := rows.Scan(&id, &title, &summary, &durationSeconds); err != nil {
				rows.Close()
				return nil, err
			}
			score := matcher.rank(title, summary)
			if score < 0 {
				continue
			}
			ranked = append(ranked, rankedSourceItem{score: score, item: SourceItem{
				ID:      id,
				Kind:    "media",
				Title:   title,
				Summary: mediaDurationSummary(durationSeconds),
			}})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	ranked = dedupeRankedSourceItems(ranked)
	sortRankedSourceItems(ranked)
	if offset >= len(ranked) {
		return []SourceItem{}, nil
	}
	end := min(len(ranked), offset+limit)
	ranked = ranked[offset:end]
	items := make([]SourceItem, len(ranked))
	for i, item := range ranked {
		items[i] = item.item
	}
	return items, nil
}

// fuzzySearchEntitiesLocked runs the ranked, reveal-annotated Entity FTS scan.
// kind/filters AND-compose the same entityFilterJoins predicates the other
// Entity query paths use, joined into THIS query so they narrow the
// fuzzySearchScanLimit-bounded scan itself — ahead of its LIMIT — rather than
// filtering an already-limited candidate window after the fact, which could
// silently under-return when more matching rows exist beyond the window.
// kind=="" with no filters (SearchEntities's only current caller) is an
// unfiltered scan across every kind, unchanged from before filters existed.
func (s *Store) fuzzySearchEntitiesLocked(ctx context.Context, visible []LibraryID, restricted map[LibraryID]bool, query string, limit int, kind string, filters []EntityField) ([]SourceItem, error) {
	filterJoins, filterArgs, err := s.entityFilterJoins(kind, filters)
	if err != nil {
		return nil, err
	}
	placeholders := make([]string, len(visible))
	args := make([]any, 0, len(visible)+len(filterArgs)+1)
	args = append(args, filterArgs...)
	for i, id := range visible {
		placeholders[i] = "?"
		args = append(args, id)
	}
	kindPredicate := ""
	if kind != "" {
		kindPredicate = ` AND e.kind = ?`
		args = append(args, kind)
	}
	args = append(args, fuzzySearchScanLimit)
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT e.id, e.title, e.library_id, f.fields
		FROM entity e
		JOIN entity_fts f ON f.entity_id = e.id`+filterJoins+`
		WHERE e.library_id IN (`+strings.Join(placeholders, ", ")+`)
		  `+kindPredicate+`
		  AND e.trashed_at_ms IS NULL
		ORDER BY e.title COLLATE NOCASE, e.id
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matcher := newSearchMatcher(query)
	var ranked []rankedSourceItem
	for rows.Next() {
		var id EntityID
		var title, fields string
		var libraryID LibraryID
		if err := rows.Scan(&id, &title, &libraryID, &fields); err != nil {
			return nil, err
		}
		score := matcher.rank(title, fields)
		if score < 0 {
			continue
		}
		ranked = append(ranked, rankedSourceItem{score: score, item: SourceItem{
			ID:         MediaID(id),
			Kind:       "entity",
			Title:      title,
			Restricted: restricted[libraryID],
		}})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortRankedSourceItems(ranked)
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	items := make([]SourceItem, len(ranked))
	for i, item := range ranked {
		items[i] = item.item
	}
	return items, nil
}

func sortRankedSourceItems(items []rankedSourceItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score < items[j].score
		}
		if items[i].item.Title != items[j].item.Title {
			return items[i].item.Title < items[j].item.Title
		}
		return items[i].item.ID < items[j].item.ID
	})
}

func dedupeRankedSourceItems(items []rankedSourceItem) []rankedSourceItem {
	best := make(map[string]rankedSourceItem, len(items))
	for _, item := range items {
		key := item.item.Kind + "\x00" + string(item.item.ID)
		if prior, ok := best[key]; !ok || item.score < prior.score {
			best[key] = item
		}
	}
	deduped := make([]rankedSourceItem, 0, len(best))
	for _, item := range best {
		deduped = append(deduped, item)
	}
	return deduped
}

// SearchRank returns -1 when text is not a useful query match. Lower is better.
func SearchRank(query string, parts ...string) int {
	return newSearchMatcher(query).rank(parts...)
}

type searchMatcher struct {
	terms []string
}

func newSearchMatcher(query string) searchMatcher {
	return searchMatcher{terms: strings.Fields(foldSearchText(query))}
}

func (m searchMatcher) rank(parts ...string) int {
	if len(m.terms) == 0 {
		return -1
	}
	haystack := foldSearchText(strings.Join(parts, " "))
	if haystack == "" {
		return -1
	}
	words := strings.Fields(haystack)
	total := 0
	for _, term := range m.terms {
		best := -1
		if strings.Contains(haystack, term) {
			best = 10
		}
		for _, word := range words {
			score := wordSearchScore(term, word)
			if score >= 0 && (best < 0 || score < best) {
				best = score
			}
		}
		if best < 0 {
			return -1
		}
		total += best
	}
	return total
}

func wordSearchScore(term, word string) int {
	switch {
	case term == word:
		return 0
	case strings.HasPrefix(word, term):
		return 1
	case strings.Contains(word, term):
		return 4
	}
	maxEdits := 1
	if len([]rune(term)) > 5 {
		maxEdits = 2
	}
	if d := boundedEditDistance(term, word, maxEdits); d >= 0 {
		return 20 + d
	}
	return -1
}

func foldSearchText(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteByte(' ')
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func boundedEditDistance(a, b string, maxDistance int) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) > len(br)+maxDistance || len(br) > len(ar)+maxDistance {
		return -1
	}
	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, ac := range ar {
		cur := make([]int, len(br)+1)
		cur[0] = i + 1
		rowBest := cur[0]
		for j, bc := range br {
			cost := 1
			if ac == bc {
				cost = 0
			}
			cur[j+1] = min(prev[j+1]+1, cur[j]+1, prev[j]+cost)
			rowBest = min(rowBest, cur[j+1])
		}
		if rowBest > maxDistance {
			return -1
		}
		prev = cur
	}
	if prev[len(br)] > maxDistance {
		return -1
	}
	return prev[len(br)]
}

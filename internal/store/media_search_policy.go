package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// MediaFieldSearchPolicy is one plugin's declared posture for a Media field.
type MediaFieldSearchPolicy struct {
	PluginID   string
	Field      string
	Type       EntityFieldType
	Searchable bool
}

// ReplaceMediaFieldSearchPolicies atomically replaces the active plugin-scoped
// Media search registry. Duplicate declarations merge by OR; conflicting types
// fail without disturbing the current registry.
func (s *Store) ReplaceMediaFieldSearchPolicies(ctx context.Context, policies []MediaFieldSearchPolicy) error {
	types := map[string]map[string]EntityFieldType{}
	next := map[string]map[string]bool{}
	for _, policy := range policies {
		if !entityIndexNamePattern.MatchString(policy.PluginID) {
			return fmt.Errorf("media field search plugin %q must be a lowercase slug", policy.PluginID)
		}
		if !entityIndexNamePattern.MatchString(policy.Field) {
			return fmt.Errorf("media field search field %q must be a lowercase slug", policy.Field)
		}
		if _, _, ok := entityFieldTableFor(policy.Type); !ok {
			return fmt.Errorf("media field search %s.%s has unknown type %q", policy.PluginID, policy.Field, policy.Type)
		}
		if policy.Searchable && policy.Type != EntityFieldText {
			return fmt.Errorf("media field search %s.%s is searchable but type %q, only text fields can be searchable", policy.PluginID, policy.Field, policy.Type)
		}
		if types[policy.PluginID] == nil {
			types[policy.PluginID] = map[string]EntityFieldType{}
			next[policy.PluginID] = map[string]bool{}
		}
		if prior, ok := types[policy.PluginID][policy.Field]; ok && prior != policy.Type {
			return fmt.Errorf("media field search %s.%s declared as both %q and %q", policy.PluginID, policy.Field, prior, policy.Type)
		}
		types[policy.PluginID][policy.Field] = policy.Type
		next[policy.PluginID][policy.Field] = next[policy.PluginID][policy.Field] || policy.Searchable
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	s.mediaFieldSearchPolicies = next
	return nil
}

func (s *Store) metadataSearchMediaLocked(ctx context.Context, filter LibraryFilter, query string, libraries []Library) ([]rankedSourceItem, error) {
	match := mediaFieldFTSQuery(query)
	if match == "" {
		return nil, nil
	}
	catalogPolicies := map[LibraryID][]string{}
	var ranked []rankedSourceItem
	for _, library := range libraries {
		if !libraryVisible(library, filter) || library.PluginID == nil {
			continue
		}
		fields := s.searchableMediaFields(*library.PluginID)
		if len(fields) == 0 {
			continue
		}
		if db := s.encryptedDBs[library.ID]; db != nil && db.state == "unsealed-registered" && db.reader != nil {
			items, err := metadataSearchDatabase(ctx, db.reader, filter, match, query, map[LibraryID][]string{library.ID: fields}, *library.PluginID)
			if err != nil {
				return nil, err
			}
			ranked = append(ranked, items...)
			continue
		}
		catalogPolicies[library.ID] = fields
	}
	if len(catalogPolicies) > 0 {
		items, err := metadataSearchDatabase(ctx, s.catalogR, filter, match, query, catalogPolicies, "")
		if err != nil {
			return nil, err
		}
		ranked = append(ranked, items...)
	}
	return ranked, nil
}

func (s *Store) searchableMediaFields(pluginID string) []string {
	var fields []string
	for field, searchable := range s.mediaFieldSearchPolicies[pluginID] {
		if searchable {
			fields = append(fields, field)
		}
	}
	sort.Strings(fields)
	return fields
}

func metadataSearchDatabase(ctx context.Context, db *sql.DB, filter LibraryFilter, match, query string, policies map[LibraryID][]string, pluginID string) ([]rankedSourceItem, error) {
	var policyClauses []string
	args := []any{match}
	ids := make([]LibraryID, 0, len(policies))
	for id := range policies {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		fields := policies[id]
		placeholders := make([]string, len(fields))
		for i := range fields {
			placeholders[i] = "?"
		}
		if pluginID == "" {
			policyClauses = append(policyClauses, "(l.id = ? AND ft.field IN ("+strings.Join(placeholders, ", ")+"))")
		} else {
			policyClauses = append(policyClauses, "(m.library_id = ? AND ft.field IN ("+strings.Join(placeholders, ", ")+"))")
		}
		args = append(args, id)
		for _, field := range fields {
			args = append(args, field)
		}
	}
	if len(policyClauses) == 0 {
		return nil, nil
	}
	var certClause string
	var certArgs []any
	if pluginID == "" {
		certClause, certArgs = certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	} else {
		certClause, certArgs = planeCertificationFilterSQL(pluginID, filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	}
	args = append(args, certArgs...)
	args = append(args, fuzzySearchScanLimit)
	libraryJoin := ""
	if pluginID == "" {
		libraryJoin = " JOIN library l ON l.id = m.library_id"
	}
	// #nosec G202 -- only generated placeholders, fixed joins, and closed certification clauses are interpolated.
	rows, err := db.QueryContext(ctx, `
		SELECT m.id, m.title, m.duration_seconds, group_concat(ft.value, ' ')
		FROM media_field_fts
		JOIN media_field_text ft ON ft.rowid = media_field_fts.rowid
		JOIN media m ON m.id = ft.media_id`+libraryJoin+`
		WHERE media_field_fts MATCH ?
		  AND m.trashed_at_ms IS NULL
		  AND (`+strings.Join(policyClauses, " OR ")+`)`+certClause+`
		GROUP BY m.id
		ORDER BY MIN(media_field_fts.rank), m.id
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matcher := newSearchMatcher(query)
	var ranked []rankedSourceItem
	for rows.Next() {
		var id MediaID
		var title, value string
		var duration int
		if err := rows.Scan(&id, &title, &duration, &value); err != nil {
			return nil, err
		}
		score := matcher.rank(title, value)
		if score < 0 {
			continue
		}
		ranked = append(ranked, rankedSourceItem{score: score, item: SourceItem{
			ID: id, Kind: "media", Title: title, Summary: mediaDurationSummary(duration),
		}})
	}
	return ranked, rows.Err()
}

func mediaFieldFTSQuery(query string) string {
	terms := strings.Fields(foldSearchText(query))
	for i, term := range terms {
		terms[i] = `"` + strings.ReplaceAll(term, `"`, `""`) + `"*`
	}
	return strings.Join(terms, " OR ")
}

package store

import (
	"context"
	"maps"
	"slices"
	"strings"
)

// entityItemsSQL returns one declared kind across visible Libraries, ordered
// by title, with field filters and browse rules applied before pagination.
// Callers hold s.mu.
func (s *Store) entityItemsSQL(ctx context.Context, filter LibraryFilter, query SourceQuery, limit, offset int) ([]SourceItem, error) {
	visible, err := s.visibleLibraryIDsLocked(ctx, filter)
	if err != nil {
		return nil, err
	}
	if len(visible) == 0 {
		return []SourceItem{}, nil
	}
	joins, args, err := s.entityFilterJoins(query.EntityKind, query.EntityFieldFilters)
	if err != nil {
		return nil, err
	}
	libraries, libraryArgs := libraryIDPlaceholders(visible)
	args = append(args, query.EntityKind)
	args = append(args, libraryArgs...)
	var browse strings.Builder
	if len(query.EntityBrowseSources) > 0 {
		joins = ` JOIN library l ON l.id = e.library_id` + joins
		for _, pluginID := range slices.Sorted(maps.Keys(query.EntityBrowseSources)) {
			predicate, sourceArgs := entityBrowseSourceSQL(query.EntityBrowseSources[pluginID])
			if predicate == "" {
				continue
			}
			// IS NOT also admits Libraries whose plugin_id is NULL.
			browse.WriteString(` AND (l.plugin_id IS NOT ? OR ` + predicate + `)`)
			args = append(args, pluginID)
			args = append(args, sourceArgs...)
		}
	}
	args = append(args, limit, offset)
	//nolint:gosec // G202: SQL fragments are generated internally; values remain bound.
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT e.id, e.title FROM entity e`+joins+`
		WHERE e.kind = ? AND e.library_id IN (`+libraries+`)
			AND e.trashed_at_ms IS NULL`+browse.String()+`
		ORDER BY e.title COLLATE NOCASE, e.id
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SourceItem{}
	for rows.Next() {
		var id EntityID
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		items = append(items, SourceItem{ID: MediaID(id), Kind: "entity", Title: title})
	}
	return items, rows.Err()
}

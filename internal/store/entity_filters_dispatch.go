package store

import "context"

// entityItemsFilteredSQL applies registered Entity field filters to the
// multi-Library, offset-paginated entity-items Source query. Callers hold s.mu.
func (s *Store) entityItemsFilteredSQL(ctx context.Context, filter LibraryFilter, kind string, filters []EntityField, limit, offset int) ([]SourceItem, error) {
	if len(filters) == 0 {
		return s.entityItemsSQL(ctx, filter, kind, limit, offset)
	}
	visible, err := s.visibleLibraryIDsLocked(ctx, filter)
	if err != nil {
		return nil, err
	}
	if len(visible) == 0 {
		return []SourceItem{}, nil
	}
	joins, joinArgs, err := s.entityFilterJoins(kind, entityKindOptionsFilters(EntityKindOptions{Filters: filters}))
	if err != nil {
		return nil, err
	}
	libraries, libraryArgs := libraryIDPlaceholders(visible)
	args := make([]any, 0, len(joinArgs)+len(libraryArgs)+3)
	args = append(args, joinArgs...)
	args = append(args, kind)
	args = append(args, libraryArgs...)
	args = append(args, limit, offset)
	//nolint:gosec // G202: joins and placeholders are generated internally; values remain bound.
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT e.id, e.title FROM entity e`+joins+`
		WHERE e.kind = ? AND e.library_id IN (`+libraries+`)
			AND e.trashed_at_ms IS NULL
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

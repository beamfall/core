package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// entityKindOptionsFilters returns opts' full AND-composed filter list: the
// legacy singular Filter (kept working for existing callers) folded in ahead
// of the plural Filters, so callers that still populate only Filter see
// identical behavior and callers using Filters compose additional predicates
// alongside it.
func entityKindOptionsFilters(opts EntityKindOptions) []EntityField {
	if opts.Filter == nil {
		return opts.Filters
	}
	combined := make([]EntityField, 0, len(opts.Filters)+1)
	combined = append(combined, *opts.Filter)
	combined = append(combined, opts.Filters...)
	return combined
}

// entityFilterJoins builds the AND-composed equality JOIN clauses one call of
// filters requires against kind's registered-Filterable fields, the single
// helper shared by every Entity query path (the offset page, the cursor page,
// and the ranked FTS scan) so a filtered read always predicates inside its
// own query rather than narrowing an already-limited result set after the
// fact. Each filter at index i gets its own deterministic alias fN — distinct
// even when two filters resolve to the SAME typed side table (e.g. two Text
// fields), where a single reused alias would collide or silently narrow only
// on the last-joined predicate. An unregistered, non-Filterable, or
// type-mismatched field errors rather than silently widening the read.
// Callers hold s.mu (read or write).
func (s *Store) entityFilterJoins(kind string, filters []EntityField) (joins string, args []any, err error) {
	var b strings.Builder
	args = make([]any, 0, len(filters)*3)
	for i, filter := range filters {
		spec, err := s.entityKindIndexLocked(kind, filter.Name)
		if err != nil {
			return "", nil, err
		}
		if !spec.Filterable {
			return "", nil, errors.New("field " + filter.Name + " is not filterable on kind " + kind)
		}
		if spec.Type != filter.Type && (spec.Type != EntityFieldText || filter.Type != EntityFieldURL) {
			return "", nil, errors.New("field " + filter.Name + " filter type mismatch")
		}
		table, column, _ := entityFieldTableFor(spec.Type)
		value, err := entityFieldValue(filter)
		if err != nil {
			return "", nil, err
		}
		alias := fmt.Sprintf("f%d", i)
		b.WriteString(` JOIN ` + table + ` ` + alias + ` ON ` + alias + `.entity_id = e.id AND ` + alias + `.kind = ? AND ` + alias + `.field = ? AND ` + alias + `.` + column + ` = ?`)
		args = append(args, kind, filter.Name, value)
	}
	return b.String(), args, nil
}

// EntitiesByKindCursorFiltered is EntitiesByKindCursor's filtered form: opts'
// AND-composed equality filters (Filters, plus the legacy singular Filter)
// narrow the SAME keyset-paginated query via entityFilterJoins, so filtering
// happens inside the ranked/ordered scan ahead of its LIMIT rather than as a
// post-filter over an already-limited page — a filtered walk over more
// matching rows than fit one page still returns every one of them across
// pages instead of silently under-returning. The (title, id) keyset ordering,
// stability guarantees, and cursor semantics are otherwise identical to
// EntitiesByKindCursor, which delegates here with the zero EntityKindOptions.
// opts.SortField/SortDesc are not honored here — cursor pagination keeps its
// fixed (title, id) order; only the offset EntitiesByKind page supports
// registered-Sortable ordering.
func (s *Store) EntitiesByKindCursorFiltered(ctx context.Context, library LibraryID, kind string, filter LibraryFilter, opts EntityKindOptions, cursor string, limit int) (EntityCursorPage, error) {
	if library == "" {
		return EntityCursorPage{}, errors.New("library ID is required")
	}
	if kind == "" {
		return EntityCursorPage{}, errors.New("entity kind is required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	afterTitle, afterID, hasCursor, err := decodeEntityCursor(cursor)
	if err != nil {
		return EntityCursorPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return EntityCursorPage{}, err
	}
	if err := s.libraryVisibleSQL(ctx, library, filter); err != nil {
		return EntityCursorPage{}, err
	}
	joins, joinArgs, err := s.entityFilterJoins(kind, entityKindOptionsFilters(opts))
	if err != nil {
		return EntityCursorPage{}, err
	}
	args := append([]any{}, joinArgs...)
	args = append(args, library, kind)
	keyset := ""
	if hasCursor {
		keyset = ` AND (e.title > ? OR (e.title = ? AND e.id > ?))`
		args = append(args, afterTitle, afterTitle, afterID)
	}
	args = append(args, limit+1)
	//nolint:gosec // G202: joins/keyset use validated fixed SQL fragments; values remain bound.
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT e.id, e.library_id, e.kind, e.title FROM entity e`+joins+`
		WHERE e.library_id = ? AND e.kind = ? AND e.trashed_at_ms IS NULL`+keyset+`
		ORDER BY e.title, e.id
		LIMIT ?
	`, args...)
	if err != nil {
		return EntityCursorPage{}, err
	}
	entities, err := scanEntities(rows)
	if err != nil {
		return EntityCursorPage{}, err
	}
	page := EntityCursorPage{}
	if len(entities) > limit {
		last := entities[limit-1]
		page.Next = encodeEntityCursor(last.Title, last.ID)
		entities = entities[:limit]
	}
	ids := make([]EntityID, len(entities))
	index := make(map[EntityID]int, len(entities))
	for i := range entities {
		ids[i] = entities[i].ID
		index[entities[i].ID] = i
	}
	if err := s.loadEntityFields(ctx, ids, func(id EntityID, field EntityField) {
		entities[index[id]].Fields = append(entities[index[id]].Fields, field)
	}); err != nil {
		return EntityCursorPage{}, err
	}
	for i := range entities {
		if entities[i].Fields == nil {
			entities[i].Fields = []EntityField{}
		}
		sortEntityFields(entities[i].Fields)
	}
	page.Entities = entities
	return page, nil
}

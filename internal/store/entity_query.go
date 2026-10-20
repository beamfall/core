package store

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
)

// EntityKindOptions narrows and orders an EntitiesByKind page by registered
// field index postures. The zero value keeps the plain title-ordered page.
type EntityKindOptions struct {
	// Filter requires equality on one registered-Filterable field: the
	// EntityField's Name/Type select the side table, the matching typed
	// member carries the wanted value. Nil means no filter. Kept working
	// for existing callers; it folds into the same AND-composed list as
	// Filters (entityKindOptionsFilters), ahead of any entries there.
	Filter *EntityField
	// Filters AND-composes equality on any number of registered-Filterable
	// fields (per-class, per-genre, ...). Nil/empty means no additional
	// filter beyond Filter, if set.
	Filters []EntityField
	// SortField names a registered-Sortable field to order by; entities
	// missing the field sort last. (title, id) stays the tiebreak. Empty
	// keeps title order.
	SortField string
	// SortDesc reverses the SortField order (never the tiebreak).
	SortDesc bool
}

// entityKindIndexLocked resolves one registered index posture; callers hold
// s.mu. Errors keep the registered/declared distinction out of the message —
// the caller only ever asked for something the contract does not offer.
func (s *Store) entityKindIndexLocked(kind, field string) (EntityFieldIndex, error) {
	spec, ok := s.entityIndexes[kind][field]
	if !ok {
		return EntityFieldIndex{}, errors.New("field " + field + " is not indexed on kind " + kind)
	}
	return spec, nil
}

// entityFieldValue lowers one EntityField's typed value to its SQL form.
func entityFieldValue(field EntityField) (any, error) {
	switch field.Type {
	case EntityFieldText, EntityFieldURL:
		return field.Text, nil
	case EntityFieldNumber:
		return field.Number, nil
	case EntityFieldFlag:
		return boolToInt(field.Flag), nil
	case EntityFieldDate:
		if field.Date.IsZero() {
			return nil, errors.New("entity date filter requires a value")
		}
		return timeToMillis(field.Date.UTC()), nil
	}
	return nil, errors.New("unknown entity field type " + string(field.Type))
}

// entitiesByKindSQL builds and runs the EntitiesByKind page query with the
// optional registered filter/sort joins. Callers hold s.mu.
func (s *Store) entitiesByKindSQL(ctx context.Context, library LibraryID, kind string, opts EntityKindOptions, limit, offset int) ([]Entity, error) {
	filterJoins, args, err := s.entityFilterJoins(kind, entityKindOptionsFilters(opts))
	if err != nil {
		return nil, err
	}
	joins := []string{filterJoins}
	order := `e.title, e.id`
	if opts.SortField != "" {
		spec, err := s.entityKindIndexLocked(kind, opts.SortField)
		if err != nil {
			return nil, err
		}
		if !spec.Sortable {
			return nil, errors.New("field " + opts.SortField + " is not sortable on kind " + kind)
		}
		table, column, _ := entityFieldTableFor(spec.Type)
		joins = append(joins, ` LEFT JOIN `+table+` sf ON sf.entity_id = e.id AND sf.field = ?`)
		args = append(args, opts.SortField)
		direction := ""
		if opts.SortDesc {
			direction = " DESC"
		}
		// #nosec G202 -- table/column come from the closed entityFieldTableFor mapping and direction is a fixed literal; the sort field itself is a bound arg.
		order = `(sf.` + column + ` IS NULL), sf.` + column + direction + `, e.title, e.id`
	}
	args = append(args, library, kind, limit, offset)
	// #nosec G202 -- joins/order are assembled only from closed entityFieldTableFor fragments and fixed literals; every dynamic value is a bound arg.
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT e.id, e.library_id, e.kind, e.title FROM entity e`+strings.Join(joins, "")+`
		WHERE e.library_id = ? AND e.kind = ? AND e.trashed_at_ms IS NULL
		ORDER BY `+order+`
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, err
	}
	return scanEntities(rows)
}

// EntityCursorPage is one page of a stable cursor-paginated EntitiesByKind
// read. Next is the opaque cursor to pass back for the following page; it is
// empty when the page is the last.
type EntityCursorPage struct {
	Entities []Entity
	Next     string
}

// encodeEntityCursor packs a (title, id) keyset into an opaque cursor token.
// The pieces are joined on a NUL byte — impossible in a title or id — so the
// split is unambiguous, and base64url keeps the token URL-safe.
func encodeEntityCursor(title string, id EntityID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(title + "\x00" + string(id)))
}

// decodeEntityCursor recovers the (title, id) keyset from a cursor token. An
// empty cursor is the start of the listing (no keyset). A malformed token is
// an error — a caller must round-trip a cursor this store minted.
func decodeEntityCursor(cursor string) (title string, id EntityID, ok bool, err error) {
	if cursor == "" {
		return "", "", false, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", false, errors.New("invalid entity cursor")
	}
	sep := strings.IndexByte(string(raw), '\x00')
	if sep < 0 {
		return "", "", false, errors.New("invalid entity cursor")
	}
	return string(raw[:sep]), EntityID(raw[sep+1:]), true, nil
}

// EntitiesByKindCursor returns one page of a Library's Entity instances of one
// declared kind using STABLE keyset-cursor pagination (the CursorPaginated
// seam): the page is anchored on the (title, id) of the last row of the prior
// page, not on an offset, so deep listing stays O(page) and never skips or
// repeats a row when instances are inserted or removed between pages
// (unlike offset paging). Ordering is (title, id) — the same order the plain
// EntitiesByKind page uses. The Library must be visible under the filter.
// limit ≤ 0 means the default page (50), capped at 200. An empty cursor starts
// the listing; the returned Next is empty on the final page.
func (s *Store) EntitiesByKindCursor(ctx context.Context, library LibraryID, kind string, filter LibraryFilter, cursor string, limit int) (EntityCursorPage, error) {
	return s.EntitiesByKindCursorFiltered(ctx, library, kind, filter, EntityKindOptions{}, cursor, limit)
}

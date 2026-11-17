package store

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"strings"
)

// RelatedEntityGroup is one Entity kind's share of the Entities related to a
// context Entity, as Source items (Kind "entity"). LibraryID is the Library of
// the group's first Entity, which names the bundle declaring the kind.
type RelatedEntityGroup struct {
	Kind      string
	LibraryID LibraryID
	Items     []SourceItem
}

// RelatedEntityGroups returns the visible Entities related to one Entity
// through Entity-to-Entity relations in both directions: the Entities it
// points at and the Entities pointing at it. Groups follow kind name; within
// a group rows carrying a relation sort order come first in that order, then
// the rest by title in natural order (a "2" before a "10", CORE-2362) and id. An Entity reached through several rows appears once (at its
// lowest sort order), the context Entity never does, and trashed Entities or
// Entities in Libraries the filter hides are omitted. The context Entity must
// itself be visible; a hidden one errors like a missing one, as
// mediaForEntitySQL does. The read runs on the Entity's catalog plane, so an
// encrypted Library's relations come from its own database.
func (s *Store) RelatedEntityGroups(ctx context.Context, entityID EntityID, filter LibraryFilter) ([]RelatedEntityGroup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	// Relations live beside the Entity: the catalog, or an encrypted
	// Library's own database (a sealed one errors here).
	plane, err := s.catalogPlaneForEntityLocked(ctx, entityID)
	if err != nil {
		return nil, err
	}
	if _, err := s.visibleEntitySQLFrom(ctx, plane.reader, entityID, filter); err != nil {
		return nil, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	var visible []LibraryID
	restricted := map[LibraryID]bool{}
	for _, library := range libraries {
		restricted[library.ID] = library.IsRestricted
		if libraryVisible(library, filter) {
			visible = append(visible, library.ID)
		}
	}
	if len(visible) == 0 {
		return []RelatedEntityGroup{}, nil
	}
	libIn, libArgs := libraryIDPlaceholders(visible)
	args := make([]any, 0, 4+len(libArgs))
	args = append(args, entityID, entityID, entityID)
	args = append(args, libArgs...)
	// #nosec G202 -- the interpolated fragment is internally generated placeholders.
	rows, err := plane.reader.QueryContext(ctx, `
		WITH linked(id, sort_order) AS (
			SELECT to_entity_id, sort_order FROM entity_relation
			WHERE from_entity_id = ? AND to_entity_id IS NOT NULL
			UNION ALL
			SELECT from_entity_id, sort_order FROM entity_relation
			WHERE to_entity_id = ?
		)
		SELECT e.id, e.library_id, e.kind, e.title, MIN(linked.sort_order) AS sort_order
		FROM linked
		JOIN entity e ON e.id = linked.id
		WHERE e.id <> ? AND e.trashed_at_ms IS NULL AND e.library_id IN (`+libIn+`)
		GROUP BY e.id
		ORDER BY e.kind, (sort_order IS NULL), sort_order, e.title, e.id
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []RelatedEntityGroup{}
	// ordered counts each group's leading rows that carry a sort order; the
	// rest are re-sorted by natural title below.
	ordered := []int{}
	for rows.Next() {
		var (
			id        EntityID
			libraryID LibraryID
			kind      string
			title     string
			sortOrder sql.NullInt64
		)
		if err := rows.Scan(&id, &libraryID, &kind, &title, &sortOrder); err != nil {
			return nil, err
		}
		if len(groups) == 0 || groups[len(groups)-1].Kind != kind {
			groups = append(groups, RelatedEntityGroup{Kind: kind, LibraryID: libraryID})
			ordered = append(ordered, 0)
		}
		if sortOrder.Valid {
			ordered[len(ordered)-1]++
		}
		group := &groups[len(groups)-1]
		// Library-level restricted flag, annotated as mediaForEntitySQL does;
		// the graph layer suppresses it unless the scope is Revealed.
		group.Items = append(group.Items, SourceItem{
			ID:         MediaID(id),
			Kind:       "entity",
			Title:      title,
			Restricted: restricted[libraryID],
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range groups {
		slices.SortStableFunc(groups[i].Items[ordered[i]:], func(a, b SourceItem) int {
			return cmp.Or(naturalCompare(a.Title, b.Title), cmp.Compare(a.Title, b.Title), cmp.Compare(a.ID, b.ID))
		})
	}
	return groups, nil
}

func asciiDigit(r rune) bool { return r >= '0' && r <= '9' }

// naturalCompare orders two titles case-insensitively with each run of digits
// compared by its numeric value, so "Part 2" sorts before "Part 10".
func naturalCompare(a, b string) int {
	ar, br := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	i, j := 0, 0
	for i < len(ar) && j < len(br) {
		if asciiDigit(ar[i]) && asciiDigit(br[j]) {
			ai, bj := i, j
			for i < len(ar) && asciiDigit(ar[i]) {
				i++
			}
			for j < len(br) && asciiDigit(br[j]) {
				j++
			}
			an := strings.TrimLeft(string(ar[ai:i]), "0")
			bn := strings.TrimLeft(string(br[bj:j]), "0")
			if c := cmp.Or(cmp.Compare(len(an), len(bn)), cmp.Compare(an, bn)); c != 0 {
				return c
			}
			continue
		}
		if c := cmp.Compare(ar[i], br[j]); c != 0 {
			return c
		}
		i++
		j++
	}
	return cmp.Compare(len(ar)-i, len(br)-j)
}

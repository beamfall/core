package store

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"strings"
)

// RelatedEntityGroup holds related cards of one kind. LibraryID names the
// Library whose plugin declares the group's presentation.
type RelatedEntityGroup struct {
	Kind        string
	LibraryID   LibraryID
	PluginID    string
	Items       []SourceItem
	GroupValues map[MediaID]string
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
// encrypted Library's relations come from its own database. Each card carries
// its year: the earliest of its directly linked visible Media.
func (s *Store) RelatedEntityGroups(ctx context.Context, entityID EntityID, filter LibraryFilter) ([]RelatedEntityGroup, error) {
	return s.RelatedEntityGroupsBy(ctx, entityID, filter, nil)
}

// RelatedEntityGroupsBy derives the nominated text field from each card's
// linked visible Media. Ruled Libraries keep independent groups (CORE-2769).
func (s *Store) RelatedEntityGroupsBy(ctx context.Context, entityID EntityID, filter LibraryFilter, groupField func(pluginID, kind string) string) ([]RelatedEntityGroup, error) {
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
	owners := map[LibraryID]string{}
	for _, library := range libraries {
		restricted[library.ID] = library.IsRestricted
		if library.PluginID != nil {
			owners[library.ID] = *library.PluginID
		}
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
			LibraryID:  libraryID,
		})
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range groups {
		slices.SortStableFunc(groups[i].Items[ordered[i]:], func(a, b SourceItem) int {
			return cmp.Or(naturalCompare(a.Title, b.Title), cmp.Compare(a.Title, b.Title), cmp.Compare(a.ID, b.ID))
		})
	}
	result := make([]RelatedEntityGroup, 0, len(groups))
	for _, group := range groups {
		for _, ownerGroup := range relatedGroupsByOwner(group, owners, groupField) {
			field := ""
			if groupField != nil {
				field = groupField(ownerGroup.PluginID, group.Kind)
			}
			values, err := s.annotateEntityCardsLocked(ctx, filter, ownerGroup.Items, field)
			if err != nil {
				return nil, err
			}
			ownerGroup.GroupValues = values
			result = append(result, ownerGroup)
		}
	}
	return result, nil
}

func relatedGroupsByOwner(group RelatedEntityGroup, owners map[LibraryID]string, groupField func(pluginID, kind string) string) []RelatedEntityGroup {
	if groupField == nil || !slices.ContainsFunc(group.Items, func(item SourceItem) bool {
		return groupField(owners[item.LibraryID], group.Kind) != ""
	}) {
		return []RelatedEntityGroup{group}
	}
	type owner struct {
		plugin  string
		library LibraryID
	}
	index := map[owner]int{}
	var groups []RelatedEntityGroup
	for _, item := range group.Items {
		key := owner{plugin: owners[item.LibraryID]}
		if groupField(key.plugin, group.Kind) != "" {
			key.library = item.LibraryID
		}
		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i
			groups = append(groups, RelatedEntityGroup{Kind: group.Kind, LibraryID: item.LibraryID, PluginID: key.plugin})
		}
		groups[i].Items = append(groups[i].Items, item)
	}
	return groups
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

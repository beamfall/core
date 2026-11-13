package store

import (
	"context"
	"database/sql"
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
// title and id. An Entity reached through several rows appears once (at its
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
	return groups, rows.Err()
}

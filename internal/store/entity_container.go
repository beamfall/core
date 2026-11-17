package store

import (
	"context"
	"database/sql"
	"errors"
)

// EntityLink is one relation row seen from the side being followed: the Entity
// at the other end, its kind, the relation name and the row's sort order (nil
// when unordered).
type EntityLink struct {
	EntityID  EntityID
	Kind      string
	Relation  string
	SortOrder *int
}

// MediaHolderLinks returns the Entities of one Library that point at a Media
// through any relation. It is a server-internal read for container
// resolution, not reveal-filtered; trashed Entities and Entities of other
// Libraries are skipped.
func (s *Store) MediaHolderLinks(ctx context.Context, libraryID LibraryID, mediaID MediaID) ([]EntityLink, error) {
	if mediaID == "" {
		return nil, errors.New("media ID is required")
	}
	return s.entityLinks(ctx, libraryID, `
		SELECT e.id, e.kind, r.relation, r.sort_order
		FROM entity_relation r
		JOIN entity e ON e.id = r.from_entity_id
		WHERE r.to_media_id = ? AND e.library_id = ? AND e.trashed_at_ms IS NULL
		ORDER BY r.relation, e.id
	`, mediaID)
}

// EntityTargetLinks returns the Entities of one Library that an Entity points
// at through entity→entity relations, with the same filtering as
// MediaHolderLinks.
func (s *Store) EntityTargetLinks(ctx context.Context, libraryID LibraryID, id EntityID) ([]EntityLink, error) {
	if id == "" {
		return nil, errors.New("entity ID is required")
	}
	return s.entityLinks(ctx, libraryID, `
		SELECT e.id, e.kind, r.relation, r.sort_order
		FROM entity_relation r
		JOIN entity e ON e.id = r.to_entity_id
		WHERE r.from_entity_id = ? AND e.library_id = ? AND e.trashed_at_ms IS NULL
		ORDER BY r.relation, e.id
	`, id)
}

// entityLinks runs one EntityLink query, bound to (key, libraryID), on the
// Library's catalog plane.
func (s *Store) entityLinks(ctx context.Context, libraryID LibraryID, query string, key any) ([]EntityLink, error) {
	if libraryID == "" {
		return nil, errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	rows, err := plane.reader.QueryContext(ctx, query, key, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var links []EntityLink
	for rows.Next() {
		var link EntityLink
		var sortOrder sql.NullInt64
		if err := rows.Scan(&link.EntityID, &link.Kind, &link.Relation, &sortOrder); err != nil {
			return nil, err
		}
		if sortOrder.Valid {
			order := int(sortOrder.Int64)
			link.SortOrder = &order
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

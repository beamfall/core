package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// TitledEntityLink is an EntityLink with the far Entity's Library and title,
// for batched container reads.
type TitledEntityLink struct {
	EntityLink
	LibraryID LibraryID
	Title     string
}

// MediaHolderLinksBatch returns, for each Media id, the untrashed Entities of
// the Media's own Library that point at it through any relation, ordered by
// relation then Entity id like MediaHolderLinks. The caller has already
// decided the Media are visible; an Entity of the same Library reveals
// nothing more. Every open catalog plane is read.
func (s *Store) MediaHolderLinksBatch(ctx context.Context, ids []MediaID) (map[MediaID][]TitledEntityLink, error) {
	return batchTitledLinks[MediaID](ctx, s, ids, `
		SELECT r.to_media_id, e.id, e.kind, r.relation, r.sort_order, e.library_id, e.title
		FROM entity_relation r
		JOIN entity e ON e.id = r.from_entity_id
		JOIN media m ON m.id = r.to_media_id
		WHERE r.to_media_id IN (%s)
			AND e.trashed_at_ms IS NULL AND e.library_id = m.library_id
		ORDER BY r.to_media_id, r.relation, e.id`)
}

// EntityTargetLinksBatch returns, for each Entity id, the untrashed Entities
// of its own Library it points at through entity→entity relations, ordered
// like EntityTargetLinks. Like MediaHolderLinksBatch it reads every open
// catalog plane and leaves visibility to the caller.
func (s *Store) EntityTargetLinksBatch(ctx context.Context, ids []EntityID) (map[EntityID][]TitledEntityLink, error) {
	return batchTitledLinks[EntityID](ctx, s, ids, `
		SELECT r.from_entity_id, e.id, e.kind, r.relation, r.sort_order, e.library_id, e.title
		FROM entity_relation r
		JOIN entity e ON e.id = r.to_entity_id
		JOIN entity source ON source.id = r.from_entity_id
		WHERE r.from_entity_id IN (%s)
			AND e.trashed_at_ms IS NULL AND e.library_id = source.library_id
		ORDER BY r.from_entity_id, r.relation, e.id`)
}

// batchTitledLinks runs one keyed TitledEntityLink query over every open
// catalog plane in bounded chunks; query has one %s for the key placeholders
// and selects the key first.
func batchTitledLinks[K ~string](ctx context.Context, s *Store, ids []K, query string) (map[K][]TitledEntityLink, error) {
	links := map[K][]TitledEntityLink{}
	if len(ids) == 0 {
		return links, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	for _, reader := range s.openCatalogReadersLocked() {
		for start := 0; start < len(ids); start += engagementChunkSize {
			chunk := ids[start:min(start+engagementChunkSize, len(ids))]
			args := make([]any, len(chunk))
			for i, id := range chunk {
				args[i] = string(id)
			}
			// #nosec G201 -- only generated placeholders are interpolated; IDs are bound args.
			rows, err := reader.QueryContext(ctx, fmt.Sprintf(query, sqlPlaceholders(len(chunk))), args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var key K
				var link TitledEntityLink
				var sortOrder sql.NullInt64
				if err := rows.Scan(&key, &link.EntityID, &link.Kind, &link.Relation, &sortOrder, &link.LibraryID, &link.Title); err != nil {
					_ = rows.Close()
					return nil, err
				}
				if sortOrder.Valid {
					order := int(sortOrder.Int64)
					link.SortOrder = &order
				}
				links[key] = append(links[key], link)
			}
			if err := rows.Close(); err != nil {
				return nil, err
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
		}
	}
	return links, nil
}

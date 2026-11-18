package store

import "context"

// OrderedContainer is the ordered container Entity that holds a Media item,
// with the Entities it relates to in either direction.
type OrderedContainer struct {
	ID        EntityID
	Kind      string
	LibraryID LibraryID
	Linked    []LinkedEntity
}

// LinkedEntity is one Entity an ordered container relates to.
type LinkedEntity struct {
	ID   EntityID
	Kind string
}

// MediaOrderedContainers maps each of many Media to every Entity holding it in
// order (the relation row carries a sort_order), by id, each with its related
// Entities ordered by kind then id, so a caller can pick the container whose
// kind is of the container archetype and find the root it sits under
// (CORE-2471). Other credits can carry a sort_order too; the store does not
// know archetypes, so it returns them all. Every open catalog plane is read,
// so an unsealed Encrypted Library's Media are covered; Media held in no
// order are absent from the result.
func (s *Store) MediaOrderedContainers(ctx context.Context, ids []MediaID) (map[MediaID][]OrderedContainer, error) {
	out := map[MediaID][]OrderedContainer{}
	if len(ids) == 0 {
		return out, nil
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
				args[i] = id
			}
			// #nosec G202 -- only generated placeholders are interpolated; every value is a bound arg.
			rows, err := reader.QueryContext(ctx, `
				WITH held AS (
					SELECT DISTINCT er.to_media_id AS media_id, er.from_entity_id AS container_id
					FROM entity_relation er
					WHERE er.sort_order IS NOT NULL AND er.from_entity_id IS NOT NULL
					  AND er.to_media_id IN (`+sqlPlaceholders(len(chunk))+`)
				),
				links AS (
					SELECT er.from_entity_id AS container_id, er.to_entity_id AS linked_id
					FROM entity_relation er JOIN held ON held.container_id = er.from_entity_id
					WHERE er.to_entity_id IS NOT NULL
					UNION
					SELECT er.to_entity_id, er.from_entity_id
					FROM entity_relation er JOIN held ON held.container_id = er.to_entity_id
				)
				SELECT held.media_id, held.container_id, c.kind, c.library_id,
				       COALESCE(l.id, ''), COALESCE(l.kind, '')
				FROM held
				JOIN entity c ON c.id = held.container_id
				LEFT JOIN links ON links.container_id = held.container_id
				LEFT JOIN entity l ON l.id = links.linked_id
				ORDER BY held.media_id, held.container_id, l.kind, l.id`, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var mediaID MediaID
				var container OrderedContainer
				var linked LinkedEntity
				if err := rows.Scan(&mediaID, &container.ID, &container.Kind, &container.LibraryID, &linked.ID, &linked.Kind); err != nil {
					_ = rows.Close()
					return nil, err
				}
				holders := out[mediaID]
				if len(holders) == 0 || holders[len(holders)-1].ID != container.ID {
					holders = append(holders, container)
				}
				if linked.ID != "" {
					last := &holders[len(holders)-1]
					last.Linked = append(last.Linked, linked)
				}
				out[mediaID] = holders
			}
			if err := rows.Close(); err != nil {
				return nil, err
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

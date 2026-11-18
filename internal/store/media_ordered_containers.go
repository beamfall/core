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

// MediaOrderedContainers maps each of many Media to the ordered container
// holding it (the relation row carries a sort_order) and that container's
// related Entities, ordered by kind then id, so a caller can find the root the
// container sits under (CORE-2471). An item in several ordered containers gets
// the one with the smallest id. Every open catalog plane is read, so an
// unsealed Encrypted Library's Media are covered; Media in no ordered
// container are absent from the result.
func (s *Store) MediaOrderedContainers(ctx context.Context, ids []MediaID) (map[MediaID]OrderedContainer, error) {
	out := map[MediaID]OrderedContainer{}
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
					SELECT er.to_media_id AS media_id, MIN(er.from_entity_id) AS container_id
					FROM entity_relation er
					WHERE er.sort_order IS NOT NULL AND er.to_media_id IN (`+sqlPlaceholders(len(chunk))+`)
					GROUP BY er.to_media_id
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
				ORDER BY held.media_id, l.kind, l.id`, args...)
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
				held, ok := out[mediaID]
				if !ok {
					held = container
				}
				if linked.ID != "" {
					held.Linked = append(held.Linked, linked)
				}
				out[mediaID] = held
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

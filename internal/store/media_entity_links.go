package store

import "context"

// MediaEntityLink is one untrashed Entity of a Media item's own Library that
// links to the Media item.
type MediaEntityLink struct {
	EntityID  EntityID
	LibraryID LibraryID
	Kind      string
	Title     string
}

// MediaEntityLinks returns, for each Media id, the untrashed Entities of its
// Library that link to it, in relation sort order, then title. The caller has
// already decided the Media are visible; an Entity always shares its Media's
// Library, so no wider visibility applies. Every open catalog plane is read.
func (s *Store) MediaEntityLinks(ctx context.Context, ids []MediaID) (map[MediaID][]MediaEntityLink, error) {
	links := map[MediaID][]MediaEntityLink{}
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
				args[i] = id
			}
			// #nosec G202 -- only generated placeholders are interpolated; IDs are bound args.
			rows, err := reader.QueryContext(ctx, `
				SELECT r.to_media_id, e.id, e.library_id, e.kind, e.title, MIN(r.sort_order) AS link_order
				FROM entity_relation r
				JOIN entity e ON e.id = r.from_entity_id
				JOIN media m ON m.id = r.to_media_id
				WHERE r.to_media_id IN (`+sqlPlaceholders(len(chunk))+`)
					AND e.trashed_at_ms IS NULL AND e.library_id = m.library_id
				GROUP BY r.to_media_id, e.id
				ORDER BY r.to_media_id, (link_order IS NULL), link_order, e.title COLLATE NOCASE, e.id`, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var mediaID MediaID
				var link MediaEntityLink
				var linkOrder any
				if err := rows.Scan(&mediaID, &link.EntityID, &link.LibraryID, &link.Kind, &link.Title, &linkOrder); err != nil {
					_ = rows.Close()
					return nil, err
				}
				links[mediaID] = append(links[mediaID], link)
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

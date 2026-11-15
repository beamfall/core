package store

import "context"

// AnnotateMediaClass sets MediaClass on each media-kind SourceItem from its
// stored playback class, so a client can pick a class-appropriate placeholder
// for a card with no artwork (CORE-1492). Entity items and Media whose class is
// unknown are left empty. Every open catalog plane is read, so an unsealed
// Encrypted Library's Media are annotated too. Safe on any SourceItem slice.
func (s *Store) AnnotateMediaClass(ctx context.Context, items []SourceItem) error {
	var ids []MediaID
	for i := range items {
		if items[i].Kind != "entity" {
			ids = append(ids, items[i].ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	classes := make(map[MediaID]MediaClass, len(ids))
	for _, reader := range s.openCatalogReadersLocked() {
		for start := 0; start < len(ids); start += engagementChunkSize {
			chunk := ids[start:min(start+engagementChunkSize, len(ids))]
			args := make([]any, len(chunk))
			for i, id := range chunk {
				args[i] = id
			}
			// #nosec G202 -- only generated placeholders are interpolated; IDs are bound args.
			rows, err := reader.QueryContext(ctx, `SELECT id, media_class FROM media WHERE id IN (`+sqlPlaceholders(len(chunk))+`)`, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id MediaID
				var class MediaClass
				if err := rows.Scan(&id, &class); err != nil {
					_ = rows.Close()
					return err
				}
				if class != "" {
					classes[id] = class
				}
			}
			if err := rows.Close(); err != nil {
				return err
			}
			if err := rows.Err(); err != nil {
				return err
			}
		}
	}
	for i := range items {
		if items[i].Kind == "entity" {
			continue
		}
		items[i].MediaClass = classes[items[i].ID]
	}
	return nil
}

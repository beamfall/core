package store

import (
	"context"
	"database/sql"
	"time"
)

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

// EntityCardDetails holds the facts needed to credit an Entity card.
type EntityCardDetails struct {
	LibraryID   LibraryID
	Kind        string
	ReleaseYear int
	Credits     []EntityCardCredit
}

// EntityCardCredit is the kind and title of an Entity reached by a card.
type EntityCardCredit struct {
	Kind  string
	Title string
}

// ReadEntityCardDetails reads the release date and outgoing Entity links of
// visible cards in batches. Only Entities in Libraries the filter shows are
// read, and each card keeps the first such Library it is found in. Links stay
// within the card's Library, exclude trashed targets, and follow sort order,
// then title. Sealed planes are skipped.
func (s *Store) ReadEntityCardDetails(ctx context.Context, filter LibraryFilter, ids []EntityID) (map[EntityID]EntityCardDetails, error) {
	details := make(map[EntityID]EntityCardDetails, len(ids))
	if len(ids) == 0 {
		return details, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	visible, err := s.visibleLibraryIDsLocked(ctx, filter)
	if err != nil || len(visible) == 0 {
		return details, err
	}
	libraryPlaceholders, libraryArgs := libraryIDPlaceholders(visible)
	for _, reader := range s.openCatalogReadersLocked() {
		for start := 0; start < len(ids); start += engagementChunkSize {
			chunk := ids[start:min(start+engagementChunkSize, len(ids))]
			args := make([]any, 0, len(chunk)+len(libraryArgs)+1)
			args = append(args, releaseDateField)
			for _, id := range chunk {
				args = append(args, id)
			}
			args = append(args, libraryArgs...)
			rows, err := reader.QueryContext(ctx, entityCardDetailsSQL(len(chunk), libraryPlaceholders), args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id EntityID
				var libraryID LibraryID
				var kind string
				var date sql.NullInt64
				var targetKind, title sql.NullString
				if err := rows.Scan(&id, &libraryID, &kind, &date, &targetKind, &title); err != nil {
					_ = rows.Close()
					return nil, err
				}
				detail, seen := details[id]
				if seen && detail.LibraryID != libraryID {
					continue
				}
				detail.LibraryID, detail.Kind = libraryID, kind
				if date.Valid {
					detail.ReleaseYear = max(0, time.UnixMilli(date.Int64).UTC().Year())
				}
				if targetKind.Valid {
					detail.Credits = append(detail.Credits, EntityCardCredit{Kind: targetKind.String, Title: title.String})
				}
				details[id] = detail
			}
			if err := rows.Close(); err != nil {
				return nil, err
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
		}
	}
	return details, nil
}

// entityCardDetailsSQL reads each card Entity's release date and linked
// Entities, binding the date field, then ids, then the visible Library ids.
// The unary + keeps SQLite seeking source by the id list: planned on
// library_id it reads every Entity in the visible Libraries (CORE-2894).
func entityCardDetailsSQL(ids int, libraryPlaceholders string) string {
	// #nosec G202 -- only generated placeholders are interpolated.
	return `
		SELECT source.id, source.library_id, source.kind, d.value_ms, target.kind, target.title
		FROM entity source
		LEFT JOIN entity_field_date d ON d.entity_id = source.id AND d.field = ?
		LEFT JOIN entity_relation r ON r.from_entity_id = source.id AND r.to_entity_id IS NOT NULL
		LEFT JOIN entity target ON target.id = r.to_entity_id
			AND target.library_id = source.library_id AND target.trashed_at_ms IS NULL
		WHERE source.id IN (` + sqlPlaceholders(ids) + `) AND source.trashed_at_ms IS NULL
			AND +source.library_id IN (` + libraryPlaceholders + `)
		GROUP BY source.id, target.id
		ORDER BY source.id, MIN(r.sort_order) IS NULL, MIN(r.sort_order), target.title COLLATE NOCASE, target.id`
}

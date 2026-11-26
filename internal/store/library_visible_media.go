package store

import (
	"context"
	"database/sql"
	"fmt"
)

// VisibleMedia is one Media item of a Library that a caller may see.
type VisibleMedia struct {
	ID        MediaID
	Title     string
	SortTitle string
	Year      int
	Width     int
	Height    int
	// Order is the item's catalog insertion order: a larger value was added later.
	Order int64
}

// TitleKey is the title the item sorts by: its sort title when set, else its title.
func (m VisibleMedia) TitleKey() string {
	if m.SortTitle != "" {
		return m.SortTitle
	}
	return m.Title
}

// LibraryVisibleMedia returns every Media of one Library that filter may see,
// with Reveal, the allow-list, certification, trash and extras applied before
// any count, exactly as a Library browse applies them. It reads the catalog
// plane only: an Encrypted Library returns no items. Per-Profile Mute is not
// applied, because a muted item is still held.
func (s *Store) LibraryVisibleMedia(ctx context.Context, filter LibraryFilter, libraryID LibraryID) ([]VisibleMedia, error) {
	filter, ok := narrowLibraryFilter(filter, libraryID)
	if !ok {
		return []VisibleMedia{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	set, err := s.libraryVisibleSetLocked(ctx, filter, "")
	if err != nil {
		return nil, err
	}
	if len(set.visible) == 0 || set.reader != s.catalogR {
		return []VisibleMedia{}, nil
	}
	items := make([]VisibleMedia, len(set.candidates))
	for i, c := range set.candidates {
		items[i] = VisibleMedia{
			ID: c.item.ID, Title: c.item.Title, SortTitle: c.sortTitle,
			Year: c.year, Width: c.width, Height: c.height, Order: c.rowID,
		}
	}
	return items, nil
}

// LibraryVisibleMediaItems returns the rows LibraryVisibleMedia would return
// for those of ids it would return, reading only those rows.
func (s *Store) LibraryVisibleMediaItems(ctx context.Context, filter LibraryFilter, libraryID LibraryID, ids []MediaID) (map[MediaID]VisibleMedia, error) {
	visible := map[MediaID]VisibleMedia{}
	filter, ok := narrowLibraryFilter(filter, libraryID)
	if !ok || len(ids) == 0 {
		return visible, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	set, err := s.libraryVisibleScopeLocked(ctx, filter)
	if err != nil {
		return nil, err
	}
	if len(set.visible) == 0 || set.reader != s.catalogR {
		return visible, nil
	}
	for start := 0; start < len(ids); start += engagementChunkSize {
		chunk := ids[start:min(start+engagementChunkSize, len(ids))]
		candidates, err := libraryBrowseCandidates(ctx, set.reader, set.certify, set.visible, nil, chunk)
		if err != nil {
			return nil, err
		}
		for _, c := range candidates {
			visible[c.item.ID] = VisibleMedia{
				ID: c.item.ID, Title: c.item.Title, SortTitle: c.sortTitle,
				Year: c.year, Width: c.width, Height: c.height, Order: c.rowID,
			}
		}
	}
	return visible, nil
}

// LibraryLiveEntityTitles returns the title of each given catalog-plane Entity
// of the Library that is not trashed. Visibility of the Media an Entity holds
// is left to the caller.
func (s *Store) LibraryLiveEntityTitles(ctx context.Context, libraryID LibraryID, ids []EntityID) (map[EntityID]string, error) {
	titles := map[EntityID]string{}
	if len(ids) == 0 {
		return titles, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	for start := 0; start < len(ids); start += engagementChunkSize {
		chunk := ids[start:min(start+engagementChunkSize, len(ids))]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, libraryID)
		for _, id := range chunk {
			args = append(args, string(id))
		}
		// #nosec G201 -- only generated placeholders are interpolated; IDs are bound args.
		rows, err := s.catalogR.QueryContext(ctx, fmt.Sprintf(
			`SELECT id, title FROM entity WHERE +library_id = ? AND trashed_at_ms IS NULL AND id IN (%s)`,
			sqlPlaceholders(len(chunk))), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id EntityID
			var title string
			if err := rows.Scan(&id, &title); err != nil {
				_ = rows.Close()
				return nil, err
			}
			titles[id] = title
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return titles, nil
}

// CatalogItemLibraries returns the Library holding each given ID that names a
// catalog-plane Media or Entity, trashed or not. It confirms no visibility.
func (s *Store) CatalogItemLibraries(ctx context.Context, ids []string) (map[string]LibraryID, error) {
	owners := map[string]LibraryID{}
	if len(ids) == 0 {
		return owners, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	for start := 0; start < len(ids); start += engagementChunkSize {
		chunk := ids[start:min(start+engagementChunkSize, len(ids))]
		args := make([]any, 0, 2*len(chunk))
		for range 2 {
			for _, id := range chunk {
				args = append(args, id)
			}
		}
		placeholders := sqlPlaceholders(len(chunk))
		// #nosec G201 -- only generated placeholders are interpolated; IDs are bound args.
		rows, err := s.catalogR.QueryContext(ctx, fmt.Sprintf(
			`SELECT id, library_id FROM media WHERE id IN (%s) UNION ALL SELECT id, library_id FROM entity WHERE id IN (%s)`,
			placeholders, placeholders), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var library LibraryID
			if err := rows.Scan(&id, &library); err != nil {
				_ = rows.Close()
				return nil, err
			}
			owners[id] = library
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return owners, nil
}

// MediaMetadataProvenanceBatch returns the stored metadata provenance of each
// given readable Media (across catalog and unsealed Encrypted Library planes)
// that has one.
func (s *Store) MediaMetadataProvenanceBatch(ctx context.Context, ids []MediaID) (map[MediaID]MediaMetadataProvenance, error) {
	records := map[MediaID]MediaMetadataProvenance{}
	if len(ids) == 0 {
		return records, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	// Media IDs are unique across planes, so each open plane contributes only
	// its own Media's provenance and sealed planes remain unreadable.
	for _, plane := range s.allCatalogPlanesLocked() {
		for start := 0; start < len(ids); start += engagementChunkSize {
			chunk := ids[start:min(start+engagementChunkSize, len(ids))]
			if err := mediaMetadataProvenanceChunk(ctx, plane.reader, chunk, records); err != nil {
				return nil, err
			}
		}
	}
	return records, nil
}

func mediaMetadataProvenanceChunk(ctx context.Context, reader *sql.DB, chunk []MediaID, records map[MediaID]MediaMetadataProvenance) error {
	args := make([]any, len(chunk))
	for i, id := range chunk {
		args[i] = string(id)
	}
	// #nosec G201 -- only generated placeholders are interpolated; IDs are bound args.
	rows, err := reader.QueryContext(ctx, fmt.Sprintf(
		`SELECT media_id, plugin_id, scraper_id, candidate_id FROM media_metadata_provenance WHERE media_id IN (%s)`,
		sqlPlaceholders(len(chunk))), args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id MediaID
		var record MediaMetadataProvenance
		if err := rows.Scan(&id, &record.PluginID, &record.ScraperID, &record.CandidateID); err != nil {
			return err
		}
		records[id] = record
	}
	return rows.Err()
}

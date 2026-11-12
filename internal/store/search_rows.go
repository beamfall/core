package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SearchRowDetail is the display detail a search row carries beyond its
// SourceItem: where the item lives and what it is. The zero value means the
// item's Library could not be resolved or is not visible to the caller.
type SearchRowDetail struct {
	LibraryID    LibraryID
	LibraryTitle string
	// Year is the Media year; 0 when unset. Entities carry no year.
	Year int
	// MediaClass is set for media-kind items, EntityKind for entity-kind items.
	MediaClass string
	EntityKind string
}

// SearchRowDetails resolves the display detail for each item, index-aligned
// with items. It is additive: it never changes which items are served. An item
// whose Library is not visible under filter, or whose catalog plane is sealed,
// gets the zero detail.
func (s *Store) SearchRowDetails(ctx context.Context, filter LibraryFilter, items []SourceItem) ([]SearchRowDetail, error) {
	details := make([]SearchRowDetail, len(items))
	if len(items) == 0 {
		return details, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	titles := make(map[LibraryID]string, len(libraries))
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			titles[library.ID] = library.Title
		}
	}
	pending := make(map[sourceItemIdentity][]int, len(items))
	for i := range items {
		kind := "media"
		if items[i].Kind == "entity" {
			kind = "entity"
		}
		key := sourceItemIdentity{kind: kind, id: items[i].ID}
		pending[key] = append(pending[key], i)
	}
	if err := searchRowDetailsFromPlane(ctx, s.catalogR, titles, details, pending); err != nil {
		return nil, err
	}
	for _, db := range s.encryptedDBs {
		if len(pending) == 0 {
			break
		}
		if db == nil || db.state != "unsealed-registered" || db.reader == nil {
			continue
		}
		if err := searchRowDetailsFromPlane(ctx, db.reader, titles, details, pending); err != nil {
			return nil, err
		}
	}
	return details, nil
}

// searchRowDetailsFromPlane fills details for every still-pending item found
// in one catalog plane. An item missing from this plane stays pending for the
// next one.
func searchRowDetailsFromPlane(ctx context.Context, db sourceItemLibraryQueryer, titles map[LibraryID]string, details []SearchRowDetail, pending map[sourceItemIdentity][]int) error {
	queries := map[string]string{
		"media":  `SELECT id, library_id, media_class, year FROM media WHERE id IN (`,
		"entity": `SELECT id, library_id, kind, NULL FROM entity WHERE id IN (`,
	}
	for _, kind := range []string{"entity", "media"} {
		args := make([]any, 0, len(pending))
		for key := range pending {
			if key.kind == kind {
				args = append(args, key.id)
			}
		}
		if len(args) == 0 {
			continue
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(args)), ", ")
		rows, err := db.QueryContext(ctx, queries[kind]+placeholders+`)`, args...)
		if err != nil {
			return fmt.Errorf("resolve %s search row detail: %w", kind, err)
		}
		for rows.Next() {
			var id MediaID
			var libraryID LibraryID
			var class string
			var year sql.NullInt64
			if err := rows.Scan(&id, &libraryID, &class, &year); err != nil {
				_ = rows.Close()
				return fmt.Errorf("resolve %s search row detail: %w", kind, err)
			}
			key := sourceItemIdentity{kind: kind, id: id}
			if title, visible := titles[libraryID]; visible {
				detail := SearchRowDetail{LibraryID: libraryID, LibraryTitle: title, Year: int(year.Int64)}
				if kind == "entity" {
					detail.EntityKind = class
				} else {
					detail.MediaClass = class
				}
				for _, index := range pending[key] {
					details[index] = detail
				}
			}
			delete(pending, key)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return fmt.Errorf("resolve %s search row detail: %w", kind, err)
		}
	}
	return nil
}

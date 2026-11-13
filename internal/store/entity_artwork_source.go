package store

import (
	"context"
	"errors"
)

// EntityRef names one Entity and the Library it lives in.
type EntityRef struct {
	ID        EntityID
	LibraryID LibraryID
}

// EntitiesOfKindAfter pages the shared catalog's Entities of one plugin's kind
// in id order, starting after the cursor. It is the artwork sweep's walk over
// the Entities that may inherit a poster; trashed Entities are skipped.
func (s *Store) EntitiesOfKindAfter(ctx context.Context, pluginID, kind string, after EntityID, limit int) ([]EntityRef, error) {
	if pluginID == "" || kind == "" {
		return nil, errors.New("plugin ID and entity kind are required")
	}
	if limit <= 0 {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT e.id, e.library_id FROM entity e
		JOIN library l ON l.id = e.library_id AND l.plugin_id = ?
		WHERE e.kind = ? AND e.trashed_at_ms IS NULL AND e.id > ?
		ORDER BY e.id
		LIMIT ?
	`, pluginID, kind, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []EntityRef
	for rows.Next() {
		var ref EntityRef
		if err := rows.Scan(&ref.ID, &ref.LibraryID); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// EntityMemberMediaIDs returns one page of the Media one Entity points at
// through a relation, ordered rows first by their sort order (the order the
// Entity's children are shown in), skipping trashed Media.
func (s *Store) EntityMemberMediaIDs(ctx context.Context, id EntityID, relation string, offset, limit int) ([]MediaID, error) {
	if id == "" || relation == "" {
		return nil, errors.New("entity ID and relation name are required")
	}
	if limit <= 0 {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT r.to_media_id FROM entity_relation r
		JOIN media m ON m.id = r.to_media_id AND m.trashed_at_ms IS NULL
		WHERE r.from_entity_id = ? AND r.relation = ?
		ORDER BY r.sort_order IS NULL, r.sort_order, r.to_media_id
		LIMIT ? OFFSET ?
	`, id, relation, limit, max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []MediaID
	for rows.Next() {
		var media MediaID
		if err := rows.Scan(&media); err != nil {
			return nil, err
		}
		ids = append(ids, media)
	}
	return ids, rows.Err()
}

// EntitiesRelatingTo returns one page of the Entities of one kind that point
// at an Entity through a relation, by title then id, skipping trashed Entities.
func (s *Store) EntitiesRelatingTo(ctx context.Context, id EntityID, fromKind, relation string, offset, limit int) ([]EntityID, error) {
	if id == "" || fromKind == "" || relation == "" {
		return nil, errors.New("entity ID, source kind and relation name are required")
	}
	if limit <= 0 {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT e.id FROM entity_relation r
		JOIN entity e ON e.id = r.from_entity_id
		WHERE r.to_entity_id = ? AND r.relation = ? AND e.kind = ? AND e.trashed_at_ms IS NULL
		ORDER BY e.title, e.id
		LIMIT ? OFFSET ?
	`, id, relation, fromKind, limit, max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []EntityID
	for rows.Next() {
		var from EntityID
		if err := rows.Scan(&from); err != nil {
			return nil, err
		}
		ids = append(ids, from)
	}
	return ids, rows.Err()
}

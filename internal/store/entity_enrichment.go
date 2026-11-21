package store

import (
	"context"
	"errors"
	"time"
)

// EntitiesDueForEnrichment returns one page of a plugin's entities of one kind,
// in id order after `after`, that the named enrichment has never matched and
// has not checked since retryBefore. Like EntitiesOfKindAfter it reads the
// shared catalog only, so encrypted Libraries are never walked, and it skips
// trashed entities.
func (s *Store) EntitiesDueForEnrichment(ctx context.Context, pluginID, kind, enrichment string, after EntityID, retryBefore time.Time, limit int) ([]EntityRef, error) {
	if pluginID == "" || kind == "" || enrichment == "" {
		return nil, errors.New("plugin ID, entity kind and enrichment are required")
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
		LEFT JOIN entity_enrichment x ON x.entity_id = e.id AND x.enrichment = ?
		WHERE e.kind = ? AND e.trashed_at_ms IS NULL AND e.id > ?
		  AND (x.entity_id IS NULL OR (x.matched = 0 AND x.checked_at_ms < ?))
		ORDER BY e.id
		LIMIT ?
	`, pluginID, enrichment, kind, after, retryBefore.UnixMilli(), limit)
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

// RecordEntityEnrichment stores the outcome of one enrichment check for an
// entity, replacing any earlier record.
func (s *Store) RecordEntityEnrichment(ctx context.Context, id EntityID, enrichment string, matched bool, at time.Time) error {
	if id == "" || enrichment == "" {
		return errors.New("entity ID and enrichment are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	flag := 0
	if matched {
		flag = 1
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO entity_enrichment (entity_id, enrichment, checked_at_ms, matched)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (entity_id, enrichment) DO UPDATE SET
		  checked_at_ms = excluded.checked_at_ms, matched = excluded.matched
	`, id, enrichment, at.UnixMilli(), flag)
	return err
}

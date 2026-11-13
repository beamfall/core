package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// entityScopeSplitPrefix prefixes the server_config rows recording that
// SplitAmbiguousEntityScopes already ran for one (kind, relation).
const entityScopeSplitPrefix = "entity-scope-split."

// entityScopeBackfillPrefix prefixes the server_config rows naming a Library
// whose Media SplitAmbiguousEntityScopes reopened and for which no backfill
// scan has been queued yet. Only a backfill scan hydrates an unchanged file
// again, so the row outlives a crash between the repair and the enqueue.
const entityScopeBackfillPrefix = "entity-scope-backfill."

// EntityRelationTargets returns the Entities one Entity points at through an
// entity→entity relation, and whether the Entity exists (and is not trashed).
// It is the hydration read behind scoped Entity identity: the relation a kind
// is scoped by tells which scope an Entity written before scoping belongs to.
func (s *Store) EntityRelationTargets(ctx context.Context, libraryID LibraryID, id EntityID, relation string) ([]EntityID, bool, error) {
	if libraryID == "" {
		return nil, false, errors.New("library ID is required")
	}
	if id == "" {
		return nil, false, errors.New("entity ID is required")
	}
	if relation == "" {
		return nil, false, errors.New("relation name is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, false, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return nil, false, err
	}
	var one int
	err = plane.reader.QueryRowContext(ctx, `
		SELECT 1 FROM entity WHERE id = ? AND library_id = ? AND trashed_at_ms IS NULL
	`, id, libraryID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT to_entity_id FROM entity_relation
		WHERE from_entity_id = ? AND relation = ? AND to_entity_id IS NOT NULL
		ORDER BY to_entity_id
	`, id, relation)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var targets []EntityID
	for rows.Next() {
		var target EntityID
		if err := rows.Scan(&target); err != nil {
			return nil, false, err
		}
		targets = append(targets, target)
	}
	return targets, true, rows.Err()
}

// SplitAmbiguousEntityScopes is the one-time repair for a plugin's Entities
// of a kind that hydration used to identify by title alone and now scopes by
// relation. An Entity whose scope relation points at exactly one Entity is
// already one scope's and is left alone. Every other one is ambiguous: with
// two or more targets it is same-titled things merged into one, with none it
// may be. For each, its scope relation rows are cleared and the hydration
// marker of every Media it relates to is cleared, so the next scan hydrates
// those Media again: the first scope seen keeps the Entity (its id, artwork
// and other relations), every other scope gets its own Entity and its Media
// move there. Every Library whose Media it reopened is recorded as awaiting a
// backfill scan (see PendingEntityScopeBackfills).
//
// It runs once per (plugin, kind, relation): the run is recorded in
// server_config, so a relation a person adds by hand later is never undone.
// It returns how many Entities it reopened. Only the shared catalog is
// repaired.
func (s *Store) SplitAmbiguousEntityScopes(ctx context.Context, pluginID, kind, relation, markerField string, now time.Time) (int, error) {
	// Authority face: Entity-relation catalog rows are main-only.
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return 0, err
	}
	if pluginID == "" || kind == "" || relation == "" || markerField == "" {
		return 0, errors.New("plugin ID, entity kind, relation and marker field are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	key := entityScopeSplitPrefix + pluginID + "." + kind + "." + relation
	result, err := tx.ExecContext(ctx, `
		INSERT INTO server_config(key, value, updated_at_ms) VALUES (?, '', ?)
		ON CONFLICT(key) DO NOTHING
	`, key, timeToMillis(now))
	if err != nil {
		return 0, err
	}
	if first, err := result.RowsAffected(); err != nil || first == 0 {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT e.id, e.library_id FROM entity e
		JOIN library l ON l.id = e.library_id AND l.plugin_id = ?
		LEFT JOIN entity_relation r
		  ON r.from_entity_id = e.id AND r.relation = ? AND r.to_entity_id IS NOT NULL
		WHERE e.kind = ?
		GROUP BY e.id
		HAVING COUNT(DISTINCT r.to_entity_id) <> 1
		ORDER BY e.id
	`, pluginID, relation, kind)
	if err != nil {
		return 0, err
	}
	var reopened []EntityID
	libraries := map[EntityID]LibraryID{}
	for rows.Next() {
		var id EntityID
		var library LibraryID
		if err := rows.Scan(&id, &library); err != nil {
			rows.Close()
			return 0, err
		}
		reopened = append(reopened, id)
		libraries[id] = library
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, id := range reopened {
		cleared, err := tx.ExecContext(ctx, `
			DELETE FROM media_field_int
			WHERE field = ? AND media_id IN (
				SELECT to_media_id FROM entity_relation
				WHERE from_entity_id = ? AND to_media_id IS NOT NULL
			)
		`, markerField, id)
		if err != nil {
			return 0, err
		}
		if n, err := cleared.RowsAffected(); err != nil {
			return 0, err
		} else if n > 0 {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO server_config(key, value, updated_at_ms) VALUES (?, '', ?)
				ON CONFLICT(key) DO NOTHING
			`, entityScopeBackfillPrefix+string(libraries[id]), timeToMillis(now)); err != nil {
				return 0, err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM entity_relation
			WHERE from_entity_id = ? AND relation = ? AND to_entity_id IS NOT NULL
		`, id, relation); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(reopened), nil
}

// PendingEntityScopeBackfills returns the Libraries whose Media a scope repair
// reopened and for which no backfill scan has been queued yet, in id order.
func (s *Store) PendingEntityScopeBackfills(ctx context.Context) ([]LibraryID, error) {
	// Authority face: only the node that authors the repair queues its scans.
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT substr(key, ?) FROM server_config WHERE substr(key, 1, ?) = ? ORDER BY key
	`, len(entityScopeBackfillPrefix)+1, len(entityScopeBackfillPrefix), entityScopeBackfillPrefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var libraries []LibraryID
	for rows.Next() {
		var library LibraryID
		if err := rows.Scan(&library); err != nil {
			return nil, err
		}
		libraries = append(libraries, library)
	}
	return libraries, rows.Err()
}

// ClearEntityScopeBackfill records that the backfill scan of a Library a scope
// repair reopened has been queued.
func (s *Store) ClearEntityScopeBackfill(ctx context.Context, libraryID LibraryID) error {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if libraryID == "" {
		return errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `DELETE FROM server_config WHERE key = ?`, entityScopeBackfillPrefix+string(libraryID))
	return err
}

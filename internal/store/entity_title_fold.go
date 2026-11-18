package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/beamfall/core/internal/serverrole"
)

// entityTitleFoldPrefix prefixes the server_config rows recording that
// ReopenFoldedEntityTitles already ran for one (plugin, kind).
const entityTitleFoldPrefix = "entity-title-fold."

// FoldEntityTitle is the comparison form of an Entity title: lower case,
// letters and digits only. Two titles that differ only in case, spacing or
// punctuation ("Gangs of London", "gangs.of.london") fold equal.
func FoldEntityTitle(title string) string {
	var out strings.Builder
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(unicode.ToLower(r))
		}
	}
	return out.String()
}

// ReopenFoldedEntityTitles is the one-time repair for a plugin's Entities of a
// kind that hydration used to identify by a title read from each file name,
// so files whose names spell one title in different case or punctuation were
// split across several Entities. For every set of two or more live Entities
// of the kind in one Library whose titles fold equal (FoldEntityTitle), the
// hydration marker of every Media they relate to, directly or through an
// Entity that relates to them, is cleared and the Library is recorded as
// awaiting a backfill scan (see PendingEntityScopeBackfills). The next scan
// hydrates those Media again under the mapper's current identity, and the
// hydration adapter merges each Media's old Entity into the new one.
// Nothing is deleted here.
//
// It runs once per (plugin, kind): the run is recorded in server_config. It
// returns how many Entities it reopened. Only the shared catalog is repaired.
func (s *Store) ReopenFoldedEntityTitles(ctx context.Context, pluginID, kind, markerField string, now time.Time) (int, error) {
	// Authority face: Entity-relation catalog rows are main-only.
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return 0, err
	}
	if pluginID == "" || kind == "" || markerField == "" {
		return 0, errors.New("plugin ID, entity kind and marker field are required")
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
	result, err := tx.ExecContext(ctx, `
		INSERT INTO server_config(key, value, updated_at_ms) VALUES (?, '', ?)
		ON CONFLICT(key) DO NOTHING
	`, entityTitleFoldPrefix+pluginID+"."+kind, timeToMillis(now))
	if err != nil {
		return 0, err
	}
	if first, err := result.RowsAffected(); err != nil || first == 0 {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT e.id, e.library_id, e.title FROM entity e
		JOIN library l ON l.id = e.library_id AND l.plugin_id = ?
		WHERE e.kind = ? AND e.trashed_at_ms IS NULL
		ORDER BY e.id
	`, pluginID, kind)
	if err != nil {
		return 0, err
	}
	type foldKey struct {
		library LibraryID
		title   string
	}
	groups := map[foldKey][]EntityID{}
	for rows.Next() {
		var id EntityID
		var library LibraryID
		var title string
		if err := rows.Scan(&id, &library, &title); err != nil {
			_ = rows.Close()
			return 0, err
		}
		key := foldKey{library: library, title: FoldEntityTitle(title)}
		groups[key] = append(groups[key], id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	keys := make([]foldKey, 0, len(groups))
	for key, ids := range groups {
		if len(ids) > 1 && key.title != "" {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].library != keys[j].library {
			return keys[i].library < keys[j].library
		}
		return keys[i].title < keys[j].title
	})
	reopened := 0
	for _, key := range keys {
		for _, id := range groups[key] {
			cleared, err := tx.ExecContext(ctx, `
				DELETE FROM media_field_int
				WHERE field = ? AND media_id IN (
					SELECT to_media_id FROM entity_relation
					WHERE from_entity_id = ? AND to_media_id IS NOT NULL
					UNION
					SELECT member.to_media_id FROM entity_relation holder
					JOIN entity_relation member ON member.from_entity_id = holder.from_entity_id
					WHERE holder.to_entity_id = ? AND member.to_media_id IS NOT NULL
				)
			`, markerField, id, id)
			if err != nil {
				return 0, err
			}
			reopened++
			if n, err := cleared.RowsAffected(); err != nil {
				return 0, err
			} else if n > 0 {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO server_config(key, value, updated_at_ms) VALUES (?, '', ?)
					ON CONFLICT(key) DO NOTHING
				`, entityScopeBackfillPrefix+string(key.library), timeToMillis(now)); err != nil {
					return 0, err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return reopened, nil
}

// CopyEntityItemState copies the per-Profile item state (ratings and
// watchlist entries) and the tags of one Entity onto another, so merging two
// Entities that are one item keeps what people recorded against either. A
// row the destination already has wins. The source's rows are kept, so
// undoing the merge loses nothing.
func (s *Store) CopyEntityItemState(ctx context.Context, from, to EntityID) error {
	if from == "" || to == "" {
		return errors.New("source and destination entity IDs are required")
	}
	if from == to {
		return nil
	}
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if _, err := s.catalogW.ExecContext(ctx, `
		INSERT OR IGNORE INTO entity_tag(entity_id, tag_id)
		SELECT ?, tag_id FROM entity_tag WHERE entity_id = ?
	`, to, from); err != nil {
		return err
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO item_rating(profile_id, target_kind, target_id, rating, rated_at_ms)
		SELECT profile_id, target_kind, ?, rating, rated_at_ms FROM item_rating
		WHERE target_kind = 'entity' AND target_id = ?
	`, to, from); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO watchlist_item(profile_id, target_kind, target_id, added_at_ms)
		SELECT profile_id, target_kind, ?, added_at_ms FROM watchlist_item
		WHERE target_kind = 'entity' AND target_id = ?
	`, to, from); err != nil {
		return err
	}
	return tx.Commit()
}

// LiveFoldEqualEntityTitle reports, for an Entity that is in trash, the title
// of a live Entity of the same kind and Library whose title folds equal to its
// own: the Entity a title-fold merge folded it into. Hydration uses it so a
// file still spelling the merged-away title lands on the live Entity instead
// of the trashed one. ok is false when the Entity is live or absent, or no
// fold-equal Entity is live; the lowest id wins a tie.
func (s *Store) LiveFoldEqualEntityTitle(ctx context.Context, libraryID LibraryID, id EntityID) (string, bool, error) {
	if libraryID == "" || id == "" {
		return "", false, errors.New("library ID and entity ID are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", false, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return "", false, err
	}
	var kind, title string
	var trashed sql.NullInt64
	err = plane.reader.QueryRowContext(ctx, `
		SELECT kind, title, trashed_at_ms FROM entity WHERE id = ? AND library_id = ?
	`, id, libraryID).Scan(&kind, &title, &trashed)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !trashed.Valid) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	live, err := liveFoldEqualEntities(ctx, plane.reader, libraryID, kind, title, "", "")
	if err != nil || len(live) == 0 {
		return "", false, err
	}
	return live[0].Title, true, nil
}

// LiveFoldEqualEntities lists the live Entities of a kind in a Library whose
// titles fold equal to title, lowest id first. With a scope relation, only
// Entities whose relation of that name points at scopeTarget are listed.
func (s *Store) LiveFoldEqualEntities(ctx context.Context, libraryID LibraryID, kind, title, scopeRelation string, scopeTarget EntityID) ([]Entity, error) {
	if libraryID == "" || kind == "" {
		return nil, errors.New("library ID and entity kind are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	return liveFoldEqualEntities(ctx, plane.reader, libraryID, kind, title, scopeRelation, scopeTarget)
}

func liveFoldEqualEntities(ctx context.Context, reader *sql.DB, libraryID LibraryID, kind, title, scopeRelation string, scopeTarget EntityID) ([]Entity, error) {
	folded := FoldEntityTitle(title)
	if folded == "" {
		return nil, nil
	}
	var rows *sql.Rows
	var err error
	if scopeRelation == "" {
		rows, err = reader.QueryContext(ctx, `
			SELECT id, title FROM entity
			WHERE library_id = ? AND kind = ? AND trashed_at_ms IS NULL
			ORDER BY id
		`, libraryID, kind)
	} else {
		rows, err = reader.QueryContext(ctx, `
			SELECT DISTINCT e.id, e.title FROM entity_relation r
			JOIN entity e ON e.id = r.from_entity_id
			WHERE r.to_entity_id = ? AND r.relation = ?
				AND e.library_id = ? AND e.kind = ? AND e.trashed_at_ms IS NULL
			ORDER BY e.id
		`, scopeTarget, scopeRelation, libraryID, kind)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entity
	for rows.Next() {
		var entity Entity
		if err := rows.Scan(&entity.ID, &entity.Title); err != nil {
			return nil, err
		}
		if FoldEntityTitle(entity.Title) == folded {
			entity.LibraryID, entity.Kind = libraryID, kind
			out = append(out, entity)
		}
	}
	return out, rows.Err()
}

// EntityTitleFoldApplies reports whether title-fold merging can run in a
// Library. It cannot in an encrypted Library: the entity merge journals in the
// shared catalog, where that Library's Entities do not exist.
func (s *Store) EntityTitleFoldApplies(ctx context.Context, libraryID LibraryID) (bool, error) {
	if libraryID == "" {
		return false, errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return false, err
	}
	return !plane.encrypted, nil
}

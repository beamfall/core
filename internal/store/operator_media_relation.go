package store

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/beamfall/core/internal/serverrole"
)

const (
	ActivitySetMediaRelation    = "media.relation.set"
	ActivityRemoveMediaRelation = "media.relation.remove"
)

// MediaRelationSnapshot carries the row values needed to recover a link.
type MediaRelationSnapshot struct {
	EntityID       EntityID          `json:"entityID"`
	LibraryID      LibraryID         `json:"libraryID"`
	Attributes     map[string]string `json:"attributes"`
	SortOrder      *int              `json:"sortOrder"`
	LockActivityID *string           `json:"lockActivityID"`
	LockedAtMS     *int64            `json:"lockedAtMS"`
	LockOne        bool              `json:"lockOne"`
}

// MediaRelationChange describes the affected rows under one relation name.
// A many-valued set or a removal snapshots only the selected link.
type MediaRelationChange struct {
	MediaID  MediaID                 `json:"mediaID"`
	Relation string                  `json:"relation"`
	Before   []MediaRelationSnapshot `json:"before"`
	After    []MediaRelationSnapshot `json:"after"`
}

// SetOperatorMediaRelation resolves or creates the selected Entity and writes
// the lock and recovery record in the Media's catalog transaction.
func (s *Store) SetOperatorMediaRelation(ctx context.Context, relation EntityRelation, entity Entity, filter LibraryFilter, activity ActivityEntry) (EntityID, error) {
	activity.Action = ActivitySetMediaRelation
	return s.writeOperatorMediaRelation(ctx, relation, entity, filter, activity, false)
}

// RemoveOperatorMediaRelation removes a selected link and its lock.
func (s *Store) RemoveOperatorMediaRelation(ctx context.Context, relation EntityRelation, filter LibraryFilter, activity ActivityEntry) error {
	activity.Action = ActivityRemoveMediaRelation
	_, err := s.writeOperatorMediaRelation(ctx, relation, Entity{}, filter, activity, true)
	return err
}

func (s *Store) writeOperatorMediaRelation(ctx context.Context, relation EntityRelation, entity Entity, filter LibraryFilter, activity ActivityEntry, remove bool) (EntityID, error) {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return "", err
	}
	if activity.OperationID == "" || relation.ToMediaID == "" || relation.Name == "" || relation.FromEntityID == "" {
		return "", ValidationError("missing relation write argument")
	}
	activity.MediaRelationChange = nil
	if err := validateActivityEntry(&activity); err != nil {
		return "", err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, relation.ToMediaID)
	if err != nil {
		return "", err
	}
	media, err := s.visibleMediaSQLFrom(ctx, plane.reader, relation.ToMediaID, filter)
	if err != nil {
		return "", err
	}
	activity.LibraryID = media.LibraryID
	activity.TargetType = "media"
	activity.TargetID = string(media.ID)
	if plane.encrypted {
		// The shared activity index holds only the opaque operation and
		// Library identifiers. Media and Entity IDs stay on the private plane.
		activity.TargetID = ""
	}
	libraries, err := s.visibleLibraryIDsLocked(ctx, filter)
	if err != nil {
		return "", err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var live bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM media
		WHERE id = ? AND library_id = ? AND trashed_at_ms IS NULL)`, media.ID, media.LibraryID).Scan(&live); err != nil {
		return "", err
	}
	if !live {
		return "", ValidationError("unknown media")
	}
	if entity.Title != "" {
		entity.LibraryID = media.LibraryID
		if err := s.resolveOperatorRelationEntityTx(ctx, tx, &entity); err != nil {
			return "", err
		}
		relation.FromEntityID = entity.ID
	}
	var kind string
	var libraryID LibraryID
	err = tx.QueryRowContext(ctx, `SELECT kind, library_id FROM entity WHERE id = ? AND trashed_at_ms IS NULL`,
		relation.FromEntityID).Scan(&kind, &libraryID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ValidationError("unknown entity")
	}
	if err != nil {
		return "", err
	}
	if !slices.Contains(libraries, libraryID) || (entity.Kind != "" && kind != entity.Kind) {
		return "", ValidationError("unknown entity")
	}

	all := relation.Cardinality == "one" && !remove
	before, err := mediaRelationSnapshotsTx(ctx, tx, relation, all)
	if err != nil {
		return "", err
	}
	// The selected Media authorizes replacing its whole single-valued link.
	// The recovery read applies visibility separately to each source Entity.
	if remove || all {
		if _, err := tx.ExecContext(ctx, `DELETE FROM entity_relation
			WHERE to_media_id = ? AND relation = ? AND (? OR from_entity_id = ?)`,
			media.ID, relation.Name, all, relation.FromEntityID); err != nil {
			return "", err
		}
	}
	if !remove {
		attributes, sortOrder, err := entityRelationRowValues(relation)
		if err != nil {
			return "", err
		}
		if err := insertMediaRelationTx(ctx, tx, relation, attributes, sortOrder); err != nil {
			return "", err
		}
		// Resolve the actor through the immutable ActivityEntry. The lock
		// holds an audit ID, so profile data is not copied into catalog rows.
		if _, err := tx.ExecContext(ctx, `UPDATE entity_relation
			SET operator_locked_at_ms = ?, operator_lock_activity_id = ?, operator_lock_one = ?
			WHERE to_media_id = ? AND relation = ? AND from_entity_id = ?`,
			timeToMillis(activity.OccurredAt), activity.OperationID, all, media.ID, relation.Name, relation.FromEntityID); err != nil {
			return "", err
		}
	}
	after, err := mediaRelationSnapshotsTx(ctx, tx, relation, all)
	if err != nil {
		return "", err
	}
	if err := recordMediaRelationActivityTx(ctx, tx, activity, relation, before, after); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	// Activity publication can be retried after a restart. Its recovery data
	// was committed atomically with the change and stays on the owning plane.
	if err := s.publishMediaRelationActivityLocked(ctx, plane.writer, activity); err != nil {
		slog.Warn("operator relation activity publication deferred", "error", err)
	}
	return relation.FromEntityID, nil
}

func (s *Store) resolveOperatorRelationEntityTx(ctx context.Context, tx *sql.Tx, entity *Entity) error {
	// The catalog writer serializes the lookup and creation, even when two
	// requests supply different spellings of the same normalized name.
	folded := FoldEntityTitle(entity.Title)
	if folded == "" {
		return s.ensureEntityTx(ctx, tx, *entity)
	}
	existing, err := foldEqualRows(ctx, tx, folded, entity.LibraryID, entity.Kind, `
		SELECT rowid, id, title FROM entity
		WHERE library_id = ? AND kind = ? AND trashed_at_ms IS NULL ORDER BY rowid
	`, entity.LibraryID, entity.Kind)
	if err != nil {
		return err
	}
	if len(existing) != 0 {
		entity.ID = existing[0].ID
		return nil
	}
	return s.ensureEntityTx(ctx, tx, *entity)
}

func mediaRelationSnapshotsTx(ctx context.Context, tx *sql.Tx, relation EntityRelation, all bool) ([]MediaRelationSnapshot, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT r.from_entity_id, e.library_id, r.attributes, r.sort_order,
		       r.operator_lock_activity_id, r.operator_locked_at_ms, r.operator_lock_one
		FROM entity_relation r JOIN entity e ON e.id = r.from_entity_id
		WHERE r.to_media_id = ? AND r.relation = ? AND (? OR r.from_entity_id = ?)
		ORDER BY r.from_entity_id
	`, relation.ToMediaID, relation.Name, all, relation.FromEntityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaRelationSnapshot{}
	for rows.Next() {
		var row MediaRelationSnapshot
		var attributes sql.NullString
		if err := rows.Scan(&row.EntityID, &row.LibraryID, &attributes, &row.SortOrder, &row.LockActivityID, &row.LockedAtMS, &row.LockOne); err != nil {
			return nil, err
		}
		row.Attributes, err = decodeRelationAttributes(attributes)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func recordMediaRelationActivityTx(ctx context.Context, tx *sql.Tx, activity ActivityEntry, relation EntityRelation, before, after []MediaRelationSnapshot) error {
	prior, err := json.Marshal(before)
	if err != nil {
		return err
	}
	next, err := json.Marshal(after)
	if err != nil {
		return err
	}
	pending, err := json.Marshal(activity)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO media_relation_activity(operation_id, media_id, relation, before_rows, after_rows, pending_activity)
		VALUES (?, ?, ?, ?, ?, ?)
	`, activity.OperationID, relation.ToMediaID, relation.Name, string(prior), string(next), string(pending))
	return err
}

func (s *Store) publishMediaRelationActivityLocked(ctx context.Context, writer *sql.DB, activity ActivityEntry) error {
	if err := appendActivitySQL(ctx, s.eventsW, activity); err != nil {
		return err
	}
	_, err := writer.ExecContext(ctx, `UPDATE media_relation_activity SET pending_activity = NULL WHERE operation_id = ?`, activity.OperationID)
	return err
}

func (s *Store) flushMediaRelationActivityLocked(ctx context.Context) error {
	if err := s.flushMediaRelationPlaneLocked(ctx, s.catalogR, s.catalogW); err != nil {
		return err
	}
	for id, db := range s.encryptedDBs {
		if reader := s.encryptedMediaReaderLocked(id); reader != nil {
			if err := s.flushMediaRelationPlaneLocked(ctx, reader, db.writer); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) flushMediaRelationPlaneLocked(ctx context.Context, reader rowsQueryer, writer *sql.DB) error {
	rows, err := reader.QueryContext(ctx, `SELECT pending_activity FROM media_relation_activity WHERE pending_activity IS NOT NULL ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var entries []ActivityEntry
	for rows.Next() {
		var raw string
		var entry ActivityEntry
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := s.publishMediaRelationActivityLocked(ctx, writer, entry); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) loadMediaRelationActivityLocked(ctx context.Context, entry *ActivityEntry, visible []LibraryID) error {
	if entry.Action != ActivitySetMediaRelation && entry.Action != ActivityRemoveMediaRelation {
		return nil
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, entry.LibraryID)
	if err != nil {
		// A sealed encrypted library's recovery payload is unavailable.
		if errors.Is(err, ErrEncryptedLibraryDBDisabled) {
			return nil
		}
		return err
	}
	var change MediaRelationChange
	var before, after string
	err = plane.reader.QueryRowContext(ctx, `
		SELECT media_id, relation, before_rows, after_rows FROM media_relation_activity WHERE operation_id = ?
	`, entry.OperationID).Scan(&change.MediaID, &change.Relation, &before, &after)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(before), &change.Before); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(after), &change.After); err != nil {
		return err
	}
	hidden := func(row MediaRelationSnapshot) bool { return !slices.Contains(visible, row.LibraryID) }
	change.Before = slices.DeleteFunc(change.Before, hidden)
	change.After = slices.DeleteFunc(change.After, hidden)
	entry.MediaRelationChange = &change
	return nil
}

// SearchEntitiesByName returns a deterministic name-prefix page across the
// caller's visible Libraries, including unsealed private catalogs.
func (s *Store) SearchEntitiesByName(ctx context.Context, filter LibraryFilter, kind, prefix string, limit int) ([]Entity, error) {
	limit = min(50, limit)
	if limit <= 0 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	libraries, err := s.visibleLibraryIDsLocked(ctx, filter)
	if err != nil || len(libraries) == 0 {
		return []Entity{}, err
	}
	placeholders, args := libraryIDPlaceholders(libraries)
	pattern := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(prefix) + "%"
	args = append(args, kind, pattern, limit)
	entities := []Entity{}
	for _, reader := range s.openCatalogReadersLocked() {
		// #nosec G202 -- only generated placeholders are interpolated.
		rows, err := reader.QueryContext(ctx, `
			SELECT id, library_id, kind, title FROM entity
			WHERE library_id IN (`+placeholders+`) AND kind = ? AND title LIKE ? ESCAPE '\'
			  AND trashed_at_ms IS NULL
			ORDER BY title COLLATE NOCASE, id LIMIT ?`, args...)
		if err != nil {
			return nil, err
		}
		matches, err := scanEntities(rows)
		if err != nil {
			return nil, err
		}
		entities = append(entities, matches...)
	}
	// Match SQLite NOCASE (ASCII) so merging per-plane pages preserves order.
	slices.SortFunc(entities, func(a, b Entity) int {
		if byTitle := strings.Compare(sqliteNoCase(a.Title), sqliteNoCase(b.Title)); byTitle != 0 {
			return byTitle
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return entities[:min(limit, len(entities))], nil
}

func sqliteNoCase(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

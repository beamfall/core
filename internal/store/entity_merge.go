package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Entity merge + trash (FEAT-6 / ADR-0005 §3, sharing the ADR-0009 §6 batch-undo
// envelope). MergeEntities folds a loser Entity into a winner of the same Kind:
// every relation row touching the loser is re-pointed to the winner (colliding
// rows deduped against the unique indexes, self-loops dropped), then the loser
// is TRASHED — recoverable, never hard-deleted (hard invariant #6). The whole
// thing is ONE op-batch = ONE undo unit: UndoBatch replays the journaled prior
// relation rows of both entities and restores the loser from Trash.
//
// The journal item lives in op_batch_entity_item (0058), the entity arm of the
// 0037 journal — that table's media_id is NOT NULL, so an entity merge could not
// ride it. The op_batch parent row is shared; UndoBatch reads both arms.

// opKindMergeEntity is the op_batch.kind for an entity merge — distinct from the
// Media-cluster opKindMerge so UndoBatch routes it to the entity-item replay.
const opKindMergeEntity = "merge-entity"

// entityRelationRow is one entity_relation row captured for the merge journal:
// every column, so undo re-inserts it verbatim. Exactly one of ToEntityID /
// ToMediaID is non-empty (the table's CHECK); SortOrder is nil when unordered.
type entityRelationRow struct {
	FromEntityID EntityID `json:"from"`
	Relation     string   `json:"rel"`
	ToEntityID   EntityID `json:"toEntity,omitempty"`
	ToMediaID    MediaID  `json:"toMedia,omitempty"`
	Attributes   *string  `json:"attrs,omitempty"`
	SortOrder    *int64   `json:"sort,omitempty"`
}

// entityMergeItemState is the per-loser merge journal snapshot: every relation
// row that touched the loser OR the winner before the merge, plus the loser's
// trashed flag. Undo deletes all rows touching either entity, re-inserts both
// snapshots verbatim, and un-trashes the loser — an exact reversal regardless of
// which loser rows were deduped away or dropped as self-loops on apply.
type entityMergeItemState struct {
	LoserRelations  []entityRelationRow `json:"loserRelations"`
	WinnerRelations []entityRelationRow `json:"winnerRelations"`
	Trashed         bool                `json:"trashed"`
}

// TrashEntity soft-deletes one Entity (hard invariant #6): it is hidden from
// every reveal-filtered read and listing but its rows survive for restore. The
// Library must be visible to the caller; an already-trashed or invisible/missing
// Entity is "entity not found" (no existence oracle past the Reveal contract).
func (s *Store) TrashEntity(ctx context.Context, id EntityID, filter LibraryFilter, now time.Time) error {
	if id == "" {
		return ValidationError("entity id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if _, err := s.visibleEntitySQL(ctx, id, filter); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx,
		`UPDATE entity SET trashed_at_ms = ? WHERE id = ? AND trashed_at_ms IS NULL`,
		timeToMillis(now.UTC()), id)
	return err
}

// RestoreEntity reverses TrashEntity, returning a trashed Entity to live state.
// It bypasses visibleEntitySQL (which hides trashed rows) and instead confirms
// the Library is visible directly — a trashed Entity is invisible to the read
// path by design, so restore re-derives visibility from the Library.
func (s *Store) RestoreEntity(ctx context.Context, id EntityID, filter LibraryFilter) error {
	if id == "" {
		return ValidationError("entity id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	var libraryID LibraryID
	var restricted int
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT e.library_id, l.is_restricted
		FROM entity e JOIN library l ON l.id = e.library_id
		WHERE e.id = ?
	`, id).Scan(&libraryID, &restricted)
	if errors.Is(err, sql.ErrNoRows) {
		return ValidationError("entity not found")
	}
	if err != nil {
		return err
	}
	if !libraryVisible(Library{ID: libraryID, IsRestricted: restricted != 0}, filter) {
		return ValidationError("entity not found")
	}
	_, err = s.catalogW.ExecContext(ctx,
		`UPDATE entity SET trashed_at_ms = NULL WHERE id = ?`, id)
	return err
}

// MergeEntities folds loserID into winnerID: both must be live,
// visible, and of the SAME Kind. Every entity_relation row touching the loser is
// re-pointed to the winner (deduped against the unique indexes, self-loops
// dropped), then the loser is trashed. Recorded as one op-batch (kind
// "merge-entity") so UndoBatch reverses the whole merge. Returns the batch id
// (the undo handle); the affected count is 1 (the trashed loser).
func (s *Store) MergeEntities(ctx context.Context, winnerID, loserID EntityID, filter LibraryFilter, now time.Time) (BulkOpBatch, error) {
	if winnerID == "" || loserID == "" {
		return BulkOpBatch{}, ValidationError("winner and loser entity ids are required")
	}
	if winnerID == loserID {
		return BulkOpBatch{}, ValidationError("cannot merge an entity into itself")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return BulkOpBatch{}, err
	}

	winner, err := s.visibleEntitySQL(ctx, winnerID, filter)
	if err != nil {
		return BulkOpBatch{}, err
	}
	loser, err := s.visibleEntitySQL(ctx, loserID, filter)
	if err != nil {
		return BulkOpBatch{}, err
	}
	if winner.Kind != loser.Kind {
		return BulkOpBatch{}, ValidationError("cannot merge entities of different kinds")
	}

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return BulkOpBatch{}, err
	}
	defer tx.Rollback()

	loserRows, err := entityRelationRowsTx(ctx, tx, loserID)
	if err != nil {
		return BulkOpBatch{}, err
	}
	winnerRows, err := entityRelationRowsTx(ctx, tx, winnerID)
	if err != nil {
		return BulkOpBatch{}, err
	}

	state, err := json.Marshal(entityMergeItemState{
		LoserRelations:  loserRows,
		WinnerRelations: winnerRows,
		Trashed:         true,
	})
	if err != nil {
		return BulkOpBatch{}, err
	}

	// Re-point: drop every loser-touching row, then re-insert each as the
	// winner-pointing form. INSERT OR IGNORE dedups against the unique indexes
	// (a collision with a winner row the merge would duplicate is silently
	// dropped — invariant: one winner row per (from, rel, to)). A row that would
	// become a self-loop after re-pointing (winner relating to itself) is dropped.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM entity_relation WHERE from_entity_id = ? OR to_entity_id = ?`,
		loserID, loserID); err != nil {
		return BulkOpBatch{}, err
	}
	for _, row := range loserRows {
		from := row.FromEntityID
		if from == loserID {
			from = winnerID
		}
		toEntity := row.ToEntityID
		if toEntity == loserID {
			toEntity = winnerID
		}
		// Self-loop after re-point (entity-target row whose both ends are now the
		// winner) carries no meaning — drop it.
		if row.ToMediaID == "" && from == toEntity {
			continue
		}
		var toEntityArg, toMediaArg any
		if row.ToMediaID != "" {
			toMediaArg = row.ToMediaID
		} else {
			toEntityArg = toEntity
		}
		var attrsArg any
		if row.Attributes != nil {
			attrsArg = *row.Attributes
		}
		var sortArg any
		if row.SortOrder != nil {
			sortArg = *row.SortOrder
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO entity_relation(from_entity_id, relation, to_entity_id, to_media_id, attributes, sort_order)
			VALUES (?, ?, ?, ?, ?, ?)
		`, from, row.Relation, toEntityArg, toMediaArg, attrsArg, sortArg); err != nil {
			return BulkOpBatch{}, err
		}
	}

	// Soft-delete the loser (audited Trash, invariant #6).
	if _, err := tx.ExecContext(ctx,
		`UPDATE entity SET trashed_at_ms = ? WHERE id = ?`,
		timeToMillis(now.UTC()), loserID); err != nil {
		return BulkOpBatch{}, err
	}
	// Drop the loser's FTS document so a trashed entity can never surface in
	// search (the read-side trash filter covers the join, this keeps the index
	// honest); UndoBatch's restore resyncs it.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM entity_fts WHERE entity_id = ?`, loserID); err != nil {
		return BulkOpBatch{}, err
	}

	batchID, err := newBatchID()
	if err != nil {
		return BulkOpBatch{}, err
	}
	if err := insertBatchTx(ctx, tx, batchID, opKindMergeEntity, now); err != nil {
		return BulkOpBatch{}, err
	}
	if err := appendBatchEntityItemTx(ctx, tx, batchID, 0, loserID, state); err != nil {
		return BulkOpBatch{}, err
	}
	if err := tx.Commit(); err != nil {
		return BulkOpBatch{}, err
	}
	return BulkOpBatch{ID: batchID, Kind: opKindMergeEntity, Affected: 1}, nil
}

// undoEntityMergeBatch reverses an entity-merge batch (op_batch.kind
// "merge-entity"): it replays the entity arm of the journal, restoring each
// loser's prior relation rows and un-trashing it, then marks the batch undone so
// it can never be undone twice. The loser's visibility is re-derived from its
// Library (a trashed entity is invisible to the read path by design, so the
// Reveal check rides the Library, like RestoreEntity); an item whose Library has
// left the caller's scope is skipped, never restored past the contract.
func (s *Store) undoEntityMergeBatch(ctx context.Context, batchID string, filter LibraryFilter, now time.Time) (BulkOpBatch, error) {
	items, err := batchEntityItems(ctx, s.catalogR, batchID)
	if err != nil {
		return BulkOpBatch{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return BulkOpBatch{}, err
	}
	defer tx.Rollback()

	reverted := 0
	for _, item := range items {
		visible, err := entityLibraryVisibleTx(ctx, tx, item.EntityID, filter)
		if err != nil {
			return BulkOpBatch{}, err
		}
		if !visible {
			continue
		}
		if err := undoMergeEntityItemTx(ctx, tx, s, item); err != nil {
			return BulkOpBatch{}, err
		}
		reverted++
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE op_batch SET undone_at_ms = ? WHERE id = ?`, timeToMillis(now), batchID); err != nil {
		return BulkOpBatch{}, err
	}
	if err := tx.Commit(); err != nil {
		return BulkOpBatch{}, err
	}
	return BulkOpBatch{ID: batchID, Kind: opKindMergeEntity, Affected: reverted}, nil
}

// entityLibraryVisibleTx reports whether an Entity's Library is visible to the
// filter, inside a transaction, regardless of the Entity's trash state — the
// restore-time visibility check (a trashed loser is hidden from visibleEntitySQL
// by design, so undo re-derives the Reveal decision from the Library).
func entityLibraryVisibleTx(ctx context.Context, tx *sql.Tx, id EntityID, filter LibraryFilter) (bool, error) {
	var libraryID LibraryID
	var restricted int
	err := tx.QueryRowContext(ctx, `
		SELECT e.library_id, l.is_restricted
		FROM entity e JOIN library l ON l.id = e.library_id
		WHERE e.id = ?
	`, id).Scan(&libraryID, &restricted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return libraryVisible(Library{ID: libraryID, IsRestricted: restricted != 0}, filter), nil
}

// entityRelationRowsTx loads every entity_relation row that touches the entity
// in either role (from or to), inside a transaction, for the merge journal.
func entityRelationRowsTx(ctx context.Context, tx *sql.Tx, id EntityID) ([]entityRelationRow, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT from_entity_id, relation, to_entity_id, to_media_id, attributes, sort_order
		FROM entity_relation
		WHERE from_entity_id = ? OR to_entity_id = ?
	`, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []entityRelationRow
	for rows.Next() {
		var row entityRelationRow
		var toEntity, attrs sql.NullString
		var toMedia sql.NullString
		var sortOrder sql.NullInt64
		if err := rows.Scan(&row.FromEntityID, &row.Relation, &toEntity, &toMedia, &attrs, &sortOrder); err != nil {
			return nil, err
		}
		if toEntity.Valid {
			row.ToEntityID = EntityID(toEntity.String)
		}
		if toMedia.Valid {
			row.ToMediaID = MediaID(toMedia.String)
		}
		if attrs.Valid {
			value := attrs.String
			row.Attributes = &value
		}
		if sortOrder.Valid {
			value := sortOrder.Int64
			row.SortOrder = &value
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// appendBatchEntityItemTx writes one row to the entity arm of the journal
// (op_batch_entity_item, 0058) — the entity sibling of appendBatchItemTx.
func appendBatchEntityItemTx(ctx context.Context, tx *sql.Tx, batchID string, seq int, entityID EntityID, priorState []byte) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO op_batch_entity_item(batch_id, seq, entity_id, prior_state) VALUES (?, ?, ?, ?)`,
		batchID, seq, entityID, string(priorState))
	return err
}

// opBatchEntityItem is one entity-arm journal row read back for undo.
type opBatchEntityItem struct {
	Seq        int
	EntityID   EntityID
	PriorState string
}

// batchEntityItems reads the entity arm of a batch's journal, ordered by seq.
func batchEntityItems(ctx context.Context, db *sql.DB, batchID string) ([]opBatchEntityItem, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT seq, entity_id, prior_state FROM op_batch_entity_item WHERE batch_id = ? ORDER BY seq`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []opBatchEntityItem
	for rows.Next() {
		var item opBatchEntityItem
		if err := rows.Scan(&item.Seq, &item.EntityID, &item.PriorState); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// undoMergeEntityItemTx reverses one entity merge (the inverse of MergeEntities),
// inside the undo transaction: delete every relation row now touching the winner
// or loser, re-insert both entities' journaled prior rows verbatim, un-trash the
// loser, and resync its FTS document. Visibility was re-confirmed by UndoBatch.
func undoMergeEntityItemTx(ctx context.Context, tx *sql.Tx, s *Store, item opBatchEntityItem) error {
	var state entityMergeItemState
	if err := json.Unmarshal([]byte(item.PriorState), &state); err != nil {
		return err
	}
	loserID := item.EntityID
	// The winner is the re-point target shared by every journaled row; recover it
	// from any loser row that pointed at it, else from the snapshot's from-ids.
	winnerID := winnerFromState(state, loserID)

	// Clear the post-merge rows for both entities, then restore the exact prior
	// sets. Deleting by both endpoints covers winner rows the merge created.
	for _, id := range []EntityID{loserID, winnerID} {
		if id == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM entity_relation WHERE from_entity_id = ? OR to_entity_id = ?`,
			id, id); err != nil {
			return err
		}
	}
	for _, row := range append(append([]entityRelationRow{}, state.WinnerRelations...), state.LoserRelations...) {
		var toEntityArg, toMediaArg any
		if row.ToMediaID != "" {
			toMediaArg = row.ToMediaID
		} else if row.ToEntityID != "" {
			toEntityArg = row.ToEntityID
		}
		var attrsArg any
		if row.Attributes != nil {
			attrsArg = *row.Attributes
		}
		var sortArg any
		if row.SortOrder != nil {
			sortArg = *row.SortOrder
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO entity_relation(from_entity_id, relation, to_entity_id, to_media_id, attributes, sort_order)
			VALUES (?, ?, ?, ?, ?, ?)
		`, row.FromEntityID, row.Relation, toEntityArg, toMediaArg, attrsArg, sortArg); err != nil {
			return err
		}
	}
	if state.Trashed {
		if _, err := tx.ExecContext(ctx,
			`UPDATE entity SET trashed_at_ms = NULL WHERE id = ?`, loserID); err != nil {
			return err
		}
		if err := resyncEntityFTSRowTx(ctx, tx, s, loserID); err != nil {
			return err
		}
	}
	return nil
}

// winnerFromState recovers the merge's winner id from a journal snapshot: it is
// the entity (other than the loser) that the loser's relations pointed at, or
// failing that the non-loser source of any winner-snapshot row.
func winnerFromState(state entityMergeItemState, loserID EntityID) EntityID {
	for _, row := range state.LoserRelations {
		if row.ToEntityID != "" && row.ToEntityID != loserID {
			return row.ToEntityID
		}
		if row.FromEntityID != loserID {
			return row.FromEntityID
		}
	}
	for _, row := range state.WinnerRelations {
		if row.FromEntityID != loserID {
			return row.FromEntityID
		}
		if row.ToEntityID != "" && row.ToEntityID != loserID {
			return row.ToEntityID
		}
	}
	return ""
}

// resyncEntityFTSRowTx rebuilds one restored Entity's FTS document (title +
// searchable field values) inside the undo transaction — the inverse of the
// loser-FTS delete MergeEntities did. Mirrors UpsertEntity's FTS write.
func resyncEntityFTSRowTx(ctx context.Context, tx *sql.Tx, s *Store, id EntityID) error {
	var entity Entity
	err := tx.QueryRowContext(ctx,
		`SELECT id, library_id, kind, title FROM entity WHERE id = ?`, id).
		Scan(&entity.ID, &entity.LibraryID, &entity.Kind, &entity.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, table := range entityFieldTables {
		column := "value"
		if table == "entity_field_date" {
			column = "value_ms"
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT field, field_type, `+column+` FROM `+table+` WHERE entity_id = ?`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var field EntityField
			switch table {
			case "entity_field_text":
				err = rows.Scan(&field.Name, &field.Type, &field.Text)
			case "entity_field_real":
				err = rows.Scan(&field.Name, &field.Type, &field.Number)
			case "entity_field_int":
				var value int64
				if err = rows.Scan(&field.Name, &field.Type, &value); err == nil {
					field.Flag = value != 0
				}
			case "entity_field_date":
				var millis int64
				if err = rows.Scan(&field.Name, &field.Type, &millis); err == nil {
					field.Date = millisToTime(millis)
				}
			}
			if err != nil {
				rows.Close()
				return err
			}
			entity.Fields = append(entity.Fields, field)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entity_fts WHERE entity_id = ?`, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO entity_fts(entity_id, title, fields) VALUES (?, ?, ?)
	`, id, entity.Title, s.entitySearchTextLocked(entity))
	return err
}

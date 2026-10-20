package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Bulk metadata edit with the shared dry-run / batch-undo envelope (ADR-0009
// §6). A bulk edit replaces a fixed set of plugin-declared field values across
// many Media at once. The envelope has three verbs:
//
//   - BulkEditPreview — pure read: the full change-set (old→new per item) the
//     client previews before committing. No write, no journal.
//   - BulkEditApply — one batch = one journal row = one undo unit. Each item's
//     prior field values are snapshotted into op_batch_item before the new
//     values land, so UndoBatch can restore the exact prior state.
//   - UndoBatch (below) — replays the journal for any batch kind.
//
// Every read is reveal-and-Library filtered; an item naming a Media the caller
// cannot see is dropped from the change-set entirely (never an existence
// oracle), exactly like the rest of the contract.

// Op-batch kinds — the closed, shape-named vocabulary stored in op_batch.kind.
const (
	opKindBulkEdit = "bulk-edit"
	opKindOrganize = "organize"
	opKindMove     = "move"
	opKindMerge    = "merge"
	opKindAITagRun = "ai-enrichment"
)

// BulkEditChange is one Media's preview row: its identity plus the field values
// before and after the edit. Old holds only the fields the edit touches that
// already had a value; New is the effective new value for every edited field.
type BulkEditChange struct {
	MediaID   MediaID
	LibraryID LibraryID
	Title     string
	Old       []MediaField
	New       []MediaField
}

// BulkOpBatch is the result of an applied bulk operation: the batch id (the
// undo handle) and how many items it affected.
type BulkOpBatch struct {
	ID       string
	Kind     string
	Affected int
}

// bulkEditItemState is the per-item journal snapshot for a bulk edit. Edited
// names every field the edit touched; Fields holds the values those fields had
// before the edit (only the ones that existed). Undo clears every Edited name,
// then re-inserts the prior Fields — so a field the edit *created* (in Edited
// but absent from Fields) is removed, and a field it *changed* is restored.
type bulkEditItemState struct {
	Edited []string     `json:"edited"`
	Fields []MediaField `json:"fields"`
}

const encryptedBulkEditError = "bulk editing is unavailable for encrypted media"

func (s *Store) rejectEncryptedBulkEditTargetsLocked(ctx context.Context, visible map[MediaID]Media) error {
	for id := range visible {
		plane, err := s.catalogPlaneForMediaLocked(ctx, id)
		if err != nil {
			return err
		}
		if plane.encrypted {
			return ValidationError(encryptedBulkEditError)
		}
	}
	return nil
}

// BulkEditPreview returns the change-set a bulk edit WOULD produce over the
// given Media (ADR-0009 §6 dry-run). Media outside the caller's scope are
// dropped silently; the result preserves the input order of the visible ones.
func (s *Store) BulkEditPreview(ctx context.Context, ids []MediaID, fields []MediaField, filter LibraryFilter) ([]BulkEditChange, error) {
	if err := validateMediaFields(fields); err != nil {
		return nil, ValidationError(err.Error())
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	visible, err := s.visibleMediaBatchSQL(ctx, ids, filter)
	if err != nil {
		return nil, err
	}
	if err := s.rejectEncryptedBulkEditTargetsLocked(ctx, visible); err != nil {
		return nil, err
	}
	edited := fieldNameSet(fields)
	changes := make([]BulkEditChange, 0, len(ids))
	seen := map[MediaID]bool{}
	for _, id := range ids {
		media, ok := visible[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		current, err := s.mediaFieldsSQL(ctx, id)
		if err != nil {
			return nil, err
		}
		old := make([]MediaField, 0, len(fields))
		for _, f := range current {
			if edited[f.Name] {
				old = append(old, f)
			}
		}
		changes = append(changes, BulkEditChange{
			MediaID:   media.ID,
			LibraryID: media.LibraryID,
			Title:     media.Title,
			Old:       old,
			New:       fields,
		})
	}
	return changes, nil
}

// BulkEditApply commits a bulk edit as one undo-able batch. For each visible
// Media it snapshots the prior values of the edited fields into the journal,
// then upserts the new values (UpsertMediaFields semantics: only the named
// fields change). Returns the batch id (the undo handle) and the affected
// count. An empty visible set writes no batch and reports zero affected.
func (s *Store) BulkEditApply(ctx context.Context, ids []MediaID, fields []MediaField, filter LibraryFilter, now time.Time) (BulkOpBatch, error) {
	if err := validateMediaFields(fields); err != nil {
		return BulkOpBatch{}, ValidationError(err.Error())
	}
	if len(fields) == 0 {
		return BulkOpBatch{}, ValidationError("bulk edit requires at least one field")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return BulkOpBatch{}, err
	}
	visible, err := s.visibleMediaBatchSQL(ctx, ids, filter)
	if err != nil {
		return BulkOpBatch{}, err
	}
	if err := s.rejectEncryptedBulkEditTargetsLocked(ctx, visible); err != nil {
		return BulkOpBatch{}, err
	}
	editedNames := make([]string, 0, len(fields))
	for _, f := range fields {
		editedNames = append(editedNames, f.Name)
	}
	// Dedup and drop out-of-scope ids BEFORE touching the DB: an empty result
	// here means no batch is written at all (no BeginTx, no rollback round
	// trip either) — same "empty visible set writes no batch" contract as
	// before, just without opening a transaction to discover it.
	editIDs := make([]MediaID, 0, len(ids))
	seen := map[MediaID]bool{}
	for _, id := range ids {
		if _, ok := visible[id]; !ok || seen[id] {
			continue
		}
		seen[id] = true
		editIDs = append(editIDs, id)
	}
	if len(editIDs) == 0 {
		return BulkOpBatch{ID: "", Kind: opKindBulkEdit, Affected: 0}, nil
	}

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return BulkOpBatch{}, err
	}
	defer tx.Rollback()

	batchID, err := newBatchID()
	if err != nil {
		return BulkOpBatch{}, err
	}
	// The batch parent must precede its items (op_batch_item.batch_id FK).
	if err := insertBatchTx(ctx, tx, batchID, opKindBulkEdit, now); err != nil {
		return BulkOpBatch{}, err
	}

	// Snapshot every target's prior values for the edited fields with set-based
	// reads chunked by ID — a handful of queries
	// instead of one query per field table per Media.
	prior, err := mediaFieldsBatchByNamesTx(ctx, tx, editIDs, editedNames)
	if err != nil {
		return BulkOpBatch{}, err
	}
	items := make([]batchItemInsert, len(editIDs))
	for i, id := range editIDs {
		state, err := json.Marshal(bulkEditItemState{Edited: editedNames, Fields: prior[id]})
		if err != nil {
			return BulkOpBatch{}, err
		}
		items[i] = batchItemInsert{Seq: i, MediaID: id, PriorState: state}
	}
	if err := appendBatchItemsTx(ctx, tx, batchID, items); err != nil {
		return BulkOpBatch{}, err
	}

	// Apply the new values with set-based statements chunked by ID
	//: every target shares the same new field values,
	// so the write collapses to a few statements per 500-ID chunk regardless
	// of how many Media are being edited, instead of one DELETE per field per
	// table plus one INSERT per field for every single Media.
	if err := upsertMediaFieldsBatchTx(ctx, tx, editIDs, fields); err != nil {
		return BulkOpBatch{}, err
	}

	if err := tx.Commit(); err != nil {
		return BulkOpBatch{}, err
	}
	// Emit a realtime media.updated per edited Media AFTER the catalog tx
	// committed (cross-DB ordering: see appendMediaEventLocked), batched with
	// one multi-row INSERT per chunk instead of one
	// events-writer statement per Media. A failed outbox append must not fail
	// the edit — the values are already persisted; the worst case is a missed
	// live push the next read reconciles.
	if eventErr := s.appendMediaEventsLocked(ctx, MediaEventUpdated, editIDs, nil, now); eventErr != nil {
		slog.Warn("media outbox events append failed", "kind", MediaEventUpdated, "count", len(editIDs), "error", eventErr)
	}
	return BulkOpBatch{ID: batchID, Kind: opKindBulkEdit, Affected: len(editIDs)}, nil
}

// UndoBatch reverses an applied batch (ADR-0009 §6: one batch = one undo unit).
// It replays each item's journaled prior state — restoring field values for a
// bulk edit, the prior file path for an organize/move, or the trashed flag for
// a merge — then marks the batch undone so it can never be undone twice. The
// batch's items must reach Media the caller can still see; an item whose Media
// has left the caller's scope is skipped, never resurrected past the contract.
func (s *Store) UndoBatch(ctx context.Context, batchID string, filter LibraryFilter, now time.Time) (BulkOpBatch, error) {
	if batchID == "" {
		return BulkOpBatch{}, ValidationError("batch id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return BulkOpBatch{}, err
	}

	var kind string
	var undoneAt int64
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT kind, undone_at_ms FROM op_batch WHERE id = ?`, batchID).Scan(&kind, &undoneAt)
	if errors.Is(err, sql.ErrNoRows) {
		return BulkOpBatch{}, ValidationError("batch not found")
	}
	if err != nil {
		return BulkOpBatch{}, err
	}
	if undoneAt != 0 {
		return BulkOpBatch{}, ValidationError("batch already undone")
	}

	// Entity-merge batches journal their item in the entity arm of the journal
	// (op_batch_entity_item), keyed by entity not Media, so they take a separate
	// replay path; every other kind reads the Media arm (op_batch_item).
	if kind == opKindMergeEntity {
		return s.undoEntityMergeBatch(ctx, batchID, filter, now)
	}
	if kind == opKindBulkEdit {
		ids, err := batchItemMediaIDs(ctx, s.catalogR, batchID)
		if err != nil {
			return BulkOpBatch{}, err
		}
		visible, err := s.visibleMediaBatchSQL(ctx, ids, filter)
		if err != nil {
			return BulkOpBatch{}, err
		}
		if err := s.rejectEncryptedBulkEditTargetsLocked(ctx, visible); err != nil {
			return BulkOpBatch{}, err
		}
	}

	items, err := batchItems(ctx, s.catalogR, batchID)
	if err != nil {
		return BulkOpBatch{}, err
	}

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return BulkOpBatch{}, err
	}
	defer tx.Rollback()

	reverted := 0
	// Realtime events to emit per reverted item, AFTER the catalog tx commits
	// (see the emit loop below). A field/path-state undo (bulk edit, organize,
	// move) restores prior values on a still-present Media → media.updated. A
	// merge undo that resurrects a trashed loser re-adds it to the catalog →
	// media.created, mirroring RestoreMedia.
	type mediaEvent struct {
		kind string
		id   MediaID
	}
	var events []mediaEvent
	var diskMoves []organizeMove
	fail := func(err error) (BulkOpBatch, error) {
		return BulkOpBatch{}, compensateFileMoves(err, diskMoves)
	}
	for _, item := range items {
		// Re-confirm visibility under the caller's scope at undo time.
		if _, err := visibleMediaTx(ctx, tx, item.MediaID, filter); err != nil {
			if errors.Is(err, errMediaNotVisible) {
				continue
			}
			return fail(err)
		}
		switch kind {
		case opKindBulkEdit:
			if err := undoBulkEditItemTx(ctx, tx, item); err != nil {
				return fail(err)
			}
			events = append(events, mediaEvent{MediaEventUpdated, item.MediaID})
		case opKindOrganize, opKindMove:
			move, moved, err := s.undoFileMoveItemTx(ctx, tx, item)
			if moved {
				diskMoves = append(diskMoves, move)
			}
			if err != nil {
				return fail(err)
			}
			events = append(events, mediaEvent{MediaEventUpdated, item.MediaID})
		case opKindMerge:
			resurrected, err := undoMergeItemTx(ctx, tx, item)
			if err != nil {
				return fail(err)
			}
			// Only a loser the merge actually soft-deleted comes back; one that
			// wasn't trashed is a no-op and emits nothing.
			if resurrected {
				events = append(events, mediaEvent{MediaEventCreated, item.MediaID})
			}
		case opKindAITagRun:
			if err := undoEnrichmentRunItemTx(ctx, tx, item, batchID, now); err != nil {
				return fail(err)
			}
			events = append(events, mediaEvent{MediaEventUpdated, item.MediaID})
		default:
			return fail(ValidationError("unknown batch kind"))
		}
		reverted++
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE op_batch SET undone_at_ms = ? WHERE id = ?`, timeToMillis(now), batchID); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	// Emit the realtime events AFTER the catalog tx committed (cross-DB ordering:
	// see appendMediaEventLocked), still holding s.mu and past guardLocked, exactly
	// like BulkEditApply / MergeApply / RestoreMedia. A failed outbox append must
	// not fail the undo — the state is already persisted; the next read reconciles.
	for _, ev := range events {
		if eventErr := s.appendMediaEventLocked(ctx, ev.kind, ev.id, nil, now); eventErr != nil {
			slog.Warn("media outbox event append failed", "kind", ev.kind, "media", ev.id, "error", eventErr)
		}
	}
	return BulkOpBatch{ID: batchID, Kind: kind, Affected: reverted}, nil
}

// undoBulkEditItemTx restores one bulk-edit item's prior field values. Every
// field the edit touched is cleared first (so a field the edit *created* — in
// Edited but with no prior value — is removed, not left at the new value), then
// the prior values are re-inserted (restoring a field the edit *changed*).
func undoBulkEditItemTx(ctx context.Context, tx *sql.Tx, item opBatchItem) error {
	var state bulkEditItemState
	if err := json.Unmarshal([]byte(item.PriorState), &state); err != nil {
		return err
	}
	current, err := mediaFieldsTx(ctx, tx, item.MediaID)
	if err != nil {
		return err
	}
	edited := map[string]bool{}
	for _, name := range state.Edited {
		edited[name] = true
		for _, table := range mediaFieldTables {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE media_id = ? AND field = ?`, item.MediaID, name); err != nil {
				return err
			}
		}
	}
	for _, field := range current {
		if edited[field.Name] {
			if err := rejectMediaFieldValueTx(ctx, tx, item.MediaID, field); err != nil {
				return err
			}
		}
	}
	return insertMediaFields(ctx, tx, item.MediaID, state.Fields)
}

// ---- shared journal helpers (used by bulk_edit.go and file_operations.go) ----

// opBatchItem is one journal row read back for undo.
type opBatchItem struct {
	Seq        int
	MediaID    MediaID
	PriorState string
}

func newBatchID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "batch-" + hex.EncodeToString(raw[:]), nil
}

func insertBatchTx(ctx context.Context, tx *sql.Tx, id, kind string, now time.Time) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO op_batch(id, kind, created_at_ms) VALUES (?, ?, ?)`,
		id, kind, timeToMillis(now.UTC()))
	return err
}

func appendBatchItemTx(ctx context.Context, tx *sql.Tx, batchID string, seq int, mediaID MediaID, priorState []byte) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO op_batch_item(batch_id, seq, media_id, prior_state) VALUES (?, ?, ?, ?)`,
		batchID, seq, mediaID, string(priorState))
	return err
}

func batchItems(ctx context.Context, db *sql.DB, batchID string) ([]opBatchItem, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT seq, media_id, prior_state FROM op_batch_item WHERE batch_id = ? ORDER BY seq`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []opBatchItem
	for rows.Next() {
		var item opBatchItem
		if err := rows.Scan(&item.Seq, &item.MediaID, &item.PriorState); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func batchItemMediaIDs(ctx context.Context, db *sql.DB, batchID string) ([]MediaID, error) {
	rows, err := db.QueryContext(ctx, `SELECT media_id FROM op_batch_item WHERE batch_id = ?`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []MediaID
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func fieldNameSet(fields []MediaField) map[string]bool {
	names := make(map[string]bool, len(fields))
	for _, f := range fields {
		names[f.Name] = true
	}
	return names
}

// errMediaNotVisible marks a Media that exists but is outside the caller's
// scope at undo time — undo skips it rather than failing the whole batch.
var errMediaNotVisible = errors.New("media not visible")

// visibleMediaTx confirms a Media is visible to the filter, inside a write
// transaction. Returns errMediaNotVisible for an out-of-scope or missing item.
func visibleMediaTx(ctx context.Context, tx *sql.Tx, id MediaID, filter LibraryFilter) (LibraryID, error) {
	var libraryID LibraryID
	var restricted int
	err := tx.QueryRowContext(ctx, `
		SELECT m.library_id, l.is_restricted
		FROM media m JOIN library l ON l.id = m.library_id
		WHERE m.id = ?
	`, id).Scan(&libraryID, &restricted)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errMediaNotVisible
	}
	if err != nil {
		return "", err
	}
	if !libraryVisible(Library{ID: libraryID, IsRestricted: restricted != 0}, filter) {
		return "", errMediaNotVisible
	}
	return libraryID, nil
}

// mediaFieldsTx loads a Media's declared field values inside a transaction (the
// read-side twin of mediaFieldsSQL, used so apply snapshots the values it is
// about to overwrite within the same transaction).
func mediaFieldsTx(ctx context.Context, tx *sql.Tx, id MediaID) ([]MediaField, error) {
	fields := []MediaField{}
	for _, table := range mediaFieldTables {
		column := "value"
		if table == "media_field_date" {
			column = "value_ms"
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT field, field_type, `+column+` FROM `+table+` WHERE media_id = ?`, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var field MediaField
			switch table {
			case "media_field_text":
				err = rows.Scan(&field.Name, &field.Type, &field.Text)
			case "media_field_real":
				err = rows.Scan(&field.Name, &field.Type, &field.Number)
			case "media_field_int":
				var value int64
				if err = rows.Scan(&field.Name, &field.Type, &value); err == nil {
					field.Flag = value != 0
				}
			case "media_field_date":
				var millis int64
				if err = rows.Scan(&field.Name, &field.Type, &millis); err == nil {
					field.Date = millisToTime(millis)
				}
			}
			if err != nil {
				rows.Close()
				return nil, err
			}
			fields = append(fields, field)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return fields, nil
}

// bulkWriteChunkSize bounds every batched bulk-edit and collection-mutation
// statement (this file and collections.go) to one query/exec per this many
// Media, matching the chunk size visibleMediaBatchSQL
// already uses for the same IN-clause-size reason: statement count grows with
// ceil(selection size / chunk), not 1:1 with the selection size.
const bulkWriteChunkSize = 500

// mediaFieldsBatchByNamesTx loads the current values of the named fields for
// many Media at once, batched by ID: 4 queries per
// chunk (one per field table) instead of 4 queries per Media. Used by
// BulkEditApply to snapshot every target's prior state for the undo journal.
func mediaFieldsBatchByNamesTx(ctx context.Context, tx *sql.Tx, ids []MediaID, names []string) (map[MediaID][]MediaField, error) {
	prior := make(map[MediaID][]MediaField, len(ids))
	for _, id := range ids {
		prior[id] = []MediaField{}
	}
	if len(ids) == 0 || len(names) == 0 {
		return prior, nil
	}
	namePlaceholders := make([]string, len(names))
	nameArgs := make([]any, len(names))
	for i, name := range names {
		namePlaceholders[i] = "?"
		nameArgs[i] = name
	}
	nameClause := " AND field IN (" + strings.Join(namePlaceholders, ", ") + ")"
	for start := 0; start < len(ids); start += bulkWriteChunkSize {
		chunk := ids[start:min(start+bulkWriteChunkSize, len(ids))]
		idPlaceholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)+len(names))
		for i, id := range chunk {
			idPlaceholders[i] = "?"
			args = append(args, id)
		}
		args = append(args, nameArgs...)
		idClause := "media_id IN (" + strings.Join(idPlaceholders, ", ") + ")"
		for _, table := range mediaFieldTables {
			column := "value"
			if table == "media_field_date" {
				column = "value_ms"
			}
			//nolint:gosec // G202: table/column come from mediaFieldTables' closed enum + a literal switch; clauses are joined "?" placeholders, all values bound via args.
			rows, err := tx.QueryContext(ctx, `
				SELECT media_id, field, field_type, `+column+` FROM `+table+`
				WHERE `+idClause+nameClause, args...)
			if err != nil {
				return nil, err
			}
			if err := scanMediaFieldsBatch(rows, table, prior); err != nil {
				return nil, err
			}
		}
	}
	return prior, nil
}

// scanMediaFieldsBatch reads one field table's rows into the per-Media prior
// map, mirroring mediaFieldsTx's per-table scan shape but keyed by media_id.
func scanMediaFieldsBatch(rows *sql.Rows, table string, dst map[MediaID][]MediaField) error {
	defer rows.Close()
	for rows.Next() {
		var id MediaID
		var field MediaField
		var err error
		switch table {
		case "media_field_text":
			err = rows.Scan(&id, &field.Name, &field.Type, &field.Text)
		case "media_field_real":
			err = rows.Scan(&id, &field.Name, &field.Type, &field.Number)
		case "media_field_int":
			var value int64
			if err = rows.Scan(&id, &field.Name, &field.Type, &value); err == nil {
				field.Flag = value != 0
			}
		case "media_field_date":
			var millis int64
			if err = rows.Scan(&id, &field.Name, &field.Type, &millis); err == nil {
				field.Date = millisToTime(millis)
			}
		}
		if err != nil {
			return err
		}
		dst[id] = append(dst[id], field)
	}
	return rows.Err()
}

// batchItemInsert is one pending op_batch_item row for appendBatchItemsTx.
type batchItemInsert struct {
	Seq        int
	MediaID    MediaID
	PriorState []byte
}

// appendBatchItemsTx journals many batch items with one multi-row INSERT per
// chunk instead of one INSERT per item.
func appendBatchItemsTx(ctx context.Context, tx *sql.Tx, batchID string, items []batchItemInsert) error {
	for start := 0; start < len(items); start += bulkWriteChunkSize {
		chunk := items[start:min(start+bulkWriteChunkSize, len(items))]
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)*4)
		for i, item := range chunk {
			placeholders[i] = "(?, ?, ?, ?)"
			args = append(args, batchID, item.Seq, item.MediaID, string(item.PriorState))
		}
		//nolint:gosec // G202: the concatenation is only joined literal "(?, ?, ?, ?)" placeholder groups; all values bound via args.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO op_batch_item(batch_id, seq, media_id, prior_state) VALUES `+strings.Join(placeholders, ", "),
			args...); err != nil {
			return err
		}
	}
	return nil
}

// upsertMediaFieldsBatchTx is the batched form of the old upsertMediaFieldsTx:
// it replaces the same field values across many Media with set-based
// statements chunked by ID, instead of one DELETE per
// field per table and one INSERT per field for every single Media. A bulk
// edit applies identical new values to every target, so each field becomes
// one multi-row DELETE/INSERT per chunk regardless of selection size. Every
// edited field auto-locks at the manual tier — a re-identify / scheduled
// re-match cannot overwrite it; manual is the top precedence,
// so the write always lands.
func upsertMediaFieldsBatchTx(ctx context.Context, tx *sql.Tx, ids []MediaID, fields []MediaField) error {
	if len(ids) == 0 {
		return nil
	}
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	for start := 0; start < len(ids); start += bulkWriteChunkSize {
		chunk := ids[start:min(start+bulkWriteChunkSize, len(ids))]
		if err := clearMediaFieldsBatchTx(ctx, tx, chunk, names); err != nil {
			return err
		}
		if err := recordFieldProvenanceBatchTx(ctx, tx, chunk, names, MutationSourceManual); err != nil {
			return err
		}
		if err := insertMediaFieldsBatchTx(ctx, tx, chunk, fields); err != nil {
			return err
		}
	}
	return nil
}

// clearMediaFieldsBatchTx defensively clears the named fields from all 4
// typed tables for a chunk of Media (a field's type may have changed since it
// was last written) — one DELETE per table for the whole chunk instead of one
// DELETE per field per table per Media.
func clearMediaFieldsBatchTx(ctx context.Context, tx *sql.Tx, ids []MediaID, names []string) error {
	idPlaceholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+len(names))
	for i, id := range ids {
		idPlaceholders[i] = "?"
		args = append(args, id)
	}
	namePlaceholders := make([]string, len(names))
	for i, name := range names {
		namePlaceholders[i] = "?"
		args = append(args, name)
	}
	where := ` WHERE media_id IN (` + strings.Join(idPlaceholders, ", ") + `) AND field IN (` + strings.Join(namePlaceholders, ", ") + `)`
	for _, table := range mediaFieldTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+where, args...); err != nil { //nolint:gosec // G202: table from mediaFieldTables' closed enum; where is joined "?" placeholders, all values bound via args.
			return err
		}
	}
	return nil
}

// recordFieldProvenanceBatchTx is the batched form of recordFieldProvenanceTx:
// one upsert per field covering a whole chunk of Media, instead of one upsert
// per (Media, field) pair. Every target in a bulk edit shares the same write
// source, so the precedence comparison embeds one literal rank (not
// user-controlled — it comes from metadataSourceRank, the package's own
// closed enum) shared by the whole statement instead of a bound parameter.
func recordFieldProvenanceBatchTx(ctx context.Context, tx *sql.Tx, ids []MediaID, names []string, source MutationSource) error {
	if len(ids) == 0 || len(names) == 0 {
		return nil
	}
	rank := strconv.Itoa(metadataSourceRank(source))
	rowPlaceholders := make([]string, len(ids))
	for i := range ids {
		rowPlaceholders[i] = "(?, ?, ?)"
	}
	values := strings.Join(rowPlaceholders, ", ")
	for _, name := range names {
		args := make([]any, 0, len(ids)*3)
		for _, id := range ids {
			args = append(args, id, name, string(source))
		}
		//nolint:gosec // G202: rank is an integer literal from metadataSourceRank's closed enum, sourceRankValues renders that same package-owned enum, values is joined "(?, ?, ?)" placeholders; all values bound via args.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO media_field_provenance(media_id, field, source) VALUES `+values+`
			ON CONFLICT(media_id, field) DO UPDATE SET source = excluded.source
			WHERE `+rank+` >= (SELECT rank FROM (`+sourceRankValues()+`) WHERE src = media_field_provenance.source)
		`, args...); err != nil {
			return err
		}
	}
	return nil
}

// insertMediaFieldsBatchTx is the batched form of insertMediaFields: every
// target Media in a bulk edit gets the same new field values, so each field
// becomes one multi-row INSERT across the whole chunk instead of one INSERT
// per Media.
func insertMediaFieldsBatchTx(ctx context.Context, tx *sql.Tx, ids []MediaID, fields []MediaField) error {
	if len(ids) == 0 {
		return nil
	}
	rowPlaceholders := make([]string, len(ids))
	for i := range ids {
		rowPlaceholders[i] = "(?, ?, ?, ?)"
	}
	values := strings.Join(rowPlaceholders, ", ")
	for _, field := range fields {
		var table string
		var value any
		switch field.Type {
		case EntityFieldText, EntityFieldURL:
			table, value = "media_field_text", field.Text
		case EntityFieldNumber:
			table, value = "media_field_real", field.Number
		case EntityFieldFlag:
			table, value = "media_field_int", boolToInt(field.Flag)
		case EntityFieldDate:
			table, value = "media_field_date", timeToMillis(field.Date.UTC())
		}
		column := "value"
		if table == "media_field_date" {
			column = "value_ms"
		}
		args := make([]any, 0, len(ids)*4)
		for _, id := range ids {
			args = append(args, id, field.Name, field.Type, value)
		}
		//nolint:gosec // G202: table/column come from mediaFieldTables' closed enum + a literal switch; values is joined placeholder groups, all values bound via args.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO `+table+`(media_id, field, field_type, `+column+`) VALUES `+values,
			args...); err != nil {
			return err
		}
	}
	return nil
}

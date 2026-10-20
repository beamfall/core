package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// EnrichmentRun is one attributed AI enrichment pass. Its ID is also the
// op_batch ID used by UndoBatch for audited revert.
type EnrichmentRun struct {
	ID         string
	Writer     string
	StartedAt  time.Time
	RevertedAt time.Time
}

type enrichmentRunItemState struct {
	PendingID string       `json:"pendingId"`
	Edited    []string     `json:"edited"`
	Fields    []MediaField `json:"fields"`
	Applied   []MediaField `json:"applied"`
}

func (s *Store) BeginEnrichmentRun(ctx context.Context, writer string, now time.Time) (EnrichmentRun, error) {
	if writer == "" {
		return EnrichmentRun{}, ValidationError("writer is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return EnrichmentRun{}, err
	}
	id, err := newBatchID()
	if err != nil {
		return EnrichmentRun{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return EnrichmentRun{}, err
	}
	defer tx.Rollback()
	if err := insertBatchTx(ctx, tx, id, opKindAITagRun, now); err != nil {
		return EnrichmentRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return EnrichmentRun{}, err
	}
	return EnrichmentRun{ID: id, Writer: writer, StartedAt: now.UTC()}, nil
}

func (s *Store) RevertEnrichmentRun(ctx context.Context, runID string, filter LibraryFilter, now time.Time) (BulkOpBatch, error) {
	return s.UndoBatch(ctx, runID, filter, now)
}

func (s *Store) enrichmentRunExistsSQL(ctx context.Context, id string) error {
	var kind string
	err := s.catalogR.QueryRowContext(ctx, `SELECT kind FROM op_batch WHERE id = ?`, id).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return ValidationError("enrichment run not found")
	}
	if err != nil {
		return err
	}
	if kind != opKindAITagRun {
		return ValidationError("enrichment run not found")
	}
	return nil
}

func (s *Store) aiTagTombstonedSQL(ctx context.Context, mediaID MediaID, tagKind, value string) (bool, error) {
	var one int
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT 1 FROM media_field_rejection
		WHERE media_id = ? AND field = ? AND field_type IN ('', ?) AND value = ?
		LIMIT 1`,
		mediaID, tagKind, EntityFieldText, value,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func appendEnrichmentRunItemTx(ctx context.Context, tx *sql.Tx, runID, pendingID string, mediaID MediaID, field MediaField) error {
	if seq, state, ok, err := enrichmentRunStateByPendingIDTx(ctx, tx, runID, pendingID); err != nil {
		return err
	} else if ok {
		state.Edited = appendUniqueString(state.Edited, field.Name)
		state.Applied = append(state.Applied, field)
		encoded, err := json.Marshal(state)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE op_batch_item SET prior_state = ? WHERE batch_id = ? AND seq = ?`,
			string(encoded), runID, seq)
		return err
	}
	return appendEnrichmentRunStateTx(ctx, tx, runID, mediaID, enrichmentRunItemState{
		PendingID: pendingID,
		Edited:    []string{field.Name},
		Applied:   []MediaField{field},
	})
}

func appendEnrichmentRunProposalTx(ctx context.Context, tx *sql.Tx, runID, pendingID string, mediaID MediaID) error {
	if _, _, ok, err := enrichmentRunStateByPendingIDTx(ctx, tx, runID, pendingID); err != nil || ok {
		return err
	}
	return appendEnrichmentRunStateTx(ctx, tx, runID, mediaID, enrichmentRunItemState{PendingID: pendingID})
}

func appendEnrichmentRunStateTx(ctx context.Context, tx *sql.Tx, runID string, mediaID MediaID, state enrichmentRunItemState) error {
	seq, err := nextBatchSeqTx(ctx, tx, runID)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return appendBatchItemTx(ctx, tx, runID, seq, mediaID, encoded)
}

func enrichmentRunStateByPendingIDTx(ctx context.Context, tx *sql.Tx, runID, pendingID string) (int, enrichmentRunItemState, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT seq, prior_state FROM op_batch_item WHERE batch_id = ? ORDER BY seq`, runID)
	if err != nil {
		return 0, enrichmentRunItemState{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var raw string
		if err := rows.Scan(&seq, &raw); err != nil {
			return 0, enrichmentRunItemState{}, false, err
		}
		var state enrichmentRunItemState
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return 0, enrichmentRunItemState{}, false, err
		}
		if state.PendingID == pendingID {
			return seq, state, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return 0, enrichmentRunItemState{}, false, err
	}
	return 0, enrichmentRunItemState{}, false, nil
}

func nextBatchSeqTx(ctx context.Context, tx *sql.Tx, batchID string) (int, error) {
	var next sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX(seq) + 1 FROM op_batch_item WHERE batch_id = ?`, batchID,
	).Scan(&next); err != nil {
		return 0, err
	}
	if !next.Valid {
		return 0, nil
	}
	return int(next.Int64), nil
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func mediaFieldExistsTx(ctx context.Context, tx *sql.Tx, id MediaID, name string) (bool, error) {
	for _, table := range mediaFieldTables {
		var one int
		err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM `+table+` WHERE media_id = ? AND field = ? LIMIT 1`, id, name,
		).Scan(&one)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	return false, nil
}

func undoEnrichmentRunItemTx(ctx context.Context, tx *sql.Tx, item opBatchItem, runID string, now time.Time) error {
	var state enrichmentRunItemState
	if err := json.Unmarshal([]byte(item.PriorState), &state); err != nil {
		return err
	}
	if state.PendingID != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE pending_ai_tag SET status = ? WHERE id = ?`, string(PendingAITagRejected), state.PendingID); err != nil {
			return err
		}
	}
	for _, field := range state.Applied {
		if err := rejectMediaFieldValueTx(ctx, tx, item.MediaID, field); err != nil {
			return err
		}
	}
	for _, name := range state.Edited {
		for _, table := range mediaFieldTables {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE media_id = ? AND field = ?`, item.MediaID, name); err != nil {
				return err
			}
		}
	}
	return insertMediaFields(ctx, tx, item.MediaID, state.Fields)
}

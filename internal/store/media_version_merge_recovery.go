package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (s *Store) recoverVersionCopyEngagement(ctx context.Context) error {
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT b.id, b.created_at_ms, json_extract(i.prior_state, '$.engagementLibraryId'),
		  EXISTS (SELECT 1 FROM media_insert_seq
		          WHERE seq = json_extract(i.prior_state, '$.survivorInsertSeq')
		            AND media_id = json_extract(i.prior_state, '$.survivor'))
		FROM op_batch b JOIN op_batch_item i ON i.batch_id = b.id
		WHERE b.kind = ? AND b.undone_at_ms = 0
		  AND json_extract(i.prior_state, '$.engagementLibraryId') IS NOT NULL
		ORDER BY b.rowid, i.seq
	`, opKindMergeVersion)
	if err != nil {
		return err
	}
	type pendingMerge struct {
		batchID      string
		atMS         int64
		library      LibraryID
		sameSurvivor bool
	}
	var pending []pendingMerge
	for rows.Next() {
		var merge pendingMerge
		if err := rows.Scan(&merge.batchID, &merge.atMS, &merge.library, &merge.sameSurvivor); err != nil {
			return errors.Join(err, rows.Close())
		}
		pending = append(pending, merge)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, merge := range pending {
		if merge.sameSurvivor {
			if err := s.finishVersionCopyEngagement(ctx, merge.batchID, merge.library, time.UnixMilli(merge.atMS)); err != nil {
				return fmt.Errorf("recover Version merge %s: %w", merge.batchID, err)
			}
		}
		if err := clearVersionCopyEngagement(ctx, tx, merge.batchID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Discard staging without a matching committed merge and survivor identity.
	_, err = s.eventsW.ExecContext(ctx, "DELETE FROM version_merge_playback")
	return err
}

// Stage before the catalog commit. Recovery ignores rows without a committed
// catalog batch, and erasure can remove rows without consulting the catalog.
func (s *Store) stageVersionMergePlayback(ctx context.Context, batchID string, from, to MediaID) error {
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	events, err := versionMergeProgressEventsTx(ctx, tx, from, to, true)
	if err != nil {
		return err
	}
	for _, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO version_merge_playback (batch_id, profile_id, source_id, media_id, payload)
			VALUES (?, ?, ?, ?, ?)
		`, batchID, event.ProfileID, from, to, string(payload)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func applyPendingVersionMergePlayback(ctx context.Context, tx *sql.Tx, batchID string) error {
	rows, err := tx.QueryContext(ctx, "DELETE FROM version_merge_playback WHERE batch_id = ? RETURNING payload", batchID)
	if err != nil {
		return err
	}
	var events []playbackProjectionEvent
	for rows.Next() {
		var payload string
		var event playbackProjectionEvent
		if err := rows.Scan(&payload); err != nil {
			return errors.Join(err, rows.Close())
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return errors.Join(err, rows.Close())
		}
		events = append(events, event)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, event := range events {
		if err := recordVersionMergeProgressTx(ctx, tx, event); err != nil {
			return err
		}
	}
	return nil
}

// The activity acknowledgement commits with the playback delta. Favorites,
// ratings, watchlist and queue changes are only attempted by the initial write.
func (s *Store) finishVersionCopyEngagement(ctx context.Context, batchID string, libraryID LibraryID, now time.Time) error {
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := applyPendingVersionMergePlayback(ctx, tx, batchID); err != nil {
		return err
	}
	// The activity lookup only deduplicates the audit entry. Pending rows
	// themselves determine which playback contributions to apply.
	var audited bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM activity_entry WHERE action = ? AND target_id = ?)
	`, ActivityMediaMerge, batchID).Scan(&audited); err != nil {
		return err
	}
	if audited {
		return tx.Commit()
	}
	if err := appendActivitySQL(ctx, tx, ActivityEntry{
		OccurredAt: now, Action: ActivityMediaMerge, TargetType: "batch",
		TargetID: batchID, LibraryID: libraryID, OperationID: batchID,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func clearVersionCopyEngagement(ctx context.Context, db sqlExecer, batchID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE op_batch_item SET prior_state = json_remove(prior_state, '$.engagementLibraryId', '$.survivorInsertSeq')
		WHERE batch_id = ? AND json_extract(prior_state, '$.engagementLibraryId') IS NOT NULL
	`, batchID)
	return err
}

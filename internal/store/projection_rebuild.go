package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	playbackProjectionEventKind = "core.playback-progress.v1"

	playbackProjectionOperationProgress     = "progress"
	playbackProjectionOperationImport       = "import"
	playbackProjectionOperationMark         = "mark"
	playbackProjectionOperationVersionMerge = "merge-version"
	playbackProjectionOperationHideContinue = "hide-continue"
)

type playbackProjectionEvent struct {
	Operation         string `json:"operation"`
	ProfileID         string `json:"profileId"`
	MediaID           string `json:"mediaId"`
	PositionSeconds   int    `json:"positionSeconds"`
	DurationSeconds   int    `json:"durationSeconds"`
	Watched           bool   `json:"watched"`
	UpdatedAtMS       int64  `json:"updatedAtMs"`
	PlayCount         int    `json:"playCount"`
	ExplicitCount     int    `json:"explicitCount"`
	LastPlayedAtMS    *int64 `json:"lastPlayedAtMs"`
	Provenance        string `json:"provenance"`
	OccurredAtMS      *int64 `json:"occurredAtMs"`
	ImportRunID       string `json:"importRunId"`
	ImportImporterID  string `json:"importImporterId"`
	ImportMatch       string `json:"importMatch"`
	EventTimeMS       *int64 `json:"eventTimeMs"`
	ChannelContext    bool   `json:"channelContext"`
	MergedFromMediaID string `json:"mergedFromMediaId,omitempty"`
	ContinueHidden    bool   `json:"continueHidden,omitempty"`
}

func appendPlaybackProjectionEvent(ctx context.Context, tx *sql.Tx, event playbackProjectionEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode playback projection event: %w", err)
	}
	id, err := randomEngagementEventID()
	if err != nil {
		return fmt.Errorf("create playback projection event ID: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO engagement_event(id, profile_id, media_id, kind, payload, occurred_at_ms, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, id, event.ProfileID, event.MediaID, playbackProjectionEventKind, string(payload),
		eventOccurredAtMS(event), event.UpdatedAtMS)
	if err != nil {
		return fmt.Errorf("append playback projection event: %w", err)
	}
	return nil
}

func eventOccurredAtMS(event playbackProjectionEvent) int64 {
	if event.OccurredAtMS != nil {
		return *event.OccurredAtMS
	}
	if event.LastPlayedAtMS != nil {
		return *event.LastPlayedAtMS
	}
	return event.UpdatedAtMS
}

func applyPlaybackProjectionEvent(ctx context.Context, tx *sql.Tx, event playbackProjectionEvent) error {
	switch event.Operation {
	case playbackProjectionOperationProgress:
		return applyProgressProjectionEvent(ctx, tx, event)
	case playbackProjectionOperationImport:
		return applyImportedPlayProjectionEvent(ctx, tx, event)
	case playbackProjectionOperationMark:
		return applyMarkProjectionEvent(ctx, tx, event)
	case playbackProjectionOperationVersionMerge:
		return applyVersionMergeProjectionEvent(ctx, tx, event)
	case playbackProjectionOperationHideContinue:
		return applyHideContinueProjectionEvent(ctx, tx, event)
	default:
		return fmt.Errorf("unknown playback projection operation %q", event.Operation)
	}
}

// applyVersionMergeProjectionEvent copies a merged Media's progress onto the
// survivor. A new survivor row keeps the merged row's Continue Watching
// removal; an existing one keeps its own.
func applyVersionMergeProjectionEvent(ctx context.Context, tx *sql.Tx, event playbackProjectionEvent) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO playback_progress(profile_id, media_id, position_seconds, duration_seconds, watched, updated_at_ms,
		  play_count, last_played_at_ms, provenance, occurred_at_ms, import_run_id, import_importer_id,
		  import_match_method, event_time_ms, channel_context, continue_hidden)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, media_id) DO UPDATE SET
		  play_count = playback_progress.play_count + excluded.play_count,
		  last_played_at_ms = CASE
		    WHEN playback_progress.last_played_at_ms IS NULL OR excluded.last_played_at_ms > playback_progress.last_played_at_ms
		    THEN excluded.last_played_at_ms ELSE playback_progress.last_played_at_ms END
	`, event.ProfileID, event.MediaID, event.PositionSeconds, event.DurationSeconds,
		boolToInt(event.Watched), event.UpdatedAtMS, event.PlayCount, event.LastPlayedAtMS,
		event.Provenance, event.OccurredAtMS, nullString(event.ImportRunID), nullString(event.ImportImporterID),
		nullString(event.ImportMatch), event.EventTimeMS, boolToInt(event.ChannelContext),
		boolToInt(event.ContinueHidden))
	return err
}

// applyProgressProjectionEvent applies one playback report. A report that
// moves the resume position clears continue_hidden, so playing an item again
// returns it to Continue Watching; a repeat of the same position (a paused
// player's heartbeat) leaves a removal in place.
func applyProgressProjectionEvent(ctx context.Context, tx *sql.Tx, event playbackProjectionEvent) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO playback_progress(profile_id, media_id, position_seconds, duration_seconds, watched, updated_at_ms, play_count, last_played_at_ms, provenance, occurred_at_ms, import_run_id, import_importer_id, import_match_method, event_time_ms, channel_context)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, media_id) DO UPDATE SET
		  position_seconds = CASE WHEN (`+positionAdvancedPredicate+`) THEN excluded.position_seconds ELSE playback_progress.position_seconds END,
		  duration_seconds = excluded.duration_seconds,
		  play_count = CASE WHEN ? > 0 THEN MAX(playback_progress.play_count, excluded.play_count) ELSE play_count + (CASE WHEN excluded.watched = 1 AND playback_progress.watched = 0 AND (`+positionAdvancedPredicate+`) THEN 1 ELSE 0 END) END,
		  last_played_at_ms = CASE WHEN (? > 0 AND excluded.play_count > playback_progress.play_count) THEN excluded.updated_at_ms WHEN excluded.watched = 1 AND playback_progress.watched = 0 AND (`+positionAdvancedPredicate+`) THEN excluded.updated_at_ms ELSE last_played_at_ms END,
		  watched = CASE WHEN (`+positionAdvancedPredicate+`) THEN excluded.watched ELSE playback_progress.watched END,
		  updated_at_ms = CASE WHEN (`+positionAdvancedPredicate+`) THEN excluded.updated_at_ms ELSE playback_progress.updated_at_ms END,
		  event_time_ms = CASE WHEN (`+positionAdvancedPredicate+`) THEN excluded.event_time_ms ELSE playback_progress.event_time_ms END,
		  channel_context = CASE WHEN (`+positionAdvancedPredicate+`) THEN excluded.channel_context ELSE playback_progress.channel_context END,
		  continue_hidden = CASE WHEN (`+positionAdvancedPredicate+`) AND excluded.position_seconds <> playback_progress.position_seconds THEN 0 ELSE playback_progress.continue_hidden END,
		  provenance = excluded.provenance,
		  occurred_at_ms = excluded.occurred_at_ms,
		  import_run_id = excluded.import_run_id,
		  import_importer_id = excluded.import_importer_id,
		  import_match_method = excluded.import_match_method
	`, event.ProfileID, event.MediaID, event.PositionSeconds, event.DurationSeconds,
		boolToInt(event.Watched), event.UpdatedAtMS, event.PlayCount, event.LastPlayedAtMS,
		event.Provenance, event.OccurredAtMS, nullString(event.ImportRunID),
		nullString(event.ImportImporterID), nullString(event.ImportMatch), event.EventTimeMS,
		boolToInt(event.ChannelContext), event.ExplicitCount, event.ExplicitCount)
	return err
}

func applyImportedPlayProjectionEvent(ctx context.Context, tx *sql.Tx, event playbackProjectionEvent) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO playback_progress(profile_id, media_id, position_seconds, duration_seconds, watched, updated_at_ms, play_count, last_played_at_ms, provenance, occurred_at_ms, import_run_id, import_importer_id, import_match_method)
		VALUES (?, ?, 0, 0, 0, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, media_id) DO UPDATE SET
		  play_count = MAX(playback_progress.play_count, excluded.play_count),
		  last_played_at_ms = CASE
		    WHEN excluded.last_played_at_ms IS NOT NULL
		      AND (playback_progress.last_played_at_ms IS NULL OR excluded.last_played_at_ms > playback_progress.last_played_at_ms)
		    THEN excluded.last_played_at_ms
		    ELSE playback_progress.last_played_at_ms END,
		  updated_at_ms = excluded.updated_at_ms,
		  provenance = excluded.provenance,
		  occurred_at_ms = CASE
		    WHEN excluded.occurred_at_ms IS NOT NULL
		      AND (playback_progress.occurred_at_ms IS NULL OR excluded.occurred_at_ms > playback_progress.occurred_at_ms)
		    THEN excluded.occurred_at_ms
		    ELSE playback_progress.occurred_at_ms END,
		  import_run_id = CASE WHEN (`+importPlayAdvancedPredicate+`) THEN excluded.import_run_id ELSE playback_progress.import_run_id END,
		  import_importer_id = CASE WHEN (`+importPlayAdvancedPredicate+`) THEN excluded.import_importer_id ELSE playback_progress.import_importer_id END,
		  import_match_method = CASE WHEN (`+importPlayAdvancedPredicate+`) THEN excluded.import_match_method ELSE playback_progress.import_match_method END
	`, event.ProfileID, event.MediaID, event.UpdatedAtMS, event.PlayCount,
		event.LastPlayedAtMS, event.Provenance, event.OccurredAtMS,
		nullString(event.ImportRunID), nullString(event.ImportImporterID),
		nullString(event.ImportMatch))
	return err
}

// rebuildEngagementProjections replays the frozen canonical log in insertion
// order into playback_progress and verifies the result before committing it.
// It is deliberately internal until operator-facing reconciliation and
// reporting are built.
//
// The replay runs through the live appliers against the real table inside
// one transaction, so it sees the same constraints, defaults and conflict
// rules as a live write. Every existing row must come back identical on every
// column; a missing or different row means the log cannot reproduce it (a
// legacy row, or a live write whose effect depended on state the log does not
// hold), and the rebuild is refused. The refusal rolls the transaction back,
// so no row changes. Pairs the log holds but the table lost are restored.
func (s *Store) rebuildEngagementProjections(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	events, err := loadPlaybackProjectionEvents(ctx, tx)
	if err != nil {
		return err
	}
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS temp.playback_progress_rebuild_base`,
		`CREATE TEMP TABLE playback_progress_rebuild_base AS SELECT * FROM playback_progress`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("snapshot playback_progress projection: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM playback_progress`); err != nil {
		return fmt.Errorf("clear playback_progress projection: %w", err)
	}
	for i, event := range events {
		if err := applyPlaybackProjectionEvent(ctx, tx, event); err != nil {
			return fmt.Errorf("rebuild playback_progress event %d: %w", i, err)
		}
	}
	// The snapshot has the table's own column list and one row per pair, so
	// each row EXCEPT leaves is a pair the replay changed or lost.
	var changed int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT * FROM temp.playback_progress_rebuild_base
			EXCEPT
			SELECT * FROM playback_progress
		)
	`).Scan(&changed); err != nil {
		return fmt.Errorf("verify playback_progress rebuild: %w", err)
	}
	if changed > 0 {
		return fmt.Errorf("playback_progress rebuild refused: replay would change %d existing rows", changed)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE temp.playback_progress_rebuild_base`); err != nil {
		return fmt.Errorf("drop playback_progress snapshot: %w", err)
	}
	return tx.Commit()
}

func loadPlaybackProjectionEvents(ctx context.Context, tx *sql.Tx) ([]playbackProjectionEvent, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT payload
		FROM engagement_event
		WHERE kind = ?
		ORDER BY rowid
	`, playbackProjectionEventKind)
	if err != nil {
		return nil, fmt.Errorf("read playback projection events: %w", err)
	}
	defer rows.Close()
	var events []playbackProjectionEvent
	for rows.Next() {
		var payload sql.NullString
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if !payload.Valid {
			return nil, errors.New("playback projection event has no payload")
		}
		var event playbackProjectionEvent
		if err := json.Unmarshal([]byte(payload.String), &event); err != nil {
			return nil, fmt.Errorf("decode playback projection event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// MaxOfflineEngagementBatch bounds one offline engagement replay call
// (ADR-0157 Amendment B7); clients split larger backlogs.
const MaxOfflineEngagementBatch = 500

// OfflineEngagementEvent is one play session of downloaded Media recorded on a
// Device while offline and replayed when it reconnects (CORE-3182).
// Progress carries the Profile, Media, Device, position and duration;
// PlayedAt is the wall-clock instant the position was observed.
type OfflineEngagementEvent struct {
	EventID   string
	Progress  PlaybackProgress
	Completed bool
	PlayedAt  time.Time
	Policy    ResumePolicy
}

// OfflineEngagementOutcome reports what one replayed event did. Duplicate is
// true when the event was already applied; BecameWatched when this event moved
// the Media from unwatched to watched (a play was counted).
type OfflineEngagementOutcome struct {
	Duplicate     bool
	BecameWatched bool
}

// ApplyOfflineEngagementEvent applies one offline engagement event exactly
// once per (Profile, Device, event ID). The ledger row, the playback projection
// event and its projection apply in one transaction, so a crash never applies
// an event without recording it or the reverse, and a projection rebuild
// reproduces the effect from the logged event.
//
// Merge rules (ADR-0157 B7): the position follows latest-timestamp-wins through
// the ordinary event-time guard; a completed event (or one past the class
// threshold) marks the Media watched whatever its timestamp; no offline event
// un-completes watched Media; a play counts on the unwatched-to-watched
// transition only.
func (s *Store) ApplyOfflineEngagementEvent(ctx context.Context, ev OfflineEngagementEvent) (OfflineEngagementOutcome, error) {
	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return OfflineEngagementOutcome{}, err
	}
	if ev.EventID == "" {
		return OfflineEngagementOutcome{}, ValidationError("event ID is required")
	}
	if err := ValidatePlaybackID(ev.EventID); err != nil {
		return OfflineEngagementOutcome{}, ValidationError("invalid event ID")
	}
	if ev.PlayedAt.IsZero() || ev.PlayedAt.Unix() <= 0 {
		return OfflineEngagementOutcome{}, ValidationError("played-at time is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return OfflineEngagementOutcome{}, err
	}
	// UnixMilli, not UnixNano: a far-future device clock must clamp, not
	// overflow past the bound below.
	now := s.serverNow().UnixMilli()
	playedAt := ev.PlayedAt.UnixMilli()
	if playedAt > now {
		// A fast device clock must not freeze the position ahead of the server.
		playedAt = now
	}
	progress := ev.Progress
	progress.EventTimeMS = playedAt
	progress.PlayCount = 0
	progress.ChannelContext = false
	// The event ID is the play's ID, so play history groups the session.
	progress.PlaybackID = ev.EventID
	event, err := newPlaybackProgressEvent(progress, ev.Completed, ev.Policy, EngagementProvenance{}, playedAt)
	if err != nil {
		return OfflineEngagementOutcome{}, err
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return OfflineEngagementOutcome{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO offline_engagement_event(profile_id, device_id, event_id, media_id, played_at_ms, applied_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, device_id, event_id) DO NOTHING
	`, progress.ProfileID, string(progress.DeviceID), ev.EventID, progress.MediaID, playedAt, now)
	if err != nil {
		return OfflineEngagementOutcome{}, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return OfflineEngagementOutcome{}, err
	}
	if inserted == 0 {
		return OfflineEngagementOutcome{Duplicate: true}, nil
	}
	var priorWatched int
	var priorEventTime sql.NullInt64
	var priorUpdatedAt int64
	err = tx.QueryRowContext(ctx, `
		SELECT watched, event_time_ms, updated_at_ms FROM playback_progress WHERE profile_id = ? AND media_id = ?
	`, progress.ProfileID, progress.MediaID).Scan(&priorWatched, &priorEventTime, &priorUpdatedAt)
	hasPrior := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return OfflineEngagementOutcome{}, err
	}
	if priorWatched != 0 {
		// Completion is sticky: an offline event never un-completes watched
		// Media and a repeat completion changes nothing, so only the ledger row
		// is kept.
		return OfflineEngagementOutcome{}, tx.Commit()
	}
	// The stored state's time: its event time, or when the server received it
	// for a report that carried none.
	priorTime := priorUpdatedAt
	if priorEventTime.Valid {
		priorTime = priorEventTime.Int64
	}
	if hasPrior && !event.Watched && priorTime > playedAt {
		// Latest timestamp wins: a stale offline position changes nothing, not
		// the position and not the duration, so only the ledger row is kept.
		return OfflineEngagementOutcome{}, tx.Commit()
	}
	if event.Watched && priorEventTime.Valid && priorEventTime.Int64 > playedAt {
		// Completion wins whatever its timestamp: lift the event time to the
		// stored one so the event-time guard lets the completion through.
		lifted := priorEventTime.Int64
		event.EventTimeMS = &lifted
	}
	if err := appendPlaybackProjectionEvent(ctx, tx, event); err != nil {
		return OfflineEngagementOutcome{}, err
	}
	if err := applyPlaybackProjectionEvent(ctx, tx, event); err != nil {
		return OfflineEngagementOutcome{}, err
	}
	if err := tx.Commit(); err != nil {
		return OfflineEngagementOutcome{}, err
	}
	return OfflineEngagementOutcome{BecameWatched: event.Watched}, nil
}

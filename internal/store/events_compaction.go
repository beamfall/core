package store

import (
	"context"
	"time"
)

// DefaultEventsRetention is the default age cutoff for time-based events.db
// compaction (PANEL-9 decision 2026-06-20: operator-configurable, default 180
// days).
const DefaultEventsRetention = 180 * 24 * time.Hour

// EventsCompactionResult reports what one retention compaction pruned, for the
// job's status log.
type EventsCompactionResult struct {
	ExpiredStreamTokens int64
	OldAuthAttempts     int64
	OldOutboxEvents     int64
	// Vacuumed is true when the post-prune VACUUM reclaimed file space. A VACUUM
	// needs a brief exclusive lock; if a concurrent reader makes it busy the prune
	// still succeeded (freed pages are reused, so logical growth is bounded) and
	// this is false — never a job failure.
	Vacuumed bool
}

// CompactEvents prunes the genuinely-disposable bookkeeping rows in events.db and
// reclaims space (PANEL-9 / ADR-0011: the events DB must not grow unbounded).
// retention is the age cutoff for time-based pruning; a value <= 0 uses
// DefaultEventsRetention. now is injected for testability.
//
// It prunes:
//   - stream_token_ledger rows whose token has already expired (the ledger is a
//     transparency-only record — ADR-0036 — never consulted to authorize bytes,
//     so an expired row is pure dead weight, pruned regardless of the window);
//   - auth_attempt rows older than retention (the rate-limit backoff consults only
//     a minutes-wide recent window, so older attempts are dead weight).
//   - event_outbox rows older than retention, preserving the newest row as the
//     cursor floor so stale readers get ErrEventCursorTooOld instead of silent
//     loss.
//
// It deliberately does NOT touch:
//   - activity_entry — append-only by ADR-0012 §11, enforced by DB triggers; the
//     audit log is durable by contract and is never retention-pruned here.
//     Capping the audit log would be a contract reversal needing its own decision.
//   - engagement_event — the canonical truth that read projections rebuild from
//     (PANEL-9 decision: engagement events are never time-pruned).
func (s *Store) CompactEvents(ctx context.Context, retention time.Duration, now time.Time) (EventsCompactionResult, error) {
	if retention <= 0 {
		retention = DefaultEventsRetention
	}
	now = normalizeTime(now)
	nowMS := timeToMillis(now)
	cutoffMS := timeToMillis(now.Add(-retention))

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return EventsCompactionResult{}, err
	}

	var result EventsCompactionResult

	expired, err := s.eventsW.ExecContext(ctx,
		"DELETE FROM stream_token_ledger WHERE expires_at_ms < ?", nowMS)
	if err != nil {
		return result, err
	}
	result.ExpiredStreamTokens, _ = expired.RowsAffected()

	oldAuth, err := s.eventsW.ExecContext(ctx,
		"DELETE FROM auth_attempt WHERE occurred_at_ms < ?", cutoffMS)
	if err != nil {
		return result, err
	}
	result.OldAuthAttempts, _ = oldAuth.RowsAffected()

	oldOutbox, err := s.eventsW.ExecContext(ctx,
		"DELETE FROM event_outbox WHERE occurred_at_ms < ? AND seq < (SELECT MAX(seq) FROM event_outbox)", cutoffMS)
	if err != nil {
		return result, err
	}
	result.OldOutboxEvents, _ = oldOutbox.RowsAffected()

	// Best-effort space reclamation: the DELETEs already bound logical growth via
	// page reuse, so a busy VACUUM (a concurrent reader holding a lock) is logged
	// by the caller via Vacuumed, never a job failure.
	if _, err := s.eventsW.ExecContext(ctx, "VACUUM"); err == nil {
		result.Vacuumed = true
	}
	return result, nil
}

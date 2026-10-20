package store

import (
	"context"
	"errors"
	"time"
)

// AuthBackoff is one auth lane's exponential-backoff policy.
// Brute-force economics, not a flat window: while the consecutive-failure count
// stays below FreeAttempts the lane is open (a legitimate fat-finger is not
// punished); from FreeAttempts onward each further consecutive failure roughly
// doubles the enforced lockout. A success (or a clean Window) resets the lane.
// Every PIN/Reveal/pairing/admin lane composes this same shape over the shared
// auth_attempt seam so none is a separate, weaker surface.
type AuthBackoff struct {
	// Window forgets failures older than this — a lane idle for Window resets
	// even without an explicit success.
	Window time.Duration
	// FreeAttempts is the consecutive-failure count at which backoff first
	// engages: while recent failures are fewer than this the lane stays open,
	// and the FreeAttempts-th failure arms BaseDelay. It is the flat
	// attempt-limit the lane previously enforced, now the start of a growing
	// curve rather than a hard wall.
	FreeAttempts int
	// BaseDelay is the lockout armed by the FreeAttempts-th consecutive failure;
	// the delay doubles per additional consecutive failure.
	BaseDelay time.Duration
	// MaxDelay caps the growing delay (a finite ceiling, so a determined
	// attacker is slowed indefinitely without an unbounded lockout).
	MaxDelay time.Duration
}

// AuthBackoffUntil reports the instant before which the lane (action, key) is
// locked. It returns the zero time when the lane is open: the most recent
// attempt succeeded, the consecutive-failure count is still below FreeAttempts,
// or the growing delay has elapsed. From FreeAttempts onward the delay grows
// exponentially with the number of consecutive failures since the last success
// within Window and resets on success. Callers gate with
// now.Before(until).
func (s *Store) AuthBackoffUntil(ctx context.Context, action, key string, now time.Time, policy AuthBackoff) (time.Time, error) {
	if action == "" || key == "" {
		return time.Time{}, errors.New("auth attempt scope is required")
	}
	failures, lastFailure, err := s.consecutiveAuthFailures(ctx, action, key, now.Add(-policy.Window))
	if err != nil {
		return time.Time{}, err
	}
	if failures < policy.FreeAttempts {
		return time.Time{}, nil
	}
	delay := backoffDelay(failures-policy.FreeAttempts+1, policy.BaseDelay, policy.MaxDelay)
	until := lastFailure.Add(delay)
	if !now.Before(until) {
		return time.Time{}, nil
	}
	return until, nil
}

// backoffDelay is the exponential curve: BaseDelay doubled (step-1) times,
// capped at MaxDelay. step is assumed >= 1 (the first locking failure).
func backoffDelay(step int, base, max time.Duration) time.Duration {
	delay := base
	for i := 1; i < step; i++ {
		delay *= 2
		if delay >= max {
			return max
		}
	}
	if delay > max {
		return max
	}
	return delay
}

// consecutiveAuthFailures returns the count of trailing failures for
// (action, key) — failures more recent than the lane's last success — that
// occurred at or after since, plus the time of the most recent such failure. A
// success resets the count: rows at or before the latest success do not count.
func (s *Store) consecutiveAuthFailures(ctx context.Context, action, key string, since time.Time) (int, time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, time.Time{}, err
	}
	rows, err := s.eventsR.QueryContext(ctx,
		`SELECT occurred_at_ms, success FROM auth_attempt
		 WHERE action = ? AND key = ? AND occurred_at_ms >= ?
		 ORDER BY occurred_at_ms DESC, id DESC`,
		action, key, timeToMillis(since),
	)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer rows.Close()
	var (
		count       int
		lastFailure time.Time
	)
	for rows.Next() {
		var occurredMs int64
		var success int
		if err := rows.Scan(&occurredMs, &success); err != nil {
			return 0, time.Time{}, err
		}
		if success != 0 {
			// Most recent attempt at/after a success is a reset point: stop
			// counting trailing failures here.
			break
		}
		if count == 0 {
			// Build directly from millis (not millisToTime, whose zero-guard
			// would collapse an attempt recorded at the Unix epoch to the zero
			// time and defeat the lockout math).
			lastFailure = time.UnixMilli(occurredMs).UTC()
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, time.Time{}, err
	}
	return count, lastFailure, nil
}

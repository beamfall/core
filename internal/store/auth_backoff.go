package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AuthBackoff is one auth lane's exponential-backoff policy.
// Brute-force economics, not a flat window: while the consecutive-failure count
// stays below FreeAttempts the lane is open (a legitimate fat-finger is not
// punished); from FreeAttempts onward each further consecutive failure roughly
// doubles the enforced lockout. A success, or Window of idleness after the
// lockout ended, resets the lane.
// Every PIN/Reveal/pairing/admin lane composes this same shape over the shared
// auth_attempt seam so none is a separate, weaker surface.
type AuthBackoff struct {
	// Window is how long a lane must stay idle after its lockout ended before
	// its failures are forgotten without an explicit success. Measuring from the
	// end of the lockout, not from each failure, keeps a caller who guesses the
	// moment every lockout ends at MaxDelay (CORE-2119).
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
// or idle Window and resets on success. Callers gate with now.Before(until).
func (s *Store) AuthBackoffUntil(ctx context.Context, action, key string, now time.Time, policy AuthBackoff) (time.Time, error) {
	if action == "" || key == "" {
		return time.Time{}, errors.New("auth attempt scope is required")
	}
	failures, lastFailure, err := s.consecutiveAuthFailures(ctx, action, key, now, policy)
	if err != nil {
		return time.Time{}, err
	}
	return backoffHorizon(failures, lastFailure, now, policy), nil
}

// ReserveAuthAttempt checks every lane (action, key) in keys against policy
// and, when none is locked, records a failed attempt on each at now, all in
// one events transaction. It returns the latest lock horizon, recording
// nothing, when any lane is locked. Reserving before the slow check stops
// parallel requests from all passing the throttle before any failure lands;
// a caller whose attempt then succeeds records a success, which resets the
// lanes.
func (s *Store) ReserveAuthAttempt(ctx context.Context, action string, keys []string, now time.Time, policy AuthBackoff) (time.Time, error) {
	if action == "" || len(keys) == 0 {
		return time.Time{}, errors.New("auth attempt scope is required")
	}
	for _, key := range keys {
		if key == "" {
			return time.Time{}, errors.New("auth attempt scope is required")
		}
	}
	if now.IsZero() {
		return time.Time{}, errors.New("auth attempt time is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return time.Time{}, err
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	var locked time.Time
	for _, key := range keys {
		failures, lastFailure, err := consecutiveAuthFailuresIn(ctx, tx, action, key, now, policy)
		if err != nil {
			return time.Time{}, err
		}
		if until := backoffHorizon(failures, lastFailure, now, policy); until.After(locked) {
			locked = until
		}
	}
	if !locked.IsZero() {
		return locked, nil
	}
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO auth_attempt(occurred_at_ms, action, key, success) VALUES (?, ?, ?, 0)",
			timeToMillis(now), action, key,
		); err != nil {
			return time.Time{}, err
		}
	}
	return time.Time{}, tx.Commit()
}

// backoffHorizon is the lock horizon for a lane with failures consecutive
// failures, the latest at lastFailure: the zero time while the lane is open.
func backoffHorizon(failures int, lastFailure, now time.Time, policy AuthBackoff) time.Time {
	if failures < policy.FreeAttempts {
		return time.Time{}
	}
	until := lastFailure.Add(lockoutAfter(failures, policy))
	if !now.Before(until) {
		return time.Time{}
	}
	return until
}

// lockoutAfter is the lockout armed by the failures-th consecutive failure:
// zero while the lane is still inside its free attempts.
func lockoutAfter(failures int, policy AuthBackoff) time.Duration {
	if failures < policy.FreeAttempts {
		return 0
	}
	return backoffDelay(failures-policy.FreeAttempts+1, policy.BaseDelay, policy.MaxDelay)
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

// authFailureScanLimit bounds how many recent attempts one lane read walks.
// It is far above the failures any shipped policy needs to reach MaxDelay, so
// a longer run is already held at the cap.
const authFailureScanLimit = 512

// consecutiveAuthFailures returns the length of the current run of failures
// for (action, key) as of now, plus the time of the most recent one. A success
// ends the run: rows at or before the latest success do not count. So does
// idleness: a failure that came Window or more after the previous failure's
// lockout ended starts a new run, and a run whose last lockout ended Window or
// more before now counts as zero.
func (s *Store) consecutiveAuthFailures(ctx context.Context, action, key string, now time.Time, policy AuthBackoff) (int, time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, time.Time{}, err
	}
	return consecutiveAuthFailuresIn(ctx, s.eventsR, action, key, now, policy)
}

type authAttemptQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func consecutiveAuthFailuresIn(ctx context.Context, q authAttemptQueryer, action, key string, now time.Time, policy AuthBackoff) (int, time.Time, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT occurred_at_ms, success FROM auth_attempt
		 WHERE action = ? AND key = ?
		 ORDER BY occurred_at_ms DESC, id DESC
		 LIMIT ?`,
		action, key, authFailureScanLimit,
	)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer rows.Close()
	// Failures since the last success, newest first.
	var failures []time.Time
	for rows.Next() {
		var occurredMs int64
		var success int
		if err := rows.Scan(&occurredMs, &success); err != nil {
			return 0, time.Time{}, err
		}
		if success != 0 {
			break
		}
		// Build directly from millis (not millisToTime, whose zero-guard
		// would collapse an attempt recorded at the Unix epoch to the zero
		// time and defeat the lockout math).
		failures = append(failures, time.UnixMilli(occurredMs).UTC())
	}
	if err := rows.Err(); err != nil {
		return 0, time.Time{}, err
	}
	if len(failures) == 0 {
		return 0, time.Time{}, nil
	}
	// Replay oldest to newest: the idle gap that ends a run depends on the
	// lockout the run had armed by then.
	count := 0
	var last time.Time
	for i := len(failures) - 1; i >= 0; i-- {
		if count > 0 && laneForgotten(count, last, failures[i], policy) {
			count = 0
		}
		count++
		last = failures[i]
	}
	if laneForgotten(count, last, now, policy) {
		return 0, time.Time{}, nil
	}
	return count, last, nil
}

// laneForgotten reports whether a run of failures consecutive failures, the
// latest at last, has been idle for Window past its lockout by at.
func laneForgotten(failures int, last, at time.Time, policy AuthBackoff) bool {
	return !at.Before(last.Add(lockoutAfter(failures, policy) + policy.Window))
}

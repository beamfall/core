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

// AuthAttemptReservation identifies the rows written for one credential check.
type AuthAttemptReservation struct {
	action string
	atMS   int64
	rows   []reservedAuthAttempt
}

// At reports when the reservation was taken, in server time.
func (r AuthAttemptReservation) At() time.Time { return millisToTime(r.atMS) }

// IntegrationAttemptAction identifies the throttle lanes committed alongside
// integration Sessions in the catalog.
const IntegrationAttemptAction = "integration.login"

func (s *Store) authAttemptDBs(action string) (reader, writer *sql.DB) {
	if action == IntegrationAttemptAction {
		return s.catalogR, s.catalogW
	}
	return s.eventsR, s.eventsW
}

type reservedAuthAttempt struct {
	id  int64
	key string
}

// ReserveAuthAttempt checks every lane (action, key) in keys against policy
// and, when none is locked, records a failed attempt on each at now, all in
// one transaction. The returned reservation identifies the committed
// rows. If any lane is locked, it returns the latest lock horizon and writes
// nothing. Reserving before the slow check stops
// parallel requests from all passing the throttle before any failure lands.
func (s *Store) ReserveAuthAttempt(ctx context.Context, action string, keys []string, now time.Time, policy AuthBackoff) (AuthAttemptReservation, time.Time, error) {
	if action == "" || len(keys) == 0 {
		return AuthAttemptReservation{}, time.Time{}, errors.New("auth attempt scope is required")
	}
	for _, key := range keys {
		if key == "" {
			return AuthAttemptReservation{}, time.Time{}, errors.New("auth attempt scope is required")
		}
	}
	if now.IsZero() {
		return AuthAttemptReservation{}, time.Time{}, errors.New("auth attempt time is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return AuthAttemptReservation{}, time.Time{}, err
	}
	_, writer := s.authAttemptDBs(action)
	tx, err := writer.BeginTx(ctx, nil)
	if err != nil {
		return AuthAttemptReservation{}, time.Time{}, err
	}
	defer tx.Rollback()
	var locked time.Time
	for _, key := range keys {
		failures, lastFailure, err := consecutiveAuthFailuresIn(ctx, tx, action, key, now, policy)
		if err != nil {
			return AuthAttemptReservation{}, time.Time{}, err
		}
		if until := backoffHorizon(failures, lastFailure, now, policy); until.After(locked) {
			locked = until
		}
	}
	if !locked.IsZero() {
		return AuthAttemptReservation{}, locked, nil
	}
	reservation := AuthAttemptReservation{action: action, atMS: timeToMillis(now), rows: make([]reservedAuthAttempt, 0, len(keys))}
	for _, key := range keys {
		var id int64
		if err := tx.QueryRowContext(ctx,
			"INSERT INTO auth_attempt(occurred_at_ms, action, key, success) VALUES (?, ?, ?, 0) RETURNING id",
			reservation.atMS, action, key,
		).Scan(&id); err != nil {
			return AuthAttemptReservation{}, time.Time{}, err
		}
		reservation.rows = append(reservation.rows, reservedAuthAttempt{id: id, key: key})
	}
	if err := tx.Commit(); err != nil {
		return AuthAttemptReservation{}, time.Time{}, err
	}
	return reservation, time.Time{}, nil
}

// importIntegrationAuthAttempts copies the legacy lanes once. The marker and
// copied rows commit together so a restart can retry an interrupted import
// without replaying attempts that were already settled in the catalog.
func (s *Store) importIntegrationAuthAttempts(ctx context.Context) error {
	const marker = "migration.integration_auth_attempts"
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var imported bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM server_config WHERE key = ?)", marker).Scan(&imported); err != nil || imported {
		return err
	}
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT id, occurred_at_ms, key, success FROM auth_attempt WHERE action = ?`, IntegrationAttemptAction)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var atMS int64
		var key string
		var success int
		if err := rows.Scan(&id, &atMS, &key, &success); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO auth_attempt(id, occurred_at_ms, action, key, success) VALUES (?, ?, ?, ?, ?)`,
			id, atMS, IntegrationAttemptAction, key, success); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO server_config(key, value, updated_at_ms) VALUES (?, '', ?)`, marker, timeToMillis(s.now())); err != nil {
		return err
	}
	return tx.Commit()
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
	reader, _ := s.authAttemptDBs(action)
	return consecutiveAuthFailuresIn(ctx, reader, action, key, now, policy)
}

type authAttemptQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func consecutiveAuthFailuresIn(ctx context.Context, q authAttemptQueryer, action, key string, now time.Time, policy AuthBackoff) (int, time.Time, error) {
	// No lockout outlasts MaxDelay, so two failures this far apart are always
	// in different runs, whatever came before the older one.
	breakGap := policy.MaxDelay + policy.Window
	// Read back from now in doubling spans until the start of the current run
	// is inside the span, so an ordinary check reads only recent attempts.
	var failures []time.Time
	complete := false
	for span := breakGap; !complete; span *= 2 {
		last := span <= 0 || span >= authRunHorizon
		if last {
			span = authRunHorizon
		}
		var err error
		failures, complete, err = authFailureRun(ctx, q, action, key, timeToMillis(now)-span.Milliseconds(), breakGap)
		if err != nil {
			return 0, time.Time{}, err
		}
		if last {
			break
		}
	}
	if len(failures) == 0 {
		return 0, time.Time{}, nil
	}
	// Replay oldest to newest: the idle gap that ends a run depends on the
	// lockout the run had armed by then. A run that reaches back past the
	// horizon is carried in at the cap.
	count := 0
	if !complete {
		count = failuresToCap(policy) - 1
	}
	var last time.Time
	for i := len(failures) - 1; i >= 0; i-- {
		if i < len(failures)-1 && laneForgotten(count, last, failures[i], policy) {
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

// authRunHorizon is how far back a lane read looks for the start of the
// current run. A run with no success and no gap of MaxDelay plus Window in
// that time is treated as already at MaxDelay: an unseen start can keep a
// lane locked, never reopen it. It is no longer than the shortest events
// retention, so compaction never cuts a run inside it.
const authRunHorizon = 24 * time.Hour

// failuresToCap is the fewest consecutive failures that arm MaxDelay.
func failuresToCap(policy AuthBackoff) int {
	n := max(policy.FreeAttempts, 1)
	// The delay doubles per failure, so it reaches any cap within 63 steps.
	for limit := n + 63; n < limit && lockoutAfter(n, policy) < policy.MaxDelay; n++ {
	}
	return n
}

// authFailureRun returns the lane's failures at or after sinceMs, newest
// first, back to the latest success or to a gap of breakGap between two
// failures. complete reports that the current run starts inside the rows
// returned: it is false when older rows could still belong to the run.
func authFailureRun(ctx context.Context, q authAttemptQueryer, action, key string, sinceMs int64, breakGap time.Duration) ([]time.Time, bool, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT occurred_at_ms, success FROM auth_attempt
		 WHERE action = ? AND key = ? AND occurred_at_ms >= ?
		 ORDER BY occurred_at_ms DESC, id DESC`,
		action, key, sinceMs,
	)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var failures []time.Time
	for rows.Next() {
		var occurredMs int64
		var success int
		if err := rows.Scan(&occurredMs, &success); err != nil {
			return nil, false, err
		}
		if success != 0 {
			return failures, true, rows.Err()
		}
		// Build directly from millis (not millisToTime, whose zero-guard
		// would collapse an attempt recorded at the Unix epoch to the zero
		// time and defeat the lockout math).
		at := time.UnixMilli(occurredMs).UTC()
		if n := len(failures); n > 0 && breakGap > 0 && failures[n-1].Sub(at) >= breakGap {
			return failures, true, rows.Err()
		}
		failures = append(failures, at)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(failures) == 0 {
		// Nothing this recent: any older run is separated by the whole span.
		return nil, true, nil
	}
	oldest := failures[len(failures)-1]
	return failures, breakGap > 0 && oldest.Sub(time.UnixMilli(sinceMs)) >= breakGap, nil
}

// laneForgotten reports whether a run of failures consecutive failures, the
// latest at last, has been idle for Window past its lockout by at.
func laneForgotten(failures int, last, at time.Time, policy AuthBackoff) bool {
	return !at.Before(last.Add(lockoutAfter(failures, policy) + policy.Window))
}

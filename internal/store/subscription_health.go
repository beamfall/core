package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// FeedErrorClass is the closed class of a Subscription feed fetch failure
// (ADR-0238 §2.2). It names the kind of failure only; the raw error text never
// reaches the Graph, because it can carry hosts and credentials.
type FeedErrorClass string

const (
	FeedErrorNone        FeedErrorClass = ""
	FeedErrorDNS         FeedErrorClass = "dns"
	FeedErrorTLS         FeedErrorClass = "tls"
	FeedErrorHTTP4xx     FeedErrorClass = "http-4xx"
	FeedErrorHTTP5xx     FeedErrorClass = "http-5xx"
	FeedErrorParse       FeedErrorClass = "parse"
	FeedErrorSSRFBlocked FeedErrorClass = "ssrf-blocked"
	FeedErrorAuth        FeedErrorClass = "auth"
	// FeedErrorNetwork covers transport failures that are none of the above:
	// a refused or reset connection, or a timeout.
	FeedErrorNetwork FeedErrorClass = "network"
)

// FeedHealthStatus is the summary the Console renders for a Subscription.
type FeedHealthStatus string

const (
	FeedHealthOK      FeedHealthStatus = "ok"
	FeedHealthFailing FeedHealthStatus = "failing"
	// FeedHealthWarning is a feed that has failed continuously for
	// FeedWarningAfter. It still polls; the Console offers Retry and Remove.
	FeedHealthWarning FeedHealthStatus = "warning"
	// FeedHealthRetired is a feed whose origin answered 410 Gone. It stops
	// polling until someone asks for a refresh.
	FeedHealthRetired FeedHealthStatus = "retired"
)

// FeedWarningAfter is how long a feed fails continuously before its health
// turns from failing to warning. A failing feed is never disabled.
const FeedWarningAfter = 7 * 24 * time.Hour

// MovedFeedObservations is how many consecutive polls must report the same
// new feed URL before the Subscription's URL is rewritten.
const MovedFeedObservations = 2

// ErrSubscriptionRefreshRateLimited means a manual refresh arrived within the
// minimum gap of the previous one.
var ErrSubscriptionRefreshRateLimited = errors.New("subscription refresh rate limited")

// SubscriptionHealth is the poller's record of a Subscription feed's health.
type SubscriptionHealth struct {
	LastErrorClass      FeedErrorClass
	FailingSince        time.Time
	ConsecutiveFailures int
	NextPollAt          time.Time
	RetiredAt           time.Time
	// MovedCandidateURL is a permanent move awaiting a second consistent
	// observation. It stays server-side: like the feed URL it can carry
	// credentials.
	MovedCandidateURL  string
	MovedObservations  int
	MovedAt            time.Time
	RefreshRequestedAt time.Time
}

// Status summarises the health at now.
func (h SubscriptionHealth) Status(now time.Time) FeedHealthStatus {
	switch {
	case !h.RetiredAt.IsZero():
		return FeedHealthRetired
	case h.FailingSince.IsZero():
		return FeedHealthOK
	case now.Sub(h.FailingSince) >= FeedWarningAfter:
		return FeedHealthWarning
	default:
		return FeedHealthFailing
	}
}

// Due reports whether the feed should be polled at now. A retired feed is
// due only after a manual refresh requested since it retired.
func (h SubscriptionHealth) Due(now time.Time) bool {
	if !h.RetiredAt.IsZero() && !h.RefreshRequestedAt.After(h.RetiredAt) {
		return false
	}
	return h.NextPollAt.IsZero() || !h.NextPollAt.After(now)
}

const subscriptionHealthColumns = `last_error_class, failing_since_ms, consecutive_failures, next_poll_at_ms, retired_at_ms, moved_candidate_url, moved_observations, moved_at_ms, refresh_requested_at_ms`

type subscriptionHealthRow struct {
	class                                            string
	failingMS, nextMS, retiredMS, movedMS, refreshMS int64
	failures, observations                           int
	candidate                                        string
}

func (r *subscriptionHealthRow) dest() []any {
	return []any{&r.class, &r.failingMS, &r.failures, &r.nextMS, &r.retiredMS, &r.candidate, &r.observations, &r.movedMS, &r.refreshMS}
}

func (r *subscriptionHealthRow) health() SubscriptionHealth {
	return SubscriptionHealth{
		LastErrorClass:      FeedErrorClass(r.class),
		FailingSince:        optionalUnixMilli(r.failingMS),
		ConsecutiveFailures: r.failures,
		NextPollAt:          optionalUnixMilli(r.nextMS),
		RetiredAt:           optionalUnixMilli(r.retiredMS),
		MovedCandidateURL:   r.candidate,
		MovedObservations:   r.observations,
		MovedAt:             optionalUnixMilli(r.movedMS),
		RefreshRequestedAt:  optionalUnixMilli(r.refreshMS),
	}
}

func optionalUnixMilli(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// RecordSubscriptionPollSuccess clears the feed's failure and retired state
// and schedules its next poll.
func (s *Store) RecordSubscriptionPollSuccess(ctx context.Context, id SubscriptionID, nextPollAt time.Time) error {
	return s.updateSubscriptionHealth(ctx, id, `
		UPDATE subscription SET last_error_class = '', failing_since_ms = 0, consecutive_failures = 0,
			retired_at_ms = 0, next_poll_at_ms = ? WHERE id = ?
	`, nextPollAt.UTC().UnixMilli(), id)
}

// RecordSubscriptionPollFailure records one failed poll: the failure class,
// when the run of failures began (kept from the first failure), one more
// consecutive failure, and the backed-off next poll.
func (s *Store) RecordSubscriptionPollFailure(ctx context.Context, id SubscriptionID, class FeedErrorClass, now, nextPollAt time.Time) error {
	return s.updateSubscriptionHealth(ctx, id, `
		UPDATE subscription SET last_error_class = ?,
			failing_since_ms = CASE WHEN failing_since_ms = 0 THEN ? ELSE failing_since_ms END,
			consecutive_failures = consecutive_failures + 1, next_poll_at_ms = ? WHERE id = ?
	`, string(class), now.UTC().UnixMilli(), nextPollAt.UTC().UnixMilli(), id)
}

// RecordSubscriptionRetired marks the feed retired: its origin answered 410
// Gone. The feed stops polling until a manual refresh.
func (s *Store) RecordSubscriptionRetired(ctx context.Context, id SubscriptionID, now time.Time) error {
	return s.updateSubscriptionHealth(ctx, id, `
		UPDATE subscription SET retired_at_ms = ?, last_error_class = ?,
			failing_since_ms = CASE WHEN failing_since_ms = 0 THEN ? ELSE failing_since_ms END,
			consecutive_failures = consecutive_failures + 1, next_poll_at_ms = 0 WHERE id = ?
	`, now.UTC().UnixMilli(), string(FeedErrorHTTP4xx), now.UTC().UnixMilli(), id)
}

func (s *Store) updateSubscriptionHealth(ctx context.Context, id SubscriptionID, query string, args ...any) error {
	if id == "" {
		return errors.New("subscription ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

// RecordSubscriptionMoveObservation records what one successful poll said
// about where the feed now lives. An empty candidate, or one equal to the
// current feed URL, clears any pending move. The same candidate seen on
// MovedFeedObservations consecutive polls rewrites the feed URL and reports
// true. A candidate already subscribed in the same Library is dropped without
// a rewrite, so the move never merges two Subscriptions.
func (s *Store) RecordSubscriptionMoveObservation(ctx context.Context, id SubscriptionID, candidate string, now time.Time) (bool, error) {
	if id == "" {
		return false, errors.New("subscription ID is required")
	}
	if candidate != "" {
		normalized, err := normalizeSubscriptionFeedURL(candidate)
		if err != nil {
			candidate = ""
		} else {
			candidate = normalized
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var feedURL, pending string
	var observations int
	err = tx.QueryRowContext(ctx, `SELECT feed_url, moved_candidate_url, moved_observations FROM subscription WHERE id = ?`, id).
		Scan(&feedURL, &pending, &observations)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrSubscriptionNotFound
	}
	if err != nil {
		return false, err
	}
	if candidate == "" || candidate == feedURL {
		if pending == "" && observations == 0 {
			return false, nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE subscription SET moved_candidate_url = '', moved_observations = 0 WHERE id = ?`, id); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	if candidate != pending {
		observations = 0
	}
	observations++
	if observations < MovedFeedObservations {
		if _, err := tx.ExecContext(ctx, `UPDATE subscription SET moved_candidate_url = ?, moved_observations = ? WHERE id = ?`, candidate, observations, id); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE subscription SET feed_url = ?, moved_candidate_url = '', moved_observations = 0, moved_at_ms = ? WHERE id = ?
	`, candidate, now.UTC().UnixMilli(), id); err != nil {
		if !strings.Contains(err.Error(), "UNIQUE") {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE subscription SET moved_candidate_url = '', moved_observations = 0 WHERE id = ?`, id); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	return true, tx.Commit()
}

// RequestSubscriptionRefresh makes the Subscription due now, a retired one
// included. A request within minGap of the previous one is refused with
// ErrSubscriptionRefreshRateLimited.
func (s *Store) RequestSubscriptionRefresh(ctx context.Context, id SubscriptionID, now time.Time, minGap time.Duration) (Subscription, error) {
	if id == "" {
		return Subscription{}, errors.New("subscription ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Subscription{}, err
	}
	nowMS := now.UTC().UnixMilli()
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE subscription SET next_poll_at_ms = ?, refresh_requested_at_ms = ?
		WHERE id = ? AND (refresh_requested_at_ms = 0 OR refresh_requested_at_ms <= ?)
	`, nowMS, nowMS, id, nowMS-minGap.Milliseconds())
	if err != nil {
		return Subscription{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Subscription{}, err
	}
	subscription, err := scanSubscription(s.catalogR.QueryRowContext(ctx, subscriptionColumns+` WHERE id = ?`, id))
	if err != nil {
		return Subscription{}, err
	}
	if affected == 0 {
		return subscription, ErrSubscriptionRefreshRateLimited
	}
	return subscription, nil
}

// RequestAllSubscriptionsRefresh makes every Subscription in a Library the
// filter can see, and outside its own minGap, due now. It reports how many it
// refreshed; Subscriptions in Libraries the filter hides are neither touched
// nor counted, so the count never reveals them. Visibility (trash, grant,
// restriction and seal, as libraryVisible decides it) is checked inside the
// UPDATE itself, so a Library hidden concurrently is never refreshed.
func (s *Store) RequestAllSubscriptionsRefresh(ctx context.Context, filter LibraryFilter, now time.Time, minGap time.Duration) (int, error) {
	if len(filter.AllowedIDs) == 0 && !filter.AllLibraries {
		return 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	nowMS := now.UTC().UnixMilli()
	args := []any{nowMS, nowMS, nowMS - minGap.Milliseconds()}
	visible := `SELECT id FROM library WHERE trashed_at_ms IS NULL`
	if len(filter.AllowedIDs) > 0 {
		visible += ` AND id IN (` + sqlPlaceholders(len(filter.AllowedIDs)) + `)`
		for _, id := range filter.AllowedIDs {
			args = append(args, id)
		}
	}
	if !filter.Revealed {
		visible += ` AND is_restricted = 0`
	}
	if len(filter.SealedIDs) > 0 {
		visible += ` AND id NOT IN (` + sqlPlaceholders(len(filter.SealedIDs)) + `)`
		for _, id := range filter.SealedIDs {
			args = append(args, id)
		}
	}
	// #nosec G202 -- only generated placeholders and closed clauses are interpolated; every value is bound.
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE subscription SET next_poll_at_ms = ?, refresh_requested_at_ms = ?
		WHERE (refresh_requested_at_ms = 0 OR refresh_requested_at_ms <= ?)
		AND library_id IN (`+visible+`)
	`, args...)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	return int(affected), err
}

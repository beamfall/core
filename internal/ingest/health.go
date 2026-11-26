package ingest

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// Feed health policy (ADR-0238 §2.2, CORE-2955).
const (
	// MinPollInterval and MaxPollInterval clamp every Subscription's poll
	// interval. The poll job runs at MinPollInterval and polls only the
	// Subscriptions that are due.
	MinPollInterval = 15 * time.Minute
	MaxPollInterval = 24 * time.Hour
	// failureBackoffBase is the delay after the first failure; each further
	// consecutive failure doubles it, up to MaxPollInterval, so a feed that
	// has failed for a month still polls daily.
	failureBackoffBase = 15 * time.Minute
	// backoffJitter spreads retries by ±20% so feeds sharing a host do not
	// retry in lockstep.
	backoffJitter = 0.2
	// DormantFeedAfter is how long without a published entry before a feed
	// drops to daily polling (ADR-0238 §2.2, CORE-2956).
	DormantFeedAfter = 180 * 24 * time.Hour

	observedCadenceWindow   = 10
	maxStoredPollEntryDates = 32
)

// clampPollInterval bounds a poll interval to [MinPollInterval, MaxPollInterval].
func clampPollInterval(interval time.Duration) time.Duration {
	return min(max(interval, MinPollInterval), MaxPollInterval)
}

func (p *Poller) nextPollInterval(ctx context.Context, subscription store.Subscription, result scrape.FeedResult, document scrape.FeedDocument, now time.Time) (time.Duration, error) {
	prevState, hasState, err := p.store.SubscriptionPollState(ctx, subscription.ID)
	if err != nil {
		return 0, err
	}
	entries, err := p.store.SubscriptionEntries(ctx, subscription.ID)
	if err != nil {
		return 0, err
	}
	if !result.NotModified {
		retained, pubDates := mergeFeedEntryDates(document, entries, prevState.Entries)
		state := store.SubscriptionPollState{
			Complete:       document.Complete,
			UpdateInterval: document.UpdateInterval,
			MaxAge:         result.MaxAge,
			Entries:        retained,
		}
		if err := p.store.SaveSubscriptionPollState(ctx, subscription.ID, state, now); err != nil {
			return 0, err
		}
		return adaptivePollInterval(document, result.MaxAge, pubDates, now), nil
	}
	if hasState {
		maxAge := prevState.MaxAge
		if result.HasCacheControl {
			maxAge = result.MaxAge
		}
		if maxAge != prevState.MaxAge {
			prevState.MaxAge = maxAge
			if err := p.store.SaveSubscriptionPollState(ctx, subscription.ID, prevState, now); err != nil {
				return 0, err
			}
		}
		pubDates := mergedStoredAndStatePubDates(entries, prevState.Entries)
		cachedDoc := scrape.FeedDocument{
			Complete:       prevState.Complete,
			UpdateInterval: prevState.UpdateInterval,
		}
		return adaptivePollInterval(cachedDoc, maxAge, pubDates, now), nil
	}
	pubDates := storedEntryPubDates(entries)
	if isFeedDormant(pubDates, now) {
		return MaxPollInterval, nil
	}
	fallbackNoTTL := adaptivePollInterval(scrape.FeedDocument{}, 0, pubDates, now)
	var cachedDoc scrape.FeedDocument
	if prev, hasPrev := previousHealthyPollInterval(subscription); hasPrev && prev != DefaultPollInterval && prev != fallbackNoTTL {
		// Pre-CORE-2956 rows lacked poll_state; a healthy interval distinct from
		// both DefaultPollInterval and observed cadence reflects an explicit hint
		// (<ttl>) and continues to take precedence over HTTP max-age.
		cachedDoc.UpdateInterval = prev
	}
	if cachedDoc.UpdateInterval > 0 || result.HasCacheControl {
		state := store.SubscriptionPollState{
			UpdateInterval: cachedDoc.UpdateInterval,
			MaxAge:         result.MaxAge,
			Entries:        retainPollEntryDates(storedPollEntryDates(entries)),
		}
		if err := p.store.SaveSubscriptionPollState(ctx, subscription.ID, state, now); err != nil {
			return 0, err
		}
	}
	return adaptivePollInterval(cachedDoc, result.MaxAge, pubDates, now), nil
}

// adaptivePollInterval chooses the healthy poll interval in
// [MinPollInterval, MaxPollInterval] (ADR-0238 §2.2, CORE-2956):
//  1. dormant feeds (no published entry in 180 days, or complete="true") poll daily;
//  2. explicit updateFrequency, RSS <ttl>, or sy:updatePeriod/sy:updateFrequency;
//  3. HTTP Cache-Control: max-age as a lower bound;
//  4. observed cadence (half the median gap between the last 10 unique publication dates).
func adaptivePollInterval(document scrape.FeedDocument, maxAge time.Duration, pubDates []time.Time, now time.Time) time.Duration {
	if document.Complete || isFeedDormant(pubDates, now) {
		return MaxPollInterval
	}
	if document.UpdateInterval > 0 {
		return clampPollInterval(document.UpdateInterval)
	}
	cadence, hasCadence := observedCadence(pubDates)
	switch {
	case maxAge > 0 && hasCadence:
		return clampPollInterval(max(maxAge, cadence))
	case maxAge > 0:
		return clampPollInterval(maxAge)
	case hasCadence:
		return clampPollInterval(cadence)
	default:
		return clampPollInterval(DefaultPollInterval)
	}
}

func pollEntryKey(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}

func unguidedPollEntryKey(ts time.Time) string {
	return pollEntryKey("unguided\x00" + ts.UTC().Format(time.RFC3339Nano))
}

func mergeFeedEntryDates(document scrape.FeedDocument, stored []store.SubscriptionEntry, prev []store.SubscriptionPollEntryDate) ([]store.SubscriptionPollEntryDate, []time.Time) {
	seen := make(map[string]bool, len(document.Entries)+len(document.UnguidedPubDates)+len(stored)+len(prev))
	merged := make([]store.SubscriptionPollEntryDate, 0, len(document.Entries)+len(document.UnguidedPubDates)+len(stored))
	for _, entry := range document.Entries {
		if entry.GUID == "" {
			continue
		}
		key := pollEntryKey(entry.GUID)
		if seen[key] {
			continue
		}
		seen[key] = true
		if !entry.PublishedAt.IsZero() {
			merged = append(merged, store.SubscriptionPollEntryDate{Key: key, PublishedAt: entry.PublishedAt.UTC()})
		}
	}
	for _, ts := range document.UnguidedPubDates {
		if ts.IsZero() {
			continue
		}
		utc := ts.UTC()
		key := unguidedPollEntryKey(utc)
		if seen[key] {
			continue
		}
		seen[key] = true
		merged = append(merged, store.SubscriptionPollEntryDate{Key: key, PublishedAt: utc})
	}
	for _, entry := range stored {
		if entry.GUID == "" {
			continue
		}
		key := pollEntryKey(entry.GUID)
		if seen[key] {
			continue
		}
		seen[key] = true
		if !entry.PublishedAt.IsZero() {
			merged = append(merged, store.SubscriptionPollEntryDate{Key: key, PublishedAt: entry.PublishedAt.UTC()})
		}
	}
	for _, entry := range prev {
		key := entry.Key
		if key == "" && !entry.PublishedAt.IsZero() {
			key = unguidedPollEntryKey(entry.PublishedAt)
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if !entry.PublishedAt.IsZero() {
			merged = append(merged, store.SubscriptionPollEntryDate{Key: key, PublishedAt: entry.PublishedAt.UTC()})
		}
	}
	retained := retainPollEntryDates(merged)
	pubDates := make([]time.Time, len(merged))
	for i, entry := range merged {
		pubDates[i] = entry.PublishedAt
	}
	return retained, pubDates
}

func retainPollEntryDates(merged []store.SubscriptionPollEntryDate) []store.SubscriptionPollEntryDate {
	slices.SortFunc(merged, func(a, b store.SubscriptionPollEntryDate) int {
		return b.PublishedAt.Compare(a.PublishedAt)
	})
	if len(merged) <= maxStoredPollEntryDates {
		return merged
	}
	retained := make([]store.SubscriptionPollEntryDate, 0, maxStoredPollEntryDates)
	overflow := make([]store.SubscriptionPollEntryDate, 0, len(merged)-maxStoredPollEntryDates)
	seenTime := make(map[int64]bool, maxStoredPollEntryDates)
	for _, entry := range merged {
		ts := entry.PublishedAt.UnixNano()
		if !seenTime[ts] && len(retained) < maxStoredPollEntryDates {
			seenTime[ts] = true
			retained = append(retained, entry)
			continue
		}
		overflow = append(overflow, entry)
	}
	for _, entry := range overflow {
		if len(retained) >= maxStoredPollEntryDates {
			break
		}
		retained = append(retained, entry)
	}
	slices.SortFunc(retained, func(a, b store.SubscriptionPollEntryDate) int {
		return b.PublishedAt.Compare(a.PublishedAt)
	})
	return retained
}

func mergedStoredAndStatePubDates(stored []store.SubscriptionEntry, stateEntries []store.SubscriptionPollEntryDate) []time.Time {
	seen := make(map[string]bool, len(stateEntries)+len(stored))
	dates := make([]time.Time, 0, len(stateEntries)+len(stored))
	for _, entry := range stateEntries {
		key := entry.Key
		if key == "" && !entry.PublishedAt.IsZero() {
			key = unguidedPollEntryKey(entry.PublishedAt)
		}
		if key != "" {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		if !entry.PublishedAt.IsZero() {
			dates = append(dates, entry.PublishedAt)
		}
	}
	for _, entry := range stored {
		if entry.GUID == "" {
			continue
		}
		key := pollEntryKey(entry.GUID)
		if seen[key] {
			continue
		}
		seen[key] = true
		if !entry.PublishedAt.IsZero() {
			dates = append(dates, entry.PublishedAt)
		}
	}
	return dates
}

func storedPollEntryDates(entries []store.SubscriptionEntry) []store.SubscriptionPollEntryDate {
	out := make([]store.SubscriptionPollEntryDate, 0, len(entries))
	for _, entry := range entries {
		if entry.GUID == "" || entry.PublishedAt.IsZero() {
			continue
		}
		out = append(out, store.SubscriptionPollEntryDate{
			Key:         pollEntryKey(entry.GUID),
			PublishedAt: entry.PublishedAt.UTC(),
		})
	}
	return out
}

func storedEntryPubDates(entries []store.SubscriptionEntry) []time.Time {
	dates := make([]time.Time, len(entries))
	for i, entry := range entries {
		dates[i] = entry.PublishedAt
	}
	return dates
}

func isFeedDormant(pubDates []time.Time, now time.Time) bool {
	var latest time.Time
	for _, ts := range pubDates {
		if ts.IsZero() {
			continue
		}
		if latest.IsZero() || ts.After(latest) {
			latest = ts
		}
	}
	if latest.IsZero() {
		return false
	}
	return now.Sub(latest) >= DormantFeedAfter
}

func observedCadence(pubDates []time.Time) (time.Duration, bool) {
	sorted := make([]time.Time, 0, len(pubDates))
	for _, ts := range pubDates {
		if !ts.IsZero() {
			sorted = append(sorted, ts.UTC())
		}
	}
	if len(sorted) < 2 {
		return 0, false
	}
	slices.SortFunc(sorted, func(a, b time.Time) int {
		return b.Compare(a)
	})
	unique := make([]time.Time, 0, min(len(sorted), observedCadenceWindow))
	for _, ts := range sorted {
		if len(unique) > 0 && ts.Equal(unique[len(unique)-1]) {
			continue
		}
		unique = append(unique, ts)
		if len(unique) >= observedCadenceWindow {
			break
		}
	}
	if len(unique) < 2 {
		return 0, false
	}
	gaps := make([]time.Duration, 0, len(unique)-1)
	for i := 0; i < len(unique)-1; i++ {
		if gap := unique[i].Sub(unique[i+1]); gap > 0 {
			gaps = append(gaps, gap)
		}
	}
	if len(gaps) == 0 {
		return 0, false
	}
	slices.Sort(gaps)
	mid := len(gaps) / 2
	var median time.Duration
	if len(gaps)%2 == 1 {
		median = gaps[mid]
	} else {
		median = gaps[mid-1] + (gaps[mid]-gaps[mid-1])/2
	}
	if median <= 0 {
		return 0, false
	}
	return median / 2, true
}

func previousHealthyPollInterval(subscription store.Subscription) (time.Duration, bool) {
	if subscription.LastPolledAt.IsZero() || !subscription.Health.NextPollAt.After(subscription.LastPolledAt) {
		return 0, false
	}
	if !subscription.Health.FailingSince.IsZero() {
		return 0, false
	}
	if subscription.Health.RefreshRequestedAt.After(subscription.LastPolledAt) {
		return 0, false
	}
	if !subscription.Health.MovedAt.IsZero() && subscription.Health.MovedAt.Equal(subscription.LastPolledAt) {
		return 0, false
	}
	return clampPollInterval(subscription.Health.NextPollAt.Sub(subscription.LastPolledAt)), true
}

// failureBackoff is the delay before the next poll after the failures-th
// consecutive failure. jitter is a uniform sample in [0, 1).
func failureBackoff(failures int, jitter float64) time.Duration {
	delay := MaxPollInterval
	if failures < 1 {
		failures = 1
	}
	if shift := failures - 1; shift < 16 {
		delay = min(failureBackoffBase<<shift, MaxPollInterval)
	}
	scaled := time.Duration(float64(delay) * (1 - backoffJitter + 2*backoffJitter*jitter))
	return min(max(scaled, time.Minute), MaxPollInterval)
}

// feedParseError marks a fetched feed body that did not parse.
type feedParseError struct{ err error }

func (e *feedParseError) Error() string { return e.err.Error() }
func (e *feedParseError) Unwrap() error { return e.err }

// ClassifyFeedError maps a feed fetch or parse failure to its closed error class.
func ClassifyFeedError(err error) store.FeedErrorClass {
	if err == nil {
		return store.FeedErrorNone
	}
	var parseErr *feedParseError
	if errors.As(err, &parseErr) {
		return store.FeedErrorParse
	}
	if status, ok := scrape.HTTPStatus(err); ok {
		switch {
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			return store.FeedErrorAuth
		case status >= 500:
			return store.FeedErrorHTTP5xx
		default:
			return store.FeedErrorHTTP4xx
		}
	}
	if scrape.IsRefused(err) {
		return store.FeedErrorSSRFBlocked
	}
	if errors.Is(err, scrape.ErrResponseTooLarge) {
		return store.FeedErrorParse
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return store.FeedErrorDNS
	}
	if isTLSError(err) {
		return store.FeedErrorTLS
	}
	return store.FeedErrorNetwork
}

func isTLSError(err error) bool {
	var (
		unknownAuthority x509.UnknownAuthorityError
		hostname         x509.HostnameError
		invalid          x509.CertificateInvalidError
		systemRoots      x509.SystemRootsError
		verification     *tls.CertificateVerificationError
		record           tls.RecordHeaderError
		alert            tls.AlertError
	)
	switch {
	case errors.As(err, &unknownAuthority), errors.As(err, &hostname), errors.As(err, &invalid),
		errors.As(err, &systemRoots), errors.As(err, &verification), errors.As(err, &record), errors.As(err, &alert):
		return true
	}
	return strings.Contains(err.Error(), "tls: ") || strings.Contains(err.Error(), "x509: ")
}

// recordFeedFailure records one failed poll with its backed-off next poll. A
// 410 Gone at a permanent destination retires the feed. The poll error is returned, joined with
// any error recording it.
func (p *Poller) recordFeedFailure(ctx context.Context, subscription store.Subscription, class store.FeedErrorClass, cause error, now time.Time) error {
	var err error
	if status, ok := scrape.HTTPStatus(cause); ok && status == http.StatusGone && !scrape.IsTransient(cause) {
		err = p.store.RecordSubscriptionRetired(ctx, subscription, now)
	} else {
		next := now.Add(failureBackoff(subscription.Health.ConsecutiveFailures+1, p.jitter()))
		err = p.store.RecordSubscriptionPollFailure(ctx, subscription, class, now, next)
	}
	return errors.Join(cause, err)
}

// observeMove accepts a permanent redirect or the parsed feed's own move
// declaration. A temporary destination cannot relocate the feed, and a 304
// without redirect leaves the previous observation untouched.
func (p *Poller) observeMove(ctx context.Context, subscription store.Subscription, result scrape.FeedResult, document scrape.FeedDocument, now time.Time) (hasCandidate, moved bool, err error) {
	target := result.PermanentURL
	if target == "" {
		if result.NotModified {
			return false, false, nil
		}
		if !result.RedirectedTemporarily {
			target = document.NewFeedURL
		}
	}
	original, _ := url.Parse(subscription.FeedURL)
	if p.engine.CheckURL(target) != nil || target == subscription.FeedURL ||
		((subscription.AuthRef != "" || (original != nil && original.User != nil)) && !scrape.SameOrigin(target, subscription.FeedURL)) {
		target = ""
	}
	moved, err = p.store.RecordSubscriptionMoveObservation(ctx, subscription.ID, target, now)
	return target != "", moved, err
}

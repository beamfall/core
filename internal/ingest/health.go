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

func pollStateMatchesSubscription(state store.SubscriptionPollState, hasState bool, subscription store.Subscription) bool {
	return hasState &&
		state.FeedURLHash != "" &&
		state.FeedURLHash == store.PollFeedURLHash(subscription.FeedURL) &&
		state.ETag == subscription.ETag &&
		state.LastModified == subscription.LastModified
}

func (p *Poller) loadEntryPollDates(ctx context.Context, id store.SubscriptionID) ([]store.SubscriptionEntry, error) {
	if p.testLoadEntryPollDates != nil {
		return p.testLoadEntryPollDates(ctx, id)
	}
	return p.store.SubscriptionEntryPollDates(ctx, id)
}

func (p *Poller) planPollSchedule(ctx context.Context, subscription store.Subscription, prevState store.SubscriptionPollState, stateBound bool, preEntries []store.SubscriptionEntry, result scrape.FeedResult, document scrape.FeedDocument, now time.Time) (time.Duration, *store.SubscriptionPollState, error) {
	urlChanged := prevState.FeedURLHash != "" && prevState.FeedURLHash != store.PollFeedURLHash(subscription.FeedURL)
	historyResetAt := subscription.Health.MovedAt
	if urlChanged {
		if now.After(historyResetAt) {
			historyResetAt = now
		}
	} else if prevState.URLChangedAt.After(historyResetAt) {
		historyResetAt = prevState.URLChangedAt
	}
	effectiveAddedAt := subscription.AddedAt
	if historyResetAt.After(effectiveAddedAt) {
		effectiveAddedAt = historyResetAt
	}

	if result.NotModified && stateBound {
		nextState := prevState
		if historyResetAt.After(nextState.URLChangedAt) {
			nextState.URLChangedAt = historyResetAt
		}
		if result.HasCacheControl {
			nextState.MaxAge = result.MaxAge
		}
		pubDates := pollStatePubDates(nextState.Entries)
		return adaptivePollInterval(scrape.FeedDocument{
			Complete:       nextState.Complete,
			UpdateInterval: nextState.UpdateInterval,
		}, nextState.MaxAge, pubDates, nextState.LastNewEntryAt, effectiveAddedAt, now), &nextState, nil
	}

	if !result.NotModified {
		eligibleEntries := filterPollEntriesSince(preEntries, historyResetAt, urlChanged)
		var priorEntries []store.SubscriptionPollEntryDate
		var priorUndated []string
		var prevUndatedDigest string
		var lastNewEntryAt time.Time
		if !urlChanged {
			priorEntries = prevState.Entries
			priorUndated = prevState.SeenUndatedGUIDs
			prevUndatedDigest = prevState.UndatedDigest
			lastNewEntryAt = prevState.LastNewEntryAt
		}
		priorDBEntries := eligibleEntries
		if prevState.FeedURLHash != "" && !urlChanged {
			priorDBEntries = make([]store.SubscriptionEntry, 0, len(eligibleEntries))
			for _, entry := range eligibleEntries {
				if !entry.PublishedAt.IsZero() {
					priorDBEntries = append(priorDBEntries, entry)
				}
			}
		}
		if documentHasNewUndatedEntry(document, priorDBEntries, priorEntries, priorUndated, prevUndatedDigest) {
			lastNewEntryAt = now
		}
		if persistedFirstSeen := latestUndatedFirstSeen(eligibleEntries); persistedFirstSeen.After(lastNewEntryAt) {
			lastNewEntryAt = persistedFirstSeen
		}
		retained, pubDates := mergeFeedEntryDates(document, eligibleEntries, priorEntries)
		undatedKeys, _ := documentUndatedKeys(document)
		state := &store.SubscriptionPollState{
			URLChangedAt:     historyResetAt,
			LastNewEntryAt:   lastNewEntryAt,
			SeenUndatedGUIDs: mergeSeenUndatedGUIDs(document, eligibleEntries, priorUndated),
			UndatedDigest:    undatedSetDigest(undatedKeys),
			Complete:         document.Complete,
			UpdateInterval:   document.UpdateInterval,
			MaxAge:           result.MaxAge,
			Entries:          retained,
		}
		return adaptivePollInterval(document, result.MaxAge, pubDates, lastNewEntryAt, effectiveAddedAt, now), state, nil
	}

	allEntries, err := p.loadEntryPollDates(ctx, subscription.ID)
	if err != nil {
		return 0, nil, err
	}
	entries := filterPollEntriesSince(allEntries, historyResetAt, urlChanged)
	pubDates := storedEntryPubDates(entries)
	var lastNewEntryAt time.Time
	var priorUndated []string
	var prevUndatedDigest string
	if !urlChanged {
		lastNewEntryAt = prevState.LastNewEntryAt
		priorUndated = prevState.SeenUndatedGUIDs
		prevUndatedDigest = prevState.UndatedDigest
	}
	if persistedFirstSeen := latestUndatedFirstSeen(entries); persistedFirstSeen.After(lastNewEntryAt) {
		lastNewEntryAt = persistedFirstSeen
	}
	if isFeedDormant(pubDates, lastNewEntryAt, effectiveAddedAt, now) {
		state := &store.SubscriptionPollState{
			URLChangedAt:     historyResetAt,
			LastNewEntryAt:   lastNewEntryAt,
			SeenUndatedGUIDs: mergeSeenUndatedGUIDs(scrape.FeedDocument{}, entries, priorUndated),
			UndatedDigest:    prevUndatedDigest,
			Entries:          retainPollEntryDates(storedPollEntryDates(entries)),
		}
		return MaxPollInterval, state, nil
	}
	fallbackNoTTL := adaptivePollInterval(scrape.FeedDocument{}, 0, pubDates, lastNewEntryAt, effectiveAddedAt, now)
	var cachedDoc scrape.FeedDocument
	if prev, hasPrev := previousHealthyPollInterval(subscription); hasPrev && prev != DefaultPollInterval && prev != fallbackNoTTL {
		// Pre-CORE-2956 rows lacked poll_state; a healthy interval distinct from
		// both DefaultPollInterval and observed cadence reflects an explicit hint
		// (<ttl>) and continues to take precedence over HTTP max-age.
		cachedDoc.UpdateInterval = prev
	}
	var nextState *store.SubscriptionPollState
	if cachedDoc.UpdateInterval > 0 || result.HasCacheControl || !historyResetAt.IsZero() || !lastNewEntryAt.IsZero() {
		nextState = &store.SubscriptionPollState{
			URLChangedAt:     historyResetAt,
			LastNewEntryAt:   lastNewEntryAt,
			SeenUndatedGUIDs: mergeSeenUndatedGUIDs(scrape.FeedDocument{}, entries, priorUndated),
			UndatedDigest:    prevUndatedDigest,
			UpdateInterval:   cachedDoc.UpdateInterval,
			MaxAge:           result.MaxAge,
			Entries:          retainPollEntryDates(storedPollEntryDates(entries)),
		}
	}
	return adaptivePollInterval(cachedDoc, result.MaxAge, pubDates, lastNewEntryAt, effectiveAddedAt, now), nextState, nil
}

// adaptivePollInterval chooses the healthy poll interval in
// [MinPollInterval, MaxPollInterval] (ADR-0238 §2.2, CORE-2956):
//  1. dormant feeds (no published entry in 180 days, or complete="true") poll daily;
//  2. explicit updateFrequency, RSS <ttl>, or sy:updatePeriod/sy:updateFrequency;
//  3. HTTP Cache-Control: max-age as a lower bound;
//  4. observed cadence (half the median gap between the last 10 unique publication dates).
func adaptivePollInterval(document scrape.FeedDocument, maxAge time.Duration, pubDates []time.Time, lastNewEntryAt, addedAt, now time.Time) time.Duration {
	if document.Complete || isFeedDormant(pubDates, lastNewEntryAt, addedAt, now) {
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
		if entry.GUID == "" || entry.PublishedAt.IsZero() {
			continue
		}
		key := pollEntryKey(entry.GUID)
		if seen[key] {
			continue
		}
		seen[key] = true
		merged = append(merged, store.SubscriptionPollEntryDate{Key: key, PublishedAt: entry.PublishedAt.UTC()})
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
		if entry.GUID == "" || entry.PublishedAt.IsZero() {
			continue
		}
		key := pollEntryKey(entry.GUID)
		if seen[key] {
			continue
		}
		seen[key] = true
		merged = append(merged, store.SubscriptionPollEntryDate{Key: key, PublishedAt: entry.PublishedAt.UTC()})
	}
	for _, entry := range prev {
		if entry.PublishedAt.IsZero() {
			continue
		}
		key := entry.Key
		if key == "" {
			key = unguidedPollEntryKey(entry.PublishedAt)
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		merged = append(merged, store.SubscriptionPollEntryDate{Key: key, PublishedAt: entry.PublishedAt.UTC()})
	}
	retained := retainPollEntryDates(merged)
	pubDates := make([]time.Time, len(merged))
	for i, entry := range merged {
		pubDates[i] = entry.PublishedAt
	}
	return retained, pubDates
}

func pollStatePubDates(entries []store.SubscriptionPollEntryDate) []time.Time {
	dates := make([]time.Time, 0, len(entries))
	for _, entry := range entries {
		if !entry.PublishedAt.IsZero() {
			dates = append(dates, entry.PublishedAt.UTC())
		}
	}
	return dates
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

func filterPollEntriesSince(entries []store.SubscriptionEntry, resetAt time.Time, urlChanged bool) []store.SubscriptionEntry {
	if urlChanged {
		return nil
	}
	if resetAt.IsZero() {
		return entries
	}
	filtered := make([]store.SubscriptionEntry, 0, len(entries))
	for _, entry := range entries {
		// Strictly after resetAt excludes entries catalogued on an older URL even
		// when consecutive polls share the same timestamp in tests; current-URL
		// entries observed at resetAt live in PollState.Entries.
		if !entry.FirstSeenAt.IsZero() && entry.FirstSeenAt.After(resetAt) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

const maxSeenUndatedGUIDs = 4096

func undatedEntryKey(entry scrape.FeedEntry) string {
	id := strings.TrimSpace(entry.GUID)
	if id == "" {
		id = strings.TrimSpace(entry.EnclosureURL)
	}
	if id == "" {
		id = strings.TrimSpace(entry.Title)
	}
	if id == "" {
		return ""
	}
	return pollEntryKey(id)
}

func documentUndatedKeys(document scrape.FeedDocument) (keys []string, hasBlank bool) {
	for _, entry := range document.Entries {
		if !entry.PublishedAt.IsZero() {
			continue
		}
		key := undatedEntryKey(entry)
		if key == "" {
			hasBlank = true
			continue
		}
		keys = append(keys, key)
	}
	for _, textKey := range document.UndatedTextKeys {
		if textKey = strings.TrimSpace(textKey); textKey != "" {
			keys = append(keys, pollEntryKey(textKey))
		}
	}
	return keys, hasBlank
}

func undatedSetDigest(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	unique := slices.Clone(keys)
	slices.Sort(unique)
	unique = slices.Compact(unique)
	sum := sha256.Sum256([]byte(strings.Join(unique, "\n")))
	return hex.EncodeToString(sum[:])
}

func documentHasNewUndatedEntry(document scrape.FeedDocument, priorDBEntries []store.SubscriptionEntry, priorDated []store.SubscriptionPollEntryDate, priorUndated []string, prevDigest string) bool {
	keys, hasBlank := documentUndatedKeys(document)
	if hasBlank {
		return true
	}
	if len(keys) == 0 {
		return false
	}
	if prevDigest != "" && undatedSetDigest(keys) == prevDigest {
		return false
	}
	knownKeys := make(map[string]bool, len(priorDBEntries)+len(priorDated)+len(priorUndated))
	for _, k := range priorUndated {
		if k != "" {
			knownKeys[k] = true
		}
	}
	for _, entry := range priorDated {
		if entry.Key != "" {
			knownKeys[entry.Key] = true
		}
	}
	for _, entry := range priorDBEntries {
		if entry.GUID != "" {
			knownKeys[pollEntryKey(entry.GUID)] = true
		}
	}
	for _, key := range keys {
		if !knownKeys[key] {
			return true
		}
	}
	return false
}

func mergeSeenUndatedGUIDs(document scrape.FeedDocument, priorDBEntries []store.SubscriptionEntry, priorUndated []string) []string {
	docKeys, _ := documentUndatedKeys(document)
	limit := max(maxSeenUndatedGUIDs, len(docKeys))
	seen := make(map[string]bool, len(docKeys)+len(priorUndated))
	merged := make([]string, 0, min(limit, len(docKeys)+len(priorUndated)))
	add := func(key string) {
		if key == "" || seen[key] || len(merged) >= limit {
			return
		}
		seen[key] = true
		merged = append(merged, key)
	}
	for _, key := range docKeys {
		add(key)
	}
	for _, k := range priorUndated {
		add(k)
	}
	for _, entry := range priorDBEntries {
		if entry.PublishedAt.IsZero() && entry.GUID != "" {
			add(pollEntryKey(entry.GUID))
		}
	}
	return merged
}

func latestUndatedFirstSeen(entries []store.SubscriptionEntry) time.Time {
	var latest time.Time
	for _, entry := range entries {
		if !entry.PublishedAt.IsZero() || entry.FirstSeenAt.IsZero() {
			continue
		}
		if latest.IsZero() || entry.FirstSeenAt.After(latest) {
			latest = entry.FirstSeenAt
		}
	}
	return latest
}

func isFeedDormant(pubDates []time.Time, lastNewEntryAt, addedAt, now time.Time) bool {
	var latest time.Time
	for _, ts := range pubDates {
		if ts.IsZero() {
			continue
		}
		if latest.IsZero() || ts.After(latest) {
			latest = ts
		}
	}
	if lastNewEntryAt.After(latest) {
		latest = lastNewEntryAt
	}
	if !latest.IsZero() {
		return now.Sub(latest) >= DormantFeedAfter
	}
	return !addedAt.IsZero() && now.Sub(addedAt) >= DormantFeedAfter
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

package ingest

// Feed health (CORE-2955), download policy (CORE-2953) and feed moves.
// Nothing here logs or returns a feed or enclosure URL: classifications are
// fixed strings and scrape errors are already URL-sanitized.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/rand/v2"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// maxPollBackoff caps the failure backoff (ADR-0238).
const maxPollBackoff = 24 * time.Hour

// feedMoveObservations is how many consecutive polls must report the same
// new feed URL before the Subscription follows it.
const feedMoveObservations = 2

// feedParseError marks a fetched feed body that did not parse.
type feedParseError struct{ err error }

func (e *feedParseError) Error() string { return e.err.Error() }
func (e *feedParseError) Unwrap() error { return e.err }

// ClassifyFeedError maps a feed fetch or parse failure to its persisted
// health class. It never inspects or returns the URL.
func ClassifyFeedError(err error) store.FeedErrorClass {
	if err == nil {
		return ""
	}
	var parseErr *feedParseError
	if errors.As(err, &parseErr) {
		return store.FeedErrorParse
	}
	if scrape.IsRefused(err) {
		return store.FeedErrorSSRFBlocked
	}
	if code, ok := scrape.HTTPStatus(err); ok {
		switch {
		case code == http.StatusUnauthorized || code == http.StatusForbidden:
			return store.FeedErrorAuth
		case code == http.StatusGone:
			return store.FeedErrorGone
		case code >= 500:
			return store.FeedErrorHTTP5xx
		case code >= 400:
			return store.FeedErrorHTTP4xx
		}
		return store.FeedErrorNetwork
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
	var verification *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	var systemRoots x509.SystemRootsError
	var recordHeader tls.RecordHeaderError
	var alert tls.AlertError
	return errors.As(err, &verification) || errors.As(err, &unknownAuthority) || errors.As(err, &invalid) ||
		errors.As(err, &hostname) || errors.As(err, &systemRoots) || errors.As(err, &recordHeader) || errors.As(err, &alert)
}

// failureBackoff is the delay before the next poll after `failures`
// consecutive failures: the base interval doubling per failure, ±20%
// jitter (random in [0,1)), capped at maxPollBackoff.
func failureBackoff(interval time.Duration, failures int, random float64) time.Duration {
	delay := interval
	for i := 1; i < failures && delay < maxPollBackoff; i++ {
		delay *= 2
	}
	delay = min(delay, maxPollBackoff)
	jittered := time.Duration(float64(delay) * (0.8 + 0.4*random))
	return min(jittered, maxPollBackoff)
}

// recordFailure persists a failed poll with its backoff and returns cause.
func (p *Poller) recordFailure(ctx context.Context, subscription store.Subscription, class store.FeedErrorClass, now time.Time, cause error) error {
	_, health, err := p.store.SubscriptionPolicyAndHealth(ctx, subscription.ID)
	if err != nil {
		return errors.Join(cause, err)
	}
	next := now.Add(failureBackoff(p.interval(), health.ConsecutiveFailures+1, p.random()))
	if err := p.store.RecordSubscriptionPollFailure(ctx, subscription.ID, class, now, next); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// finishFeedPoll records a reachable feed: one feed-move observation when
// observed (an empty candidate resets a pending move), then success. The next
// poll is due now after a move or a partial poll, else after one interval.
func (p *Poller) finishFeedPoll(ctx context.Context, subscription store.Subscription, candidate string, observed, retrySoon bool, now time.Time) error {
	moved := false
	if observed {
		var err error
		if moved, err = p.observeFeedMove(ctx, subscription, candidate); err != nil {
			return err
		}
	}
	next := now.Add(p.interval())
	if moved || retrySoon {
		next = now
	}
	return p.store.RecordSubscriptionPollSuccess(ctx, subscription.ID, now, next)
}

// observeFeedMove records one observation of a new feed URL. A candidate
// that fails the SSRF guard, or (for a private feed) leaves the credential's
// origin, counts as no observation. The store rewrites feed_url after
// feedMoveObservations consistent observations and keeps the old URL when
// the new one belongs to another Subscription in the Library.
func (p *Poller) observeFeedMove(ctx context.Context, subscription store.Subscription, candidate string) (bool, error) {
	if candidate != "" && !p.acceptableFeedMove(subscription, candidate) {
		candidate = ""
	}
	return p.store.ObserveSubscriptionFeedMove(ctx, subscription.ID, candidate)
}

func (p *Poller) acceptableFeedMove(subscription store.Subscription, candidate string) bool {
	if p.engine.CheckURL(candidate) != nil {
		return false
	}
	return subscription.AuthRef == "" || scrape.SameOrigin(subscription.FeedURL, candidate)
}

// catalogueEntries is the feed's enclosure-bearing entries, first GUID
// occurrence wins, in document order.
func catalogueEntries(document scrape.FeedDocument) []scrape.FeedEntry {
	seen := make(map[string]bool, len(document.Entries))
	entries := make([]scrape.FeedEntry, 0, len(document.Entries))
	for _, entry := range document.Entries {
		if entry.EnclosureURL == "" || seen[entry.GUID] {
			continue
		}
		seen[entry.GUID] = true
		entries = append(entries, entry)
	}
	return entries
}

// catalogue upserts every enclosure-bearing entry and returns the GUIDs the
// download policy selects. The boolean reports whether the Library holds a
// file-less catalogue.
func (p *Poller) catalogue(ctx context.Context, subscription store.Subscription, document scrape.FeedDocument, now time.Time) (map[string]bool, bool, error) {
	entries := catalogueEntries(document)
	rows := make([]store.FeedCatalogEntry, len(entries))
	for i, entry := range entries {
		rows[i] = store.FeedCatalogEntry{
			GUID:            entry.GUID,
			Title:           entry.Title,
			Description:     entry.Description,
			PublishedAt:     entry.PublishedAt,
			DurationSeconds: entry.DurationSeconds,
			EnclosureURL:    entry.EnclosureURL,
			EnclosureType:   entry.EnclosureType,
			EnclosureLength: entry.EnclosureLength,
			Position:        i,
			FeedTitle:       document.Title,
		}
	}
	_, err := p.store.UpsertSubscriptionCatalog(ctx, subscription.ID, rows, now)
	encrypted := errors.Is(err, store.ErrSubscriptionCatalogUnsupported)
	if err != nil && !encrypted {
		return nil, false, err
	}
	policy, _, err := p.store.SubscriptionPolicyAndHealth(ctx, subscription.ID)
	if err != nil {
		return nil, false, err
	}
	firstPoll := subscription.LastPolledAt.IsZero()
	if encrypted {
		selected, err := p.uncataloguedDownloads(ctx, subscription.ID, policy, firstPoll, document.Serial, entries, now)
		return selected, false, err
	}
	stored, err := p.store.SubscriptionEntries(ctx, subscription.ID)
	if err != nil {
		return nil, false, err
	}
	byGUID := make(map[string]store.SubscriptionEntry, len(stored))
	for _, entry := range stored {
		byGUID[entry.GUID] = entry
	}
	selected := map[string]bool{}
	var queued []string
	initialLatest := firstPoll && policy.Policy == store.DownloadPolicyLatest
	for _, guid := range selectByPolicy(policy, firstPoll, document.Serial, subscription.AddedAt, entries, byGUID) {
		if entry, ok := byGUID[guid]; ok && (entry.Downloaded || entry.DownloadState == store.EntryDownloadDownloaded) {
			continue
		}
		selected[guid] = true
		if initialLatest {
			queued = append(queued, guid)
		}
	}
	if initialLatest {
		if err := p.store.RecordInitialSubscriptionDownloads(ctx, subscription.ID, queued, nil, now); err != nil {
			return nil, false, err
		}
	}
	return selected, true, nil
}

func (p *Poller) uncataloguedDownloads(ctx context.Context, id store.SubscriptionID, policy store.SubscriptionPolicy, firstPoll, serial bool, entries []scrape.FeedEntry, now time.Time) (map[string]bool, error) {
	selected := map[string]bool{}
	if firstPoll {
		for _, guid := range selectByPolicy(policy, true, serial, time.Time{}, entries, nil) {
			selected[guid] = true
		}
		var skipped []string
		for _, entry := range entries {
			if !selected[entry.GUID] {
				skipped = append(skipped, entry.GUID)
			}
		}
		return selected, p.store.RecordInitialSubscriptionDownloads(ctx, id, nil, skipped, now)
	}
	if policy.Policy == store.DownloadPolicyStreamOnly {
		return selected, nil
	}
	var skipped map[string]bool
	if policy.Policy != store.DownloadPolicyEverything {
		var err error
		skipped, err = p.store.SubscriptionBackCatalogue(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	for _, entry := range entries {
		if !skipped[entry.GUID] {
			selected[entry.GUID] = true
		}
	}
	return selected, nil
}

// selectByPolicy returns the GUIDs a policy wants on disk, before filtering
// out existing downloads. Latest bootstraps with the first N of a serial
// feed or newest N by date, then downloads new arrivals like new-only.
func selectByPolicy(policy store.SubscriptionPolicy, firstPoll, serial bool, addedAt time.Time, entries []scrape.FeedEntry, stored map[string]store.SubscriptionEntry) []string {
	var guids []string
	switch policy.Policy {
	case store.DownloadPolicyStreamOnly:
		return nil
	case store.DownloadPolicyEverything:
		for _, entry := range entries {
			guids = append(guids, entry.GUID)
		}
	case store.DownloadPolicyNewOnly, store.DownloadPolicyLatest:
		if policy.Policy == store.DownloadPolicyNewOnly || !firstPoll {
			for _, entry := range entries {
				row, ok := stored[entry.GUID]
				retryLatest := policy.Policy == store.DownloadPolicyLatest &&
					(row.DownloadState == store.EntryDownloadQueued || row.DownloadState == store.EntryDownloadFailed)
				if ok && (row.FirstSeenAt.After(addedAt) || retryLatest) {
					guids = append(guids, entry.GUID)
				}
			}
			return guids
		}
		n := policy.LatestN
		if n <= 0 {
			n = store.DefaultDownloadLatestN
		}
		ranked := make([]scrape.FeedEntry, 0, len(entries))
		for _, entry := range entries {
			if stored[entry.GUID].FirstSeenAt.After(addedAt) {
				// The first poll may have saved a catalogue before it was
				// interrupted. New arrivals do not compete with that snapshot.
				guids = append(guids, entry.GUID)
			} else {
				ranked = append(ranked, entry)
			}
		}
		if !serial {
			slices.SortStableFunc(ranked, func(a, b scrape.FeedEntry) int {
				switch {
				case a.PublishedAt.IsZero() != b.PublishedAt.IsZero():
					if a.PublishedAt.IsZero() {
						return 1
					}
					return -1
				default:
					return b.PublishedAt.Compare(a.PublishedAt)
				}
			})
		}
		for _, entry := range ranked[:min(n, len(ranked))] {
			guids = append(guids, entry.GUID)
		}
	}
	return guids
}

// ProbeFeed fetches and parses a feed without subscribing: its title, how
// many entries carry an enclosure, and the sum of their declared enclosure
// lengths (what `everything` would download). Errors classify with
// ClassifyFeedError.
func (p *Poller) ProbeFeed(ctx context.Context, feedURL string) (title string, entryCount int, catalogBytes int64, err error) {
	result, err := p.engine.FetchConditional(ctx, feedURL, "", "")
	if err != nil {
		return "", 0, 0, err
	}
	document, err := scrape.ParseFeed(result.Data)
	if err != nil {
		return "", 0, 0, &feedParseError{err: err}
	}
	entries := catalogueEntries(document)
	for _, entry := range entries {
		catalogBytes += entry.EnclosureLength
	}
	return document.Title, len(entries), catalogBytes, nil
}

// PollSubscriptionNow starts one poll of a Subscription in the background
// and returns at once. The caller enforces the refresh window
// (store.RequestSubscriptionRefresh). A retired feed, or one already being
// polled, is skipped.
func (p *Poller) PollSubscriptionNow(ctx context.Context, id store.SubscriptionID) error {
	subscription, err := p.store.Subscription(ctx, id)
	if err != nil {
		return err
	}
	_, health, err := p.store.SubscriptionPolicyAndHealth(ctx, id)
	if err != nil {
		return err
	}
	if !health.RetiredAt.IsZero() {
		return nil
	}
	background := context.WithoutCancel(ctx)
	p.background.Go(func() {
		if _, err := p.pollGuarded(background, subscription); err != nil {
			p.logger.WarnContext(background, "subscription poll failed", "subscription", string(id))
		}
	})
	return nil
}

// pollGuarded runs one poll unless the Subscription is already being polled
// (the job and an on-demand refresh must not download the same entry twice).
func (p *Poller) pollGuarded(ctx context.Context, subscription store.Subscription) (int, error) {
	if _, busy := p.inflight.LoadOrStore(subscription.ID, struct{}{}); busy {
		return 0, nil
	}
	defer p.inflight.Delete(subscription.ID)
	current, err := p.store.Subscription(ctx, subscription.ID)
	if err != nil {
		return 0, err
	}
	return p.pollSubscription(ctx, current)
}

func (p *Poller) clock() time.Time {
	if p.now != nil {
		return p.now().UTC()
	}
	return time.Now().UTC()
}

func (p *Poller) interval() time.Duration {
	return DefaultPollInterval
}

func (p *Poller) random() float64 {
	if p.jitter != nil {
		return p.jitter()
	}
	return rand.Float64() // #nosec G404 -- poll-schedule jitter, not a security value
}

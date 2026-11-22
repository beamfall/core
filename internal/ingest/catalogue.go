package ingest

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

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
	policy, err := p.store.SubscriptionDownloadPolicy(ctx, subscription.ID)
	if err != nil {
		return nil, false, err
	}
	var selectInitial func() []string
	if policy.Policy == store.DownloadPolicyLatest {
		selectInitial = func() []string {
			return selectByPolicy(policy, true, document.Serial, subscription.AddedAt, entries, nil)
		}
	}
	_, err = p.store.UpsertSubscriptionCatalogWithInitialDownloads(ctx, subscription.ID, rows, now, selectInitial)
	encrypted := errors.Is(err, store.ErrSubscriptionCatalogUnsupported)
	if err != nil && !encrypted {
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
	result, err := p.engine.FetchFeed(ctx, feedURL, "", "", "")
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

// PollSubscriptionNow starts a due Subscription poll in the background after
// a follow or policy change. Scheduled polls share the same in-flight guard.
func (p *Poller) PollSubscriptionNow(ctx context.Context, id store.SubscriptionID) error {
	subscription, err := p.store.Subscription(ctx, id)
	if err != nil {
		return err
	}
	if !subscription.Health.Due(p.now()) {
		return nil
	}
	background := context.WithoutCancel(ctx)
	p.background.Go(func() {
		if _, err := p.pollGuarded(background, id); err != nil {
			p.logger.WarnContext(background, "subscription poll failed", "subscription", string(id))
		}
	})
	return nil
}

// pollGuarded reloads the Subscription under the in-flight guard so either
// poll entry point sees the latest policy, URL and due state.
func (p *Poller) pollGuarded(ctx context.Context, id store.SubscriptionID) (int, error) {
	if _, busy := p.inflight.LoadOrStore(id, struct{}{}); busy {
		return 0, nil
	}
	defer p.inflight.Delete(id)
	current, err := p.store.Subscription(ctx, id)
	if err != nil {
		return 0, err
	}
	if !current.Health.Due(p.now()) {
		return 0, nil
	}
	return p.pollSubscription(ctx, current)
}

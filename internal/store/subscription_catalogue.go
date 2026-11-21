package store

import (
	"context"
	"errors"
	"time"
)

// Feed catalogue, download policy and feed health (ADR-0238, CORE-2953 and
// CORE-2955). CONTRACT STUBS: the signatures below are the agreed interface
// between store, ingest, playback and graph; bodies land with CORE-2953.

// errSubscriptionCatalogueUnimplemented marks a contract stub.
var errSubscriptionCatalogueUnimplemented = errors.New("subscription catalogue: not implemented")

// ErrSubscriptionRefreshTooSoon means a refresh was asked for within the
// 5-minute window of the previous one (ADR-0238).
var ErrSubscriptionRefreshTooSoon = errors.New("subscription refreshed too recently")

// DownloadPolicy decides which catalogued feed entries the poller downloads.
type DownloadPolicy string

const (
	DownloadPolicyLatest     DownloadPolicy = "latest"
	DownloadPolicyNewOnly    DownloadPolicy = "new-only"
	DownloadPolicyEverything DownloadPolicy = "everything"
	DownloadPolicyStreamOnly DownloadPolicy = "stream-only"

	// DefaultDownloadLatestN is N for the `latest` policy.
	DefaultDownloadLatestN = 3

	// SubscriptionRefreshInterval is the minimum gap between refreshes.
	SubscriptionRefreshInterval = 5 * time.Minute
)

// FeedErrorClass classifies a failed poll. "" means the last poll succeeded
// (or none has run). The class never carries the feed URL or response body.
type FeedErrorClass string

const (
	FeedErrorDNS         FeedErrorClass = "dns"
	FeedErrorTLS         FeedErrorClass = "tls"
	FeedErrorHTTP4xx     FeedErrorClass = "http-4xx"
	FeedErrorHTTP5xx     FeedErrorClass = "http-5xx"
	FeedErrorParse       FeedErrorClass = "parse"
	FeedErrorSSRFBlocked FeedErrorClass = "ssrf-blocked"
	FeedErrorAuth        FeedErrorClass = "auth"
	FeedErrorGone        FeedErrorClass = "gone" // HTTP 410: the feed is retired
	FeedErrorNetwork     FeedErrorClass = "network"
)

// SubscriptionHealth is a Subscription's persisted poll outcome.
type SubscriptionHealth struct {
	LastErrorClass      FeedErrorClass
	FailingSince        time.Time // zero when healthy
	ConsecutiveFailures int
	NextPollAt          time.Time // zero means due now
	LastSuccessAt       time.Time
	RetiredAt           time.Time // set by a 410; the poller skips retired feeds
	RefreshRequestedAt  time.Time
}

// SubscriptionPolicy is a Subscription's download policy.
type SubscriptionPolicy struct {
	Policy  DownloadPolicy
	LatestN int
}

// SubscriptionEntryDownloadState tracks a catalogued entry's download.
type SubscriptionEntryDownloadState string

const (
	EntryDownloadNone       SubscriptionEntryDownloadState = "none"
	EntryDownloadQueued     SubscriptionEntryDownloadState = "queued"
	EntryDownloadDownloaded SubscriptionEntryDownloadState = "downloaded"
	EntryDownloadFailed     SubscriptionEntryDownloadState = "failed"
)

// FeedCatalogEntry is one parsed feed entry the poller hands the store.
// EnclosureURL is secret class (invariant 5): never logged, never on the wire.
type FeedCatalogEntry struct {
	GUID            string
	Title           string
	Description     string
	PublishedAt     time.Time
	DurationSeconds int64
	EnclosureURL    string
	EnclosureType   string
	EnclosureLength int64
	// Position is the entry's index in the feed document (0 = first).
	Position int
	// FeedTitle is the channel title, used for the Media's grouping metadata.
	FeedTitle string
}

// SubscriptionEntry is a catalogued feed entry bound to its Media.
type SubscriptionEntry struct {
	SubscriptionID  SubscriptionID
	GUID            string
	MediaID         MediaID
	Title           string
	FeedPosition    int
	PublishedAt     time.Time
	FirstSeenAt     time.Time
	EnclosureURL    string // secret class: never logged, never on the wire
	EnclosureType   string
	EnclosureLength int64
	DownloadState   SubscriptionEntryDownloadState
	DownloadPath    string
	// Downloaded reports whether the Media has a live, non-provisional file.
	Downloaded bool
}

// SubscriptionEntryStreamKey is the proxy-tee registration GUID for an entry;
// its Media ID is ProvisionalMediaID(library, SubscriptionEntryStreamKey(...)).
func SubscriptionEntryStreamKey(id SubscriptionID, guid string) string {
	return string(id) + "\x00" + guid
}

// SubscriptionEntryMediaID is the file-less Media ID for a feed entry.
func SubscriptionEntryMediaID(libraryID LibraryID, id SubscriptionID, guid string) MediaID {
	return ProvisionalMediaID(libraryID, SubscriptionEntryStreamKey(id, guid))
}

// UpsertSubscriptionCatalog records every entry of a parsed feed: new entries
// get a file-less Media, known entries refresh position, title and enclosure.
// It returns the entries created by this call.
func (s *Store) UpsertSubscriptionCatalog(ctx context.Context, id SubscriptionID, entries []FeedCatalogEntry, now time.Time) ([]SubscriptionEntry, error) {
	return nil, errSubscriptionCatalogueUnimplemented
}

// SubscriptionEntries lists a Subscription's catalogued entries in feed order.
func (s *Store) SubscriptionEntries(ctx context.Context, id SubscriptionID) ([]SubscriptionEntry, error) {
	return nil, errSubscriptionCatalogueUnimplemented
}

// SubscriptionEntryForMedia returns the entry and Subscription a Media
// represents; ok is false when the Media is not a feed entry.
func (s *Store) SubscriptionEntryForMedia(ctx context.Context, mediaID MediaID) (SubscriptionEntry, Subscription, bool, error) {
	return SubscriptionEntry{}, Subscription{}, false, errSubscriptionCatalogueUnimplemented
}

// SetSubscriptionEntryDownload records an entry's download state and target
// path. A path recorded here binds the scan import of that file to the
// entry's Media.
func (s *Store) SetSubscriptionEntryDownload(ctx context.Context, id SubscriptionID, guid string, state SubscriptionEntryDownloadState, path string) error {
	return errSubscriptionCatalogueUnimplemented
}

// SubscriptionPolicyAndHealth reads the policy and health of a Subscription.
func (s *Store) SubscriptionPolicyAndHealth(ctx context.Context, id SubscriptionID) (SubscriptionPolicy, SubscriptionHealth, error) {
	return SubscriptionPolicy{}, SubscriptionHealth{}, errSubscriptionCatalogueUnimplemented
}

// SetSubscriptionDownloadPolicy changes the policy. latestN <= 0 keeps the
// default.
func (s *Store) SetSubscriptionDownloadPolicy(ctx context.Context, id SubscriptionID, policy SubscriptionPolicy) error {
	return errSubscriptionCatalogueUnimplemented
}

// RecordSubscriptionPollSuccess clears the failure state.
func (s *Store) RecordSubscriptionPollSuccess(ctx context.Context, id SubscriptionID, at, nextPollAt time.Time) error {
	return errSubscriptionCatalogueUnimplemented
}

// RecordSubscriptionPollFailure records a failed poll. FeedErrorGone also
// sets RetiredAt.
func (s *Store) RecordSubscriptionPollFailure(ctx context.Context, id SubscriptionID, class FeedErrorClass, at, nextPollAt time.Time) error {
	return errSubscriptionCatalogueUnimplemented
}

// RequestSubscriptionRefresh makes the Subscription due now. It returns
// ErrSubscriptionRefreshTooSoon within SubscriptionRefreshInterval of the
// previous request.
func (s *Store) RequestSubscriptionRefresh(ctx context.Context, id SubscriptionID, now time.Time) error {
	return errSubscriptionCatalogueUnimplemented
}

// DueSubscriptions lists unretired Subscriptions whose NextPollAt <= now.
func (s *Store) DueSubscriptions(ctx context.Context, now time.Time) ([]Subscription, error) {
	return nil, errSubscriptionCatalogueUnimplemented
}

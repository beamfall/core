package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// Feed catalogue, download policy and feed health (ADR-0238, CORE-2953 and
// CORE-2955). The signatures below are the agreed interface between store,
// ingest, playback and graph.
//
// Grouping: a feed's entries are grouped by their subscription_entry rows
// (SubscriptionEntries lists them in feed order, SubscriptionEntryForMedia
// maps a Media back to its Subscription). Before the catalogue nothing grouped
// a feed's downloads: the scan import minted one ungrouped Media per file. The
// import binding in importMediaFileSQL now attaches a downloaded file to its
// entry's Media, so downloads share the same grouping as file-less entries.

// ErrSubscriptionCatalogUnsupported means the Subscription's Library keeps its
// Media on the Encrypted-Library plane, where the catalogue (a catalog-plane
// table referencing media) cannot bind entries. The poller falls back to its
// download-every-unseen-entry behaviour for such Libraries.
var ErrSubscriptionCatalogUnsupported = errors.New("subscription catalogue is unavailable for encrypted libraries")

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
//
// A new entry whose GUID was already downloaded before the catalogue existed
// (a subscription_seen_entry receipt whose path is a live file of the Library)
// binds to that file's Media instead of minting a second one. The back
// catalogue of a never-polled Subscription is stamped FirstSeenAt = AddedAt, so
// the new-only policy downloads only entries that arrive after the follow.
func (s *Store) UpsertSubscriptionCatalog(ctx context.Context, id SubscriptionID, entries []FeedCatalogEntry, now time.Time) ([]SubscriptionEntry, error) {
	if id == "" {
		return nil, errors.New("subscription ID is required")
	}
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	subscription, err := scanSubscription(s.catalogR.QueryRowContext(ctx, subscriptionColumns+` WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	var encrypted int
	if err := s.catalogR.QueryRowContext(ctx, `SELECT encrypted FROM library WHERE id = ?`, subscription.LibraryID).Scan(&encrypted); err != nil {
		return nil, err
	}
	if encrypted != 0 {
		return nil, ErrSubscriptionCatalogUnsupported
	}
	created, minted, err := s.upsertSubscriptionCatalogSQL(ctx, subscription, entries, now.UTC())
	if err != nil {
		return nil, err
	}
	for _, mediaID := range minted {
		if eventErr := s.appendMediaEventLocked(ctx, MediaEventCreated, mediaID, nil, now.UTC()); eventErr != nil {
			slog.Warn("media outbox event append failed", "kind", MediaEventCreated, "media", mediaID, "error", eventErr)
		}
	}
	return created, nil
}

func (s *Store) upsertSubscriptionCatalogSQL(ctx context.Context, subscription Subscription, entries []FeedCatalogEntry, now time.Time) ([]SubscriptionEntry, []MediaID, error) {
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	known := map[string]MediaID{}
	rows, err := tx.QueryContext(ctx, `SELECT guid, media_id FROM subscription_entry WHERE subscription_id = ?`, subscription.ID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var guid string
		var mediaID MediaID
		if err := rows.Scan(&guid, &mediaID); err != nil {
			rows.Close()
			return nil, nil, err
		}
		known[guid] = mediaID
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	firstSeen := now
	if len(known) == 0 && subscription.LastPolledAt.IsZero() {
		firstSeen = subscription.AddedAt
	}

	created := []SubscriptionEntry{}
	minted := []MediaID{}
	feedTitle := ""
	for _, entry := range entries {
		guid := strings.TrimSpace(entry.GUID)
		if guid == "" {
			continue
		}
		if feedTitle == "" {
			feedTitle = strings.TrimSpace(entry.FeedTitle)
		}
		title := strings.TrimSpace(entry.Title)
		if mediaID, ok := known[guid]; ok {
			if mediaID == "" {
				continue // a duplicate GUID later in the same document
			}
			if err := refreshSubscriptionEntryTx(ctx, tx, subscription.ID, guid, mediaID, title, entry); err != nil {
				return nil, nil, err
			}
			known[guid] = ""
			continue
		}
		known[guid] = ""
		row := SubscriptionEntry{
			SubscriptionID:  subscription.ID,
			GUID:            guid,
			Title:           title,
			FeedPosition:    entry.Position,
			PublishedAt:     entry.PublishedAt,
			FirstSeenAt:     firstSeen,
			EnclosureURL:    entry.EnclosureURL,
			EnclosureType:   entry.EnclosureType,
			EnclosureLength: max(0, entry.EnclosureLength),
			DownloadState:   EntryDownloadNone,
		}
		archived, path, err := archivedSubscriptionMediaTx(ctx, tx, subscription, guid)
		if err != nil {
			return nil, nil, err
		}
		if archived != "" {
			row.MediaID, row.DownloadState, row.DownloadPath, row.Downloaded = archived, EntryDownloadDownloaded, path, true
		} else {
			row.MediaID = SubscriptionEntryMediaID(subscription.LibraryID, subscription.ID, guid)
			inserted, err := insertSubscriptionEntryMediaTx(ctx, tx, subscription, row, entry)
			if err != nil {
				return nil, nil, err
			}
			if inserted {
				minted = append(minted, row.MediaID)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO media_external_id(media_id, scheme, value) VALUES (?, ?, ?)
		`, row.MediaID, subscriptionFeedGUIDScheme, guid); err != nil {
			return nil, nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO subscription_entry(subscription_id, guid, media_id, feed_position, published_at_ms,
			  first_seen_at_ms, enclosure_url, enclosure_type, enclosure_length, download_state, download_path)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, row.SubscriptionID, row.GUID, row.MediaID, row.FeedPosition, unixMilliOrZero(row.PublishedAt),
			row.FirstSeenAt.UnixMilli(), row.EnclosureURL, row.EnclosureType, row.EnclosureLength,
			row.DownloadState, row.DownloadPath); err != nil {
			return nil, nil, err
		}
		created = append(created, row)
	}
	if subscription.Title == "" && feedTitle != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE subscription SET title = ? WHERE id = ? AND title = ''`, feedTitle, subscription.ID); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return created, minted, nil
}

// refreshSubscriptionEntryTx updates a known entry's feed-sourced fields. The
// Media title follows the feed unless identification locked it; probe facts
// of a downloaded file are never overwritten by the feed's guesses.
func refreshSubscriptionEntryTx(ctx context.Context, tx *sql.Tx, id SubscriptionID, guid string, mediaID MediaID, title string, entry FeedCatalogEntry) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE subscription_entry
		SET feed_position = ?, published_at_ms = ?, enclosure_url = ?, enclosure_type = ?, enclosure_length = ?
		WHERE subscription_id = ? AND guid = ?
	`, entry.Position, unixMilliOrZero(entry.PublishedAt), entry.EnclosureURL, entry.EnclosureType,
		max(0, entry.EnclosureLength), id, guid); err != nil {
		return err
	}
	if title == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE media SET title = ?
		WHERE id = ? AND title <> ? AND title_identified = 0 AND trashed_at_ms IS NULL
	`, title, mediaID, title)
	return err
}

// archivedSubscriptionMediaTx resolves a pre-catalogue download of guid: the
// receipt's path when it is a live, untrashed file of the Library.
func archivedSubscriptionMediaTx(ctx context.Context, tx *sql.Tx, subscription Subscription, guid string) (MediaID, string, error) {
	var mediaID MediaID
	var path string
	err := tx.QueryRowContext(ctx, `
		SELECT mf.media_id, mf.path
		FROM subscription_seen_entry se
		JOIN media_file mf ON mf.path = se.path
		JOIN media m ON m.id = mf.media_id
		WHERE se.subscription_id = ? AND se.guid = ? AND se.path <> ''
		  AND m.library_id = ? AND m.trashed_at_ms IS NULL
		  AND mf.missing_at_ms IS NULL AND mf.provisional = 0
		LIMIT 1
	`, subscription.ID, guid, subscription.LibraryID).Scan(&mediaID, &path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return mediaID, path, err
}

// insertSubscriptionEntryMediaTx creates the entry's file-less Media and its
// stream key. inserted is false when the Media row already existed.
func insertSubscriptionEntryMediaTx(ctx context.Context, tx *sql.Tx, subscription Subscription, row SubscriptionEntry, entry FeedCatalogEntry) (bool, error) {
	container, codec, class := enclosureMediaFacts(entry.EnclosureType)
	title := row.Title
	if title == "" {
		title = row.GUID
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO media(id, library_id, title, summary, duration_seconds, container, codec, media_class, mutated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'scan')
		ON CONFLICT(id) DO NOTHING
	`, row.MediaID, subscription.LibraryID, title, strings.TrimSpace(entry.Description),
		max(0, int(entry.DurationSeconds)), container, codec, class)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO media_external_id(media_id, scheme, value) VALUES (?, ?, ?)
	`, row.MediaID, ProvisionalExternalIDScheme, SubscriptionEntryStreamKey(subscription.ID, row.GUID)); err != nil {
		return false, err
	}
	return affected > 0, nil
}

// enclosureMediaFacts guesses container, codec and class from an enclosure
// MIME type until a download is probed. mp3 is the default, matching
// provisional Media.
func enclosureMediaFacts(mimeType string) (container, codec string, class MediaClass) {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if i := strings.IndexByte(mimeType, ';'); i >= 0 {
		mimeType = strings.TrimSpace(mimeType[:i])
	}
	switch mimeType {
	case "audio/mp4", "audio/m4a", "audio/x-m4a", "audio/mp4a-latm":
		return "m4a", "aac", MediaClassAudio
	case "audio/aac", "audio/aacp", "audio/x-aac":
		return "aac", "aac", MediaClassAudio
	case "audio/ogg", "audio/vorbis":
		return "ogg", "vorbis", MediaClassAudio
	case "audio/opus":
		return "opus", "opus", MediaClassAudio
	case "audio/flac", "audio/x-flac":
		return "flac", "flac", MediaClassAudio
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav", "pcm_s16le", MediaClassAudio
	case "video/mp4", "video/x-m4v":
		return "mp4", "h264", MediaClassVideo
	case "video/quicktime":
		return "mov", "h264", MediaClassVideo
	case "video/webm":
		return "webm", "vp9", MediaClassVideo
	}
	return "mp3", "mp3", MediaClassAudio
}

const subscriptionEntryColumns = `
	SELECT se.subscription_id, se.guid, se.media_id, m.title, se.feed_position, se.published_at_ms,
	  se.first_seen_at_ms, se.enclosure_url, se.enclosure_type, se.enclosure_length,
	  se.download_state, se.download_path,
	  EXISTS (SELECT 1 FROM media_file mf WHERE mf.media_id = se.media_id
	    AND mf.missing_at_ms IS NULL AND mf.provisional = 0)
	FROM subscription_entry se JOIN media m ON m.id = se.media_id`

func scanSubscriptionEntryRow(scan func(...any) error) (SubscriptionEntry, error) {
	var entry SubscriptionEntry
	var publishedMS, firstSeenMS int64
	var downloaded int
	if err := scan(&entry.SubscriptionID, &entry.GUID, &entry.MediaID, &entry.Title, &entry.FeedPosition,
		&publishedMS, &firstSeenMS, &entry.EnclosureURL, &entry.EnclosureType, &entry.EnclosureLength,
		&entry.DownloadState, &entry.DownloadPath, &downloaded); err != nil {
		return SubscriptionEntry{}, err
	}
	entry.PublishedAt = timeFromMilliOrZero(publishedMS)
	entry.FirstSeenAt = time.UnixMilli(firstSeenMS).UTC()
	entry.Downloaded = downloaded != 0
	return entry, nil
}

// SubscriptionEntries lists a Subscription's catalogued entries in feed order.
func (s *Store) SubscriptionEntries(ctx context.Context, id SubscriptionID) ([]SubscriptionEntry, error) {
	if id == "" {
		return nil, errors.New("subscription ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, subscriptionEntryColumns+`
		WHERE se.subscription_id = ? ORDER BY se.feed_position, se.first_seen_at_ms, se.guid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []SubscriptionEntry{}
	for rows.Next() {
		entry, err := scanSubscriptionEntryRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// SubscriptionEntryForMedia returns the entry and Subscription a Media
// represents; ok is false when the Media is not a feed entry.
func (s *Store) SubscriptionEntryForMedia(ctx context.Context, mediaID MediaID) (SubscriptionEntry, Subscription, bool, error) {
	if mediaID == "" {
		return SubscriptionEntry{}, Subscription{}, false, errors.New("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SubscriptionEntry{}, Subscription{}, false, err
	}
	entry, err := scanSubscriptionEntryRow(s.catalogR.QueryRowContext(ctx, subscriptionEntryColumns+`
		WHERE se.media_id = ? ORDER BY se.first_seen_at_ms, se.subscription_id, se.guid LIMIT 1`, mediaID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionEntry{}, Subscription{}, false, nil
	}
	if err != nil {
		return SubscriptionEntry{}, Subscription{}, false, err
	}
	subscription, err := scanSubscription(s.catalogR.QueryRowContext(ctx, subscriptionColumns+` WHERE id = ?`, entry.SubscriptionID))
	if err != nil {
		return SubscriptionEntry{}, Subscription{}, false, err
	}
	return entry, subscription, true, nil
}

// SetSubscriptionEntryDownload records an entry's download state and target
// path. A path recorded here binds the scan import of that file to the
// entry's Media.
func (s *Store) SetSubscriptionEntryDownload(ctx context.Context, id SubscriptionID, guid string, state SubscriptionEntryDownloadState, path string) error {
	if id == "" {
		return errors.New("subscription ID is required")
	}
	if strings.TrimSpace(guid) == "" {
		return errors.New("entry GUID is required")
	}
	switch state {
	case EntryDownloadNone, EntryDownloadQueued, EntryDownloadDownloaded, EntryDownloadFailed:
	default:
		return ValidationError("download state is invalid")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE subscription_entry SET download_state = ?, download_path = ?
		WHERE subscription_id = ? AND guid = ?
	`, state, strings.TrimSpace(path), id, guid)
	if err != nil {
		return err
	}
	return requireOneRow(result, ValidationError("subscription entry not found"))
}

// SubscriptionPolicyAndHealth reads the policy and health of a Subscription.
func (s *Store) SubscriptionPolicyAndHealth(ctx context.Context, id SubscriptionID) (SubscriptionPolicy, SubscriptionHealth, error) {
	if id == "" {
		return SubscriptionPolicy{}, SubscriptionHealth{}, errors.New("subscription ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SubscriptionPolicy{}, SubscriptionHealth{}, err
	}
	var policy SubscriptionPolicy
	var health SubscriptionHealth
	var failingMS, nextMS, successMS, retiredMS, refreshMS int64
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT download_policy, download_latest_n, last_error_class, failing_since_ms, consecutive_failures,
		  next_poll_at_ms, last_success_at_ms, retired_at_ms, refresh_requested_at_ms
		FROM subscription WHERE id = ?
	`, id).Scan(&policy.Policy, &policy.LatestN, &health.LastErrorClass, &failingMS, &health.ConsecutiveFailures,
		&nextMS, &successMS, &retiredMS, &refreshMS)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionPolicy{}, SubscriptionHealth{}, ErrSubscriptionNotFound
	}
	if err != nil {
		return SubscriptionPolicy{}, SubscriptionHealth{}, err
	}
	if policy.LatestN <= 0 {
		policy.LatestN = DefaultDownloadLatestN
	}
	health.FailingSince = timeFromMilliOrZero(failingMS)
	health.NextPollAt = timeFromMilliOrZero(nextMS)
	health.LastSuccessAt = timeFromMilliOrZero(successMS)
	health.RetiredAt = timeFromMilliOrZero(retiredMS)
	health.RefreshRequestedAt = timeFromMilliOrZero(refreshMS)
	return policy, health, nil
}

// SetSubscriptionDownloadPolicy changes the policy. latestN <= 0 keeps the
// default. The Subscription becomes due now and its conditional-GET
// validators are cleared, so the next poll applies the policy to the full
// feed rather than to an unchanged (304) response.
func (s *Store) SetSubscriptionDownloadPolicy(ctx context.Context, id SubscriptionID, policy SubscriptionPolicy) error {
	if id == "" {
		return errors.New("subscription ID is required")
	}
	switch policy.Policy {
	case DownloadPolicyLatest, DownloadPolicyNewOnly, DownloadPolicyEverything, DownloadPolicyStreamOnly:
	default:
		return ValidationError("download policy is invalid")
	}
	latestN := policy.LatestN
	if latestN <= 0 {
		latestN = DefaultDownloadLatestN
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE subscription
		SET download_policy = ?, download_latest_n = ?, next_poll_at_ms = 0, etag = '', last_modified = ''
		WHERE id = ?
	`, policy.Policy, latestN, id)
	if err != nil {
		return err
	}
	return requireOneRow(result, ErrSubscriptionNotFound)
}

// RecordSubscriptionPollSuccess clears the failure state.
func (s *Store) RecordSubscriptionPollSuccess(ctx context.Context, id SubscriptionID, at, nextPollAt time.Time) error {
	if id == "" {
		return errors.New("subscription ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE subscription
		SET last_error_class = '', failing_since_ms = 0, consecutive_failures = 0,
		  last_success_at_ms = ?, next_poll_at_ms = ?
		WHERE id = ?
	`, at.UTC().UnixMilli(), unixMilliOrZero(nextPollAt), id)
	if err != nil {
		return err
	}
	return requireOneRow(result, ErrSubscriptionNotFound)
}

// RecordSubscriptionPollFailure records a failed poll. FeedErrorGone also
// sets RetiredAt.
func (s *Store) RecordSubscriptionPollFailure(ctx context.Context, id SubscriptionID, class FeedErrorClass, at, nextPollAt time.Time) error {
	if id == "" {
		return errors.New("subscription ID is required")
	}
	switch class {
	case FeedErrorDNS, FeedErrorTLS, FeedErrorHTTP4xx, FeedErrorHTTP5xx, FeedErrorParse,
		FeedErrorSSRFBlocked, FeedErrorAuth, FeedErrorGone, FeedErrorNetwork:
	default:
		return ValidationError("feed error class is invalid")
	}
	atMS := at.UTC().UnixMilli()
	retiredMS := int64(0)
	if class == FeedErrorGone {
		retiredMS = atMS
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE subscription
		SET last_error_class = ?,
		  failing_since_ms = CASE WHEN failing_since_ms = 0 THEN ? ELSE failing_since_ms END,
		  consecutive_failures = consecutive_failures + 1,
		  next_poll_at_ms = ?,
		  retired_at_ms = CASE WHEN ? > 0 AND retired_at_ms = 0 THEN ? ELSE retired_at_ms END
		WHERE id = ?
	`, class, atMS, unixMilliOrZero(nextPollAt), retiredMS, retiredMS, id)
	if err != nil {
		return err
	}
	return requireOneRow(result, ErrSubscriptionNotFound)
}

// RequestSubscriptionRefresh makes the Subscription due now. It returns
// ErrSubscriptionRefreshTooSoon within SubscriptionRefreshInterval of the
// previous request. The conditional-GET validators are cleared so the refresh
// fetches the full feed.
func (s *Store) RequestSubscriptionRefresh(ctx context.Context, id SubscriptionID, now time.Time) error {
	if id == "" {
		return errors.New("subscription ID is required")
	}
	nowMS := now.UTC().UnixMilli()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE subscription
		SET refresh_requested_at_ms = ?, next_poll_at_ms = ?, etag = '', last_modified = ''
		WHERE id = ? AND (refresh_requested_at_ms = 0 OR refresh_requested_at_ms <= ?)
	`, nowMS, nowMS, id, nowMS-SubscriptionRefreshInterval.Milliseconds())
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	var one int
	err = s.catalogR.QueryRowContext(ctx, `SELECT 1 FROM subscription WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSubscriptionNotFound
	}
	if err != nil {
		return err
	}
	return ErrSubscriptionRefreshTooSoon
}

// DueSubscriptions lists unretired Subscriptions whose NextPollAt <= now.
func (s *Store) DueSubscriptions(ctx context.Context, now time.Time) ([]Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return s.querySubscriptions(ctx, subscriptionColumns+`
		WHERE retired_at_ms = 0 AND next_poll_at_ms <= ? ORDER BY next_poll_at_ms, id`, now.UTC().UnixMilli())
}

// ObserveSubscriptionFeedMove records one poll's feed-move observation: the
// new feed URL a permanent redirect or an in-feed new-feed-url announced, or
// "" when the poll saw none. The feed URL is rewritten only when two
// consecutive observations agree; a different URL restarts the count and ""
// clears it. A move to a URL another Subscription of the Library already
// follows is dropped and the old URL kept. moved reports a rewrite. The
// caller vets the URL (SSRF guard) first; newURL is secret class and never
// appears in an error.
func (s *Store) ObserveSubscriptionFeedMove(ctx context.Context, id SubscriptionID, newURL string) (moved bool, err error) {
	if id == "" {
		return false, errors.New("subscription ID is required")
	}
	if strings.TrimSpace(newURL) != "" {
		// Never wrap the url.Parse error: it embeds the raw URL.
		if newURL, err = normalizeSubscriptionFeedURL(newURL); err != nil {
			return false, err
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
	defer tx.Rollback()
	var libraryID LibraryID
	var current, pending string
	var count int
	err = tx.QueryRowContext(ctx, `
		SELECT library_id, feed_url, pending_feed_url, pending_feed_url_count FROM subscription WHERE id = ?
	`, id).Scan(&libraryID, &current, &pending, &count)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrSubscriptionNotFound
	}
	if err != nil {
		return false, err
	}
	switch {
	case newURL == "" || newURL == current:
		pending, count = "", 0
	case newURL == pending:
		count++
	default:
		pending, count = newURL, 1
	}
	if count >= 2 {
		var taken int
		err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM subscription WHERE library_id = ? AND feed_url = ? AND id <> ?
		`, libraryID, pending, id).Scan(&taken)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if err == nil {
			pending, count = "", 0
		} else {
			if _, err := tx.ExecContext(ctx, `
				UPDATE subscription
				SET feed_url = ?, pending_feed_url = '', pending_feed_url_count = 0, etag = '', last_modified = ''
				WHERE id = ?
			`, pending, id); err != nil {
				return false, err
			}
			return true, tx.Commit()
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE subscription SET pending_feed_url = ?, pending_feed_url_count = ? WHERE id = ?
	`, pending, count, id); err != nil {
		return false, err
	}
	return false, tx.Commit()
}

func requireOneRow(result sql.Result, notFound error) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return notFound
	}
	return nil
}

func unixMilliOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixMilli()
}

func timeFromMilliOrZero(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

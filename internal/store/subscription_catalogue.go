package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// Feed catalogue and download policy (ADR-0238, CORE-2953).
//
// Grouping: a feed's entries are grouped by their subscription_entry rows
// (SubscriptionEntries lists them in feed order, SubscriptionEntryForMedia
// maps a Media back to its Subscription). Before the catalogue nothing grouped
// a feed's downloads: the scan import minted one ungrouped Media per file. The
// import binding in importMediaFileSQL now attaches a downloaded file to its
// entry's Media, so downloads share the same grouping as file-less entries.

// ErrSubscriptionCatalogUnsupported means the Subscription's Library keeps its
// Media on the Encrypted-Library plane, where the catalogue (a catalog-plane
// table referencing media) cannot bind entries.
var ErrSubscriptionCatalogUnsupported = errors.New("subscription catalogue is unavailable for encrypted libraries")

// DownloadPolicy decides which catalogued feed entries the poller downloads.
type DownloadPolicy string

const (
	DownloadPolicyLatest     DownloadPolicy = "latest"
	DownloadPolicyNewOnly    DownloadPolicy = "new-only"
	DownloadPolicyEverything DownloadPolicy = "everything"
	DownloadPolicyStreamOnly DownloadPolicy = "stream-only"

	// DefaultDownloadLatestN is N for the `latest` policy.
	DefaultDownloadLatestN = 3
)

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

func feedGUIDExternalID(guid string) string {
	if u, err := url.Parse(guid); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return fmt.Sprintf("urn:beamfall:feed-entry:%x", sha256.Sum256([]byte("guid\x00"+guid)))
	}
	return guid
}

func feedEntryFallbackTitle(guid string) string {
	if u, err := url.Parse(guid); err == nil && (u.Host != "" || u.Scheme == "http" || u.Scheme == "https") {
		return "Untitled"
	}
	return guid
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
			_ = rows.Close()
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
	} else if firstSeen.UnixMilli() <= subscription.AddedAt.UnixMilli() {
		firstSeen = subscription.AddedAt.Add(time.Millisecond)
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
		`, row.MediaID, subscriptionFeedGUIDScheme, feedGUIDExternalID(guid)); err != nil {
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
	if title != "" {
		displayTitle := titleOrFeedFallback(title, guid)
		if _, err := tx.ExecContext(ctx, `
			UPDATE media SET title = ?
			WHERE id = ? AND title <> ? AND title_identified = 0 AND trashed_at_ms IS NULL
		`, displayTitle, mediaID, displayTitle); err != nil {
			return err
		}
	}
	return repairLegacyFeedIdentity(ctx, tx, id, guid, mediaID)
}

func repairLegacyFeedIdentity(ctx context.Context, tx *sql.Tx, id SubscriptionID, guid string, mediaID MediaID) error {
	if title := feedEntryFallbackTitle(guid); title != guid {
		if _, err := tx.ExecContext(ctx, `UPDATE media SET title = ? WHERE id = ? AND title = ?`, title, mediaID, guid); err != nil {
			return err
		}
	}
	if opaque := feedGUIDExternalID(guid); opaque != guid {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO media_external_id(media_id, scheme, value)
			SELECT media_id, scheme, CASE WHEN scheme = ? THEN ? ELSE ? END
			FROM media_external_id WHERE media_id = ? AND ((scheme = ? AND value = ?) OR (scheme = ? AND value = ?))
		`, subscriptionFeedGUIDScheme, opaque, SubscriptionEntryStreamKey(id, opaque),
			mediaID, subscriptionFeedGUIDScheme, guid, ProvisionalExternalIDScheme, SubscriptionEntryStreamKey(id, guid)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			DELETE FROM media_external_id
			WHERE media_id = ? AND ((scheme = ? AND value = ?) OR (scheme = ? AND value = ?))
		`, mediaID, subscriptionFeedGUIDScheme, guid, ProvisionalExternalIDScheme, SubscriptionEntryStreamKey(id, guid))
		return err
	}
	return nil
}

func titleOrFeedFallback(title, guid string) string {
	if title == "" || title == guid {
		return feedEntryFallbackTitle(guid)
	}
	return title
}

// SubscriptionBackCatalogue is the set of skipped initial GUIDs recorded for
// a Library that cannot hold file-less Media.
func (s *Store) SubscriptionBackCatalogue(ctx context.Context, id SubscriptionID) (map[string]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT guid FROM subscription_seen_entry
		WHERE subscription_id = ? AND path = '' AND seen_at_ms = 0
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	skipped := map[string]bool{}
	for rows.Next() {
		var guid string
		if err := rows.Scan(&guid); err != nil {
			return nil, err
		}
		skipped[guid] = true
	}
	return skipped, rows.Err()
}

// RecordInitialSubscriptionDownloads commits the initial selection before
// downloading any bytes. When the Library has no catalogue, it records
// excluded GUIDs with a blank path and zero time instead of queuing Media.
func (s *Store) RecordInitialSubscriptionDownloads(ctx context.Context, id SubscriptionID, queued, skipped []string, now time.Time) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, guid := range queued {
		if _, err := tx.ExecContext(ctx, `
			UPDATE subscription_entry SET download_state = ?
			WHERE subscription_id = ? AND guid = ? AND download_state = ?
		`, EntryDownloadQueued, id, guid, EntryDownloadNone); err != nil {
			return err
		}
	}
	for _, guid := range skipped {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO subscription_seen_entry(subscription_id, guid, seen_at_ms, path)
			VALUES (?, ?, 0, '')
		`, id, guid); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE subscription SET last_polled_at_ms = ?, etag = '', last_modified = '' WHERE id = ?
	`, now.UnixMilli(), id); err != nil {
		return err
	}
	return tx.Commit()
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
	title := titleOrFeedFallback(row.Title, row.GUID)
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
	`, row.MediaID, ProvisionalExternalIDScheme, provisionalGUIDExternalID(SubscriptionEntryStreamKey(subscription.ID, row.GUID))); err != nil {
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
	entry.PublishedAt = optionalUnixMilli(publishedMS)
	entry.FirstSeenAt = time.UnixMilli(firstSeenMS).UTC()
	entry.Downloaded = downloaded != 0
	if entry.Title == entry.GUID {
		entry.Title = feedEntryFallbackTitle(entry.GUID)
	}
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

// SubscriptionDownloadPolicy reads the catalogue download policy for a Subscription.
func (s *Store) SubscriptionDownloadPolicy(ctx context.Context, id SubscriptionID) (SubscriptionPolicy, error) {
	if id == "" {
		return SubscriptionPolicy{}, errors.New("subscription ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return SubscriptionPolicy{}, err
	}
	var policy SubscriptionPolicy
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT download_policy, download_latest_n FROM subscription WHERE id = ?
	`, id).Scan(&policy.Policy, &policy.LatestN)
	if errors.Is(err, sql.ErrNoRows) {
		return SubscriptionPolicy{}, ErrSubscriptionNotFound
	}
	if err != nil {
		return SubscriptionPolicy{}, err
	}
	if policy.LatestN <= 0 {
		policy.LatestN = DefaultDownloadLatestN
	}
	return policy, nil
}

// SetSubscriptionDownloadPolicy changes the policy. latestN <= 0 keeps the
// default. An active Subscription becomes due now. Clearing its conditional-GET
// validators makes the next poll apply the policy to the full
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
		SET download_policy = ?, download_latest_n = ?, etag = '', last_modified = '',
		    next_poll_at_ms = CASE WHEN retired_at_ms = 0 THEN 0 ELSE next_poll_at_ms END
		WHERE id = ?
	`, policy.Policy, latestN, id)
	if err != nil {
		return err
	}
	return requireOneRow(result, ErrSubscriptionNotFound)
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

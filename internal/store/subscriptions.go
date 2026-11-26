package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// ErrSubscriptionNotFound means no Subscription exists with the given ID.
var ErrSubscriptionNotFound = errors.New("subscription not found")

// ErrEncryptedLibrarySubscription refuses a Subscription in an Encrypted
// Library (CORE-0706). Its feed URL, title, root, entry GUIDs and download
// paths would sit in the shared catalog in plaintext, so the operation is
// refused until the Library's own plane can hold them.
var ErrEncryptedLibrarySubscription = ValidationError("subscriptions are not available in an Encrypted Library yet")

// ErrSubscriptionEntryCollision reports that an entry GUID already names a
// different file. The conflicting binding is kept for review in
// subscription_entry_collision instead of being dropped.
var ErrSubscriptionEntryCollision = ValidationError("entry GUID is already associated with a different media file")

// SubscriptionID is a stable Subscription identifier.
type SubscriptionID string

type RetentionKind string

const (
	RetentionKeepAll           RetentionKind = "keep-all"
	RetentionKeepLastN         RetentionKind = "keep-last-n"
	RetentionDeleteAfterPlayed RetentionKind = "delete-after-played"

	subscriptionFeedGUIDScheme = "feed-guid"
)

// Subscription is a Library-level feed subscription: the
// poller fetches FeedURL, detects new entries by GUID, and downloads their
// enclosures into Root for the standard scan pipeline to import.
//
// FeedURL can embed private-feed credentials (invariant 5): it must never be
// logged or echoed into error text — validation errors below are deliberately
// generic for that reason.
type Subscription struct {
	ID        SubscriptionID
	LibraryID LibraryID
	FeedURL   string
	// AuthRef is the secure-store KEY for a private feed's Authorization
	// header — NEVER the header itself (invariant #5). The literal credential
	// lives in the 0600 secretFiles store under this ref; the poller resolves
	// it at fetch time. "" means an anonymous (public) feed with no secret.
	AuthRef string
	// Root is the folder downloads land in — the scan root the poller
	// enqueues after new enclosures arrive.
	Root         string
	Title        string
	AddedAt      time.Time
	LastPolledAt time.Time
	// ETag and LastModified are the conditional-GET validators returned by
	// the previous poll ("" until a poll stores them).
	ETag          string
	LastModified  string
	RetentionKind RetentionKind
	RetentionN    *int
	// Health is the poller's record of how the feed is fetching (CORE-2955).
	Health SubscriptionHealth
}

// SubscriptionEntryAssociation is a feed-scoped association between one
// Subscription item GUID and the archived Media file that represents it.
type SubscriptionEntryAssociation struct {
	SubscriptionID         SubscriptionID
	GUID                   string
	MediaID                MediaID
	Path                   string
	ReplayedItemStateSweep int
}

// AddSubscription inserts a Subscription for an existing Library. The ID is
// server-issued; AddedAt comes from now. One Subscription per
// (library, feed URL) — duplicates are an error.
func (s *Store) AddSubscription(ctx context.Context, subscription Subscription, now time.Time) (Subscription, error) {
	subscription.Title = strings.TrimSpace(subscription.Title)
	subscription.Root = strings.TrimSpace(subscription.Root)
	if subscription.LibraryID == "" {
		return Subscription{}, ValidationError("library ID is required")
	}
	// Never wrap the url.Parse error: it embeds the raw URL, which can carry
	// private-feed credentials.
	feedURL, err := normalizeSubscriptionFeedURL(subscription.FeedURL)
	if err != nil {
		return Subscription{}, err
	}
	subscription.FeedURL = feedURL

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Subscription{}, err
	}
	if err := s.subscriptionLibraryLocked(ctx, subscription.LibraryID); err != nil {
		return Subscription{}, err
	}
	if subscription.Root == "" {
		roots, err := libraryRootsSQL(ctx, s.catalogR, subscription.LibraryID)
		if err != nil {
			return Subscription{}, err
		}
		if len(roots) == 0 {
			return Subscription{}, ValidationError("subscription root folder is required because the library has no persisted roots")
		}
		subscription.Root = roots[0]
	}
	id, err := randomSubscriptionID()
	if err != nil {
		return Subscription{}, err
	}
	subscription.ID = id
	subscription.AddedAt = now.UTC()
	subscription.LastPolledAt = time.Time{}
	subscription.ETag = ""
	subscription.LastModified = ""
	if subscription.RetentionKind == "" {
		subscription.RetentionKind = RetentionKeepAll
	}
	if err := validateRetention(subscription.RetentionKind, subscription.RetentionN); err != nil {
		return Subscription{}, err
	}
	if _, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO subscription(id, library_id, feed_url, auth_ref, root, title, added_at_ms, retention_kind, retention_n)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, subscription.ID, subscription.LibraryID, subscription.FeedURL, subscription.AuthRef,
		subscription.Root, subscription.Title, subscription.AddedAt.UnixMilli(), subscription.RetentionKind, nullableRetentionN(subscription.RetentionN)); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Subscription{}, errors.New("subscription already exists for this library and feed")
		}
		return Subscription{}, err
	}
	return subscription, nil
}

// GetOrCreateSubscription resolves an existing Subscription by (Library, feed
// URL) or creates it. It is the idempotent migration/import path; unlike
// AddSubscription, an existing row is a successful no-op.
func (s *Store) GetOrCreateSubscription(ctx context.Context, subscription Subscription, now time.Time) (Subscription, bool, error) {
	subscription.Title = strings.TrimSpace(subscription.Title)
	subscription.Root = strings.TrimSpace(subscription.Root)
	if subscription.LibraryID == "" {
		return Subscription{}, false, ValidationError("library ID is required")
	}
	feedURL, err := normalizeSubscriptionFeedURL(subscription.FeedURL)
	if err != nil {
		return Subscription{}, false, err
	}
	subscription.FeedURL = feedURL

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Subscription{}, false, err
	}
	if err := s.subscriptionLibraryLocked(ctx, subscription.LibraryID); err != nil {
		return Subscription{}, false, err
	}
	existing, err := scanSubscription(s.catalogR.QueryRowContext(ctx, subscriptionColumns+` WHERE library_id = ? AND feed_url = ?`, subscription.LibraryID, subscription.FeedURL))
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrSubscriptionNotFound) {
		return Subscription{}, false, err
	}
	if subscription.Root == "" {
		roots, err := libraryRootsSQL(ctx, s.catalogR, subscription.LibraryID)
		if err != nil {
			return Subscription{}, false, err
		}
		if len(roots) == 0 {
			return Subscription{}, false, ValidationError("subscription root folder is required because the library has no persisted roots")
		}
		subscription.Root = roots[0]
	}
	id, err := randomSubscriptionID()
	if err != nil {
		return Subscription{}, false, err
	}
	subscription.ID = id
	subscription.AddedAt = now.UTC()
	subscription.LastPolledAt = time.Time{}
	subscription.ETag = ""
	subscription.LastModified = ""
	if subscription.RetentionKind == "" {
		subscription.RetentionKind = RetentionKeepAll
	}
	if err := validateRetention(subscription.RetentionKind, subscription.RetentionN); err != nil {
		return Subscription{}, false, err
	}
	if _, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO subscription(id, library_id, feed_url, auth_ref, root, title, added_at_ms, retention_kind, retention_n)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, subscription.ID, subscription.LibraryID, subscription.FeedURL, subscription.AuthRef,
		subscription.Root, subscription.Title, subscription.AddedAt.UnixMilli(), subscription.RetentionKind, nullableRetentionN(subscription.RetentionN)); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			existing, readErr := scanSubscription(s.catalogR.QueryRowContext(ctx, subscriptionColumns+` WHERE library_id = ? AND feed_url = ?`, subscription.LibraryID, subscription.FeedURL))
			if readErr == nil {
				return existing, false, nil
			}
			return Subscription{}, false, readErr
		}
		return Subscription{}, false, err
	}
	return subscription, true, nil
}

// subscriptionLibraryLocked admits a new Subscription only into an existing
// plaintext Library; see ErrEncryptedLibrarySubscription.
func (s *Store) subscriptionLibraryLocked(ctx context.Context, libraryID LibraryID) error {
	var encrypted int
	err := s.catalogR.QueryRowContext(ctx, `SELECT encrypted FROM library WHERE id = ?`, libraryID).Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return ValidationError("library not found")
	}
	if err != nil {
		return err
	}
	if encrypted != 0 {
		return ErrEncryptedLibrarySubscription
	}
	return nil
}

// SubscriptionPrivacyCheck reports ErrEncryptedLibrarySubscription when the
// Subscription belongs to an Encrypted Library, so the poller can stop before
// fetching the feed or downloading an enclosure it could not record.
func (s *Store) SubscriptionPrivacyCheck(ctx context.Context, id SubscriptionID) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return s.refuseEncryptedSubscriptionLocked(ctx, id)
}

// refuseEncryptedSubscriptionLocked refuses an entry or title write for a
// Subscription that an Encrypted Library left in the shared catalog before
// this refusal existed, so no new GUID, path or title of it lands there. An
// unknown Subscription passes through to the caller's own not-found handling.
func (s *Store) refuseEncryptedSubscriptionLocked(ctx context.Context, id SubscriptionID) error {
	var encrypted int
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT l.encrypted FROM subscription sub JOIN library l ON l.id = sub.library_id WHERE sub.id = ?
	`, id).Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if encrypted != 0 {
		return ErrEncryptedLibrarySubscription
	}
	return nil
}

func normalizeSubscriptionFeedURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", ValidationError("feed URL must be an absolute http(s) URL")
	}
	return parsed.String(), nil
}

func (s *Store) SetSubscriptionRetention(ctx context.Context, id SubscriptionID, kind RetentionKind, n *int) (Subscription, error) {
	if id == "" {
		return Subscription{}, errors.New("subscription ID is required")
	}
	if err := validateRetention(kind, n); err != nil {
		return Subscription{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Subscription{}, err
	}
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE subscription SET retention_kind = ?, retention_n = ? WHERE id = ?
	`, kind, nullableRetentionN(n), id)
	if err != nil {
		return Subscription{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Subscription{}, err
	}
	if affected == 0 {
		return Subscription{}, ErrSubscriptionNotFound
	}
	return scanSubscription(s.catalogR.QueryRowContext(ctx, subscriptionColumns+` WHERE id = ?`, id))
}

// SetSubscriptionAuthRef records the secure-store KEY for a private feed's
// Authorization header (invariant #5). ref is the secretFiles entry name, never
// the credential itself; "" clears it (an anonymous feed). The caller writes the
// literal secret to the secure store under this ref BEFORE persisting it here,
// so a stored ref always resolves. Server-internal: the graph layer owns the
// secure-store write and supplies the ref.
func (s *Store) SetSubscriptionAuthRef(ctx context.Context, id SubscriptionID, ref string) error {
	if id == "" {
		return errors.New("subscription ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `UPDATE subscription SET auth_ref = ? WHERE id = ?`, ref, id)
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

// Subscription returns one Subscription by ID.
func (s *Store) Subscription(ctx context.Context, id SubscriptionID) (Subscription, error) {
	if id == "" {
		return Subscription{}, errors.New("subscription ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Subscription{}, err
	}
	row := s.catalogR.QueryRowContext(ctx, subscriptionColumns+` WHERE id = ?`, id)
	return scanSubscription(row)
}

// Subscriptions returns the Library's Subscriptions ordered by added time.
func (s *Store) Subscriptions(ctx context.Context, libraryID LibraryID) ([]Subscription, error) {
	if libraryID == "" {
		return nil, ValidationError("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return s.querySubscriptions(ctx, subscriptionColumns+` WHERE library_id = ? ORDER BY added_at_ms, id`, libraryID)
}

// AllSubscriptions returns every Subscription ordered by ID. Server-internal
// (the poll job): client reads go through Subscriptions with the caller's
// Library visibility already checked.
func (s *Store) AllSubscriptions(ctx context.Context) ([]Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return s.querySubscriptions(ctx, subscriptionColumns+` ORDER BY id`)
}

// RemoveSubscription deletes a Subscription (and its seen-entry bookkeeping,
// by cascade) and returns the removed row. Already-downloaded Media stays —
// removing a Subscription stops future polls, never destroys the archive.
func (s *Store) RemoveSubscription(ctx context.Context, id SubscriptionID) (Subscription, error) {
	if id == "" {
		return Subscription{}, errors.New("subscription ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Subscription{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Subscription{}, err
	}
	defer tx.Rollback()
	removed, err := scanSubscription(tx.QueryRowContext(ctx, subscriptionColumns+` WHERE id = ?`, id))
	if err != nil {
		return Subscription{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM subscription WHERE id = ?`, id); err != nil {
		return Subscription{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM server_config WHERE key = ?`, subscriptionPollStateKey(id)); err != nil {
		return Subscription{}, err
	}
	if err := tx.Commit(); err != nil {
		return Subscription{}, err
	}
	return removed, nil
}

// maxSubscriptionTitleRunes caps a Subscription title taken from imported or
// fetched data.
const maxSubscriptionTitleRunes = 256

// SubscriptionTitle returns title trimmed and capped for use as a Subscription
// title, or "" when it is a URL, the feed URL or the feed's host. OPML writers
// fill outline text with the feed URL or host when a feed has no title, and a
// feed URL can carry a bearer token that viewers would then see as the title.
func SubscriptionTitle(title, feedURL string) string {
	title = strings.TrimSpace(title)
	if title == "" || title == strings.TrimSpace(feedURL) {
		return ""
	}
	if containsURL(title) {
		return ""
	}
	if feed, err := url.Parse(feedURL); err == nil && feed.Hostname() != "" && strings.EqualFold(title, feed.Hostname()) {
		return ""
	}
	if runes := []rune(title); len(runes) > maxSubscriptionTitleRunes {
		title = strings.TrimSpace(string(runes[:maxSubscriptionTitleRunes]))
	}
	return title
}

// urlPattern finds a URL anywhere in a title, whatever punctuation or
// brackets surround it: a scheme separator, a scheme-relative //host, or a
// dotted host (any script, optional root dot) or bracketed IPv6 host
// (optional port) followed by a path or query.
var urlPattern = regexp.MustCompile(`(?i)://|//[^\s/]|[\p{L}\p{M}\p{N}_-]+([.。．｡][\p{L}\p{M}\p{N}_-]+)+[.。．｡]?(:[0-9]+)?(/|\?\S)|\[[0-9a-f.]*:[0-9a-f:.]*\](:[0-9]+)?(/|\?\S)`)

// containsURL reports whether title contains a URL, with or without a scheme,
// so a signed feed URL inside prose never becomes a visible title.
// containsURL reports whether title holds anything that reads as a URL. It
// matches on the NFKC form with format characters and variation selectors
// removed, so compatibility and IDNA-ignored variants of URL syntax are caught
// as one class instead of one code point at a time.
func containsURL(title string) bool {
	folded := strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Variation_Selector, r) {
			return -1
		}
		return r
	}, norm.NFKC.String(title))
	return urlPattern.MatchString(folded)
}

// FillSubscriptionTitle sets the Subscription's title only while it is empty,
// so a poll can name a feed imported without a title but never overwrites
// one. title passes through SubscriptionTitle first; a title it rejects is a
// no-op. Reports whether the title was set.
func (s *Store) FillSubscriptionTitle(ctx context.Context, id SubscriptionID, title string) (bool, error) {
	if id == "" {
		return false, errors.New("subscription ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	if err := s.refuseEncryptedSubscriptionLocked(ctx, id); err != nil {
		return false, err
	}
	var feedURL string
	if err := s.catalogW.QueryRowContext(ctx, `SELECT feed_url FROM subscription WHERE id = ?`, id).Scan(&feedURL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrSubscriptionNotFound
		}
		return false, err
	}
	title = SubscriptionTitle(title, feedURL)
	if title == "" {
		return false, nil
	}
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE subscription SET title = ? WHERE id = ? AND COALESCE(title, '') = ''
	`, title, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// MarkSubscriptionPolled records a completed poll: the poll time and the
// conditional-GET validators the next poll sends.
func (s *Store) MarkSubscriptionPolled(ctx context.Context, id SubscriptionID, etag, lastModified string, now time.Time) error {
	if id == "" {
		return errors.New("subscription ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE subscription SET last_polled_at_ms = ?, etag = ?, last_modified = ? WHERE id = ?
	`, now.UTC().UnixMilli(), etag, lastModified, id)
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

// SubscriptionSeenGUIDs returns the set of feed-entry GUIDs already ingested
// for the Subscription — the new-entry detection key.
func (s *Store) SubscriptionSeenGUIDs(ctx context.Context, id SubscriptionID) (map[string]bool, error) {
	if id == "" {
		return nil, errors.New("subscription ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT guid FROM subscription_seen_entry
		WHERE subscription_id = ? AND (path <> '' OR seen_at_ms <> 0)
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var guid string
		if err := rows.Scan(&guid); err != nil {
			return nil, err
		}
		seen[guid] = true
	}
	return seen, rows.Err()
}

// MarkSubscriptionEntrySeen records one ingested feed-entry GUID. The path
// may be empty while a durable staged download awaits publication. Re-marking
// an already-seen GUID is a no-op, except that a back-catalogue marker can
// gain a download receipt.
func (s *Store) MarkSubscriptionEntrySeen(ctx context.Context, id SubscriptionID, guid string, path string, now time.Time) error {
	return s.markSubscriptionEntrySeen(ctx, id, guid, path, now, nil)
}

// MarkSubscriptionFeedEntrySeen records the entry's zero-based position in the
// fetched feed in the durable receipt, which may await publication.
func (s *Store) MarkSubscriptionFeedEntrySeen(ctx context.Context, id SubscriptionID, guid string, path string, now time.Time, position int) error {
	return s.markSubscriptionEntrySeen(ctx, id, guid, path, now, &position)
}

func (s *Store) markSubscriptionEntrySeen(ctx context.Context, id SubscriptionID, guid string, path string, now time.Time, position *int) error {
	if id == "" {
		return errors.New("subscription ID is required")
	}
	if strings.TrimSpace(guid) == "" {
		return errors.New("entry GUID is required")
	}
	path = strings.TrimSpace(path)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if err := s.refuseEncryptedSubscriptionLocked(ctx, id); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO subscription_seen_entry(subscription_id, guid, seen_at_ms, path, feed_position)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(subscription_id, guid) DO UPDATE SET seen_at_ms = excluded.seen_at_ms, path = excluded.path,
		  feed_position = excluded.feed_position
		WHERE subscription_seen_entry.path = '' AND subscription_seen_entry.seen_at_ms = 0
	`, id, guid, now.UTC().UnixMilli(), path, position)
	return err
}

// PublishSubscriptionEntry records the published path in both the receipt and
// the optional catalogue row. It holds the catalogue writer during publish
// so a concurrent scan import sees the binding when it writes the Media.
func (s *Store) PublishSubscriptionEntry(ctx context.Context, id SubscriptionID, guid string, publish func() (string, error)) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if err := s.refuseEncryptedSubscriptionLocked(ctx, id); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	path, err := publish()
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE subscription_seen_entry SET path = ? WHERE subscription_id = ? AND guid = ?
	`, path, id, guid)
	if err != nil {
		return err
	}
	if err := requireOneRow(result, ValidationError("subscription receipt not found")); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE subscription_entry SET download_state = 'queued', download_path = ?
		WHERE subscription_id = ? AND guid = ? AND download_path <> ?
	`, path, id, guid, path); err != nil {
		return err
	}
	return tx.Commit()
}

// AliasSubscriptionEntryGUID adds a GUID for an existing receipt. It keeps
// the original receipt and copies the path, timestamp and feed position so both GUIDs
// refer to the same file and retention counts it once.
func (s *Store) AliasSubscriptionEntryGUID(ctx context.Context, id SubscriptionID, existingGUID, guid string) error {
	if id == "" {
		return errors.New("subscription ID is required")
	}
	if strings.TrimSpace(existingGUID) == "" || strings.TrimSpace(guid) == "" {
		return errors.New("entry GUID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if err := s.refuseEncryptedSubscriptionLocked(ctx, id); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO subscription_seen_entry(subscription_id, guid, seen_at_ms, path, feed_position)
		SELECT subscription_id, ?, seen_at_ms, path, feed_position FROM subscription_seen_entry
		WHERE subscription_id = ? AND guid = ?
		ON CONFLICT(subscription_id, guid) DO UPDATE SET seen_at_ms = excluded.seen_at_ms, path = excluded.path,
		  feed_position = excluded.feed_position
		WHERE subscription_seen_entry.path = '' AND subscription_seen_entry.seen_at_ms = 0
	`, guid, id, existingGUID)
	return err
}

// AssociateSubscriptionEntryMedia repairs feed identity for archived feed
// Media that existed before the Subscription poller recorded a seen entry. The
// caller supplies the Media explicitly; this method never resolves by
// duplicate-friendly GUID alone.
func (s *Store) AssociateSubscriptionEntryMedia(ctx context.Context, id SubscriptionID, guid string, mediaID MediaID, now time.Time) (SubscriptionEntryAssociation, error) {
	// The GUID is the feed's exact key: it is matched byte for byte against
	// the poller's receipts, never trimmed or otherwise normalized.
	if id == "" {
		return SubscriptionEntryAssociation{}, errors.New("subscription ID is required")
	}
	if strings.TrimSpace(guid) == "" {
		return SubscriptionEntryAssociation{}, errors.New("entry GUID is required")
	}
	if mediaID == "" {
		return SubscriptionEntryAssociation{}, errors.New("media ID is required")
	}
	s.mu.RLock()
	if err := s.guardLocked(); err != nil {
		s.mu.RUnlock()
		return SubscriptionEntryAssociation{}, err
	}
	if err := s.refuseEncryptedSubscriptionLocked(ctx, id); err != nil {
		s.mu.RUnlock()
		return SubscriptionEntryAssociation{}, err
	}
	subscription, err := scanSubscription(s.catalogR.QueryRowContext(ctx, subscriptionColumns+` WHERE id = ?`, id))
	if err != nil {
		s.mu.RUnlock()
		return SubscriptionEntryAssociation{}, err
	}
	path, err := s.subscriptionAssociationPathLocked(ctx, subscription, mediaID)
	if err != nil {
		s.mu.RUnlock()
		return SubscriptionEntryAssociation{}, err
	}
	if err := s.upsertSubscriptionEntryAssociationLocked(ctx, id, guid, mediaID, path, now); err != nil {
		s.mu.RUnlock()
		return SubscriptionEntryAssociation{}, err
	}
	s.mu.RUnlock()

	replayed, err := s.ReplayItemStateImports(ctx, now)
	if err != nil {
		return SubscriptionEntryAssociation{}, err
	}
	return SubscriptionEntryAssociation{
		SubscriptionID:         id,
		GUID:                   guid,
		MediaID:                mediaID,
		Path:                   path,
		ReplayedItemStateSweep: replayed,
	}, nil
}

func (s *Store) subscriptionAssociationPathLocked(ctx context.Context, subscription Subscription, mediaID MediaID) (string, error) {
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT mf.path
		FROM media_file mf
		JOIN media m ON m.id = mf.media_id
		WHERE mf.media_id = ?
		  AND m.library_id = ?
		  AND m.trashed_at_ms IS NULL
		  AND mf.missing_at_ms IS NULL
		ORDER BY mf.version_label, mf.part_ordinal, mf.path
	`, mediaID, subscription.LibraryID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	paths := []string{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return "", err
		}
		if pathWithinRoot(subscription.Root, path) {
			paths = append(paths, path)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	switch len(paths) {
	case 0:
		return "", ValidationError("media is not a live file under the subscription root")
	case 1:
		return paths[0], nil
	default:
		return "", ValidationError("media has multiple live files under the subscription root")
	}
}

func (s *Store) upsertSubscriptionEntryAssociationLocked(ctx context.Context, id SubscriptionID, guid string, mediaID MediaID, path string, now time.Time) error {
	var existing string
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT path FROM subscription_seen_entry WHERE subscription_id = ? AND guid = ?
	`, id, guid).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		existing = strings.TrimSpace(existing)
		if existing != "" && existing != path {
			if err := s.recordSubscriptionEntryCollisionLocked(ctx, id, guid, existing, path, mediaID, now); err != nil {
				return err
			}
			return ErrSubscriptionEntryCollision
		}
		_, err = s.catalogW.ExecContext(ctx, `
			UPDATE subscription_seen_entry
			SET path = ?, seen_at_ms = ?
			WHERE subscription_id = ? AND guid = ?
		`, path, now.UTC().UnixMilli(), id, guid)
	} else {
		_, err = s.catalogW.ExecContext(ctx, `
			INSERT INTO subscription_seen_entry(subscription_id, guid, seen_at_ms, path)
			VALUES (?, ?, ?, ?)
		`, id, guid, now.UTC().UnixMilli(), path)
	}
	if err != nil {
		return err
	}
	_, err = s.catalogW.ExecContext(ctx, `
		INSERT OR IGNORE INTO media_external_id(media_id, scheme, value)
		VALUES (?, ?, ?)
	`, mediaID, subscriptionFeedGUIDScheme, feedGUIDExternalID(guid))
	return err
}

// SubscriptionEntryCollision is one durable review item: a feed entry GUID
// that already names BoundPath was offered for a different file.
type SubscriptionEntryCollision struct {
	SubscriptionID  SubscriptionID
	GUID            string
	BoundPath       string
	IncomingPath    string
	IncomingMediaID MediaID
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	Observations    int
}

// recordSubscriptionEntryCollisionLocked keeps a same-GUID, different-file
// offer for review. A repeat offer of the same file refreshes the one row.
func (s *Store) recordSubscriptionEntryCollisionLocked(ctx context.Context, id SubscriptionID, guid, boundPath, incomingPath string, mediaID MediaID, now time.Time) error {
	at := now.UTC().UnixMilli()
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO subscription_entry_collision(subscription_id, guid, bound_path, incoming_path, incoming_media_id, first_seen_at_ms, last_seen_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(subscription_id, guid, incoming_path) DO UPDATE SET
			bound_path = excluded.bound_path,
			incoming_media_id = excluded.incoming_media_id,
			last_seen_at_ms = excluded.last_seen_at_ms,
			observations = subscription_entry_collision.observations + 1
	`, id, guid, boundPath, incomingPath, mediaID, at, at)
	return err
}

// SubscriptionEntryCollisions lists one Subscription's GUID collisions, oldest
// first, for operator review.
func (s *Store) SubscriptionEntryCollisions(ctx context.Context, id SubscriptionID) ([]SubscriptionEntryCollision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT subscription_id, guid, bound_path, incoming_path, incoming_media_id,
		       first_seen_at_ms, last_seen_at_ms, observations
		FROM subscription_entry_collision WHERE subscription_id = ?
		ORDER BY first_seen_at_ms, guid, incoming_path
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubscriptionEntryCollision
	for rows.Next() {
		var c SubscriptionEntryCollision
		var first, last int64
		if err := rows.Scan(&c.SubscriptionID, &c.GUID, &c.BoundPath, &c.IncomingPath, &c.IncomingMediaID,
			&first, &last, &c.Observations); err != nil {
			return nil, err
		}
		c.FirstSeenAt = time.UnixMilli(first).UTC()
		c.LastSeenAt = time.UnixMilli(last).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

func pathWithinRoot(root, path string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	path = filepath.Clean(strings.TrimSpace(path))
	if root == "." || path == "." {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

const subscriptionColumns = `SELECT id, library_id, feed_url, auth_ref, root, title, added_at_ms, last_polled_at_ms, etag, last_modified, retention_kind, retention_n, ` + subscriptionHealthColumns + ` FROM subscription`

func (s *Store) querySubscriptions(ctx context.Context, query string, args ...any) ([]Subscription, error) {
	rows, err := s.catalogR.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	subscriptions := []Subscription{}
	for rows.Next() {
		subscription, err := scanSubscriptionRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		subscriptions = append(subscriptions, subscription)
	}
	return subscriptions, rows.Err()
}

func scanSubscription(row *sql.Row) (Subscription, error) {
	subscription, err := scanSubscriptionRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Subscription{}, ErrSubscriptionNotFound
	}
	return subscription, err
}

func scanSubscriptionRow(scan func(...any) error) (Subscription, error) {
	var subscription Subscription
	var addedMS, polledMS int64
	var retentionN sql.NullInt64
	var health subscriptionHealthRow
	dest := []any{&subscription.ID, &subscription.LibraryID, &subscription.FeedURL, &subscription.AuthRef,
		&subscription.Root, &subscription.Title, &addedMS, &polledMS,
		&subscription.ETag, &subscription.LastModified, &subscription.RetentionKind, &retentionN}
	if err := scan(append(dest, health.dest()...)...); err != nil {
		return Subscription{}, err
	}
	subscription.Health = health.health()
	if subscription.RetentionKind == "" {
		subscription.RetentionKind = RetentionKeepAll
	}
	if retentionN.Valid {
		n := int(retentionN.Int64)
		subscription.RetentionN = &n
	}
	subscription.AddedAt = time.UnixMilli(addedMS).UTC()
	if polledMS > 0 {
		subscription.LastPolledAt = time.UnixMilli(polledMS).UTC()
	}
	return subscription, nil
}

func validateRetention(kind RetentionKind, n *int) error {
	switch kind {
	case RetentionKeepAll, RetentionDeleteAfterPlayed:
		return nil
	case RetentionKeepLastN:
		// At least 1: keep-last-0 would silently trash every non-favorited
		// item on the next sweep — "delete everything" is not a retention
		// policy anyone sets by intent.
		if n == nil || *n < 1 {
			return ValidationError("retention count must be at least 1")
		}
		return nil
	default:
		return ValidationError("retention kind is invalid")
	}
}

func nullableRetentionN(n *int) any {
	if n == nil {
		return nil
	}
	return *n
}

func randomSubscriptionID() (SubscriptionID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return SubscriptionID("subscription-" + hex.EncodeToString(raw[:])), nil
}

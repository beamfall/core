package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// ErrSubscriptionNotFound means no Subscription exists with the given ID.
var ErrSubscriptionNotFound = errors.New("subscription not found")

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
	var count int
	if err := s.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM library WHERE id = ?`, subscription.LibraryID).Scan(&count); err != nil {
		return Subscription{}, err
	}
	if count == 0 {
		return Subscription{}, ValidationError("library not found")
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
	var count int
	if err := s.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM library WHERE id = ?`, subscription.LibraryID).Scan(&count); err != nil {
		return Subscription{}, false, err
	}
	if count == 0 {
		return Subscription{}, false, ValidationError("library not found")
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
	removed, err := scanSubscription(s.catalogR.QueryRowContext(ctx, subscriptionColumns+` WHERE id = ?`, id))
	if err != nil {
		return Subscription{}, err
	}
	if _, err := s.catalogW.ExecContext(ctx, `DELETE FROM subscription WHERE id = ?`, id); err != nil {
		return Subscription{}, err
	}
	return removed, nil
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
	rows, err := s.catalogR.QueryContext(ctx, `SELECT guid FROM subscription_seen_entry WHERE subscription_id = ?`, id)
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

// MarkSubscriptionEntrySeen records one ingested feed-entry GUID and the
// absolute path where its enclosure landed. Re-marking an already-seen GUID
// is a no-op, including path preservation.
func (s *Store) MarkSubscriptionEntrySeen(ctx context.Context, id SubscriptionID, guid string, path string, now time.Time) error {
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
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO subscription_seen_entry(subscription_id, guid, seen_at_ms, path)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(subscription_id, guid) DO NOTHING
	`, id, guid, now.UTC().UnixMilli(), path)
	return err
}

// AssociateSubscriptionEntryMedia repairs feed identity for archived feed
// Media that existed before the Subscription poller recorded a seen entry. The
// caller supplies the Media explicitly; this method never resolves by
// duplicate-friendly GUID alone.
func (s *Store) AssociateSubscriptionEntryMedia(ctx context.Context, id SubscriptionID, guid string, mediaID MediaID, now time.Time) (SubscriptionEntryAssociation, error) {
	guid = strings.TrimSpace(guid)
	if id == "" {
		return SubscriptionEntryAssociation{}, errors.New("subscription ID is required")
	}
	if guid == "" {
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
			return ValidationError("entry GUID is already associated with a different media file")
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
	`, mediaID, subscriptionFeedGUIDScheme, guid)
	return err
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

const subscriptionColumns = `SELECT id, library_id, feed_url, auth_ref, root, title, added_at_ms, last_polled_at_ms, etag, last_modified, retention_kind, retention_n FROM subscription`

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
	if err := scan(&subscription.ID, &subscription.LibraryID, &subscription.FeedURL, &subscription.AuthRef,
		&subscription.Root, &subscription.Title, &addedMS, &polledMS,
		&subscription.ETag, &subscription.LastModified, &subscription.RetentionKind, &retentionN); err != nil {
		return Subscription{}, err
	}
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

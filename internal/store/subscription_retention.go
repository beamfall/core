package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) SetFollow(ctx context.Context, profileID ProfileID, subscriptionID SubscriptionID, on bool, now time.Time) error {
	if profileID == "" || subscriptionID == "" {
		return ValidationError("profile ID and subscription ID are required")
	}
	profile, err := s.Profile(ctx, profileID)
	if err != nil {
		return err
	}
	if profile.Kind == ProfileManaged {
		return ValidationError("managed profiles cannot follow subscriptions")
	}
	if _, err := s.Subscription(ctx, subscriptionID); err != nil {
		if errors.Is(err, ErrSubscriptionNotFound) {
			return ValidationError("subscription not found")
		}
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if on {
		_, err = s.eventsW.ExecContext(ctx, `
			INSERT INTO follow(profile_id, subscription_id, followed_at_ms)
			VALUES (?, ?, ?)
			ON CONFLICT(profile_id, subscription_id) DO UPDATE SET
			  followed_at_ms = excluded.followed_at_ms
		`, profileID, subscriptionID, timeToMillis(now.UTC()))
		return err
	}
	_, err = s.eventsW.ExecContext(ctx, `DELETE FROM follow WHERE profile_id = ? AND subscription_id = ?`, profileID, subscriptionID)
	return err
}

func (s *Store) SubscriptionFollowed(ctx context.Context, profileID ProfileID, subscriptionID SubscriptionID) (bool, error) {
	if profileID == "" || subscriptionID == "" {
		return false, ValidationError("profile ID and subscription ID are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var one int
	err := s.eventsR.QueryRowContext(ctx, `
		SELECT 1 FROM follow WHERE profile_id = ? AND subscription_id = ?
	`, profileID, subscriptionID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

const liveSubscriptionReceipts = `
	FROM subscription_seen_entry se
	JOIN subscription sub ON sub.id = se.subscription_id
	JOIN media_file mf ON mf.path = se.path AND mf.missing_at_ms IS NULL
	JOIN media m ON m.id = mf.media_id AND m.library_id = sub.library_id AND m.trashed_at_ms IS NULL
	WHERE se.path <> ''`

const receiptGUIDMayBeURL = `(lower(se.guid) LIKE 'http:%' OR lower(se.guid) LIKE 'https:%')`

func (s *Store) ReconcileSubscriptionSeenEntries(ctx context.Context) (int, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, 0, err
	}
	var skippedBlank int
	if err := s.catalogR.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM subscription_seen_entry
		WHERE path = '' AND seen_at_ms <> 0
	`).Scan(&skippedBlank); err != nil {
		return 0, 0, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO media_external_id(media_id, scheme, value)
		SELECT mf.media_id, ?, se.guid`+liveSubscriptionReceipts+` AND NOT `+receiptGUIDMayBeURL, subscriptionFeedGUIDScheme)
	if err != nil {
		return 0, 0, err
	}
	urlIDs, err := reconcileURLGUIDs(ctx, tx)
	if err != nil {
		return 0, 0, err
	}
	if err := repairLegacyFeedCatalogue(ctx, tx); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	if _, err := s.ReplayItemStateImports(ctx, time.Now().UTC()); err != nil {
		return 0, 0, err
	}
	return int(affected + urlIDs), skippedBlank, nil
}

func reconcileURLGUIDs(ctx context.Context, tx *sql.Tx) (int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT mf.media_id, se.subscription_id, se.guid`+liveSubscriptionReceipts+` AND `+receiptGUIDMayBeURL)
	if err != nil {
		return 0, err
	}
	identities, err := scanSubscriptionGUIDs(rows)
	if err != nil {
		return 0, err
	}
	var affected int64
	for _, entry := range identities {
		result, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO media_external_id(media_id, scheme, value) VALUES (?, ?, ?)
		`, entry.mediaID, subscriptionFeedGUIDScheme, feedGUIDExternalID(entry.guid))
		if err != nil {
			return 0, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		affected += n
		if err := repairLegacyFeedIdentity(ctx, tx, entry.subscriptionID, entry.guid, entry.mediaID); err != nil {
			return 0, err
		}
	}
	return affected, nil
}

func repairLegacyFeedCatalogue(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT se.media_id, se.subscription_id, se.guid
		FROM subscription_entry se JOIN media m ON m.id = se.media_id
		WHERE (`+receiptGUIDMayBeURL+` OR instr(se.guid, '://') > 0 OR se.guid LIKE '//%')
		  AND (m.title = se.guid OR EXISTS (
		    SELECT 1 FROM media_external_id e WHERE e.media_id = se.media_id
		      AND ((e.scheme = ? AND e.value = se.guid) OR (e.scheme = ? AND e.value = se.subscription_id || char(0) || se.guid))
		  ))
	`, subscriptionFeedGUIDScheme, ProvisionalExternalIDScheme)
	if err != nil {
		return err
	}
	identities, err := scanSubscriptionGUIDs(rows)
	if err != nil {
		return err
	}
	for _, entry := range identities {
		if err := repairLegacyFeedIdentity(ctx, tx, entry.subscriptionID, entry.guid, entry.mediaID); err != nil {
			return err
		}
	}
	return nil
}

type subscriptionGUID struct {
	mediaID        MediaID
	subscriptionID SubscriptionID
	guid           string
}

func scanSubscriptionGUIDs(rows *sql.Rows) ([]subscriptionGUID, error) {
	defer rows.Close()
	var entries []subscriptionGUID
	for rows.Next() {
		var entry subscriptionGUID
		if err := rows.Scan(&entry.mediaID, &entry.subscriptionID, &entry.guid); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// ApplySubscriptionRetentionPolicies runs the autonomous retention sweep:
// every Media a Subscription's retention policy condemns is routed through
// TrashMedia (never hard-deleted). With trash disabled (hard invariant 6:
// autonomous destruction is sanctioned only while trash is on) the whole
// sweep refuses with ErrTrashDisabledConfirmationRequired, skipped is true,
// the skip counter increments once per sweep, and no TrashMedia call is made.
// Interactive deletes and the expired-trash purge are not gated here.
func (s *Store) ApplySubscriptionRetentionPolicies(ctx context.Context) (trashed int, skipped bool, err error) {
	if s.trashDisabled {
		s.trashDisabledSweepSkips.Add(1)
		return 0, true, ErrTrashDisabledConfirmationRequired
	}
	subscriptions, err := s.AllSubscriptions(ctx)
	if err != nil {
		return 0, false, err
	}
	for _, subscription := range subscriptions {
		var ids []MediaID
		switch subscription.RetentionKind {
		case RetentionKeepAll, "":
			continue
		case RetentionKeepLastN:
			ids, err = s.keepLastNCandidates(ctx, subscription)
		case RetentionDeleteAfterPlayed:
			ids, err = s.deleteAfterPlayedCandidates(ctx, subscription)
		default:
			continue
		}
		if err != nil {
			return trashed, false, err
		}
		filter := LibraryFilter{AllowedIDs: []LibraryID{subscription.LibraryID}, Revealed: true}
		for _, id := range ids {
			if err := s.TrashMedia(ctx, id, filter); err != nil {
				return trashed, false, err
			}
			trashed++
		}
	}
	return trashed, false, nil
}

// TrashDisabledSweepSkips reports how many retention sweeps were skipped
// because trash is disabled.
func (s *Store) TrashDisabledSweepSkips() int64 {
	return s.trashDisabledSweepSkips.Load()
}

func (s *Store) keepLastNCandidates(ctx context.Context, subscription Subscription) ([]MediaID, error) {
	keep := 0
	if subscription.RetentionN != nil {
		keep = *subscription.RetentionN
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT DISTINCT m.id, se.seen_at_ms
		FROM subscription_seen_entry se
		JOIN media_file mf ON mf.path = se.path AND mf.missing_at_ms IS NULL
		JOIN media m ON m.id = mf.media_id AND m.library_id = ? AND m.trashed_at_ms IS NULL
		WHERE se.subscription_id = ?
		  AND se.path <> ''
		ORDER BY se.seen_at_ms DESC, m.id
	`, subscription.LibraryID, subscription.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var trash []MediaID
	index := 0
	for rows.Next() {
		var id MediaID
		var seen int64
		if err := rows.Scan(&id, &seen); err != nil {
			return nil, err
		}
		favorited, err := s.mediaFavoritedByAny(ctx, id)
		if err != nil {
			return nil, err
		}
		if favorited {
			continue
		}
		if index >= keep {
			trash = append(trash, id)
		}
		index++
	}
	return trash, rows.Err()
}

func (s *Store) deleteAfterPlayedCandidates(ctx context.Context, subscription Subscription) ([]MediaID, error) {
	followers, err := s.subscriptionFollowers(ctx, subscription.ID)
	if err != nil || len(followers) == 0 {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT DISTINCT m.id
		FROM subscription_seen_entry se
		JOIN media_file mf ON mf.path = se.path AND mf.missing_at_ms IS NULL
		JOIN media m ON m.id = mf.media_id AND m.library_id = ? AND m.trashed_at_ms IS NULL
		WHERE se.subscription_id = ?
		  AND se.path <> ''
		ORDER BY se.seen_at_ms, m.id
	`, subscription.LibraryID, subscription.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var trash []MediaID
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		favorited, err := s.mediaFavoritedByAny(ctx, id)
		if err != nil {
			return nil, err
		}
		if favorited {
			continue
		}
		playedByAll := true
		for _, profileID := range followers {
			played, err := s.profilePlayed(ctx, profileID, id)
			if err != nil {
				return nil, err
			}
			if !played {
				playedByAll = false
				break
			}
		}
		if playedByAll {
			trash = append(trash, id)
		}
	}
	return trash, rows.Err()
}

func (s *Store) mediaFavoritedByAny(ctx context.Context, mediaID MediaID) (bool, error) {
	var one int
	err := s.eventsR.QueryRowContext(ctx, `
		SELECT 1 FROM media_favorite WHERE media_id = ? LIMIT 1
	`, mediaID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) subscriptionFollowers(ctx context.Context, subscriptionID SubscriptionID) ([]ProfileID, error) {
	rows, err := s.eventsR.QueryContext(ctx, `SELECT profile_id FROM follow WHERE subscription_id = ? ORDER BY profile_id`, subscriptionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var followers []ProfileID
	for rows.Next() {
		var id ProfileID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		followers = append(followers, id)
	}
	return followers, rows.Err()
}

func (s *Store) profilePlayed(ctx context.Context, profileID ProfileID, mediaID MediaID) (bool, error) {
	var one int
	err := s.eventsR.QueryRowContext(ctx, `
		SELECT 1 FROM playback_progress
		WHERE profile_id = ? AND media_id = ? AND play_count >= 1
	`, profileID, mediaID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

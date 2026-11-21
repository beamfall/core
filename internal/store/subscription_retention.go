package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
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

func (s *Store) ReconcileSubscriptionSeenEntries(ctx context.Context) (int, int, error) {
	affected, skippedBlank, err := s.reconcileSubscriptionSeenPaths(ctx)
	if err != nil {
		return 0, 0, err
	}
	if _, err := s.ReplayItemStateImports(ctx, time.Now().UTC()); err != nil {
		return 0, 0, err
	}
	return affected, skippedBlank, nil
}

func (s *Store) reconcileSubscriptionSeenPaths(ctx context.Context) (int, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, 0, err
	}
	var skippedBlank int
	if err := s.catalogR.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM subscription_seen_entry
		WHERE path = ''
	`).Scan(&skippedBlank); err != nil {
		return 0, 0, err
	}
	result, err := s.catalogW.ExecContext(ctx, `
		INSERT OR IGNORE INTO media_external_id(media_id, scheme, value)
		SELECT mf.media_id, ?, se.guid
		FROM subscription_seen_entry se
		JOIN subscription sub ON sub.id = se.subscription_id
		JOIN media_file mf ON mf.path = se.path AND mf.missing_at_ms IS NULL
		JOIN media m ON m.id = mf.media_id AND m.library_id = sub.library_id AND m.trashed_at_ms IS NULL
		WHERE se.path <> ''
	`, subscriptionFeedGUIDScheme)
	if err != nil {
		return 0, 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	return int(affected), skippedBlank, nil
}

// ApplySubscriptionRetentionPolicies moves eligible Media to recoverable Trash.
// The sweep refuses while Trash is disabled and counts one skip per sweep.
func (s *Store) ApplySubscriptionRetentionPolicies(ctx context.Context) (trashed int, skipped bool, err error) {
	if s.trashDisabled {
		s.trashDisabledSweepSkips.Add(1)
		return 0, true, ErrTrashDisabledConfirmationRequired
	}
	subscriptions, err := s.retentionSubscriptions(ctx)
	if err != nil {
		return 0, false, err
	}
	// Bound the number of writes while holding the database writers. Readers
	// use WAL connections throughout, and other writers can run between batches.
	const batchSize = 16
	for _, subscription := range subscriptions {
		ids, err := s.planSubscriptionRetention(ctx, subscription)
		if err != nil {
			return trashed, false, err
		}
		for batch := range slices.Chunk(ids, batchSize) {
			moved, err := s.trashSubscriptionRetentionBatch(ctx, subscription.ID, batch)
			trashed += moved
			if err != nil {
				return trashed, false, err
			}
		}
	}
	return trashed, false, nil
}

// TrashDisabledSweepSkips reports how many retention sweeps were skipped
// because trash is disabled.
func (s *Store) TrashDisabledSweepSkips() int64 {
	return s.trashDisabledSweepSkips.Load()
}

const retentionSubscriptionFilter = `
	WHERE retention_kind IN (?, ?)
	  AND library_id IN (SELECT id FROM library WHERE trashed_at_ms IS NULL AND encrypted = 0)`

func (s *Store) retentionSubscriptions(ctx context.Context) ([]Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return s.querySubscriptions(ctx, subscriptionColumns+retentionSubscriptionFilter+` ORDER BY id`,
		RetentionKeepLastN, RetentionDeleteAfterPlayed)
}

type retentionQueryer interface {
	queryRower
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *Store) planSubscriptionRetention(ctx context.Context, subscription Subscription) ([]MediaID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return subscriptionRetentionCandidates(ctx, s.catalogR, s.eventsR, subscription)
}

// Paths bind a receipt (and any pending star for that receipt) to any live
// Part of a Media in the Subscription's Library. GUIDs alone are feed-scoped.
const retentionEntriesFrom = `
	FROM subscription_seen_entry se
	JOIN subscription sub ON sub.id = se.subscription_id
	JOIN media_file mf ON mf.path = se.path AND mf.missing_at_ms IS NULL
	JOIN media m ON m.id = mf.media_id AND m.library_id = sub.library_id AND m.trashed_at_ms IS NULL
	WHERE se.path <> ''`

func subscriptionRetentionCandidates(ctx context.Context, catalog, events retentionQueryer, subscription Subscription) ([]MediaID, error) {
	rows, err := catalog.QueryContext(ctx, `
		WITH entries AS (
			SELECT m.id, se.seen_at_ms, se.feed_position,
			       ROW_NUMBER() OVER (PARTITION BY m.id
			           ORDER BY se.seen_at_ms DESC, se.feed_position NULLS LAST) AS occurrence
		`+retentionEntriesFrom+` AND sub.id = ?
		), pending_stars AS (
			SELECT DISTINCT m.id
			FROM item_state_import isi
			JOIN subscription_seen_entry se ON se.subscription_id = isi.subscription_id AND se.guid = isi.item_guid
			JOIN subscription sub ON sub.id = se.subscription_id
			JOIN media_file mf ON mf.path = se.path AND mf.missing_at_ms IS NULL
			JOIN media m ON m.id = mf.media_id AND m.library_id = sub.library_id AND m.trashed_at_ms IS NULL
			WHERE isi.applied_at_ms = 0 AND isi.starred = 1
			  AND sub.library_id = ? AND se.path <> ''
		)
		SELECT entries.id FROM entries
		WHERE occurrence = 1 AND entries.id NOT IN (SELECT id FROM pending_stars)
		ORDER BY seen_at_ms DESC, feed_position NULLS LAST, id
	`, subscription.ID, subscription.LibraryID)
	if err != nil {
		return nil, err
	}
	ids, err := scanRetentionIDs(rows)
	if err != nil || len(ids) == 0 {
		return nil, err
	}

	// Use one events query for the whole Subscription's candidates. The query
	// checks play counts for all followers without round trips per Profile.
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	keep := 0
	if subscription.RetentionKind == RetentionKeepLastN && subscription.RetentionN != nil {
		keep = *subscription.RetentionN
	}
	rows, err = events.QueryContext(ctx, `
		WITH followers AS MATERIALIZED (SELECT profile_id FROM follow WHERE subscription_id = ?)
		SELECT candidate.value FROM json_each(?) candidate
		WHERE candidate.value NOT IN (SELECT media_id FROM media_favorite)
		AND (? <> ? OR (
			EXISTS (SELECT 1 FROM followers)
			AND NOT EXISTS (
				SELECT 1 FROM followers f
				WHERE NOT EXISTS (
					SELECT 1 FROM playback_progress p
					WHERE p.profile_id = f.profile_id AND p.media_id = candidate.value AND p.play_count >= 1
				)
			)
		))
		ORDER BY candidate.key LIMIT -1 OFFSET ?
	`, subscription.ID, string(encoded), subscription.RetentionKind, RetentionDeleteAfterPlayed, keep)
	if err != nil {
		return nil, err
	}
	return scanRetentionIDs(rows)
}

func scanRetentionIDs(rows *sql.Rows) ([]MediaID, error) {
	defer rows.Close()
	var ids []MediaID
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) trashSubscriptionRetentionBatch(ctx context.Context, subscriptionID SubscriptionID, batch []MediaID) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	// Reserve catalog then events, the same order as Version merges. Immediate
	// transactions keep fresh catalog receipts/imports and events engagement
	// stable through the Trash write without taking the Store's exclusive lock.
	catalog, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer catalog.Rollback()
	subscription, err := scanSubscription(catalog.QueryRowContext(ctx, subscriptionColumns+retentionSubscriptionFilter+` AND id = ?`,
		RetentionKeepLastN, RetentionDeleteAfterPlayed, subscriptionID))
	if errors.Is(err, ErrSubscriptionNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	events, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer events.Rollback()
	current, err := subscriptionRetentionCandidates(ctx, catalog, events, subscription)
	if err != nil {
		return 0, err
	}
	eligible := make(map[MediaID]bool, len(current))
	for _, id := range current {
		eligible[id] = true
	}
	now := time.Now().UTC()
	var moved []MediaID
	for _, id := range batch {
		if !eligible[id] {
			continue
		}
		waiting, err := retentionAwaitingFollowers(ctx, catalog, events, id)
		if err != nil {
			return 0, err
		}
		if waiting {
			continue
		}
		ok, err := setMediaTrashedAt(ctx, catalog, id, now)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		if err := appendActivitySQL(ctx, events, ActivityEntry{
			OccurredAt: now,
			Action:     ActivityMediaTrash,
			TargetType: "media",
			TargetID:   string(id),
			LibraryID:  subscription.LibraryID,
		}); err != nil {
			return 0, err
		}
		moved = append(moved, id)
	}
	if len(moved) == 0 {
		return 0, nil
	}
	if err := catalog.Commit(); err != nil {
		return 0, err
	}
	// As in other cross-database Media writes, commit catalog first so the
	// activity log and outbox cannot report a rolled-back move.
	if err := events.Commit(); err != nil {
		return len(moved), err
	}
	if err := s.appendMediaEventsLocked(ctx, MediaEventDeleted, moved, mediaDeletedPayload(subscription.LibraryID), now); err != nil {
		slog.Warn("media outbox event append failed", "kind", MediaEventDeleted, "error", err)
	}
	return len(moved), nil
}

func retentionAwaitingFollowers(ctx context.Context, catalog, events queryRower, id MediaID) (bool, error) {
	var subscriptions string
	if err := catalog.QueryRowContext(ctx, `SELECT json_group_array(DISTINCT sub.id)
		`+retentionEntriesFrom+` AND m.id = ? AND sub.retention_kind = ?`,
		id, RetentionDeleteAfterPlayed).Scan(&subscriptions); err != nil {
		return false, err
	}
	var waiting bool
	err := events.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM json_each(?) sub
			WHERE NOT EXISTS (SELECT 1 FROM follow WHERE subscription_id = sub.value)
			   OR EXISTS (
				SELECT 1 FROM follow f
				WHERE f.subscription_id = sub.value AND NOT EXISTS (
					SELECT 1 FROM playback_progress p
					WHERE p.profile_id = f.profile_id AND p.media_id = ? AND p.play_count >= 1
				)
			)
		)
	`, subscriptions, id).Scan(&waiting)
	return waiting, err
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

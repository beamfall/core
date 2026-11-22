package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
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

// ApplySubscriptionRetentionPolicies moves eligible Media to recoverable Trash.
// The sweep refuses while Trash is disabled and counts one skip per sweep.
//
// Candidates are listed once. Each one is then decided again and moved in its
// own catalog write transaction, so the decision and the Trash write see the
// same receipts, policies and pending stars. The events writer holds Favorites,
// follows and play counts stable through that decision and write.
// Each move journals its actorless media.trash activity with the catalog write.
// The activity and live outbox event are published after the commit.
func (s *Store) ApplySubscriptionRetentionPolicies(ctx context.Context) (trashed int, skipped bool, err error) {
	if s.trashDisabled {
		s.trashDisabledSweepSkips.Add(1)
		return 0, true, ErrTrashDisabledConfirmationRequired
	}
	candidates, err := s.subscriptionRetentionCandidates(ctx)
	if err != nil {
		return 0, false, err
	}
	for _, id := range candidates {
		now := time.Now().UTC()
		moved, err := s.trashSubscriptionRetentionCandidate(ctx, id, now)
		if moved {
			trashed++
		}
		if err != nil {
			return trashed, false, err
		}
	}
	return trashed, false, nil
}

// TrashDisabledSweepSkips reports how many retention sweeps were skipped
// because trash is disabled.
func (s *Store) TrashDisabledSweepSkips() int64 {
	return s.trashDisabledSweepSkips.Load()
}

// retentionPendingStarIDs lists Media an imported star still waits to reach.
const retentionPendingStarIDs = `SELECT m.id` + liveSubscriptionReceipts + `
	AND EXISTS (SELECT 1 FROM item_state_import isi
	            WHERE isi.subscription_id = se.subscription_id AND isi.item_guid = se.guid
	              AND isi.applied_at_ms = 0 AND isi.starred = 1)`

const retentionLibraryEligible = `sub.library_id IN (SELECT id FROM library WHERE trashed_at_ms IS NULL AND encrypted = 0)`

// subscriptionRetentionCandidates lists every Media a policy might move: all
// of a delete-after-played Subscription's Media, and a keep-last-N
// Subscription's Media past its first N in receipt and feed order. Exempt Media
// only push a cutoff later, so this is a superset; the per-Media decision is
// the authority.
func (s *Store) subscriptionRetentionCandidates(ctx context.Context) ([]MediaID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	if err := s.flushSubscriptionRetentionActivityLocked(ctx); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		WITH entries AS (
			SELECT sub.id AS subscription_id, sub.retention_kind, sub.retention_n, m.id AS media_id,
			       se.seen_at_ms, se.feed_position,
			       ROW_NUMBER() OVER (PARTITION BY sub.id, m.id
			           ORDER BY se.seen_at_ms DESC, se.feed_position NULLS LAST) AS occurrence
			`+liveSubscriptionReceipts+`
			  AND sub.retention_kind IN (?, ?) AND `+retentionLibraryEligible+`
		), heads AS (
			SELECT media_id, retention_kind, retention_n,
			       ROW_NUMBER() OVER (PARTITION BY subscription_id
			           ORDER BY seen_at_ms DESC, feed_position NULLS LAST, media_id) AS rank
			FROM entries WHERE occurrence = 1
		)
		SELECT DISTINCT media_id FROM heads
		WHERE retention_kind = ? OR rank > retention_n
		ORDER BY media_id
	`, RetentionKeepLastN, RetentionDeleteAfterPlayed, RetentionDeleteAfterPlayed)
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

// trashSubscriptionRetentionCandidate decides and moves one Media in a single
// catalog write transaction. A read-only pass first keeps the catalog writer
// free for the Media that stay. The outbox event follows the commit, as in
// TrashMedia.
func (s *Store) trashSubscriptionRetentionCandidate(ctx context.Context, id MediaID, now time.Time) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	if _, ok, err := subscriptionRetentionPermits(ctx, s.catalogR, s.eventsR, id); err != nil || !ok {
		return false, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	activity, moved, err := s.commitSubscriptionRetention(ctx, tx, id, now)
	if err != nil || !moved {
		return false, err
	}
	if eventErr := s.appendMediaEventLocked(ctx, MediaEventDeleted, id, mediaDeletedPayload(activity.LibraryID), now); eventErr != nil {
		slog.Warn("media outbox event append failed", "kind", MediaEventDeleted, "media", id, "error", eventErr)
	}
	return true, s.publishSubscriptionRetentionActivityLocked(ctx, activity)
}

func (s *Store) commitSubscriptionRetention(ctx context.Context, catalog *sql.Tx, id MediaID, now time.Time) (ActivityEntry, bool, error) {
	// Take writers in catalog-then-events order. Hold follows and play counts
	// stable through the catalog commit, then release the events writer before
	// the outbox append. This transaction only reads from events.
	events, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return ActivityEntry{}, false, err
	}
	defer events.Rollback()
	libraryID, ok, err := subscriptionRetentionPermits(ctx, catalog, events, id)
	if err != nil || !ok {
		return ActivityEntry{}, false, err
	}
	activity := subscriptionRetentionActivity("subscription-retention:"+rand.Text(), id, libraryID, now)
	if _, err := catalog.ExecContext(ctx, `
		INSERT INTO subscription_retention_activity(operation_id, media_id, library_id, occurred_at_ms)
		VALUES (?, ?, ?, ?)
	`, activity.OperationID, id, libraryID, timeToMillis(now)); err != nil {
		return ActivityEntry{}, false, err
	}
	moved, err := setMediaTrashedAt(ctx, catalog, id, now)
	if err != nil || !moved {
		return ActivityEntry{}, false, err
	}
	if err := catalog.Commit(); err != nil {
		return ActivityEntry{}, false, err
	}
	return activity, true, nil
}

func subscriptionRetentionActivity(operationID string, id MediaID, libraryID LibraryID, now time.Time) ActivityEntry {
	return ActivityEntry{
		OperationID: operationID, OccurredAt: now, Action: ActivityMediaTrash,
		TargetType: "media", TargetID: string(id), LibraryID: libraryID,
	}
}

func (s *Store) publishSubscriptionRetentionActivityLocked(ctx context.Context, activity ActivityEntry) error {
	if err := appendActivitySQL(ctx, s.eventsW, activity); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `DELETE FROM subscription_retention_activity WHERE operation_id = ?`, activity.OperationID)
	return err
}

func (s *Store) flushSubscriptionRetentionActivityLocked(ctx context.Context) error {
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT operation_id, media_id, library_id, occurred_at_ms
		FROM subscription_retention_activity ORDER BY occurred_at_ms, operation_id
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var pending []ActivityEntry
	for rows.Next() {
		var operationID string
		var id MediaID
		var libraryID LibraryID
		var occurredAt int64
		if err := rows.Scan(&operationID, &id, &libraryID, &occurredAt); err != nil {
			return err
		}
		pending = append(pending, subscriptionRetentionActivity(operationID, id, libraryID, time.UnixMilli(occurredAt).UTC()))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, activity := range pending {
		if err := s.publishSubscriptionRetentionActivityLocked(ctx, activity); err != nil {
			return err
		}
	}
	return nil
}

type retentionQueryer interface {
	queryRower
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type retentionReference struct {
	subscriptionID SubscriptionID
	kind           RetentionKind
	n              sql.NullInt64
	libraryID      LibraryID
	eligible       bool
}

// subscriptionRetentionPermits reports whether every Subscription that
// references the Media permits moving it to Trash: keep-last-N only past its
// cutoff, delete-after-played only once every follower has played it, and
// keep-all never. A Favorite, Keep flag or pending imported star protects it
// from every policy. Both writers are held for the final decision.
func subscriptionRetentionPermits(ctx context.Context, catalog retentionQueryer, events queryRower, id MediaID) (LibraryID, bool, error) {
	references, err := retentionReferences(ctx, catalog, id)
	if err != nil || len(references) == 0 {
		return "", false, err
	}
	var pendingStar, favorite bool
	if err := catalog.QueryRowContext(ctx, `SELECT ? IN (`+retentionPendingStarIDs+`)`, id).Scan(&pendingStar); err != nil || pendingStar {
		return "", false, err
	}
	if err := events.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM media_favorite WHERE media_id = ?)`, id).Scan(&favorite); err != nil || favorite {
		return "", false, err
	}
	for _, reference := range references {
		ok := false
		switch {
		case !reference.eligible:
		case reference.kind == RetentionKeepLastN && reference.n.Valid:
			ok, err = retentionPastKeepCutoff(ctx, catalog, events, reference.subscriptionID, id, reference.n.Int64)
		case reference.kind == RetentionDeleteAfterPlayed:
			ok, err = retentionPlayedByEveryFollower(ctx, events, reference.subscriptionID, id)
		}
		if err != nil || !ok {
			return "", false, err
		}
	}
	return references[0].libraryID, true, nil
}

func retentionReferences(ctx context.Context, catalog retentionQueryer, id MediaID) ([]retentionReference, error) {
	rows, err := catalog.QueryContext(ctx, `
		SELECT DISTINCT sub.id, sub.retention_kind, sub.retention_n, m.library_id, `+retentionLibraryEligible+`
		`+liveSubscriptionReceipts+` AND m.id = ?
		ORDER BY sub.id
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var references []retentionReference
	for rows.Next() {
		var reference retentionReference
		if err := rows.Scan(&reference.subscriptionID, &reference.kind, &reference.n, &reference.libraryID, &reference.eligible); err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	return references, rows.Err()
}

// retentionPastKeepCutoff reports whether at least keep unprotected Media rank
// strictly ahead of the Media in the Subscription. Each Media ranks by its
// newest receipt; within one receipt time an earlier feed position ranks
// first. An unprotected Media with a legacy receipt ties its receipt-time
// group, so only newer groups can push that group past the cutoff.
func retentionPastKeepCutoff(ctx context.Context, catalog retentionQueryer, events queryRower, subscriptionID SubscriptionID, id MediaID, keep int64) (bool, error) {
	rows, err := catalog.QueryContext(ctx, `
		WITH target AS (
			SELECT se.seen_at_ms AS seen, se.feed_position AS position
			`+liveSubscriptionReceipts+` AND sub.id = ? AND m.id = ?
			ORDER BY se.seen_at_ms DESC, se.feed_position NULLS LAST LIMIT 1
		), heads AS (
			SELECT m.id, MAX(se.seen_at_ms) AS seen,
			       MIN(se.feed_position) FILTER (WHERE se.seen_at_ms = (SELECT seen FROM target)) AS position
			`+liveSubscriptionReceipts+` AND sub.id = ? AND m.id <> ?
			  AND se.seen_at_ms >= (SELECT seen FROM target)
			  AND m.id NOT IN (`+retentionPendingStarIDs+`)
			GROUP BY m.id
		)
		SELECT id, heads.seen > target.seen, heads.seen = target.seen AND heads.position IS NULL
		FROM heads CROSS JOIN target
		WHERE heads.seen > target.seen OR heads.position IS NULL OR heads.position < target.position
	`, subscriptionID, id, subscriptionID, id)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	type rankedMedia struct {
		ID     MediaID `json:"id"`
		Newer  bool    `json:"newer"`
		Legacy bool    `json:"legacy"`
	}
	var ranked []rankedMedia
	for rows.Next() {
		var media rankedMedia
		if err := rows.Scan(&media.ID, &media.Newer, &media.Legacy); err != nil {
			return false, err
		}
		ranked = append(ranked, media)
	}
	if err := rows.Err(); err != nil || int64(len(ranked)) < keep {
		return false, err
	}
	encoded, err := json.Marshal(ranked)
	if err != nil {
		return false, err
	}
	var unprotected int64
	err = events.QueryRowContext(ctx, `
		SELECT CASE WHEN COUNT(*) FILTER (WHERE value ->> 'legacy') > 0
		            THEN COUNT(*) FILTER (WHERE value ->> 'newer')
		            ELSE COUNT(*) END
		FROM json_each(?)
		WHERE (value ->> 'id') NOT IN (SELECT media_id FROM media_favorite)
	`, string(encoded)).Scan(&unprotected)
	return unprotected >= keep, err
}

// retentionPlayedByEveryFollower requires at least one follower, and a play by
// each of them.
func retentionPlayedByEveryFollower(ctx context.Context, events queryRower, subscriptionID SubscriptionID, id MediaID) (bool, error) {
	var played bool
	err := events.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM follow WHERE subscription_id = ?)
		   AND NOT EXISTS (
			SELECT 1 FROM follow f
			WHERE f.subscription_id = ? AND NOT EXISTS (
				SELECT 1 FROM playback_progress p
				WHERE p.profile_id = f.profile_id AND p.media_id = ? AND p.play_count >= 1
			)
		)
	`, subscriptionID, subscriptionID, id).Scan(&played)
	return played, err
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

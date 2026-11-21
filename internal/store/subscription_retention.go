package store

import (
	"context"
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
//
// Candidates are listed once. Each one is then decided again and moved in its
// own catalog write transaction, so the decision and the Trash write see the
// same receipts, policies and pending stars. Favorite and Keep writes take the
// catalog writer too (SetMediaFavorite), so neither can land between them.
// Like scan retirement, each move records its outbox event and one actorless
// media.trash activity entry after the catalog commit.
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
		libraryID, moved, err := s.trashSubscriptionRetentionCandidate(ctx, id, now)
		if err != nil {
			return trashed, false, err
		}
		if !moved {
			continue
		}
		trashed++
		if err := s.AppendActivity(ctx, ActivityEntry{
			OccurredAt: now,
			Action:     ActivityMediaTrash,
			TargetType: "media",
			TargetID:   string(id),
			LibraryID:  libraryID,
		}); err != nil {
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

// Paths bind a receipt (and any pending star for that receipt) to any live
// Part of a Media in the Subscription's Library. GUIDs alone are feed-scoped.
const retentionEntriesFrom = `
	FROM subscription_seen_entry se
	JOIN subscription sub ON sub.id = se.subscription_id
	JOIN media_file mf ON mf.path = se.path AND mf.missing_at_ms IS NULL
	JOIN media m ON m.id = mf.media_id AND m.library_id = sub.library_id AND m.trashed_at_ms IS NULL
	WHERE se.path <> ''`

// retentionPendingStarIDs lists Media an imported star still waits to reach.
const retentionPendingStarIDs = `SELECT m.id` + retentionEntriesFrom + `
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
	rows, err := s.catalogR.QueryContext(ctx, `
		WITH entries AS (
			SELECT sub.id AS subscription_id, sub.retention_kind, sub.retention_n, m.id AS media_id,
			       se.seen_at_ms, se.feed_position,
			       ROW_NUMBER() OVER (PARTITION BY sub.id, m.id
			           ORDER BY se.seen_at_ms DESC, se.feed_position NULLS LAST) AS occurrence
			`+retentionEntriesFrom+`
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
func (s *Store) trashSubscriptionRetentionCandidate(ctx context.Context, id MediaID, now time.Time) (LibraryID, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", false, err
	}
	if _, ok, err := subscriptionRetentionPermits(ctx, s.catalogR, s.eventsR, id); err != nil || !ok {
		return "", false, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	libraryID, ok, err := subscriptionRetentionPermits(ctx, tx, s.eventsR, id)
	if err != nil || !ok {
		return "", false, err
	}
	moved, err := setMediaTrashedAt(ctx, tx, id, now)
	if err != nil || !moved {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	if eventErr := s.appendMediaEventLocked(ctx, MediaEventDeleted, id, mediaDeletedPayload(libraryID), now); eventErr != nil {
		slog.Warn("media outbox event append failed", "kind", MediaEventDeleted, "media", id, "error", eventErr)
	}
	return libraryID, true, nil
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
// from every policy. Inside a catalog write transaction the catalog reads are
// stable, and Favorites are too because their writes take the catalog writer.
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
		`+retentionEntriesFrom+` AND m.id = ?
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
// first. A NULL position (a legacy receipt) never ranks ahead of or behind its
// receipt-time group, so a tie at the cutoff is kept whole.
func retentionPastKeepCutoff(ctx context.Context, catalog retentionQueryer, events queryRower, subscriptionID SubscriptionID, id MediaID, keep int64) (bool, error) {
	rows, err := catalog.QueryContext(ctx, `
		WITH target AS (
			SELECT se.seen_at_ms AS seen, se.feed_position AS position
			`+retentionEntriesFrom+` AND sub.id = ? AND m.id = ?
			ORDER BY se.seen_at_ms DESC, se.feed_position NULLS LAST LIMIT 1
		)
		SELECT DISTINCT m.id
		`+retentionEntriesFrom+` AND sub.id = ? AND m.id <> ?
		  AND (se.seen_at_ms > (SELECT seen FROM target)
		       OR (se.seen_at_ms = (SELECT seen FROM target) AND se.feed_position < (SELECT position FROM target)))
		  AND m.id NOT IN (`+retentionPendingStarIDs+`)
	`, subscriptionID, id, subscriptionID, id)
	if err != nil {
		return false, err
	}
	ahead, err := scanRetentionIDs(rows)
	if err != nil || int64(len(ahead)) < keep {
		return false, err
	}
	encoded, err := json.Marshal(ahead)
	if err != nil {
		return false, err
	}
	var unprotected int64
	err = events.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM json_each(?)
		WHERE value NOT IN (SELECT media_id FROM media_favorite)
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

package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"strings"
)

// New entries from followed Subscriptions (CORE-2958, ADR-0238): the
// Profile-scoped subscription-new Source and the per-follow route that can
// also add them to the Profile's Play Queue.

// NewEntryRoute is where a followed Subscription's new entries go for one
// Profile.
type NewEntryRoute string

const (
	// NewEntryRouteNew lists new entries in the subscription-new Source only.
	NewEntryRouteNew NewEntryRoute = "new"
	// NewEntryRouteQueueEnd also appends them to the Play Queue.
	NewEntryRouteQueueEnd NewEntryRoute = "queue-end"
	// NewEntryRouteQueueFront also puts them next in the Play Queue.
	NewEntryRouteQueueFront NewEntryRoute = "queue-front"
)

// ErrNotFollowing reports a route change for a Subscription the Profile does
// not follow.
var ErrNotFollowing = errors.New("profile does not follow the subscription")

// Valid reports whether r is one of the closed routes.
func (r NewEntryRoute) Valid() bool {
	return r == NewEntryRouteNew || r == NewEntryRouteQueueEnd || r == NewEntryRouteQueueFront
}

// SetFollowNewEntryRoute changes where new entries of a followed Subscription
// go for profileID. The follow time, which bounds what counts as new, is kept.
// Switching from the New-only route to a Play Queue route starts the queue
// from entries catalogued after the switch: entries already listed as new
// stay in the Source and are not added to the queue all at once.
func (s *Store) SetFollowNewEntryRoute(ctx context.Context, profileID ProfileID, subscriptionID SubscriptionID, route NewEntryRoute) error {
	if profileID == "" || subscriptionID == "" {
		return ValidationError("profile ID and subscription ID are required")
	}
	if !route.Valid() {
		return ValidationError("route must be new, queue-end or queue-front")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	var seenThrough int64
	if err := s.catalogR.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(first_seen_at_ms), 0) FROM subscription_entry WHERE subscription_id = ?
	`, subscriptionID).Scan(&seenThrough); err != nil {
		return err
	}
	result, err := s.eventsW.ExecContext(ctx, `
		UPDATE follow SET
		  new_entry_routed_ms = CASE WHEN new_entry_route = 'new' AND ? <> 'new'
		    THEN MAX(new_entry_routed_ms, ?) ELSE new_entry_routed_ms END,
		  new_entry_route = ?
		WHERE profile_id = ? AND subscription_id = ?
	`, string(route), seenThrough, string(route), profileID, subscriptionID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFollowing
	}
	return nil
}

// FollowNewEntryRoute is profileID's route for subscriptionID; a Subscription
// the Profile does not follow reads as NewEntryRouteNew.
func (s *Store) FollowNewEntryRoute(ctx context.Context, profileID ProfileID, subscriptionID SubscriptionID) (NewEntryRoute, error) {
	if profileID == "" || subscriptionID == "" {
		return "", ValidationError("profile ID and subscription ID are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	var route string
	err := s.eventsR.QueryRowContext(ctx, `
		SELECT new_entry_route FROM follow WHERE profile_id = ? AND subscription_id = ?
	`, profileID, subscriptionID).Scan(&route)
	if errors.Is(err, sql.ErrNoRows) {
		return NewEntryRouteNew, nil
	}
	return NewEntryRoute(route), err
}

type followRow struct {
	profileID  ProfileID
	subID      SubscriptionID
	followedMS int64
	route      NewEntryRoute
	routedMS   int64
}

// subscriptionNewSQL is the subscription-new Source: unplayed catalogued Media
// of the Subscriptions profileID follows, published (or first seen) after the
// follow, newest first, under filter and the Profile's Mute.
func (s *Store) subscriptionNewSQL(ctx context.Context, profileID ProfileID, filter LibraryFilter, limit, offset int) ([]SourceItem, error) {
	items := []SourceItem{}
	follows, err := s.profileFollowsSQL(ctx, profileID)
	if err != nil || len(follows) == 0 || limit == 0 {
		return items, err
	}
	candidates, err := s.newEntryMediaSQL(ctx, follows)
	if err != nil || len(candidates) == 0 {
		return items, err
	}
	played, err := playedAmong(ctx, s.eventsR, profileID, candidates)
	if err != nil {
		return nil, err
	}
	rows := make([]profileItemRow, 0, len(candidates))
	for _, id := range candidates {
		if !played[id] {
			rows = append(rows, profileItemRow{kind: ItemTargetMedia, id: string(id)})
		}
	}
	visible, err := s.visibleProfileItemsSQL(ctx, rows, filter)
	if err != nil {
		return nil, err
	}
	for _, relations := range s.relationReadersLocked() {
		visible, err = s.applyMuteFilterLocked(ctx, relations, profileID, filter, visible)
		if err != nil {
			return nil, err
		}
	}
	start := min(max(offset, 0), len(visible))
	end := len(visible)
	if limit > 0 {
		end = min(start+limit, end)
	}
	return append(items, visible[start:end]...), nil
}

func (s *Store) profileFollowsSQL(ctx context.Context, profileID ProfileID) ([]followRow, error) {
	return s.followRowsSQL(ctx, `
		SELECT profile_id, subscription_id, followed_at_ms, new_entry_route, new_entry_routed_ms
		FROM follow WHERE profile_id = ? ORDER BY subscription_id
	`, profileID)
}

func (s *Store) followRowsSQL(ctx context.Context, query string, args ...any) ([]followRow, error) {
	rows, err := s.eventsR.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []followRow
	for rows.Next() {
		var row followRow
		var route string
		if err := rows.Scan(&row.profileID, &row.subID, &row.followedMS, &route, &row.routedMS); err != nil {
			return nil, err
		}
		row.route = NewEntryRoute(route)
		out = append(out, row)
	}
	return out, rows.Err()
}

// subscriptionEntryTimeSQL is an entry's effective publish time: the feed's
// date when it gave one, otherwise when the server first saw it. A missing
// date is stored as 0; dates before 1970 are negative and still count.
const subscriptionEntryTimeSQL = `CASE WHEN published_at_ms <> 0 THEN published_at_ms ELSE first_seen_at_ms END`

// newEntryFollowChunk bounds the follows bound into one query.
const newEntryFollowChunk = 400

// newEntryMediaSQL lists the Media of entries newer than each follow, newest
// first. Subscriptions are catalogued in the plain catalog only.
func (s *Store) newEntryMediaSQL(ctx context.Context, follows []followRow) ([]MediaID, error) {
	type entry struct {
		id       MediaID
		at       int64
		position int64
	}
	var entries []entry
	for chunk := range slices.Chunk(follows, newEntryFollowChunk) {
		args := make([]any, 0, 2*len(chunk))
		for _, follow := range chunk {
			args = append(args, follow.subID, follow.followedMS)
		}
		values := strings.TrimSuffix(strings.Repeat("(?, ?),", len(chunk)), ",")
		// #nosec G202 -- only generated placeholders and the closed entry-time expression are interpolated; every value is bound.
		rows, err := s.catalogR.QueryContext(ctx, `
			WITH followed(subscription_id, followed_at_ms) AS (VALUES `+values+`)
			SELECT e.media_id, `+subscriptionEntryTimeSQL+`, e.feed_position
			FROM subscription_entry e JOIN followed f ON f.subscription_id = e.subscription_id
			WHERE `+subscriptionEntryTimeSQL+` > f.followed_at_ms
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.id, &e.at, &e.position); err != nil {
				_ = rows.Close()
				return nil, err
			}
			entries = append(entries, e)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(entries, func(a, b entry) int {
		return cmp.Or(cmp.Compare(b.at, a.at), cmp.Compare(a.position, b.position), cmp.Compare(a.id, b.id))
	})
	seen := map[MediaID]bool{}
	ids := make([]MediaID, 0, len(entries))
	for _, e := range entries {
		if !seen[e.id] {
			seen[e.id] = true
			ids = append(ids, e.id)
		}
	}
	return ids, nil
}

// PlayedMedia reports which of ids profileID has played (watched, or played at
// least once). A scope without a Profile has played nothing.
func (s *Store) PlayedMedia(ctx context.Context, profileID ProfileID, ids []MediaID) (map[MediaID]bool, error) {
	if profileID == "" || len(ids) == 0 {
		return map[MediaID]bool{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return playedAmong(ctx, s.eventsR, profileID, ids)
}

// playedAmong reports which of ids profileID has played, read through q.
func playedAmong(ctx context.Context, q rowsQueryer, profileID ProfileID, ids []MediaID) (map[MediaID]bool, error) {
	played := map[MediaID]bool{}
	for chunk := range slices.Chunk(ids, 500) {
		args := make([]any, 0, len(chunk)+1)
		args = append(args, profileID)
		for _, id := range chunk {
			args = append(args, id)
		}
		// #nosec G202 -- only generated placeholders are interpolated; every value is bound.
		rows, err := q.QueryContext(ctx, `
			SELECT media_id FROM playback_progress
			WHERE profile_id = ? AND (watched = 1 OR play_count >= 1)
			  AND media_id IN (`+sqlPlaceholders(len(chunk))+`)
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id MediaID
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			played[id] = true
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return played, nil
}

// errFollowChanged reports that a follow was removed, re-routed or routed by
// another pass between reading it and writing its Play Queue.
var errFollowChanged = errors.New("follow changed while routing new entries")

// RouteNewSubscriptionEntries adds a Subscription's not yet routed new entries
// to the Play Queue of every follower whose route asks for it. Each follower
// gets only entries published after its follow, oldest first, under its own
// Library, certification (resolveCertification maps its ceilings) and Mute
// policy, minus what it already played. The follow's watermark advances in
// the queue's events transaction, so a pass interrupted by a crash resumes on
// the next poll and an entry is never routed twice. A follower whose write
// fails is skipped and logged; the poll does not fail.
func (s *Store) RouteNewSubscriptionEntries(ctx context.Context, subscriptionID SubscriptionID, resolveCertification func([]ProfileCertificationCeiling) []CertificationConstraint) error {
	if resolveCertification == nil {
		return nil
	}
	followers, err := s.queueRoutedFollowers(ctx, subscriptionID)
	if err != nil {
		return err
	}
	for _, follower := range followers {
		err := s.routeFollowerEntries(ctx, follower, resolveCertification)
		if err != nil && !errors.Is(err, errFollowChanged) {
			slog.Warn("new entries not added to the play queue", "subscription", subscriptionID, "profile", follower.profileID, "error", err)
		}
	}
	return nil
}

func (s *Store) queueRoutedFollowers(ctx context.Context, subscriptionID SubscriptionID) ([]followRow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return s.followRowsSQL(ctx, `
		SELECT profile_id, subscription_id, followed_at_ms, new_entry_route, new_entry_routed_ms
		FROM follow WHERE subscription_id = ? AND new_entry_route <> 'new'
		ORDER BY profile_id
	`, subscriptionID)
}

func (s *Store) routeFollowerEntries(ctx context.Context, follower followRow, resolveCertification func([]ProfileCertificationCeiling) []CertificationConstraint) error {
	pending, watermark, err := s.unroutedEntries(ctx, follower)
	if err != nil || watermark <= follower.routedMS {
		return err
	}
	profile, err := s.Profile(ctx, follower.profileID)
	if err != nil {
		return err
	}
	placement := QueueEnd
	if follower.route == NewEntryRouteQueueFront {
		placement = QueueNext
	}
	auth := QueueWriteAuthorization{
		ProfileID:            follower.profileID,
		Filter:               LibraryFilter{AllowedIDs: profile.Library},
		Now:                  s.serverNow,
		ResolveCertification: resolveCertification,
	}
	return s.writeQueue(ctx, auth, queueWriteTargets{ids: pending}, func(view queueWriteView) error {
		ids, err := s.routableLocked(ctx, view, pending)
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			view.queue.addMany(ids, placement, view.visible)
		}
		return advanceFollowWatermark(ctx, view.events, follower, watermark)
	})
}

// unroutedEntries lists the follower's entries first seen after its watermark
// and published after its follow, oldest first, with the new watermark.
func (s *Store) unroutedEntries(ctx context.Context, follower followRow) ([]MediaID, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, 0, err
	}
	// #nosec G202 -- only the closed entry-time expression is interpolated; every value is bound.
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT media_id, first_seen_at_ms FROM subscription_entry
		WHERE subscription_id = ? AND first_seen_at_ms > ? AND `+subscriptionEntryTimeSQL+` > ?
		ORDER BY `+subscriptionEntryTimeSQL+`, feed_position DESC, media_id
	`, follower.subID, follower.routedMS, follower.followedMS)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	watermark := follower.routedMS
	var ids []MediaID
	for rows.Next() {
		var id MediaID
		var firstSeen int64
		if err := rows.Scan(&id, &firstSeen); err != nil {
			return nil, 0, err
		}
		ids = append(ids, id)
		watermark = max(watermark, firstSeen)
	}
	return ids, watermark, rows.Err()
}

// routableLocked keeps the pending Media the follower may queue: visible,
// not locked by certification, not muted and not played.
func (s *Store) routableLocked(ctx context.Context, view queueWriteView, pending []MediaID) ([]MediaID, error) {
	items := make([]SourceItem, 0, len(pending))
	for _, id := range pending {
		if media, ok := view.visible[id]; ok {
			items = append(items, queueSourceItem(media))
		}
	}
	if err := annotateLocked(ctx, view.readers, view.filter.Certification, items, view.filter.GrantedMediaIDs); err != nil {
		return nil, err
	}
	items = slices.DeleteFunc(items, func(item SourceItem) bool { return item.Locked })
	var err error
	for _, relations := range s.relationReadersLocked() {
		if items, err = s.applyMuteFilterLocked(ctx, relations, view.profileID, view.filter, items); err != nil {
			return nil, err
		}
	}
	candidates := make([]MediaID, len(items))
	for i, item := range items {
		candidates[i] = item.ID
	}
	played, err := playedAmong(ctx, view.events, view.profileID, candidates)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(candidates, func(id MediaID) bool { return played[id] }), nil
}

// advanceFollowWatermark moves the follow's watermark only while the follow
// is still the one routing read: same follow time, route and watermark.
func advanceFollowWatermark(ctx context.Context, tx *sql.Tx, follower followRow, watermark int64) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE follow SET new_entry_routed_ms = ?
		WHERE profile_id = ? AND subscription_id = ? AND followed_at_ms = ?
		  AND new_entry_route = ? AND new_entry_routed_ms = ?
	`, watermark, follower.profileID, follower.subID, follower.followedMS, string(follower.route), follower.routedMS)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return errFollowChanged
	}
	return nil
}

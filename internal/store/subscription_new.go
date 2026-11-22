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
	result, err := s.eventsW.ExecContext(ctx, `
		UPDATE follow SET new_entry_route = ? WHERE profile_id = ? AND subscription_id = ?
	`, string(route), profileID, subscriptionID)
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
	played, err := s.playedAmongSQL(ctx, profileID, candidates)
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
		SELECT profile_id, subscription_id, followed_at_ms, new_entry_route
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
		if err := rows.Scan(&row.profileID, &row.subID, &row.followedMS, &route); err != nil {
			return nil, err
		}
		row.route = NewEntryRoute(route)
		out = append(out, row)
	}
	return out, rows.Err()
}

// subscriptionEntryTimeSQL is an entry's effective publish time: the feed's
// date when it gave one, otherwise when the server first saw it.
const subscriptionEntryTimeSQL = `CASE WHEN published_at_ms > 0 THEN published_at_ms ELSE first_seen_at_ms END`

// newEntryMediaSQL lists the Media of entries newer than each follow, newest
// first. Subscriptions are catalogued in the plain catalog only.
func (s *Store) newEntryMediaSQL(ctx context.Context, follows []followRow) ([]MediaID, error) {
	clauses := make([]string, 0, len(follows))
	args := make([]any, 0, 2*len(follows))
	for _, follow := range follows {
		clauses = append(clauses, `(subscription_id = ? AND `+subscriptionEntryTimeSQL+` > ?)`)
		args = append(args, follow.subID, follow.followedMS)
	}
	// #nosec G202 -- only closed clause text with bound placeholders is interpolated; every value is bound.
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT media_id FROM subscription_entry
		WHERE `+strings.Join(clauses, " OR ")+`
		ORDER BY `+subscriptionEntryTimeSQL+` DESC, feed_position, media_id
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[MediaID]bool{}
	var ids []MediaID
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// playedAmongSQL reports which of ids profileID has played.
func (s *Store) playedAmongSQL(ctx context.Context, profileID ProfileID, ids []MediaID) (map[MediaID]bool, error) {
	played := map[MediaID]bool{}
	for chunk := range slices.Chunk(ids, 500) {
		args := make([]any, 0, len(chunk)+1)
		args = append(args, profileID)
		for _, id := range chunk {
			args = append(args, id)
		}
		// #nosec G202 -- only generated placeholders are interpolated; every value is bound.
		rows, err := s.eventsR.QueryContext(ctx, `
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

// RouteNewSubscriptionEntries adds entries a poll just catalogued to the Play
// Queue of every follower whose route asks for it. Each follower gets only the
// entries published after its follow, oldest first, under its own Library and
// certification policy (resolveCertification maps its ceilings). A follower
// the queue refuses is skipped and logged; the poll does not fail.
func (s *Store) RouteNewSubscriptionEntries(ctx context.Context, subscriptionID SubscriptionID, created []SubscriptionEntry, resolveCertification func([]ProfileCertificationCeiling) []CertificationConstraint) error {
	if len(created) == 0 || resolveCertification == nil {
		return nil
	}
	followers, err := s.queueRoutedFollowers(ctx, subscriptionID)
	if err != nil {
		return err
	}
	ordered := slices.Clone(created)
	slices.SortStableFunc(ordered, func(a, b SubscriptionEntry) int {
		return cmp.Or(cmp.Compare(entryTimeMS(a), entryTimeMS(b)), cmp.Compare(b.FeedPosition, a.FeedPosition))
	})
	for _, follower := range followers {
		var ids []MediaID
		for _, entry := range ordered {
			if entryTimeMS(entry) > follower.followedMS {
				ids = append(ids, entry.MediaID)
			}
		}
		if len(ids) == 0 {
			continue
		}
		if err := s.routeToQueue(ctx, follower, ids, resolveCertification); err != nil {
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
		SELECT profile_id, subscription_id, followed_at_ms, new_entry_route
		FROM follow WHERE subscription_id = ? AND new_entry_route <> 'new'
		ORDER BY profile_id
	`, subscriptionID)
}

func (s *Store) routeToQueue(ctx context.Context, follower followRow, ids []MediaID, resolveCertification func([]ProfileCertificationCeiling) []CertificationConstraint) error {
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
	_, err = s.QueueAddMany(ctx, auth, ids, placement)
	return err
}

func entryTimeMS(entry SubscriptionEntry) int64 {
	if !entry.PublishedAt.IsZero() && entry.PublishedAt.UnixMilli() > 0 {
		return entry.PublishedAt.UnixMilli()
	}
	return entry.FirstSeenAt.UnixMilli()
}

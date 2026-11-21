package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
)

// ProfileHideWatchedPrefKey is the reserved per-Profile preference that hides
// Media the Profile has finished from catalog shelves and from library browse
// that names no watch filter (CORE-2836). It lives in profile_client_prefs so
// every client shares it and it needs no schema change. "on" turns it on, matching the other boolean prefs; any
// other value, or no row, leaves it off.
const ProfileHideWatchedPrefKey = "profile.hide-watched"

// profileHideWatchedLocked reports whether profileID has hide watched on. The
// caller holds s.mu.
func (s *Store) profileHideWatchedLocked(ctx context.Context, profileID ProfileID) (bool, error) {
	if profileID == "" {
		return false, nil
	}
	var value string
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT value FROM profile_client_prefs WHERE profile_id = ? AND key = ?
	`, profileID, ProfileHideWatchedPrefKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return value == "on", err
}

// hideWatchedApplies reports whether a Source kind is a catalog shelf that
// hide watched filters. The Profile's own engagement lists (what it is
// watching, has played, saved or rated) are about its history and keep
// finished Media.
func hideWatchedApplies(kind string) bool {
	switch kind {
	case "continue-watching", "next-up", "recently-played", "most-played", "watchlist",
		"favorites", "rated", "queue", "resume-progress", "top-tracks", "top-artists",
		"top-albums", "listening-summary", "watched-by-any-profile":
		return false
	}
	return true
}

// watchedMediaLocked returns the Media profileID has finished. The caller
// holds s.mu.
func (s *Store) watchedMediaLocked(ctx context.Context, profileID ProfileID) (map[MediaID]bool, error) {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id FROM playback_progress WHERE profile_id = ? AND watched <> 0
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	watched := map[MediaID]bool{}
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		watched[id] = true
	}
	return watched, rows.Err()
}

// dropWatched removes the finished Media from items. A rolled-up container
// card drops once every Media it stands for is finished; any other Entity
// card passes through, since browse judges its finished state.
func dropWatched(items []SourceItem, watched map[MediaID]bool) []SourceItem {
	out := make([]SourceItem, 0, len(items))
	for _, item := range items {
		if item.Kind != "entity" {
			if watched[item.ID] {
				continue
			}
		} else if len(item.rollUpMembers) > 0 && !slices.ContainsFunc(item.rollUpMembers, func(id MediaID) bool { return !watched[id] }) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// hiddenWatchedLocked returns the finished Media a Hide watched read leaves
// out: nil when the Profile does not hide watched, the Source is one of its own
// engagement lists, or nothing is finished. The caller holds s.mu.
func (s *Store) hiddenWatchedLocked(ctx context.Context, query SourceQuery) (map[MediaID]bool, error) {
	if !hideWatchedApplies(query.Kind) {
		return nil, nil
	}
	hide, err := s.profileHideWatchedLocked(ctx, query.ProfileID)
	if err != nil || !hide {
		return nil, err
	}
	watched, err := s.watchedMediaLocked(ctx, query.ProfileID)
	if err != nil || len(watched) == 0 {
		return nil, err
	}
	return watched, nil
}

// maxProfileFilteredRead bounds how many raw items one filtered page reads.
const maxProfileFilteredRead = 20000

// profileSourceItemsLocked reads one page of a Source for a Profile.
//
// Per-Profile Mute (ADR-0054) is an implicit negative Source predicate applied
// here, at Source evaluation, so every passive-discovery rail is filtered
// uniformly. It is deliberately NOT applied in SearchMedia or any direct media
// fetch: a muted target stays searchable and playable (§2.2). Hide watched
// (CORE-2836) drops finished Media from catalog shelves the same way. A
// profile-less read or a text search passes through unchanged.
//
// With either filter in play the page is taken from the filtered list, read
// from the top and widened until it holds offset+limit survivors or the Source
// runs out, so a See All never ends early on a short page. The caller holds s.mu.
func (s *Store) profileSourceItemsLocked(ctx context.Context, filter LibraryFilter, query SourceQuery, limit, offset int) ([]SourceItem, error) {
	if query.ProfileID == "" || strings.TrimSpace(query.Query) != "" {
		return s.sourceItemsForKind(ctx, filter, query, limit, offset)
	}
	watched, err := s.hiddenWatchedLocked(ctx, query)
	if err != nil {
		return nil, err
	}
	muted, err := s.profileHasMutesLocked(ctx, query.ProfileID)
	if err != nil {
		return nil, err
	}
	if watched == nil && !muted {
		return s.sourceItemsForKind(ctx, filter, query, limit, offset)
	}
	want := offset + limit
	read := want + len(watched)
	for {
		read = min(read, maxProfileFilteredRead)
		raw, err := s.sourceItemsForKind(ctx, filter, query, read, 0)
		if err != nil {
			return nil, err
		}
		kept, err := s.applyMuteFilterLocked(ctx, s.catalogR, query.ProfileID, filter, raw)
		if err != nil {
			return nil, err
		}
		if watched != nil {
			kept = dropWatched(kept, watched)
		}
		if len(kept) >= want || len(raw) < read || read == maxProfileFilteredRead {
			return pageOf(kept, limit, offset), nil
		}
		read *= 2
	}
}

// profileHasMutesLocked reports whether the Profile has any Mute. The caller holds s.mu.
func (s *Store) profileHasMutesLocked(ctx context.Context, profileID ProfileID) (bool, error) {
	var found bool
	err := s.eventsR.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM profile_mute WHERE profile_id = ?)`, profileID).Scan(&found)
	return found, err
}

// pageOf returns items[offset : offset+limit], clamped.
func pageOf(items []SourceItem, limit, offset int) []SourceItem {
	if offset >= len(items) {
		return []SourceItem{}
	}
	return items[offset:min(len(items), offset+limit)]
}

// effectiveBrowseWatchLocked returns the watch filter a browse runs with: the
// one it names, or unwatched when it names none and the Profile hides watched.
// The caller holds s.mu.
func (s *Store) effectiveBrowseWatchLocked(ctx context.Context, query LibraryBrowseQuery) (string, error) {
	if query.Watch != "" {
		return query.Watch, nil
	}
	hide, err := s.profileHideWatchedLocked(ctx, query.ProfileID)
	if err != nil || !hide {
		return "", err
	}
	return LibraryBrowseWatchUnwatched, nil
}

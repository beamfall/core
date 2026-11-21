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

// hideWatchedWindow returns the Media a Hide watched page leaves out, and the
// raw window to read so that, once they are dropped, the page still holds
// limit items from offset onward: every finished Media ahead of the window
// could shift it, so the read starts at the top and runs that many further.
// No hide, or nothing finished, keeps the window as asked. The caller holds s.mu.
func (s *Store) hideWatchedWindowLocked(ctx context.Context, query SourceQuery, limit, offset int) (watched map[MediaID]bool, readLimit, readOffset int, err error) {
	if query.ProfileID == "" || strings.TrimSpace(query.Query) != "" || !hideWatchedApplies(query.Kind) {
		return nil, limit, offset, nil
	}
	hide, err := s.profileHideWatchedLocked(ctx, query.ProfileID)
	if err != nil || !hide {
		return nil, limit, offset, err
	}
	watched, err = s.watchedMediaLocked(ctx, query.ProfileID)
	if err != nil || len(watched) == 0 {
		return nil, limit, offset, err
	}
	return watched, offset + limit + len(watched), 0, nil
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

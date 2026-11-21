package store

import (
	"context"
	"database/sql"
	"errors"
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

// dropWatchedLocked removes from items the Media profileID has finished.
// Entity items pass through: their finished state is judged by browse, not
// here. The caller holds s.mu.
func (s *Store) dropWatchedLocked(ctx context.Context, profileID ProfileID, items []SourceItem) ([]SourceItem, error) {
	if len(items) == 0 {
		return items, nil
	}
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]SourceItem, 0, len(items))
	for _, item := range items {
		if item.Kind != "entity" && watched[item.ID] {
			continue
		}
		out = append(out, item)
	}
	return out, nil
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

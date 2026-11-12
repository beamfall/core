package store

import (
	"context"
	"regexp"
)

// ProfileAvatarPrefKey is the reserved per-Profile preference that holds the
// Profile's chosen avatar token (CORE-1196). It lives in profile_client_prefs
// so it needs no schema change, but unlike other preferences it is shared by
// every client and shown to other Profiles in the picker, so the value is
// restricted to a short token. Core never interprets the token: a client that
// does not recognise it shows its generated avatar instead.
const ProfileAvatarPrefKey = "profile.avatar"

var profileAvatarToken = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,47}$`)

func validateProfileAvatar(value string) error {
	if value == "" || profileAvatarToken.MatchString(value) {
		return nil
	}
	return ValidationError("avatar must be a short token of lowercase letters, digits, \".\" and \"-\"")
}

// ProfileAvatars returns the chosen avatar token of every Profile that has
// one. Profiles without a choice are absent.
func (s *Store) ProfileAvatars(ctx context.Context) (map[ProfileID]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT profile_id, value FROM profile_client_prefs WHERE key = ?
	`, ProfileAvatarPrefKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	avatars := map[ProfileID]string{}
	for rows.Next() {
		var id ProfileID
		var value string
		if err := rows.Scan(&id, &value); err != nil {
			return nil, err
		}
		avatars[id] = value
	}
	return avatars, rows.Err()
}

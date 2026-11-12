package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProfileAvatarRoundTripAndClear(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

	if err := store.SetClientPref(ctx, "profile-default", ProfileAvatarPrefKey, "rocket.3", now); err != nil {
		t.Fatalf("SetClientPref returned error: %v", err)
	}
	// Another preference on the same Profile is not an avatar.
	if err := store.SetClientPref(ctx, "profile-default", "web.theme", "dark", now); err != nil {
		t.Fatalf("SetClientPref returned error: %v", err)
	}
	avatars, err := store.ProfileAvatars(ctx)
	if err != nil {
		t.Fatalf("ProfileAvatars returned error: %v", err)
	}
	if len(avatars) != 1 || avatars["profile-default"] != "rocket.3" {
		t.Fatalf("avatars = %v, want only profile-default=rocket.3", avatars)
	}

	if err := store.SetClientPref(ctx, "profile-default", ProfileAvatarPrefKey, "", now); err != nil {
		t.Fatalf("clearing the avatar returned error: %v", err)
	}
	avatars, err = store.ProfileAvatars(ctx)
	if err != nil {
		t.Fatalf("ProfileAvatars returned error: %v", err)
	}
	if len(avatars) != 0 {
		t.Fatalf("avatars after clear = %v, want none", avatars)
	}
}

func TestProfileAvatarRefusesAnythingButAToken(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

	if err := store.SetClientPref(ctx, "profile-default", ProfileAvatarPrefKey, "star.1", now); err != nil {
		t.Fatalf("SetClientPref returned error: %v", err)
	}
	for name, value := range map[string]string{
		"url":       "https://example.test/a.png",
		"markup":    "<img src=x>",
		"uppercase": "Rocket",
		"space":     "rocket 3",
		"leading":   ".rocket",
		"too long":  strings.Repeat("a", 49),
	} {
		if err := store.SetClientPref(ctx, "profile-default", ProfileAvatarPrefKey, value, now); err == nil {
			t.Errorf("%s: SetClientPref accepted %q", name, value)
		}
	}
	avatars, err := store.ProfileAvatars(ctx)
	if err != nil {
		t.Fatalf("ProfileAvatars returned error: %v", err)
	}
	if avatars["profile-default"] != "star.1" {
		t.Fatalf("avatar = %q, want the last accepted value star.1", avatars["profile-default"])
	}
}

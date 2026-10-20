package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestClientPrefsRoundTrip(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	if err := store.SetClientPref(ctx, "profile-default", "web.subtitle-style", `{"size":"large"}`, now); err != nil {
		t.Fatalf("SetClientPref returned error: %v", err)
	}
	if err := store.SetClientPref(ctx, "profile-default", "tv.theme", "dark", now.Add(time.Minute)); err != nil {
		t.Fatalf("SetClientPref returned error: %v", err)
	}

	prefs, err := store.ClientPrefs(ctx, "profile-default")
	if err != nil {
		t.Fatalf("ClientPrefs returned error: %v", err)
	}
	if len(prefs) != 2 {
		t.Fatalf("len(prefs) = %d, want 2", len(prefs))
	}
	// Ordered by key.
	if prefs[0].Key != "tv.theme" || prefs[0].Value != "dark" {
		t.Fatalf("prefs[0] = %+v", prefs[0])
	}
	if prefs[1].Key != "web.subtitle-style" || prefs[1].Value != `{"size":"large"}` {
		t.Fatalf("prefs[1] = %+v", prefs[1])
	}
	if !prefs[0].UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("prefs[0].UpdatedAt = %v", prefs[0].UpdatedAt)
	}
}

func TestClientPrefUpsertAndDelete(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	if err := store.SetClientPref(ctx, "profile-default", "web.theme", "light", now); err != nil {
		t.Fatal(err)
	}
	if err := store.SetClientPref(ctx, "profile-default", "web.theme", "dark", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	prefs, err := store.ClientPrefs(ctx, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if len(prefs) != 1 || prefs[0].Value != "dark" || !prefs[0].UpdatedAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("after upsert prefs = %+v", prefs)
	}

	// Empty value deletes — clients reset to their own defaults.
	if err := store.SetClientPref(ctx, "profile-default", "web.theme", "", now); err != nil {
		t.Fatal(err)
	}
	prefs, err = store.ClientPrefs(ctx, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if len(prefs) != 0 {
		t.Fatalf("after delete prefs = %+v", prefs)
	}
}

func TestClientPrefsAreScopedPerProfile(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	if err := store.SetClientPref(ctx, "profile-default", "web.theme", "dark", now); err != nil {
		t.Fatal(err)
	}
	prefs, err := store.ClientPrefs(ctx, "profile-managed")
	if err != nil {
		t.Fatal(err)
	}
	if len(prefs) != 0 {
		t.Fatalf("other profile sees prefs = %+v", prefs)
	}
}

func TestSetClientPrefValidation(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	cases := map[string]struct {
		profile ProfileID
		key     string
		value   string
	}{
		"missing profile":  {"", "web.theme", "dark"},
		"unknown profile":  {"profile-ghost", "web.theme", "dark"},
		"empty key":        {"profile-default", "", "dark"},
		"unnamespaced key": {"profile-default", "theme", "dark"},
		"dot-prefixed key": {"profile-default", ".theme", "dark"},
		"oversized key":    {"profile-default", "web." + strings.Repeat("k", 200), "dark"},
		"oversized value":  {"profile-default", "web.theme", strings.Repeat("v", 5000)},
	}
	for name, tc := range cases {
		if err := store.SetClientPref(ctx, tc.profile, tc.key, tc.value, now); err == nil {
			t.Errorf("%s: SetClientPref accepted", name)
		}
	}
}

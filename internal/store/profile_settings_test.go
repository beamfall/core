package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProfilePlaybackPolicyDefaults(t *testing.T) {
	store := openSeeded(t)

	profile, err := store.Profile(context.Background(), "profile-default")
	if err != nil {
		t.Fatalf("Profile returned error: %v", err)
	}
	if profile.MaxConcurrentStreams != nil {
		t.Fatalf("MaxConcurrentStreams = %v, want nil (unlimited)", *profile.MaxConcurrentStreams)
	}
	if !profile.TranscodeAllowed {
		t.Fatal("TranscodeAllowed = false, want true by default")
	}
	if profile.Disabled {
		t.Fatal("Disabled = true, want false by default")
	}
	if len(profile.AudioLanguages) != 0 || len(profile.SubtitleLanguages) != 0 {
		t.Fatalf("language lists = %v / %v, want empty", profile.AudioLanguages, profile.SubtitleLanguages)
	}
	if profile.SubtitleMode != SubtitleModeOff {
		t.Fatalf("SubtitleMode = %q, want off", profile.SubtitleMode)
	}
	if profile.PreferSDH {
		t.Fatal("PreferSDH = true, want false by default")
	}
}

func TestUpdateProfileSettingsPersistsAllFields(t *testing.T) {
	store := openSeeded(t)
	limit := 2
	transcode := false
	disabled := true
	audio := []string{"ja", "en"}
	subtitles := []string{"en"}
	mode := SubtitleModeSmart
	sdh := true

	updated, err := store.UpdateProfileSettings(context.Background(), "profile-default", ProfileSettings{
		MaxConcurrentStreams: &limit,
		TranscodeAllowed:     &transcode,
		Disabled:             &disabled,
		AudioLanguages:       &audio,
		SubtitleLanguages:    &subtitles,
		SubtitleMode:         &mode,
		PreferSDH:            &sdh,
	})
	if err != nil {
		t.Fatalf("UpdateProfileSettings returned error: %v", err)
	}
	if updated.MaxConcurrentStreams == nil || *updated.MaxConcurrentStreams != 2 {
		t.Fatalf("MaxConcurrentStreams = %v, want 2", updated.MaxConcurrentStreams)
	}

	profile, err := store.Profile(context.Background(), "profile-default")
	if err != nil {
		t.Fatalf("Profile returned error: %v", err)
	}
	if profile.MaxConcurrentStreams == nil || *profile.MaxConcurrentStreams != 2 {
		t.Fatalf("persisted MaxConcurrentStreams = %v, want 2", profile.MaxConcurrentStreams)
	}
	if profile.TranscodeAllowed {
		t.Fatal("TranscodeAllowed = true, want false")
	}
	if !profile.Disabled {
		t.Fatal("Disabled = false, want true")
	}
	if len(profile.AudioLanguages) != 2 || profile.AudioLanguages[0] != "ja" || profile.AudioLanguages[1] != "en" {
		t.Fatalf("AudioLanguages = %v, want [ja en] (order preserved)", profile.AudioLanguages)
	}
	if len(profile.SubtitleLanguages) != 1 || profile.SubtitleLanguages[0] != "en" {
		t.Fatalf("SubtitleLanguages = %v, want [en]", profile.SubtitleLanguages)
	}
	if profile.SubtitleMode != SubtitleModeSmart {
		t.Fatalf("SubtitleMode = %q, want smart", profile.SubtitleMode)
	}
	if !profile.PreferSDH {
		t.Fatal("PreferSDH = false, want true")
	}
}

func TestUpdateProfileSettingsPartialWriteLeavesOthersUnchanged(t *testing.T) {
	store := openSeeded(t)
	audio := []string{"de"}
	if _, err := store.UpdateProfileSettings(context.Background(), "profile-default", ProfileSettings{AudioLanguages: &audio}); err != nil {
		t.Fatalf("seeding audio languages: %v", err)
	}

	sdh := true
	if _, err := store.UpdateProfileSettings(context.Background(), "profile-default", ProfileSettings{PreferSDH: &sdh}); err != nil {
		t.Fatalf("UpdateProfileSettings returned error: %v", err)
	}

	profile, err := store.Profile(context.Background(), "profile-default")
	if err != nil {
		t.Fatalf("Profile returned error: %v", err)
	}
	if len(profile.AudioLanguages) != 1 || profile.AudioLanguages[0] != "de" {
		t.Fatalf("AudioLanguages = %v, want [de] untouched", profile.AudioLanguages)
	}
	if !profile.PreferSDH {
		t.Fatal("PreferSDH = false, want true")
	}
}

func TestUpdateProfileSettingsZeroClearsStreamCap(t *testing.T) {
	store := openSeeded(t)
	limit := 3
	if _, err := store.UpdateProfileSettings(context.Background(), "profile-default", ProfileSettings{MaxConcurrentStreams: &limit}); err != nil {
		t.Fatalf("setting cap: %v", err)
	}
	clear := 0
	updated, err := store.UpdateProfileSettings(context.Background(), "profile-default", ProfileSettings{MaxConcurrentStreams: &clear})
	if err != nil {
		t.Fatalf("clearing cap: %v", err)
	}
	if updated.MaxConcurrentStreams != nil {
		t.Fatalf("MaxConcurrentStreams = %v, want nil after clearing", *updated.MaxConcurrentStreams)
	}
}

func TestUpdateProfileSettingsValidation(t *testing.T) {
	store := openSeeded(t)
	negative := -1
	badMode := SubtitleMode("loud")
	emptyLanguage := []string{"en", " "}

	cases := []struct {
		name     string
		settings ProfileSettings
	}{
		{"no fields", ProfileSettings{}},
		{"negative cap", ProfileSettings{MaxConcurrentStreams: &negative}},
		{"unknown subtitle mode", ProfileSettings{SubtitleMode: &badMode}},
		{"empty language entry", ProfileSettings{SubtitleLanguages: &emptyLanguage}},
	}
	for _, tc := range cases {
		if _, err := store.UpdateProfileSettings(context.Background(), "profile-default", tc.settings); err == nil {
			t.Fatalf("%s: UpdateProfileSettings accepted invalid settings", tc.name)
		}
	}
}

func TestUpdateProfileSettingsUnknownProfile(t *testing.T) {
	store := openSeeded(t)
	sdh := true
	if _, err := store.UpdateProfileSettings(context.Background(), "profile-missing", ProfileSettings{PreferSDH: &sdh}); err == nil {
		t.Fatal("UpdateProfileSettings accepted unknown profile")
	}
}

func disableProfile(t *testing.T, store *Store, id ProfileID) {
	t.Helper()
	disabled := true
	if _, err := store.UpdateProfileSettings(context.Background(), id, ProfileSettings{Disabled: &disabled}); err != nil {
		t.Fatalf("disabling profile: %v", err)
	}
}

func TestEnrollDeviceRefusesDisabledProfile(t *testing.T) {
	store := openSeeded(t)
	disableProfile(t, store, "profile-default")

	err := store.EnrollDevice(
		context.Background(),
		Device{ID: "device-new", Name: "Living Room", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("test-device-secret")},
		Session{ID: "session-new", DeviceID: "device-new", ProfileID: "profile-default"},
	)
	if !errors.Is(err, ErrProfileDisabled) {
		t.Fatalf("EnrollDevice error = %v, want ErrProfileDisabled", err)
	}
}

func TestCreatePairingCodeRefusesDisabledProfile(t *testing.T) {
	store := openSeeded(t)
	disableProfile(t, store, "profile-default")

	err := store.CreatePairingCode(context.Background(), PairingCode{
		Code:       "ABCDEF",
		ProfileID:  "profile-default",
		DeviceRole: DeviceRoleViewer,
		ExpiresAt:  time.Now().Add(time.Hour),
	})
	if !errors.Is(err, ErrProfileDisabled) {
		t.Fatalf("CreatePairingCode error = %v, want ErrProfileDisabled", err)
	}
}

func TestRedeemPairingCodeRefusesProfileDisabledAfterMint(t *testing.T) {
	store := openSeeded(t)
	now := time.Now().UTC()
	if err := store.CreatePairingCode(context.Background(), PairingCode{
		Code:       "ABCDEF",
		ProfileID:  "profile-default",
		DeviceRole: DeviceRoleViewer,
		ExpiresAt:  now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreatePairingCode returned error: %v", err)
	}
	disableProfile(t, store, "profile-default")

	_, err := store.RedeemPairingCode(context.Background(), "ABCDEF", now,
		Device{ID: "device-new", Name: "Living Room", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("test-device-secret")},
		Session{ID: "session-new", DeviceID: "device-new"},
	)
	if !errors.Is(err, ErrProfileDisabled) {
		t.Fatalf("RedeemPairingCode error = %v, want ErrProfileDisabled", err)
	}
}

func TestDisableProfilePreservesEngagementHistory(t *testing.T) {
	store := openSeeded(t)
	if err := store.UpsertPlaybackProgress(context.Background(), PlaybackProgress{
		ProfileID:       "profile-default",
		MediaID:         "media-alpha",
		PositionSeconds: 42,
		DurationSeconds: 120,
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatalf("UpsertPlaybackProgress returned error: %v", err)
	}
	disableProfile(t, store, "profile-default")

	progress, ok, err := store.PlaybackProgressFor(context.Background(), "profile-default", "media-alpha")
	if err != nil {
		t.Fatalf("PlaybackProgressFor returned error: %v", err)
	}
	if !ok || progress.PositionSeconds != 42 {
		t.Fatalf("progress after disable = %+v ok=%v, want position 42 preserved", progress, ok)
	}
}

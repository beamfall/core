package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPlayerControllersMembershipAndLiveEvaluation(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	all := LibraryFilter{Revealed: true, AllLibraries: true}

	player, err := st.CreateProfile(ctx, "Karaoke TV", ProfilePlayer, all)
	if err != nil {
		t.Fatalf("CreateProfile(player): %v", err)
	}
	otherPlayer, err := st.CreateProfile(ctx, "Lounge TV", ProfilePlayer, all)
	if err != nil {
		t.Fatalf("CreateProfile(otherPlayer): %v", err)
	}
	adult2, err := st.CreateProfile(ctx, "Partner", ProfileAdult, all)
	if err != nil {
		t.Fatalf("CreateProfile(adult2): %v", err)
	}
	managed2, err := st.CreateProfile(ctx, "Kid Two", ProfileManaged, all)
	if err != nil {
		t.Fatalf("CreateProfile(managed2): %v", err)
	}

	// Default policy: allAdults=true, empty include/exclude.
	cfg, err := st.PlayerControllers(ctx, player.ID)
	if err != nil {
		t.Fatalf("PlayerControllers default: %v", err)
	}
	if !cfg.AllAdults || len(cfg.Include) != 0 || len(cfg.Exclude) != 0 {
		t.Fatalf("default PlayerControllers = %+v", cfg)
	}

	// Validation checks.
	if _, err := st.SetPlayerControllers(ctx, "profile-default", true, nil, nil); err == nil {
		t.Fatal("SetPlayerControllers on adult profile succeeded")
	}
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, []ProfileID{"no-such-profile"}, nil); err == nil {
		t.Fatal("SetPlayerControllers with unknown include profile succeeded")
	}
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, []ProfileID{otherPlayer.ID}, nil); err == nil {
		t.Fatal("SetPlayerControllers with player profile in include succeeded")
	}
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, nil, []ProfileID{otherPlayer.ID}); err == nil {
		t.Fatal("SetPlayerControllers with player profile in exclude succeeded")
	}
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, []ProfileID{adult2.ID}, []ProfileID{adult2.ID}); err == nil {
		t.Fatal("SetPlayerControllers with overlap in include/exclude succeeded")
	}

	// Configure allAdults=true, include=[profile-managed], exclude=[adult2].
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, []ProfileID{"profile-managed"}, []ProfileID{adult2.ID}); err != nil {
		t.Fatalf("SetPlayerControllers: %v", err)
	}

	// Later-created adult joins automatically while allAdults=true.
	laterAdult, err := st.CreateProfile(ctx, "Later Adult", ProfileAdult, all)
	if err != nil {
		t.Fatalf("CreateProfile(laterAdult): %v", err)
	}

	cases := []struct {
		name   string
		caller ProfileID
		want   bool
	}{
		{"owner adult via allAdults", "profile-default", true},
		{"later-created adult via allAdults", laterAdult.ID, true},
		{"excluded adult denied", adult2.ID, false},
		{"included managed allowed", "profile-managed", true},
		{"unlisted managed denied", managed2.ID, false},
		{"self player denied", player.ID, false},
		{"other player denied", otherPlayer.ID, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := st.IsPlayerController(ctx, player.ID, tc.caller)
			if err != nil {
				t.Fatalf("IsPlayerController(%s): %v", tc.caller, err)
			}
			if got != tc.want {
				t.Fatalf("IsPlayerController(%s) = %v, want %v", tc.caller, got, tc.want)
			}
		})
	}

	// Demoting laterAdult -> managed stops its controller access immediately.
	managedKind := ProfileManaged
	if _, err := st.UpdateProfileSettings(ctx, laterAdult.ID, ProfileSettings{Kind: &managedKind}); err != nil {
		t.Fatalf("demote laterAdult: %v", err)
	}
	if got, err := st.IsPlayerController(ctx, player.ID, laterAdult.ID); err != nil || got {
		t.Fatalf("demoted adult still controller: got=%v err=%v", got, err)
	}

	// Switching allAdults=false keeps only explicit include (profile-managed).
	if _, err := st.SetPlayerControllers(ctx, player.ID, false, []ProfileID{"profile-managed"}, nil); err != nil {
		t.Fatalf("SetPlayerControllers(allAdults=false): %v", err)
	}
	if got, err := st.IsPlayerController(ctx, player.ID, "profile-default"); err != nil || got {
		t.Fatalf("profile-default with allAdults=false: got=%v err=%v", got, err)
	}
	if got, err := st.IsPlayerController(ctx, player.ID, "profile-managed"); err != nil || !got {
		t.Fatalf("included profile-managed with allAdults=false: got=%v err=%v", got, err)
	}

	// Deleting included profile cascades from player_controller.
	if err := st.DeleteProfile(ctx, managed2.ID); err != nil {
		t.Fatalf("DeleteProfile(managed2): %v", err)
	}
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, []ProfileID{"profile-managed"}, nil); err != nil {
		t.Fatalf("re-set: %v", err)
	}
	if err := st.DeleteProfile(ctx, "profile-managed"); err != nil {
		t.Fatalf("DeleteProfile(profile-managed): %v", err)
	}
	afterDelete, err := st.PlayerControllers(ctx, player.ID)
	if err != nil {
		t.Fatalf("PlayerControllers after delete: %v", err)
	}
	if len(afterDelete.Include) != 0 {
		t.Fatalf("cascade delete failed, include = %v", afterDelete.Include)
	}
}

func TestPlayerControlCommitGuardDetectsRebindAndRemoval(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	all := LibraryFilter{Revealed: true, AllLibraries: true}

	player, err := st.CreateProfile(ctx, "Bar TV", ProfilePlayer, all)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		Device{
			ID: "device-bar", Name: "Bar TV", Role: DeviceRoleViewer,
			SecretHash: HashDeviceSecret("bar-secret"), PlayerProfileID: player.ID,
			PlayerDisplayName: "Bar TV", PlayerBindingGeneration: 1,
		},
		Session{ID: "session-bar", DeviceID: "device-bar", ProfileID: player.ID, ExpiresAt: now.Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}

	guard := PlayerControlGuard{
		PlayerProfileID:   player.ID,
		CallerProfileID:   "profile-default",
		BindingGeneration: 1,
	}
	payload := SessionControlPayload{
		Command:    SessionCommandPlayMedia,
		MediaID:    "media-alpha",
		FromDevice: "device-mac",
		ProfileID:  player.ID,
	}

	// Stale generation is refused without writing outbox or bumping revision.
	badGen := guard
	badGen.BindingGeneration = 2
	if _, err := st.ControlRendererSessionForPlayer(ctx, "session-bar", 0, "cmd_guard_bad_gen_01", payload, nil, now, badGen); !errors.Is(err, ErrPlayerControlUnauthorized) {
		t.Fatalf("stale generation err = %v, want ErrPlayerControlUnauthorized", err)
	}
	if rev, err := st.RendererSessionRevision(ctx, "session-bar"); err != nil || rev != 0 {
		t.Fatalf("revision after rejected guard = %d, %v", rev, err)
	}

	// Matching guard succeeds and updates last-seen touch.
	res, err := st.ControlRendererSessionForPlayer(ctx, "session-bar", 0, "cmd_guard_valid_0001", payload, nil, now, guard)
	if err != nil || res.Revision != 1 {
		t.Fatalf("valid guard = %+v, %v", res, err)
	}
	if err := st.TouchDeviceLastSeenBySession(ctx, "session-bar", now.Add(5*time.Second)); err != nil {
		t.Fatalf("TouchDeviceLastSeenBySession: %v", err)
	}
	displays, err := st.PlayerBoundDevices(ctx)
	if err != nil {
		t.Fatalf("PlayerBoundDevices: %v", err)
	}
	if len(displays) != 1 || displays[0].SessionID != "session-bar" || displays[0].LastSeenAt.IsZero() {
		t.Fatalf("PlayerBoundDevices = %+v", displays)
	}

	// Removing controller from list blocks commit even before rebind.
	if _, err := st.SetPlayerControllers(ctx, player.ID, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ControlRendererSessionForPlayer(ctx, "session-bar", 1, "cmd_guard_removed_01", payload, nil, now, guard); !errors.Is(err, ErrPlayerControlUnauthorized) {
		t.Fatalf("removed controller guard err = %v, want ErrPlayerControlUnauthorized", err)
	}
}

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPlayerProfileKindFixityAndPolicies(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)

	// Restricted library exists in seed ("library-archive"); player profiles
	// must never receive restricted libraries at create or via SetProfileLibraries.
	revealedAll := LibraryFilter{Revealed: true, AllLibraries: true}
	player, err := st.CreateProfile(ctx, "Lounge TV", ProfilePlayer, revealedAll)
	if err != nil {
		t.Fatalf("CreateProfile(player): %v", err)
	}
	if player.Kind != ProfilePlayer || player.WalkUpEnabled {
		t.Fatalf("player profile = %+v, want kind=player walkUpEnabled=false", player)
	}
	for _, libID := range player.Library {
		if libID == "library-archive" {
			t.Fatal("CreateProfile(player) granted restricted library-archive")
		}
	}
	if _, err := st.SetProfileLibraries(ctx, player.ID, []LibraryID{"library-local", "library-archive"}); err == nil {
		t.Fatal("SetProfileLibraries granted restricted library to player profile")
	}

	// Kind fixity: player cannot become adult/managed; adult/managed cannot become player.
	adultKind := ProfileAdult
	managedKind := ProfileManaged
	playerKind := ProfilePlayer
	if _, err := st.UpdateProfileSettings(ctx, player.ID, ProfileSettings{Kind: &adultKind}); err == nil {
		t.Fatal("UpdateProfileSettings changed player -> adult")
	}
	if _, err := st.UpdateProfileSettings(ctx, player.ID, ProfileSettings{Kind: &managedKind}); err == nil {
		t.Fatal("UpdateProfileSettings changed player -> managed")
	}
	if _, err := st.UpdateProfileSettings(ctx, "profile-managed", ProfileSettings{Kind: &playerKind}); err == nil {
		t.Fatal("UpdateProfileSettings changed managed -> player")
	}
	nonOwnerAdult, err := st.CreateProfile(ctx, "Other Adult", ProfileAdult, revealedAll)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateProfileSettings(ctx, nonOwnerAdult.ID, ProfileSettings{Kind: &playerKind}); err == nil {
		t.Fatal("UpdateProfileSettings changed adult -> player")
	}

	// Reveal and PIN paths must refuse player profiles.
	if err := st.SetProfileRevealPIN(ctx, player.ID, "1234"); !errors.Is(err, ErrNoRevealPath) {
		t.Fatalf("SetProfileRevealPIN(player) = %v, want ErrNoRevealPath", err)
	}
	if err := st.VerifyRevealPIN(ctx, player.ID, "1234"); !errors.Is(err, ErrNoRevealPath) {
		t.Fatalf("VerifyRevealPIN(player) = %v, want ErrNoRevealPath", err)
	}
	if set, err := st.ProfileRevealPINSet(ctx, player.ID); err != nil || set {
		t.Fatalf("ProfileRevealPINSet(player) = %v, %v, want false", set, err)
	}
	if err := st.SetProfileEntryPIN(ctx, player.ID, "2468"); !errors.Is(err, ErrNoProfileEntryPIN) {
		t.Fatalf("SetProfileEntryPIN(player) = %v, want ErrNoProfileEntryPIN", err)
	}
	if err := st.VerifyProfileEntryPIN(ctx, player.ID, "2468"); !errors.Is(err, ErrNoProfileEntryPIN) {
		t.Fatalf("VerifyProfileEntryPIN(player) = %v, want ErrNoProfileEntryPIN", err)
	}

	// switchProfile into a player profile is refused with "profile not found".
	if err := st.SetSessionProfile(ctx, "session-default", player.ID); err == nil || err.Error() != "profile not found" {
		t.Fatalf("SetSessionProfile(player) = %v, want profile not found", err)
	}
	if err := st.SetSessionProfileWithEntryPIN(ctx, "session-default", player.ID, ""); err == nil || err.Error() != "profile not found" {
		t.Fatalf("SetSessionProfileWithEntryPIN(player) = %v, want profile not found", err)
	}
}

func TestDevicePlayerBindingLifecycleAndRenewal(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	all := LibraryFilter{Revealed: true, AllLibraries: true}
	playerA, err := st.CreateProfile(ctx, "Karaoke TV", ProfilePlayer, all)
	if err != nil {
		t.Fatal(err)
	}
	playerB, err := st.CreateProfile(ctx, "Garage TV", ProfilePlayer, all)
	if err != nil {
		t.Fatal(err)
	}

	if err := st.EnrollDevice(ctx,
		Device{ID: "device-sat", Name: "Apple TV", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("sat-secret")},
		Session{ID: "session-sat-1", DeviceID: "device-sat", ProfileID: "profile-default", ExpiresAt: now.Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}

	// Seed a stream_token_ledger grant and a pending session.control outbox event for session-sat-1.
	if err := st.RecordStreamToken(ctx, "session-sat-1", "profile-default", "device-sat", "media-direct", "media-alpha", false, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendSessionControlEvent(ctx, "session-sat-1", SessionControlPayload{
		Command: SessionCommandPlayMedia, MediaID: "media-alpha", FromDevice: "device-mac", ProfileID: "profile-default",
	}, now); err != nil {
		t.Fatal(err)
	}

	// First binding of already-enrolled device increments generation (0 -> 1)
	// and ends all sessions + cancels ledger & pending control rows.
	bound, err := st.SetDevicePlayerBinding(ctx, "device-sat", playerA.ID, "Karaoke room TV")
	if err != nil {
		t.Fatalf("SetDevicePlayerBinding: %v", err)
	}
	if bound.PlayerProfileID != playerA.ID || bound.PlayerDisplayName != "Karaoke room TV" || bound.PlayerBindingGeneration != 1 {
		t.Fatalf("bound device = %+v", bound)
	}
	if _, err := st.Session(ctx, "session-sat-1"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("old session after bind = %v, want ErrSessionNotFound", err)
	}
	var ledgerCount, outboxControlCount int
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM stream_token_ledger WHERE session_id = 'session-sat-1'`).Scan(&ledgerCount); err != nil {
		t.Fatal(err)
	}
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_outbox WHERE kind = ? AND aggregate_id = 'session-sat-1'`, SessionControlKind).Scan(&outboxControlCount); err != nil {
		t.Fatal(err)
	}
	if ledgerCount != 0 || outboxControlCount != 0 {
		t.Fatalf("stale artifacts survived bind: ledger=%d outbox=%d", ledgerCount, outboxControlCount)
	}

	// Renewal for bound device issues a Session on playerA even though previous session ended.
	renewed, err := st.RenewDeviceSession(ctx, "device-sat", "session-sat-2", now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("RenewDeviceSession(bound): %v", err)
	}
	if renewed.ProfileID != playerA.ID {
		t.Fatalf("renewed.ProfileID = %q, want %q", renewed.ProfileID, playerA.ID)
	}
	if err := st.SetSessionRevealed(ctx, renewed.ID, true); !errors.Is(err, ErrNoRevealPath) {
		t.Fatalf("SetSessionRevealed on player session = %v, want ErrNoRevealPath", err)
	}
	if err := st.SetSessionProfile(ctx, renewed.ID, "profile-default"); err == nil {
		t.Fatal("SetSessionProfile succeeded from bound device session")
	}

	// Append play-media control command for bound session -> records commanded media for 12h.
	if err := st.AppendSessionControlEvent(ctx, renewed.ID, SessionControlPayload{
		Command: SessionCommandPlayMedia, MediaID: "media-alpha", FromDevice: "device-mac", ProfileID: playerA.ID,
	}, now); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.IsPlayerCommandedMedia(ctx, renewed.ID, "media-alpha", now.Add(11*time.Hour)); err != nil || !ok {
		t.Fatalf("IsPlayerCommandedMedia within 12h = %v, %v, want true", ok, err)
	}
	if ok, err := st.IsPlayerCommandedMedia(ctx, renewed.ID, "media-alpha", now.Add(13*time.Hour)); err != nil || ok {
		t.Fatalf("IsPlayerCommandedMedia past 12h = %v, %v, want false", ok, err)
	}

	// Changing binding increments generation (1 -> 2) and ends session-sat-2 + clears commanded media.
	rebound, err := st.SetDevicePlayerBinding(ctx, "device-sat", playerB.ID, "Garage display")
	if err != nil {
		t.Fatalf("SetDevicePlayerBinding(change): %v", err)
	}
	if rebound.PlayerProfileID != playerB.ID || rebound.PlayerBindingGeneration != 2 {
		t.Fatalf("rebound = %+v", rebound)
	}
	if _, err := st.Session(ctx, renewed.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("session after rebind = %v, want ErrSessionNotFound", err)
	}
	if ok, err := st.IsPlayerCommandedMedia(ctx, renewed.ID, "media-alpha", now); err != nil || ok {
		t.Fatalf("commanded media survived rebind: %v, %v", ok, err)
	}

	// Renewing again issues on playerB, then clearing binding increments generation (2 -> 3)
	// and ends sessions so subsequent renewal fails with ErrNoDeviceSession.
	renewedB, err := st.RenewDeviceSession(ctx, "device-sat", "session-sat-3", now.Add(24*time.Hour))
	if err != nil || renewedB.ProfileID != playerB.ID {
		t.Fatalf("RenewDeviceSession(playerB) = %+v, %v", renewedB, err)
	}
	cleared, err := st.ClearDevicePlayerBinding(ctx, "device-sat")
	if err != nil {
		t.Fatalf("ClearDevicePlayerBinding: %v", err)
	}
	if cleared.PlayerProfileID != "" || cleared.PlayerDisplayName != "" || cleared.PlayerBindingGeneration != 3 {
		t.Fatalf("cleared = %+v", cleared)
	}
	if _, err := st.Session(ctx, renewedB.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("session after clear = %v, want ErrSessionNotFound", err)
	}
	if _, err := st.RenewDeviceSession(ctx, "device-sat", "session-sat-4", now.Add(24*time.Hour)); !errors.Is(err, ErrNoDeviceSession) {
		t.Fatalf("RenewDeviceSession after clear = %v, want ErrNoDeviceSession", err)
	}
}

func TestDeletePlayerProfileRevokesBoundDevices(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC()

	player, err := st.CreateProfile(ctx, "Bar TV", ProfilePlayer, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		Device{
			ID: "device-bar", Name: "Bar TV", Role: DeviceRoleViewer,
			SecretHash: HashDeviceSecret("bar-secret"), PlayerProfileID: player.ID,
			PlayerDisplayName: "Home Bar TV", PlayerBindingGeneration: 1,
		},
		Session{ID: "session-bar", DeviceID: "device-bar", ProfileID: player.ID, ExpiresAt: now.Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteProfile(ctx, player.ID); err != nil {
		t.Fatalf("DeleteProfile(player): %v", err)
	}
	if _, err := st.Profile(ctx, player.ID); err == nil {
		t.Fatal("deleted player profile still readable")
	}
	dev, err := st.Device(ctx, "device-bar")
	if err != nil {
		t.Fatal(err)
	}
	if !dev.Revoked || dev.PlayerProfileID != "" {
		t.Fatalf("bound device after player profile delete = %+v, want revoked and unbound", dev)
	}
	if _, err := st.Session(ctx, "session-bar"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("session after player profile delete = %v, want ErrSessionNotFound", err)
	}
}

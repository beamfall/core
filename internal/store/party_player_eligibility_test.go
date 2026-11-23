package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func setupPartyPlayerFixture(t *testing.T, st *Store, now time.Time) (Profile, Device, Session) {
	t.Helper()
	ctx := context.Background()
	player, err := st.CreateProfile(ctx, "Stage TV", ProfilePlayer, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("CreateProfile(player): %v", err)
	}
	dev := Device{
		ID:                      "device-stage-tv",
		Name:                    "Stage TV",
		Role:                    DeviceRoleViewer,
		Surface:                 DeviceSurfaceTVOSViewer,
		SecretHash:              HashDeviceSecret("stage-tv-secret"),
		PlayerProfileID:         player.ID,
		PlayerDisplayName:       "Stage TV",
		PlayerBindingGeneration: 1,
	}
	sess := Session{
		ID:        "session-stage-tv",
		DeviceID:  dev.ID,
		ProfileID: player.ID,
		ExpiresAt: now.Add(24 * time.Hour),
	}
	if err := st.EnrollDevice(ctx, dev, sess); err != nil {
		t.Fatalf("EnrollDevice: %v", err)
	}
	return player, dev, sess
}

func TestPartyStageSessionBindingAndTwoRegistrations(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	view := startTestParty(t, st, false)
	host := partyHostFilter()
	if err := st.AddPartyEntry(ctx, "profile-default", host, "media-alpha", "", "Avery"); err != nil {
		t.Fatal(err)
	}
	if err := st.AdvanceParty(ctx, "profile-default", host, ""); err != nil {
		t.Fatal(err)
	}

	// Same Session registers two screens, each keeping its own ticker flag.
	scr1, err := st.RegisterPartyScreen(ctx, view.Session.ID, "profile-default", "session-default", "Popout", "popout", "local", host, nil)
	if err != nil {
		t.Fatal(err)
	}
	scr2, err := st.RegisterPartyScreen(ctx, view.Session.ID, "profile-default", "session-default", "Booth", "joined", "local", host, map[string]LibraryFilter{scr1.ID: host})
	if err != nil {
		t.Fatal(err)
	}
	if scr1.Ticker || !scr2.Ticker {
		t.Fatalf("initial ticker flags = (%v, %v), want (false, true)", scr1.Ticker, scr2.Ticker)
	}

	// Both work for session-default with independent flags.
	stage1, err := st.PartyStage(ctx, view.Session.ID, scr1.ID, "session-default", host, host, nil)
	if err != nil || stage1.Ticker {
		t.Fatalf("PartyStage(scr1) = %+v, %v", stage1, err)
	}
	stage2, err := st.PartyStage(ctx, view.Session.ID, scr2.ID, "session-default", host, host, nil)
	if err != nil || !stage2.Ticker {
		t.Fatalf("PartyStage(scr2) = %+v, %v", stage2, err)
	}

	// Another Session (even on the SAME Profile!) gets the exact same non-leaking refusal as nonexistent.
	_, errOther := st.PartyStage(ctx, view.Session.ID, scr1.ID, "session-revealed", host, host, nil)
	_, errMissing := st.PartyStage(ctx, view.Session.ID, "screen-does-not-exist", "session-default", host, host, nil)
	if !errors.Is(errOther, ErrPartyInvalid) || errOther == nil || errMissing == nil || errOther.Error() != errMissing.Error() {
		t.Fatalf("other-session err = %v, missing err = %v; want identical refusal", errOther, errMissing)
	}
	// ReportPartyScreen also refuses another Session on the same Profile.
	if err := st.ReportPartyScreen(ctx, scr1.ID, "profile-default", "session-revealed", 10, 20); !errors.Is(err, ErrPartyInvalid) {
		t.Fatalf("ReportPartyScreen from other session = %v, want ErrPartyInvalid", err)
	}
}

func TestPartyStageReleaseTriggersAndD4InParty(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	st.now = func() time.Time { return now }

	player, dev, dispSess := setupPartyPlayerFixture(t, st, now)
	if _, err := st.SetProfileLibraries(ctx, player.ID, []LibraryID{"library-local"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Start party covering library-local and library-archive (revealed host).
	revealedHost := LibraryFilter{AllLibraries: true, Revealed: true}
	partyID, err := st.StartPartySession(ctx, PartyStartInput{
		Host:       "profile-default",
		HostName:   "Rowan",
		LibraryIDs: []LibraryID{"library-local", "library-archive"},
		Filter:     revealedHost,
	})
	if err != nil {
		t.Fatal(err)
	}
	display := PlayerDisplay{
		DeviceID:          dev.ID,
		DeviceName:        dev.Name,
		PlayerProfileID:   player.ID,
		DisplayName:       dev.PlayerDisplayName,
		BindingGeneration: dev.PlayerBindingGeneration,
		SessionID:         dispSess.ID,
	}

	// 1. Join reports partial library coverage (missing library-archive).
	joinView, err := st.AddPartyPlayerDisplay(ctx, "profile-default", revealedHost, display, "local")
	if err != nil {
		t.Fatalf("AddPartyPlayerDisplay: %v", err)
	}
	if joinView.CoversLibraries || len(joinView.MissingLibraryIDs) != 1 || joinView.MissingLibraryIDs[0] != "library-archive" {
		t.Fatalf("join coverage = %+v, want missing library-archive", joinView)
	}

	// 2. D4 non-stop commands fail with ErrDisplayInParty; stop commits and releases display.
	guard := PlayerControlGuard{
		PlayerProfileID:   player.ID,
		CallerProfileID:   "profile-default",
		BindingGeneration: 1,
	}
	playPayload, err := NewSessionControlPayload(SessionCommandPlayMedia, SessionControlArgs{MediaID: "media-alpha"}, "device-mac", player.ID, "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ControlRendererSessionForPlayer(ctx, dispSess.ID, 0, "cmd_in_party_play_01", playPayload, nil, now, guard); !errors.Is(err, ErrDisplayInParty) {
		t.Fatalf("play-media while in party = %v, want ErrDisplayInParty", err)
	}
	stopPayload, err := NewSessionControlPayload(SessionCommandStop, SessionControlArgs{}, "device-mac", player.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.ControlRendererSessionForPlayer(ctx, dispSess.ID, 0, "cmd_in_party_stop_01", stopPayload, nil, now, guard)
	if err != nil || res.Revision != 1 {
		t.Fatalf("stop while in party = %+v, %v", res, err)
	}
	if inParty, err := st.IsPlayerDeviceInLiveParty(ctx, dev.ID, dispSess.ID, now); err != nil || inParty {
		t.Fatalf("still in party after stop: %v, %v", inParty, err)
	}

	// Re-join and verify SetPlayerControllers removing host releases the display.
	joinView, err = st.AddPartyPlayerDisplay(ctx, "profile-default", revealedHost, display, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetPlayerControllers(ctx, player.ID, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	if inParty, err := st.IsPlayerDeviceInLiveParty(ctx, dev.ID, dispSess.ID, now); err != nil || inParty {
		t.Fatalf("still in party after SetPlayerControllers removed host: %v, %v", inParty, err)
	}
	// Re-add controller, re-join, and verify rebind releases the display.
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPartyPlayerDisplay(ctx, "profile-default", revealedHost, display, "local"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetDevicePlayerBinding(ctx, dev.ID, player.ID, "Stage TV Renamed"); err != nil {
		t.Fatal(err)
	}
	if inParty, err := st.IsPlayerDeviceInLiveParty(ctx, dev.ID, dispSess.ID, now); err != nil || inParty {
		t.Fatalf("still in party after rebind: %v, %v", inParty, err)
	}
	_ = partyID
}

func TestRelockAndSwitchProfileClearPlaybackGrants(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := st.RecordStreamToken(ctx, "session-revealed", "profile-default", "device-tv", "media-hls", "media-archive", true, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordStreamToken(ctx, "session-revealed", "profile-default", "device-tv", "media-direct", "media-alpha", false, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	restrictedIDs, err := st.SessionRestrictedStreamMediaIDs(ctx, "session-revealed")
	if err != nil || len(restrictedIDs) != 1 || restrictedIDs[0] != "media-archive" {
		t.Fatalf("SessionRestrictedStreamMediaIDs = %v, %v", restrictedIDs, err)
	}

	// Relock removes restricted grant while keeping unrestricted grant.
	if err := st.SetSessionRevealed(ctx, "session-revealed", false); err != nil {
		t.Fatal(err)
	}
	restrictedIDs, err = st.SessionRestrictedStreamMediaIDs(ctx, "session-revealed")
	if err != nil || len(restrictedIDs) != 0 {
		t.Fatalf("restricted grants after relock = %v, %v", restrictedIDs, err)
	}
	var count int
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM stream_token_ledger WHERE session_id = 'session-revealed'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("remaining stream tokens after relock = %d, %v; want 1 unrestricted", count, err)
	}

	// switchProfile clears all stream tokens for that session.
	if err := st.SetSessionProfileWithEntryPIN(ctx, "session-revealed", "profile-managed", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM stream_token_ledger WHERE session_id = 'session-revealed'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("remaining stream tokens after switchProfile = %d, %v; want 0", count, err)
	}
}

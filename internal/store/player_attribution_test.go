package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPlayerProfileEngagementWritersRefusePlayerProfile(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	all := LibraryFilter{Revealed: true, AllLibraries: true}

	player, err := st.CreateProfile(ctx, "Lounge TV", ProfilePlayer, all)
	if err != nil {
		t.Fatalf("CreateProfile(player): %v", err)
	}

	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: player.ID, MediaID: "media-alpha", PositionSeconds: 30, DurationSeconds: 120,
	}, false, ResumePolicy{Class: MediaClassVideo}); err == nil {
		t.Fatal("UpsertPlaybackProgress under player profile succeeded, want refusal")
	}
	if err := st.UpsertPlaybackProgressWithProvenance(ctx, PlaybackProgress{
		ProfileID: player.ID, MediaID: "media-alpha", PositionSeconds: 30, DurationSeconds: 120,
	}, true, ResumePolicy{Class: MediaClassVideo}, EngagementProvenance{Source: "import"}); err == nil {
		t.Fatal("UpsertPlaybackProgressWithProvenance under player profile succeeded, want refusal")
	}
	if err := st.ApplyImportedPlays(ctx, player.ID, "media-alpha", 2, time.Now().UTC(), EngagementProvenance{Source: "import"}); err == nil {
		t.Fatal("ApplyImportedPlays under player profile succeeded, want refusal")
	}
	if _, err := st.ApplyOfflineEngagementEvent(ctx, OfflineEngagementEvent{
		EventID:  "ev-player-1",
		Progress: PlaybackProgress{ProfileID: player.ID, MediaID: "media-alpha", PositionSeconds: 30, DurationSeconds: 120},
		PlayedAt: time.Now().UTC(),
		Policy:   ResumePolicy{Class: MediaClassVideo},
	}); err == nil {
		t.Fatal("ApplyOfflineEngagementEvent under player profile succeeded, want refusal")
	}
	if _, err := st.SetItemWatched(ctx, player.ID, ItemTarget{MediaID: "media-alpha"}, true, all); err == nil {
		t.Fatal("SetItemWatched under player profile succeeded, want refusal")
	}
	if err := st.SetItemWatchlisted(ctx, player.ID, ItemTarget{MediaID: "media-alpha"}, true, all); err == nil {
		t.Fatal("SetItemWatchlisted under player profile succeeded, want refusal")
	}
	if err := st.SetItemRating(ctx, player.ID, ItemTarget{MediaID: "media-alpha"}, 8, all); err == nil {
		t.Fatal("SetItemRating under player profile succeeded, want refusal")
	}
	if _, err := st.AppendEngagementEvent(ctx, EngagementEvent{
		ProfileID: player.ID, MediaID: "media-alpha", Kind: "custom.counter", Value: 1,
	}); err == nil {
		t.Fatal("AppendEngagementEvent under player profile succeeded, want refusal")
	}
	if _, err := st.SetContinueWatchingHidden(ctx, player.ID, "media-alpha", true, all); err == nil {
		t.Fatal("SetContinueWatchingHidden under player profile succeeded, want refusal")
	}
	if err := st.SetMediaFavorite(ctx, player.ID, "media-alpha", true, all); err == nil {
		t.Fatal("SetMediaFavorite under player profile succeeded, want refusal")
	}

	if _, ok, err := st.PlaybackProgressFor(ctx, player.ID, "media-alpha"); err != nil || ok {
		t.Fatalf("PlaybackProgressFor(player) = %v, %v, want false", ok, err)
	}
}

func TestPlayerCommandedOriginatorResumeAndGrantLifecycle(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	all := LibraryFilter{Revealed: true, AllLibraries: true}

	player, err := st.CreateProfile(ctx, "Karaoke TV", ProfilePlayer, all)
	if err != nil {
		t.Fatal(err)
	}
	alice, err := st.CreateProfile(ctx, "Alice", ProfileAdult, all)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateProfile(ctx, "Bob", ProfileAdult, all)
	if err != nil {
		t.Fatal(err)
	}

	if err := st.EnrollDevice(ctx,
		Device{
			ID: "device-karaoke-tv", Name: "Karaoke TV", Role: DeviceRoleViewer,
			SecretHash: HashDeviceSecret("tv-sec"), PlayerProfileID: player.ID,
			PlayerDisplayName: "Karaoke TV", PlayerBindingGeneration: 1,
		},
		Session{ID: "sess-tv", DeviceID: "device-karaoke-tv", ProfileID: player.ID, ExpiresAt: now.Add(24 * time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		Device{ID: "device-alice", Name: "Alice Phone", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("a-sec")},
		Session{ID: "sess-alice", DeviceID: "device-alice", ProfileID: alice.ID, ExpiresAt: now.Add(24 * time.Hour)},
	); err != nil {
		t.Fatal(err)
	}

	// Seed Alice's resume position at 47s.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: alice.ID, MediaID: "media-alpha", PositionSeconds: 47, DurationSeconds: 120,
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}

	playPayload, err := NewSessionControlPayload(SessionCommandPlayMedia, SessionControlArgs{MediaID: "media-alpha"}, "device-alice", player.ID, "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	guardAlice := PlayerControlGuard{
		PlayerProfileID:   player.ID,
		CallerProfileID:   alice.ID,
		CallerSessionID:   "sess-alice",
		BindingGeneration: 1,
	}
	res, err := st.ControlRendererSessionForPlayer(ctx, "sess-tv", 0, "cmd_play_attr_0001", playPayload, nil, now, guardAlice)
	if err != nil {
		t.Fatalf("ControlRendererSessionForPlayer(play-media): %v", err)
	}
	if res.Revision != 1 {
		t.Fatalf("revision = %d, want 1", res.Revision)
	}

	// Replay with same commandID must remain deterministic even after Alice's position changes.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: alice.ID, MediaID: "media-alpha", PositionSeconds: 80, DurationSeconds: 120,
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	replayRes, err := st.ControlRendererSessionForPlayer(ctx, "sess-tv", 0, "cmd_play_attr_0001", playPayload, nil, now, guardAlice)
	if err != nil || !replayRes.Replayed {
		t.Fatalf("replay = %+v, %v, want replayed", replayRes, err)
	}

	// Outbox play-media carries Alice's initial 47s resume position (47000 ms).
	events, err := st.ReadEventOutbox(ctx, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var gotPosMS *int64
	for _, ev := range events {
		if ev.Kind == SessionControlKind && ev.AggregateID == "sess-tv" {
			var p SessionControlPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatal(err)
			}
			gotPosMS = p.PositionMs
		}
	}
	if gotPosMS == nil || *gotPosMS != 47000 {
		t.Fatalf("outbox play-media positionMs = %v, want 47000", gotPosMS)
	}

	// Active commanded attribution names Alice and generation 1.
	attr, ok, err := st.ActivePlayerCommandedAttribution(ctx, "sess-tv", "media-alpha", now)
	if err != nil || !ok || attr.OriginatorProfileID != alice.ID || attr.OriginatorSessionID != "sess-alice" || attr.PlayerBindingGeneration != 1 {
		t.Fatalf("ActivePlayerCommandedAttribution = %+v ok=%v err=%v", attr, ok, err)
	}

	// Another controller (Bob) sends pause: originator must stay Alice!
	pausePayload, err := NewSessionControlPayload(SessionCommandPause, SessionControlArgs{}, "device-bob", player.ID, "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ControlRendererSessionForPlayer(ctx, "sess-tv", 1, "cmd_pause_bob_0001", pausePayload, nil, now, PlayerControlGuard{
		PlayerProfileID: player.ID, CallerProfileID: bob.ID, BindingGeneration: 1,
	}); err != nil {
		t.Fatalf("Bob pause: %v", err)
	}
	attrAfterPause, ok, err := st.ActivePlayerCommandedAttribution(ctx, "sess-tv", "media-alpha", now)
	if err != nil || !ok || attrAfterPause.OriginatorProfileID != alice.ID {
		t.Fatalf("originator changed after Bob pause: %+v ok=%v err=%v", attrAfterPause, ok, err)
	}

	// A slow upload holds the shared store lock while reading its body.
	// Publishing Alice's grant and reading an unrelated session must both
	// complete while that lock is held.
	grant := PlaybackGrant{
		GrantID: "pg-1", SessionID: "sess-tv", DeviceID: "device-karaoke-tv",
		ProfileID: player.ID, MediaID: "media-alpha",
		OriginatorProfileID: alice.ID, OriginatorSessionID: "sess-alice",
		PlayerBindingGeneration: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	func() {
		st.mu.RLock()
		defer st.mu.RUnlock()
		publication := make(chan error, 1)
		go func() {
			published, err := st.CreatePlaybackGrant(ctx, grant, 2, PlayerOriginatorPolicy{})
			if err == nil && !published {
				err = ValidationError("grant was refused")
			}
			publication <- err
		}()
		select {
		case err := <-publication:
			if err != nil {
				t.Fatalf("CreatePlaybackGrant: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("grant publication waited on the shared store lock")
		}
		if _, err := st.Session(ctx, "session-default"); err != nil {
			t.Fatalf("unrelated session read: %v", err)
		}
	}()
	policy := ResumePolicy{Class: MediaClassVideo}
	report := func(session SessionID, generation int, grant string, seq *int64, pos int, finished bool) (PlayerReportResolution, error) {
		return st.RecordPlayerPlaybackReport(ctx, session, "device-karaoke-tv", player.ID, generation, PlaybackProgress{
			MediaID: "media-alpha", GrantID: grant, ReportSequence: seq, PositionSeconds: pos, DurationSeconds: 120,
		}, finished, policy, now)
	}

	// Report seq 1 -> accepted for Alice.
	seq1 := int64(1)
	r1, err := report("sess-tv", 1, "pg-1", &seq1, 55, false)
	if err != nil || !r1.Accepted || r1.OriginatorProfileID != alice.ID || r1.Progress.PositionSeconds != 55 {
		t.Fatalf("seq 1 resolution = %+v, %v", r1, err)
	}

	// Duplicate (pg-1, seq 1) -> dropped!
	dup, err := report("sess-tv", 1, "pg-1", &seq1, 55, false)
	if err != nil || dup.Accepted || !dup.Duplicate {
		t.Fatalf("duplicate resolution = %+v, %v, want duplicate dropped", dup, err)
	}

	// Fail after the canonical event append and after the in-transaction grant
	// and sequence writes, then retry the exact same completion.
	seq2 := int64(2)
	if _, err := st.eventsW.ExecContext(ctx, `
		CREATE TRIGGER fail_player_progress BEFORE UPDATE ON playback_progress
		WHEN NEW.profile_id = OLD.profile_id AND NEW.media_id = 'media-alpha'
		BEGIN
			SELECT CASE WHEN EXISTS (
				SELECT 1 FROM playback_grant_report r JOIN playback_grant g USING (grant_id)
				WHERE g.grant_id = 'pg-1' AND r.seq = 2 AND g.ended_at_ms IS NOT NULL
			) THEN RAISE(ABORT, 'injected attributed projection failure')
			ELSE RAISE(ABORT, 'report did not reach expected transaction stage') END;
		END
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := report("sess-tv", 1, "pg-1", &seq2, 120, true); err == nil || !strings.Contains(err.Error(), "injected attributed projection failure") {
		t.Fatalf("injected completion = %v", err)
	}
	progress, ok, err := st.PlaybackProgressFor(ctx, alice.ID, "media-alpha")
	if err != nil || !ok || progress.PositionSeconds != 55 || progress.Watched {
		t.Fatalf("progress after rollback = %+v, %v, %v", progress, ok, err)
	}
	if _, ok, err := st.ActivePlayerCommandedAttribution(ctx, "sess-tv", "media-alpha", now); err != nil || !ok {
		t.Fatalf("command was consumed by failed completion: active=%v, err=%v", ok, err)
	}
	if _, err := st.eventsW.ExecContext(ctx, `DROP TRIGGER fail_player_progress`); err != nil {
		t.Fatal(err)
	}
	r2, err := report("sess-tv", 1, "pg-1", &seq2, 120, true)
	if err != nil || !r2.Accepted || !r2.Ended || !r2.Progress.Watched || r2.Progress.PlayCount != 1 {
		t.Fatalf("seq 2 finish resolution = %+v, %v", r2, err)
	}

	// Report at seq 3 on ended grant -> dropped!
	seq3 := int64(3)
	r3, err := report("sess-tv", 1, "pg-1", &seq3, 120, false)
	if err != nil || r3.Accepted || r3.DropReason != "grant-ended" {
		t.Fatalf("post-finish resolution = %+v, %v, want grant-ended", r3, err)
	}

	// Capture Alice's decision, then replace it with Bob's command for the
	// same Media at the same timestamp. Publish Bob before Alice resumes.
	if _, err := st.ControlRendererSessionForPlayer(ctx, "sess-tv", 2, "cmd_alice_delayed_1", playPayload, nil, now, guardAlice); err != nil {
		t.Fatal(err)
	}
	delayed := PlaybackGrant{
		GrantID: "pg-delayed", SessionID: "sess-tv", DeviceID: "device-karaoke-tv",
		ProfileID: player.ID, MediaID: "media-alpha",
		OriginatorProfileID: alice.ID, OriginatorSessionID: "sess-alice",
		PlayerBindingGeneration: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	guardBob := PlayerControlGuard{PlayerProfileID: player.ID, CallerProfileID: bob.ID, BindingGeneration: 1}
	bobPayload, err := NewSessionControlPayload(SessionCommandPlayMedia, SessionControlArgs{MediaID: "media-alpha"}, "device-bob", player.ID, "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ControlRendererSessionForPlayer(ctx, "sess-tv", 3, "cmd_bob_replaces_1", bobPayload, nil, now, guardBob); err != nil {
		t.Fatal(err)
	}
	bobGrant := delayed
	bobGrant.GrantID, bobGrant.OriginatorProfileID, bobGrant.OriginatorSessionID = "pg-2", bob.ID, ""
	if err := st.SkipPlayerCommandedAttribution(ctx, delayed.SessionID, delayed.MediaID, 3); err != nil {
		t.Fatal(err)
	}
	if published, err := st.CreatePlaybackGrant(ctx, bobGrant, 4, PlayerOriginatorPolicy{}); err != nil || !published {
		t.Fatalf("publish Bob's grant = %v, %v", published, err)
	}
	if published, err := st.CreatePlaybackGrant(ctx, delayed, 3, PlayerOriginatorPolicy{}); err != nil || published {
		t.Fatalf("publish stale grant = %v, %v", published, err)
	}
	active, ok, err := st.ActivePlaybackGrantForSession(ctx, "sess-tv", "media-alpha", now)
	if err != nil || !ok || active.GrantID != bobGrant.GrantID {
		t.Fatalf("active grant after stale publish = %+v, %v, %v", active, ok, err)
	}
	if _, ok, err := st.PlaybackGrant(ctx, delayed.GrantID); err != nil || ok {
		t.Fatalf("stale grant persisted = %v, %v", ok, err)
	}

	// Matching media, originator, generation and timestamps still cannot
	// authorize a decision captured before a replacement command.
	delayedBob := bobGrant
	delayedBob.GrantID = "pg-delayed-bob"
	if _, err := st.ControlRendererSessionForPlayer(ctx, "sess-tv", 4, "cmd_bob_repeats_01", bobPayload, nil, now, guardBob); err != nil {
		t.Fatal(err)
	}
	bobGrant.GrantID = "pg-bob-current"
	if published, err := st.CreatePlaybackGrant(ctx, bobGrant, 5, PlayerOriginatorPolicy{}); err != nil || !published {
		t.Fatalf("publish Bob's replacement grant = %v, %v", published, err)
	}
	if published, err := st.CreatePlaybackGrant(ctx, delayedBob, 4, PlayerOriginatorPolicy{}); err != nil || published {
		t.Fatalf("publish stale grant with identical attribution = %v, %v", published, err)
	}

	// Rebinding invalidates Bob's grant.
	if _, err := st.SetDevicePlayerBinding(ctx, "device-karaoke-tv", player.ID, "Rebound TV"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RenewDeviceSession(ctx, "device-karaoke-tv", "sess-tv-2", now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rOldGen, err := report("sess-tv-2", 2, bobGrant.GrantID, &seq1, 60, false)
	if err != nil || rOldGen.Accepted {
		t.Fatalf("old generation resolution = %+v, %v, want dropped", rOldGen, err)
	}

	// Deleted originator on an active grant finishes without recording.
	guardBob.BindingGeneration = 2
	if _, err := st.ControlRendererSessionForPlayer(ctx, "sess-tv-2", 0, "cmd_bob_new_binding_1", bobPayload, nil, now, guardBob); err != nil {
		t.Fatal(err)
	}
	if published, err := st.CreatePlaybackGrant(ctx, PlaybackGrant{
		GrantID: "pg-3", SessionID: "sess-tv-2", DeviceID: "device-karaoke-tv",
		ProfileID: player.ID, MediaID: "media-alpha",
		OriginatorProfileID: bob.ID, PlayerBindingGeneration: 2,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}, 1, PlayerOriginatorPolicy{}); err != nil || !published {
		t.Fatalf("publish rebound grant = %v, %v", published, err)
	}
	if err := st.DeleteProfile(ctx, bob.ID); err != nil {
		t.Fatal(err)
	}
	rDeleted, err := report("sess-tv-2", 2, "pg-3", &seq1, 120, true)
	if err != nil || rDeleted.Accepted || !rDeleted.Ended || rDeleted.DropReason != "originator-unavailable" {
		t.Fatalf("deleted originator resolution = %+v, %v, want originator-unavailable & ended", rDeleted, err)
	}
}

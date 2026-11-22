package store

import (
	"context"
	"testing"
	"time"
)

// TestEndPlaybackKeepsMyDataTokens covers CORE-2461: ending a playback drops
// it from the active-session reads at once but keeps its still-valid grant on
// My Data's stream-token list; a late progress report does not revive it, and
// only a new grant does.
func TestEndPlaybackKeepsMyDataTokens(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	expires := now.Add(time.Hour)

	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-hls", "media-alpha", false, expires); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordStreamToken(ctx, "session-managed", "profile-default", "device-tv", "media-direct", "media-alpha", false, expires); err != nil {
		t.Fatal(err)
	}
	assertLive := func(step string, want ...SessionID) {
		t.Helper()
		active, err := st.ActiveSessions(ctx, now, profileDefaultFilter(true))
		if err != nil {
			t.Fatalf("%s: ActiveSessions: %v", step, err)
		}
		var got []SessionID
		for _, a := range active {
			got = append(got, a.SessionID)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: live sessions = %v, want %v", step, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: live sessions = %v, want %v", step, got, want)
			}
		}
		count, err := st.ActiveSessionCount(ctx, now)
		if err != nil || count != len(want) {
			t.Fatalf("%s: ActiveSessionCount = %d, %v; want %d", step, count, err, len(want))
		}
	}
	assertTokens := func(step string, at time.Time, want int) {
		t.Helper()
		tokens, err := st.MyStreamTokens(ctx, "profile-default", at)
		if err != nil {
			t.Fatalf("%s: MyStreamTokens: %v", step, err)
		}
		if len(tokens) != want {
			t.Fatalf("%s: My Data tokens = %d, want %d", step, len(tokens), want)
		}
	}

	assertLive("before end", "session-default", "session-managed")
	assertTokens("before end", now, 2)

	if err := st.EndPlayback(ctx, "session-default", "media-alpha"); err != nil {
		t.Fatal(err)
	}
	assertLive("after end", "session-managed")
	assertTokens("after end", now, 2)

	if err := st.TouchStreamTokenActivity(ctx, "session-default", "profile-default", "media-alpha", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	assertLive("after a late progress report", "session-managed")

	if err := st.EndPlayback(ctx, "session-default", "media-alpha"); err != nil {
		t.Fatalf("repeated EndPlayback: %v", err)
	}
	assertTokens("after a repeated end", now, 2)

	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-hls", "media-alpha", false, expires); err != nil {
		t.Fatal(err)
	}
	assertLive("after a new grant", "session-default", "session-managed")

	if err := st.EndPlayback(ctx, "session-default", "media-alpha"); err != nil {
		t.Fatal(err)
	}
	assertTokens("after the ended grant expires", expires, 0)
}

// TestActiveSessionsFollowLatestProfile covers CORE-2682: a Profile switch
// keeps the session, so a session's grants for one Media stay one playback that
// names the Profile whose grant was active last, and a progress report touches
// only the reporting Profile's grants.
func TestActiveSessionsFollowLatestProfile(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	if err := st.RecordStreamToken(ctx, "session-default", "profile-managed", "device-tv", "media-direct", "media-alpha", false, expires); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-hls", "media-alpha", false, expires); err != nil {
		t.Fatal(err)
	}
	playing := func(profile ProfileID, at time.Time) {
		t.Helper()
		if err := st.TouchStreamTokenActivity(ctx, "session-default", profile, "media-alpha", at); err != nil {
			t.Fatal(err)
		}
		active, err := st.ActiveSessions(ctx, now, profileDefaultFilter(true))
		if err != nil {
			t.Fatal(err)
		}
		if len(active) != 1 || active[0].ProfileID != profile || !active[0].LastActiveAt.Equal(at.Truncate(time.Millisecond)) {
			t.Fatalf("live playbacks = %+v, want one naming %s active at %v", active, profile, at)
		}
		if !active[0].Transcoding || active[0].ProfileTied {
			t.Fatalf("collapsed playback lost the HLS flag or is tied: %+v", active[0])
		}
	}
	playing("profile-default", now.Add(time.Minute))
	playing("profile-managed", now.Add(2*time.Minute))

	// Both Profiles reporting within one millisecond leave the ledger unable to
	// name the last reporter, so the row says so.
	tie := now.Add(3 * time.Minute)
	for _, profile := range []ProfileID{"profile-managed", "profile-default"} {
		if err := st.TouchStreamTokenActivity(ctx, "session-default", profile, "media-alpha", tie); err != nil {
			t.Fatal(err)
		}
	}
	active, err := st.ActiveSessions(ctx, now, profileDefaultFilter(true))
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || !active[0].ProfileTied {
		t.Fatalf("live playbacks after a same-millisecond switch = %+v, want one tied row", active)
	}
	playing("profile-default", tie.Add(time.Second))
}

// TestActiveSessionsAndCountApplyActivityWindow covers CORE-2346:
// ActiveSessions and ActiveSessionCount apply the same LiveSessionActivityWindow
// (and reject legacy 0-activity rows), while My Data keeps unexpired tokens.
func TestActiveSessionsAndCountApplyActivityWindow(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	expires := now.Add(time.Hour)

	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false, expires); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordStreamToken(ctx, "session-managed", "profile-managed", "device-tv", "media-hls", "media-alpha", false, expires); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchStreamTokenActivity(ctx, "session-managed", "profile-managed", "media-alpha", now.Add(LiveSessionActivityWindow)); err != nil {
		t.Fatal(err)
	}

	assertActive := func(step string, at time.Time, want ...SessionID) {
		t.Helper()
		active, err := st.ActiveSessions(ctx, at, profileDefaultFilter(true))
		if err != nil {
			t.Fatalf("%s: ActiveSessions: %v", step, err)
		}
		if len(active) != len(want) {
			t.Fatalf("%s: ActiveSessions len = %d, want %d (%+v)", step, len(active), len(want), active)
		}
		for i := range want {
			if active[i].SessionID != want[i] {
				t.Fatalf("%s: ActiveSessions[%d] = %s, want %s", step, i, active[i].SessionID, want[i])
			}
		}
		count, err := st.ActiveSessionCount(ctx, at)
		if err != nil || count != len(want) {
			t.Fatalf("%s: ActiveSessionCount = %d, %v; want %d", step, count, err, len(want))
		}
	}

	// Just at the boundary (now + LiveSessionActivityWindow from session-default's
	// activity, or backdated): test both inside and just past the window.
	atBoundary := now.Add(LiveSessionActivityWindow)
	if _, err := st.eventsW.ExecContext(ctx, `UPDATE stream_token_ledger SET last_active_ms = ? WHERE session_id = ?`, timeToMillis(now), "session-default"); err != nil {
		t.Fatal(err)
	}
	assertActive("at exact window boundary", atBoundary, "session-default", "session-managed")

	pastBoundary := atBoundary.Add(time.Millisecond)
	assertActive("1ms past window boundary", pastBoundary, "session-managed")

	// My Data still lists session-default's unexpired token even while idle.
	tokens, err := st.MyStreamTokens(ctx, "profile-default", pastBoundary)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("MyStreamTokens for idle playback = %d, %v; want 1", len(tokens), err)
	}

	// A progress report brings the idle playback back.
	if err := st.TouchStreamTokenActivity(ctx, "session-default", "profile-default", "media-alpha", pastBoundary); err != nil {
		t.Fatal(err)
	}
	assertActive("after progress report", pastBoundary, "session-default", "session-managed")

	// A legacy row with last_active_ms = 0 stays inactive even if now is at Unix epoch 0.
	if _, err := st.eventsW.ExecContext(ctx, `UPDATE stream_token_ledger SET last_active_ms = 0 WHERE session_id = ?`, "session-default"); err != nil {
		t.Fatal(err)
	}
	epoch := time.Unix(0, 0).UTC()
	if active, err := st.ActiveSessions(ctx, epoch, profileDefaultFilter(true)); err != nil || len(active) != 1 || active[0].SessionID != "session-managed" {
		t.Fatalf("epoch ActiveSessions with legacy 0 row = %+v, %v; want only session-managed", active, err)
	}
	if count, err := st.ActiveSessionCount(ctx, epoch); err != nil || count != 1 {
		t.Fatalf("epoch ActiveSessionCount with legacy 0 row = %d, %v; want 1", count, err)
	}
}

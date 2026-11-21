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

// TestActiveSessionsKeepProfilesApart covers CORE-2682: a Profile switch keeps
// the session, so grants one session holds for one Media under two Profiles
// are two playbacks, and a progress report touches only the reporting
// Profile's grants.
func TestActiveSessionsKeepProfilesApart(t *testing.T) {
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
	lastActive := func() map[ProfileID]time.Time {
		t.Helper()
		active, err := st.ActiveSessions(ctx, now, profileDefaultFilter(true))
		if err != nil {
			t.Fatal(err)
		}
		out := map[ProfileID]time.Time{}
		for _, a := range active {
			if _, dup := out[a.ProfileID]; dup {
				t.Fatalf("Profile %s listed twice: %+v", a.ProfileID, active)
			}
			out[a.ProfileID] = a.LastActiveAt
		}
		return out
	}
	before := lastActive()
	if len(before) != 2 {
		t.Fatalf("live playbacks = %v, want one per Profile", before)
	}
	later := now.Add(time.Minute)
	if err := st.TouchStreamTokenActivity(ctx, "session-default", "profile-default", "media-alpha", later); err != nil {
		t.Fatal(err)
	}
	after := lastActive()
	if !after["profile-default"].Equal(later.Truncate(time.Millisecond)) {
		t.Fatalf("reporting Profile's activity = %v, want %v", after["profile-default"], later)
	}
	if !after["profile-managed"].Equal(before["profile-managed"]) {
		t.Fatalf("other Profile's activity moved: %v -> %v", before["profile-managed"], after["profile-managed"])
	}
}

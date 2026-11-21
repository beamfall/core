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
		if !active[0].Transcoding {
			t.Fatalf("collapsed playback lost the HLS flag: %+v", active[0])
		}
	}
	playing("profile-default", now.Add(time.Minute))
	playing("profile-managed", now.Add(2*time.Minute))
}

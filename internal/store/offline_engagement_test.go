package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func offlineEvent(profile ProfileID, eventID string, position int, completed bool, playedAt time.Time) OfflineEngagementEvent {
	return OfflineEngagementEvent{
		EventID: eventID,
		Progress: PlaybackProgress{
			ProfileID: profile, MediaID: "media-alpha", DeviceID: "device-phone",
			PositionSeconds: position, DurationSeconds: 1000,
		},
		Completed: completed,
		PlayedAt:  playedAt,
		Policy:    ResumePolicy{Class: MediaClassVideo},
	}
}

func mustProgress(t *testing.T, st *Store, profile ProfileID) PlaybackProgress {
	t.Helper()
	progress, ok, err := st.PlaybackProgressFor(context.Background(), profile, "media-alpha")
	if err != nil || !ok {
		t.Fatalf("progress read: ok=%v err=%v", ok, err)
	}
	return progress
}

func TestOfflineEngagementReplayIsIdempotentPerDeviceEvent(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour)
	ev := offlineEvent("profile-default", "evt-1", 0, true, at)
	out, err := st.ApplyOfflineEngagementEvent(ctx, ev)
	if err != nil || out.Duplicate || !out.BecameWatched {
		t.Fatalf("first apply = %+v, %v; want applied and watched", out, err)
	}
	first := mustProgress(t, st, "profile-default")
	if !first.Watched || first.PlayCount != 1 {
		t.Fatalf("after completion: %+v, want watched with one play", first)
	}
	out, err = st.ApplyOfflineEngagementEvent(ctx, ev)
	if err != nil || !out.Duplicate || out.BecameWatched {
		t.Fatalf("replay = %+v, %v; want duplicate", out, err)
	}
	if again := mustProgress(t, st, "profile-default"); again.PlayCount != 1 || !again.Watched || !again.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("replay changed progress: %+v, want %+v", again, first)
	}
	// The same event ID from another Device is a different event.
	other := ev
	other.Progress.DeviceID = "device-tablet"
	if out, err := st.ApplyOfflineEngagementEvent(ctx, other); err != nil || out.Duplicate {
		t.Fatalf("other device = %+v, %v; want applied", out, err)
	}
}

func TestOfflineEngagementReplayIsProfileScoped(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour)
	if _, err := st.ApplyOfflineEngagementEvent(ctx, offlineEvent("profile-default", "evt-1", 300, false, at)); err != nil {
		t.Fatal(err)
	}
	out, err := st.ApplyOfflineEngagementEvent(ctx, offlineEvent("profile-managed", "evt-1", 600, false, at))
	if err != nil || out.Duplicate {
		t.Fatalf("second Profile = %+v, %v; want applied", out, err)
	}
	if got := mustProgress(t, st, "profile-default").PositionSeconds; got != 300 {
		t.Fatalf("default position = %d, want 300", got)
	}
	if got := mustProgress(t, st, "profile-managed").PositionSeconds; got != 600 {
		t.Fatalf("managed position = %d, want 600", got)
	}
}

func TestOfflineEngagementOlderPositionNeverOverwritesNewer(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now()
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: "media-alpha",
		PositionSeconds: 500, DurationSeconds: 1000, EventTimeMS: now.Add(-time.Minute).UnixMilli(),
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyOfflineEngagementEvent(ctx, offlineEvent("profile-default", "evt-old", 120, false, now.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	if got := mustProgress(t, st, "profile-default").PositionSeconds; got != 500 {
		t.Fatalf("older offline position overwrote newer: %d, want 500", got)
	}
	// A newer offline position wins, and a future device clock is clamped.
	if _, err := st.ApplyOfflineEngagementEvent(ctx, offlineEvent("profile-default", "evt-new", 700, false, now.Add(24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	progress := mustProgress(t, st, "profile-default")
	if progress.PositionSeconds != 700 {
		t.Fatalf("newer offline position = %d, want 700", progress.PositionSeconds)
	}
	if progress.EventTimeMS > time.Now().UnixMilli() {
		t.Fatalf("future played-at not clamped: event time %d", progress.EventTimeMS)
	}
}

func TestOfflineEngagementCompletionIsStickyAndCountsOnce(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now()
	// A newer online partial position, then an older offline completion:
	// completion wins whatever its timestamp.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: "media-alpha",
		PositionSeconds: 200, DurationSeconds: 1000, EventTimeMS: now.Add(-time.Minute).UnixMilli(),
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	out, err := st.ApplyOfflineEngagementEvent(ctx, offlineEvent("profile-default", "evt-done", 0, true, now.Add(-2*time.Hour)))
	if err != nil || !out.BecameWatched {
		t.Fatalf("older completion = %+v, %v; want watched", out, err)
	}
	progress := mustProgress(t, st, "profile-default")
	if !progress.Watched || progress.PositionSeconds != 0 || progress.PlayCount != 1 {
		t.Fatalf("after older completion: %+v, want watched, position 0, one play", progress)
	}
	// A later partial offline play leaves it watched and uncounted.
	out, err = st.ApplyOfflineEngagementEvent(ctx, offlineEvent("profile-default", "evt-partial", 300, false, now.Add(-time.Second)))
	if err != nil || out.Duplicate || out.BecameWatched {
		t.Fatalf("partial on watched = %+v, %v", out, err)
	}
	// Crossing the threshold without the completed flag on watched Media
	// counts nothing either.
	if _, err := st.ApplyOfflineEngagementEvent(ctx, offlineEvent("profile-default", "evt-past", 990, false, now.Add(-time.Millisecond))); err != nil {
		t.Fatal(err)
	}
	if again := mustProgress(t, st, "profile-default"); !again.Watched || again.PlayCount != 1 || again.PositionSeconds != 0 {
		t.Fatalf("watched Media changed: %+v", again)
	}
}

func TestOfflineEngagementThresholdMarksWatched(t *testing.T) {
	st := openSeeded(t)
	out, err := st.ApplyOfflineEngagementEvent(context.Background(), offlineEvent("profile-default", "evt-1", 960, false, time.Now().Add(-time.Hour)))
	if err != nil || !out.BecameWatched {
		t.Fatalf("past threshold = %+v, %v; want watched", out, err)
	}
	if progress := mustProgress(t, st, "profile-default"); !progress.Watched || progress.PlayCount != 1 {
		t.Fatalf("past threshold: %+v", progress)
	}
}

func TestOfflineEngagementSurvivesProjectionRebuild(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now()
	for _, ev := range []OfflineEngagementEvent{
		offlineEvent("profile-default", "evt-a", 300, false, now.Add(-3*time.Hour)),
		offlineEvent("profile-default", "evt-b", 0, true, now.Add(-2*time.Hour)),
		offlineEvent("profile-default", "evt-c", 100, false, now.Add(-time.Hour)),
	} {
		if _, err := st.ApplyOfflineEngagementEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	before := mustProgress(t, st, "profile-default")
	st.mu.RLock()
	err := st.rebuildEngagementProjections(ctx)
	st.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	after := mustProgress(t, st, "profile-default")
	if after.Watched != before.Watched || after.PlayCount != before.PlayCount || after.PositionSeconds != before.PositionSeconds || after.EventTimeMS != before.EventTimeMS {
		t.Fatalf("rebuild changed progress: before %+v after %+v", before, after)
	}
}

func TestOfflineEngagementRejectsInvalidEvents(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour)
	for name, ev := range map[string]OfflineEngagementEvent{
		"missing id":      offlineEvent("profile-default", "", 10, false, at),
		"padded id":       offlineEvent("profile-default", " evt ", 10, false, at),
		"missing time":    offlineEvent("profile-default", "evt-1", 10, false, time.Time{}),
		"negative":        offlineEvent("profile-default", "evt-2", -1, false, at),
		"missing profile": offlineEvent("", "evt-3", 10, false, at),
	} {
		_, err := st.ApplyOfflineEngagementEvent(ctx, ev)
		var validation ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("%s: err = %v, want validation error", name, err)
		}
	}
	// A rejected event is not recorded, so a corrected resend applies.
	if out, err := st.ApplyOfflineEngagementEvent(ctx, offlineEvent("profile-default", "evt-2", 10, false, at)); err != nil || out.Duplicate {
		t.Fatalf("resend after rejection = %+v, %v", out, err)
	}
}

func TestOfflineEngagementLedgerClearedWithProfileMedia(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	ev := offlineEvent("profile-default", "evt-1", 300, false, time.Now().Add(-time.Hour))
	if _, err := st.ApplyOfflineEngagementEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteProfileFootprint(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM offline_engagement_event`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("ledger rows after Profile erasure = %d, want 0", rows)
	}
}

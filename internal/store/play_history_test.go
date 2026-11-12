package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// progressAt appends one playback-progress event at a chosen instant, the way
// the progress upsert writes it.
func progressAt(t *testing.T, st *Store, profile ProfileID, media MediaID, at time.Time, watched bool) {
	t.Helper()
	progressReported(t, st, profile, media, at, time.Time{}, watched)
}

// progressReported appends a progress event that arrived at the server at one
// instant and carries the client's own event time; a zero observed time is a
// client that sent none.
func progressReported(t *testing.T, st *Store, profile ProfileID, media MediaID, at, observed time.Time, watched bool) {
	t.Helper()
	var eventTime *int64
	if !observed.IsZero() {
		ms := timeToMillis(observed)
		eventTime = &ms
	}
	payload, err := json.Marshal(playbackProjectionEvent{
		Operation: playbackProjectionOperationProgress, ProfileID: string(profile), MediaID: string(media),
		DurationSeconds: 3600, Watched: watched, UpdatedAtMS: timeToMillis(at), EventTimeMS: eventTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("play-test-%s-%s-%d", profile, media, at.UnixNano())
	if _, err := st.eventsW.Exec(`
		INSERT INTO engagement_event(id, profile_id, media_id, kind, payload, occurred_at_ms, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, profile, media, playbackProjectionEventKind, string(payload), timeToMillis(at), timeToMillis(at)); err != nil {
		t.Fatalf("insert progress event: %v", err)
	}
}

// feature:play-history
func TestPlayHistoryDerivesPlaysAndFilters(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllLibraries: true}
	revealed := LibraryFilter{AllLibraries: true, Revealed: true}
	day := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)

	// profile-default watches media-alpha for ten minutes, then again two
	// hours later to the end: two plays, the second finished.
	progressAt(t, st, "profile-default", "media-alpha", day, false)
	progressAt(t, st, "profile-default", "media-alpha", day.Add(10*time.Minute), false)
	progressAt(t, st, "profile-default", "media-alpha", day.Add(2*time.Hour), false)
	progressAt(t, st, "profile-default", "media-alpha", day.Add(2*time.Hour+20*time.Minute), true)
	// profile-managed plays it once the next day, and plays the restricted
	// media-archive.
	progressAt(t, st, "profile-managed", "media-alpha", day.Add(24*time.Hour), false)
	progressAt(t, st, "profile-managed", "media-alpha", day.Add(24*time.Hour+5*time.Minute), false)
	progressAt(t, st, "profile-managed", "media-archive", day.Add(25*time.Hour), false)

	plays, err := st.PlayHistory(ctx, all, PlayHistoryQuery{})
	if err != nil {
		t.Fatalf("PlayHistory: %v", err)
	}
	if len(plays) != 4 {
		t.Fatalf("plays = %d, want 4: %#v", len(plays), plays)
	}
	// Newest first; the restricted play is listed without its title.
	if plays[0].MediaID != "media-archive" || plays[0].Title != "" || plays[0].LibraryID != "" {
		t.Errorf("unrevealed restricted play = %#v, want it listed with no title or library", plays[0])
	}
	if plays[1].ProfileName == "" || plays[1].Title == "" || plays[1].LibraryID != "library-local" || plays[1].LibraryTitle == "" {
		t.Errorf("visible play = %#v, want profile name, title and library", plays[1])
	}
	if plays[1].WatchedSeconds != 300 || plays[1].Finished {
		t.Errorf("managed play = %ds finished=%v, want 300s unfinished", plays[1].WatchedSeconds, plays[1].Finished)
	}
	if plays[2].WatchedSeconds != 1200 || !plays[2].Finished || !plays[2].StartedAt.Equal(day.Add(2*time.Hour)) {
		t.Errorf("second default play = %#v, want 1200s finished starting two hours in", plays[2])
	}
	if plays[3].WatchedSeconds != 600 || plays[3].Finished {
		t.Errorf("first default play = %#v, want 600s unfinished", plays[3])
	}

	byProfile, err := st.PlayHistory(ctx, all, PlayHistoryQuery{ProfileID: "profile-default"})
	if err != nil || len(byProfile) != 2 {
		t.Fatalf("profile filter = %d plays (err %v), want 2", len(byProfile), err)
	}
	byDate, err := st.PlayHistory(ctx, all, PlayHistoryQuery{Since: day.Add(12 * time.Hour), Until: day.Add(24*time.Hour + 30*time.Minute)})
	if err != nil || len(byDate) != 1 || byDate[0].ProfileID != "profile-managed" {
		t.Fatalf("date filter = %#v (err %v), want the one managed play", byDate, err)
	}
	byLibrary, err := st.PlayHistory(ctx, all, PlayHistoryQuery{LibraryID: "library-local"})
	if err != nil || len(byLibrary) != 3 {
		t.Fatalf("library filter = %d plays (err %v), want 3", len(byLibrary), err)
	}
	hidden, err := st.PlayHistory(ctx, all, PlayHistoryQuery{LibraryID: "library-archive"})
	if err != nil || len(hidden) != 0 {
		t.Fatalf("unrevealed library filter = %d plays (err %v), want none", len(hidden), err)
	}
	shown, err := st.PlayHistory(ctx, revealed, PlayHistoryQuery{LibraryID: "library-archive"})
	if err != nil || len(shown) != 1 || shown[0].Title == "" {
		t.Fatalf("revealed library filter = %#v (err %v), want the archive play with its title", shown, err)
	}
	limited, err := st.PlayHistory(ctx, all, PlayHistoryQuery{Limit: 2})
	if err != nil || len(limited) != 2 {
		t.Fatalf("limit = %d plays (err %v), want 2", len(limited), err)
	}

	stats, err := st.PlayStats(ctx, all, day.Add(-time.Hour))
	if err != nil {
		t.Fatalf("PlayStats: %v", err)
	}
	if len(stats.Profiles) != 2 || stats.Profiles[0].ProfileID != "profile-default" ||
		stats.Profiles[0].Plays != 2 || stats.Profiles[0].WatchedSeconds != 1800 ||
		stats.Profiles[1].Plays != 2 || stats.Profiles[1].WatchedSeconds != 300 {
		t.Errorf("profile ranking = %#v, want default (2 plays, 1800s) ahead of managed (2 plays, 300s)", stats.Profiles)
	}
	if len(stats.Items) != 1 || stats.Items[0].MediaID != "media-alpha" || stats.Items[0].Plays != 3 || stats.Items[0].Title == "" {
		t.Errorf("item ranking = %#v, want only the visible media-alpha with 3 plays", stats.Items)
	}
	recent, err := st.PlayStats(ctx, all, day.Add(12*time.Hour))
	if err != nil || len(recent.Profiles) != 1 || recent.Profiles[0].ProfileID != "profile-managed" {
		t.Errorf("windowed ranking = %#v (err %v), want only managed", recent.Profiles, err)
	}
}

// feature:play-history
func TestPlayHistoryDatesDelayedReportsByWhenTheyWerePlayed(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllLibraries: true}
	played := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
	flushed := played.Add(26 * time.Hour)

	// The first report arrives live. The device then goes offline and sends
	// the rest of the same play the next day.
	progressReported(t, st, "profile-default", "media-alpha", played, played, false)
	progressReported(t, st, "profile-default", "media-alpha", flushed, played.Add(10*time.Minute), false)
	progressReported(t, st, "profile-default", "media-alpha", flushed.Add(time.Second), played.Add(20*time.Minute), true)
	// A client whose clock runs a day ahead is dated by arrival instead.
	progressReported(t, st, "profile-managed", "media-alpha", played.Add(time.Hour), played.Add(25*time.Hour), false)

	plays, err := st.PlayHistory(ctx, all, PlayHistoryQuery{})
	if err != nil {
		t.Fatalf("PlayHistory: %v", err)
	}
	if len(plays) != 2 {
		t.Fatalf("plays = %d, want 2: %#v", len(plays), plays)
	}
	if plays[0].ProfileID != "profile-managed" || !plays[0].StartedAt.Equal(played.Add(time.Hour)) {
		t.Errorf("fast-clock play = %#v, want it dated by arrival an hour in", plays[0])
	}
	if !plays[1].StartedAt.Equal(played) || plays[1].WatchedSeconds != 1200 || !plays[1].Finished {
		t.Errorf("delayed play = %#v, want one finished 1200s play starting when it was played", plays[1])
	}

	// The date filter and the rankings follow the play, not the upload.
	thatDay, err := st.PlayHistory(ctx, all, PlayHistoryQuery{ProfileID: "profile-default", Until: played.Add(4 * time.Hour)})
	if err != nil || len(thatDay) != 1 {
		t.Fatalf("day-of filter = %#v (err %v), want the delayed play", thatDay, err)
	}
	nextDay, err := st.PlayHistory(ctx, all, PlayHistoryQuery{Since: played.Add(4 * time.Hour)})
	if err != nil || len(nextDay) != 0 {
		t.Fatalf("next-day filter = %#v (err %v), want nothing", nextDay, err)
	}
	stats, err := st.PlayStats(ctx, all, played.Add(4*time.Hour))
	if err != nil || len(stats.Profiles) != 0 || len(stats.Items) != 0 {
		t.Fatalf("next-day ranking = %#v (err %v), want empty", stats, err)
	}
}

// feature:play-history
func TestPlayHistoryOpenEndedReadMatchesTheWholeLog(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllLibraries: true}
	until := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	bound := until.Add(-playHistoryReach[0])

	// Two recent plays, one play that straddles the first bound of an
	// open-ended read, and one long before it.
	progressAt(t, st, "profile-default", "media-alpha", until.Add(-2*time.Hour), false)
	progressAt(t, st, "profile-default", "media-alpha", until.Add(-2*time.Hour+10*time.Minute), false)
	progressAt(t, st, "profile-managed", "media-alpha", until.Add(-26*time.Hour), false)
	progressAt(t, st, "profile-managed", "media-alpha", until.Add(-26*time.Hour+5*time.Minute), false)
	progressAt(t, st, "profile-default", "media-alpha", bound.Add(-10*time.Minute), false)
	progressAt(t, st, "profile-default", "media-alpha", bound.Add(10*time.Minute), false)
	progressAt(t, st, "profile-managed", "media-alpha", bound.Add(-200*24*time.Hour), false)
	progressAt(t, st, "profile-managed", "media-alpha", bound.Add(-200*24*time.Hour+15*time.Minute), false)

	whole, err := st.PlayHistory(ctx, all, PlayHistoryQuery{Since: bound.Add(-400 * 24 * time.Hour), Until: until})
	if err != nil || len(whole) != 4 {
		t.Fatalf("whole log = %d plays (err %v), want 4", len(whole), err)
	}
	for limit := 1; limit <= 5; limit++ {
		got, err := st.PlayHistory(ctx, all, PlayHistoryQuery{Until: until, Limit: limit})
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		want := whole[:min(limit, len(whole))]
		if len(got) != len(want) {
			t.Fatalf("limit %d = %d plays, want %d", limit, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("limit %d play %d = %#v, want %#v", limit, i, got[i], want[i])
			}
		}
	}
	if whole[2].WatchedSeconds != 1200 {
		t.Errorf("straddling play = %ds, want the whole 1200s", whole[2].WatchedSeconds)
	}
}

// feature:play-history
func TestPlayHistoryWindowKeepsPlaysWhole(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllLibraries: true}
	since := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
	until := since.Add(6 * time.Hour)

	// One play straddles since, one sits inside, one straddles until.
	progressAt(t, st, "profile-default", "media-alpha", since.Add(-10*time.Minute), false)
	progressAt(t, st, "profile-default", "media-alpha", since.Add(10*time.Minute), false)
	progressAt(t, st, "profile-default", "media-alpha", since.Add(20*time.Minute), false)
	progressAt(t, st, "profile-managed", "media-alpha", since.Add(2*time.Hour), false)
	progressAt(t, st, "profile-managed", "media-alpha", since.Add(2*time.Hour+5*time.Minute), false)
	progressAt(t, st, "profile-default", "media-alpha", until.Add(-10*time.Minute), false)
	progressAt(t, st, "profile-default", "media-alpha", until.Add(15*time.Minute), true)

	got, err := st.PlayHistory(ctx, all, PlayHistoryQuery{Since: since, Until: until})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("window = %#v, want the two plays that started inside it", got)
	}
	if !got[0].StartedAt.Equal(until.Add(-10*time.Minute)) || got[0].WatchedSeconds != 1500 || !got[0].Finished {
		t.Errorf("play straddling until = %#v, want it whole: 1500s, finished", got[0])
	}
	if !got[1].StartedAt.Equal(since.Add(2*time.Hour)) || got[1].WatchedSeconds != 300 {
		t.Errorf("inner play = %#v, want 300s from since+2h", got[1])
	}

	earlier, err := st.PlayHistory(ctx, all, PlayHistoryQuery{Since: since.Add(-time.Hour), Until: since})
	if err != nil {
		t.Fatal(err)
	}
	if len(earlier) != 1 || !earlier[0].StartedAt.Equal(since.Add(-10*time.Minute)) || earlier[0].WatchedSeconds != 1800 {
		t.Fatalf("window before since = %#v, want the straddling play whole: 1800s from since-10m", earlier)
	}

	stats, err := st.PlayStats(ctx, all, since)
	if err != nil {
		t.Fatal(err)
	}
	plays := 0
	for _, profile := range stats.Profiles {
		plays += profile.Plays
	}
	if plays != 2 {
		t.Errorf("stats since = %d plays, want 2 (the straddling play started earlier)", plays)
	}
}

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
	payload, err := json.Marshal(playbackProjectionEvent{
		Operation: playbackProjectionOperationProgress, ProfileID: string(profile), MediaID: string(media),
		DurationSeconds: 3600, Watched: watched, UpdatedAtMS: timeToMillis(at),
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

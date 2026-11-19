package store

import (
	"context"
	"testing"
)

// CORE-2320: recent catalog additions follow insertion order, not title order.
func TestRecentlyAddedIgnoresAlphabeticalOrder(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	ids := make([]MediaID, 0, 2)
	for _, title := range []string{"Zulu first", "Alpha second"} {
		item, _, err := st.ImportMediaFile(ctx, ImportMediaFile{LibraryID: "library-local", Path: "/parity/" + title + ".mkv", Title: title, Container: "mkv", Codec: "h264", ContentFingerprint: "parity-" + title})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, item.ID)
	}
	items, err := st.SourceItems(ctx, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "recently-added", LibraryID: "library-local", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != ids[1] || items[1].ID != ids[0] {
		t.Fatalf("recent additions = %+v, want newest imported first", items)
	}
}

// CORE-2320: different play counts and recency must produce different ordering.
func TestRecentAndMostPlayedUseDifferentOrdering(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	for _, id := range []MediaID{"media-alpha", "media-archive"} {
		if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{ProfileID: "profile-default", MediaID: id, PositionSeconds: 120, DurationSeconds: 120}, true, ResumePolicy{Class: MediaClassAudio}); err != nil {
			t.Fatal(err)
		}
	}
	// Fix the projection's recency explicitly so wall-clock ties cannot weaken
	// the fixture: the older item has more plays, the newer item fewer.
	result, err := st.eventsW.ExecContext(ctx, `UPDATE playback_progress SET play_count = CASE media_id WHEN 'media-alpha' THEN 5 ELSE 1 END, last_played_at_ms = CASE media_id WHEN 'media-alpha' THEN 100 ELSE 200 END WHERE profile_id = 'profile-default' AND media_id IN ('media-alpha', 'media-archive')`)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 2 {
		t.Fatalf("fixture updated %d rows: %v", n, err)
	}
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	recent, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "recently-played", ProfileID: "profile-default"})
	if err != nil {
		t.Fatal(err)
	}
	most, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "most-played", ProfileID: "profile-default"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || len(most) != 2 || recent[0].ID != "media-archive" || most[0].ID != "media-alpha" {
		t.Fatalf("recent=%+v most=%+v, want recency and frequency to differ", recent, most)
	}
}

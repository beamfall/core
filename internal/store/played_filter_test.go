package store

import (
	"context"
	"slices"
	"testing"
)

// TestPlayedFilterNarrowsHistoryBeforePaging covers the History search and
// date filters (CORE-3333): text matches the Media title or a linked Entity's
// title across folded terms, the last-played bounds are inclusive/exclusive,
// and both apply before offset so later pages stay full.
func TestPlayedFilterNarrowsHistoryBeforePaging(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	if err := st.UpsertEntity(ctx, Entity{ID: "entity-coltrane", LibraryID: "library-local", Kind: "person", Title: "John Cóltrane"}); err != nil {
		t.Fatal(err)
	}
	ids := map[string]MediaID{}
	playedAt := map[string]int64{"Blue Train": 1_000, "Giant Steps": 2_000, "Naima": 3_000, "So What": 4_000}
	for _, title := range []string{"Blue Train", "Giant Steps", "Naima", "So What"} {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: "library-local", Path: "/media/history-" + title + ".flac", Title: title,
			Container: "flac", Codec: "flac", ContentFingerprint: "sha256:history-" + title,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[title] = media.ID
		if title != "So What" {
			if err := st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: "entity-coltrane", Name: "performer", ToMediaID: media.ID}); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{ProfileID: "profile-default", MediaID: media.ID, PositionSeconds: 120, DurationSeconds: 120}, true, ResumePolicy{Class: MediaClassAudio}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.eventsW.ExecContext(ctx, `UPDATE playback_progress SET last_played_at_ms = ? WHERE profile_id = ? AND media_id = ?`, playedAt[title], "profile-default", media.ID); err != nil {
			t.Fatal(err)
		}
	}
	titles := func(played PlayedFilter, limit, offset int) []string {
		t.Helper()
		items, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "recently-played", ProfileID: "profile-default", Played: played, Limit: limit, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, item := range items {
			for title, id := range ids {
				if item.ID == id {
					out = append(out, title)
				}
			}
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		played PlayedFilter
		limit  int
		offset int
		want   []string
	}{
		{"title words, any case", PlayedFilter{Query: "  giant STEPS "}, 0, 0, []string{"Giant Steps"}},
		{"linked entity, folded", PlayedFilter{Query: "coltrane"}, 0, 0, []string{"Naima", "Giant Steps", "Blue Train"}},
		{"terms span title and entity", PlayedFilter{Query: "naima john"}, 0, 0, []string{"Naima"}},
		{"no match", PlayedFilter{Query: "coltrane what"}, 0, 0, []string{}},
		{"date window", PlayedFilter{PlayedAfter: 2_000, PlayedBefore: 4_000}, 0, 0, []string{"Naima", "Giant Steps"}},
		{"text and date", PlayedFilter{Query: "coltrane", PlayedBefore: 3_000}, 0, 0, []string{"Giant Steps", "Blue Train"}},
		{"offset counts matches", PlayedFilter{Query: "coltrane"}, 1, 1, []string{"Giant Steps"}},
	} {
		got := titles(tc.played, tc.limit, tc.offset)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: recently-played = %v, want %v", tc.name, got, tc.want)
		}
	}
	most, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "most-played", ProfileID: "profile-default", Played: PlayedFilter{Query: "so what"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(most) != 1 || most[0].ID != ids["So What"] {
		t.Fatalf("most-played search = %#v, want only So What", most)
	}
}

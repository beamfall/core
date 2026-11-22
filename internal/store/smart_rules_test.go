package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// feature:collection-editor — smart playlists (CORE-2771): rule sources over
// generic Media fields, evaluated live per Profile on every read.

type smartTrack struct {
	title    string
	duration int
	year     int
	genre    string
	bpm      float64
}

// seedSmartTracks imports audio Media into library-local with a duration,
// year, a "genre" entity relation and a plugin "bpm" number field.
func seedSmartTracks(t *testing.T, st *Store, tracks []smartTrack) map[string]MediaID {
	t.Helper()
	ctx := context.Background()
	ids := map[string]MediaID{}
	for _, track := range tracks {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:           "library-local",
			Path:                "/music/" + track.title + ".flac",
			Title:               track.title,
			Container:           "flac",
			Codec:               "flac",
			Class:               MediaClassAudio,
			Year:                track.year,
			DurationSecondsHint: track.duration,
			ContentFingerprint:  "fp-smart-" + track.title,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[track.title] = media.ID
		if track.bpm > 0 {
			if err := st.SetMediaFields(ctx, media.ID, []MediaField{{Name: "bpm", Type: EntityFieldNumber, Number: track.bpm}}); err != nil {
				t.Fatal(err)
			}
		}
		if track.genre != "" {
			genreID := EntityID("genre-" + track.genre)
			if err := st.UpsertEntity(ctx, Entity{ID: genreID, LibraryID: "library-local", Kind: "genre", Title: track.genre}); err != nil {
				t.Fatal(err)
			}
			if err := st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: genreID, Name: "classifies", ToMediaID: media.ID}); err != nil {
				t.Fatal(err)
			}
		}
	}
	return ids
}

var smartLocal = LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

func smartItems(t *testing.T, st *Store, filter LibraryFilter, profile ProfileID, id SmartCollectionID, limit, offset int) []string {
	t.Helper()
	items := mustSourceItems(t, st, filter, SourceQuery{Kind: "smart-collection", Value: string(id), ProfileID: profile, Limit: limit, Offset: offset})
	titles := make([]string, 0, len(items))
	for _, item := range items {
		titles = append(titles, item.Title)
	}
	return titles
}

func mustSmartCollection(t *testing.T, st *Store, library LibraryID, source, sort string) SmartCollectionID {
	t.Helper()
	c, err := st.CreateSmartCollection(context.Background(), SmartCollection{LibraryID: library, Name: "Smart", SourceExpr: source, SortExpr: sort}, time.Now())
	if err != nil {
		t.Fatalf("CreateSmartCollection: %v", err)
	}
	return c.ID
}

func smartFixture(t *testing.T) (*Store, map[string]MediaID) {
	t.Helper()
	st := openSeeded(t)
	ids := seedSmartTracks(t, st, []smartTrack{
		{title: "Arrival", duration: 200, year: 1979, genre: "Rock", bpm: 120},
		{title: "Blue Line", duration: 320, year: 1994, genre: "Jazz", bpm: 90},
		{title: "Cold Front", duration: 150, year: 2004, genre: "Rock", bpm: 140},
		{title: "Dawn Chorus", duration: 410, year: 2021, genre: "Ambient"},
	})
	return st, ids
}

func TestSmartRulesMatchAllAndAny(t *testing.T) {
	st, _ := smartFixture(t)
	all := mustSmartCollection(t, st, "library-local",
		`{"kind":"rules","match":"all","rules":[{"field":"entity:genre","op":"is","value":"rock"},{"field":"year","op":"gte","value":1990}]}`, "")
	if got := smartItems(t, st, smartLocal, "profile-default", all, 50, 0); !slices.Equal(got, []string{"Cold Front"}) {
		t.Fatalf("match all = %v", got)
	}
	anyID := mustSmartCollection(t, st, "library-local",
		`{"kind":"rules","match":"any","rules":[{"field":"entity:genre","op":"is","value":"Jazz"},{"field":"duration","op":"gt","value":400}]}`, "")
	if got := smartItems(t, st, smartLocal, "profile-default", anyID, 50, 0); !slices.Equal(got, []string{"Blue Line", "Dawn Chorus"}) {
		t.Fatalf("match any = %v", got)
	}
}

func TestSmartRulesTextPluginAndNegatedOperators(t *testing.T) {
	st, _ := smartFixture(t)
	cases := []struct {
		name  string
		rules string
		want  []string
	}{
		{"title contains", `[{"field":"title","op":"contains","value":"LINE"}]`, []string{"Blue Line"}},
		{"title begins", `[{"field":"title","op":"beginsWith","value":"co"}]`, []string{"Cold Front"}},
		{"plugin number range", `[{"field":"field:bpm","op":"gte","value":100},{"field":"field:bpm","op":"lt","value":130}]`, []string{"Arrival"}},
		{"plugin number equality", `[{"field":"field:bpm","op":"is","value":90}]`, []string{"Blue Line"}},
		// An item with no genre relation, or no bpm, matches the negated rule.
		{"genre is not", `[{"field":"entity:genre","op":"isNot","value":"Rock"},{"field":"year","op":"gt","value":0}]`, []string{"Blue Line", "Dawn Chorus"}},
		{"plugin is not", `[{"field":"field:bpm","op":"isNot","value":120},{"field":"year","op":"gt","value":0}]`, []string{"Blue Line", "Cold Front", "Dawn Chorus"}},
		{"title not contains", `[{"field":"title","op":"notContains","value":"o"}]`, []string{"Arrival", "Blue Line", "Sample Media"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := mustSmartCollection(t, st, "library-local", `{"kind":"rules","rules":`+tc.rules+`}`, "")
			if got := smartItems(t, st, smartLocal, "profile-default", id, 50, 0); !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSmartRulesRefreshOnPlayAndRating proves the list follows the reading
// Profile's play history and ratings without any refresh step.
func TestSmartRulesRefreshOnPlayAndRating(t *testing.T) {
	st, ids := smartFixture(t)
	ctx := context.Background()
	played := mustSmartCollection(t, st, "library-local",
		`{"kind":"rules","rules":[{"field":"playCount","op":"gte","value":1},{"field":"lastPlayed","op":"inLast","value":7}]}`, `{"by":"playCount","dir":"desc"}`)
	if got := smartItems(t, st, smartLocal, "profile-default", played, 50, 0); len(got) != 0 {
		t.Fatalf("before any play = %v", got)
	}
	audio := ResumePolicy{Class: MediaClassAudio}
	for _, title := range []string{"Blue Line", "Arrival", "Arrival"} {
		if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{ProfileID: "profile-default", MediaID: ids[title], DurationSeconds: 100}, true, audio); err != nil {
			t.Fatal(err)
		}
		// Leave the watched state so the next finished report counts again.
		if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{ProfileID: "profile-default", MediaID: ids[title], PositionSeconds: 1, DurationSeconds: 100}, false, audio); err != nil {
			t.Fatal(err)
		}
	}
	got := smartItems(t, st, smartLocal, "profile-default", played, 50, 0)
	if !slices.Equal(got, []string{"Arrival", "Blue Line"}) {
		t.Fatalf("after plays = %v", got)
	}
	if got := smartItems(t, st, smartLocal, "profile-managed", played, 50, 0); len(got) != 0 {
		t.Fatalf("another profile sees %v", got)
	}

	neverPlayed := mustSmartCollection(t, st, "library-local",
		`{"kind":"rules","rules":[{"field":"lastPlayed","op":"notInLast","value":30},{"field":"year","op":"gt","value":0}]}`, "")
	if got := smartItems(t, st, smartLocal, "profile-default", neverPlayed, 50, 0); !slices.Equal(got, []string{"Cold Front", "Dawn Chorus"}) {
		t.Fatalf("not played recently = %v", got)
	}

	rated := mustSmartCollection(t, st, "library-local", `{"kind":"rules","rules":[{"field":"rating","op":"gte","value":8}]}`, "")
	if err := st.SetItemRating(ctx, "profile-default", ItemTarget{MediaID: ids["Dawn Chorus"]}, 9, smartLocal); err != nil {
		t.Fatal(err)
	}
	if got := smartItems(t, st, smartLocal, "profile-default", rated, 50, 0); !slices.Equal(got, []string{"Dawn Chorus"}) {
		t.Fatalf("rated = %v", got)
	}
	if err := st.SetItemRating(ctx, "profile-default", ItemTarget{MediaID: ids["Dawn Chorus"]}, 0, smartLocal); err != nil {
		t.Fatal(err)
	}
	if got := smartItems(t, st, smartLocal, "profile-default", rated, 50, 0); len(got) != 0 {
		t.Fatalf("after clearing the rating = %v", got)
	}
}

func TestSmartRulesRefreshOnLibraryChange(t *testing.T) {
	st, _ := smartFixture(t)
	id := mustSmartCollection(t, st, "library-local", `{"kind":"rules","rules":[{"field":"entity:genre","op":"is","value":"Ambient"}]}`, "")
	if got := smartItems(t, st, smartLocal, "profile-default", id, 50, 0); !slices.Equal(got, []string{"Dawn Chorus"}) {
		t.Fatalf("before = %v", got)
	}
	seedSmartTracks(t, st, []smartTrack{{title: "Echoes", duration: 300, year: 2022, genre: "Ambient"}})
	if got := smartItems(t, st, smartLocal, "profile-default", id, 50, 0); !slices.Equal(got, []string{"Dawn Chorus", "Echoes"}) {
		t.Fatalf("after import = %v", got)
	}
}

func TestSmartRulesSortAndLimitAcrossPages(t *testing.T) {
	st, _ := smartFixture(t)
	id := mustSmartCollection(t, st, "library-local",
		`{"kind":"rules","rules":[{"field":"year","op":"gt","value":0}],"limit":3}`, `{"by":"duration","dir":"desc"}`)
	pages := [][]string{
		smartItems(t, st, smartLocal, "profile-default", id, 2, 0),
		smartItems(t, st, smartLocal, "profile-default", id, 2, 2),
		smartItems(t, st, smartLocal, "profile-default", id, 2, 4),
	}
	if !slices.Equal(pages[0], []string{"Dawn Chorus", "Blue Line"}) || !slices.Equal(pages[1], []string{"Arrival"}) || len(pages[2]) != 0 {
		t.Fatalf("pages = %v", pages)
	}

	byYear := mustSmartCollection(t, st, "library-local", `{"kind":"rules","rules":[]}`, `{"by":"year"}`)
	got := smartItems(t, st, smartLocal, "profile-default", byYear, 50, 0)
	// The seeded item has no year and sorts last.
	if !slices.Equal(got, []string{"Arrival", "Blue Line", "Cold Front", "Dawn Chorus", "Sample Media"}) {
		t.Fatalf("by year = %v", got)
	}

	random := mustSmartCollection(t, st, "library-local", `{"kind":"rules","rules":[{"field":"year","op":"gt","value":0}],"limit":3}`, `{"by":"random"}`)
	first := smartItems(t, st, smartLocal, "profile-default", random, 50, 0)
	paged := append(smartItems(t, st, smartLocal, "profile-default", random, 2, 0), smartItems(t, st, smartLocal, "profile-default", random, 2, 2)...)
	if len(first) != 3 || !slices.Equal(first, paged) {
		t.Fatalf("random = %v, paged = %v", first, paged)
	}
}

// TestSmartRulesLimitCountsSurvivors proves the rule limit caps the list the
// Profile sees after Hide watched drops finished Media, for an ordered and a
// random sort.
func TestSmartRulesLimitCountsSurvivors(t *testing.T) {
	st, ids := smartFixture(t)
	ctx := context.Background()
	if err := st.SetClientPref(ctx, "profile-default", ProfileHideWatchedPrefKey, "on", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{ProfileID: "profile-default", MediaID: ids["Arrival"], DurationSeconds: 100}, true, ResumePolicy{Class: MediaClassAudio}); err != nil {
		t.Fatal(err)
	}
	first := mustSmartCollection(t, st, "library-local", `{"kind":"rules","rules":[{"field":"year","op":"gt","value":0}],"limit":1}`, "")
	if got := smartItems(t, st, smartLocal, "profile-default", first, 50, 0); !slices.Equal(got, []string{"Blue Line"}) {
		t.Fatalf("limit 1 after hiding the watched first match = %v", got)
	}
	random := mustSmartCollection(t, st, "library-local", `{"kind":"rules","rules":[{"field":"year","op":"gt","value":0}],"limit":3}`, `{"by":"random"}`)
	got := smartItems(t, st, smartLocal, "profile-default", random, 50, 0)
	if len(got) != 3 || slices.Contains(got, "Arrival") {
		t.Fatalf("random limit 3 = %v", got)
	}
	if got := smartItems(t, st, smartLocal, "profile-managed", first, 50, 0); !slices.Equal(got, []string{"Arrival"}) {
		t.Fatalf("a Profile that keeps watched Media = %v", got)
	}
}

// TestSmartRulesRecentDatesEndNow proves "in the last N days" stops at now:
// a future date is outside it and inside its complement.
func TestSmartRulesRecentDatesEndNow(t *testing.T) {
	st, ids := smartFixture(t)
	ctx := context.Background()
	for title, offset := range map[string]time.Duration{"Blue Line": -48 * time.Hour, "Cold Front": 48 * time.Hour} {
		if err := st.SetMediaFields(ctx, ids[title], []MediaField{{Name: "seen", Type: EntityFieldDate, Date: time.Now().Add(offset)}}); err != nil {
			t.Fatal(err)
		}
	}
	recent := mustSmartCollection(t, st, "library-local", `{"kind":"rules","rules":[{"field":"field:seen","op":"inLast","value":7}]}`, "")
	if got := smartItems(t, st, smartLocal, "profile-default", recent, 50, 0); !slices.Equal(got, []string{"Blue Line"}) {
		t.Fatalf("in the last 7 days = %v", got)
	}
	notRecent := mustSmartCollection(t, st, "library-local", `{"kind":"rules","rules":[{"field":"field:seen","op":"notInLast","value":7},{"field":"year","op":"gt","value":0}]}`, "")
	if got := smartItems(t, st, smartLocal, "profile-default", notRecent, 50, 0); !slices.Equal(got, []string{"Arrival", "Cold Front", "Dawn Chorus"}) {
		t.Fatalf("not in the last 7 days = %v", got)
	}
}

func TestSmartRulesRespectLibraryVisibility(t *testing.T) {
	st := openSeeded(t)
	id := mustSmartCollection(t, st, "library-archive", `{"kind":"rules","rules":[]}`, "")
	archive := LibraryFilter{AllowedIDs: []LibraryID{"library-archive"}}
	if got := smartItems(t, st, archive, "profile-default", id, 50, 0); len(got) != 0 {
		t.Fatalf("restricted library unrevealed = %v", got)
	}
	revealed := archive
	revealed.Revealed = true
	if got := smartItems(t, st, revealed, "profile-default", id, 50, 0); !slices.Equal(got, []string{"Archive Media"}) {
		t.Fatalf("revealed = %v", got)
	}
	if got := smartItems(t, st, smartLocal, "profile-default", id, 50, 0); len(got) != 0 {
		t.Fatalf("other library session = %v", got)
	}
}

func TestSmartRulesValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	bad := []struct{ source, sort string }{
		{`{"kind":"rules","rules":[{"field":"mood","op":"is","value":"x"}]}`, ""},
		{`{"kind":"rules","rules":[{"field":"title","op":"gt","value":"x"}]}`, ""},
		{`{"kind":"rules","rules":[{"field":"year","op":"is","value":"1990"}]}`, ""},
		{`{"kind":"rules","rules":[{"field":"lastPlayed","op":"inLast","value":1.5}]}`, ""},
		{`{"kind":"rules","match":"some","rules":[]}`, ""},
		{`{"kind":"rules","rules":[],"limit":0}`, ""},
		{`{"kind":"rules","rules":[],"limit":5001}`, ""},
		{`{"kind":"rules","rules":[]}`, `{"by":"shoeSize"}`},
		{`{"kind":"rules","rules":[]}`, `{"by":"title","dir":"up"}`},
	}
	for _, tc := range bad {
		_, err := st.CreateSmartCollection(ctx, SmartCollection{LibraryID: "library-local", Name: "Bad", SourceExpr: tc.source, SortExpr: tc.sort}, time.Now())
		var verr ValidationError
		if !errors.As(err, &verr) {
			t.Errorf("source %s sort %s: err = %v, want validation error", tc.source, tc.sort, err)
		}
	}
	id := mustSmartCollection(t, st, "library-local", `{"kind":"rules","rules":[]}`, "")
	_, err := st.UpdateSmartCollection(ctx, SmartCollection{ID: id, SourceExpr: `{"kind":"rules","rules":[{"field":"rating","op":"contains","value":"x"}]}`})
	requireValidationError(t, "update", err)
	if _, err := decodeSmartCollectionSourceExpr(`{"kind":"rules"}`); err == nil {
		t.Fatal("the generic decoder must refuse rule sources")
	}
}

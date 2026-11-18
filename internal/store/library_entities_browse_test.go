package store

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"
)

// libraryEntityBrowseFixture seeds four "box" Entities judged by the Media
// they reach (CORE-2440):
//
//	Alpha: a (2001, 100s, 2160p, watched first, rated 5), b (1995, 50s, 720p)
//	Beta:  c (2010, 300s, 1080p, watched second, rated 8), in genre Rock
//	Delta: d through its lid (1980, 1000s, 480p, in progress last), linked to genre Jazz
//	Gamma: no Media
func libraryEntityBrowseFixture(t *testing.T) (*Store, LibraryEntitiesQuery) {
	t.Helper()
	st, lib, _ := relatedFixture(t)
	ctx := context.Background()
	for id, title := range map[EntityID]string{"entity-box-a": "Alpha", "entity-box-b": "Beta", "entity-box-d": "Delta", "entity-box-g": "Gamma"} {
		upsertRelatedEntity(t, st, id, lib.ID, "box", title)
	}
	upsertRelatedEntity(t, st, "entity-lid-1", lib.ID, "lid", "Lid")
	upsertRelatedEntity(t, st, "entity-genre-rock", lib.ID, "genre", "Rock")
	upsertRelatedEntity(t, st, "entity-genre-jazz", lib.ID, "genre", "Jazz")
	media := map[string]MediaID{}
	for _, m := range []struct {
		name                   string
		year, duration, height int
	}{{"a", 2001, 100, 2160}, {"b", 1995, 50, 720}, {"c", 2010, 300, 1080}, {"d", 1980, 1000, 480}} {
		item, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: lib.ID, Path: "/shelf/" + m.name + ".mkv", Title: m.name, Container: "mkv", Codec: "h264",
			Class: MediaClassVideo, ContentFingerprint: "fp-" + m.name, Year: m.year,
			DurationSecondsHint: m.duration, VideoWidthHint: m.height * 16 / 9, VideoHeightHint: m.height,
		})
		if err != nil {
			t.Fatal(err)
		}
		media[m.name] = item.ID
	}
	relateEntities(t, st,
		EntityRelation{FromEntityID: "entity-box-a", Name: "holds", ToMediaID: media["a"]},
		EntityRelation{FromEntityID: "entity-box-a", Name: "holds", ToMediaID: media["b"]},
		EntityRelation{FromEntityID: "entity-box-b", Name: "holds", ToMediaID: media["c"]},
		EntityRelation{FromEntityID: "entity-lid-1", Name: "lid-of", ToEntityID: "entity-box-d"},
		EntityRelation{FromEntityID: "entity-lid-1", Name: "holds", ToMediaID: media["d"]},
		EntityRelation{FromEntityID: "entity-genre-rock", Name: "tags", ToMediaID: media["c"]},
		EntityRelation{FromEntityID: "entity-box-d", Name: "styled", ToEntityID: "entity-genre-jazz"},
	)
	profile := ProfileID("profile-default")
	// The write stamps its own clock, so each write waits a moment to keep
	// the last-watched order a, c, d.
	for _, p := range []PlaybackProgress{
		{ProfileID: profile, MediaID: media["a"], PositionSeconds: 100, DurationSeconds: 100, Watched: true},
		{ProfileID: profile, MediaID: media["c"], PositionSeconds: 300, DurationSeconds: 300, Watched: true},
		{ProfileID: profile, MediaID: media["d"], PositionSeconds: 200, DurationSeconds: 1000},
	} {
		if err := st.UpsertPlaybackProgress(ctx, p, p.Watched, ResumePolicy{Class: MediaClassVideo}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Millisecond)
	}
	filter := LibraryFilter{AllLibraries: true}
	for name, rating := range map[string]int{"a": 5, "c": 8} {
		if err := st.SetItemRating(ctx, profile, ItemTarget{MediaID: media[name]}, rating, filter); err != nil {
			t.Fatal(err)
		}
	}
	return st, LibraryEntitiesQuery{
		LibraryID:  lib.ID,
		Kinds:      []string{"box"},
		Kind:       "box",
		ChildKinds: []string{"lid"},
		Browse:     LibraryBrowseQuery{ProfileID: profile},
	}
}

func browseEntityTitles(t *testing.T, st *Store, query LibraryEntitiesQuery) ([]string, LibraryEntitiesResult) {
	t.Helper()
	got, err := st.LibraryEntities(context.Background(), LibraryFilter{AllLibraries: true}, query)
	if err != nil {
		t.Fatal(err)
	}
	titles := make([]string, len(got.Items))
	for i, item := range got.Items {
		titles[i] = item.Item.Title
	}
	return titles, got
}

// TestLibraryEntitiesBrowseSorts pins each sort on an Entity kind: keys come
// from reached Media, an Entity without the key trails in both directions, and
// a title sort reports the letter index.
func TestLibraryEntitiesBrowseSorts(t *testing.T) {
	st, base := libraryEntityBrowseFixture(t)
	for _, tc := range []struct {
		sort string
		desc bool
		want []string
	}{
		{"", false, []string{"Alpha", "Beta", "Delta", "Gamma"}},
		{LibraryBrowseSortTitle, true, []string{"Gamma", "Delta", "Beta", "Alpha"}},
		{LibraryBrowseSortYear, false, []string{"Delta", "Alpha", "Beta", "Gamma"}},
		{LibraryBrowseSortYear, true, []string{"Beta", "Alpha", "Delta", "Gamma"}},
		{LibraryBrowseSortAdded, false, []string{"Alpha", "Beta", "Delta", "Gamma"}},
		{LibraryBrowseSortAdded, true, []string{"Delta", "Beta", "Alpha", "Gamma"}},
		{LibraryBrowseSortDuration, false, []string{"Alpha", "Beta", "Delta", "Gamma"}},
		{LibraryBrowseSortDuration, true, []string{"Delta", "Beta", "Alpha", "Gamma"}},
		{LibraryBrowseSortLastWatched, true, []string{"Delta", "Beta", "Alpha", "Gamma"}},
		{LibraryBrowseSortRating, true, []string{"Beta", "Alpha", "Delta", "Gamma"}},
		{LibraryBrowseSortRating, false, []string{"Alpha", "Beta", "Delta", "Gamma"}},
	} {
		query := base
		query.Browse.Sort, query.Browse.Descending = tc.sort, tc.desc
		titles, got := browseEntityTitles(t, st, query)
		if !reflect.DeepEqual(titles, tc.want) || got.Total != 4 {
			t.Errorf("sort %q desc=%v = %v (total %d), want %v", tc.sort, tc.desc, titles, got.Total, tc.want)
		}
		wantLetters := tc.sort == "" || tc.sort == LibraryBrowseSortTitle
		if (len(got.Letters) > 0) != wantLetters {
			t.Errorf("sort %q letters = %v, want letters only for a title sort", tc.sort, got.Letters)
		}
	}

	_, ascending := browseEntityTitles(t, st, base)
	if want := []LibraryBrowseLetter{{"A", 0, 1}, {"B", 1, 1}, {"D", 2, 1}, {"G", 3, 1}}; !reflect.DeepEqual(ascending.Letters, want) {
		t.Fatalf("letters = %v, want %v", ascending.Letters, want)
	}

	shuffle := base
	shuffle.Browse.Sort, shuffle.Browse.Seed = LibraryBrowseSortRandom, "s1"
	first, _ := browseEntityTitles(t, st, shuffle)
	again, _ := browseEntityTitles(t, st, shuffle)
	sorted := slices.Sorted(slices.Values(first))
	if !reflect.DeepEqual(first, again) || !reflect.DeepEqual(sorted, []string{"Alpha", "Beta", "Delta", "Gamma"}) {
		t.Fatalf("seeded shuffle = %v then %v, want one stable permutation of every box", first, again)
	}

	// Paging walks the sorted set and keeps its total.
	page := base
	page.Browse.Sort, page.Browse.Descending = LibraryBrowseSortYear, true
	page.Limit, page.Offset = 2, 1
	if titles, got := browseEntityTitles(t, st, page); !reflect.DeepEqual(titles, []string{"Alpha", "Delta"}) || got.Total != 4 {
		t.Fatalf("year page = %v (total %d), want [Alpha Delta] of 4", titles, got.Total)
	}
}

// TestLibraryEntitiesBrowseFilters pins each filter on an Entity kind: watch
// states over reached Media, the earliest year, any matching quality, the
// highest rating, and grouping values linked to the Entity or its Media. An
// Entity reaching no Media drops out of every filtered view; kind totals stay
// unfiltered.
func TestLibraryEntitiesBrowseFilters(t *testing.T) {
	st, base := libraryEntityBrowseFixture(t)
	for name, tc := range map[string]struct {
		browse LibraryBrowseQuery
		want   []string
	}{
		"unwatched":    {LibraryBrowseQuery{Watch: LibraryBrowseWatchUnwatched}, []string{"Alpha", "Delta"}},
		"in progress":  {LibraryBrowseQuery{Watch: LibraryBrowseWatchInProgress}, []string{"Delta"}},
		"watched":      {LibraryBrowseQuery{Watch: LibraryBrowseWatchWatched}, []string{"Beta"}},
		"years":        {LibraryBrowseQuery{YearFrom: 1990, YearTo: 2005}, []string{"Alpha"}},
		"until 1990":   {LibraryBrowseQuery{YearTo: 1990}, []string{"Delta"}},
		"uhd":          {LibraryBrowseQuery{Quality: QualityTierUHD}, []string{"Alpha"}},
		"720p":         {LibraryBrowseQuery{Quality: QualityTierHD720}, []string{"Alpha"}},
		"rating":       {LibraryBrowseQuery{MinRating: 6}, []string{"Beta"}},
		"media group":  {LibraryBrowseQuery{EntityFilters: []EntityFilter{{Kind: "genre", Name: "Rock"}}}, []string{"Beta"}},
		"direct group": {LibraryBrowseQuery{EntityFilters: []EntityFilter{{Kind: "genre", Name: "Jazz"}}}, []string{"Delta"}},
		"both groups":  {LibraryBrowseQuery{EntityFilters: []EntityFilter{{Kind: "genre", Name: "Rock"}, {Kind: "genre", Name: "Jazz"}}}, []string{}},
		"combined":     {LibraryBrowseQuery{Watch: LibraryBrowseWatchUnwatched, Sort: LibraryBrowseSortYear, Descending: true}, []string{"Alpha", "Delta"}},
	} {
		query := base
		tc.browse.ProfileID = base.Browse.ProfileID
		query.Browse = tc.browse
		titles, got := browseEntityTitles(t, st, query)
		if !reflect.DeepEqual(titles, tc.want) || got.Total != len(tc.want) || got.Totals["box"] != 4 {
			t.Errorf("%s = %v (total %d, kind total %d), want %v", name, titles, got.Total, got.Totals["box"], tc.want)
		}
	}

	// Without a Profile nothing is watched or rated.
	anon := base
	anon.Browse = LibraryBrowseQuery{Watch: LibraryBrowseWatchUnwatched}
	if titles, _ := browseEntityTitles(t, st, anon); !reflect.DeepEqual(titles, []string{"Alpha", "Beta", "Delta"}) {
		t.Fatalf("unwatched without a Profile = %v, want every box with Media", titles)
	}
}

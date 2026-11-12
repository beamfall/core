package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

func mustLibraryBrowse(t *testing.T, st *Store, query LibraryBrowseQuery) LibraryBrowseResult {
	t.Helper()
	query.LibraryID = "library-demo-open"
	result, err := st.LibraryBrowse(context.Background(), LibraryFilter{AllowedIDs: []LibraryID{"library-demo-open"}}, query)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestLibraryBrowseCountsSortsAndPages(t *testing.T) {
	st := openDemo(t)
	all := mustLibraryBrowse(t, st, LibraryBrowseQuery{})
	if all.Total < 8 || len(all.Items) != all.Total {
		t.Fatalf("total = %d, items = %d; want the whole open demo library", all.Total, len(all.Items))
	}
	titles := func(items []SourceItem) []string {
		out := make([]string, len(items))
		for i, item := range items {
			out[i] = item.Title
		}
		return out
	}
	if got := titles(all.Items); !slices.IsSorted(got) {
		t.Fatalf("default order is not by title: %v", got)
	}
	desc := mustLibraryBrowse(t, st, LibraryBrowseQuery{Descending: true})
	reversed := slices.Clone(titles(all.Items))
	slices.Reverse(reversed)
	if got := titles(desc.Items); !slices.Equal(got, reversed) {
		t.Fatalf("descending title order = %v, want %v", got, reversed)
	}

	// Paging keeps the total and walks the same order.
	page := mustLibraryBrowse(t, st, LibraryBrowseQuery{Limit: 3, Offset: 2})
	if page.Total != all.Total || !slices.Equal(titles(page.Items), titles(all.Items[2:5])) {
		t.Fatalf("page = %v (total %d), want %v (total %d)", titles(page.Items), page.Total, titles(all.Items[2:5]), all.Total)
	}

	newest := mustLibraryBrowse(t, st, LibraryBrowseQuery{Sort: LibraryBrowseSortYear, Descending: true})
	oldest := mustLibraryBrowse(t, st, LibraryBrowseQuery{Sort: LibraryBrowseSortYear})
	if oldest.Items[0].ID != "media-demo-bunny" || newest.Items[0].ID == oldest.Items[0].ID || newest.Total != all.Total {
		t.Fatalf("year order: oldest-first opens on %s, newest-first on %s", oldest.Items[0].ID, newest.Items[0].ID)
	}
	shortest := mustLibraryBrowse(t, st, LibraryBrowseQuery{Sort: LibraryBrowseSortDuration})
	short := slices.IndexFunc(shortest.Items, func(i SourceItem) bool { return i.ID == "media-demo-caminandes" })
	long := slices.IndexFunc(shortest.Items, func(i SourceItem) bool { return i.ID == "media-demo-sintel" })
	if short < 0 || long < 0 || short > long {
		t.Fatalf("shortest-first puts the 90s item at %d and the 888s item at %d", short, long)
	}

	// A seeded shuffle is stable for one seed and covers the same set.
	a := mustLibraryBrowse(t, st, LibraryBrowseQuery{Sort: LibraryBrowseSortRandom, Seed: "s1"})
	b := mustLibraryBrowse(t, st, LibraryBrowseQuery{Sort: LibraryBrowseSortRandom, Seed: "s1"})
	if !slices.Equal(titles(a.Items), titles(b.Items)) || a.Total != all.Total {
		t.Fatalf("seeded shuffle is not stable: %v vs %v", titles(a.Items), titles(b.Items))
	}
}

func TestLibraryBrowseFiltersAndFacets(t *testing.T) {
	st := openDemo(t)
	ids := func(result LibraryBrowseResult) []MediaID {
		out := make([]MediaID, len(result.Items))
		for i, item := range result.Items {
			out[i] = item.ID
		}
		slices.Sort(out)
		return out
	}
	all := mustLibraryBrowse(t, st, LibraryBrowseQuery{FacetEntityKind: "category"})
	if !slices.Equal(all.Facets.Entities, []string{"Comedy", "Science"}) {
		t.Fatalf("entity facets = %v, want [Comedy Science]", all.Facets.Entities)
	}
	if !slices.Contains(all.Facets.Decades, 2010) || !slices.Contains(all.Facets.Decades, 2020) || !slices.IsSortedFunc(all.Facets.Decades, func(a, b int) int { return b - a }) {
		t.Fatalf("decade facets = %v, want newest-first incl. 2010 and 2020", all.Facets.Decades)
	}
	if !slices.Equal(all.Facets.Qualities, []string{QualityTierUHD, QualityTierHD1080, QualityTierHD720}) {
		t.Fatalf("quality facets = %v", all.Facets.Qualities)
	}

	comedy := mustLibraryBrowse(t, st, LibraryBrowseQuery{EntityFilter: &EntityFilter{Kind: "category", Name: "Comedy"}})
	if got := ids(comedy); !slices.Equal(got, []MediaID{"media-demo-bunny", "media-demo-caminandes"}) || comedy.Total != 2 {
		t.Fatalf("comedy = %v (total %d)", got, comedy.Total)
	}
	tens := mustLibraryBrowse(t, st, LibraryBrowseQuery{YearFrom: 2010, YearTo: 2019})
	if got := ids(tens); !slices.Equal(got, []MediaID{"media-demo-caminandes", "media-demo-sintel", "media-demo-spring", "media-demo-steel"}) {
		t.Fatalf("2010s = %v", got)
	}
	uhd := mustLibraryBrowse(t, st, LibraryBrowseQuery{Quality: QualityTierUHD})
	if got := ids(uhd); !slices.Equal(got, []MediaID{"media-demo-steel"}) {
		t.Fatalf("uhd = %v", got)
	}

	profile := ProfileID("profile-demo-default")
	now := time.Now().UTC()
	for _, p := range []PlaybackProgress{
		{ProfileID: profile, MediaID: "media-demo-bunny", PositionSeconds: 590, DurationSeconds: 596, Watched: true, UpdatedAt: now.Add(-time.Hour)},
		{ProfileID: profile, MediaID: "media-demo-sintel", PositionSeconds: 200, DurationSeconds: 888, UpdatedAt: now},
	} {
		if err := st.UpsertPlaybackProgress(context.Background(), p, p.Watched, ResumePolicy{Class: MediaClassVideo}); err != nil {
			t.Fatal(err)
		}
	}
	inProgress := mustLibraryBrowse(t, st, LibraryBrowseQuery{ProfileID: profile, Watch: LibraryBrowseWatchInProgress})
	if got := ids(inProgress); !slices.Contains(got, "media-demo-sintel") || slices.Contains(got, "media-demo-bunny") || inProgress.Total >= all.Total {
		t.Fatalf("in-progress = %v (total %d of %d)", got, inProgress.Total, all.Total)
	}
	unwatched := mustLibraryBrowse(t, st, LibraryBrowseQuery{ProfileID: profile, Watch: LibraryBrowseWatchUnwatched})
	if unwatched.Total >= all.Total || slices.Contains(ids(unwatched), "media-demo-bunny") || !slices.Contains(ids(unwatched), "media-demo-sintel") {
		t.Fatalf("unwatched total = %d (all %d), ids %v", unwatched.Total, all.Total, ids(unwatched))
	}
	recent := mustLibraryBrowse(t, st, LibraryBrowseQuery{ProfileID: profile, Sort: LibraryBrowseSortLastWatched, Descending: true})
	order := make([]MediaID, len(recent.Items))
	for i, item := range recent.Items {
		order[i] = item.ID
	}
	sintel, bunny := slices.Index(order, "media-demo-sintel"), slices.Index(order, "media-demo-bunny")
	if sintel < 0 || bunny < 0 || sintel > bunny || recent.Total != all.Total {
		t.Fatalf("last-watched order = %v (total %d of %d)", order, recent.Total, all.Total)
	}

	// A Library outside the caller's allow-list yields nothing, not an error.
	other, err := st.LibraryBrowse(context.Background(), LibraryFilter{AllowedIDs: []LibraryID{"library-demo-archive"}}, LibraryBrowseQuery{LibraryID: "library-demo-open"})
	if err != nil || other.Total != 0 || len(other.Items) != 0 {
		t.Fatalf("out-of-scope library = %+v, %v", other, err)
	}
}

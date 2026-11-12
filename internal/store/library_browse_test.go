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
	all := mustLibraryBrowse(t, st, LibraryBrowseQuery{FacetEntityKinds: []string{"category", "absent"}})
	if len(all.Facets.Entities) != 2 || all.Facets.Entities[0].Kind != "category" ||
		!slices.Equal(all.Facets.Entities[0].Titles, []string{"Comedy", "Science"}) ||
		all.Facets.Entities[1].Kind != "absent" || len(all.Facets.Entities[1].Titles) != 0 {
		t.Fatalf("entity facets = %+v, want category [Comedy Science] then an empty absent kind", all.Facets.Entities)
	}
	if !slices.Contains(all.Facets.Decades, 2010) || !slices.Contains(all.Facets.Decades, 2020) || !slices.IsSortedFunc(all.Facets.Decades, func(a, b int) int { return b - a }) {
		t.Fatalf("decade facets = %v, want newest-first incl. 2010 and 2020", all.Facets.Decades)
	}
	if !slices.Equal(all.Facets.Qualities, []string{QualityTierUHD, QualityTierHD1080, QualityTierHD720}) {
		t.Fatalf("quality facets = %v", all.Facets.Qualities)
	}

	comedy := mustLibraryBrowse(t, st, LibraryBrowseQuery{EntityFilters: []EntityFilter{{Kind: "category", Name: "Comedy"}}})
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

func TestLibraryBrowseEntityFacetsOnlyNameVisibleItems(t *testing.T) {
	st := openDemo(t)
	ctx := context.Background()
	profile := ProfileID("profile-demo-default")
	// Mute the Comedy category: its items leave the browse, so the category
	// must stop being offered as a filter.
	var comedy string
	if err := st.catalogR.QueryRowContext(ctx, `SELECT id FROM entity WHERE kind = 'category' AND title = 'Comedy' AND library_id = 'library-demo-open'`).Scan(&comedy); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMute(ctx, profile, MuteKindEntity, comedy, false, true); err != nil {
		t.Fatal(err)
	}
	muted := mustLibraryBrowse(t, st, LibraryBrowseQuery{ProfileID: profile, FacetEntityKinds: []string{"category"}})
	if slices.ContainsFunc(muted.Items, func(i SourceItem) bool { return i.ID == "media-demo-bunny" }) {
		t.Fatal("a muted category's item is still browsable")
	}
	if len(muted.Facets.Entities) != 1 || !slices.Equal(muted.Facets.Entities[0].Titles, []string{"Science"}) {
		t.Fatalf("entity facets with Comedy muted = %+v, want [Science]", muted.Facets.Entities)
	}
}

func TestLibraryBrowseHidesItemsWhoseFilesAreAllMissing(t *testing.T) {
	st := openDemo(t)
	ctx := context.Background()
	before := mustLibraryBrowse(t, st, LibraryBrowseQuery{})
	res, err := st.catalogW.ExecContext(ctx, `UPDATE media_file SET missing_at_ms = 1 WHERE media_id = 'media-demo-bunny'`)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		t.Fatal("demo item has no file rows to mark missing")
	}
	after := mustLibraryBrowse(t, st, LibraryBrowseQuery{})
	if after.Total != before.Total-1 || slices.ContainsFunc(after.Items, func(i SourceItem) bool { return i.ID == "media-demo-bunny" }) {
		t.Fatalf("total %d -> %d; an item with no present file must leave the browse", before.Total, after.Total)
	}
}

func TestLibraryBrowseReadsAnOpenEncryptedLibrary(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	library := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, library, false, true); err != nil {
		t.Fatal(err)
	}
	browse := func() LibraryBrowseResult {
		t.Helper()
		result, err := st.LibraryBrowse(ctx, LibraryFilter{AllLibraries: true}, LibraryBrowseQuery{LibraryID: library})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if sealed := browse(); sealed.Total != 0 {
		t.Fatalf("sealed encrypted library browses %d items, want 0", sealed.Total)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, library, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	imported, _, err := st.ImportMediaFile(ctx, encryptedImport(library, "sha256:browse-encrypted", "/enc/browse.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	open := browse()
	if open.Total != 1 || len(open.Items) != 1 || open.Items[0].ID != imported.ID {
		t.Fatalf("open encrypted library browse = %+v, want the one imported item", open)
	}

	// A hide-mode certification ceiling is applied on the Encrypted Library's
	// own plane, which has no library table to join.
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE library SET plugin_id = 'photos' WHERE id = ?`, library); err != nil {
		t.Fatal(err)
	}
	tx, err := st.encryptedDBs[library].writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertMediaFields(ctx, tx, imported.ID, []MediaField{{Name: certificationField, Type: EntityFieldText, Text: "high"}}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for allowed, want := range map[string]int{"low": 0, "high": 1} {
		filter := LibraryFilter{AllLibraries: true, Certification: []CertificationConstraint{{PluginID: "photos", Allowed: []string{allowed}}}}
		result, err := st.LibraryBrowse(ctx, filter, LibraryBrowseQuery{LibraryID: library})
		if err != nil {
			t.Fatalf("browse under a %q ceiling: %v", allowed, err)
		}
		if result.Total != want {
			t.Fatalf("browse under a %q ceiling = %d items, want %d", allowed, result.Total, want)
		}
	}
}

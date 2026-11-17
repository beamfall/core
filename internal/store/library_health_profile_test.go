package store

import (
	"context"
	"testing"
	"time"
)

// healthCountsFor returns one Library's health counts.
func healthCountsFor(t *testing.T, st *Store, libraryID LibraryID) map[HealthDimension]int {
	t.Helper()
	counts, err := st.LibraryHealthCounts(context.Background(), LibraryFilter{AllowedIDs: []LibraryID{libraryID}})
	if err != nil {
		t.Fatalf("LibraryHealthCounts: %v", err)
	}
	for _, c := range counts {
		if c.LibraryID == libraryID {
			return c.Counts
		}
	}
	t.Fatalf("%s missing from health counts", libraryID)
	return nil
}

// healthItemSet returns the drill-down items of one dimension as a set.
func healthItemSet(t *testing.T, st *Store, libraryID LibraryID, dim HealthDimension) map[MediaID]bool {
	t.Helper()
	items, err := st.LibraryHealthItems(context.Background(), libraryID, dim, LibraryFilter{AllowedIDs: []LibraryID{libraryID}}, 0)
	if err != nil {
		t.Fatalf("LibraryHealthItems(%s): %v", dim, err)
	}
	set := map[MediaID]bool{}
	for _, item := range items {
		set[item.MediaID] = true
	}
	return set
}

func applyHealthArtwork(t *testing.T, st *Store, id MediaID, slot string) {
	t.Helper()
	ctx := context.Background()
	all := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	if err := st.UpsertArtworkCandidate(ctx, id, slot, "https://art/"+string(id)+"/"+slot, "", ""); err != nil {
		t.Fatalf("UpsertArtworkCandidate: %v", err)
	}
	cands, err := st.ArtworkCandidates(ctx, id, slot, all)
	if err != nil || len(cands) != 1 {
		t.Fatalf("ArtworkCandidates = %v, err %v", cands, err)
	}
	if err := st.MarkArtworkCandidateApplied(ctx, cands[0].ID); err != nil {
		t.Fatalf("MarkArtworkCandidateApplied: %v", err)
	}
}

func setHealthMediaClass(t *testing.T, st *Store, id MediaID, class string) {
	t.Helper()
	if _, err := st.catalogW.ExecContext(context.Background(),
		`UPDATE media SET media_class = ? WHERE id = ?`, class, id); err != nil {
		t.Fatalf("set media class: %v", err)
	}
}

func identifyHealthMedia(t *testing.T, st *Store, id MediaID) {
	t.Helper()
	title := "Identified " + string(id)
	if err := st.UpdateMediaMetadata(context.Background(), id, MediaMetadata{Title: &title}); err != nil {
		t.Fatalf("UpdateMediaMetadata: %v", err)
	}
}

// TestLibraryHealthPluginLessLibrary: a Library no plugin owns reports its
// unidentified Media as no-scraper and reports no unmatched, scrape-failure or
// missing-artwork count, drill-down or cache row.
//
// feature:library-health
func TestLibraryHealthPluginLessLibrary(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	before := healthCountsFor(t, st, "library-local")
	seedHealthMedia(t, st, "library-local", "loose-a", "/m/loose-a.mp4", "1111000000000001")
	seedHealthMedia(t, st, "library-local", "loose-b", "/m/loose-b.mp4", "1111000000000002")
	// A stale cache row from before the Library lost its plugin is dropped.
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO library_health_cache (library_id, dimension, count, refreshed_at_ms)
		VALUES ('library-local', 'unmatched', 99, 1)`); err != nil {
		t.Fatalf("seed stale cache row: %v", err)
	}

	for _, refresh := range []bool{false, true} {
		if refresh {
			if err := st.RefreshLibraryHealthCache(ctx, time.Now().UTC()); err != nil {
				t.Fatalf("RefreshLibraryHealthCache: %v", err)
			}
		}
		counts := healthCountsFor(t, st, "library-local")
		for _, dim := range []HealthDimension{HealthUnmatched, HealthScrapeFailure, HealthMissingArtwork} {
			if n, ok := counts[dim]; ok {
				t.Errorf("refresh=%v: %s = %d reported for a plugin-less Library", refresh, dim, n)
			}
		}
		for _, dim := range []HealthDimension{HealthNoScraper, HealthBroken, HealthDuplicate} {
			if _, ok := counts[dim]; !ok {
				t.Errorf("refresh=%v: %s missing from %v", refresh, dim, counts)
			}
		}
		if got := counts[HealthNoScraper] - before[HealthNoScraper]; got != 2 {
			t.Errorf("refresh=%v: no-scraper delta = %d, want 2", refresh, got)
		}
	}

	if items := healthItemSet(t, st, "library-local", HealthUnmatched); len(items) != 0 {
		t.Errorf("unmatched drill-down = %v, want empty", items)
	}
	if bulk, err := st.LibraryHealthBulkItems(ctx, "library-local", HealthMissingArtwork, all); err != nil || len(bulk) != 0 {
		t.Errorf("missing-artwork bulk items = %v, err %v, want empty", bulk, err)
	}
	if ok, err := st.LibraryHealthApplies(ctx, "library-local", HealthScrapeFailure, all); err != nil || ok {
		t.Errorf("LibraryHealthApplies(scrape-failure) = %v, %v, want false", ok, err)
	}
	if ok, err := st.LibraryHealthApplies(ctx, "library-local", HealthNoScraper, all); err != nil || !ok {
		t.Errorf("LibraryHealthApplies(no-scraper) = %v, %v, want true", ok, err)
	}
	var stale int
	if err := st.catalogR.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM library_health_cache WHERE library_id = 'library-local' AND dimension = 'unmatched'`).Scan(&stale); err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if stale != 0 {
		t.Errorf("stale unmatched cache row survived refresh")
	}
}

// TestLibraryHealthMusicLikeLibrary: a plugin with a searchable scraper, no
// automatic identification, local-tag hydration and square art. Tagged Media
// are identified, scrape failures do not apply, and stored thumb art counts.
//
// feature:library-health
func TestLibraryHealthMusicLikeLibrary(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	setHealthLibraryPlugin(t, st, "library-local", "tags-plugin")
	stored := map[MediaID]map[string]bool{}
	st.SetLibraryHealthProfiler(func(pluginID string) LibraryHealthProfile {
		if pluginID != "tags-plugin" {
			return LibraryHealthProfile{}
		}
		return LibraryHealthProfile{
			Plugin:              true,
			Searchable:          true,
			LocalIdentityField:  "tags.hydrated",
			ClassField:          "class",
			ArtworkSlots:        map[string][]string{"audio": {"poster", "thumb"}},
			DefaultArtworkSlots: []string{"poster", "thumb"},
		}
	})
	st.SetLibraryHealthArtworkProbe(func(id MediaID, slot string) bool { return stored[id][slot] })

	before := healthCountsFor(t, st, "library-local")
	tagged := seedHealthMedia(t, st, "library-local", "tagged", "/m/tagged.flac", "2222000000000001")
	withID := seedHealthMedia(t, st, "library-local", "with-id", "/m/with-id.flac", "2222000000000002")
	untagged := seedHealthMedia(t, st, "library-local", "untagged", "/m/untagged.flac", "2222000000000003")
	for _, id := range []MediaID{tagged, withID, untagged} {
		setHealthMediaClass(t, st, id, "audio")
	}
	if err := st.UpsertMediaFields(ctx, tagged, []MediaField{{Name: "tags.hydrated", Type: EntityFieldFlag, Flag: true}}); err != nil {
		t.Fatalf("UpsertMediaFields: %v", err)
	}
	stored[tagged] = map[string]bool{"thumb": true}
	if err := st.AddMediaExternalID(ctx, withID, "probe-id", "abc-123"); err != nil {
		t.Fatalf("AddMediaExternalID: %v", err)
	}
	applyHealthArtwork(t, st, withID, "poster")

	counts := healthCountsFor(t, st, "library-local")
	for _, dim := range []HealthDimension{HealthScrapeFailure, HealthNoScraper} {
		if n, ok := counts[dim]; ok {
			t.Errorf("%s = %d reported for a plugin without auto-identification", dim, n)
		}
	}
	if got := counts[HealthUnmatched] - before[HealthUnmatched]; got != 1 {
		t.Errorf("unmatched delta = %d, want 1 (only the untagged item)", got)
	}
	if got := counts[HealthMissingArtwork] - before[HealthMissingArtwork]; got != 1 {
		t.Errorf("missing-artwork delta = %d, want 1 (only the untagged item)", got)
	}
	unmatched := healthItemSet(t, st, "library-local", HealthUnmatched)
	if !unmatched[untagged] || unmatched[tagged] || unmatched[withID] {
		t.Errorf("unmatched items = %v, want untagged only among the fixture", unmatched)
	}
	missing := healthItemSet(t, st, "library-local", HealthMissingArtwork)
	if !missing[untagged] || missing[tagged] || missing[withID] {
		t.Errorf("missing-artwork items = %v, want untagged only among the fixture", missing)
	}
}

// TestLibraryHealthEpisodeLikeLibrary: an auto-identifying plugin whose
// archetypes take different artwork slots. An episode-like item with a thumb
// has artwork; a movie-like item with only a thumb does not. Only attempted
// scrapes count as scrape failures.
//
// feature:library-health
func TestLibraryHealthEpisodeLikeLibrary(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	setHealthLibraryPlugin(t, st, "library-local", "episodic-plugin")
	st.SetLibraryHealthProfiler(func(string) LibraryHealthProfile {
		return LibraryHealthProfile{
			Plugin:       true,
			Searchable:   true,
			AutoIdentify: true,
			ClassField:   "class",
			ArtworkSlots: map[string][]string{
				"episode": {"poster", "thumb"},
				"movie":   {"poster", "backdrop"},
			},
			DefaultArtworkSlots: []string{"poster", "backdrop", "thumb"},
		}
	})

	before := healthCountsFor(t, st, "library-local")
	setClass := func(id MediaID, class string) {
		if err := st.UpsertMediaFields(ctx, id, []MediaField{{Name: "class", Type: EntityFieldText, Text: class}}); err != nil {
			t.Fatalf("UpsertMediaFields: %v", err)
		}
	}
	episode := seedHealthMedia(t, st, "library-local", "episode", "/m/episode.mkv", "3333000000000001")
	identifyHealthMedia(t, st, episode)
	setClass(episode, "episode")
	applyHealthArtwork(t, st, episode, "thumb")

	movie := seedHealthMedia(t, st, "library-local", "movie", "/m/movie.mkv", "3333000000000002")
	identifyHealthMedia(t, st, movie)
	setClass(movie, "movie")
	applyHealthArtwork(t, st, movie, "thumb")

	failed := seedHealthMedia(t, st, "library-local", "failed", "/m/failed.mkv", "3333000000000003")
	if err := st.QueueUnmatched(ctx, UnmatchedItem{MediaID: failed, PluginID: "episodic-plugin", ScraperID: "probe-scraper"}); err != nil {
		t.Fatalf("QueueUnmatched: %v", err)
	}
	review := seedHealthMedia(t, st, "library-local", "review", "/m/review.mkv", "3333000000000004")
	if err := st.QueueUnmatched(ctx, UnmatchedItem{MediaID: review, PluginID: "episodic-plugin", ScraperID: UnmatchedManualScraperID}); err != nil {
		t.Fatalf("QueueUnmatched manual: %v", err)
	}

	counts := healthCountsFor(t, st, "library-local")
	if _, ok := counts[HealthNoScraper]; ok {
		t.Errorf("no-scraper reported for a searchable plugin: %v", counts)
	}
	if got := counts[HealthUnmatched] - before[HealthUnmatched]; got != 2 {
		t.Errorf("unmatched delta = %d, want 2 (failed, review)", got)
	}
	if got := counts[HealthScrapeFailure] - before[HealthScrapeFailure]; got != 1 {
		t.Errorf("scrape-failure delta = %d, want 1 (failed only)", got)
	}
	missing := healthItemSet(t, st, "library-local", HealthMissingArtwork)
	if missing[episode] {
		t.Error("episode-like item with a thumb counted as missing artwork")
	}
	if !missing[movie] {
		t.Error("movie-like item with only a thumb not counted as missing artwork")
	}
	failures := healthItemSet(t, st, "library-local", HealthScrapeFailure)
	if !failures[failed] || failures[review] {
		t.Errorf("scrape-failure items = %v, want failed and not review", failures)
	}
}

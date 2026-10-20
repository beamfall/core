package store

import (
	"context"
	"testing"
	"time"
)

// seedHealthMedia imports a Media file into a Library for the health fixtures.
func seedHealthMedia(t *testing.T, st *Store, libraryID LibraryID, id, path, oshash string) MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:           libraryID,
		Path:                path,
		Title:               id,
		Container:           "mp4",
		Codec:               "h264",
		ContentFingerprint:  "sha256:" + id,
		DurationSecondsHint: 100,
		OSHash:              oshash,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile(%s): %v", id, err)
	}
	return media.ID
}

// TestLibraryHealthFiveDimensions is the server vertical slice for the Library
// Health Dashboard: the five generic dimensions count and list the
// right Media, the aggregate is the per-Library fold, and the bulk operations
// route through the audited Trash path so nothing is hard-deleted.
//
// feature:library-health
// feature:unmatched-queue
// feature:dedupe-review
func TestLibraryHealthFiveDimensions(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	// Baseline: the development seed already places one Media in library-local
	// (media-alpha, unmatched + fileless). Assert against deltas so the test is
	// robust to seed changes — the dimension logic, not the seed, is under test.
	baseCounts, err := st.LibraryHealthCounts(ctx, all)
	if err != nil {
		t.Fatalf("baseline LibraryHealthCounts: %v", err)
	}
	base := map[HealthDimension]int{}
	for _, c := range baseCounts {
		if c.LibraryID == "library-local" {
			base = c.Counts
		}
	}

	// Matched, healthy item: identified title + applied poster + a unique hash.
	matched := seedHealthMedia(t, st, "library-local", "matched", "/m/matched.mp4", "aaaa000000000001")
	title := "Matched Title"
	if err := st.UpdateMediaMetadata(ctx, matched, MediaMetadata{Title: &title}); err != nil {
		t.Fatalf("UpdateMediaMetadata: %v", err)
	}
	if err := st.UpsertArtworkCandidate(ctx, matched, "poster", "https://art/p.jpg", "", ""); err != nil {
		t.Fatalf("UpsertArtworkCandidate: %v", err)
	}
	cands, err := st.ArtworkCandidates(ctx, matched, "poster", all)
	if err != nil || len(cands) != 1 {
		t.Fatalf("ArtworkCandidates = %v, err %v", cands, err)
	}
	if err := st.MarkArtworkCandidateApplied(ctx, cands[0].ID); err != nil {
		t.Fatalf("MarkArtworkCandidateApplied: %v", err)
	}

	// Unmatched item (no identified title, no external id, no artwork) — also a
	// scrape-failure and missing-artwork by construction.
	unmatched := seedHealthMedia(t, st, "library-local", "unmatched", "/m/unmatched.mp4", "bbbb000000000002")

	// A duplicate pair: two distinct Media sharing one non-empty oshash.
	dupA := seedHealthMedia(t, st, "library-local", "dupa", "/m/dupa.mp4", "cccc000000000003")
	dupB := seedHealthMedia(t, st, "library-local", "dupb", "/m/dupb.mp4", "cccc000000000003")

	// A broken item: mark its only file missing.
	broken := seedHealthMedia(t, st, "library-local", "broken", "/m/broken.mp4", "dddd000000000004")
	if _, err := st.catalogW.ExecContext(ctx,
		`UPDATE media_file SET missing_at_ms = 1 WHERE media_id = ?`, broken); err != nil {
		t.Fatalf("mark broken: %v", err)
	}

	counts, err := st.LibraryHealthCounts(ctx, all)
	if err != nil {
		t.Fatalf("LibraryHealthCounts: %v", err)
	}
	var local *LibraryHealth
	for i := range counts {
		if counts[i].LibraryID == "library-local" {
			local = &counts[i]
		}
	}
	if local == nil {
		t.Fatal("library-local missing from counts")
	}

	delta := func(dim HealthDimension) int { return local.Counts[dim] - base[dim] }
	// unmatched delta: unmatched + dupA + dupB + broken (matched is identified). +4.
	if got := delta(HealthUnmatched); got != 4 {
		t.Errorf("unmatched count delta = %d, want 4", got)
	}
	// broken delta: only the broken item (its sole file is missing). +1.
	if got := delta(HealthBroken); got != 1 {
		t.Errorf("broken count delta = %d, want 1", got)
	}
	// scrape-failure delta: unmatched-and-no-external-id — same +4 as unmatched
	// here (none have external IDs).
	if got := delta(HealthScrapeFailure); got != 4 {
		t.Errorf("scrape-failure count delta = %d, want 4", got)
	}
	// missing-artwork delta: everything new except matched (applied poster). +4.
	if got := delta(HealthMissingArtwork); got != 4 {
		t.Errorf("missing-artwork count delta = %d, want 4", got)
	}
	// duplicate delta: dupA + dupB share an oshash. broken's hash is
	// missing-filtered; matched's is unique. +2.
	if got := delta(HealthDuplicate); got != 2 {
		t.Errorf("duplicate count delta = %d, want 2", got)
	}

	// Drill-down list for unmatched contains exactly the unmatched set.
	items, err := st.LibraryHealthItems(ctx, "library-local", HealthUnmatched, all, 0)
	if err != nil {
		t.Fatalf("LibraryHealthItems: %v", err)
	}
	got := map[MediaID]bool{}
	for _, it := range items {
		got[it.MediaID] = true
	}
	for _, want := range []MediaID{unmatched, dupA, dupB, broken} {
		if !got[want] {
			t.Errorf("unmatched list missing %s", want)
		}
	}
	if got[matched] {
		t.Errorf("unmatched list wrongly includes matched")
	}

	// An unknown dimension is rejected (closed vocabulary).
	if _, err := st.LibraryHealthItems(ctx, "library-local", HealthDimension("performers"), all, 0); err == nil {
		t.Error("expected error for unknown dimension")
	}

	// Duplicate groups: one group {dupA, dupB}, survivor first (lowest ID).
	groups, err := st.HealthDuplicateGroups(ctx, "library-local", all)
	if err != nil {
		t.Fatalf("HealthDuplicateGroups: %v", err)
	}
	if len(groups) != 1 || len(groups[0]) != 2 {
		t.Fatalf("groups = %v, want one pair", groups)
	}

	// Bulk-merge: keep the survivor, trash the rest. Routes through Trash —
	// the trashed copy is restorable, never hard-deleted.
	survivor := groups[0][0].MediaID
	keep := map[MediaID]bool{survivor: true}
	trashed, err := st.HealthBulkTrash(ctx, "library-local", HealthDuplicate, all, keep)
	if err != nil {
		t.Fatalf("HealthBulkTrash (merge): %v", err)
	}
	if trashed != 1 {
		t.Fatalf("bulk-merge trashed %d, want 1", trashed)
	}
	// The trashed duplicate is in the audited Trash and restorable.
	trash, err := st.TrashedMediaList(ctx, all)
	if err != nil {
		t.Fatalf("TrashedMediaList: %v", err)
	}
	found := false
	for _, e := range trash {
		if e.ID != survivor && (e.ID == dupA || e.ID == dupB) {
			found = true
		}
	}
	if !found {
		t.Error("merged duplicate not found in trash (must be recoverable, invariant #6)")
	}
	// After the merge the duplicate dimension is empty (survivor is now alone).
	groups, err = st.HealthDuplicateGroups(ctx, "library-local", all)
	if err != nil {
		t.Fatalf("HealthDuplicateGroups after merge: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("groups after merge = %v, want none", groups)
	}

	// Bulk-trash-broken trashes the broken set, sparing the pre-seeded broken
	// item (media-alpha) so the assertion isolates our fixture's contribution.
	baseBroken, err := st.LibraryHealthItems(ctx, "library-local", HealthBroken, all, 0)
	if err != nil {
		t.Fatalf("baseline broken items: %v", err)
	}
	keepBroken := map[MediaID]bool{}
	for _, it := range baseBroken {
		if it.MediaID != broken {
			keepBroken[it.MediaID] = true
		}
	}
	trashed, err = st.HealthBulkTrash(ctx, "library-local", HealthBroken, all, keepBroken)
	if err != nil {
		t.Fatalf("HealthBulkTrash (broken): %v", err)
	}
	if trashed != 1 {
		t.Fatalf("bulk-trash-broken trashed %d, want 1", trashed)
	}
}

func TestHealthDuplicateGroupsDeduplicatesMultipartMedia(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	hash := "eeee000000000005"

	multipart, _, err := st.ImportMediaFileParts(ctx, []ImportMediaFilePart{
		{
			SourceOrdinal: 1,
			File: ImportMediaFile{
				LibraryID:           "library-local",
				Path:                "/m/multipart-cd1.mp4",
				Title:               "multipart",
				Container:           "mp4",
				Codec:               "h264",
				ContentFingerprint:  "sha256:multipart-cd1",
				DurationSecondsHint: 100,
				OSHash:              hash,
			},
		},
		{
			SourceOrdinal: 2,
			File: ImportMediaFile{
				LibraryID:           "library-local",
				Path:                "/m/multipart-cd2.mp4",
				Title:               "multipart",
				Container:           "mp4",
				Codec:               "h264",
				ContentFingerprint:  "sha256:multipart-cd2",
				DurationSecondsHint: 100,
				OSHash:              hash,
			},
		},
	})
	if err != nil {
		t.Fatalf("ImportMediaFileParts: %v", err)
	}
	single := seedHealthMedia(t, st, "library-local", "single-duplicate", "/m/single-duplicate.mp4", hash)

	groups, err := st.HealthDuplicateGroups(ctx, "library-local", all)
	if err != nil {
		t.Fatalf("HealthDuplicateGroups: %v", err)
	}
	if len(groups) != 1 || len(groups[0]) != 2 {
		t.Fatalf("groups = %#v, want one two-Media duplicate group", groups)
	}
	seen := map[MediaID]int{}
	for _, item := range groups[0] {
		seen[item.MediaID]++
	}
	if seen[multipart.ID] != 1 || seen[single] != 1 {
		t.Fatalf("groups = %#v, want multipart %s and single %s exactly once", groups, multipart.ID, single)
	}
}

// TestLibraryHealthBrokenIncludesDecodeBroken is the store-level proof that the
// decode-verify signal folds into the existing `broken` dimension: a
// Media whose file is present on disk (missing_at_ms IS NULL) but flagged
// decode_broken = 1 counts and lists as broken, exactly like a file-gone item,
// and clearing the flag removes it.
func TestLibraryHealthBrokenIncludesDecodeBroken(t *testing.T) {
	// feature:decode-verify feature:library-health
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	baseCounts, err := st.LibraryHealthCounts(ctx, all)
	if err != nil {
		t.Fatalf("baseline LibraryHealthCounts: %v", err)
	}
	base := 0
	for _, c := range baseCounts {
		if c.LibraryID == "library-local" {
			base = c.Counts[HealthBroken]
		}
	}

	// A Media whose only file is present (not missing) but fails decode-verify.
	id := seedHealthMedia(t, st, "library-local", "decode-bad", "/m/decode-bad.mp4", "eeee000000000005")
	ok, err := st.SetMediaFileDecodeBroken(ctx, id, true, time.UnixMilli(1))
	if err != nil {
		t.Fatalf("SetMediaFileDecodeBroken(true): %v", err)
	}
	if !ok {
		t.Fatal("SetMediaFileDecodeBroken reported no file updated, want the present file")
	}

	brokenNow := func() int {
		counts, err := st.LibraryHealthCounts(ctx, all)
		if err != nil {
			t.Fatalf("LibraryHealthCounts: %v", err)
		}
		for _, c := range counts {
			if c.LibraryID == "library-local" {
				return c.Counts[HealthBroken]
			}
		}
		t.Fatal("library-local missing from counts")
		return 0
	}

	if got := brokenNow() - base; got != 1 {
		t.Fatalf("broken delta after decode-flag = %d, want 1", got)
	}
	items, err := st.LibraryHealthItems(ctx, "library-local", HealthBroken, all, 0)
	if err != nil {
		t.Fatalf("LibraryHealthItems: %v", err)
	}
	found := false
	for _, it := range items {
		if it.MediaID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("decode-broken item %s not in broken drill-down: %#v", id, items)
	}

	// Clearing the flag (a later clean decode) removes it from the dimension.
	if _, err := st.SetMediaFileDecodeBroken(ctx, id, false, time.UnixMilli(2)); err != nil {
		t.Fatalf("SetMediaFileDecodeBroken(false): %v", err)
	}
	if got := brokenNow() - base; got != 0 {
		t.Fatalf("broken delta after clear = %d, want 0", got)
	}
}

// TestLibraryHealthRevealFiltered confirms a restricted Library's health is
// invisible to an unrevealed caller — the dashboard obeys the Reveal contract
// like every other read path, and never becomes an existence oracle.
func TestLibraryHealthRevealFiltered(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// Seed an unmatched item into the restricted Archive.
	seedHealthMedia(t, st, "library-archive", "secret", "/m/secret.mp4", "")

	unrevealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: false}
	counts, err := st.LibraryHealthCounts(ctx, unrevealed)
	if err != nil {
		t.Fatalf("LibraryHealthCounts: %v", err)
	}
	for _, c := range counts {
		if c.LibraryID == "library-archive" {
			t.Fatal("restricted Library leaked into unrevealed health counts")
		}
	}
	// A drill-down on the hidden Library yields an empty list, not an error.
	items, err := st.LibraryHealthItems(ctx, "library-archive", HealthUnmatched, unrevealed, 0)
	if err != nil {
		t.Fatalf("LibraryHealthItems hidden: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("hidden-library drill-down = %v, want empty", items)
	}

	// Revealed, the Library and its health become visible.
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}
	counts, err = st.LibraryHealthCounts(ctx, revealed)
	if err != nil {
		t.Fatalf("LibraryHealthCounts revealed: %v", err)
	}
	seen := false
	for _, c := range counts {
		if c.LibraryID == "library-archive" {
			seen = true
			if c.Counts[HealthUnmatched] < 1 {
				t.Errorf("revealed archive unmatched = %d, want >=1", c.Counts[HealthUnmatched])
			}
		}
	}
	if !seen {
		t.Error("revealed archive missing from health counts")
	}
}

// TestLibraryHealthCacheRefreshAndRead verifies that RefreshLibraryHealthCache
// pre-computes counts that LibraryHealthCounts subsequently serves from the
// cache, and that the cached values match what the live COUNT queries return.
//
// feature:library-health
func TestLibraryHealthCacheRefreshAndRead(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	// Seed two unmatched items into library-local so the dimension is non-zero.
	seedHealthMedia(t, st, "library-local", "cache-a", "/m/cache-a.mp4", "eeee000000000001")
	seedHealthMedia(t, st, "library-local", "cache-b", "/m/cache-b.mp4", "ffff000000000002")

	// Capture live counts before the cache is populated — these are ground truth.
	live, err := st.LibraryHealthCounts(ctx, all)
	if err != nil {
		t.Fatalf("live LibraryHealthCounts: %v", err)
	}
	var liveCounts map[HealthDimension]int
	for _, c := range live {
		if c.LibraryID == "library-local" {
			liveCounts = c.Counts
		}
	}
	if liveCounts == nil {
		t.Fatal("library-local missing from live counts")
	}

	// Refresh the cache and confirm no error.
	now := time.Now().UTC()
	if err := st.RefreshLibraryHealthCache(ctx, now); err != nil {
		t.Fatalf("RefreshLibraryHealthCache: %v", err)
	}

	// LibraryHealthCounts now reads from the cache. Counts must match live.
	cached, err := st.LibraryHealthCounts(ctx, all)
	if err != nil {
		t.Fatalf("cached LibraryHealthCounts: %v", err)
	}
	var cachedCounts map[HealthDimension]int
	for _, c := range cached {
		if c.LibraryID == "library-local" {
			cachedCounts = c.Counts
		}
	}
	if cachedCounts == nil {
		t.Fatal("library-local missing from cached counts")
	}
	for _, dim := range HealthDimensions {
		if cachedCounts[dim] != liveCounts[dim] {
			t.Errorf("dimension %s: cached %d != live %d", dim, cachedCounts[dim], liveCounts[dim])
		}
	}

	// A second refresh with updated data reflects the new state. Trash one of
	// the seeded items, refresh again, verify the cached broken count increases.
	if _, err := st.catalogW.ExecContext(ctx,
		`UPDATE media_file SET missing_at_ms = 1 WHERE media_id IN (
			SELECT id FROM media WHERE title = 'cache-a'
		)`); err != nil {
		t.Fatalf("mark cache-a broken: %v", err)
	}
	if err := st.RefreshLibraryHealthCache(ctx, now.Add(time.Second)); err != nil {
		t.Fatalf("second RefreshLibraryHealthCache: %v", err)
	}
	recached, err := st.LibraryHealthCounts(ctx, all)
	if err != nil {
		t.Fatalf("recached LibraryHealthCounts: %v", err)
	}
	var recachedBroken int
	for _, c := range recached {
		if c.LibraryID == "library-local" {
			recachedBroken = c.Counts[HealthBroken]
		}
	}
	if recachedBroken <= liveCounts[HealthBroken] {
		t.Errorf("expected broken count to increase after marking file missing; got %d, was %d", recachedBroken, liveCounts[HealthBroken])
	}
}

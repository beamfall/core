package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// splitTitle imports two unlabelled copies of one title as separate Media, as
// a scan did before it labelled Versions (CORE-2473), and returns the earlier
// (survivor) and later (merged) Media.
func splitTitle(t *testing.T, st *Store) (MediaID, MediaID, string, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	remux := filepath.Join(root, "Disclosure.Day.2026.REPACK.1080p.Remux.mkv")
	web := filepath.Join(root, "Disclosure.Day.2026.2160p.WEB-DL.mkv")
	first, _, err := st.ImportMediaFile(ctx, framedImport("library-local", remux, 1920, 800))
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := st.ImportMediaFile(ctx, framedImport("library-local", web, 3840, 1600))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("the two copies share a Media; the test needs a split title")
	}
	return first.ID, second.ID, filepath.Base(remux), filepath.Base(web)
}

// queuedMedia lists profile-default's queue in order.
func queuedMedia(t *testing.T, st *Store) []MediaID {
	t.Helper()
	rows, err := st.myQueueRows(context.Background(), "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]MediaID, len(rows))
	for i, row := range rows {
		ids[i] = row.MediaID
	}
	return ids
}

// TestMergeSplitVersionsKeepsUserStateAndUndoRestoresTheSplit: a rescan's
// merge folds the later copy into the earlier Media under derived labels,
// copies its user state without overwriting the survivor's, trashes it, and
// an undo puts the split back.
func TestMergeSplitVersionsKeepsUserStateAndUndoRestoresTheSplit(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	keep, drop, remux, web := splitTitle(t, st)
	now := time.Now().UTC()
	ms := timeToMillis(now)

	video := ResumePolicy{Class: MediaClassVideo}
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: drop, PositionSeconds: 600, DurationSeconds: 6000,
	}, false, video); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO item_rating(profile_id, target_kind, target_id, rating, rated_at_ms) VALUES ('profile-default', 'media', ?, 4, ?)`, []any{drop, ms}},
		{`INSERT INTO item_rating(profile_id, target_kind, target_id, rating, rated_at_ms) VALUES ('profile-default', 'media', ?, 9, ?)`, []any{keep, ms}},
		{`INSERT INTO item_rating(profile_id, target_kind, target_id, rating, rated_at_ms) VALUES ('profile-other', 'media', ?, 7, ?)`, []any{drop, ms}},
		{`INSERT INTO media_favorite(profile_id, media_id, favorited_at_ms) VALUES ('profile-default', ?, ?)`, []any{drop, ms}},
		{`INSERT INTO watchlist_item(profile_id, target_kind, target_id, added_at_ms) VALUES ('profile-default', 'media', ?, ?)`, []any{drop, ms}},
	} {
		if _, err := st.eventsW.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	insertRawQueueRow(t, st, "profile-default", 0, "media-alpha")
	insertRawQueueRow(t, st, "profile-default", 1, drop)
	for _, stmt := range []string{
		`INSERT INTO collection(id, library_id, name, created_at_ms, updated_at_ms) VALUES ('col-a', 'library-local', 'Picks', 1, 1)`,
		`INSERT INTO collection_media(collection_id, media_id, sort_order, added_at_ms) VALUES ('col-a', '` + string(drop) + `', 3, 1)`,
		`INSERT INTO tag(id, name, created_at_ms, updated_at_ms) VALUES ('tag-a', 'Favourite cut', 1, 1)`,
		`INSERT INTO media_tag(media_id, tag_id) VALUES ('` + string(drop) + `', 'tag-a')`,
	} {
		if _, err := st.catalogW.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	result, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{drop, keep}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.BatchID == "" || result.Survivor != keep || len(result.Merged) != 1 || result.Merged[0] != drop {
		t.Fatalf("merge = %+v, want %s folded into %s", result, drop, keep)
	}
	if got, want := versionLabels(t, st, keep), map[string]string{remux: "1080p", web: "2160p"}; !equalLabels(got, want) {
		t.Fatalf("survivor labels = %v, want %v", got, want)
	}
	if _, err := st.Media(ctx, drop, filter); err == nil {
		t.Fatal("the merged Media is still live")
	}
	var width, height int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT video_width, video_height FROM media WHERE id = ?`, keep).Scan(&width, &height); err != nil {
		t.Fatal(err)
	}
	if width != 1920 || height != 800 {
		t.Fatalf("survivor frame = %dx%d, want its first Version's 1920x800", width, height)
	}

	progress, ok, err := st.PlaybackProgressFor(ctx, "profile-default", keep)
	if err != nil || !ok || progress.PositionSeconds != 600 {
		t.Fatalf("survivor progress = %+v ok=%v err=%v, want the merged copy's 600s", progress, ok, err)
	}
	ratings := map[string]int{}
	rows, err := st.eventsR.QueryContext(ctx, `SELECT profile_id, rating FROM item_rating WHERE target_kind = 'media' AND target_id = ?`, keep)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var profile string
		var rating int
		if err := rows.Scan(&profile, &rating); err != nil {
			t.Fatal(err)
		}
		ratings[profile] = rating
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if ratings["profile-default"] != 9 || ratings["profile-other"] != 7 {
		t.Fatalf("survivor ratings = %v, want its own 9 kept and the other Profile's 7 copied", ratings)
	}
	for name, query := range map[string]string{
		"favourite": `SELECT COUNT(*) FROM media_favorite WHERE media_id = ?`,
		"watchlist": `SELECT COUNT(*) FROM watchlist_item WHERE target_kind = 'media' AND target_id = ?`,
	} {
		var n int
		if err := st.eventsR.QueryRowContext(ctx, query, keep).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("survivor %s rows = %d, want 1", name, n)
		}
	}
	for name, query := range map[string]string{
		"collection": `SELECT COUNT(*) FROM collection_media WHERE media_id = ? AND sort_order = 3`,
		"tag":        `SELECT COUNT(*) FROM media_tag WHERE media_id = ?`,
	} {
		var n int
		if err := st.catalogR.QueryRowContext(ctx, query, keep).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("survivor %s rows = %d, want 1", name, n)
		}
	}
	if got := queuedMedia(t, st); len(got) != 2 || got[1] != keep {
		t.Fatalf("queue after merge = %v, want the survivor in the merged copy's place", got)
	}
	// The copied progress rides the engagement log, so a rebuild keeps it.
	if err := st.rebuildEngagementProjections(ctx); err != nil {
		t.Fatalf("projection rebuild after merge: %v", err)
	}
	if progress, ok, err := st.PlaybackProgressFor(ctx, "profile-default", keep); err != nil || !ok || progress.PositionSeconds != 600 {
		t.Fatalf("rebuilt survivor progress = %+v ok=%v err=%v", progress, ok, err)
	}
	events := mediaOutboxEvents(t, st)
	if got := countMediaEvents(events, MediaEventDeleted, drop); got != 1 {
		t.Fatalf("merged Media media.deleted = %d, want 1", got)
	}
	if got := countMediaEvents(events, MediaEventUpdated, keep); got != 1 {
		t.Fatalf("survivor media.updated = %d, want 1", got)
	}

	createdBefore := countMediaEvents(events, MediaEventCreated, drop)
	if _, err := st.UndoBatch(ctx, result.BatchID, filter, now.Add(time.Minute)); err != nil {
		t.Fatalf("UndoBatch: %v", err)
	}
	if got, want := versionLabels(t, st, keep), map[string]string{remux: ""}; !equalLabels(got, want) {
		t.Fatalf("survivor labels after undo = %v, want %v", got, want)
	}
	if got, want := versionLabels(t, st, drop), map[string]string{web: ""}; !equalLabels(got, want) {
		t.Fatalf("restored Media labels after undo = %v, want %v", got, want)
	}
	if _, err := st.Media(ctx, drop, filter); err != nil {
		t.Fatalf("restored Media not live: %v", err)
	}
	if got := queuedMedia(t, st); len(got) != 2 || got[1] != drop {
		t.Fatalf("queue after undo = %v, want the restored Media back in its place", got)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM collection_media WHERE media_id = ?`,
		`SELECT COUNT(*) FROM media_tag WHERE media_id = ?`,
	} {
		var n int
		if err := st.catalogR.QueryRowContext(ctx, query, keep).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%q on survivor after undo = %d, want 0", query, n)
		}
	}
	if got := countMediaEvents(mediaOutboxEvents(t, st), MediaEventCreated, drop) - createdBefore; got != 1 {
		t.Fatalf("undo media.created for the restored Media = %d, want 1", got)
	}

	// A rescan after the undo leaves the split the user chose.
	again, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{keep, drop}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if again.BatchID != "" || len(again.Merged) != 0 {
		t.Fatalf("merge after undo = %+v, want nothing", again)
	}
}

// TestMergeSplitVersionsSkipsAnEncryptedLibrary: an encrypted Library's
// catalog lives on its own plane, so the merge leaves it alone.
func TestMergeSplitVersionsSkipsAnEncryptedLibrary(t *testing.T) {
	st, first := seededEncryptedMedia(t, "Disclosure Day")
	defer st.Close()
	ctx := context.Background()
	imported := encryptedImport("library-local", "sha256:Disclosure Day 2160p", "/enc/Disclosure Day 2160p.mkv")
	imported.Title = "Disclosure Day"
	second, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatal(err)
	}
	result, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{first.ID, second.ID}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.BatchID != "" || len(result.Merged) != 0 {
		t.Fatalf("merge in an encrypted Library = %+v, want nothing", result)
	}
	var batches int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM op_batch WHERE kind = ?`, opKindMergeVersion).Scan(&batches); err != nil {
		t.Fatal(err)
	}
	if batches != 0 {
		t.Fatalf("version merge batches = %d, want 0", batches)
	}
}

// mediaFrame reads a Media row's frame size.
func mediaFrame(t *testing.T, st *Store, id MediaID) (int, int) {
	t.Helper()
	var width, height int
	if err := st.catalogR.QueryRowContext(context.Background(), `SELECT video_width, video_height FROM media WHERE id = ?`, id).Scan(&width, &height); err != nil {
		t.Fatal(err)
	}
	return width, height
}

// TestUndoVersionMergeRefusesAMissingMergedFile: once the merged file is
// missing (or replaced in place), undo would restore a Media with no live
// file, so it refuses and leaves the batch undoable.
func TestUndoVersionMergeRefusesAMissingMergedFile(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	keep, drop, _, web := splitTitle(t, st)
	now := time.Now().UTC()
	result, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{keep, drop}, now)
	if err != nil || len(result.Merged) != 1 {
		t.Fatalf("merge = %+v err=%v", result, err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media_file SET missing_at_ms = 1 WHERE path LIKE ?`, "%"+web); err != nil {
		t.Fatal(err)
	}
	var verr ValidationError
	if _, err := st.UndoBatch(ctx, result.BatchID, filter, now.Add(time.Minute)); !errors.As(err, &verr) {
		t.Fatalf("UndoBatch with the merged file missing: err = %v, want a validation error", err)
	}
	var undone int64
	if err := st.catalogR.QueryRowContext(ctx, `SELECT undone_at_ms FROM op_batch WHERE id = ?`, result.BatchID).Scan(&undone); err != nil {
		t.Fatal(err)
	}
	if undone != 0 {
		t.Fatal("a refused undo marked the batch undone")
	}
	if _, err := st.Media(ctx, drop, filter); err == nil {
		t.Fatal("a refused undo restored the merged Media")
	}
}

// TestUndoVersionMergesOutOfOrderRestoresTheFirstVersionFacts: two merges
// into one survivor undone first-to-last leave the survivor with its own
// file's facts, whether the second merged file became first (a stale
// snapshot must not return) or not (restored facts must not be cleared).
func TestUndoVersionMergesOutOfOrderRestoresTheFirstVersionFacts(t *testing.T) {
	type copyFile struct {
		name          string
		width, height int
	}
	for _, tc := range []struct {
		name          string
		second, third copyFile
	}{
		{"third sorts after the first merged", copyFile{"Disclosure.Day.2026.REPACK.1080p.Remux.mkv", 1920, 800}, copyFile{"Disclosure.Day.2026.720p.HDTV.mkv", 1280, 536}},
		{"third sorts first", copyFile{"Disclosure.Day.2026.1440p.WEB-DL.mkv", 2560, 1072}, copyFile{"Disclosure.Day.2026.REPACK.1080p.Remux.mkv", 1920, 800}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openSeeded(t)
			ctx := context.Background()
			filter := LibraryFilter{Revealed: true, AllLibraries: true}
			root := t.TempDir()
			var ids []MediaID
			for _, f := range []copyFile{{"Disclosure.Day.2026.2160p.WEB-DL.mkv", 3840, 1600}, tc.second, tc.third} {
				media, _, err := st.ImportMediaFile(ctx, framedImport("library-local", filepath.Join(root, f.name), f.width, f.height))
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, media.ID)
			}
			if ids[0] == ids[1] || ids[1] == ids[2] {
				t.Fatal("the copies share a Media; the test needs a split title")
			}
			now := time.Now().UTC()
			first, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{ids[0], ids[1]}, now)
			if err != nil || first.Survivor != ids[0] || len(first.Merged) != 1 {
				t.Fatalf("first merge = %+v err=%v", first, err)
			}
			second, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{ids[0], ids[2]}, now)
			if err != nil || second.Survivor != ids[0] || len(second.Merged) != 1 {
				t.Fatalf("second merge = %+v err=%v", second, err)
			}
			for _, batch := range []string{first.BatchID, second.BatchID} {
				if _, err := st.UndoBatch(ctx, batch, filter, now.Add(time.Minute)); err != nil {
					t.Fatalf("UndoBatch %s: %v", batch, err)
				}
			}
			if width, height := mediaFrame(t, st, ids[0]); width != 3840 || height != 1600 {
				t.Fatalf("survivor frame after both undos = %dx%d, want its own 3840x1600", width, height)
			}
		})
	}
}

// A Version merge repoints each occurrence and undo follows the surviving entry
// IDs, even when several entries were added at the same time.
func TestUndoVersionMergeLeavesARequeuedSurvivorEntry(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	const profile = "profile-default"
	keep, drop, _, _ := splitTitle(t, st)
	read := func() []PlayQueueEntry {
		t.Helper()
		queue, err := st.PlayQueue(ctx, profile, filter)
		if err != nil {
			t.Fatal(err)
		}
		return queue.Entries
	}
	if err := st.QueueAddMany(ctx, profile, []MediaID{keep, drop, drop}, QueueEnd, filter); err != nil {
		t.Fatal(err)
	}
	original := read()

	now := time.Now().UTC()
	result, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{keep, drop}, now)
	if err != nil || len(result.Merged) != 1 {
		t.Fatalf("merge = %+v err=%v", result, err)
	}
	merged := read()
	if len(merged) != len(original) {
		t.Fatalf("merge changed queue length: %+v", merged)
	}
	for i, entry := range merged {
		if entry.ID != original[i].ID || entry.Item.ID != keep {
			t.Fatalf("entry %d after merge = %+v", i, entry)
		}
	}
	if err := st.QueueRemoveEntry(ctx, profile, original[1].ID, filter); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueAddMany(ctx, profile, []MediaID{keep}, QueueEnd, filter); err != nil {
		t.Fatal(err)
	}
	requeued := read()
	if _, err := st.UndoBatch(ctx, result.BatchID, filter, now.Add(time.Minute)); err != nil {
		t.Fatalf("UndoBatch: %v", err)
	}
	undone := read()
	if len(undone) != 3 || undone[0].ID != original[0].ID || undone[0].Item.ID != keep ||
		undone[1].ID != original[2].ID || undone[1].Item.ID != drop ||
		undone[2].ID != requeued[2].ID || undone[2].Item.ID != keep {
		t.Fatalf("queue after undo = %+v", undone)
	}
}

// TestMergeSplitVersionsCountsOnlyLiveFiles: a copy whose file replaced an
// earlier one still holds a single live file, so it merges.
func TestMergeSplitVersionsCountsOnlyLiveFiles(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	keep, drop, _, web := splitTitle(t, st)
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media_file SET missing_at_ms = 1 WHERE path LIKE ?`, "%"+web); err != nil {
		t.Fatal(err)
	}
	replacement := framedImport("library-local", filepath.Join(t.TempDir(), web), 3840, 1600)
	if _, err := st.ImportMediaFileVersion(ctx, MediaVersionImport{MediaID: drop, File: replacement}); err != nil {
		t.Fatal(err)
	}
	result, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{keep, drop}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if result.Survivor != keep || len(result.Merged) != 1 || result.Merged[0] != drop {
		t.Fatalf("merge = %+v, want %s folded into %s", result, drop, keep)
	}
}

// TestMergeSplitVersionsLeavesRelatedMediaApart: explicit edition-of cuts and
// Extras are separate Media on purpose, so a rescan never folds them.
func TestMergeSplitVersionsLeavesRelatedMediaApart(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, st *Store, keep, drop MediaID)
	}{
		{"edition-of cuts", func(t *testing.T, st *Store, keep, drop MediaID) {
			if err := st.LinkMediaRelation(context.Background(), drop, MediaRelationEditionOf, keep); err != nil {
				t.Fatal(err)
			}
		}},
		{"an Extra", func(t *testing.T, st *Store, _, drop MediaID) {
			if _, err := st.catalogW.ExecContext(context.Background(), `UPDATE media SET extra = 1 WHERE id = ?`, drop); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openSeeded(t)
			keep, drop, _, _ := splitTitle(t, st)
			tc.setup(t, st, keep, drop)
			result, err := st.MergeSplitVersions(context.Background(), "library-local", []MediaID{keep, drop}, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Merged) != 0 {
				t.Fatalf("merge = %+v, want %s left apart", result, drop)
			}
		})
	}
}

// TestRestoreMediaRefusesAMergedVersion: the merged Media's file lives on the
// survivor, so Trash Restore refuses it until the merge is undone, after which
// the Media is live again with its file.
func TestRestoreMediaRefusesAMergedVersion(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	keep, drop, _, _ := splitTitle(t, st)
	now := time.Now().UTC()
	result, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{keep, drop}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Merged) != 1 {
		t.Fatalf("merge = %+v, want %s folded", result, drop)
	}
	if err := st.RestoreMedia(ctx, drop, filter); !errors.Is(err, ErrRestoreMergedVersion) {
		t.Fatalf("RestoreMedia = %v, want ErrRestoreMergedVersion", err)
	}
	if _, err := st.UndoBatch(ctx, result.BatchID, filter, now.Add(time.Minute)); err != nil {
		t.Fatalf("UndoBatch: %v", err)
	}
	var trashed sql.NullInt64
	if err := st.catalogR.QueryRowContext(ctx, `SELECT trashed_at_ms FROM media WHERE id = ?`, drop).Scan(&trashed); err != nil {
		t.Fatal(err)
	}
	if trashed.Valid {
		t.Fatalf("%s still trashed after undo", drop)
	}
}

func TestUndoVersionMergeRefusesAHiddenCopy(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	keep, drop, _, _ := splitTitle(t, st)
	now := time.Now().UTC()
	result, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{keep, drop}, now)
	if err != nil {
		t.Fatal(err)
	}
	hidden := LibraryFilter{AllowedIDs: []LibraryID{"library-elsewhere"}, Revealed: true}
	if _, err := st.UndoBatch(ctx, result.BatchID, hidden, now.Add(time.Minute)); err == nil || err.Error() != "batch not found" {
		t.Fatalf("UndoBatch outside scope = %v, want batch not found", err)
	}
	full := LibraryFilter{Revealed: true, AllLibraries: true}
	if err := st.RestoreMedia(ctx, drop, full); !errors.Is(err, ErrRestoreMergedVersion) {
		t.Fatalf("RestoreMedia after refused undo = %v, want ErrRestoreMergedVersion", err)
	}
	if _, err := st.UndoBatch(ctx, result.BatchID, full, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("UndoBatch in scope: %v", err)
	}
	var trashed sql.NullInt64
	if err := st.catalogR.QueryRowContext(ctx, `SELECT trashed_at_ms FROM media WHERE id = ?`, drop).Scan(&trashed); err != nil {
		t.Fatal(err)
	}
	if trashed.Valid {
		t.Fatalf("%s still trashed after undo", drop)
	}
}

func TestUndoOrdinaryMergeRefusesAVersionMergedCopy(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	keep, drop, _, _ := splitTitle(t, st)
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media_file SET oshash = '00000000eeee0001' WHERE media_id IN (?, ?)`, keep, drop); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	dedupe, err := st.MergeApply(ctx, "library-local", keep, filter, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RestoreMedia(ctx, drop, filter); err != nil {
		t.Fatal(err)
	}
	result, err := st.MergeSplitVersions(ctx, "library-local", []MediaID{keep, drop}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Merged) != 1 {
		t.Fatalf("version merge = %+v, want %s folded", result, drop)
	}
	if _, err := st.UndoBatch(ctx, dedupe.ID, filter, now.Add(2*time.Minute)); err == nil || err.Error() != errUndoMergedVersionText {
		t.Fatalf("UndoBatch of the older merge = %v, want %q", err, errUndoMergedVersionText)
	}
	var trashed sql.NullInt64
	if err := st.catalogR.QueryRowContext(ctx, `SELECT trashed_at_ms FROM media WHERE id = ?`, drop).Scan(&trashed); err != nil {
		t.Fatal(err)
	}
	if !trashed.Valid {
		t.Fatalf("%s left the trash without its file", drop)
	}
}

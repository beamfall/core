package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerifiedVersionMergeCommitFailureAndRecovery(t *testing.T) {
	const missingSeq = "missing survivor sequence"
	for _, failure := range []string{"catalog commit", "events commit", "after events commit", "erase after events failure",
		"erase profile after events failure", "erase unrelated Media after events failure", "purge and re-import after events failure",
		"restore backup after events failure", missingSeq} {
		t.Run(failure, func(t *testing.T) {
			ctx := t.Context()
			dir := migratedDataDir(t)
			opts := Options{SeedDevelopmentData: true}
			var backupDir string
			if failure == "restore backup after events failure" {
				backupDir = t.TempDir()
				opts.AllowedRoots = []string{backupDir}
			}
			st, err := Open(ctx, dir, opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			pair := verifiedVersionPair(t, st)
			keep, drop := pair.Sample.MediaID, pair.Legacy.MediaID
			filter := LibraryFilter{AllLibraries: true}
			for _, id := range []MediaID{keep, drop} {
				if err := st.SetMediaFavorite(ctx, "profile-default", id, true, filter); err != nil {
					t.Fatal(err)
				}
			}
			for _, progress := range []PlaybackProgress{
				{ProfileID: "profile-default", MediaID: keep, PositionSeconds: 12, DurationSeconds: 100, PlayCount: 3, EventTimeMS: 1000},
				{ProfileID: "profile-default", MediaID: drop, PositionSeconds: 25, DurationSeconds: 100, PlayCount: 2, EventTimeMS: 2000},
				{ProfileID: "profile-managed", MediaID: keep, PositionSeconds: 7, DurationSeconds: 100, PlayCount: 4, EventTimeMS: 1000},
				{ProfileID: "profile-managed", MediaID: drop, PositionSeconds: 9, DurationSeconds: 100, PlayCount: 3, EventTimeMS: 2000},
			} {
				if backupDir != "" && progress.ProfileID == "profile-managed" {
					continue
				}
				if err := st.UpsertPlaybackProgress(ctx, progress, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
					t.Fatal(err)
				}
			}
			assertProgress := func(count, position int) {
				t.Helper()
				got, found, err := st.PlaybackProgressFor(ctx, "profile-default", keep)
				if err != nil || !found || got.PlayCount != count || got.PositionSeconds != position {
					t.Fatalf("survivor progress = %+v, found = %v, err = %v, want %d plays at %d", got, found, err, count, position)
				}
			}
			assertProgress(3, 12)
			writer := st.catalogW
			trigger := `AFTER UPDATE OF trashed_at_ms ON main.media`
			switch failure {
			case "events commit", "erase after events failure", "erase profile after events failure", "erase unrelated Media after events failure",
				"purge and re-import after events failure", "restore backup after events failure", missingSeq:
				writer = st.eventsW
				trigger = `AFTER INSERT ON main.activity_entry WHEN NEW.action = 'media.merge'`
			case "after events commit":
				trigger = `AFTER UPDATE OF prior_state ON main.op_batch_item`
			}
			// The statement succeeds and only COMMIT fails. TEMP keeps the
			// failure on this connection and lets Open exercise real recovery.
			for _, stmt := range []string{
				`CREATE TEMP TABLE merge_fault_parent (id INTEGER PRIMARY KEY)`,
				`CREATE TEMP TABLE merge_fault_child (parent INTEGER REFERENCES merge_fault_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
				`CREATE TEMP TRIGGER merge_fault ` + trigger + ` BEGIN INSERT INTO merge_fault_child VALUES (1); END`,
			} {
				if _, err := writer.ExecContext(ctx, stmt); err != nil {
					t.Fatal(err)
				}
			}
			result, err := mergeVerifiedVersionPair(t, st, pair)
			if failure == "after events commit" {
				if err != nil || len(result.Merged) != 1 {
					t.Fatalf("merge = %+v, err = %v", result, err)
				}
				var pending bool
				if err := st.catalogR.QueryRowContext(ctx, `
					SELECT json_extract(prior_state, '$.engagementLibraryId') IS NOT NULL
					FROM op_batch_item WHERE batch_id = ?
				`, result.BatchID).Scan(&pending); err != nil || !pending {
					t.Fatalf("pending journal after acknowledgement fault = %v, err = %v", pending, err)
				}
				assertProgress(5, 12)
			} else {
				wantError := "catalog commit failed"
				if writer == st.eventsW {
					wantError = "engagement pending"
					if !errors.Is(err, ErrVersionMergeEngagementPending) || strings.Count(err.Error(), "FOREIGN KEY constraint failed") != 2 {
						t.Fatalf("expected an initial failure and one failed inline retry: %v", err)
					}
				}
				if err == nil || !strings.Contains(err.Error(), wantError) || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
					t.Fatalf("merge = %+v, want injected %s error, got %v", result, failure, err)
				}
				assertProgress(3, 12)
				if writer == st.eventsW {
					if purged, err := st.PurgeTrashedBefore(ctx, time.Now().Add(time.Hour)); err != nil || purged != 0 {
						t.Fatalf("purge removed pending merge source: purged = %d, err = %v", purged, err)
					}
				}
			}
			switch failure {
			case missingSeq:
				var batchID string
				err := st.catalogW.QueryRowContext(ctx, `
					UPDATE op_batch_item SET prior_state = json_remove(prior_state, '$.survivorInsertSeq')
					WHERE json_extract(prior_state, '$.survivor') = ?
					  AND json_extract(prior_state, '$.survivorInsertSeq') IS NOT NULL
					RETURNING batch_id
				`, keep).Scan(&batchID)
				if err != nil || tableRows(t, st.eventsR, "version_merge_playback") == 0 {
					t.Fatalf("expected a pending merge with staged playback, err = %v", err)
				}
			case "restore backup after events failure":
				if count := tableRows(t, st.eventsR, "version_merge_playback"); count != 1 {
					t.Fatalf("backup input has %d pending playback rows, want 1", count)
				}
				installBackupTestSealer(st, dir)
				backup, err := st.BackupWithOptions(ctx, backupDir, BackupOptions{})
				if err != nil {
					t.Fatal(err)
				}
				opener, _ := bootstrapBackupTestCredentials(t, dir, time.Now())
				dir = filepath.Join(t.TempDir(), "restored")
				if _, err := RestoreBackupWithOptions(ctx, backup.Envelope, dir, RestoreOptions{SnapshotOpener: opener}); err != nil {
					t.Fatal(err)
				}
			case "events commit":
				if err := st.SetMediaFavorite(ctx, "profile-default", keep, false, filter); err != nil {
					t.Fatal(err)
				}
			case "erase after events failure":
				if err := st.DeleteMyEngagement(ctx, "profile-default", MyDataDeleteMedia, keep, filter); err != nil {
					t.Fatal(err)
				}
			case "erase profile after events failure":
				if err := st.DeleteMyEngagement(ctx, "profile-default", MyDataDeleteAll, "", filter); err != nil {
					t.Fatal(err)
				}
			case "erase unrelated Media after events failure":
				progress := PlaybackProgress{ProfileID: "profile-default", MediaID: "media-alpha", PlayCount: 1, EventTimeMS: 3000}
				if err := st.UpsertPlaybackProgress(ctx, progress, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
					t.Fatal(err)
				}
				if err := st.DeleteMyEngagement(ctx, "profile-default", MyDataDeleteMedia, progress.MediaID, filter); err != nil {
					t.Fatal(err)
				}
			case "purge and re-import after events failure":
				var rowID int64
				if err := st.catalogR.QueryRowContext(ctx, `SELECT rowid FROM media WHERE id = ?`, keep).Scan(&rowID); err != nil {
					t.Fatal(err)
				}
				if err := st.TrashMedia(ctx, keep, filter); err != nil {
					t.Fatal(err)
				}
				if purged, err := st.PurgeTrashedBefore(ctx, time.Now().Add(time.Hour)); err != nil || purged != 1 {
					t.Fatalf("purge survivor: purged = %d, err = %v", purged, err)
				}
				sample := pair.Sample
				media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
					LibraryID: "library-local", Path: sample.Path, Title: "Re-imported", Container: "mp4", Codec: "h264",
					ContentFingerprint: sample.ContentFingerprint, OSHash: sample.OSHash, SizeBytes: sample.SizeBytes,
				})
				if err != nil || media.ID != keep {
					t.Fatalf("re-imported Media ID = %s, want %s, err = %v", media.ID, keep, err)
				}
				var reusedRowID int64
				if err := st.catalogR.QueryRowContext(ctx, `SELECT rowid FROM media WHERE id = ?`, keep).Scan(&reusedRowID); err != nil || reusedRowID != rowID {
					t.Fatalf("re-import rowid = %d, want reused rowid %d, err = %v", reusedRowID, rowID, err)
				}
			}

			for range 2 {
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				st, err = Open(ctx, dir, opts)
				if err != nil {
					t.Fatal(err)
				}
				if failure == "purge and re-import after events failure" {
					for _, profile := range []ProfileID{"profile-default", "profile-managed"} {
						if progress, found, err := st.PlaybackProgressFor(ctx, profile, keep); err != nil || found {
							t.Fatalf("recovery replayed purged progress for %s: %+v, found = %v, err = %v", profile, progress, found, err)
						}
					}
				} else if failure != "catalog commit" && failure != missingSeq && backupDir == "" {
					progress, found, err := st.PlaybackProgressFor(ctx, "profile-managed", keep)
					if err != nil || !found || progress.PlayCount != 7 || progress.PositionSeconds != 7 {
						t.Fatalf("other Profile progress = %+v, found = %v, err = %v", progress, found, err)
					}
				}
				var staged int
				if err := st.eventsR.QueryRowContext(ctx, `SELECT count(*) FROM version_merge_playback`).Scan(&staged); err != nil || staged != 0 {
					t.Fatalf("journal after recovery has %d pending rows, err = %v", staged, err)
				}
				files, err := st.MediaFilesForMedia(ctx, keep, filter)
				if err != nil {
					t.Fatal(err)
				}
				switch failure {
				case "purge and re-import after events failure":
					if len(files) != 1 || files[0].ContentFingerprint != pair.Sample.ContentFingerprint {
						t.Fatalf("re-imported Media has unexpected files: %+v", files)
					}
				case "catalog commit":
					if _, err := st.Media(ctx, drop, filter); err != nil || len(files) != 1 {
						t.Fatalf("failed merge changed catalog: files = %+v, err = %v", files, err)
					}
					assertProgress(3, 12)
				default:
					if _, err := st.Media(ctx, drop, filter); err == nil || len(files) != 2 {
						t.Fatalf("committed merge missing after reopen: files = %+v, loser err = %v", files, err)
					}
					switch failure {
					case "erase after events failure", "erase profile after events failure":
						if progress, found, err := st.PlaybackProgressFor(ctx, "profile-default", keep); err != nil || found {
							t.Fatalf("recovery restored erased progress: %+v, found = %v, err = %v", progress, found, err)
						}
					case missingSeq:
						assertProgress(3, 12)
					default:
						assertProgress(5, 12)
					}
					if failure == "events commit" || failure == "erase after events failure" {
						if favorite, err := st.MediaFavorited(ctx, "profile-default", keep); err != nil || favorite {
							t.Fatalf("recovery restored removed favorite: favorite = %v, err = %v", favorite, err)
						}
					}
				}
			}
		})
	}
}

func TestVerifiedVersionMergeReplaysProgressIndependentOfPlayHistory(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		importTo   bool
		markFrom   bool
		legacyOnly bool
		position   int
		watched    bool
		plays      int
	}{
		{name: "import on source preserves resume", position: 34, plays: 5},
		{name: "survivor position wins over a newer legacy row without a report timestamp", importTo: true, position: 34, plays: 6},
		{name: "survivor watched state wins over a legacy mark", markFrom: true, position: 34, plays: 6},
		{name: "legacy state fills a missing survivor row", legacyOnly: true, markFrom: true, watched: true, plays: 5},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := t.Context()
			st := openSeeded(t)
			pair := verifiedVersionPair(t, st)
			keep, drop := pair.Sample.MediaID, pair.Legacy.MediaID
			record := func(event playbackProjectionEvent) {
				t.Helper()
				tx, err := st.eventsW.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if err := appendPlaybackProjectionEvent(ctx, tx, event); err != nil {
					t.Fatal(err)
				}
				if err := applyPlaybackProjectionEvent(ctx, tx, event); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			if !scenario.legacyOnly {
				record(playbackProjectionEvent{
					Operation: playbackProjectionOperationProgress, ProfileID: "profile-default", MediaID: string(keep),
					PositionSeconds: 34, DurationSeconds: 100, UpdatedAtMS: 1000, PlayCount: 1,
				})
			}
			importID, importedLast := drop, time.UnixMilli(1800)
			if scenario.importTo {
				importID = keep
				record(playbackProjectionEvent{
					Operation: playbackProjectionOperationProgress, ProfileID: "profile-default", MediaID: string(drop),
					PositionSeconds: 21, DurationSeconds: 100, UpdatedAtMS: 2000, PlayCount: 2,
				})
			}
			if err := st.ApplyImportedPlays(ctx, "profile-default", importID, 4, importedLast, EngagementProvenance{}); err != nil {
				t.Fatal(err)
			}
			wantLast := importedLast.UnixMilli()
			if scenario.markFrom {
				wantLast = 3000
				record(playbackProjectionEvent{
					Operation: playbackProjectionOperationMark, ProfileID: "profile-default", MediaID: string(drop),
					Watched: true, DurationSeconds: 100, UpdatedAtMS: wantLast,
				})
			}
			if result, err := mergeVerifiedVersionPair(t, st, pair); err != nil || len(result.Merged) != 1 {
				t.Fatalf("merge = %+v, err = %v", result, err)
			}
			assertProgress := func() {
				t.Helper()
				progress, found, err := st.PlaybackProgressFor(ctx, "profile-default", keep)
				if err != nil || !found || progress.PositionSeconds != scenario.position ||
					progress.Watched != scenario.watched || progress.PlayCount != scenario.plays {
					t.Fatalf("merged progress = %+v, found = %v, err = %v", progress, found, err)
				}
				var last sql.NullInt64
				if err := st.eventsR.QueryRowContext(ctx, `
					SELECT last_played_at_ms FROM playback_progress WHERE profile_id = ? AND media_id = ?
				`, "profile-default", keep).Scan(&last); err != nil || !last.Valid || last.Int64 != wantLast {
					t.Fatalf("last played = %v, err = %v, want %d", last, err, wantLast)
				}
			}
			assertProgress()
			if err := st.rebuildEngagementProjections(ctx); err != nil {
				t.Fatal(err)
			}
			assertProgress()
		})
	}
}

func verifiedVersionPair(t *testing.T, st *Store) VersionCopyPair {
	t.Helper()
	ctx := t.Context()
	root := t.TempDir()
	var files [2]MediaFile
	for i, name := range []string{"legacy.mp4", "sample.mp4"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("identical bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		fingerprint, err := HashMediaFileContent(path)
		if i != 0 {
			fingerprint, err = SampleMediaFileContentChecked(ctx, path, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		_, files[i], err = st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: "library-local", Path: path, Title: name, Container: "mp4", Codec: "h264",
			ContentFingerprint: fingerprint, OSHash: "same-oshash", SizeBytes: int64(len("identical bytes")),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return VersionCopyPair{Sample: files[1], Legacy: files[0]}
}

func mergeVerifiedVersionPair(t *testing.T, st *Store, pair VersionCopyPair) (VersionMerge, error) {
	t.Helper()
	return st.MergeSplitVersions(t.Context(), "library-local", []MediaID{pair.Sample.MediaID, pair.Legacy.MediaID},
		time.Now(), VersionMergeOptions{Survivor: pair.Sample.MediaID, VerifiedCopy: &pair})
}

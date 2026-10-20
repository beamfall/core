package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// feature:file-operations — preview-first organize/move/merge over Media files
// with the shared dry-run / batch-undo envelope (ADR-0009 §5–6). These tests
// exercise the store engine: organize renders a naming template and moves bytes
// on disk (reversibly), and merge routes byte-identical duplicates through the
// audited Trash path, restorable by undo.

// importFileOnDisk creates a real file at path and imports it as a Media in the
// library, returning the Media id. oshash drives duplicate detection.
func importFileOnDisk(t *testing.T, st *Store, libraryID LibraryID, path, title, oshash string) MediaID {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(title+" bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:          libraryID,
		Path:               path,
		Title:              title,
		Container:          "mp4",
		Codec:              "h264",
		Class:              MediaClassVideo,
		ContentFingerprint: HashMediaFilePath(path),
		OSHash:             oshash,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile(%s): %v", path, err)
	}
	return media.ID
}

func TestMoveFileOnDiskDoesNotOverwriteDestination(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src.mp4")
	dst := filepath.Join(root, "dst.mp4")
	if err := os.WriteFile(src, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("destination"), 0o644); err != nil {
		t.Fatal(err)
	}
	move := managedMove{strategy: managedMoveHardlink}
	if err := executeManagedMove(move, src, dst); err == nil {
		t.Fatal("executeManagedMove overwrote existing destination")
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "destination" {
		t.Fatalf("destination = %q, want unchanged", data)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source missing after failed move: %v", err)
	}
}

func TestApplyFileMovesTxCompensatesInReverseOrder(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	pathA := filepath.Join(root, "a.mp4")
	pathB := filepath.Join(root, "b.mp4")
	pathC := filepath.Join(root, "c.mp4")
	if err := os.WriteFile(pathA, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media_file SET path = ? WHERE media_id = ?`, pathB, "media-alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media_file SET path = ? WHERE media_id = ?`, pathA, "media-archive"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TRIGGER fail_second_move_journal
		BEFORE INSERT ON op_batch_item
		WHEN NEW.seq = 1
		BEGIN
			SELECT RAISE(ABORT, 'injected move journal failure');
		END
	`); err != nil {
		t.Fatal(err)
	}
	tx, err := st.catalogW.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := insertBatchTx(ctx, tx, "batch-reverse-compensation", opKindOrganize, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	plan := []organizeMove{
		{mediaID: "media-alpha", oldPath: pathB, newPath: pathC},
		{mediaID: "media-archive", oldPath: pathA, newPath: pathB},
	}
	plan, err = st.prepareFileMoves(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyFileMovesTx(ctx, tx, "batch-reverse-compensation", plan); err == nil || !strings.Contains(err.Error(), "injected move journal failure") {
		t.Fatalf("applyFileMovesTx err = %v, want injected failure", err)
	}

	assertFileContent(t, pathA, "a")
	assertFileContent(t, pathB, "b")
	if _, err := os.Stat(pathC); !os.IsNotExist(err) {
		t.Fatalf("compensation left terminal path %s: %v", pathC, err)
	}
}

func TestUndoBatchCompensatesDiskMoveWhenBatchUpdateFails(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	oldPath := filepath.Join(root, "old.mp4")
	newPath := filepath.Join(root, "new.mp4")
	mediaID := importFileOnDisk(t, st, "library-local", newPath, "Undo Failure", "00000000abcddcba")
	state, err := json.Marshal(fileMoveItemState{OldPath: oldPath, NewPath: newPath})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.catalogW.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertBatchTx(ctx, tx, "batch-undo-failure", opKindOrganize, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := appendBatchItemTx(ctx, tx, "batch-undo-failure", 0, mediaID, state); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TRIGGER fail_undo_batch_update
		BEFORE UPDATE OF undone_at_ms ON op_batch
		WHEN NEW.id = 'batch-undo-failure'
		BEGIN
			SELECT RAISE(ABORT, 'injected undo batch update failure');
		END
	`); err != nil {
		t.Fatal(err)
	}

	if _, err := st.UndoBatch(ctx, "batch-undo-failure", LibraryFilter{Revealed: true, AllLibraries: true}, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "injected undo batch update failure") {
		t.Fatalf("UndoBatch err = %v, want injected failure", err)
	}
	assertFileContent(t, newPath, "Undo Failure bytes")
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("failed undo left file at prior path %s: %v", oldPath, err)
	}
	var catalogPath string
	if err := st.catalogR.QueryRowContext(ctx, `SELECT path FROM media_file WHERE media_id = ?`, mediaID).Scan(&catalogPath); err != nil {
		t.Fatal(err)
	}
	if catalogPath != newPath {
		t.Fatalf("catalog path after rollback = %q, want %q", catalogPath, newPath)
	}
}

func TestCompensateFileMovesJoinsCauseAndEveryFilesystemFailure(t *testing.T) {
	root := t.TempDir()
	firstOld := filepath.Join(root, "first-old.mp4")
	firstNew := filepath.Join(root, "first-new.mp4")
	secondOld := filepath.Join(root, "second-old.mp4")
	secondNew := filepath.Join(root, "second-new.mp4")
	for _, path := range []string{firstOld, firstNew, secondOld, secondNew} {
		if err := os.WriteFile(path, []byte(filepath.Base(path)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cause := errors.New("injected transaction failure")
	err := compensateFileMoves(cause, []organizeMove{
		{oldPath: firstOld, newPath: firstNew, move: managedMove{strategy: managedMoveHardlink}},
		{oldPath: secondOld, newPath: secondNew, move: managedMove{strategy: managedMoveHardlink}},
	})
	if !errors.Is(err, cause) {
		t.Fatalf("compensation err = %v, want original cause", err)
	}
	for _, path := range []string{firstOld, secondOld} {
		if !strings.Contains(err.Error(), path) {
			t.Fatalf("compensation err = %v, want failure for %s", err, path)
		}
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	// #nosec G304 -- path is constructed beneath a test-owned temporary directory.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("content at %s = %q, want %q", path, got, want)
	}
}

func TestOrganizePreviewAndApplyMovesFilesReversibly(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(root, "unsorted", "raw-clip.mp4")
	mediaID := importFileOnDisk(t, st, "library-local", oldPath, "Atlas", "00000000aaaa0001")
	if err := st.SetMediaFields(ctx, mediaID, []MediaField{
		{Name: "collection", Type: EntityFieldText, Text: "Voyages"},
	}); err != nil {
		t.Fatal(err)
	}
	subscription, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/organize.xml",
		Root:      root,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, subscription.ID, "organize-guid", oldPath, now); err != nil {
		t.Fatal(err)
	}

	template := "{collection}/{title}"
	wantNew := filepath.Join(root, "Voyages", "Atlas.mp4")

	// Preview: the old→new change-set, no disk touch.
	changes, err := st.OrganizePreview(ctx, "library-local", template, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].OldPath != oldPath || changes[0].NewPath != wantNew {
		t.Fatalf("preview = %#v, want %s -> %s", changes, oldPath, wantNew)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("preview moved the file: %v", err)
	}

	// Apply: bytes move on disk and the record repoints.
	batch, err := st.OrganizeApply(ctx, "library-local", template, LibraryFilter{AllLibraries: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if batch.ID == "" || batch.Kind != opKindOrganize || batch.Affected != 1 {
		t.Fatalf("organize batch = %#v", batch)
	}
	if _, err := os.Stat(wantNew); err != nil {
		t.Fatalf("file not at new path after apply: %v", err)
	}
	assertFileContent(t, wantNew, "Atlas bytes")
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old path still exists after apply: %v", err)
	}
	file, err := st.PrimaryMediaFile(ctx, mediaID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if file.Path != wantNew {
		t.Fatalf("record path = %q, want %q", file.Path, wantNew)
	}
	var seenPath string
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT path FROM subscription_seen_entry WHERE subscription_id = ? AND guid = ?
	`, subscription.ID, "organize-guid").Scan(&seenPath); err != nil {
		t.Fatal(err)
	}
	if seenPath != wantNew {
		t.Fatalf("seen-entry path after organize = %q, want %q", seenPath, wantNew)
	}

	// Undo: bytes move back, the record repoints back.
	if _, err := st.UndoBatch(ctx, batch.ID, LibraryFilter{AllLibraries: true}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("file not restored to old path after undo: %v", err)
	}
	assertFileContent(t, oldPath, "Atlas bytes")
	if _, err := os.Stat(wantNew); !os.IsNotExist(err) {
		t.Fatalf("new path still exists after undo: %v", err)
	}
	file, err = st.PrimaryMediaFile(ctx, mediaID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if file.Path != oldPath {
		t.Fatalf("record path after undo = %q, want %q", file.Path, oldPath)
	}
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT path FROM subscription_seen_entry WHERE subscription_id = ? AND guid = ?
	`, subscription.ID, "organize-guid").Scan(&seenPath); err != nil {
		t.Fatal(err)
	}
	if seenPath != oldPath {
		t.Fatalf("seen-entry path after undo = %q, want %q", seenPath, oldPath)
	}
}

// Evidence — LCRES-1 (2026-08-21 UTC)
//
// Planted red (HEAD 816b70bec21df3f975728ff214733de662e74e72):
// command: go test ./internal/store -run '^TestUndoBatchRepointsCatalogWhenOrganizedFileIsMissing$' -count=1
// start: 2026-08-21T01:27:49Z; end: 2026-08-21T01:27:53Z; elapsed: 4s; exit: 1
// failure: file_operations_test.go:372: record path after missing-file undo = ".../Voyages/Missing.mp4", want ".../unsorted/missing.mp4"
// log: .agent-evidence/20260821T012749Z-LCRES-1-planted-red.sK2jNP/output.log
//
// Restored implementation (HEAD 816b70bec21df3f975728ff214733de662e74e72):
// command: go test ./internal/store -run '^TestUndoBatchRepointsCatalogWhenOrganizedFileIsMissing$' -count=1
// start: 2026-08-21T01:28:31Z; end: 2026-08-21T01:28:35Z; elapsed: 4s; exit: 0
// log: .agent-evidence/20260821T012831Z-LCRES-1-focused-pass.GgZzmY/output.log
//
// Package regression (HEAD 816b70bec21df3f975728ff214733de662e74e72):
// command: go test ./internal/store -count=1
// start: 2026-08-21T01:29:08Z; end: 2026-08-21T01:31:20Z; elapsed: 132s; exit: 0
// log: .agent-evidence/20260821T012908Z-LCRES-1-store-package.roeM1L/output.log
func TestUndoBatchRepointsCatalogWhenOrganizedFileIsMissing(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(root, "unsorted", "missing.mp4")
	mediaID := importFileOnDisk(t, st, "library-local", oldPath, "Missing", "00000000aaaa0002")
	if err := st.SetMediaFields(ctx, mediaID, []MediaField{
		{Name: "collection", Type: EntityFieldText, Text: "Voyages"},
	}); err != nil {
		t.Fatal(err)
	}
	subscription, err := st.AddSubscription(ctx, Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/missing-organized-file.xml",
		Root:      root,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, subscription.ID, "missing-guid", oldPath, now); err != nil {
		t.Fatal(err)
	}

	batch, err := st.OrganizeApply(ctx, "library-local", "{collection}/{title}", LibraryFilter{AllLibraries: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(root, "Voyages", "Missing.mp4")
	if err := os.Remove(newPath); err != nil {
		t.Fatal(err)
	}
	updatedBeforeUndo := countMediaEvents(mediaOutboxEvents(t, st), MediaEventUpdated, mediaID)
	undoAt := now.Add(time.Minute)

	undone, err := st.UndoBatch(ctx, batch.ID, LibraryFilter{AllLibraries: true}, undoAt)
	if err != nil {
		t.Fatal(err)
	}
	if undone.ID != batch.ID || undone.Kind != opKindOrganize || undone.Affected != 1 {
		t.Fatalf("undo batch = %#v, want organize batch %q affecting 1", undone, batch.ID)
	}

	file, err := st.PrimaryMediaFile(ctx, mediaID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if file.Path != oldPath {
		t.Fatalf("record path after missing-file undo = %q, want %q", file.Path, oldPath)
	}
	var seenPath string
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT path FROM subscription_seen_entry WHERE subscription_id = ? AND guid = ?
	`, subscription.ID, "missing-guid").Scan(&seenPath); err != nil {
		t.Fatal(err)
	}
	if seenPath != oldPath {
		t.Fatalf("seen-entry path after missing-file undo = %q, want %q", seenPath, oldPath)
	}
	if got := countMediaEvents(mediaOutboxEvents(t, st), MediaEventUpdated, mediaID); got != updatedBeforeUndo+1 {
		t.Fatalf("media.updated count after missing-file undo = %d, want %d", got, updatedBeforeUndo+1)
	}
	var undoneAt int64
	if err := st.catalogR.QueryRowContext(ctx, `SELECT undone_at_ms FROM op_batch WHERE id = ?`, batch.ID).Scan(&undoneAt); err != nil {
		t.Fatal(err)
	}
	if undoneAt != timeToMillis(undoAt) {
		t.Fatalf("undone_at_ms = %d, want %d", undoneAt, timeToMillis(undoAt))
	}
}

func TestOrganizeDropsItemsWithUnresolvableTokens(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "clip.mp4")
	importFileOnDisk(t, st, "library-local", path, "Lonely", "00000000bbbb0001")
	// No "collection" field set, so the template cannot fully render — the item
	// is dropped from the plan rather than placed under a half-rendered name.

	changes, err := st.OrganizePreview(ctx, "library-local", "{collection}/{title}", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("preview = %#v, want empty (unresolvable token)", changes)
	}
}

func TestOrganizeRejectsInvalidArgs(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if _, err := st.OrganizePreview(ctx, "library-local", "  ", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("preview accepted an empty template")
	}
	// A hidden Library yields an empty preview, never an error/oracle.
	changes, err := st.OrganizePreview(ctx, "library-archive", "{title}", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("hidden library preview = %#v, want empty", changes)
	}
	// Apply on an unknown/hidden Library is an error.
	if _, err := st.OrganizeApply(ctx, "library-archive", "{title}", LibraryFilter{AllLibraries: true}, time.Now().UTC()); err == nil {
		t.Fatal("apply accepted a hidden library")
	}
}

func TestOrganizeRejectsTraversalOutOfRoot(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	parent := t.TempDir()
	root := filepath.Join(parent, "library")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(root, "clip.mp4")
	mediaID := importFileOnDisk(t, st, "library-local", oldPath, "Atlas", "00000000ffff0001")

	// escapedFile is where a successful traversal would land the bytes — a
	// sibling of the Library root, outside it. It must never be created.
	escapedFile := filepath.Join(parent, "Atlas.mp4")

	// 1. A literal ".." segment in the template is rejected outright.
	if _, err := st.OrganizePreview(ctx, "library-local", "../{title}", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("preview accepted a template with a .. segment")
	}
	if _, err := st.OrganizeApply(ctx, "library-local", "../{title}", LibraryFilter{AllLibraries: true}, now); err == nil {
		t.Fatal("apply accepted a template with a .. segment")
	}

	// 2. A field value of ".." cannot introduce traversal: sanitizePathSegment
	// neutralises it, so the file stays under root (subdir "_").
	if err := st.SetMediaFields(ctx, mediaID, []MediaField{
		{Name: "collection", Type: EntityFieldText, Text: ".."},
	}); err != nil {
		t.Fatal(err)
	}
	changes, err := st.OrganizePreview(ctx, "library-local", "{collection}/{title}", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("preview with a .. field value errored: %v", err)
	}
	wantSanitized := filepath.Join(root, "_", "Atlas.mp4")
	if len(changes) != 1 || changes[0].NewPath != wantSanitized {
		t.Fatalf("preview = %#v, want new path %s (sanitized)", changes, wantSanitized)
	}

	// The escaping destination was never written, and the source is untouched.
	if _, err := os.Stat(escapedFile); !os.IsNotExist(err) {
		t.Fatalf("traversal wrote a file outside root: %v", err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("source file disturbed by a rejected/sanitized organize: %v", err)
	}
	file, err := st.PrimaryMediaFile(ctx, mediaID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if file.Path != oldPath {
		t.Fatalf("record path = %q, want %q (unmoved after rejected/sanitized organize)", file.Path, oldPath)
	}

	// 3. A normal template still moves the file under root.
	if err := st.SetMediaFields(ctx, mediaID, []MediaField{
		{Name: "collection", Type: EntityFieldText, Text: "Voyages"},
	}); err != nil {
		t.Fatal(err)
	}
	wantNew := filepath.Join(root, "Voyages", "Atlas.mp4")
	batch, err := st.OrganizeApply(ctx, "library-local", "{collection}/{title}", LibraryFilter{AllLibraries: true}, now)
	if err != nil {
		t.Fatalf("normal organize errored: %v", err)
	}
	if batch.Affected != 1 {
		t.Fatalf("normal organize batch = %#v, want 1 affected", batch)
	}
	if _, err := os.Stat(wantNew); err != nil {
		t.Fatalf("normal organize did not place the file under root: %v", err)
	}
}

func TestMergeTrashesDuplicatesThroughTrashAndUndoRestores(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	dup := "00000000cccc0001"
	keep := importFileOnDisk(t, st, "library-local", filepath.Join(root, "keep.mp4"), "Keep", dup)
	drop := importFileOnDisk(t, st, "library-local", filepath.Join(root, "drop.mp4"), "Drop", dup)
	// A non-duplicate, distinct content, must be untouched.
	other := importFileOnDisk(t, st, "library-local", filepath.Join(root, "other.mp4"), "Other", "00000000dddd0001")

	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	batch, err := st.MergeApply(ctx, "library-local", keep, filter, now)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Kind != opKindMerge || batch.Affected != 1 {
		t.Fatalf("merge batch = %#v, want 1 trashed", batch)
	}

	// The dropped duplicate is in Trash (soft-deleted, not hard-deleted); keep
	// and other are still live.
	if _, err := st.Media(ctx, drop, filter); err == nil {
		t.Fatal("dropped duplicate still visible — not trashed")
	}
	if _, err := st.Media(ctx, keep, filter); err != nil {
		t.Fatalf("survivor missing after merge: %v", err)
	}
	if _, err := st.Media(ctx, other, filter); err != nil {
		t.Fatalf("non-duplicate trashed by merge: %v", err)
	}
	trash, err := st.TrashedMediaList(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range trash {
		if entry.ID == drop {
			found = true
		}
	}
	if !found {
		t.Fatalf("dropped duplicate not in Trash: %#v", trash)
	}
	// The dropped file's bytes survive on disk (recoverable, invariant #6).
	if _, err := os.Stat(filepath.Join(root, "drop.mp4")); err != nil {
		t.Fatalf("merge deleted source bytes: %v", err)
	}

	// Undo restores the dropped duplicate from Trash.
	if _, err := st.UndoBatch(ctx, batch.ID, filter, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Media(ctx, drop, filter); err != nil {
		t.Fatalf("dropped duplicate not restored after undo: %v", err)
	}
}

func TestMergeRejectsNonDuplicateKeep(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	lone := importFileOnDisk(t, st, "library-local", filepath.Join(root, "lone.mp4"), "Lone", "00000000eeee0001")
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	if _, err := st.MergeApply(ctx, "library-local", lone, filter, time.Now().UTC()); err == nil ||
		!strings.Contains(err.Error(), "duplicate cluster") {
		t.Fatalf("merge of a lone file err = %v, want duplicate-cluster rejection", err)
	}
	if _, err := st.MergeApply(ctx, "library-local", "", filter, time.Now().UTC()); err == nil {
		t.Fatal("merge accepted an empty keep id")
	}
}

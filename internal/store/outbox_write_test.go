package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// mediaOutboxEvents reads every media-aggregate outbox row, oldest first. The
// SSE read half (ReadEventOutbox) is unscoped — reveal filtering is the delivery
// side's concern — so this sees the write side directly, which is what these
// tests assert.
func mediaOutboxEvents(t *testing.T, st *Store) []OutboxEvent {
	t.Helper()
	all, err := st.ReadEventOutbox(context.Background(), 0, 1000)
	if err != nil {
		t.Fatalf("ReadEventOutbox: %v", err)
	}
	media := make([]OutboxEvent, 0, len(all))
	for _, e := range all {
		if e.AggregateType == OutboxAggregateMedia {
			media = append(media, e)
		}
	}
	return media
}

// countMediaEvents returns how many media outbox rows match kind+aggregateID.
func countMediaEvents(events []OutboxEvent, kind string, id MediaID) int {
	n := 0
	for _, e := range events {
		if e.Kind == kind && e.AggregateID == string(id) {
			n++
		}
	}
	return n
}

// TestImportMediaFileEmitsCreatedThenUpdated proves the scan/create write path
// is wired to the realtime outbox (H13): a first import emits media.created and
// a re-import of the same content emits media.updated (not a second created).
func TestImportMediaFileEmitsCreatedThenUpdated(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// Baseline: the dev seed plants media-alpha/media-archive directly via the
	// seed tx, which does NOT route through ImportMediaFile, so there are no
	// pre-existing media outbox rows.
	if n := len(mediaOutboxEvents(t, st)); n != 0 {
		t.Fatalf("seed planted %d media outbox rows, want 0 (seed bypasses the event path)", n)
	}

	first, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/h13-created.mp4",
		Title:              "H13 Created",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:h13create",
	})
	if err != nil {
		t.Fatalf("first ImportMediaFile: %v", err)
	}
	events := mediaOutboxEvents(t, st)
	if got := countMediaEvents(events, MediaEventCreated, first.ID); got != 1 {
		t.Fatalf("media.created count for %s = %d, want 1", first.ID, got)
	}
	if got := countMediaEvents(events, MediaEventUpdated, first.ID); got != 0 {
		t.Fatalf("media.updated count after first import = %d, want 0", got)
	}

	// Re-import the same content (same fingerprint) → the row already exists →
	// media.updated, never a second media.created.
	second, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/h13-created-moved.mp4",
		Title:              "H13 Created Moved",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:h13create",
	})
	if err != nil {
		t.Fatalf("second ImportMediaFile: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("re-import id = %q, want same %q", second.ID, first.ID)
	}
	events = mediaOutboxEvents(t, st)
	if got := countMediaEvents(events, MediaEventCreated, first.ID); got != 1 {
		t.Fatalf("media.created count after re-import = %d, want 1 (no second created)", got)
	}
	if got := countMediaEvents(events, MediaEventUpdated, first.ID); got != 1 {
		t.Fatalf("media.updated count after re-import = %d, want 1", got)
	}
}

// TestTrashMediaEmitsDeleted proves the trash write path emits exactly one
// media.deleted (delivery is proven by the server's
// TestEventStreamDeliversDeletionRevealFiltered, which gates the event by the
// payload Library). (The RowsAffected>0 guard that suppresses an event for a
// no-op trash is not reachable through the public method — TrashMedia's
// visibility check excludes trashed rows, so re-trashing errors before the
// UPDATE — but the guard still covers a concurrent-writer TOCTOU race.)
func TestTrashMediaEmitsDeleted(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}

	if err := st.TrashMedia(ctx, "media-alpha", revealed); err != nil {
		t.Fatalf("TrashMedia: %v", err)
	}
	events := mediaOutboxEvents(t, st)
	if got := countMediaEvents(events, MediaEventDeleted, "media-alpha"); got != 1 {
		t.Fatalf("media.deleted count = %d, want 1", got)
	}
}

// TestUpdateMediaMetadataEmitsUpdated proves the metadata write path emits
// media.updated.
func TestUpdateMediaMetadataEmitsUpdated(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	title := "Renamed Alpha"
	if err := st.UpdateMediaMetadata(ctx, "media-alpha", MediaMetadata{Title: &title}); err != nil {
		t.Fatalf("UpdateMediaMetadata: %v", err)
	}
	events := mediaOutboxEvents(t, st)
	if got := countMediaEvents(events, MediaEventUpdated, "media-alpha"); got != 1 {
		t.Fatalf("media.updated count = %d, want 1", got)
	}
}

// TestFailedMediaWriteEmitsNoEvent proves the cross-DB ordering guarantee: when
// the catalog write fails (and its tx rolls back), no outbox row is written —
// an event can never reference an unpersisted Media. Importing into a
// nonexistent library fails inside importMediaFileSQL's tx before any commit.
func TestFailedMediaWriteEmitsNoEvent(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	_, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-does-not-exist",
		Path:               "/media/h13-phantom.mp4",
		Title:              "Phantom",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:h13phantom",
	})
	if err == nil {
		t.Fatal("ImportMediaFile into a missing library should fail")
	}
	if n := len(mediaOutboxEvents(t, st)); n != 0 {
		t.Fatalf("failed import wrote %d media outbox rows, want 0 (no phantom event)", n)
	}
}

// deletionPayloadLibrary returns the LibraryID carried by the (single) expected
// media.deleted event for id, failing the test if there isn't exactly one.
func deletionPayloadLibrary(t *testing.T, events []OutboxEvent, id MediaID) LibraryID {
	t.Helper()
	var found *OutboxEvent
	for i := range events {
		if events[i].Kind == MediaEventDeleted && events[i].AggregateID == string(id) {
			if found != nil {
				t.Fatalf("more than one media.deleted for %s", id)
			}
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatalf("no media.deleted event for %s", id)
	}
	var payload MediaDeletedPayload
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatalf("media.deleted payload for %s is not JSON: %v (%q)", id, err, found.Payload)
	}
	return payload.LibraryID
}

// TestTrashMediaDeletedEventCarriesLibrary proves the H13 follow-up B write side:
// a media.deleted event records the deleted Media's LibraryID in its payload, so
// the delivery side can reveal-filter by Library without a live read of the gone
// row. The restricted item's payload must carry library-archive.
func TestTrashMediaDeletedEventCarriesLibrary(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}

	if err := st.TrashMedia(ctx, "media-alpha", revealed); err != nil {
		t.Fatalf("TrashMedia open: %v", err)
	}
	if err := st.TrashMedia(ctx, "media-archive", revealed); err != nil {
		t.Fatalf("TrashMedia restricted: %v", err)
	}
	events := mediaOutboxEvents(t, st)
	if lib := deletionPayloadLibrary(t, events, "media-alpha"); lib != "library-local" {
		t.Fatalf("media-alpha deletion library = %q, want library-local", lib)
	}
	if lib := deletionPayloadLibrary(t, events, "media-archive"); lib != "library-archive" {
		t.Fatalf("media-archive deletion library = %q, want library-archive", lib)
	}
}

// TestRestoreMediaEmitsCreated proves un-trash emits media.created so a client
// that dropped the item on media.deleted re-adds it.
func TestRestoreMediaEmitsCreated(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	if err := st.TrashMedia(ctx, "media-alpha", filter); err != nil {
		t.Fatalf("TrashMedia: %v", err)
	}
	if err := st.RestoreMedia(ctx, "media-alpha", filter); err != nil {
		t.Fatalf("RestoreMedia: %v", err)
	}
	events := mediaOutboxEvents(t, st)
	if got := countMediaEvents(events, MediaEventCreated, "media-alpha"); got != 1 {
		t.Fatalf("media.created count after restore = %d, want 1", got)
	}
}

// TestBulkEditApplyEmitsUpdatedPerItem proves a bulk descriptive-field edit
// emits one media.updated per affected Media (and only the visible ones).
func TestBulkEditApplyEmitsUpdatedPerItem(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	fields := []MediaField{{Name: "collection", Type: EntityFieldText, Text: "Voyages"}}
	batch, err := st.BulkEditApply(ctx, []MediaID{"media-alpha"}, fields, filter, now)
	if err != nil {
		t.Fatalf("BulkEditApply: %v", err)
	}
	if batch.Affected != 1 {
		t.Fatalf("bulk edit affected = %d, want 1", batch.Affected)
	}
	events := mediaOutboxEvents(t, st)
	if got := countMediaEvents(events, MediaEventUpdated, "media-alpha"); got != 1 {
		t.Fatalf("media.updated count after bulk edit = %d, want 1", got)
	}
}

// TestOrganizeApplyEmitsUpdatedPerMovedMedia proves a path move emits
// media.updated for every moved Media after the catalog commit.
func TestOrganizeApplyEmitsUpdatedPerMovedMedia(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(root, "unsorted", "raw-clip.mp4")
	mediaID := importFileOnDisk(t, st, "library-local", oldPath, "Atlas", "00000000h130001")
	if err := st.SetMediaFields(ctx, mediaID, []MediaField{
		{Name: "collection", Type: EntityFieldText, Text: "Voyages"},
	}); err != nil {
		t.Fatal(err)
	}
	// Baseline the ImportMediaFile + SetMediaFields rows so this assertion is
	// specifically about the organize apply.
	before := countMediaEvents(mediaOutboxEvents(t, st), MediaEventUpdated, mediaID)

	batch, err := st.OrganizeApply(ctx, "library-local", "{collection}/{title}", LibraryFilter{AllLibraries: true}, now)
	if err != nil {
		t.Fatalf("OrganizeApply: %v", err)
	}
	if batch.Affected != 1 {
		t.Fatalf("OrganizeApply affected = %d, want 1", batch.Affected)
	}
	events := mediaOutboxEvents(t, st)
	if got := countMediaEvents(events, MediaEventUpdated, mediaID); got != before+1 {
		t.Fatalf("media.updated count after organize = %d, want %d", got, before+1)
	}
}

// TestFieldWritesEmitUpdated proves the three plugin field-write paths each emit
// media.updated, and that the no-op early returns emit nothing (a write that
// changed no row must not announce a change).
func TestFieldWritesEmitUpdated(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// SetMediaFields (full replace) → one updated.
	if err := st.SetMediaFields(ctx, "media-alpha", []MediaField{
		{Name: "collection", Type: EntityFieldText, Text: "A"},
	}); err != nil {
		t.Fatalf("SetMediaFields: %v", err)
	}
	// UpsertMediaFields (partial replace) → one updated.
	if err := st.UpsertMediaFields(ctx, "media-alpha", []MediaField{
		{Name: "rating", Type: EntityFieldNumber, Number: 4},
	}); err != nil {
		t.Fatalf("UpsertMediaFields: %v", err)
	}
	// InsertMediaFieldsIfAbsent writing a new field → one updated.
	if err := st.InsertMediaFieldsIfAbsent(ctx, "media-alpha", []MediaField{
		{Name: "studio", Type: EntityFieldText, Text: "X"},
	}); err != nil {
		t.Fatalf("InsertMediaFieldsIfAbsent new: %v", err)
	}
	if got := countMediaEvents(mediaOutboxEvents(t, st), MediaEventUpdated, "media-alpha"); got != 3 {
		t.Fatalf("media.updated count after three field writes = %d, want 3", got)
	}

	// A no-op InsertMediaFieldsIfAbsent (field already present) writes nothing and
	// must emit nothing.
	if err := st.InsertMediaFieldsIfAbsent(ctx, "media-alpha", []MediaField{
		{Name: "studio", Type: EntityFieldText, Text: "Y"},
	}); err != nil {
		t.Fatalf("InsertMediaFieldsIfAbsent absent-noop: %v", err)
	}
	if got := countMediaEvents(mediaOutboxEvents(t, st), MediaEventUpdated, "media-alpha"); got != 3 {
		t.Fatalf("media.updated count after no-op insert = %d, want 3 (no extra event)", got)
	}
}

// TestMergeApplyEmitsDeletedAndUpdated proves a dedupe merge emits media.deleted
// (with the cluster's LibraryID) for each merged-away loser and one
// media.updated for the survivor.
func TestMergeApplyEmitsDeletedAndUpdated(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	dup := "00000000eeee0001"
	keep := importFileOnDisk(t, st, "library-local", filepath.Join(root, "keep.mp4"), "Keep", dup)
	drop := importFileOnDisk(t, st, "library-local", filepath.Join(root, "drop.mp4"), "Drop", dup)

	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	batch, err := st.MergeApply(ctx, "library-local", keep, filter, now)
	if err != nil {
		t.Fatalf("MergeApply: %v", err)
	}
	if batch.Affected != 1 {
		t.Fatalf("merge affected = %d, want 1", batch.Affected)
	}
	events := mediaOutboxEvents(t, st)
	if got := countMediaEvents(events, MediaEventDeleted, drop); got != 1 {
		t.Fatalf("merged-away media.deleted count = %d, want 1", got)
	}
	if lib := deletionPayloadLibrary(t, events, drop); lib != "library-local" {
		t.Fatalf("merge deletion library = %q, want library-local", lib)
	}
	if got := countMediaEvents(events, MediaEventUpdated, keep); got != 1 {
		t.Fatalf("survivor media.updated count = %d, want 1", got)
	}
	// The survivor is never announced deleted.
	if got := countMediaEvents(events, MediaEventDeleted, keep); got != 0 {
		t.Fatalf("survivor wrongly announced deleted (count=%d)", got)
	}
}

// TestUndoBatchBulkEditEmitsUpdated proves undoing a bulk edit emits one
// media.updated per affected Media — the field/path-state undo case (the undo
// restored prior values on a still-present Media), mirroring how BulkEditApply
// announces the forward edit.
func TestUndoBatchBulkEditEmitsUpdated(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	edit := []MediaField{{Name: "genre", Type: EntityFieldText, Text: "new"}}
	batch, err := st.BulkEditApply(ctx, []MediaID{"media-alpha"}, edit, LibraryFilter{Revealed: true, AllLibraries: true}, now)
	if err != nil {
		t.Fatalf("BulkEditApply: %v", err)
	}
	// The forward apply already announced one media.updated; baseline that so the
	// undo's event is an observable increment, not a pre-existing row.
	if got := countMediaEvents(mediaOutboxEvents(t, st), MediaEventUpdated, "media-alpha"); got != 1 {
		t.Fatalf("media.updated after apply = %d, want 1", got)
	}

	undone, err := st.UndoBatch(ctx, batch.ID, LibraryFilter{Revealed: true, AllLibraries: true}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("UndoBatch: %v", err)
	}
	if undone.Affected != 1 {
		t.Fatalf("undo affected = %d, want 1", undone.Affected)
	}
	// Undo emits the affected Media's media.updated → count is now 2 (apply + undo).
	if got := countMediaEvents(mediaOutboxEvents(t, st), MediaEventUpdated, "media-alpha"); got != 2 {
		t.Fatalf("media.updated after undo = %d, want 2 (apply + undo)", got)
	}
	// A field/path-state undo never resurrects, so no media.created.
	if got := countMediaEvents(mediaOutboxEvents(t, st), MediaEventCreated, "media-alpha"); got != 0 {
		t.Fatalf("bulk-edit undo wrongly emitted media.created (count=%d)", got)
	}
}

// TestUndoBatchMergeEmitsCreated proves undoing a dedupe merge that resurrects a
// trashed loser emits media.created for that loser — the inverse of MergeApply's
// media.deleted, mirroring how RestoreMedia announces a restored-from-trash row.
func TestUndoBatchMergeEmitsCreated(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	dup := "00000000eeee0002"
	keep := importFileOnDisk(t, st, "library-local", filepath.Join(root, "keep.mp4"), "Keep", dup)
	drop := importFileOnDisk(t, st, "library-local", filepath.Join(root, "drop.mp4"), "Drop", dup)

	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	batch, err := st.MergeApply(ctx, "library-local", keep, filter, now)
	if err != nil {
		t.Fatalf("MergeApply: %v", err)
	}
	// Baseline the loser's media.created: importFileOnDisk routes through
	// ImportMediaFile, which already emitted one at import. MergeApply then trashed
	// the loser (media.deleted) — it adds no created. The undo's created must be an
	// observable increment over this baseline.
	createdBefore := countMediaEvents(mediaOutboxEvents(t, st), MediaEventCreated, drop)
	if createdBefore != 1 {
		t.Fatalf("media.created for loser before undo = %d, want 1 (the import)", createdBefore)
	}
	// MergeApply already announced one media.updated for the survivor; the undo
	// must not add another (the undo restores the loser, not the survivor).
	survivorUpdatedBefore := countMediaEvents(mediaOutboxEvents(t, st), MediaEventUpdated, keep)

	undone, err := st.UndoBatch(ctx, batch.ID, filter, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("UndoBatch: %v", err)
	}
	if undone.Affected != 1 {
		t.Fatalf("undo affected = %d, want 1", undone.Affected)
	}
	events := mediaOutboxEvents(t, st)
	// The resurrected loser re-enters the catalog → one additional media.created.
	if got := countMediaEvents(events, MediaEventCreated, drop); got != createdBefore+1 {
		t.Fatalf("resurrected loser media.created count = %d, want %d (import + undo)", got, createdBefore+1)
	}
	// A merge undo restores the loser, not the survivor — the survivor gets no
	// additional undo event (its catalog row never changed on undo).
	if got := countMediaEvents(events, MediaEventUpdated, keep); got != survivorUpdatedBefore {
		t.Fatalf("survivor media.updated = %d, want %d (no extra from undo)", got, survivorUpdatedBefore)
	}
}

// TestRestrictedMediaEventWritesUnconditionally documents the write/deliver
// split: the write side records a restricted-Media event unconditionally (it
// names its Media in AggregateID); reveal filtering happens only at SSE
// delivery (server.eventPermitted, covered by the server tests). A re-import of
// restricted media-archive must still produce an outbox row here.
func TestRestrictedMediaEventWritesUnconditionally(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}

	if err := st.TrashMedia(ctx, "media-archive", revealed); err != nil {
		t.Fatalf("TrashMedia restricted: %v", err)
	}
	events := mediaOutboxEvents(t, st)
	if got := countMediaEvents(events, MediaEventDeleted, "media-archive"); got != 1 {
		t.Fatalf("restricted media.deleted write count = %d, want 1 (write is unconditional; filtering is delivery-side)", got)
	}
}

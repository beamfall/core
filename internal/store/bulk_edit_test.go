package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// feature:bulk-edit — bulk metadata edit with the dry-run preview envelope and
// batch undo. These tests exercise the store engine: preview is a
// pure read, apply commits one undo-able batch, and undo restores the exact
// prior field values.

// fieldByName indexes a Media's field values for assertions.
func fieldByName(t *testing.T, st *Store, id MediaID) map[string]MediaField {
	t.Helper()
	got, err := st.MediaFields(context.Background(), id, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("MediaFields(%s): %v", id, err)
	}
	byName := map[string]MediaField{}
	for _, f := range got {
		byName[f.Name] = f
	}
	return byName
}

func TestBulkEditPreviewIsPureAndScoped(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// media-alpha (visible) already carries a value the edit will overwrite;
	// media-archive lives in the restricted library, hidden without reveal.
	if err := st.SetMediaFields(ctx, "media-alpha", []MediaField{
		{Name: "genre", Type: EntityFieldText, Text: "old"},
	}); err != nil {
		t.Fatal(err)
	}

	edit := []MediaField{{Name: "genre", Type: EntityFieldText, Text: "new"}}
	ids := []MediaID{"media-alpha", "media-archive"}

	// Unrevealed: only media-alpha is in the change-set; media-archive is
	// silently absent (never an existence oracle).
	changes, err := st.BulkEditPreview(ctx, ids, edit, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].MediaID != "media-alpha" {
		t.Fatalf("preview = %#v, want only media-alpha", changes)
	}
	if len(changes[0].Old) != 1 || changes[0].Old[0].Text != "old" {
		t.Fatalf("preview Old = %#v, want genre=old", changes[0].Old)
	}
	if len(changes[0].New) != 1 || changes[0].New[0].Text != "new" {
		t.Fatalf("preview New = %#v, want genre=new", changes[0].New)
	}

	// Preview must not have mutated anything.
	if got := fieldByName(t, st, "media-alpha")["genre"].Text; got != "old" {
		t.Fatalf("preview mutated the store: genre = %q, want old", got)
	}

	// Revealed: media-archive joins the change-set with no prior value.
	revealed, err := st.BulkEditPreview(ctx, ids, edit, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(revealed) != 2 {
		t.Fatalf("revealed preview = %d items, want 2", len(revealed))
	}
	_ = now
}

func TestBulkEditApplyAndUndoRoundTrip(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC)

	// Prior state: media-alpha has genre=old + a year only it carries;
	// media-archive has no genre (the edit creates it).
	if err := st.SetMediaFields(ctx, "media-alpha", []MediaField{
		{Name: "genre", Type: EntityFieldText, Text: "old"},
		{Name: "rating", Type: EntityFieldNumber, Number: 5},
	}); err != nil {
		t.Fatal(err)
	}

	edit := []MediaField{{Name: "genre", Type: EntityFieldText, Text: "new"}}
	ids := []MediaID{"media-alpha", "media-archive"}

	batch, err := st.BulkEditApply(ctx, ids, edit, LibraryFilter{Revealed: true, AllLibraries: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if batch.ID == "" || batch.Kind != opKindBulkEdit || batch.Affected != 2 {
		t.Fatalf("apply batch = %#v, want bulk-edit with 2 affected and an id", batch)
	}

	// Both items now hold the new value; the untouched rating survives.
	if got := fieldByName(t, st, "media-alpha"); got["genre"].Text != "new" || got["rating"].Number != 5 {
		t.Fatalf("media-alpha after apply = %#v", got)
	}
	if got := fieldByName(t, st, "media-archive")["genre"].Text; got != "new" {
		t.Fatalf("media-archive genre after apply = %q, want new", got)
	}

	// Undo restores: media-alpha back to genre=old (rating still intact), and
	// media-archive's genre (created by the edit) is removed.
	undone, err := st.UndoBatch(ctx, batch.ID, LibraryFilter{Revealed: true, AllLibraries: true}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if undone.Affected != 2 {
		t.Fatalf("undo affected = %d, want 2", undone.Affected)
	}
	if got := fieldByName(t, st, "media-alpha"); got["genre"].Text != "old" || got["rating"].Number != 5 {
		t.Fatalf("media-alpha after undo = %#v, want genre=old rating=5", got)
	}
	if _, present := fieldByName(t, st, "media-archive")["genre"]; present {
		t.Fatal("media-archive genre survived undo, want removed (edit created it)")
	}

	// A second undo of the same batch is refused.
	if _, err := st.UndoBatch(ctx, batch.ID, LibraryFilter{Revealed: true, AllLibraries: true}, now.Add(2*time.Minute)); err == nil ||
		!strings.Contains(err.Error(), "already undone") {
		t.Fatalf("double undo err = %v, want already-undone", err)
	}
}

func TestBulkEditApplyValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// An empty id list is not an error — there is simply nothing visible to
	// edit, so no batch is written.
	empty, err := st.BulkEditApply(ctx, nil, []MediaField{{Name: "g", Type: EntityFieldText, Text: "x"}}, LibraryFilter{AllLibraries: true}, now)
	if err != nil {
		t.Fatalf("empty id list errored: %v", err)
	}
	if empty.ID != "" || empty.Affected != 0 {
		t.Fatalf("empty id list batch = %#v, want empty", empty)
	}

	// No fields is a validation error, not a silent no-op.
	if _, err := st.BulkEditApply(ctx, []MediaID{"media-alpha"}, nil, LibraryFilter{Revealed: true, AllLibraries: true}, now); err == nil {
		t.Fatal("apply accepted an empty field set")
	}

	// All-hidden id set writes no batch and reports zero affected.
	batch, err := st.BulkEditApply(ctx, []MediaID{"media-archive"},
		[]MediaField{{Name: "g", Type: EntityFieldText, Text: "x"}}, LibraryFilter{AllLibraries: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if batch.ID != "" || batch.Affected != 0 {
		t.Fatalf("all-hidden apply = %#v, want empty batch", batch)
	}
}

func TestBulkEditRejectsEncryptedMediaWithoutPlaintextLeaks(t *testing.T) {
	st, media := seededEncryptedMedia(t, "bulk-edit-secret")
	defer st.Close()
	ctx := context.Background()
	field := MediaField{Name: "caption", Type: EntityFieldText, Text: "protected prior value"}
	if err := st.SetMediaFields(ctx, media.ID, []MediaField{field}); err != nil {
		t.Fatal(err)
	}

	edit := []MediaField{{Name: "caption", Type: EntityFieldText, Text: "plaintext replacement"}}
	for name, call := range map[string]func() error{
		"preview": func() error {
			_, err := st.BulkEditPreview(ctx, []MediaID{media.ID}, edit, LibraryFilter{AllLibraries: true})
			return err
		},
		"apply": func() error {
			_, err := st.BulkEditApply(ctx, []MediaID{media.ID}, edit, LibraryFilter{AllLibraries: true}, time.Now().UTC())
			return err
		},
	} {
		if err := call(); err == nil || err.Error() != encryptedBulkEditError {
			t.Fatalf("%s error = %v, want %q", name, err, encryptedBulkEditError)
		}
	}

	for _, table := range mediaFieldTables {
		var count int
		if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE media_id = ?`, media.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("plaintext %s rows = %d, want 0", table, count)
		}
	}
	var batches, items, events int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM op_batch WHERE kind = ?`, opKindBulkEdit).Scan(&batches); err != nil {
		t.Fatal(err)
	}
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM op_batch_item WHERE media_id = ?`, media.ID).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_outbox WHERE aggregate_id = ?`, media.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if batches != 0 || items != 0 || events != 0 {
		t.Fatalf("plaintext leaks: batches=%d items=%d events=%d", batches, items, events)
	}
	if got := fieldByName(t, st, media.ID)["caption"].Text; got != field.Text {
		t.Fatalf("protected field = %q, want unchanged %q", got, field.Text)
	}
	if err := st.UpdateLibraryRestricted(ctx, media.LibraryID, true); err != nil {
		t.Fatal(err)
	}
	for name, ids := range map[string][]MediaID{"hidden encrypted": {media.ID}, "unknown": {"media-unknown"}} {
		changes, err := st.BulkEditPreview(ctx, ids, edit, LibraryFilter{AllLibraries: true})
		if err != nil || len(changes) != 0 {
			t.Fatalf("%s preview = %#v, %v; want hidden empty result", name, changes, err)
		}
		batch, err := st.BulkEditApply(ctx, ids, edit, LibraryFilter{AllLibraries: true}, time.Now().UTC())
		if err != nil || batch.ID != "" || batch.Affected != 0 {
			t.Fatalf("%s apply = %#v, %v; want hidden empty result", name, batch, err)
		}
	}

	// Simulate a legacy/corrupt plaintext journal reference. Undo must reject it
	// before loading the prior-state payload or emitting an update event.
	if _, err := st.catalogW.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `INSERT INTO op_batch(id, kind, created_at_ms) VALUES ('batch-encrypted', ?, 1)`, opKindBulkEdit); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `INSERT INTO op_batch_item(batch_id, seq, media_id, prior_state) VALUES ('batch-encrypted', 0, ?, '{"edited":["caption"],"fields":[]}')`, media.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UndoBatch(ctx, "batch-encrypted", LibraryFilter{Revealed: true, AllLibraries: true}, time.Now().UTC()); err == nil || err.Error() != encryptedBulkEditError {
		t.Fatalf("undo error = %v, want %q", err, encryptedBulkEditError)
	}
	var undoneAt int64
	if err := st.catalogR.QueryRowContext(ctx, `SELECT undone_at_ms FROM op_batch WHERE id = 'batch-encrypted'`).Scan(&undoneAt); err != nil {
		t.Fatal(err)
	}
	if undoneAt != 0 {
		t.Fatalf("rejected encrypted undo marked batch undone at %d", undoneAt)
	}
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_outbox WHERE aggregate_id = ?`, media.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("rejected encrypted undo emitted %d plaintext events", events)
	}
}

func TestUndoBatchUnknownBatch(t *testing.T) {
	st := openSeeded(t)
	if _, err := st.UndoBatch(context.Background(), "batch-nope", LibraryFilter{Revealed: true, AllLibraries: true}, time.Now().UTC()); err == nil ||
		!strings.Contains(err.Error(), "batch not found") {
		t.Fatalf("unknown batch err = %v, want not-found", err)
	}
	if _, err := st.UndoBatch(context.Background(), "", LibraryFilter{AllLibraries: true}, time.Now().UTC()); err == nil {
		t.Fatal("undo accepted empty batch id")
	}
}

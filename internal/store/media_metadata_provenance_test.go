package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestMediaMetadataProvenanceUpsertAndCascade(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	if _, ok, err := st.MediaMetadataProvenance(ctx, "media-alpha"); err != nil || ok {
		t.Fatalf("provenance before identify = ok %v, err %v; want none", ok, err)
	}
	if err := st.SetMediaMetadataProvenance(ctx, "media-alpha", "demo", "lookup", "cand-1"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.MediaMetadataProvenance(ctx, "media-alpha")
	if err != nil || !ok {
		t.Fatalf("provenance = ok %v, err %v", ok, err)
	}
	if got.PluginID != "demo" || got.ScraperID != "lookup" || got.CandidateID != "cand-1" {
		t.Fatalf("provenance = %#v, want demo/lookup", got)
	}

	if err := st.SetMediaMetadataProvenance(ctx, "media-alpha", "other", "second", "cand-1"); err != nil {
		t.Fatal(err)
	}
	got, _, err = st.MediaMetadataProvenance(ctx, "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if got.PluginID != "other" || got.ScraperID != "second" {
		t.Fatalf("provenance after re-identify = %#v, want other/second", got)
	}

	if err := st.SetMediaMetadataProvenance(ctx, "media-missing", "demo", "lookup", "cand-1"); !errors.As(err, new(ValidationError)) {
		t.Fatalf("missing media err = %v, want validation error", err)
	}
	if err := st.SetMediaMetadataProvenance(ctx, "media-alpha", " ", "lookup", "cand-1"); !errors.As(err, new(ValidationError)) {
		t.Fatalf("blank plugin err = %v, want validation error", err)
	}

	if _, err := st.catalogW.ExecContext(ctx, `DELETE FROM media_metadata_provenance WHERE media_id = 'media-alpha'`); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaMetadataProvenance(ctx, "media-alpha", "demo", "lookup", "cand-1"); err != nil {
		t.Fatal(err)
	}
	var onDelete string
	if err := st.catalogR.QueryRowContext(ctx,
		`SELECT on_delete FROM pragma_foreign_key_list('media_metadata_provenance') WHERE "table" = 'media'`).Scan(&onDelete); err != nil {
		t.Fatal(err)
	}
	if onDelete != "CASCADE" {
		t.Fatalf("media FK on_delete = %q, want CASCADE", onDelete)
	}
}

func TestApplyMediaIdentificationRollsBackAfterMetadataWrite(t *testing.T) {
	ctx := t.Context()
	dataDir := migratedDataDir(t)
	st, err := Open(ctx, dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	const id = MediaID("media-alpha")
	original := MediaMetadataProvenance{PluginID: "demo", ScraperID: "lookup", CandidateID: "original"}
	originalFields := []MediaField{{Name: "providerCode", Type: EntityFieldText, Text: "original"}}
	originalTitle := "Original match title"
	if err := st.ApplyMediaIdentification(ctx, id, MediaMetadata{Title: &originalTitle}, original, originalFields); err != nil {
		t.Fatal(err)
	}
	manualTitle := "Manual title"
	if err := st.UpdateMediaMetadata(ctx, id, MediaMetadata{Title: &manualTitle, Source: MutationSourceManual}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TRIGGER reject_replacement_field BEFORE INSERT ON media_field_text
		WHEN NEW.media_id = 'media-alpha' AND NEW.field = 'providerCode' AND NEW.value = 'replacement'
		BEGIN
			SELECT CASE
				WHEN (SELECT title FROM media WHERE id = NEW.media_id) = 'Replacement title'
				AND (SELECT candidate_id FROM media_metadata_provenance WHERE media_id = NEW.media_id) = 'replacement'
				THEN RAISE(ABORT, 'injected failure after metadata and candidate writes')
				ELSE RAISE(ABORT, 'replacement did not reach the expected write stage')
			END;
		END`); err != nil {
		t.Fatal(err)
	}
	replacementTitle := "Replacement title"
	replacement := MediaMetadataProvenance{PluginID: "demo", ScraperID: "lookup", CandidateID: "replacement"}
	replacementFields := []MediaField{{Name: "providerCode", Type: EntityFieldText, Text: "replacement"}}
	metadata := MediaMetadata{Title: &replacementTitle, ClearLocks: []string{"title"}}
	if err := st.ApplyMediaIdentification(ctx, id, metadata, replacement, replacementFields); err == nil ||
		!strings.Contains(err.Error(), "injected failure after metadata and candidate writes") {
		t.Fatalf("identification error = %v, want injected plugin-field failure after metadata and provenance", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	filter := LibraryFilter{AllLibraries: true, Revealed: true}
	media, err := reopened.Media(ctx, id, filter)
	if err != nil || media.Title != manualTitle {
		t.Fatalf("title after reopen = %q, err %v; want %q", media.Title, err, manualTitle)
	}
	locks, err := reopened.MediaFieldLocks(ctx, id, filter)
	if err != nil || !slices.Contains(locks, MediaFieldLock{Field: "title", Locked: true}) {
		t.Fatalf("locks after reopen = %v, err %v; want locked title", locks, err)
	}
	got, ok, err := reopened.MediaMetadataProvenance(ctx, id)
	if err != nil || !ok || got != original {
		t.Fatalf("provenance after reopen = %+v, found %v, err %v; want %+v", got, ok, err, original)
	}
	fields, err := reopened.MediaFields(ctx, id, filter)
	if err != nil || !slices.Equal(fields, originalFields) {
		t.Fatalf("plugin fields after reopen = %+v, err %v; want %+v", fields, err, originalFields)
	}
}

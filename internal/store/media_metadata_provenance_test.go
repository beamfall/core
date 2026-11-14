package store

import (
	"context"
	"errors"
	"testing"
)

func TestMediaMetadataProvenanceUpsertAndCascade(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	if _, ok, err := st.MediaMetadataProvenance(ctx, "media-alpha"); err != nil || ok {
		t.Fatalf("provenance before identify = ok %v, err %v; want none", ok, err)
	}
	if err := st.SetMediaMetadataProvenance(ctx, "media-alpha", "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.MediaMetadataProvenance(ctx, "media-alpha")
	if err != nil || !ok {
		t.Fatalf("provenance = ok %v, err %v", ok, err)
	}
	if got.PluginID != "demo" || got.ScraperID != "lookup" {
		t.Fatalf("provenance = %#v, want demo/lookup", got)
	}

	if err := st.SetMediaMetadataProvenance(ctx, "media-alpha", "other", "second"); err != nil {
		t.Fatal(err)
	}
	got, _, err = st.MediaMetadataProvenance(ctx, "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if got.PluginID != "other" || got.ScraperID != "second" {
		t.Fatalf("provenance after re-identify = %#v, want other/second", got)
	}

	if err := st.SetMediaMetadataProvenance(ctx, "media-missing", "demo", "lookup"); !errors.As(err, new(ValidationError)) {
		t.Fatalf("missing media err = %v, want validation error", err)
	}
	if err := st.SetMediaMetadataProvenance(ctx, "media-alpha", " ", "lookup"); !errors.As(err, new(ValidationError)) {
		t.Fatalf("blank plugin err = %v, want validation error", err)
	}

	if _, err := st.catalogW.ExecContext(ctx, `DELETE FROM media_metadata_provenance WHERE media_id = 'media-alpha'`); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaMetadataProvenance(ctx, "media-alpha", "demo", "lookup"); err != nil {
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

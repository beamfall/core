package app

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

func TestNewLowersActivatedMediaFieldSearchPolicies(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true
	bundle := testBundle("photos")
	bundle.MediaFields = []plugin.FieldSpec{
		{Name: "place", Type: "text", Searchable: true},
		{Name: "note", Type: "text"},
	}
	app, err := New(ctx, cfg, nil, []plugin.Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	library, err := app.store.CreateLibrary(ctx, "Photos", false, "photos", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := app.store.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: library.ID, Path: "/photos/image.jpg", Title: "Image", Container: "jpg", Codec: "jpeg",
		Class: store.MediaClassImage, ContentFingerprint: "sha256:media-search-policy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetMediaFields(ctx, media.ID, []store.MediaField{
		{Name: "place", Type: store.EntityFieldText, Text: "Toronto Harbour"},
		{Name: "note", Type: store.EntityFieldText, Text: "Private Needle"},
	}); err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllLibraries: true}
	hits, err := app.store.SearchMedia(ctx, filter, "harbour", 10)
	if err != nil || len(hits) != 1 || hits[0].ID != media.ID {
		t.Fatalf("searchable field hits = %v, err=%v", hits, err)
	}
	hits, err = app.store.SearchMedia(ctx, filter, "needle", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("undeclared-searchable field leaked hits: %v", hits)
	}
}

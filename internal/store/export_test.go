package store

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestExportCatalogChunksMediaCuration(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const count = catalogExportMediaBatchSize + 1

	tx, err := st.catalogW.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := batchInsert(ctx, tx,
		"INSERT INTO media(id, library_id, title, duration_seconds, container, codec, media_class) VALUES ",
		7, count, func(i int) []any {
			return []any{
				fmt.Sprintf("media-export-%04d", i), "library-local", fmt.Sprintf("Export %04d", i),
				60, "mp4", "h264", MediaClassVideo,
			}
		}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	first := MediaID("media-export-0000")
	last := MediaID(fmt.Sprintf("media-export-%04d", count-1))
	for i, id := range []MediaID{first, last} {
		fields := []MediaField{
			{Name: "text", Type: EntityFieldText, Text: "value"},
			{Name: "number", Type: EntityFieldNumber, Number: float64(i + 1)},
			{Name: "flag", Type: EntityFieldFlag, Flag: true},
			{Name: "date", Type: EntityFieldDate, Date: time.Date(2026, time.July, 10+i, 0, 0, 0, 0, time.UTC)},
		}
		if err := st.SetMediaFields(ctx, id, fields); err != nil {
			t.Fatalf("SetMediaFields(%s): %v", id, err)
		}
		if _, err := st.CreateMarker(ctx, Marker{
			MediaID: id, Title: "marker-" + string(id), StartSeconds: float64(i), Tags: []string{"beta", "alpha"},
		}, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}, time.Now()); err != nil {
			t.Fatalf("CreateMarker(%s): %v", id, err)
		}
		sourceURL := "https://images.example.test/" + string(id) + ".jpg"
		if err := st.UpsertArtworkCandidate(ctx, id, "poster", sourceURL, "plugin", "scraper"); err != nil {
			t.Fatalf("UpsertArtworkCandidate(%s): %v", id, err)
		}
		if err := st.MarkArtworkCandidateApplied(ctx, ArtworkCandidateID(id, "poster", sourceURL)); err != nil {
			t.Fatalf("MarkArtworkCandidateApplied(%s): %v", id, err)
		}
	}

	export, err := st.ExportCatalog(ctx, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(export.Media), count+1; got != want { // plus seeded media-alpha
		t.Fatalf("exported Media = %d, want %d", got, want)
	}
	byID := make(map[MediaID]ExportedMedia, len(export.Media))
	for _, item := range export.Media {
		byID[item.Media.ID] = item
	}
	for _, id := range []MediaID{first, last} {
		item, ok := byID[id]
		if !ok {
			t.Fatalf("export missing %s across batch boundary", id)
		}
		if got := len(item.Fields); got != 4 {
			t.Fatalf("%s fields = %d, want 4: %#v", id, got, item.Fields)
		}
		if got := []string{item.Fields[0].Name, item.Fields[1].Name, item.Fields[2].Name, item.Fields[3].Name}; !slices.Equal(got, []string{"date", "flag", "number", "text"}) {
			t.Fatalf("%s field order = %v", id, got)
		}
		if got := len(item.Markers); got != 1 || item.Markers[0].Title != "marker-"+string(id) {
			t.Fatalf("%s markers = %#v", id, item.Markers)
		}
		if got := item.Markers[0].Tags; !slices.Equal(got, []string{"alpha", "beta"}) {
			t.Fatalf("%s marker tags = %v", id, got)
		}
		if got := len(item.ArtworkPin); got != 1 || item.ArtworkPin[0].Slot != "poster" {
			t.Fatalf("%s artwork pins = %#v", id, item.ArtworkPin)
		}
	}
}

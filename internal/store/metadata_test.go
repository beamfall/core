package store_test

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func ptr[T any](v T) *T { return &v }

func TestUpdateMediaMetadataAppliesOnlyNonNilFields(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	filter := store.LibraryFilter{Revealed: true, AllLibraries: true}

	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{
		Title:   ptr("Identified Title"),
		Summary: ptr("A proper summary."),
		Year:    ptr(1999),
	}); err != nil {
		t.Fatalf("full update: %v", err)
	}
	media, err := st.Media(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	if media.Title != "Identified Title" || media.Summary != "A proper summary." || media.Year != 1999 {
		t.Fatalf("media after full update = %#v", media)
	}

	// Partial update: only year changes; title and summary stay.
	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{Year: ptr(2001)}); err != nil {
		t.Fatalf("partial update: %v", err)
	}
	media, err = st.Media(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	if media.Title != "Identified Title" || media.Summary != "A proper summary." || media.Year != 2001 {
		t.Fatalf("media after partial update = %#v", media)
	}
}

func TestUpdateMediaMetadataValidation(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()

	if err := st.UpdateMediaMetadata(ctx, "", store.MediaMetadata{Year: ptr(2000)}); err == nil {
		t.Fatal("accepted empty media ID")
	}
	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{}); err == nil {
		t.Fatal("accepted empty metadata")
	}
	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{Title: ptr("  ")}); err == nil {
		t.Fatal("accepted blank title")
	}
	if err := st.UpdateMediaMetadata(ctx, "media-nope", store.MediaMetadata{Year: ptr(2000)}); err == nil {
		t.Fatal("accepted unknown media")
	}
}

// TestMutationProvenanceStamped verifies that UpdateMediaMetadata stamps
// mutated_by = operator by default (the dominant explicit-identification path)
// and that a caller may override it to a specific MutationSource.
func TestMutationProvenanceStamped(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	filter := store.LibraryFilter{Revealed: true, AllLibraries: true}

	// Default (Source == ""): should land as MutationSourceOperator.
	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{
		Title: ptr("Provenance Title"),
	}); err != nil {
		t.Fatalf("UpdateMediaMetadata: %v", err)
	}
	media, err := st.Media(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	if media.MutatedBy != store.MutationSourceOperator {
		t.Fatalf("MutatedBy = %q, want %q", media.MutatedBy, store.MutationSourceOperator)
	}

	// Explicit Source = AI.
	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{
		Year:   ptr(2025),
		Source: store.MutationSourceAI,
	}); err != nil {
		t.Fatalf("UpdateMediaMetadata with AI source: %v", err)
	}
	media, err = st.Media(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	if media.MutatedBy != store.MutationSourceAI {
		t.Fatalf("MutatedBy = %q after AI write, want %q", media.MutatedBy, store.MutationSourceAI)
	}
}

func TestAIMetadataDoesNotOverwriteLockedOrAuthoredFields(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	filter := store.LibraryFilter{Revealed: true, AllLibraries: true}
	title := "Operator Title"
	summary := "Operator summary."
	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{
		Title:   &title,
		Summary: &summary,
	}); err != nil {
		t.Fatalf("operator UpdateMediaMetadata: %v", err)
	}
	aiTitle := "AI Title"
	aiSummary := "AI summary."
	aiYear := 2026
	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{
		Title: &aiTitle, Summary: &aiSummary, Year: &aiYear, Source: store.MutationSourceAI,
	}); err != nil {
		t.Fatalf("AI UpdateMediaMetadata: %v", err)
	}
	media, err := st.Media(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	if media.Title != title || media.Summary != summary {
		t.Fatalf("AI overwrote locked fields: title=%q summary=%q", media.Title, media.Summary)
	}
}

func TestADR0072ScalarOverwriteMatrixForImportParserEnrichmentAndUserLocks(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	filter := store.LibraryFilter{Revealed: true, AllLibraries: true}
	imported := store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/matrix/item.mp4",
		Title:              "parser title",
		Summary:            "import summary",
		Year:               2001,
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-adr0072-matrix",
	}
	media, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatalf("initial import: %v", err)
	}

	imported.Title = "parser title refreshed"
	imported.Summary = "import summary refreshed"
	imported.Year = 2002
	refreshed, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatalf("unlocked refresh: %v", err)
	}
	if refreshed.Title != imported.Title || refreshed.Summary != imported.Summary || refreshed.Year != imported.Year {
		t.Fatalf("unlocked scanner/parser/import refresh = %#v", refreshed)
	}

	aiTitle, aiSummary, aiYear := "ai title", "ai summary", 2003
	if err := st.UpdateMediaMetadata(ctx, media.ID, store.MediaMetadata{
		Title: &aiTitle, Summary: &aiSummary, Year: &aiYear, Source: store.MutationSourceAI,
	}); err != nil {
		t.Fatalf("AI lower-precedence write: %v", err)
	}
	got, err := st.Media(ctx, media.ID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != imported.Title || got.Summary != imported.Summary || got.Year != imported.Year {
		t.Fatalf("AI overwrote non-empty importer/parser fields: %#v", got)
	}

	userTitle, userSummary, userYear := "user title", "user summary", 2004
	if err := st.UpdateMediaMetadata(ctx, media.ID, store.MediaMetadata{
		Title: &userTitle, Summary: &userSummary, Year: &userYear,
	}); err != nil {
		t.Fatalf("user/scraper apply: %v", err)
	}
	imported.Title = "parser blocked"
	imported.Summary = "import blocked"
	imported.Year = 2005
	if _, _, err := st.ImportMediaFile(ctx, imported); err != nil {
		t.Fatalf("locked refresh: %v", err)
	}
	got, err = st.Media(ctx, media.ID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != userTitle || got.Summary != userSummary || got.Year != userYear {
		t.Fatalf("locked user fields were overwritten: %#v", got)
	}

	if _, err := st.SetMediaFieldLock(ctx, media.ID, "summary", false, filter); err != nil {
		t.Fatalf("unlock summary: %v", err)
	}
	imported.Summary = "import after unlock"
	if _, _, err := st.ImportMediaFile(ctx, imported); err != nil {
		t.Fatalf("unlocked summary refresh: %v", err)
	}
	got, err = st.Media(ctx, media.ID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != userTitle || got.Summary != imported.Summary || got.Year != userYear {
		t.Fatalf("summary unlock matrix result = %#v", got)
	}

	empty, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/matrix/empty.mp4",
		Title:              "empty metadata item",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-adr0072-empty",
	})
	if err != nil {
		t.Fatalf("empty import: %v", err)
	}
	if err := st.UpdateMediaMetadata(ctx, empty.ID, store.MediaMetadata{
		Summary: &aiSummary, Year: &aiYear, Source: store.MutationSourceAI,
	}); err != nil {
		t.Fatalf("AI gap fill: %v", err)
	}
	got, err = st.Media(ctx, empty.ID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != aiSummary || got.Year != aiYear || got.MutatedBy != store.MutationSourceAI {
		t.Fatalf("AI gap fill = %#v", got)
	}
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/matrix/empty.mp4",
		Title:              "empty metadata item",
		Summary:            "import beats ai",
		Year:               2006,
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-adr0072-empty",
	}); err != nil {
		t.Fatalf("import over AI: %v", err)
	}
	got, err = st.Media(ctx, empty.ID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "import beats ai" || got.Year != 2006 {
		t.Fatalf("import did not refresh machine-derived fields: %#v", got)
	}
}

// The 0006 FTS trigger fires on title UPDATEs, so an identified title is
// immediately searchable; summaries are deliberately outside FTS (0011).
func TestUpdateMediaMetadataTitleStaysSearchable(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	filter := store.LibraryFilter{AllLibraries: true}

	if err := st.UpdateMediaMetadata(ctx, "media-alpha", store.MediaMetadata{
		Title:   ptr("Renamed Feature"),
		Summary: ptr("Unsearchable summary text"),
	}); err != nil {
		t.Fatal(err)
	}
	hits, err := st.SearchMedia(ctx, filter, "renamed", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "media-alpha" {
		t.Fatalf("search for new title = %#v, want media-alpha", hits)
	}
	hits, err = st.SearchMedia(ctx, filter, "sample", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("search for old title = %#v, want none", hits)
	}
	hits, err = st.SearchMedia(ctx, filter, "unsearchable", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("summary text matched FTS = %#v, want none (summary is not indexed)", hits)
	}
}

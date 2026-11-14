package store

import (
	"context"
	"testing"
)

// TestSearchRowDetailsHonoursLibraryVisibility pins that a search row's display
// detail is served only for a Library the caller's scope can see.
func TestSearchRowDetailsHonoursLibraryVisibility(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	items := []SourceItem{{ID: "media-alpha", Kind: "media"}, {ID: "media-archive", Kind: "media"}, {ID: "missing", Kind: "entity"}}
	allowed := []LibraryID{"library-local", "library-archive"}

	hidden, err := st.SearchRowDetails(ctx, LibraryFilter{AllowedIDs: allowed}, items)
	if err != nil {
		t.Fatal(err)
	}
	if hidden[0].LibraryID != "library-local" || hidden[0].LibraryTitle == "" || hidden[0].MediaClass != "video" {
		t.Errorf("visible row detail = %+v, want library-local with a title and class", hidden[0])
	}
	if hidden[1] != (SearchRowDetail{}) || hidden[2] != (SearchRowDetail{}) {
		t.Errorf("restricted and missing rows = %+v, %+v, want zero detail", hidden[1], hidden[2])
	}

	revealed, err := st.SearchRowDetails(ctx, LibraryFilter{AllowedIDs: allowed, Revealed: true}, items)
	if err != nil {
		t.Fatal(err)
	}
	if revealed[1].LibraryID != "library-archive" {
		t.Errorf("revealed row detail = %+v, want library-archive", revealed[1])
	}
}

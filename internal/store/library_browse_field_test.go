package store

import (
	"context"
	"slices"
	"testing"
)

// TestLibraryBrowseSortsAndSearchesByMediaField covers CORE-3362: a field sort
// orders by a text Media field then title, puts items without a value last in
// both directions, letter-indexes only the valued items, and a text filter
// also matches the named fields.
func TestLibraryBrowseSortsAndSearchesByMediaField(t *testing.T) {
	st := openDemo(t)
	ctx := context.Background()
	tx, err := st.catalogW.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for id, artist := range map[MediaID]string{"media-demo-steel": "Zed", "media-demo-bunny": "abba", "media-demo-sintel": "Abba"} {
		if err := insertMediaFields(ctx, tx, id, []MediaField{{Name: "artist", Type: EntityFieldText, Text: artist}}); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ids := func(result LibraryBrowseResult) []MediaID {
		out := make([]MediaID, len(result.Items))
		for i, item := range result.Items {
			out[i] = item.ID
		}
		return out
	}

	asc := mustLibraryBrowse(t, st, LibraryBrowseQuery{Sort: LibraryBrowseSortField, SortField: "artist", Limit: 200})
	if got := ids(asc)[:3]; !slices.Equal(got, []MediaID{"media-demo-bunny", "media-demo-sintel", "media-demo-steel"}) {
		t.Fatalf("ascending head = %v", got)
	}
	blanks := ids(mustLibraryBrowse(t, st, LibraryBrowseQuery{Limit: 200}))
	blanks = slices.DeleteFunc(blanks, func(id MediaID) bool {
		return id == "media-demo-bunny" || id == "media-demo-sintel" || id == "media-demo-steel"
	})
	if got := ids(asc)[3:]; !slices.Equal(got, blanks) {
		t.Fatalf("items without an artist = %v, want title order %v", got, blanks)
	}
	wantLetters := []LibraryBrowseLetter{{Letter: "A", Offset: 0, Count: 2}, {Letter: "Z", Offset: 2, Count: 1}}
	if !slices.Equal(asc.Letters, wantLetters) {
		t.Fatalf("letters = %v, want %v", asc.Letters, wantLetters)
	}

	desc := mustLibraryBrowse(t, st, LibraryBrowseQuery{Sort: LibraryBrowseSortField, SortField: "artist", Descending: true, Limit: 200})
	if got := ids(desc)[:3]; !slices.Equal(got, []MediaID{"media-demo-steel", "media-demo-sintel", "media-demo-bunny"}) {
		t.Fatalf("descending head = %v", got)
	}
	if got := ids(desc)[3:]; !slices.Equal(got, blanks) {
		t.Fatalf("descending blanks = %v, want them last in title order %v", got, blanks)
	}

	for text, want := range map[string][]MediaID{
		"abba":        {"media-demo-bunny", "media-demo-sintel"},
		"ABBA sintel": {"media-demo-sintel"},
		"zed bunny":   {},
	} {
		got := mustLibraryBrowse(t, st, LibraryBrowseQuery{Text: text, TextFields: []string{"artist"}})
		if !slices.Equal(ids(got), want) {
			t.Errorf("text %q = %v, want %v", text, ids(got), want)
		}
	}
	if got := mustLibraryBrowse(t, st, LibraryBrowseQuery{Text: "abba"}); got.Total != 0 {
		t.Errorf("text without TextFields matched %v; fields are opt-in", ids(got))
	}
}

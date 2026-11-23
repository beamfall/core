package store

import (
	"context"
	"fmt"
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

// TestLibraryBrowseFiltersAndFacetsByMediaField covers CORE-3369: field facets
// count each value over the whole Library, most items first, and field filters
// keep only the items holding every named value.
func TestLibraryBrowseFiltersAndFacetsByMediaField(t *testing.T) {
	st := openDemo(t)
	ctx := context.Background()
	tx, err := st.catalogW.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for id, fields := range map[MediaID][]MediaField{
		"media-demo-steel":  {{Name: "class", Type: EntityFieldText, Text: "cdg"}, {Name: "singers", Type: EntityFieldText, Text: "duet"}},
		"media-demo-bunny":  {{Name: "class", Type: EntityFieldText, Text: "lrc"}, {Name: "singers", Type: EntityFieldText, Text: "duet"}},
		"media-demo-sintel": {{Name: "class", Type: EntityFieldText, Text: "cdg"}},
	} {
		if err := insertMediaFields(ctx, tx, id, fields); err != nil {
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
		slices.Sort(out)
		return out
	}

	all := mustLibraryBrowse(t, st, LibraryBrowseQuery{FacetFields: []string{"class", "singers"}, FieldFilters: map[string]string{"singers": "duet"}, Limit: 200})
	want := []LibraryBrowseFieldFacet{
		{Field: "class", Values: []string{"cdg", "lrc"}, Counts: []int{2, 1}},
		{Field: "singers", Values: []string{"duet"}, Counts: []int{2}},
	}
	if len(all.Facets.Fields) != len(want) {
		t.Fatalf("field facets = %+v, want %+v", all.Facets.Fields, want)
	}
	for i, facet := range all.Facets.Fields {
		if facet.Field != want[i].Field || !slices.Equal(facet.Values, want[i].Values) || !slices.Equal(facet.Counts, want[i].Counts) {
			t.Errorf("facet %d = %+v, want %+v", i, facet, want[i])
		}
	}
	if got := ids(all); !slices.Equal(got, []MediaID{"media-demo-bunny", "media-demo-steel"}) {
		t.Errorf("duet filter = %v", got)
	}
	both := mustLibraryBrowse(t, st, LibraryBrowseQuery{FieldFilters: map[string]string{"singers": "duet", "class": "cdg"}})
	if got := ids(both); !slices.Equal(got, []MediaID{"media-demo-steel"}) {
		t.Errorf("duet+cdg filter = %v", got)
	}
	if got := mustLibraryBrowse(t, st, LibraryBrowseQuery{FieldFilters: map[string]string{"class": "CDG"}}); got.Total != 0 {
		t.Errorf("filter matched a different case: %v", ids(got))
	}
	if got := mustLibraryBrowse(t, st, LibraryBrowseQuery{}); len(got.Facets.Fields) != 0 {
		t.Errorf("unrequested field facets = %+v", got.Facets.Fields)
	}
}

// TestLibraryBrowseFieldFacetKeepsTheMostCommonValues: a free-text field
// cannot make a facet Library-sized; it reports the most common values only.
func TestLibraryBrowseFieldFacetKeepsTheMostCommonValues(t *testing.T) {
	counts := map[string]int{"duet": 9, "solo": 9, "trio": 4}
	for i := range maxLibraryBrowseFieldFacetValues + 20 {
		counts[fmt.Sprintf("free-%03d", i)] = 1
	}
	values, counted := topLibraryBrowseFieldValues(counts)
	if len(values) != maxLibraryBrowseFieldFacetValues || len(counted) != len(values) {
		t.Fatalf("kept %d values, %d counts; want %d", len(values), len(counted), maxLibraryBrowseFieldFacetValues)
	}
	if !slices.Equal(values[:4], []string{"duet", "solo", "trio", "free-000"}) || !slices.Equal(counted[:3], []int{9, 9, 4}) {
		t.Errorf("top values = %v %v, want most items first, then by value", values[:4], counted[:4])
	}
}

package graph

import (
	"slices"
	"testing"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestLibraryItemsFieldSortNeedsADeclaredSortableTextField covers CORE-3362:
// "field:<name>" sorts by a plugin's Sortable text Media field, anything else
// is refused rather than answered in title order, and the text filter also
// reads the plugin's Searchable text fields.
func TestLibraryItemsFieldSortNeedsADeclaredSortableTextField(t *testing.T) {
	bundle := plugin.Bundle{MediaFields: []plugin.FieldSpec{
		{Name: "artist", Type: "text", Searchable: true, Sortable: true},
		{Name: "disc", Type: "text", Searchable: true},
		{Name: "tempo", Type: "number", Searchable: true, Sortable: true},
	}}
	parse := func(sort string) (store.LibraryBrowseQuery, error) {
		return libraryItemsQuery(auth.Scope{}, request{Variables: map[string]any{"libraryID": "lib", "sort": sort}})
	}
	for _, bad := range []string{"field", "field:", "field:  "} {
		if _, err := parse(bad); err == nil {
			t.Errorf("sort %q parsed; want a client error", bad)
		}
	}
	for sort, ok := range map[string]bool{"field:artist": true, "field:disc": false, "field:tempo": false, "field:ghost": false} {
		query, err := parse(sort)
		if err != nil {
			t.Fatalf("sort %q: %v", sort, err)
		}
		if query.Sort != store.LibraryBrowseSortField {
			t.Fatalf("sort %q parsed to %q", sort, query.Sort)
		}
		err = libraryBrowseMediaFields(bundle, &query)
		if (err == nil) != ok {
			t.Errorf("sort %q: err = %v, want accepted %v", sort, err, ok)
		}
	}
	query, err := parse("title")
	if err != nil {
		t.Fatal(err)
	}
	if err := libraryBrowseMediaFields(bundle, &query); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(query.TextFields, []string{"artist", "disc"}) {
		t.Errorf("text fields = %v, want the Searchable text fields", query.TextFields)
	}
	if err := libraryBrowseMediaFields(plugin.Bundle{}, &store.LibraryBrowseQuery{Sort: store.LibraryBrowseSortField, SortField: "artist"}); err == nil {
		t.Error("a plugin-less Library accepted a field sort")
	}
}

// TestLibraryItemsFieldSortOnAnUnseenLibraryAnswersEmpty: which fields sort
// describes a Library's plugin, so a Library the caller cannot see, whether
// hidden or unknown, answers an empty page rather than a sort error.
func TestLibraryItemsFieldSortOnAnUnseenLibraryAnswersEmpty(t *testing.T) {
	handler := newTestHandlerWithStore(storetest.Open(t))
	scope := auth.Scope{ProfileID: "profile-default", LibraryIDs: []store.LibraryID{"library-local"}}
	for _, sort := range []string{"field:artist", "field:ghost"} {
		resp, err := handler.libraryItems(t.Context(), scope, request{Variables: map[string]any{
			"libraryID": "library-hidden", "sort": sort,
		}})
		if err != nil {
			t.Fatalf("sort %q on an unseen Library: %v, want an empty page", sort, err)
		}
		if resp.Total != 0 || len(resp.Items) != 0 {
			t.Fatalf("sort %q on an unseen Library = %d items, want none", sort, resp.Total)
		}
	}
}

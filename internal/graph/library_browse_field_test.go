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

// TestLibraryItemsFieldFiltersNeedADeclaredFilterableTextField covers
// CORE-3369: fieldFilters and facetFields take only a plugin's Filterable text
// Media fields that are not Sensitive, a malformed, blank or oversized filter
// is refused rather than dropped, and repeated facet fields are reported once.
func TestLibraryItemsFieldFiltersNeedADeclaredFilterableTextField(t *testing.T) {
	bundle := plugin.Bundle{MediaFields: []plugin.FieldSpec{
		{Name: "class", Type: "text", Filterable: true},
		{Name: "singers", Type: "text", Filterable: true},
		{Name: "lyrics-sync", Type: "text"},
		{Name: "bpm", Type: "number", Filterable: true},
		{Name: "owner-note", Type: "text", Filterable: true, Sensitive: true},
	}}
	parse := func(vars map[string]any) (store.LibraryBrowseQuery, error) {
		vars["libraryID"] = "lib"
		req := request{Variables: vars}
		query, err := libraryItemsQuery(auth.Scope{}, req)
		if err != nil {
			return query, err
		}
		return query, libraryItemsFieldQuery(req, &query)
	}
	for name, vars := range map[string]map[string]any{
		"blank value":     {"fieldFilters": map[string]any{"class": " "}},
		"blank field":     {"fieldFilters": map[string]any{" ": "cdg"}},
		"non-string":      {"fieldFilters": map[string]any{"class": 1}},
		"padded field":    {"fieldFilters": map[string]any{" class": "cdg"}},
		"padded value":    {"fieldFilters": map[string]any{"class": " cdg"}},
		"list filters":    {"fieldFilters": []any{"class"}},
		"text facets":     {"facetFields": "class"},
		"blank facet":     {"facetFields": []any{"class", " "}},
		"padded facet":    {"facetFields": []any{"class "}},
		"number facet":    {"facetFields": []any{1}},
		"too many facets": {"facetFields": []any{"a", "b", "c", "d", "e", "f", "g", "h", "i"}},
		"too many": {"fieldFilters": map[string]any{
			"a": "1", "b": "1", "c": "1", "d": "1", "e": "1", "f": "1", "g": "1", "h": "1", "i": "1",
		}},
	} {
		if _, err := parse(vars); err == nil {
			t.Errorf("%s parsed; want a client error", name)
		}
	}
	for name, tc := range map[string]struct {
		vars map[string]any
		ok   bool
	}{
		"filterable filter":   {map[string]any{"fieldFilters": map[string]any{"class": "cdg", "singers": "duet"}}, true},
		"filterable facets":   {map[string]any{"facetFields": []any{"class", "singers", "class"}}, true},
		"unfilterable filter": {map[string]any{"fieldFilters": map[string]any{"lyrics-sync": "word"}}, false},
		"number filter":       {map[string]any{"fieldFilters": map[string]any{"bpm": "120"}}, false},
		"unknown facet":       {map[string]any{"facetFields": []any{"ghost"}}, false},
		"sensitive filter":    {map[string]any{"fieldFilters": map[string]any{"owner-note": "x"}}, false},
		"sensitive facet":     {map[string]any{"facetFields": []any{"owner-note"}}, false},
		"null arguments":      {map[string]any{"fieldFilters": nil, "facetFields": nil}, true},
	} {
		query, err := parse(tc.vars)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		err = libraryBrowseMediaFields(bundle, &query)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want accepted %v", name, err, tc.ok)
		}
		if name == "filterable facets" && !slices.Equal(query.FacetFields, []string{"class", "singers"}) {
			t.Errorf("facet fields = %v, want each once in request order", query.FacetFields)
		}
	}
}

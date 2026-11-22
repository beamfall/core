package scrape

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/plugin"
)

// itemLookupServer serves a series record and one episode record, recording
// the paths it was asked for.
func itemLookupServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/tv/42":
			fmt.Fprint(w, `{"name":"Harbor","overview":"Series overview.","first_air_date":"2008-01-20","poster_path":"/series.jpg",
				"seasons":[{"season_number":0,"poster_path":"/specials.jpg"},{"season_number":2,"overview":"Second.","air_date":"2009-03-08","poster_path":"/s2.jpg"}]}`)
		case "/tv/42/season/2/episode/5":
			fmt.Fprint(w, `{"name":"Pilot Two","overview":"Episode overview.","air_date":"2009-04-12","still_path":"/still.jpg"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

func itemLookupSpec(endpoint string) plugin.ScraperSpec {
	return plugin.ScraperSpec{
		ID:                 "tv",
		Kind:               "http-json",
		Endpoint:           endpoint,
		LookupTemplate:     "/tv/{id}",
		ArtworkBaseURL:     "https://img.example.test",
		FieldMap:           map[string]string{"title": "name", "summary": "overview", "year": "first_air_date"},
		ArtworkMap:         map[string]string{"poster": "poster_path"},
		Transforms:         map[string]string{"year": "year-from-date"},
		ItemLookupTemplate: "/tv/{id}/season/{sequence}/episode/{index}",
		ItemFieldMap:       map[string]string{"summary": "overview", "year": "air_date"},
		ItemArtworkMap:     map[string]string{"thumb": "still_path"},
		EntityMaps: []plugin.EntityMap{
			{Kind: "series", Container: true, ArtworkPath: "poster_path", FieldMap: map[string]string{"overview": "overview", "first-air-date": "first_air_date"}},
			{Kind: "part", Container: true, Path: "seasons[season_number={sequence}]", ArtworkPath: "poster_path", FieldMap: map[string]string{"overview": "overview"}},
		},
	}
}

func TestLookupOverlaysItemDocumentAndHydratesContainers(t *testing.T) {
	server, paths := itemLookupServer(t)
	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), itemLookupSpec(server.URL), "42", Variables{Sequence: "02", Index: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(); len(got) != 2 || got[1] != "/tv/42/season/2/episode/5" {
		t.Fatalf("paths = %v, want record then item lookup with the zero-stripped sequence", got)
	}
	if result.Fields["title"] != "Harbor" {
		t.Fatalf("title = %v, want the record title (no item title is mapped)", result.Fields["title"])
	}
	if result.Fields["summary"] != "Episode overview." {
		t.Fatalf("summary = %v, want the item overview", result.Fields["summary"])
	}
	if fmt.Sprint(result.Fields["year"]) != "2009" {
		t.Fatalf("year = %v, want the transformed item air date", result.Fields["year"])
	}
	if result.Artwork["thumb"] != "https://img.example.test/still.jpg" || result.Artwork["poster"] != "https://img.example.test/series.jpg" {
		t.Fatalf("artwork = %v, want item thumb and record poster", result.Artwork)
	}
	if len(result.Entities) != 2 {
		t.Fatalf("entities = %#v, want two container results", result.Entities)
	}
	series, part := result.Entities[0], result.Entities[1]
	if !series.Container || series.Kind != "series" || series.Name != "" || series.ArtworkURL != "https://img.example.test/series.jpg" ||
		series.Fields["overview"] != "Series overview." || series.Fields["first-air-date"] != "2008-01-20" {
		t.Fatalf("series = %#v", series)
	}
	if !part.Container || part.Kind != "part" || part.ArtworkURL != "https://img.example.test/s2.jpg" || part.Fields["overview"] != "Second." {
		t.Fatalf("part = %#v, want the element selected by the sequence", part)
	}
	if series.SequenceScoped || !part.SequenceScoped {
		t.Fatalf("sequence scoped: series %t, part %t; want only the map whose Path names {sequence}", series.SequenceScoped, part.SequenceScoped)
	}
}

// TestLookupTemplateNamingSequenceScopesEveryContainerResult (CORE-3128): a
// container result read from a document the item's {sequence} selected
// describes that sequence, whatever its map Path.
func TestLookupTemplateNamingSequenceScopesEveryContainerResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/42/season/2" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"name":"Season 2","overview":"Second."}`)
	}))
	t.Cleanup(server.Close)
	spec := plugin.ScraperSpec{
		ID: "tv", Kind: "http-json", Endpoint: server.URL, LookupTemplate: "/tv/{id}/season/{sequence}",
		FieldMap:   map[string]string{"title": "name"},
		EntityMaps: []plugin.EntityMap{{Kind: "part", Container: true, FieldMap: map[string]string{"overview": "overview"}}},
	}
	result, err := New(WithInsecureLoopback()).Lookup(context.Background(), spec, "42", Variables{Sequence: "2", Index: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entities) != 1 || !result.Entities[0].SequenceScoped {
		t.Fatalf("entities = %#v, want one sequence-scoped container result", result.Entities)
	}
}

func TestLookupSkipsItemDocumentAndOrdinalPathsWhenOrdinalUnknown(t *testing.T) {
	server, paths := itemLookupServer(t)
	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), itemLookupSpec(server.URL), "42", Variables{Index: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(); len(got) != 1 {
		t.Fatalf("paths = %v, want only the record lookup", got)
	}
	if result.Fields["summary"] != "Series overview." {
		t.Fatalf("summary = %v, want the record overview", result.Fields["summary"])
	}
	if len(result.Entities) != 1 || result.Entities[0].Kind != "series" {
		t.Fatalf("entities = %#v, want only the root container map", result.Entities)
	}
}

func TestLookupKeepsRecordMappingWhenItemDocumentFails(t *testing.T) {
	server, _ := itemLookupServer(t)
	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), itemLookupSpec(server.URL), "42", Variables{Sequence: "3", Index: 1})
	if err != nil {
		t.Fatalf("Lookup = %v, want the item 404 to be non-fatal", err)
	}
	if result.Fields["summary"] != "Series overview." || result.Artwork["thumb"] != "" {
		t.Fatalf("result = %#v, want the record mapping only", result)
	}
	for _, entity := range result.Entities {
		if entity.Kind == "part" {
			t.Fatalf("entities = %#v, want no part for a sequence the record lacks", result.Entities)
		}
	}
}

func TestSequenceSubstitution(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"01", "1"}, {"0", "0"}, {"00", "0"}, {"12", "12"}, {"1.5", "1.5"}, {" ", ""}} {
		if got := (Variables{Sequence: tc.in}).sequenceValue(); got != tc.want {
			t.Fatalf("sequenceValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if (Variables{}).namesUnknownOrdinal("seasons") || !(Variables{}).namesUnknownOrdinal("x[n={index}]") {
		t.Fatal("namesUnknownOrdinal must flag only templates naming an unknown ordinal")
	}
}

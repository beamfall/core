package scrape

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/plugin"
)

// TestLookupResolvesContainerArtworkTitleAndLinks (CORE-2514, CORE-2513): a
// container map's ArtworkMap, TitlePath and ContainerRelations resolve into
// the result's artwork slots, display title and links to the sibling-hydrated
// Entities in source order; paths that do not resolve leave them unset.
func TestLookupResolvesContainerArtworkTitleAndLinks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"name":" 90 Day Fiancé ","overview":"Series.","backdrop_path":"/back.jpg","logo_path":"",
			"genres":[{"name":"Reality"},{"name":"Drama"}],"credits":{"cast":[{"name":"Lead"},{"name":"Second"}]}}`)
	}))
	defer server.Close()
	spec := plugin.ScraperSpec{
		ID: "tv", Kind: "http-json", Endpoint: server.URL, LookupTemplate: "/tv/{id}",
		ArtworkBaseURL: "https://img.example.test",
		FieldMap:       map[string]string{"title": "name"},
		EntityMaps: []plugin.EntityMap{
			{Kind: "genre", Relation: "genre-of", Path: "genres", NamePath: "name"},
			{Kind: "person", Relation: "credited-in", Path: "credits.cast", NamePath: "name"},
			{
				Kind: "series", Container: true,
				ArtworkMap:         map[string]string{"backdrop": "backdrop_path", "logo": "logo_path", "banner": "banner_path"},
				TitlePath:          "name",
				ContainerRelations: map[string]string{"in-genre": "genre-of", "credit": "credited-in"},
			},
		},
	}
	result, err := New(WithInsecureLoopback()).Lookup(context.Background(), spec, "42", Variables{})
	if err != nil {
		t.Fatal(err)
	}
	var series EntityResult
	for _, entity := range result.Entities {
		if entity.Container {
			series = entity
		}
	}
	if series.Title != "90 Day Fiancé" {
		t.Fatalf("title = %q, want the trimmed accented name", series.Title)
	}
	if len(series.Artwork) != 1 || series.Artwork["backdrop"] != "https://img.example.test/back.jpg" {
		t.Fatalf("artwork = %v, want only the resolved backdrop", series.Artwork)
	}
	var links []string
	for _, link := range series.Links {
		links = append(links, fmt.Sprintf("%s:%s:%s", link.Relation, link.Kind, link.Name))
	}
	want := "[credit:person:Lead credit:person:Second in-genre:genre:Reality in-genre:genre:Drama]"
	if fmt.Sprint(links) != want {
		t.Fatalf("links = %v, want %s", links, want)
	}
}

package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// scraperHolderBundle declares a container kind with a single-holder relation
// to Media and a scraper whose lookup document may name a group object. The
// non-container map creates and links the group Entity (title, provider id and
// poster); the container map adds the backdrop to the holder it found.
func scraperHolderBundle(endpoint string) singleBundle {
	return singleBundle{
		ID: "demo",
		EntityKinds: []plugin.EntityKind{{
			Name: "set", Archetype: "container", OrderedChildren: true,
			Fields:    []plugin.FieldSpec{{Name: "provider-id", Type: "text"}},
			Relations: []plugin.RelationSpec{{Name: "set-member", ToKind: plugin.MediaKindName, Cardinality: "one"}},
		}},
		Scrapers: []plugin.ScraperSpec{{
			ID: "item", Kind: "http-json", Endpoint: endpoint, LookupTemplate: "/item/{id}",
			ArtworkBaseURL: endpoint + "/img",
			FieldMap:       map[string]string{"title": "title", "year": "release_date"},
			Transforms:     map[string]string{"year": "year-from-date"},
			EntityMaps: []plugin.EntityMap{
				{
					Kind: "set", Relation: "set-member", Path: "group", NamePath: "name",
					ArtworkPath: "poster_path", FieldMap: map[string]string{"provider-id": "id"},
				},
				{Kind: "set", Container: true, Path: "group", ArtworkMap: map[string]string{"backdrop": "backdrop_path"}},
			},
		}},
	}
}

// TestIdentifyLinksScraperDeclaredHolder (CORE-3152): two lookups naming the
// same group object hydrate one container Entity with the group title, the
// provider id, a poster and a backdrop, linked to both Media and listed in
// year order (the later release identified first) through the entity-media
// read path and the container chain. Re-identifying against a document with
// no group removes the link; against another group moves it.
func TestIdentifyLinksScraperDeclaredHolder(t *testing.T) {
	ctx := context.Background()
	docs := map[string]string{
		"/item/2": `{"title":"Afterword","release_date":"2003-05-15","group":{"id":77,"name":"Synthetic Set","poster_path":"/set.jpg","backdrop_path":"/set-backdrop.jpg"}}`,
		"/item/1": `{"title":"Origin","release_date":"1999-03-31","group":{"id":77,"name":"Synthetic Set","poster_path":"/set.jpg","backdrop_path":"/set-backdrop.jpg"}}`,
		"/item/3": `{"title":"Origin","release_date":"1999-03-31","group":null}`,
		"/item/4": `{"title":"Origin","release_date":"1999-03-31","group":{"id":88,"name":"Other Set","poster_path":"/other.jpg","backdrop_path":"/other-backdrop.jpg"}}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doc, ok := docs[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(doc))
	}))
	defer server.Close()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	library, err := st.CreateLibrary(ctx, "Items", false, "demo", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	importMedia := func(name string) store.MediaID {
		t.Helper()
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/synthetic/" + name + ".mkv",
			Title: name, Container: "mkv", Codec: "h264", Class: store.MediaClassVideo,
			ContentFingerprint: "fp-holder-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		return media.ID
	}
	first, second := importMedia("first"), importMedia("second")
	art := artwork.NewService(st, copyArtworkTools{}, t.TempDir())
	service := identify.NewService(scrape.New(scrape.WithInsecureLoopback()), scraperHolderBundle(server.URL), st)
	service.SetEntities(st)
	service.SetArtwork(urlFetcher{}, art)
	apply := func(media store.MediaID, id string) {
		t.Helper()
		if err := service.Apply(ctx, media, "demo", "item", id); err != nil {
			t.Fatal(err)
		}
	}
	members := func(id store.EntityID) string {
		t.Helper()
		items, err := st.MediaForEntity(ctx, id, metadataLibraryFilter)
		if err != nil {
			t.Fatal(err)
		}
		titles := make([]string, 0, len(items))
		for _, item := range items {
			titles = append(titles, item.Title)
		}
		return strings.Join(titles, ",")
	}
	holder := func(media store.MediaID) store.EntityID {
		t.Helper()
		chain, err := service.ContainerEntitiesForMedia(ctx, media)
		if err != nil {
			t.Fatal(err)
		}
		if len(chain) == 0 {
			return ""
		}
		if len(chain) != 1 || chain[0].Kind != "set" {
			t.Fatalf("container chain = %+v, want one set", chain)
		}
		return chain[0].ID
	}

	apply(second, "2")
	apply(first, "1")
	set := identify.EntityIDForName(library.ID, "set", "Synthetic Set")
	entity, err := st.Entity(ctx, set, metadataLibraryFilter)
	if err != nil {
		t.Fatal(err)
	}
	if entity.Title != "Synthetic Set" {
		t.Fatalf("set title = %q, want the group name", entity.Title)
	}
	var providerID string
	for _, field := range entity.Fields {
		if field.Name == "provider-id" {
			providerID = field.Text
		}
	}
	if providerID != "77" {
		t.Fatalf("set provider-id = %q, want 77 (fields %+v)", providerID, entity.Fields)
	}
	for slot, want := range map[string]string{artwork.SlotPoster: "/img/set.jpg", artwork.SlotBackdrop: "/img/set-backdrop.jpg"} {
		got, err := os.ReadFile(art.EntityArtworkPath(set, slot))
		if err != nil || !strings.HasSuffix(string(got), want) {
			t.Fatalf("set %s = %q, %v; want %s", slot, got, err, want)
		}
	}
	if got := members(set); got != "Origin,Afterword" {
		t.Fatalf("set members = %q, want year order, not title order", got)
	}
	if holder(first) != set || holder(second) != set {
		t.Fatal("both Media should be held by the one set")
	}

	apply(first, "3")
	if got := holder(first); got != "" {
		t.Fatalf("first held by %q after a match with no group, want none", got)
	}
	if got := members(set); got != "Afterword" {
		t.Fatalf("set members = %q after removal, want only the second", got)
	}

	apply(first, "1")
	apply(first, "4")
	other := identify.EntityIDForName(library.ID, "set", "Other Set")
	if got := holder(first); got != other {
		t.Fatalf("first held by %q after moving, want %q", got, other)
	}
	if got := members(set); got != "Afterword" {
		t.Fatalf("set members = %q after the move, want no stale link", got)
	}
	if got := members(other); got != "Origin" {
		t.Fatalf("other set members = %q, want the moved Media", got)
	}
}

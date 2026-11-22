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
// non-container map creates and links the group Entity, named by the group's
// provider id so a renamed group stays one Entity and two groups sharing a
// name stay two (provider id and poster); the container map titles the holder
// it found with the group name and adds the backdrop. groupPath is the
// document path of the group object.
func scraperHolderBundle(endpoint, groupPath string) singleBundle {
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
					Kind: "set", Relation: "set-member", Path: groupPath, NamePath: "id",
					ArtworkPath: "poster_path", FieldMap: map[string]string{"provider-id": "id"},
				},
				{
					Kind: "set", Container: true, Path: groupPath, TitlePath: "name",
					ArtworkMap: map[string]string{"backdrop": "backdrop_path"},
				},
			},
		}},
	}
}

// TestIdentifyLinksScraperDeclaredHolder (CORE-3152): two lookups naming the
// same group object hydrate one container Entity with the group title, the
// provider id, a poster and a backdrop, linked to both Media and listed in
// year order (the later release identified first) through the entity-media
// read path and the container chain. The holder is keyed by the group's
// provider id: a renamed group keeps its Entity and takes the new title, and
// another group with the same name gets its own. Re-identifying against a
// document with no group removes the scraper's link but keeps an operator's;
// against another group moves it. The fallback sort order is the stored year
// after field precedence, so a manual year outranks the scraper's.
func TestIdentifyLinksScraperDeclaredHolder(t *testing.T) {
	ctx := context.Background()
	docs := map[string]string{
		"/item/2": `{"title":"Afterword","release_date":"2003-05-15","group":{"id":77,"name":"Synthetic Set","poster_path":"/set.jpg","backdrop_path":"/set-backdrop.jpg"}}`,
		"/item/1": `{"title":"Origin","release_date":"1999-03-31","group":{"id":77,"name":"Synthetic Set","poster_path":"/set.jpg","backdrop_path":"/set-backdrop.jpg"}}`,
		"/item/3": `{"title":"Loner","release_date":"1999-03-31","group":null}`,
		"/item/4": `{"title":"Origin","release_date":"1999-03-31","group":{"id":88,"name":"Other Set","poster_path":"/other.jpg","backdrop_path":"/other-backdrop.jpg"}}`,
		"/item/5": `{"title":"Origin","release_date":"1999-03-31","group":{"id":77,"name":"Renamed Set","poster_path":"/set.jpg","backdrop_path":"/set-backdrop.jpg"}}`,
		"/item/7": `{"title":"Stray","release_date":"2005-01-01","group":{"name":"Hijack Name","backdrop_path":"/hijack.jpg"}}`,
		"/item/8": `{"title":"Origin","release_date":"1999-03-31","group":{"id":77}}`,
		"/item/6": `{"title":"Namesake","release_date":"2001-01-01","group":{"id":99,"name":"Renamed Set","poster_path":"/twin.jpg","backdrop_path":"/twin-backdrop.jpg"}}`,
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
	first, second, third, fourth := importMedia("first"), importMedia("second"), importMedia("third"), importMedia("fourth")
	art := artwork.NewService(st, copyArtworkTools{}, t.TempDir())
	service := identify.NewService(scrape.New(scrape.WithInsecureLoopback()), scraperHolderBundle(server.URL, "group"), st)
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
	set := identify.EntityIDForName(library.ID, "set", "77")
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

	apply(first, "5")
	entity, err = st.Entity(ctx, set, metadataLibraryFilter)
	if err != nil {
		t.Fatal(err)
	}
	if entity.Title != "Renamed Set" || holder(first) != set || holder(second) != set {
		t.Fatalf("renamed group: title %q, holders %q/%q; want one set %q titled Renamed Set",
			entity.Title, holder(first), holder(second), set)
	}
	apply(first, "8")
	if entity, err = st.Entity(ctx, set, metadataLibraryFilter); err != nil || entity.Title != "Renamed Set" || holder(first) != set {
		t.Fatalf("group without a name: title %q, %v, holder %q; want the stored title kept on %q",
			entity.Title, err, holder(first), set)
	}
	apply(fourth, "6")
	twin := identify.EntityIDForName(library.ID, "set", "99")
	if got := holder(fourth); got != twin || twin == set {
		t.Fatalf("same-name group held by %q, want its own set %q", got, twin)
	}
	if got := members(set); got != "Origin,Afterword" {
		t.Fatalf("set members = %q, want the same-name group kept apart", got)
	}

	manualYear := 2010
	if err := st.UpdateMediaMetadata(ctx, first, store.MediaMetadata{Year: &manualYear, Source: store.MutationSourceManual}); err != nil {
		t.Fatal(err)
	}
	apply(first, "1")
	if got := members(set); got != "Afterword,Origin" {
		t.Fatalf("set members = %q, want the manual year (2010) to order, not the scraped 1999", got)
	}

	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: set, Name: "set-member", ToMediaID: third, Cardinality: "one",
	}); err != nil {
		t.Fatal(err)
	}
	apply(third, "7")
	if entity, err = st.Entity(ctx, set, metadataLibraryFilter); err != nil || entity.Title != "Synthetic Set" {
		t.Fatalf("curated set title = %q, %v after a group without an id, want Synthetic Set untouched", entity.Title, err)
	}
	apply(third, "3")
	if got := holder(third); got != set {
		t.Fatalf("operator-linked third held by %q after a match with no group, want the curated %q", got, set)
	}

	apply(first, "3")
	if got := holder(first); got != "" {
		t.Fatalf("first held by %q after a match with no group, want none", got)
	}
	if got := members(set); got != "Afterword,Loner" {
		t.Fatalf("set members = %q after removal, want the second and the curated third", got)
	}

	apply(first, "1")
	apply(first, "4")
	other := identify.EntityIDForName(library.ID, "set", "88")
	if got := holder(first); got != other {
		t.Fatalf("first held by %q after moving, want %q", got, other)
	}
	if got := members(set); got != "Afterword,Loner" {
		t.Fatalf("set members = %q after the move, want no stale link", got)
	}
	if got := members(other); got != "Origin" {
		t.Fatalf("other set members = %q, want the moved Media", got)
	}
}

// holderOrdinals is a MediaTitleSource whose parser ordinal is settable, so
// a test can make an ordinal entity map path resolvable or unknown.
type holderOrdinals struct{ sequence string }

func (holderOrdinals) ParsedMediaTitle(context.Context, store.MediaID, string) (string, error) {
	return "", nil
}

func (o *holderOrdinals) ParsedMediaOrdinal(context.Context, store.MediaID, string) (string, int, error) {
	return o.sequence, 0, nil
}

// TestIdentifyKeepsHolderLinkWhenMapSkipped (CORE-3152): a holder map whose
// path names an ordinal the parser does not know is skipped by the engine,
// which says nothing about the document, so the earlier link stays, even
// though a fallback map onto the same relation resolves nothing. Both maps
// resolving nothing once the ordinal is known removes it.
func TestIdentifyKeepsHolderLinkWhenMapSkipped(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"title":"Origin","release_date":"1999-03-31","parts":[{"part":"1","group":{"id":77,"name":"Synthetic Set"}}]}`))
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
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: library.ID, Path: "/synthetic/part.mkv", Title: "part",
		Container: "mkv", Codec: "h264", Class: store.MediaClassVideo, ContentFingerprint: "fp-holder-part",
	})
	if err != nil {
		t.Fatal(err)
	}
	ordinals := &holderOrdinals{sequence: "1"}
	bundle := scraperHolderBundle(server.URL, "parts[part={sequence}].group")
	bundle.Scrapers[0].EntityMaps = append(bundle.Scrapers[0].EntityMaps,
		plugin.EntityMap{Kind: "set", Relation: "set-member", Path: "fallback", NamePath: "id"})
	service := identify.NewService(scrape.New(scrape.WithInsecureLoopback()), bundle, st)
	service.SetEntities(st)
	service.SetMediaTitles(ordinals)
	set := identify.EntityIDForName(library.ID, "set", "77")
	heldBy := func() store.EntityID {
		t.Helper()
		chain, err := service.ContainerEntitiesForMedia(ctx, media.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(chain) == 0 {
			return ""
		}
		return chain[0].ID
	}
	for _, step := range []struct {
		sequence string
		want     store.EntityID
	}{{"1", set}, {"", set}, {"2", ""}} {
		ordinals.sequence = step.sequence
		if err := service.Apply(ctx, media.ID, "demo", "item", "1"); err != nil {
			t.Fatal(err)
		}
		if got := heldBy(); got != step.want {
			t.Fatalf("sequence %q: held by %q, want %q", step.sequence, got, step.want)
		}
	}
}

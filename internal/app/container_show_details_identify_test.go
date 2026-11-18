package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// showDetailsBundle is containerLookupBundle with genre and person kinds, the
// show relations toward them and a show map that also declares a backdrop, the
// matched title and links to the genres and cast the lookup hydrates.
func showDetailsBundle(endpoint string) singleBundle {
	bundle := containerLookupBundle(endpoint)
	bundle.EntityKinds[0].Relations = []plugin.RelationSpec{
		{Name: "in-genre", ToKind: "genre"},
		{Name: "credit", ToKind: "person"},
	}
	bundle.EntityKinds = append(bundle.EntityKinds,
		plugin.EntityKind{Name: "genre", Archetype: "category", Relations: []plugin.RelationSpec{{Name: "genre-of", ToKind: plugin.MediaKindName}}},
		plugin.EntityKind{Name: "person", Archetype: "person", Relations: []plugin.RelationSpec{{Name: "credited-in", ToKind: plugin.MediaKindName}}},
	)
	spec := &bundle.Scrapers[0]
	spec.EntityMaps = []plugin.EntityMap{
		{Kind: "genre", Relation: "genre-of", Path: "genres", NamePath: "name"},
		{Kind: "person", Relation: "credited-in", Path: "credits.cast", NamePath: "name"},
		{
			Kind: "show", Container: true, ArtworkPath: "poster_path",
			ArtworkMap:         map[string]string{"backdrop": "backdrop_path"},
			TitlePath:          "name",
			ContainerRelations: map[string]string{"in-genre": "genre-of", "credit": "credited-in"},
		},
	}
	return bundle
}

// TestIdentifyHydratesShowDetails (CORE-2514, CORE-2513): identifying an
// episode gives the show Entity local hydration built from the folder the
// matched title (apostrophes and accents kept, the Entity ID unchanged), the
// backdrop, and relations to the genres and cast in billing order. A title
// path that does not resolve keeps the folder spelling. Re-identifying against
// a match with other genres and cast replaces the links rather than adding to
// them, and a match with an empty cast clears the cast.
func TestIdentifyHydratesShowDetails(t *testing.T) {
	for _, tc := range []struct{ folder, matched, want string }{
		{"A Mothers Son", `"name":"A Mother's Son",`, "A Mother's Son"},
		{"90 Day Fiance", `"name":"90 Day Fiancé",`, "90 Day Fiancé"},
		{"A Good Girls Guide to Murder", "", "A Good Girls Guide to Murder"},
	} {
		t.Run(tc.folder, func(t *testing.T) {
			ctx := context.Background()
			var mu sync.Mutex
			genres, cast := `{"name":"Drama"},{"name":"Mystery"}`, `{"name":"Lead Actor"},{"name":"Second Actor"}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/tv/42" {
					http.NotFound(w, r)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				fmt.Fprintf(w, `{%s"overview":"Series overview.","poster_path":"/series.jpg","backdrop_path":"/backdrop.jpg",
					"genres":[%s],"credits":{"cast":[%s]}}`, tc.matched, genres, cast)
			}))
			defer server.Close()
			st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			library, err := st.CreateLibrary(ctx, "Shows", false, "demo", "profile-default")
			if err != nil {
				t.Fatal(err)
			}
			media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: library.ID, Path: "/synthetic/" + tc.folder + "/Season 01/S01E01.mkv",
				Title: "S01E01", Container: "mkv", Codec: "h264", Class: store.MediaClassVideo,
				ContentFingerprint: "fp-show-details-0001",
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := (showSeasonHydrator{st: st}).HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{MediaID: media.ID, LibraryID: library.ID}); err != nil {
				t.Fatal(err)
			}
			if err := st.UpsertEntity(ctx, store.Entity{ID: "entity-show", LibraryID: library.ID, Kind: "show", Title: tc.folder}); err != nil {
				t.Fatal(err)
			}
			art := artwork.NewService(st, copyArtworkTools{}, t.TempDir())
			service := identify.NewService(scrape.New(scrape.WithInsecureLoopback()), showDetailsBundle(server.URL), st)
			service.SetEntities(st)
			service.SetArtwork(urlFetcher{}, art)
			service.SetMediaTitles(parserMediaTitles{store: st, parsers: map[string]scanpkg.FilenameParser{"demo": ordinalParser{}}})

			if err := service.Apply(ctx, media.ID, "demo", "tv", "42"); err != nil {
				t.Fatal(err)
			}
			show, err := st.Entity(ctx, "entity-show", metadataLibraryFilter)
			if err != nil {
				t.Fatal(err)
			}
			if show.Title != tc.want {
				t.Fatalf("show title = %q, want %q", show.Title, tc.want)
			}
			backdrop, err := os.ReadFile(art.EntityArtworkPath("entity-show", artwork.SlotBackdrop))
			if err != nil || !strings.HasSuffix(string(backdrop), "/img/backdrop.jpg") {
				t.Fatalf("show backdrop = %q, %v; want the backdrop_path image", backdrop, err)
			}
			assertRelated := func(want map[string][]string) {
				t.Helper()
				groups, err := st.RelatedEntityGroups(ctx, "entity-show", metadataLibraryFilter)
				if err != nil {
					t.Fatal(err)
				}
				got := map[string][]string{}
				for _, group := range groups {
					for _, item := range group.Items {
						got[group.Kind] = append(got[group.Kind], item.Title)
					}
				}
				for kind, names := range want {
					if strings.Join(got[kind], ",") != strings.Join(names, ",") {
						t.Fatalf("show related %s = %v, want %v in source order", kind, got[kind], names)
					}
				}
			}
			assertRelated(map[string][]string{"genre": {"Drama", "Mystery"}, "person": {"Lead Actor", "Second Actor"}})

			mu.Lock()
			genres, cast = `{"name":"Comedy"}`, `{"name":"Other Actor"}`
			mu.Unlock()
			if err := service.Apply(ctx, media.ID, "demo", "tv", "42"); err != nil {
				t.Fatal(err)
			}
			assertRelated(map[string][]string{"genre": {"Comedy"}, "person": {"Other Actor"}})

			mu.Lock()
			cast = ""
			mu.Unlock()
			if err := service.Apply(ctx, media.ID, "demo", "tv", "42"); err != nil {
				t.Fatal(err)
			}
			assertRelated(map[string][]string{"genre": {"Comedy"}, "person": nil})
		})
	}
}

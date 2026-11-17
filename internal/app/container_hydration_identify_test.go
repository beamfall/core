package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
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

// ordinalParser parses "S02E05"-style names into sequence "02", index 5 and
// titles each file by its base name.
type ordinalParser struct{}

func (ordinalParser) Parse(p string, _ scanpkg.ParseProbe) (scanpkg.ParsedMedia, bool) {
	var sequence, index int
	if _, err := fmt.Sscanf(path.Base(p), "S%02dE%02d", &sequence, &index); err != nil {
		return scanpkg.ParsedMedia{}, false
	}
	return scanpkg.ParsedMedia{Title: "Harbor", Sequence: fmt.Sprintf("%02d", sequence), Index: index}, true
}

func (ordinalParser) MediaTitle(p string, _ scanpkg.ParseProbe) string {
	return strings.TrimSuffix(path.Base(p), path.Ext(p))
}

// urlFetcher returns a JPEG whose bytes name the URL fetched.
type urlFetcher struct{}

func (urlFetcher) FetchResource(_ context.Context, rawURL string) ([]byte, error) {
	return []byte(jpegMagic + rawURL), nil
}

func containerLookupServer(t *testing.T) (*httptest.Server, func() []string) {
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
				"seasons":[{"season_number":1,"poster_path":"/s1.jpg"},{"season_number":2,"overview":"Second.","air_date":"2009-03-08","poster_path":"/s2.jpg"}]}`)
		case "/tv/42/season/2/episode/5":
			fmt.Fprint(w, `{"name":"Scraped Name","overview":"Episode overview.","air_date":"2010-04-12"}`)
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

func containerLookupBundle(endpoint string) singleBundle {
	bundle := showSeasonBundle()
	bundle.EntityKinds[0].Fields = []plugin.FieldSpec{{Name: "overview", Type: "text"}, {Name: "first-air-date", Type: "date"}}
	bundle.EntityKinds[1].Fields = []plugin.FieldSpec{{Name: "overview", Type: "text"}}
	bundle.Scrapers = []plugin.ScraperSpec{{
		ID: "tv", Kind: "http-json", Endpoint: endpoint, ArtworkBaseURL: endpoint + "/img",
		LookupTemplate:     "/tv/{id}",
		FieldMap:           map[string]string{"title": "name", "summary": "overview", "year": "first_air_date"},
		Transforms:         map[string]string{"year": "year-from-date"},
		ItemLookupTemplate: "/tv/{id}/season/{sequence}/episode/{index}",
		ItemFieldMap:       map[string]string{"summary": "overview", "year": "air_date"},
		EntityMaps: []plugin.EntityMap{
			{Kind: "show", Container: true, ArtworkPath: "poster_path", FieldMap: map[string]string{"overview": "overview", "first-air-date": "first_air_date"}},
			{Kind: "season", Container: true, Path: "seasons[season_number={sequence}]", ArtworkPath: "poster_path", FieldMap: map[string]string{"overview": "overview"}},
		},
	}}
	return bundle
}

// TestIdentifyHydratesContainersFromItemLookup (CORE-2335): identifying an
// episode reads its per-item document by the parser's ordinal, takes the
// item's summary and air-date year while keeping the parser title, and merges
// the show and season fields and posters onto the Entities local hydration
// wrote, keeping their titles. Nothing new is created.
func TestIdentifyHydratesContainersFromItemLookup(t *testing.T) {
	ctx := context.Background()
	server, paths := containerLookupServer(t)
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
		LibraryID: library.ID, Path: "/synthetic/Harbor/Season 02/S02E05.mkv",
		Title: "S02E05", Container: "mkv", Codec: "h264", Class: store.MediaClassVideo,
		ContentFingerprint: "fp-container-hydration-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := (showSeasonHydrator{st: st}).HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{MediaID: media.ID, LibraryID: library.ID}); err != nil {
		t.Fatal(err)
	}
	two := 2
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: "entity-season-1", Name: "season-of", ToEntityID: "entity-show", SortOrder: &two}); err != nil {
		t.Fatal(err)
	}
	art := artwork.NewService(st, copyArtworkTools{}, t.TempDir())
	service := identify.NewService(scrape.New(scrape.WithInsecureLoopback()), containerLookupBundle(server.URL), st)
	service.SetEntities(st)
	service.SetArtwork(urlFetcher{}, art)
	service.SetMediaTitles(parserMediaTitles{store: st, parsers: map[string]scanpkg.FilenameParser{"demo": ordinalParser{}}})

	if err := service.Apply(ctx, media.ID, "demo", "tv", "42"); err != nil {
		t.Fatal(err)
	}
	if got := paths(); len(got) < 2 || got[1] != "/tv/42/season/2/episode/5" {
		t.Fatalf("lookup paths = %v, want the record then the item by the parser ordinal", got)
	}
	got, err := st.Media(ctx, media.ID, metadataLibraryFilter)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "S02E05" || got.Year != 2010 || got.Summary != "Episode overview." {
		t.Fatalf("media = title %q year %d summary %q; want the parser title, the item year and summary", got.Title, got.Year, got.Summary)
	}
	for id, want := range map[store.EntityID]struct{ title, overview, poster string }{
		"entity-show":     {"Show", "Series overview.", "/img/series.jpg"},
		"entity-season-1": {"Season 1", "Second.", "/img/s2.jpg"},
	} {
		entity, err := st.Entity(ctx, id, metadataLibraryFilter)
		if err != nil {
			t.Fatal(err)
		}
		overview := ""
		for _, field := range entity.Fields {
			if field.Name == "overview" {
				overview = field.Text
			}
		}
		if entity.Title != want.title || overview != want.overview {
			t.Fatalf("%s = title %q overview %q; want %q, %q", id, entity.Title, overview, want.title, want.overview)
		}
		poster, err := os.ReadFile(art.EntityPosterPath(id))
		if err != nil || !strings.HasSuffix(string(poster), want.poster) {
			t.Fatalf("%s poster = %q, %v; want the %s poster", id, poster, err, want.poster)
		}
	}
	for _, kind := range []string{"show", "season"} {
		if _, err := st.Entity(ctx, identify.EntityIDForName(library.ID, kind, "Harbor"), metadataLibraryFilter); err == nil {
			t.Fatalf("identification created a %s Entity; want container results merged onto existing Entities only", kind)
		}
	}
}

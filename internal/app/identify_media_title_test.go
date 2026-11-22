package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"

	filmtv "github.com/beamfall/plugin-filmtv"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// fileTitleParser parses every file to the container named by its directory
// and titles each file by its own base name, the shape of a parser whose
// parsed title names a group several files share.
type fileTitleParser struct{}

func (fileTitleParser) Parse(p string, _ scanpkg.ParseProbe) (scanpkg.ParsedMedia, bool) {
	return scanpkg.ParsedMedia{Title: path.Base(path.Dir(p))}, true
}

func (fileTitleParser) MediaTitle(p string, _ scanpkg.ParseProbe) string {
	return strings.TrimSuffix(path.Base(p), path.Ext(p))
}

// containerOnlyParser has no per-file title.
type containerOnlyParser struct{}

func (containerOnlyParser) Parse(p string, _ scanpkg.ParseProbe) (scanpkg.ParsedMedia, bool) {
	return scanpkg.ParsedMedia{Title: path.Base(path.Dir(p))}, true
}

type singleBundle plugin.Bundle

func (b singleBundle) Bundle(id plugin.ID) (plugin.Bundle, bool) {
	return plugin.Bundle(b), id == b.ID
}

// TestIdentifyKeepsParserMediaTitle (CORE-1713): identifying one of several
// files that share a container through a lookup that names the container (a
// series-shaped document) keeps the parser's per-file title, so sibling files
// stay distinguishable. Without the title source the container name replaces
// every file's title.
func TestIdentifyKeepsParserMediaTitle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"name": "Synthetic Group", "overview": "A synthetic group.", "first_air_date": "2005-01-10"}`)
	}))
	defer server.Close()
	spec := plugin.ScraperSpec{
		ID: "group", Kind: "http-json", Endpoint: server.URL, LookupTemplate: "/group/{id}",
		FieldMap:   map[string]string{"title": "name", "summary": "overview", "year": "first_air_date"},
		Transforms: map[string]string{"year": "year-from-date"},
	}
	bundles := singleBundle{ID: "demo", Scrapers: []plugin.ScraperSpec{spec}}

	cases := []struct {
		name    string
		library store.LibraryID
		parser  scanpkg.FilenameParser // nil wires no title source
		want    string
	}{
		{name: "parser title kept", library: "library-local", parser: fileTitleParser{}, want: "2023-03-01"},
		{name: "restricted library", library: "library-archive", parser: fileTitleParser{}, want: "2023-03-01"},
		{name: "no title source", library: "library-local", want: "Synthetic Group"},
		{name: "parser without MediaTitle", library: "library-local", parser: containerOnlyParser{}, want: "Synthetic Group"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: tc.library, Path: "/synthetic/Synthetic Group/2023-03-01.mkv",
				Title: "2023-03-01", Container: "mkv", Codec: "h264", Class: store.MediaClassVideo,
				ContentFingerprint: "fp-media-title-0001",
			})
			if err != nil {
				t.Fatal(err)
			}
			service := identify.NewService(scrape.New(scrape.WithInsecureLoopback()), bundles, st)
			if tc.parser != nil {
				service.SetMediaTitles(parserMediaTitles{store: st, parsers: map[string]scanpkg.FilenameParser{"demo": tc.parser}})
			}
			if err := service.Apply(ctx, media.ID, "demo", "group", "7"); err != nil {
				t.Fatal(err)
			}
			got, err := st.Media(ctx, media.ID, metadataLibraryFilter)
			if err != nil {
				t.Fatal(err)
			}
			if got.Title != tc.want || got.Year != 2005 || got.Summary != "A synthetic group." {
				t.Errorf("identified media = title %q year %d summary %q, want title %q, year 2005, the lookup summary", got.Title, got.Year, got.Summary, tc.want)
			}
		})
	}
}

// TestParserMediaTitlesWithoutFile: a Media with no readable file reports an
// error, which Apply treats as "keep the scraper's title"; a plugin with no
// parser reports no title.
func TestParserMediaTitlesWithoutFile(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	titles := parserMediaTitles{store: st, parsers: map[string]scanpkg.FilenameParser{"demo": fileTitleParser{}}}
	if _, err := titles.ParsedMediaTitle(ctx, "media-absent", "demo"); err == nil {
		t.Fatal("ParsedMediaTitle for an unknown Media succeeded; want an error")
	}
	if got, err := titles.ParsedMediaTitle(ctx, "media-absent", "other"); err != nil || got != "" {
		t.Fatalf("ParsedMediaTitle for a plugin with no parser = %q, %v; want empty, nil", got, err)
	}
}

// identifiedTitleParser is a fileTitleParser that also names the file from
// an identification's "episode-title" field value.
type identifiedTitleParser struct{ fileTitleParser }

func (p identifiedTitleParser) IdentifiedMediaTitle(file string, probe scanpkg.ParseProbe, fields map[string]string) string {
	if name := fields["episode-title"]; name != "" {
		return p.MediaTitle(file, probe) + " · " + name
	}
	return ""
}

// TestIdentifyNamesTheEpisode (CORE-2470): through the real scrape engine and
// store, a parser with the identified-title hook titles the identified Media
// from the lookup's plugin Media field value, with its punctuation; a parser
// without the hook keeps its per-file title, and so does one given no value.
func TestIdentifyNamesTheEpisode(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		parser scanpkg.FilenameParser
		want   string
	}{
		{name: "identified name", body: `{"name": "Synthetic Group", "episode": "I Don't Want to Go"}`,
			parser: identifiedTitleParser{}, want: "S03E02 · I Don't Want to Go"},
		{name: "parser without the hook", body: `{"name": "Synthetic Group", "episode": "I Don't Want to Go"}`,
			parser: fileTitleParser{}, want: "S03E02"},
		{name: "no identified value", body: `{"name": "Synthetic Group"}`,
			parser: identifiedTitleParser{}, want: "S03E02"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			spec := plugin.ScraperSpec{
				ID: "episode", Kind: "http-json", Endpoint: server.URL, LookupTemplate: "/episode/{id}",
				FieldMap: map[string]string{"title": "name", "media.episode-title": "episode"},
			}
			bundles := singleBundle{ID: "demo", Scrapers: []plugin.ScraperSpec{spec},
				MediaFields: []plugin.FieldSpec{{Name: "episode-title", Type: "text"}}}
			ctx := context.Background()
			st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: "library-local", Path: "/synthetic/Synthetic Group/S03E02.mkv",
				Title: "S03E02", Container: "mkv", Codec: "h264", Class: store.MediaClassVideo,
				ContentFingerprint: "fp-media-title-0002",
			})
			if err != nil {
				t.Fatal(err)
			}
			service := identify.NewService(scrape.New(scrape.WithInsecureLoopback()), bundles, st)
			service.SetMediaTitles(parserMediaTitles{store: st, parsers: map[string]scanpkg.FilenameParser{"demo": tc.parser}})
			if err := service.Apply(ctx, media.ID, "demo", "episode", "7"); err != nil {
				t.Fatal(err)
			}
			got, err := st.Media(ctx, media.ID, metadataLibraryFilter)
			if err != nil {
				t.Fatal(err)
			}
			if got.Title != tc.want {
				t.Errorf("identified title = %q, want %q", got.Title, tc.want)
			}
		})
	}
}

func TestOperatorOrdinalSurvivesRescanAndRehydration(t *testing.T) {
	r := newRehydrationScan(t)
	r.path = path.Join(r.root, "Sample - S01E03.flac")
	r.write("synthetic bytes for scan and hydration", 3*time.Hour, map[string]string{"title": "One", "album": "First"})
	r.scan()
	id := r.mediaID
	ctx := t.Context()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/lookup/42/1/3":
			fmt.Fprint(w, `{"name":"Matched item","summary":"parsed ordinal"}`)
		case "/lookup/42/2/5":
			fmt.Fprint(w, `{"name":"Matched item","summary":"operator ordinal"}`)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	bundle := singleBundle{ID: testHydrationPluginID, Scrapers: []plugin.ScraperSpec{{
		ID: "lookup", Kind: "http-json", Endpoint: server.URL, LookupTemplate: "/lookup/{id}/{sequence}/{index}",
		FieldMap: map[string]string{"title": "name", "summary": "summary"},
	}}}
	service := identify.NewService(scrape.New(scrape.WithInsecureLoopback()), bundle, r.st)
	service.SetMediaTitles(parserMediaTitles{store: r.st, parsers: map[string]scanpkg.FilenameParser{testHydrationPluginID: filmtv.New()}})

	assertSummary := func(want string) {
		t.Helper()
		media, err := r.st.Media(ctx, id, metadataLibraryFilter)
		if err != nil || media.Summary != want {
			t.Fatalf("lookup path = %q, err %v, want %q", media.Summary, err, want)
		}
	}

	if err := service.Apply(ctx, id, testHydrationPluginID, "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	assertSummary("parsed ordinal")
	ordinal := store.MediaOrdinal{Sequence: "2", Index: 5}
	if err := service.ApplyFixMatch(ctx, id, testHydrationPluginID, "lookup", "42", nil, &ordinal, "profile-default"); err != nil {
		t.Fatal(err)
	}
	assertSummary("operator ordinal")

	r.touch(2 * time.Hour)
	r.scan()
	if r.mediaID != id {
		t.Fatalf("rescanned Media = %s, want %s", r.mediaID, id)
	}
	r.rehydrate("sha256:updated-tags", map[string]string{"title": "Two", "album": "Second"})
	if got := r.field("album"); got != "Second" {
		t.Fatalf("rehydrated album = %q, want Second", got)
	}
	if err := service.Refresh(ctx, id); err != nil {
		t.Fatal(err)
	}
	assertSummary("operator ordinal")
}

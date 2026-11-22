package graph

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"testing"

	sdk "github.com/beamfall/plugin-sdk"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/l10n"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

const fixMatchChildrenTestQuery = `query($mediaID: String!, $candidateID: String!, $level: Int!, $parentKey: String) {
	fixMatchChildren(mediaID: $mediaID, pluginID: "demo", scraperID: "lookup", candidateID: $candidateID, level: $level, parentKey: $parentKey) {
		key label sequence index year artworkURL
	}
}`

// The registry validates the declaration against a production-shaped endpoint.
// Only the identify engine's test bundle view points at the fixture server.
type fixtureScraperBundles struct {
	identify.Bundles
	endpoint string
}

func (b fixtureScraperBundles) Bundle(id plugin.ID) (plugin.Bundle, bool) {
	bundle, ok := b.Bundles.Bundle(id)
	if ok {
		bundle.Scrapers[0].Endpoint = b.endpoint
	}
	return bundle, ok
}

func TestFixMatchChildrenRecordedListings(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.RequestURI())
		mu.Unlock()
		switch r.URL.RequestURI() {
		case "/api/v1/candidates/42/groups":
			http.ServeFile(w, r, "testdata/fix-match-children/level-1.json")
		case "/api/v1/candidates/42/groups/0/items/0?index=0":
			http.ServeFile(w, r, "testdata/fix-match-children/level-2.json")
		case "/api/v1/candidates/43/groups":
			_, _ = w.Write([]byte(`{"data":{"groups":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	handler, st := newIdentifyTestHandler(t, func(bundle *plugin.Bundle) {
		spec := &bundle.Scrapers[0]
		spec.ArtworkBaseURL = "https://images.example.test/base"
		spec.MinRequestIntervalMS = 1
		spec.Children = []sdk.ChildListing{
			{
				Level: "group", ListTemplate: "/candidates/{id}/groups",
				ItemsPath: "data.groups[?type.type=official]", Key: "key", Label: "name",
				Sequence: "number", Index: "index", Year: "date", Artwork: "image", MaxItems: 2,
			},
			{
				Level: "item", ListTemplate: "/candidates/{id}/groups/{parent}/items/{sequence}?index={index}",
				ItemsPath: "$", Key: "key", Label: "name",
				Sequence: "number", Index: "index", Year: "date", Artwork: "image",
			},
		}
	})
	engine := scrape.New(scrape.WithInsecureLoopback())
	handler.SetIdentify(identify.NewService(engine, fixtureScraperBundles{handler.plugins, server.URL + "/api/v1"}, st))
	library, err := st.CreateLibrary(t.Context(), "Children fixture", false, "demo", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
		LibraryID: library.ID, Path: "/fixtures/listing.mkv", Title: "Listing",
		Container: "mkv", Codec: "h264", ContentFingerprint: "sha256:listing",
	})
	if err != nil {
		t.Fatal(err)
	}
	var sources []FixMatchSourceInfo
	execGraph(t, handler, authorizeManager, fixMatchSourcesTestQuery, map[string]any{"mediaID": string(media.ID)}, "fixMatchSources", &sources)
	if len(sources) != 1 || !sources[0].SupportsChildren || !slices.Equal(sources[0].ChildLevels, []string{"group", "item"}) {
		t.Fatalf("sources = %+v", sources)
	}

	read := func(level int, parent any) []scrape.ChildRow {
		t.Helper()
		var rows []scrape.ChildRow
		execGraph(t, handler, authorizeManager, fixMatchChildrenTestQuery,
			map[string]any{"mediaID": string(media.ID), "candidateID": "42", "level": level, "parentKey": parent},
			"fixMatchChildren", &rows)
		return rows
	}
	first := []scrape.ChildRow{
		{Key: "0", Label: "Extras", Sequence: new("0"), Index: new(0), Year: new(2024), ArtworkURL: new("https://images.example.test/base/extras.jpg")},
		{Key: "primary", Label: "Primary", Sequence: new("A"), ArtworkURL: new("https://images.example.test/primary.jpg")},
	}
	second := []scrape.ChildRow{
		{Key: "27", Label: "Opening", Sequence: new("2"), Index: new(1), Year: new(2023), ArtworkURL: new("https://images.example.test/base/opening.jpg")},
		{Key: "finale", Label: "Finale", Index: new(3)},
	}
	if got := read(1, nil); !reflect.DeepEqual(got, first) {
		t.Fatalf("level 1 = %+v, want %+v", got, first)
	}
	for range 2 {
		if got := read(2, "0"); !reflect.DeepEqual(got, second) {
			t.Fatalf("level 2 = %+v, want %+v", got, second)
		}
	}

	for _, tc := range []struct {
		name, candidate string
		level           int
		parent          any
		message         string
	}{
		{name: "zero", candidate: "42", level: 0, message: "no such level"},
		{name: "too high", candidate: "42", level: 3, message: "no such level"},
		{name: "extra parent", candidate: "42", level: 1, parent: "0", message: "parentKey is forbidden at level 1"},
		{name: "missing parent", candidate: "42", level: 2, message: "parentKey is required after level 1"},
		{name: "empty parent", candidate: "42", level: 2, parent: "", message: "invalid parentKey"},
		{name: "unlisted parent", candidate: "42", level: 2, parent: "missing", message: "parentKey was not returned by the previous level"},
		{name: "filtered parent", candidate: "42", level: 2, parent: "dvd", message: "parentKey was not returned by the previous level"},
		{name: "another candidate's parent", candidate: "43", level: 2, parent: "0", message: "parentKey was not returned by the previous level"},
		{name: "capped parent", candidate: "42", level: 2, parent: "truncated", message: "parentKey was not returned by the previous level"},
		{name: "missing ordinal", candidate: "42", level: 2, parent: "primary", message: "parent row has no valid ordinal for this level"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertFixMatchChildError(t, handler, authorizeManager, string(media.ID), tc.candidate, tc.level, tc.parent, ErrorCodeInvalid, tc.message)
		})
	}
	for _, invalid := range []string{"a/b", "..", "%2F", "%2f"} {
		assertFixMatchChildError(t, handler, authorizeManager, string(media.ID), invalid, 1, nil, ErrorCodeInvalid, "invalid candidateID")
		assertFixMatchChildError(t, handler, authorizeManager, string(media.ID), "42", 2, invalid, ErrorCodeInvalid, "invalid parentKey")
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"/api/v1/candidates/42/groups", "/api/v1/candidates/42/groups/0/items/0?index=0", "/api/v1/candidates/43/groups"}
	if !slices.Equal(requests, want) {
		t.Fatalf("fixture requests = %v, want %v with all repeats served from cache", requests, want)
	}
}

func TestFixMatchChildrenRequiresManagerVisibleMediaAndDeclaredListing(t *testing.T) {
	handler, _ := newIdentifyTestHandler(t)
	assertFixMatchChildError(t, handler, authorizeViewer, "media-alpha", "42", 1, nil, ErrorCodeForbidden, "manager role required")
	assertFixMatchChildError(t, handler, authorizeManager, "media-archive", "42", 1, nil, ErrorCodeNotFound, "unknown media")
	assertFixMatchChildError(t, handler, authorizeManager, "media-alpha", "42", 1, nil, ErrorCodeInvalid, "scraper declares no child listing")
	assertFixMatchChildError(t, handler, func(req *http.Request) {
		authorizeManager(req)
		*req = *req.WithContext(l10n.WithLocale(req.Context(), "fr"))
	}, "media-alpha", "42", 1, nil, ErrorCodeInvalid, "La source ne déclare aucune liste d’éléments enfants.")
}

func assertFixMatchChildError(t *testing.T, handler *Handler, authorize func(*http.Request), mediaID, candidateID string, level int, parent any, code ErrorCode, message string) {
	t.Helper()
	resp := postGraphJSON(t, handler, authorize, fixMatchChildrenTestQuery,
		map[string]any{"mediaID": mediaID, "candidateID": candidateID, "level": level, "parentKey": parent})
	if len(resp.Errors) != 1 || resp.Errors[0].Extensions.Code != code || resp.Errors[0].Message != message {
		t.Fatalf("errors = %#v, want %s %q", resp.Errors, code, message)
	}
}

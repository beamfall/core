package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/plugin"
)

type staticCredentials map[string]string

func (s staticCredentials) Secret(ref string) (string, error) {
	secret, ok := s[ref]
	if !ok {
		return "", fmt.Errorf("no secret %q", ref)
	}
	return secret, nil
}

type staticConfig map[string]map[string]string

func (s staticConfig) PluginConfigValues(_ context.Context, pluginID string) (map[string]string, error) {
	return s[pluginID], nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type manualClock struct {
	mu          sync.Mutex
	now         time.Time
	waits       []time.Duration
	timers      []chan time.Time
	waitCreated chan struct{}
}

func newManualClock() *manualClock {
	return &manualClock{
		now:         time.Unix(0, 0),
		waitCreated: make(chan struct{}, 16),
	}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) After(wait time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waits = append(c.waits, wait)
	timer := make(chan time.Time)
	c.timers = append(c.timers, timer)
	c.waitCreated <- struct{}{}
	return timer
}

func (c *manualClock) advance(wait time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(wait)
	c.mu.Unlock()
}

func (c *manualClock) releaseLast() {
	c.mu.Lock()
	timer := c.timers[len(c.timers)-1]
	c.mu.Unlock()
	close(timer)
}

func TestLookupHTTPJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lookup/vega%20prime" && r.URL.EscapedPath() != "/lookup/vega%20prime" {
			t.Errorf("unexpected path %q", r.URL.EscapedPath())
		}
		fmt.Fprint(w, `{
			"results": [{"name": "Vega Prime", "launched": 1997, "active": true}],
			"art": {"front": "https://img.invalid/front.jpg"}
		}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID:             "starcharts",
		Kind:           "http-json",
		Endpoint:       server.URL,
		LookupTemplate: "/lookup/{id}",
		FieldMap: map[string]string{
			"title":   "results.0.name",
			"year":    "results.0.launched",
			"flag":    "results.0.active",
			"missing": "results.0.nope",
		},
		ArtworkMap: map[string]string{"poster": "art.front"},
	}, "vega prime")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"title": "Vega Prime", "year": 1997.0, "flag": "true"}
	for field, value := range want {
		if result.Fields[field] != value {
			t.Errorf("field %q = %v, want %v", field, result.Fields[field], value)
		}
	}
	if _, ok := result.Fields["missing"]; ok {
		t.Error("unresolved path should be omitted, not present")
	}
	if result.Artwork["poster"] != "https://img.invalid/front.jpg" {
		t.Errorf("poster = %q", result.Artwork["poster"])
	}
}

func TestLookupHTTPGraphQL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		var body struct{ Query string }
		if err := jsonDecode(r, &body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.Query, `signal-9`) {
			t.Errorf("query missing substituted id: %q", body.Query)
		}
		fmt.Fprint(w, `{"data": {"node": {"name": "Signal Nine", "made": 2003}}}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID:             "graphsource",
		Kind:           "http-graphql",
		Endpoint:       server.URL,
		LookupTemplate: `query { node(ref: "{id}") { name made } }`,
		FieldMap:       map[string]string{"title": "node.name", "year": "node.made"},
	}, "signal-9")
	if err != nil {
		t.Fatal(err)
	}
	if result.Fields["title"] != "Signal Nine" || result.Fields["year"] != 2003.0 {
		t.Errorf("fields = %v", result.Fields)
	}
}

func TestGraphQLValueEscaperEscapesControlCharacters(t *testing.T) {
	query := `query { search(term: "` + graphQLValueEscaper.Replace("line\ncarriage\rtab\tform\fquote\"slash\\") + `") { id } }`
	payload, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(body.Query, "\n\r\t\f") {
		t.Fatalf("query contains raw control character: %q", body.Query)
	}
	if want := `query { search(term: "line\ncarriage\rtab\tform\fquote\"slash\\") { id } }`; body.Query != want {
		t.Fatalf("query = %q, want %q", body.Query, want)
	}
}

// TestSearchCandidatesThenLookup walks the whole ADR-0003 §4 identification
// chain against one local server: SearchTemplate → candidates (FieldMap per
// element, transform applied, artwork composed onto ArtworkBaseURL,
// CandidateIDPath as the handle) → the chosen candidate's ID feeds Lookup.
// Auth rides the "query:<param>" style on both legs.
func TestSearchCandidatesThenLookup(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/charts/search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "vega & prime" {
			t.Errorf("q = %q, want the raw query (no injection split)", got)
		}
		if got := r.URL.Query().Get("api_key"); got != "s3cret" {
			t.Errorf("api_key = %q", got)
		}
		fmt.Fprint(w, `{"results": [
			{"id": 42, "name": "Vega Prime", "released": "1997-03-09", "art": "/vega.jpg"},
			{"id": 43, "name": "Vega Secundus", "released": "2003-11-20", "art": "/secundus.jpg"}
		]}`)
	})
	mux.HandleFunc("/charts/entry/42", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("api_key"); got != "s3cret" {
			t.Errorf("lookup api_key = %q", got)
		}
		fmt.Fprint(w, `{"id": 42, "name": "Vega Prime", "released": "1997-03-09",
			"art": "/vega.jpg", "log": "The full survey entry."}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	spec := plugin.ScraperSpec{
		ID:              "starcharts",
		Kind:            "http-json",
		Endpoint:        server.URL,
		AuthRef:         "starcharts-key",
		AuthStyle:       "query:api_key",
		SearchTemplate:  "/charts/search?q={query}",
		CandidatesPath:  "results",
		CandidateIDPath: "id",
		LookupTemplate:  "/charts/entry/{id}",
		FieldMap: map[string]string{
			"title":   "name",
			"year":    "released",
			"summary": "log",
		},
		Transforms:     map[string]string{"year": "year-from-date"},
		ArtworkMap:     map[string]string{"poster": "art"},
		ArtworkBaseURL: "https://img.example/w500",
	}
	engine := New(WithInsecureLoopback(), WithCredentials(staticCredentials{"starcharts-key": "s3cret"}))

	candidates, err := engine.Search(context.Background(), spec, "vega & prime")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("got %d candidates, want 2", len(candidates))
	}
	first := candidates[0]
	if first.ID != "42" {
		t.Errorf("candidate ID = %q, want 42", first.ID)
	}
	if first.Fields["title"] != "Vega Prime" || first.Fields["year"] != 1997.0 {
		t.Errorf("candidate fields = %v", first.Fields)
	}
	if _, ok := first.Fields["summary"]; ok {
		t.Error("search element has no `log`; summary should be absent")
	}
	if first.Artwork["poster"] != "https://img.example/w500/vega.jpg" {
		t.Errorf("candidate poster = %q, want the composed CDN URL", first.Artwork["poster"])
	}
	if candidates[1].ID != "43" || candidates[1].Fields["year"] != 2003.0 {
		t.Errorf("second candidate = %+v", candidates[1])
	}

	full, err := engine.Lookup(context.Background(), spec, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if full.Fields["summary"] != "The full survey entry." || full.Fields["year"] != 1997.0 {
		t.Errorf("lookup fields = %v", full.Fields)
	}
	if full.ID != "" {
		t.Errorf("lookup Result.ID = %q, want empty (IDs are a search concern)", full.ID)
	}
}

func TestSearchPaginatesAndSpacesPageRequests(t *testing.T) {
	var mu sync.Mutex
	pages := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		mu.Lock()
		pages = append(pages, page)
		mu.Unlock()
		fmt.Fprintf(w, `{"results": [{"id": %q, "name": "Page %s"}]}`, page, page)
	}))
	defer server.Close()

	clock := newManualClock()
	engine := New(WithInsecureLoopback())
	engine.clock = clock
	done := make(chan struct {
		results []Result
		err     error
	}, 1)
	go func() {
		results, err := engine.Search(context.Background(), plugin.ScraperSpec{
			ID: "paged", Kind: "http-json", Endpoint: server.URL,
			SearchTemplate:       "/search?q={query}&page={page}",
			MaxPages:             2,
			CandidatesPath:       "results",
			CandidateIDPath:      "id",
			FieldMap:             map[string]string{"title": "name"},
			MinRequestIntervalMS: 60,
		}, "vega")
		done <- struct {
			results []Result
			err     error
		}{results: results, err: err}
	}()
	<-clock.waitCreated
	mu.Lock()
	gotPages := append([]string(nil), pages...)
	mu.Unlock()
	if len(clock.waits) != 1 || clock.waits[0] != 60*time.Millisecond {
		t.Fatalf("waits = %v, want one 60ms wait before page 2", clock.waits)
	}
	if len(gotPages) != 1 || gotPages[0] != "1" {
		t.Fatalf("pages before releasing wait = %v, want only page 1", gotPages)
	}
	clock.advance(60 * time.Millisecond)
	clock.releaseLast()
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if len(got.results) != 2 || got.results[0].ID != "1" || got.results[1].ID != "2" {
		t.Fatalf("results = %#v", got.results)
	}
}

// TestSearchMapsDivergeFromLookupMaps pins the per-template maps: a source
// whose search elements (flat) and lookup document (data.-enveloped) diverge
// declares SearchFieldMap/SearchArtworkMap — Search resolves candidates
// through the search maps, Lookup keeps FieldMap/ArtworkMap, and the one
// Transforms vocabulary applies by field name on both legs.
func TestSearchMapsDivergeFromLookupMaps(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/atlas/search", func(w http.ResponseWriter, r *http.Request) {
		// Search shape: flat elements, artwork as an absolute URL.
		fmt.Fprint(w, `{"matches": [
			{"atlas_id": "a-7", "label": "Comet Run", "debut": "1988-07-02",
			 "art_url": "https://art.example/comet.jpg"}
		]}`)
	})
	mux.HandleFunc("/atlas/entry/a-7", func(w http.ResponseWriter, r *http.Request) {
		// Lookup shape: data envelope, different key names, relative art.
		fmt.Fprint(w, `{"status": "ok", "data": {"name": "Comet Run",
			"chronicle": "The long entry.", "debut": "1988-07-02",
			"image": "/full/comet.jpg"}}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	spec := plugin.ScraperSpec{
		ID:              "staratlas",
		Kind:            "http-json",
		Endpoint:        server.URL,
		SearchTemplate:  "/atlas/search?q={query}",
		CandidatesPath:  "matches",
		CandidateIDPath: "atlas_id",
		LookupTemplate:  "/atlas/entry/{id}",
		FieldMap: map[string]string{
			"title":   "data.name",
			"summary": "data.chronicle",
			"year":    "data.debut",
		},
		ArtworkMap:       map[string]string{"poster": "data.image"},
		SearchFieldMap:   map[string]string{"title": "label", "year": "debut"},
		SearchArtworkMap: map[string]string{"poster": "art_url"},
		Transforms:       map[string]string{"year": "year-from-date"},
		ArtworkBaseURL:   "https://img.example/w500",
	}
	engine := New(WithInsecureLoopback())

	candidates, err := engine.Search(context.Background(), spec, "comet")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(candidates))
	}
	candidate := candidates[0]
	if candidate.ID != "a-7" {
		t.Errorf("candidate ID = %q, want a-7", candidate.ID)
	}
	if candidate.Fields["title"] != "Comet Run" || candidate.Fields["year"] != 1988.0 {
		t.Errorf("candidate fields = %v, want search-map title + transformed year", candidate.Fields)
	}
	if _, ok := candidate.Fields["summary"]; ok {
		t.Error("summary is a lookup-map field; candidates must not resolve it")
	}
	if candidate.Artwork["poster"] != "https://art.example/comet.jpg" {
		t.Errorf("candidate poster = %q, want the search map's absolute URL", candidate.Artwork["poster"])
	}

	full, err := engine.Lookup(context.Background(), spec, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if full.Fields["title"] != "Comet Run" || full.Fields["summary"] != "The long entry." {
		t.Errorf("lookup fields = %v, want the data.-enveloped values", full.Fields)
	}
	if full.Fields["year"] != 1988.0 {
		t.Errorf("lookup year = %v, want the shared transform applied on the lookup leg too", full.Fields["year"])
	}
	if full.Artwork["poster"] != "https://img.example/w500/full/comet.jpg" {
		t.Errorf("lookup poster = %q, want the lookup map's relative path composed onto the base", full.Artwork["poster"])
	}
}

// TestSearchMapsFallBackPerMap pins the independent fallback: overriding
// fields alone leaves artwork resolving through the shared ArtworkMap.
func TestSearchMapsFallBackPerMap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"matches": [{"atlas_id": "a-1", "label": "Drift", "art": "/drift.jpg"}]}`)
	}))
	defer server.Close()

	candidates, err := New(WithInsecureLoopback()).Search(context.Background(), plugin.ScraperSpec{
		ID:              "halfatlas",
		Kind:            "http-json",
		Endpoint:        server.URL,
		SearchTemplate:  "/search?q={query}",
		CandidatesPath:  "matches",
		CandidateIDPath: "atlas_id",
		LookupTemplate:  "/entry/{id}",
		FieldMap:        map[string]string{"title": "data.name"},
		ArtworkMap:      map[string]string{"poster": "art"},
		SearchFieldMap:  map[string]string{"title": "label"},
	}, "drift")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Fields["title"] != "Drift" {
		t.Fatalf("candidates = %+v, want the search-map title", candidates)
	}
	if candidates[0].Artwork["poster"] != "/drift.jpg" {
		t.Errorf("poster = %q, want the shared ArtworkMap to keep serving search", candidates[0].Artwork["poster"])
	}
}

func TestSearchGraphQLCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Query string }
		if err := jsonDecode(r, &body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.Query, "beacon") {
			t.Errorf("query missing substituted term: %q", body.Query)
		}
		fmt.Fprint(w, `{"data": {"searchEntry": [
			{"ref": "e-1", "name": "Beacon Drift", "made": "2009-05-01"},
			{"ref": "e-2", "name": "Beacon Rise", "made": "2011-08-12"}
		]}}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	candidates, err := engine.Search(context.Background(), plugin.ScraperSpec{
		ID:              "graphsource",
		Kind:            "http-graphql",
		Endpoint:        server.URL,
		SearchTemplate:  `query Search { searchEntry(term: "{query}") { ref name made } }`,
		CandidatesPath:  "searchEntry",
		CandidateIDPath: "ref",
		FieldMap:        map[string]string{"title": "name", "year": "made"},
		Transforms:      map[string]string{"year": "year-from-date"},
	}, "beacon")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("got %d candidates, want 2", len(candidates))
	}
	if candidates[0].ID != "e-1" || candidates[0].Fields["title"] != "Beacon Drift" || candidates[0].Fields["year"] != 2009.0 {
		t.Errorf("first candidate = %+v", candidates[0])
	}
}

func TestSearchRefusedWithoutTemplateOrOnRSS(t *testing.T) {
	engine := New(WithInsecureLoopback())
	if _, err := engine.Search(context.Background(), plugin.ScraperSpec{
		ID: "x", Kind: "http-json", Endpoint: "https://x.example", LookupTemplate: "/x/{id}",
	}, "q"); err == nil || !strings.Contains(err.Error(), "no search template") {
		t.Fatalf("expected no-search-template error, got %v", err)
	}
	if _, err := engine.Search(context.Background(), plugin.ScraperSpec{
		ID: "x", Kind: "rss", Endpoint: "https://x.example",
		SearchTemplate: "/find?q={query}", CandidatesPath: "item",
	}, "q"); err == nil || !strings.Contains(err.Error(), "do not search") {
		t.Fatalf("expected rss-search refusal, got %v", err)
	}
}

func TestSearchCandidatesCapped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"results": [`)
		for i := 0; i < 40; i++ {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"id": %d}`, i)
		}
		fmt.Fprint(w, `]}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	candidates, err := engine.Search(context.Background(), plugin.ScraperSpec{
		ID: "bigsearch", Kind: "http-json", Endpoint: server.URL,
		SearchTemplate: "/search?q={query}", CandidatesPath: "results", CandidateIDPath: "id",
	}, "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != maxCandidates {
		t.Fatalf("got %d candidates, want the %d cap", len(candidates), maxCandidates)
	}
}

func TestLookupRetries429WithRetryAfter(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"name": "Vega Prime"}`)
	}))
	defer server.Close()

	clock := newManualClock()
	engine := New(WithInsecureLoopback())
	engine.clock = clock
	done := make(chan error, 1)
	go func() {
		_, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
			ID: "retrysource", Kind: "http-json", Endpoint: server.URL, LookupTemplate: "/{id}",
			FieldMap: map[string]string{"title": "name"},
		}, "1")
		done <- err
	}()

	<-clock.waitCreated
	if len(clock.waits) != 1 || clock.waits[0] != 2*time.Second {
		t.Fatalf("waits = %v, want one 2s Retry-After wait", clock.waits)
	}
	clock.advance(2 * time.Second)
	clock.releaseLast()
	if err := <-done; err != nil {
		t.Fatalf("Lookup returned error: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestLookupRetries429WithBoundedExponentialAndCap(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	clock := newManualClock()
	engine := New(WithInsecureLoopback())
	engine.clock = clock
	done := make(chan error, 1)
	go func() {
		_, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
			ID: "retrysource", Kind: "http-json", Endpoint: server.URL, LookupTemplate: "/{id}",
			FieldMap: map[string]string{"title": "name"},
		}, "1")
		done <- err
	}()

	wantWaits := []time.Duration{25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond}
	for i, want := range wantWaits {
		<-clock.waitCreated
		if len(clock.waits) != i+1 || clock.waits[i] != want {
			t.Fatalf("waits = %v, want retry %d to wait %v", clock.waits, i+1, want)
		}
		clock.advance(want)
		clock.releaseLast()
	}
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "status 429") {
		t.Fatalf("err = %v, want surfaced 429 after capped retries", err)
	}
	if requests != 4 {
		t.Fatalf("requests = %d, want 4 attempts (initial + 3 retries)", requests)
	}
}

const testFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">
  <channel>
    <title>Signal Watch</title>
    <itunes:image href="https://img.invalid/cover.jpg"/>
    <item>
      <title>Aurora Run</title>
      <enclosure url="https://cdn.invalid/aurora.mp3" length="1" type="audio/mpeg"/>
    </item>
    <item>
      <title>Beacon Drift</title>
    </item>
  </channel>
</rss>`

func TestLookupRSSIDIsTheURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/feed.xml" {
			t.Errorf("path = %q", r.URL.Path)
		}
		fmt.Fprint(w, testFeed)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID:             "feedsource",
		Kind:           "rss",
		Endpoint:       server.URL,
		LookupTemplate: "{id}", // the id IS the feed URL
		FieldMap: map[string]string{
			"title":   "channel.title",
			"summary": "item.0.title",
			"url":     "item.0.enclosure.url",
		},
		ArtworkMap: map[string]string{"thumb": "itunes:image@href"},
	}, server.URL+"/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"title":   "Signal Watch",
		"summary": "Aurora Run",
		"url":     "https://cdn.invalid/aurora.mp3",
	}
	for field, value := range want {
		if result.Fields[field] != value {
			t.Errorf("field %q = %q, want %q", field, result.Fields[field], value)
		}
	}
	if result.Artwork["thumb"] != "https://img.invalid/cover.jpg" {
		t.Errorf("thumb = %q", result.Artwork["thumb"])
	}
}

func TestAuthRefSendsBearer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer s3cret" {
			t.Errorf("Authorization = %q", got)
		}
		fmt.Fprint(w, `{"name": "x"}`)
	}))
	defer server.Close()

	spec := plugin.ScraperSpec{
		ID:             "authsource",
		Kind:           "http-json",
		Endpoint:       server.URL,
		LookupTemplate: "/{id}",
		AuthRef:        "authsource-token",
		FieldMap:       map[string]string{"title": "name"},
	}

	engine := New(WithInsecureLoopback(), WithCredentials(staticCredentials{"authsource-token": "s3cret"}))
	if _, err := engine.Lookup(context.Background(), spec, "1"); err != nil {
		t.Fatal(err)
	}

	// Without a CredentialSource the same spec must fail before any request.
	bare := New(WithInsecureLoopback())
	if _, err := bare.Lookup(context.Background(), spec, "1"); err == nil {
		t.Fatal("expected error when AuthRef is set but no CredentialSource is configured")
	}
}

func TestAuthStyleHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Api-Key"); got != "s3cret" {
			t.Errorf("X-Api-Key = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want unset for header style", got)
		}
		fmt.Fprint(w, `{"name": "x"}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback(), WithCredentials(staticCredentials{"keysource-token": "s3cret"}))
	_, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID: "keysource", Kind: "http-json", Endpoint: server.URL,
		LookupTemplate: "/{id}", AuthRef: "keysource-token", AuthStyle: "header:X-Api-Key",
		FieldMap: map[string]string{"title": "name"},
	}, "1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuthStyleQueryTransportErrorRedactsKey(t *testing.T) {
	engine := New(WithCredentials(staticCredentials{"google-books-key": "GOOGLE-BOOKS-KEY"}))
	engine.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.Query().Get("key"); got != "GOOGLE-BOOKS-KEY" {
			t.Fatalf("key = %q", got)
		}
		return nil, fmt.Errorf("simulated transport failure")
	})}

	_, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID:             "books",
		Kind:           "http-json",
		Endpoint:       "https://books.example.test",
		LookupTemplate: "/volumes?q={id}",
		AuthRef:        "google-books-key",
		AuthStyle:      "query:key",
		FieldMap:       map[string]string{"title": "title"},
	}, "dune")
	if err == nil {
		t.Fatal("Lookup succeeded, want transport error")
	}
	if msg := err.Error(); strings.Contains(msg, "GOOGLE-BOOKS-KEY") || strings.Contains(msg, "key=") {
		t.Fatalf("transport error leaked query credential: %v", err)
	}
}

// TestAuthStyleLoginBearer pins the declarative TVDB-shaped exchange: POST
// {SecretField: secret} to Login.Path, read Login.TokenPath, send it as the
// bearer token — and reuse the cached token instead of logging in per call.
func TestAuthStyleLoginBearer(t *testing.T) {
	logins := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		logins++
		if r.Method != http.MethodPost {
			t.Errorf("login method = %q, want POST", r.Method)
		}
		var body map[string]string
		if err := jsonDecode(r, &body); err != nil {
			t.Fatal(err)
		}
		if body["apikey"] != "s3cret" {
			t.Errorf("login body = %v", body)
		}
		fmt.Fprint(w, `{"status": "success", "data": {"token": "jwt-abc"}}`)
	})
	mux.HandleFunc("/series/9", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer jwt-abc" {
			t.Errorf("Authorization = %q, want the exchanged token", got)
		}
		fmt.Fprint(w, `{"data": {"name": "Beacon Drift"}}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	spec := plugin.ScraperSpec{
		ID: "tokensource", Kind: "http-json", Endpoint: server.URL,
		AuthRef: "tokensource-key", AuthStyle: "login-bearer",
		Login:          &plugin.LoginSpec{Path: "/login", SecretField: "apikey", TokenPath: "data.token"},
		LookupTemplate: "/series/{id}",
		FieldMap:       map[string]string{"title": "data.name"},
	}
	engine := New(WithInsecureLoopback(), WithCredentials(staticCredentials{"tokensource-key": "s3cret"}))
	for i := 0; i < 2; i++ {
		result, err := engine.Lookup(context.Background(), spec, "9")
		if err != nil {
			t.Fatal(err)
		}
		if result.Fields["title"] != "Beacon Drift" {
			t.Errorf("title = %q", result.Fields["title"])
		}
	}
	if logins != 1 {
		t.Fatalf("login ran %d times across two lookups, want 1 (cached token)", logins)
	}
}

// TestInvalidateCredentialClearsLoginBearerCache pins WEB-5H rotation/disable:
// an AuthRef-scoped credential change invalidates every login-bearer token
// cached for that ref, so the next request exchanges the secret again.
func TestInvalidateCredentialClearsLoginBearerCache(t *testing.T) {
	logins := 0
	current := ""
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		logins++
		current = fmt.Sprintf("jwt-%d", logins)
		fmt.Fprintf(w, `{"data": {"token": %q}}`, current)
	})
	mux.HandleFunc("/series/9", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+current {
			t.Errorf("Authorization = %q, want Bearer %s", got, current)
		}
		fmt.Fprint(w, `{"data": {"name": "Beacon Drift"}}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	spec := plugin.ScraperSpec{
		ID: "tokensource", Kind: "http-json", Endpoint: server.URL,
		AuthRef: "tokensource-key", AuthStyle: "login-bearer",
		Login:          &plugin.LoginSpec{Path: "/login", SecretField: "apikey", TokenPath: "data.token"},
		LookupTemplate: "/series/{id}",
		FieldMap:       map[string]string{"title": "data.name"},
	}
	engine := New(WithInsecureLoopback(), WithCredentials(staticCredentials{"tokensource-key": "s3cret"}))
	for i := 0; i < 2; i++ {
		if _, err := engine.Lookup(context.Background(), spec, "9"); err != nil {
			t.Fatal(err)
		}
	}
	if logins != 1 {
		t.Fatalf("login ran %d times before invalidation, want 1", logins)
	}
	engine.InvalidateCredential("tokensource-key")
	if _, err := engine.Lookup(context.Background(), spec, "9"); err != nil {
		t.Fatal(err)
	}
	if logins != 2 {
		t.Fatalf("login ran %d times after invalidation, want 2", logins)
	}
}

// TestLoginBearerReloginOn401 pins the expired-token path: a 401 on an
// authed request invalidates the cached token, re-runs the login exchange
// once, and retries the request with the fresh token.
func TestLoginBearerReloginOn401(t *testing.T) {
	logins := 0
	valid := ""
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		logins++
		valid = fmt.Sprintf("jwt-%d", logins)
		fmt.Fprintf(w, `{"data": {"token": %q}}`, valid)
	})
	mux.HandleFunc("/series/9", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+valid {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"data": {"name": "Beacon Drift"}}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	spec := plugin.ScraperSpec{
		ID: "tokensource", Kind: "http-json", Endpoint: server.URL,
		AuthRef: "tokensource-key", AuthStyle: "login-bearer",
		Login:          &plugin.LoginSpec{Path: "/login", SecretField: "apikey", TokenPath: "data.token"},
		LookupTemplate: "/series/{id}",
		FieldMap:       map[string]string{"title": "data.name"},
	}
	engine := New(WithInsecureLoopback(), WithCredentials(staticCredentials{"tokensource-key": "s3cret"}))
	if _, err := engine.Lookup(context.Background(), spec, "9"); err != nil {
		t.Fatal(err)
	}
	// The issuer expires jwt-1 server-side; the cached token is now stale.
	valid = "jwt-1-expired"
	result, err := engine.Lookup(context.Background(), spec, "9")
	if err != nil {
		t.Fatalf("lookup after server-side expiry: %v", err)
	}
	if result.Fields["title"] != "Beacon Drift" {
		t.Errorf("title = %q", result.Fields["title"])
	}
	if logins != 2 {
		t.Fatalf("login ran %d times, want 2 (initial + relogin after 401)", logins)
	}
}

// A 401 that survives the one relogin retry surfaces as the error — the
// engine never loops the exchange.
func TestLoginBearerSecond401Surfaces(t *testing.T) {
	logins := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		logins++
		fmt.Fprintf(w, `{"data": {"token": "jwt-%d"}}`, logins)
	})
	mux.HandleFunc("/series/9", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	spec := plugin.ScraperSpec{
		ID: "tokensource", Kind: "http-json", Endpoint: server.URL,
		AuthRef: "tokensource-key", AuthStyle: "login-bearer",
		Login:          &plugin.LoginSpec{Path: "/login", SecretField: "apikey", TokenPath: "data.token"},
		LookupTemplate: "/series/{id}",
		FieldMap:       map[string]string{"title": "data.name"},
	}
	engine := New(WithInsecureLoopback(), WithCredentials(staticCredentials{"tokensource-key": "s3cret"}))
	_, err := engine.Lookup(context.Background(), spec, "9")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want a surfaced 401", err)
	}
	if logins != 2 {
		t.Fatalf("login ran %d times, want 2 (the retry is once, not a loop)", logins)
	}
}

// TestUserAgentSentOnEveryRequest pins the etiquette header on each request
// shape the engine makes: search, lookup, the login exchange, and
// FetchResource (MusicBrainz throttles UA-less clients).
func TestUserAgentSentOnEveryRequest(t *testing.T) {
	agents := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		agents["/login"] = r.Header.Get("User-Agent")
		fmt.Fprint(w, `{"data": {"token": "jwt-abc"}}`)
	})
	mux.HandleFunc("/find", func(w http.ResponseWriter, r *http.Request) {
		agents["/find"] = r.Header.Get("User-Agent")
		fmt.Fprint(w, `{"results": [{"id": "9", "name": "Vega Prime"}]}`)
	})
	mux.HandleFunc("/item/9", func(w http.ResponseWriter, r *http.Request) {
		agents["/item/9"] = r.Header.Get("User-Agent")
		fmt.Fprint(w, `{"name": "Vega Prime"}`)
	})
	mux.HandleFunc("/poster.jpg", func(w http.ResponseWriter, r *http.Request) {
		agents["/poster.jpg"] = r.Header.Get("User-Agent")
		fmt.Fprint(w, "bytes")
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	spec := plugin.ScraperSpec{
		ID: "tokensource", Kind: "http-json", Endpoint: server.URL,
		AuthRef: "tokensource-key", AuthStyle: "login-bearer",
		Login:           &plugin.LoginSpec{Path: "/login", SecretField: "apikey", TokenPath: "data.token"},
		SearchTemplate:  "/find?q={query}",
		CandidatesPath:  "results",
		CandidateIDPath: "id",
		LookupTemplate:  "/item/{id}",
		FieldMap:        map[string]string{"title": "name"},
	}
	engine := New(WithInsecureLoopback(), WithCredentials(staticCredentials{"tokensource-key": "s3cret"}))
	if _, err := engine.Search(context.Background(), spec, "vega"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Lookup(context.Background(), spec, "9"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.FetchResource(context.Background(), server.URL+"/poster.jpg"); err != nil {
		t.Fatal(err)
	}
	want := "beamfall/dev (+https://beamfall.com)"
	for _, path := range []string{"/login", "/find", "/item/9", "/poster.jpg"} {
		if agents[path] != want {
			t.Errorf("User-Agent on %s = %q, want %q", path, agents[path], want)
		}
	}
}

// TestMinRequestIntervalSpacesRequests pins the declarative rate limit: two
// requests for the same scraper ID are at least the interval apart.
func TestMinRequestIntervalSpacesRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"name": "Vega Prime"}`)
	}))
	defer server.Close()

	spec := plugin.ScraperSpec{
		ID: "slowsource", Kind: "http-json", Endpoint: server.URL, LookupTemplate: "/{id}",
		FieldMap:             map[string]string{"title": "name"},
		MinRequestIntervalMS: 60,
	}
	clock := newManualClock()
	engine := New(WithInsecureLoopback())
	engine.clock = clock
	if _, err := engine.Lookup(context.Background(), spec, "1"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := engine.Lookup(context.Background(), spec, "1")
		done <- err
	}()
	<-clock.waitCreated
	if len(clock.waits) != 1 || clock.waits[0] != 60*time.Millisecond {
		t.Fatalf("waits = %v, want one 60ms limiter wait", clock.waits)
	}
	clock.advance(60 * time.Millisecond)
	clock.releaseLast()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The rate-limit anchor is per scraper ID — one slow source never throttles
// another — and a cancelled context ends the wait instead of sleeping it out.
func TestMinRequestIntervalPerIDAndContextCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"name": "Vega Prime"}`)
	}))
	defer server.Close()

	spec := func(id string) plugin.ScraperSpec {
		return plugin.ScraperSpec{
			ID: id, Kind: "http-json", Endpoint: server.URL, LookupTemplate: "/{id}",
			FieldMap:             map[string]string{"title": "name"},
			MinRequestIntervalMS: 5000, // long enough that any wait here would fail the test
		}
	}
	clock := newManualClock()
	engine := New(WithInsecureLoopback())
	engine.clock = clock
	if _, err := engine.Lookup(context.Background(), spec("slowsource"), "1"); err != nil {
		t.Fatal(err)
	}
	// A different scraper ID has its own anchor: no wait, well inside 1s.
	otherCtx, cancelOther := context.WithTimeout(context.Background(), time.Second)
	defer cancelOther()
	if _, err := engine.Lookup(otherCtx, spec("othersource"), "1"); err != nil {
		t.Fatalf("different scraper ID was throttled: %v", err)
	}
	// The same ID inside its interval waits — and honors cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := engine.Lookup(ctx, spec("slowsource"), "1")
		done <- err
	}()
	<-clock.waitCreated
	cancel()
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "rate-limit wait") {
		t.Fatalf("err = %v, want a cancelled rate-limit wait", err)
	}
}

func TestTransforms(t *testing.T) {
	cases := []struct {
		kind, in, want string
		ok             bool
	}{
		{"year-from-date", "2014-09-05", "2014", true},
		{"year-from-date", "1997", "1997", true},
		{"year-from-date", "Wed, 16 Jul 2010 12:00:00 -0700", "2010", true},
		{"year-from-date", "not a date", "", false},
		{"date-from-rfc822", "Fri, 05 Sep 2014 08:30:00 GMT", "2014-09-05", true},
		{"date-from-rfc822", "Fri, 5 Sep 2014 08:30:00 +0200", "2014-09-05", true},
		{"date-from-rfc822", "2014-09-05", "2014-09-05", true},
		{"date-from-rfc822", "soonish", "", false},
		{"seconds-from-clock", "53:35", "3215", true},
		{"seconds-from-clock", "1:02:03", "3723", true},
		{"seconds-from-clock", "3215", "3215", true},
		{"seconds-from-clock", "1:2:3:4", "", false},
		{"seconds-from-clock", "twelve", "", false},
	}
	for _, c := range cases {
		got, ok := transform(c.kind, c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("transform(%q, %q) = (%q, %v), want (%q, %v)", c.kind, c.in, got, ok, c.want, c.ok)
		}
	}
}

// An unparseable value under a declared transform is omitted, like an
// unresolved path — never passed through raw.
func TestTransformFailureOmitsField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"name": "Vega Prime", "released": "unknown"}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID: "starcharts", Kind: "http-json", Endpoint: server.URL, LookupTemplate: "/{id}",
		FieldMap:   map[string]string{"title": "name", "year": "released"},
		Transforms: map[string]string{"year": "year-from-date"},
	}, "1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.Fields["year"]; ok {
		t.Errorf("year = %q, want omitted for unparseable input", result.Fields["year"])
	}
	if result.Fields["title"] != "Vega Prime" {
		t.Errorf("title = %q", result.Fields["title"])
	}
}

func TestTypedFieldCoercionSkipsBadFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"name": "Vega Prime", "year": 1997, "score": "8.5", "active": true, "bad": "soonish"}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID: "typesource", Kind: "http-json", Endpoint: server.URL, LookupTemplate: "/{id}",
		FieldMap:   map[string]string{"title": "name", "year": "year", "score": "score", "active": "active", "bad": "bad"},
		FieldTypes: map[string]string{"year": "number", "score": "number", "active": "flag", "bad": "number"},
	}, "1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Fields["title"] != "Vega Prime" || result.Fields["year"] != 1997.0 ||
		result.Fields["score"] != 8.5 || result.Fields["active"] != true {
		t.Fatalf("fields = %#v", result.Fields)
	}
	if _, ok := result.Fields["bad"]; ok {
		t.Fatalf("bad field = %#v, want skipped", result.Fields["bad"])
	}
}

func TestResponseSizeCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"pad": %q}`, strings.Repeat("x", 2048))
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback(), WithMaxResponseBytes(1024))
	_, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID: "bigsource", Kind: "http-json", Endpoint: server.URL, LookupTemplate: "/{id}",
	}, "1")
	if err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("expected size-cap error, got %v", err)
	}
}

func TestUnknownKindRefused(t *testing.T) {
	engine := New()
	_, err := engine.Lookup(context.Background(), plugin.ScraperSpec{ID: "x", Kind: "ftp", LookupTemplate: "/{id}"}, "1")
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestLookupRefusedWithoutTemplate(t *testing.T) {
	engine := New()
	_, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID: "x", Kind: "http-json", Endpoint: "https://x.example",
		SearchTemplate: "/find?q={query}", CandidatesPath: "results",
	}, "1")
	if err == nil || !strings.Contains(err.Error(), "no lookup template") {
		t.Fatalf("expected no-lookup-template error, got %v", err)
	}
}

func jsonDecode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// Lookup resolves the spec's EntityMaps against the lookup document: list
// paths with a NamePath, scalar paths without one, scalar-list elements,
// unresolved paths and blank names omitted — in declared-map then document
// order.
func TestLookupResolvesEntityMaps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{
			"name": "Vega Prime",
			"crew": [{"name": "Ana"}, {"name": "Brik"}, {"name": "  "}, {"rank": "captain"}],
			"flagship": "Dauntless",
			"ports": ["Meridian", "Kestrel Station"]
		}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID:             "starcharts",
		Kind:           "http-json",
		Endpoint:       server.URL,
		LookupTemplate: "/lookup/{id}",
		FieldMap:       map[string]string{"title": "name"},
		EntityMaps: []plugin.EntityMap{
			{Kind: "navigator", Relation: "logged-by", Path: "crew", NamePath: "name"},
			{Kind: "vessel", Relation: "sailed-by", Path: "flagship"},
			{Kind: "port", Relation: "visited-by", Path: "ports"},
			{Kind: "ghost", Relation: "haunts", Path: "nope.nothing"},
		},
	}, "vega-prime")
	if err != nil {
		t.Fatal(err)
	}
	want := []EntityResult{
		{Kind: "navigator", Relation: "logged-by", Name: "Ana", SortOrder: scrapeIntPtr(0)},
		{Kind: "navigator", Relation: "logged-by", Name: "Brik", SortOrder: scrapeIntPtr(1)},
		{Kind: "vessel", Relation: "sailed-by", Name: "Dauntless"},
		{Kind: "port", Relation: "visited-by", Name: "Meridian", SortOrder: scrapeIntPtr(0)},
		{Kind: "port", Relation: "visited-by", Name: "Kestrel Station", SortOrder: scrapeIntPtr(1)},
	}
	if len(result.Entities) != len(want) {
		t.Fatalf("entities = %#v, want %#v", result.Entities, want)
	}
	for i, entity := range want {
		if !entityResultEqual(result.Entities[i], entity) {
			t.Fatalf("entities[%d] = %#v, want %#v", i, result.Entities[i], entity)
		}
	}
}

func scrapeIntPtr(value int) *int {
	return &value
}

func entityResultEqual(got, want EntityResult) bool {
	if got.Kind != want.Kind || got.Relation != want.Relation || got.Name != want.Name || got.ArtworkURL != want.ArtworkURL {
		return false
	}
	if got.SortOrder == nil || want.SortOrder == nil {
		return got.SortOrder == nil && want.SortOrder == nil
	}
	return *got.SortOrder == *want.SortOrder
}

// EntityMap ArtworkPaths resolve element-relative and compose with
// ArtworkBaseURL like ArtworkMap values: site-relative paths get the base
// prefixed, absolute URLs pass through, unresolved or blank paths leave
// ArtworkURL empty.
func TestLookupResolvesEntityMapArtworkPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{
			"crew": [
				{"name": "Ana", "badge": "/badges/ana.jpg"},
				{"name": "Brik", "badge": "https://elsewhere.example/brik.png"},
				{"name": "Cole"},
				{"name": "Dara", "badge": ""}
			]
		}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID:             "starcharts",
		Kind:           "http-json",
		Endpoint:       server.URL,
		LookupTemplate: "/lookup/{id}",
		ArtworkBaseURL: "https://img.example.test/full/",
		EntityMaps: []plugin.EntityMap{
			{Kind: "navigator", Relation: "logged-by", Path: "crew", NamePath: "name", ArtworkPath: "badge"},
		},
	}, "vega-prime")
	if err != nil {
		t.Fatal(err)
	}
	want := []EntityResult{
		{Kind: "navigator", Relation: "logged-by", Name: "Ana", ArtworkURL: "https://img.example.test/full/badges/ana.jpg", SortOrder: scrapeIntPtr(0)},
		{Kind: "navigator", Relation: "logged-by", Name: "Brik", ArtworkURL: "https://elsewhere.example/brik.png", SortOrder: scrapeIntPtr(1)},
		{Kind: "navigator", Relation: "logged-by", Name: "Cole", SortOrder: scrapeIntPtr(2)},
		{Kind: "navigator", Relation: "logged-by", Name: "Dara", SortOrder: scrapeIntPtr(3)},
	}
	if len(result.Entities) != len(want) {
		t.Fatalf("entities = %#v, want %#v", result.Entities, want)
	}
	for i, entity := range want {
		if !entityResultEqual(result.Entities[i], entity) {
			t.Fatalf("entities[%d] = %#v, want %#v", i, result.Entities[i], entity)
		}
	}
}

func TestLookupRendersArtworkTemplates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"title": "Vega Prime", "cover": 12345}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID:                  "starcharts",
		Kind:                "http-json",
		Endpoint:            server.URL,
		LookupTemplate:      "/lookup/{id}",
		FieldMap:            map[string]string{"title": "title"},
		ArtworkMap:          map[string]string{"poster": "cover"},
		ArtworkURLTemplates: map[string]string{"poster": "https://img.example.test/b/id/{value}-L.jpg"},
	}, "vega-prime")
	if err != nil {
		t.Fatal(err)
	}
	if result.Artwork["poster"] != "https://img.example.test/b/id/12345-L.jpg" {
		t.Fatalf("poster = %q, want rendered cover template", result.Artwork["poster"])
	}
}

func TestLookupResolvesEntityMapReferenceLookups(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/lookup/vega-prime":
			fmt.Fprint(w, `{"crew": [{"author": {"key": "/authors/OL1A"}}, {"author": {"key": "/authors/OL2A"}}]}`)
		case "/authors/OL1A.json":
			fmt.Fprint(w, `{"name": "Ana", "bio": "Captain."}`)
		case "/authors/OL2A.json":
			fmt.Fprint(w, `{"name": "Brik"}`)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID:             "starcharts",
		Kind:           "http-json",
		Endpoint:       server.URL,
		LookupTemplate: "/lookup/{id}",
		EntityMaps: []plugin.EntityMap{
			{Kind: "navigator", Relation: "logged-by", Path: "crew", LookupIDPath: "author.key", LookupTemplate: "{id}.json", NamePath: "name",
				FieldMap: map[string]string{"bio": "bio"}},
		},
	}, "vega-prime")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entities) != 2 {
		t.Fatalf("entities = %#v, want 2", result.Entities)
	}
	if result.Entities[0].Name != "Ana" || result.Entities[0].Fields["bio"] != "Captain." || result.Entities[0].SortOrder == nil || *result.Entities[0].SortOrder != 0 {
		t.Fatalf("entity 0 = %#v, want fetched Ana at order 0", result.Entities[0])
	}
	if result.Entities[1].Name != "Brik" || result.Entities[1].Fields != nil || result.Entities[1].SortOrder == nil || *result.Entities[1].SortOrder != 1 {
		t.Fatalf("entity 1 = %#v, want fetched Brik at order 1", result.Entities[1])
	}
}

func TestLookupCapsEntityMapReferenceLookups(t *testing.T) {
	var pathsMu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathsMu.Lock()
		paths = append(paths, r.URL.Path)
		pathsMu.Unlock()

		switch r.URL.Path {
		case "/lookup/anthology":
			fmt.Fprint(w, `{"authors": [
				{"author": {"key": "/authors/OL1A"}},
				{"author": {"key": "/authors/OL2A"}},
				{"author": {"key": "/authors/OL3A"}},
				{"author": {"key": "/authors/OL4A"}}
			]}`)
		case "/authors/OL1A.json":
			fmt.Fprint(w, `{"name": "Ana"}`)
		case "/authors/OL2A.json":
			fmt.Fprint(w, `{"name": "Brik"}`)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID:             "openlibrary",
		Kind:           "http-json",
		Endpoint:       server.URL,
		LookupTemplate: "/lookup/{id}",
		EntityMaps: []plugin.EntityMap{
			{Kind: "person", Relation: "credited-on", Path: "authors", LookupIDPath: "author.key", LookupTemplate: "{id}.json", MaxLookups: 2, NamePath: "name"},
		},
	}, "anthology")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entities) != 2 {
		t.Fatalf("entities = %#v, want 2 capped author entities", result.Entities)
	}
	pathsMu.Lock()
	defer pathsMu.Unlock()
	if len(paths) != 3 {
		t.Fatalf("requests = %d (%v), want 3: primary lookup plus 2 capped author lookups", len(paths), paths)
	}
	if paths[0] != "/lookup/anthology" || paths[1] != "/authors/OL1A.json" || paths[2] != "/authors/OL2A.json" {
		t.Fatalf("paths = %v, want only first 2 author lookups", paths)
	}
}

// EntityMap FieldMap paths resolve element-relative (raw strings for the
// consumer to type-parse) and ParentNamePath resolves a same-kind parent
// name; unresolved or blank paths leave the value empty.
func TestLookupResolvesEntityMapFieldsAndParent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{
			"label": {"name": "Sub Label", "founded": "1998", "parent": "Parent Label"},
			"crew": [
				{"name": "Ana", "bio": "Captain.", "born": "1990-04-01"},
				{"name": "Brik"}
			]
		}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID:             "starcharts",
		Kind:           "http-json",
		Endpoint:       server.URL,
		LookupTemplate: "/lookup/{id}",
		EntityMaps: []plugin.EntityMap{
			{Kind: "navigator", Relation: "logged-by", Path: "crew", NamePath: "name",
				FieldMap: map[string]string{"bio": "bio", "born": "born"}},
			{Kind: "house", Relation: "from-house", Path: "label", NamePath: "name",
				FieldMap:       map[string]string{"founded": "founded"},
				ParentNamePath: "parent", ParentRelation: "child-of"},
		},
	}, "vega-prime")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entities) != 3 {
		t.Fatalf("entities = %#v, want 3 (2 crew + 1 label)", result.Entities)
	}
	ana := result.Entities[0]
	if ana.Name != "Ana" || ana.Fields["bio"] != "Captain." || ana.Fields["born"] != "1990-04-01" {
		t.Fatalf("ana = %#v, want bio+born fields", ana)
	}
	// Brik resolved no field paths → nil Fields, never an empty map.
	if brik := result.Entities[1]; brik.Name != "Brik" || brik.Fields != nil {
		t.Fatalf("brik = %#v, want nil Fields", brik)
	}
	label := result.Entities[2]
	if label.Name != "Sub Label" || label.Fields["founded"] != "1998" ||
		label.ParentName != "Parent Label" || label.ParentRelation != "child-of" {
		t.Fatalf("label = %#v, want founded field + parent link", label)
	}
}

// Search candidates never carry entities — EntityMaps resolve against the
// lookup document only.
func TestSearchCandidatesCarryNoEntities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"hits": [{"id": 1, "name": "Vega Prime", "crew": [{"name": "Ana"}]}]}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	results, err := engine.Search(context.Background(), plugin.ScraperSpec{
		ID:              "starcharts",
		Kind:            "http-json",
		Endpoint:        server.URL,
		SearchTemplate:  "/find?q={query}",
		CandidatesPath:  "hits",
		CandidateIDPath: "id",
		LookupTemplate:  "/lookup/{id}",
		FieldMap:        map[string]string{"title": "name"},
		EntityMaps: []plugin.EntityMap{
			{Kind: "navigator", Relation: "logged-by", Path: "crew", NamePath: "name"},
		},
	}, "vega")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Entities != nil {
		t.Fatalf("search results = %#v, want one candidate with nil entities", results)
	}
}

func TestLookupSubstitutesFingerprintVariables(t *testing.T) {
	var gotURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		fmt.Fprint(w, `{"name": "Found"}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	_, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID:             "charts",
		Kind:           "http-json",
		Endpoint:       server.URL,
		LookupTemplate: "/by-print/{phash}?oshash={oshash}&duration={duration}",
		FieldMap:       map[string]string{"title": "name"},
	}, "ignored-id", Variables{OSHash: "00ff&12", PHash: "a1b2c3d4e5f60718", DurationSeconds: 5400})
	if err != nil {
		t.Fatal(err)
	}
	// Path-escaped in the path segment, query-escaped after "?" — the "&"
	// inside the value cannot inject an extra parameter.
	if want := "/by-print/a1b2c3d4e5f60718?oshash=00ff%2612&duration=5400"; gotURL != want {
		t.Fatalf("request URL = %q, want %q", gotURL, want)
	}
}

func TestLookupSubstitutesPluginConfigInTemplateAndPredicate(t *testing.T) {
	var gotURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.EscapedPath() + "?" + r.URL.RawQuery
		fmt.Fprint(w, `{"release_dates":[{"region":"US","rating":"PG-13"},{"region":"CA","rating":"14A"}]}`)
	}))
	defer server.Close()

	engine := New(
		WithInsecureLoopback(),
		WithConfigSource(staticConfig{"film": {"language": "fr CA", "region": "CA"}}),
	)
	result, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID:             "catalog",
		Kind:           "http-json",
		Endpoint:       server.URL,
		LookupTemplate: "/{config.language}/entry/{id}?language={config.language}",
		FieldMap:       map[string]string{"rating": "release_dates[region={config.region}].rating"},
	}, "42", Variables{PluginID: "film"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "/fr%20CA/entry/42?language=fr+CA"; gotURL != want {
		t.Fatalf("request URL = %q, want %q", gotURL, want)
	}
	if got := result.Fields["rating"]; got != "14A" {
		t.Fatalf("rating = %#v, want config-selected 14A", got)
	}
}

func TestSearchSubstitutesFingerprintVariablesInGraphQL(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotQuery = body.Query
		fmt.Fprint(w, `{"data": {"byPrint": [{"ref": "x9", "name": "Found"}]}}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	results, err := engine.Search(context.Background(), plugin.ScraperSpec{
		ID:       "atlas",
		Kind:     "http-graphql",
		Endpoint: server.URL,
		SearchTemplate: `query ByPrint { byPrint(hash: "{oshash}", seconds: {duration}) ` +
			`{ ref name } }`,
		CandidatesPath:  "byPrint",
		CandidateIDPath: "ref",
		SearchFieldMap:  map[string]string{"title": "name"},
	}, "", Variables{OSHash: `00"ff`, DurationSeconds: 90})
	if err != nil {
		t.Fatal(err)
	}
	// GraphQL string escaping applies to fingerprint values too.
	if want := `query ByPrint { byPrint(hash: "00\"ff", seconds: 90) { ref name } }`; gotQuery != want {
		t.Fatalf("query = %q, want %q", gotQuery, want)
	}
	if len(results) != 1 || results[0].ID != "x9" {
		t.Fatalf("results = %+v, want one candidate x9", results)
	}
}

func TestFingerprintPlaceholdersSubstituteEmptyWhenUnknown(t *testing.T) {
	var gotURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		fmt.Fprint(w, `{"name": "Found"}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	// No Variables argument at all: the placeholders must not leak literally.
	_, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID:             "charts",
		Kind:           "http-json",
		Endpoint:       server.URL,
		LookupTemplate: "/entry/{id}?oshash={oshash}&phash={phash}&duration={duration}",
		FieldMap:       map[string]string{"title": "name"},
	}, "e7")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/entry/e7?oshash=&phash=&duration="; gotURL != want {
		t.Fatalf("request URL = %q, want %q", gotURL, want)
	}
}

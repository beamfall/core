package scrape

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/plugin"
)

// The CORE-1127 and CORE-1128 tests drive a TheTVDB v4-shaped scraper
// declaration against recorded fixtures (testdata/tvdb-v4, see its README for
// the swagger they follow). The declaration mirrors the plugin's: a subscriber
// PIN as the AuthRef plus the project key as a second login field, a {year}
// search filter, translations with original-language fallback, remote-id
// lookups and artwork types read from the /artwork/types reference document.

const (
	fixturePIN    = "fixture-pin-4821"
	fixtureAPIKey = "fixture-project-key"
)

func tvdbFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "tvdb-v4", name)) // #nosec G304 -- reads this package's own recorded fixtures by name
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// tvdbServer serves the fixtures and records every request so tests can
// assert what went on the wire.
type tvdbServer struct {
	t        *testing.T
	mu       sync.Mutex
	logins   []map[string]string
	requests []*http.Request
	counts   map[string]int
	server   *httptest.Server
}

func newTVDBServer(t *testing.T) *tvdbServer {
	s := &tvdbServer{t: t, counts: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := jsonDecode(r, &body); err != nil {
			t.Error(err)
		}
		s.record(r, body)
		_, _ = w.Write(tvdbFixture(t, "login.json"))
	})
	authorized := func(name string, serve func(*http.Request) string) {
		mux.HandleFunc(name, func(w http.ResponseWriter, r *http.Request) {
			s.record(r, nil)
			if r.Header.Get("Authorization") != "Bearer fixture-bearer-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write(tvdbFixture(t, serve(r)))
		})
	}
	authorized("/search", func(r *http.Request) string {
		if r.URL.Query().Get("year") == "2021" {
			return "search-2021.json"
		}
		return "search-all.json"
	})
	authorized("/series/700303/extended", func(*http.Request) string { return "series-extended.json" })
	authorized("/search/remoteid/", func(*http.Request) string { return "remoteid.json" })
	authorized("/artwork/types", func(*http.Request) string { return "artwork-types.json" })
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

func (s *tvdbServer) record(r *http.Request, login map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r)
	s.counts[r.URL.Path]++
	if login != nil {
		s.logins = append(s.logins, login)
	}
}

func (s *tvdbServer) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[path]
}

const artworkTypeID = "{ref.artwork-types:data[slug=%s,recordType=series].id}"

func tvdbSpec(endpoint string) plugin.ScraperSpec {
	artwork := func(slug string) string {
		return "data.artworks[type=" + strings.Replace(artworkTypeID, "%s", slug, 1) + "].image"
	}
	return plugin.ScraperSpec{
		ID: "tvdb-series", Kind: "http-json", Endpoint: endpoint,
		AuthRef: "tvdb-subscriber-pin", AuthStyle: "login-bearer",
		Login: &plugin.LoginSpec{
			Path: "/login", SecretField: "pin",
			SecretFields: map[string]string{"apikey": "tvdb-api-key"}, // #nosec G101 -- a secure-store reference name, not a credential.
			TokenPath:    "data.token",
		},
		SearchTemplate:  "/search?query={query}&type=series&year={year}",
		CandidatesPath:  "data",
		CandidateIDPath: "tvdb_id",
		SearchFieldMap:  map[string]string{"title": "name", "year": "year"},
		LookupTemplate:  "/series/{id}/extended?meta=translations",
		LookupByIDTemplate: map[string]string{
			"tvdb": "/series/{id}/extended?meta=translations",
			"imdb": "/search/remoteid/{id}",
		},
		FieldMap: map[string]string{
			"title":   "data.translations.nameTranslations[language={config.metadata-language}].name||data.name||data.0.series.name",
			"summary": "data.translations.overviewTranslations[language={config.metadata-language}].overview||data.overview||data.0.series.overview",
			"year":    "data.year||data.0.series.year",
		},
		ArtworkMap: map[string]string{
			"poster":   "data.image||data.0.series.image",
			"backdrop": artwork("background"),
			"banner":   artwork("banner"),
			"logo":     artwork("clearlogo"),
		},
	}
}

func tvdbExtensions(plugin.ScraperSpec) plugin.ScraperExtensions {
	return plugin.ScraperExtensions{ReferenceDocuments: map[string]plugin.ReferenceDocument{
		"artwork-types": {Path: "/artwork/types"},
	}}
}

func newTVDBEngine(credentials CredentialSource, language string) *Engine {
	engine := New(
		WithInsecureLoopback(),
		WithCredentials(credentials),
		WithConfigSource(staticConfig{"fixture-plugin": {"metadata-language": language}}),
	)
	engine.extensions = tvdbExtensions
	return engine
}

// storedCredentials reports an unsaved secret the way the credential store
// does, as fs.ErrNotExist.
type storedCredentials map[string]string

func (s storedCredentials) Secret(ref string) (string, error) {
	if secret, ok := s[ref]; ok {
		return secret, nil
	}
	return "", fmt.Errorf("secret %q: %w", ref, fs.ErrNotExist)
}

func pinCredentials() storedCredentials {
	return storedCredentials{"tvdb-subscriber-pin": fixturePIN, "tvdb-api-key": fixtureAPIKey}
}

func pluginVars(year int) Variables { return Variables{PluginID: "fixture-plugin", Year: year} }

// CORE-1127: with a saved PIN the login body carries both the project key and
// the PIN, the token is reused, and the PIN goes nowhere else on the wire.
func TestTVDBLoginSendsSubscriberPINOnlyInTheLoginBody(t *testing.T) {
	server := newTVDBServer(t)
	engine := newTVDBEngine(pinCredentials(), "eng")
	spec := tvdbSpec(server.server.URL)
	for i := 0; i < 2; i++ {
		if _, err := engine.Lookup(context.Background(), spec, "700303", pluginVars(0)); err != nil {
			t.Fatal(err)
		}
	}
	if len(server.logins) != 1 {
		t.Fatalf("logins = %d across two lookups, want 1 (token reused)", len(server.logins))
	}
	if got := server.logins[0]; len(got) != 2 || got["apikey"] != fixtureAPIKey || got["pin"] != fixturePIN {
		t.Fatalf("login body = %v, want exactly apikey and pin", got)
	}
	for _, r := range server.requests {
		if r.URL.Path == "/login" {
			continue
		}
		wire := r.URL.String() + " " + strings.Join(headerValues(r.Header), " ")
		if strings.Contains(wire, fixturePIN) || strings.Contains(wire, fixtureAPIKey) {
			t.Fatalf("request %s carries a credential outside the login body", r.URL.Path)
		}
	}
}

func headerValues(header http.Header) []string {
	var values []string
	for name, vs := range header {
		values = append(values, name)
		values = append(values, vs...)
	}
	return values
}

// CORE-1127 as narrowed by the CORE-1129 owner decision: the PIN is the
// scraper's AuthRef, so without a saved PIN the scraper reports the missing
// credential and never attempts a login with the project key alone.
func TestTVDBWithoutSubscriberPINReportsMissingCredentialAndSkipsLogin(t *testing.T) {
	server := newTVDBServer(t)
	engine := newTVDBEngine(storedCredentials{"tvdb-api-key": fixtureAPIKey}, "eng")
	_, err := engine.Lookup(context.Background(), tvdbSpec(server.server.URL), "700303", pluginVars(0))
	var missing *CredentialMissingError
	if !errors.As(err, &missing) || missing.MissingCredential() != "tvdb-subscriber-pin" {
		t.Fatalf("err = %v, want the missing subscriber PIN", err)
	}
	if len(server.requests) != 0 {
		t.Fatalf("requests = %d without a PIN, want none", len(server.requests))
	}
}

// CORE-1128: the token lives for the swagger's one-month validity minus a
// margin, then the next request logs in again.
func TestTVDBLoginTokenOutlivesAnHourAndExpiresBeforeAMonth(t *testing.T) {
	server := newTVDBServer(t)
	engine := newTVDBEngine(pinCredentials(), "eng")
	clock := newManualClock()
	engine.clock = clock
	spec := tvdbSpec(server.server.URL)
	lookup := func() {
		t.Helper()
		if _, err := engine.Lookup(context.Background(), spec, "700303", pluginVars(0)); err != nil {
			t.Fatal(err)
		}
	}
	lookup()
	clock.advance(7 * 24 * time.Hour)
	lookup()
	if len(server.logins) != 1 {
		t.Fatalf("logins after a week = %d, want 1", len(server.logins))
	}
	clock.advance(21 * 24 * time.Hour)
	lookup()
	if len(server.logins) != 2 {
		t.Fatalf("logins after 28 days = %d, want 2 (token refreshed before the month ends)", len(server.logins))
	}
}

// CORE-1128 AC: search passes the year when known, and of two same-titled
// series the one from that year is matched.
func TestTVDBSearchYearPicksTheSameTitledSeriesFromThatYear(t *testing.T) {
	server := newTVDBServer(t)
	engine := newTVDBEngine(pinCredentials(), "eng")
	spec := tvdbSpec(server.server.URL)

	results, err := engine.Search(context.Background(), spec, "Harbor Lights", pluginVars(2021))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "700202" || fmt.Sprint(results[0].Fields["year"]) != "2021" {
		t.Fatalf("year-filtered results = %+v, want only 700202 from 2021", results)
	}
	last := server.requests[len(server.requests)-1]
	if got := last.URL.Query().Get("year"); got != "2021" {
		t.Fatalf("search year = %q, want 2021", got)
	}

	results, err = engine.Search(context.Background(), spec, "Harbor Lights", pluginVars(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("unfiltered results = %d, want both same-titled series", len(results))
	}
	last = server.requests[len(server.requests)-1]
	if last.URL.Query().Has("year") {
		t.Fatalf("search sent year=%q with no known year, want the parameter omitted", last.URL.Query().Get("year"))
	}
}

// CORE-1128 AC: titles and summaries come back in the configured language,
// and fall back to the record's original language when no translation exists.
func TestTVDBLookupUsesConfiguredLanguageWithOriginalFallback(t *testing.T) {
	server := newTVDBServer(t)
	spec := tvdbSpec(server.server.URL)

	english, err := newTVDBEngine(pinCredentials(), "eng").Lookup(context.Background(), spec, "700303", pluginVars(0))
	if err != nil {
		t.Fatal(err)
	}
	if english.Fields["title"] != "North Beacon" || english.Fields["summary"] != "A lighthouse keeper watches the coast." {
		t.Fatalf("eng fields = %v, want the English translation", english.Fields)
	}

	fallback, err := newTVDBEngine(pinCredentials(), "fra").Lookup(context.Background(), spec, "700303", pluginVars(0))
	if err != nil {
		t.Fatal(err)
	}
	if fallback.Fields["title"] != "Faro del Norte" || fallback.Fields["summary"] != "Una guardiana de faro vigila la costa." {
		t.Fatalf("fra fields = %v, want the original-language record", fallback.Fields)
	}
	for _, r := range server.requests {
		if r.URL.Path == "/series/700303/extended" && r.URL.Query().Get("meta") != "translations" {
			t.Fatalf("lookup query = %q, want meta=translations", r.URL.RawQuery)
		}
	}
}

// CORE-1128 AC: a remote id (IMDb here) resolves through /search/remoteid and
// maps through the same field and artwork maps as a direct lookup.
func TestTVDBRemoteIDLookupResolvesTheSeries(t *testing.T) {
	server := newTVDBServer(t)
	engine := newTVDBEngine(pinCredentials(), "eng")
	result, err := engine.LookupByID(context.Background(), tvdbSpec(server.server.URL), "imdb", "tt0700303", pluginVars(0))
	if err != nil {
		t.Fatal(err)
	}
	if result.Fields["title"] != "Faro del Norte" || fmt.Sprint(result.Fields["year"]) != "2019" {
		t.Fatalf("remote-id fields = %v", result.Fields)
	}
	if result.Artwork["poster"] != "https://artworks.example.org/posters/700303.jpg" {
		t.Fatalf("remote-id poster = %q", result.Artwork["poster"])
	}
	if server.count("/search/remoteid/tt0700303") != 1 {
		t.Fatalf("remote-id endpoint calls = %v", server.counts)
	}
	if _, err := engine.LookupByID(context.Background(), tvdbSpec(server.server.URL), "undeclared", "1", pluginVars(0)); err == nil {
		t.Fatal("undeclared id provider did not error")
	}
}

// CORE-1128 AC: background, banner and clearlogo artwork come from the
// extended record, with type ids read from /artwork/types (series record
// type only), and that static document is fetched once and kept for a week.
func TestTVDBExtraArtworkUsesCachedArtworkTypes(t *testing.T) {
	server := newTVDBServer(t)
	engine := newTVDBEngine(pinCredentials(), "eng")
	clock := newManualClock()
	engine.clock = clock
	spec := tvdbSpec(server.server.URL)
	lookup := func() Result {
		t.Helper()
		result, err := engine.Lookup(context.Background(), spec, "700303", pluginVars(0))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := lookup()
	want := map[string]string{
		"poster":   "https://artworks.example.org/posters/700303.jpg",
		"backdrop": "https://artworks.example.org/backgrounds/700303.jpg",
		"banner":   "https://artworks.example.org/banners/700303.jpg",
		"logo":     "https://artworks.example.org/clearlogos/700303.png",
	}
	for kind, url := range want {
		if result.Artwork[kind] != url {
			t.Errorf("artwork %s = %q, want %q", kind, result.Artwork[kind], url)
		}
	}
	clock.advance(6 * 24 * time.Hour)
	lookup()
	if got := server.count("/artwork/types"); got != 1 {
		t.Fatalf("artwork types fetched %d times within a week, want 1", got)
	}
	clock.advance(2 * 24 * time.Hour)
	lookup()
	if got := server.count("/artwork/types"); got != 2 {
		t.Fatalf("artwork types fetched %d times after a week, want 2", got)
	}
}

// An unavailable reference document drops only the artwork that needs it.
func TestTVDBArtworkTypesUnavailableKeepsThePoster(t *testing.T) {
	server := newTVDBServer(t)
	engine := newTVDBEngine(pinCredentials(), "eng")
	engine.extensions = func(plugin.ScraperSpec) plugin.ScraperExtensions { return plugin.ScraperExtensions{} }
	result, err := engine.Lookup(context.Background(), tvdbSpec(server.server.URL), "700303", pluginVars(0))
	if err != nil {
		t.Fatal(err)
	}
	if result.Artwork["poster"] == "" {
		t.Fatal("poster dropped with the reference document undeclared")
	}
	for _, kind := range []string{"backdrop", "banner", "logo"} {
		if _, ok := result.Artwork[kind]; ok {
			t.Errorf("artwork %s present without its type ids", kind)
		}
	}
	if server.count("/artwork/types") != 0 {
		t.Fatal("undeclared reference document was fetched")
	}
}

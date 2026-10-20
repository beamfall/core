//go:build io

package ioe2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
)

// staticCreds is a scrape CredentialSource backed by an in-memory map.
type staticCreds map[string]string

func (s staticCreds) Secret(ref string) (string, error) {
	v, ok := s[ref]
	if !ok {
		return "", fmt.Errorf("no secret %q", ref)
	}
	return v, nil
}

// TestEgress_scrape_fetch drives the REAL scrape.Engine.FetchDocument against a
// loopback double and asserts it issued the GET and returned the body.
//
// io:egress-scrape-fetch
func TestEgress_scrape_fetch(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotUA     string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotUA = r.Header.Get("User-Agent")
		_, _ = io.WriteString(w, "<rss><channel></channel></rss>")
	}))
	defer srv.Close()

	engine := scrape.New(scrape.WithInsecureLoopback())
	data, err := engine.FetchDocument(context.Background(), srv.URL+"/feed.xml", nil)
	if err != nil {
		t.Fatalf("FetchDocument: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/feed.xml" {
		t.Errorf("path = %q, want /feed.xml", gotPath)
	}
	if gotUA == "" {
		t.Error("User-Agent header was empty; scrape fetches must identify themselves")
	}
	if !strings.Contains(string(data), "<rss>") {
		t.Errorf("body = %q, want the served document", data)
	}
}

// TestEgress_scrape_authorized_post drives the REAL scrape.Engine through a
// login-bearer GraphQL search against a loopback double: it asserts the login
// POST (secret in the JSON body) AND the authorized JSON POST that follows.
//
// io:egress-scrape-authorized-post
func TestEgress_scrape_authorized_post(t *testing.T) {
	var (
		loginHits  int32
		searchHits int32
		gotSecret  string
		gotAuth    string
		gotQuery   string
	)
	// The engine posts the GraphQL query to spec.Endpoint and the login to
	// spec.Endpoint + Login.Path, so the login lands under /graphql/login.
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql/login", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&loginHits, 1)
		if r.Method != http.MethodPost {
			t.Errorf("login method = %q, want POST", r.Method)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotSecret = body["apikey"]
		_, _ = io.WriteString(w, `{"data":{"token":"jwt-xyz"}}`)
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&searchHits, 1)
		if r.Method != http.MethodPost {
			t.Errorf("graphql method = %q, want POST", r.Method)
		}
		gotAuth = r.Header.Get("Authorization")
		var body struct{ Query string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotQuery = body.Query
		_, _ = io.WriteString(w, `{"data":{"results":[{"name":"Vega Prime"}]}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	engine := scrape.New(
		scrape.WithInsecureLoopback(),
		scrape.WithCredentials(staticCreds{"src-key": "s3cret"}),
	)
	spec := plugin.ScraperSpec{
		ID:             "authsource",
		Kind:           "http-graphql",
		Endpoint:       srv.URL + "/graphql",
		AuthRef:        "src-key",
		AuthStyle:      "login-bearer",
		Login:          &plugin.LoginSpec{Path: "/login", SecretField: "apikey", TokenPath: "data.token"},
		SearchTemplate: `query { results(q: "{query}") { name } }`,
		CandidatesPath: "results",
		FieldMap:       map[string]string{"title": "name"},
	}
	results, err := engine.Search(context.Background(), spec, "vega")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if got := atomic.LoadInt32(&loginHits); got != 1 {
		t.Errorf("login double hit %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&searchHits); got != 1 {
		t.Errorf("search double hit %d times, want 1", got)
	}
	if gotSecret != "s3cret" {
		t.Errorf("login body apikey = %q, want s3cret (secret travels in the JSON body)", gotSecret)
	}
	if gotAuth != "Bearer jwt-xyz" {
		t.Errorf("authorized POST Authorization = %q, want the exchanged bearer token", gotAuth)
	}
	if !strings.Contains(gotQuery, "vega") {
		t.Errorf("graphql query = %q, want the substituted search term", gotQuery)
	}
	if len(results) != 1 || results[0].Fields["title"] != "Vega Prime" {
		t.Errorf("results = %+v, want one Vega Prime", results)
	}
}

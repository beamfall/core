package scrape

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/beamfall/plugin-sdk"

	"github.com/beamfall/core/internal/plugin"
)

func TestListChildrenUsesFetchControls(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.RequestURI())
		mu.Unlock()
		if r.URL.EscapedPath() == "/api/lists/extra" {
			fmt.Fprint(w, "[23029420804234]")
			return
		}
		fmt.Fprint(w, `[{"id":1}]`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback(), WithMaxResponseBytes(12), WithConfigSource(staticConfig{"demo": {"locale": "en US"}}))
	clock := newManualClock()
	engine.clock = clock
	spec := plugin.ScraperSpec{
		ID: "recorded", Kind: "http-json", Endpoint: server.URL + "/api",
		MinRequestIntervalMS: 10,
		Children: []sdk.ChildListing{{
			Level: "item", ListTemplate: "/lists/{id}?locale={config.locale}", ItemsPath: "$", Key: "id", Label: "name",
		}},
	}
	read := func(id string) error {
		_, err := engine.ListChildren(t.Context(), spec, 1, id, nil, Variables{PluginID: "demo"})
		return err
	}
	if err := read("first value"); err != nil {
		t.Fatal(err)
	}
	if err := read("first value"); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- read("extra") }()
	select {
	case <-clock.waitCreated:
	case err := <-done:
		t.Fatalf("uncached request skipped the rate limit: %v", err)
	case <-time.After(time.Second):
		t.Fatal("uncached request did not reach the rate limiter")
	}
	mu.Lock()
	want := []string{"/api/lists/first%20value?locale=en+US"}
	if !slices.Equal(requests, want) {
		t.Errorf("requests before releasing rate limit = %v, want %v", requests, want)
	}
	mu.Unlock()
	clock.advance(10 * time.Millisecond)
	clock.releaseLast()
	if err := <-done; err == nil {
		t.Fatal("oversized response escaped the fetch cap")
	}

	clock.advance(childResponseTTL)
	if err := read("first value"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want = append(want, "/api/lists/extra?locale=en+US", want[0])
	if !slices.Equal(requests, want) {
		t.Fatalf("requests after cache expiration = %v, want %v", requests, want)
	}
}

func TestListChildrenReplaysFromRootForThirdLevel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first/42":
			fmt.Fprint(w, `[{"id":"left"},{"id":"right"}]`)
		case "/second/left":
			fmt.Fprint(w, `[{"id":"other"}]`)
		case "/second/right":
			fmt.Fprint(w, `[{"id":"selected","n":0}]`)
		case "/third/42/selected/0":
			fmt.Fprint(w, `[{"id":"leaf"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	spec := plugin.ScraperSpec{
		ID: "recorded", Kind: "http-json", Endpoint: server.URL,
		Children: []sdk.ChildListing{
			{Level: "first", ListTemplate: "/first/{id}", ItemsPath: "$", Key: "id", Label: "id"},
			{Level: "second", ListTemplate: "/second/{parent}", ItemsPath: "$", Key: "id", Label: "id", Index: "n"},
			{Level: "third", ListTemplate: "/third/{id}/{parent}/{index}", ItemsPath: "$", Key: "id", Label: "id"},
		},
	}
	engine := New(WithInsecureLoopback())
	rows, err := engine.ListChildren(t.Context(), spec, 3, "42", new("selected"), Variables{})
	if err != nil || len(rows) != 1 || rows[0].Key != "leaf" {
		t.Fatalf("third level = %v, %v", rows, err)
	}
}

// TestListChildrenRefusesAmbiguousThirdLevelParent (CORE-3127): the wire key
// carries no ancestor, so a key returned under two earlier rows is usable only
// when both rows open the same listing.
func TestListChildrenRefusesAmbiguousThirdLevelParent(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	secondRight := `[{"id":"1","s":"2"}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path)
		right := secondRight
		mu.Unlock()
		switch r.URL.Path {
		case "/first/42":
			fmt.Fprint(w, `[{"id":"left"},{"id":"right"}]`)
		case "/second/left":
			fmt.Fprint(w, `[{"id":"1","s":"1"}]`)
		case "/second/right":
			fmt.Fprint(w, right)
		case "/third/42/1":
			fmt.Fprint(w, `[{"id":"leaf-1"}]`)
		case "/third/42/2":
			fmt.Fprint(w, `[{"id":"leaf-2"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	spec := plugin.ScraperSpec{
		ID: "recorded", Kind: "http-json", Endpoint: server.URL,
		Children: []sdk.ChildListing{
			{Level: "first", ListTemplate: "/first/{id}", ItemsPath: "$", Key: "id", Label: "id"},
			{Level: "second", ListTemplate: "/second/{parent}", ItemsPath: "$", Key: "id", Label: "id", Sequence: "s"},
			{Level: "third", ListTemplate: "/third/{id}/{sequence}", ItemsPath: "$", Key: "id", Label: "id"},
		},
	}
	rows, err := New(WithInsecureLoopback()).ListChildren(t.Context(), spec, 3, "42", new("1"), Variables{})
	if !errors.Is(err, ErrChildParentAmbig) {
		t.Fatalf("third level = %v, %v; want the ambiguous parent refused", rows, err)
	}
	mu.Lock()
	for _, path := range requests {
		if strings.HasPrefix(path, "/third/") {
			t.Fatalf("requests = %v, want no third-level fetch for an ambiguous parent", requests)
		}
	}
	// The same key under both rows is fine when both substitute the same values.
	secondRight = `[{"id":"1","s":"1"}]`
	mu.Unlock()
	rows, err = New(WithInsecureLoopback()).ListChildren(t.Context(), spec, 3, "42", new("1"), Variables{})
	if err != nil || len(rows) != 1 || rows[0].Key != "leaf-1" {
		t.Fatalf("third level with agreeing parents = %v, %v", rows, err)
	}
}

// TestListChildrenRefusesKeyBeneathAmbiguousAncestor (CORE-3127): a key found
// only under an ancestor key that is itself ambiguous is unreachable, since no
// successful read of the previous level could have returned it.
func TestListChildrenRefusesKeyBeneathAmbiguousAncestor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first/42":
			fmt.Fprint(w, `[{"id":"a","s":"1"},{"id":"a","s":"2"},{"id":"b","s":"3"}]`)
		case "/second/1":
			fmt.Fprint(w, `[{"id":"x"}]`)
		case "/second/2":
			fmt.Fprint(w, `[{"id":"y"}]`)
		case "/second/3":
			fmt.Fprint(w, `[{"id":"z"}]`)
		case "/third/y", "/third/z":
			fmt.Fprint(w, `[{"id":"leaf"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	spec := plugin.ScraperSpec{
		ID: "recorded", Kind: "http-json", Endpoint: server.URL,
		Children: []sdk.ChildListing{
			{Level: "first", ListTemplate: "/first/{id}", ItemsPath: "$", Key: "id", Label: "id", Sequence: "s"},
			{Level: "second", ListTemplate: "/second/{sequence}", ItemsPath: "$", Key: "id", Label: "id"},
			{Level: "third", ListTemplate: "/third/{parent}", ItemsPath: "$", Key: "id", Label: "id"},
		},
	}
	engine := New(WithInsecureLoopback())
	if _, err := engine.ListChildren(t.Context(), spec, 2, "42", new("a"), Variables{}); !errors.Is(err, ErrChildParentAmbig) {
		t.Fatalf("second level under the shared key = %v, want it refused as ambiguous", err)
	}
	if rows, err := engine.ListChildren(t.Context(), spec, 3, "42", new("y"), Variables{}); !errors.Is(err, ErrChildParentRow) {
		t.Fatalf("third level beneath the ambiguous key = %v, %v; want the parent refused as unreturned", rows, err)
	}
	if rows, err := engine.ListChildren(t.Context(), spec, 3, "42", new("z"), Variables{}); err != nil || len(rows) != 1 {
		t.Fatalf("third level beneath an unambiguous key = %v, %v", rows, err)
	}
}

func TestListChildrenRefusesCrossOriginRedirect(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("child listing reached an origin other than the scraper endpoint")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer destination.Close()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer endpoint.Close()
	spec := plugin.ScraperSpec{
		ID: "redirect", Kind: "http-json", Endpoint: endpoint.URL,
		Children: []sdk.ChildListing{{
			Level: "item", ListTemplate: "/list/{id}", ItemsPath: "$", Key: "id", Label: "name",
		}},
	}
	engine := New(WithInsecureLoopback())
	if _, err := engine.ListChildren(t.Context(), spec, 1, "42", nil, Variables{}); err == nil {
		t.Fatal("cross-origin redirect was accepted")
	}
}

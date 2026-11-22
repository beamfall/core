package scrape

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/plugin"
)

// TestSearchSubstitutesHints covers CORE-2333: {hint.<key>} placeholders in a
// SearchTemplate's query take the item's hint values, query-escaped; a missing
// optional hint renders empty; a missing required hint skips the search.
func TestSearchSubstitutesHints(t *testing.T) {
	var mu sync.Mutex
	var gotRaw []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotRaw = append(gotRaw, r.URL.RawQuery)
		mu.Unlock()
		fmt.Fprint(w, `{"results": [{"id": 7}]}`)
	}))
	defer server.Close()

	spec := plugin.ScraperSpec{
		ID:              "catalog",
		Kind:            "http-json",
		Endpoint:        server.URL,
		SearchTemplate:  "/search?q={query}&by={hint.creator}&in={hint.collection}",
		CandidatesPath:  "results",
		CandidateIDPath: "id",
		LookupTemplate:  "/entry/{id}",
	}
	engine := New(WithInsecureLoopback())
	ctx := context.Background()

	if _, err := engine.Search(ctx, spec, "Blue", Variables{Hints: map[string]string{
		"creator":    "A & B",
		"collection": "x=y#1",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Search(ctx, spec, "Blue", Variables{Hints: map[string]string{"creator": "Solo"}}); err != nil {
		t.Fatal(err)
	}
	spec.RequiredSearchHints = []string{"collection"}
	candidates, err := engine.Search(ctx, spec, "Blue", Variables{Hints: map[string]string{"creator": "Solo"}})
	if err != nil || candidates != nil {
		t.Fatalf("required hint missing: candidates = %v, err = %v; want skipped", candidates, err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"q=Blue&by=A+%26+B&in=x%3Dy%231", "q=Blue&by=Solo&in="}
	if len(gotRaw) != len(want) {
		t.Fatalf("requests = %q, want %q", gotRaw, want)
	}
	for i := range want {
		if gotRaw[i] != want[i] {
			t.Errorf("request %d query = %q, want %q", i, gotRaw[i], want[i])
		}
	}
}

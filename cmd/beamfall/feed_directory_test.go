package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/jsonstate"
	"github.com/beamfall/core/internal/l10n"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store/storetest"
)

const directoryFixture = `{"results":[{"collectionName":"Daily","feedUrl":"https://example.com/feed.xml"}]}`

func newTestFeedDirectorySearcher(t *testing.T, engine feedDirectoryFetcher, secrets app.SecretSource, endpoint, template string, now func() time.Time) *feedDirectorySearcher {
	t.Helper()
	return newFeedDirectorySearcher(engine, secrets, endpoint, template, filepath.Join(t.TempDir(), "budget.json"), now)
}

func TestFeedDirectoryCache(t *testing.T) {
	now := time.Unix(100, 0)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		term := "DAILY News"
		if requests.Add(1) > 1 {
			term = "daily news"
		}
		if r.URL.Path != "/search" || r.URL.Query().Get("term") != term {
			t.Errorf("unexpected request %s", r.URL)
		}
		_, _ = io.WriteString(w, directoryFixture)
	}))
	defer server.Close()
	searcher := newTestFeedDirectorySearcher(t, scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "/search?term={query}", func() time.Time { return now })

	results, err := searcher.SearchFeeds(t.Context(), "  DAILY \tNews ")
	if err != nil || len(results) != 1 {
		t.Fatalf("first search = %v, %v", results, err)
	}
	results[0].Title = "changed"
	now = now.Add(10*time.Minute - time.Nanosecond)
	results, err = searcher.SearchFeeds(t.Context(), "daily news")
	if err != nil || len(results) != 1 || results[0].Title != "Daily" || requests.Load() != 1 {
		t.Fatalf("cache hit = %v, %v, %d requests", results, err, requests.Load())
	}
	now = now.Add(time.Nanosecond)
	if _, err := searcher.SearchFeeds(t.Context(), "daily news"); err != nil || requests.Load() != 2 {
		t.Fatalf("expired search = %v, %d requests", err, requests.Load())
	}
}

func TestFeedDirectoryCacheEvictsOldest(t *testing.T) {
	now := time.Unix(100, 0)
	searcher := newTestFeedDirectorySearcher(t, nil, nil, "", "", func() time.Time { return now })
	searches := 0
	search := func(context.Context) ([]graph.FeedSearchResult, error) {
		searches++
		return nil, nil
	}
	for i := range feedSearchCacheSize + 1 {
		_, _ = searcher.searchCached(t.Context(), fmt.Sprint(i), search)
		now = now.Add(time.Second)
	}
	if got := len(searcher.cache); got != feedSearchCacheSize {
		t.Fatalf("cache length = %d, want %d", got, feedSearchCacheSize)
	}
	if _, _ = searcher.searchCached(t.Context(), fmt.Sprint(feedSearchCacheSize), search); searches != feedSearchCacheSize+1 {
		t.Fatal("newest empty result was not cached")
	}
	if _, _ = searcher.searchCached(t.Context(), "0", search); searches != feedSearchCacheSize+2 {
		t.Fatal("oldest result survived capacity eviction")
	}
}

type directoryWaitContext struct {
	context.Context
	observeDone func()
}

func (c directoryWaitContext) Done() <-chan struct{} {
	c.observeDone()
	return c.Context.Done()
}

func TestFeedDirectorySharesInFlightEquivalentSearches(t *testing.T) {
	for _, test := range []struct {
		name        string
		cancelFirst bool
	}{
		{name: "share result"},
		{name: "first caller disconnects", cancelFirst: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			testFeedDirectorySharesInFlight(t, test.cancelFirst)
		})
	}
}

func testFeedDirectorySharesInFlight(t *testing.T, cancelFirst bool) {
	t.Helper()
	started := make(chan struct{})
	respond := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(started)
		}
		<-respond
		_, _ = io.WriteString(w, directoryFixture)
	}))
	defer server.Close()
	release := sync.OnceFunc(func() { close(respond) })
	searcher := newTestFeedDirectorySearcher(t, scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "/search?term={query}", time.Now)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	firstCtx, cancelFirstCaller := context.WithCancel(ctx)
	defer cancelFirstCaller()
	var group sync.WaitGroup
	defer func() {
		cancel()
		release()
		group.Wait()
	}()
	var first, second []graph.FeedSearchResult
	var firstErr error
	firstDone := make(chan struct{})
	group.Go(func() {
		defer close(firstDone)
		first, firstErr = searcher.SearchFeeds(firstCtx, "  DAILY \tNews ")
	})
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first search did not reach the server")
	}
	joining := make(chan struct{})
	follower := directoryWaitContext{Context: ctx, observeDone: sync.OnceFunc(func() { close(joining) })}
	group.Go(func() {
		var err error
		second, err = searcher.SearchFeeds(follower, "daily news")
		if err != nil {
			t.Errorf("equivalent search: %v", err)
		}
	})
	select {
	case <-joining:
	case <-ctx.Done():
		t.Fatal("equivalent search did not join the in-flight search")
	}
	if cancelFirst {
		cancelFirstCaller()
		select {
		case <-firstDone:
		case <-ctx.Done():
			t.Fatal("canceled caller did not return while the upstream was blocked")
		}
	}
	release()
	group.Wait()
	if requests.Load() != 1 || len(second) != 1 || second[0].Title != "Daily" {
		t.Fatalf("remaining search returned %v across %d upstream requests", second, requests.Load())
	}
	if cancelFirst {
		if !errors.Is(firstErr, context.Canceled) || len(first) != 0 {
			t.Fatalf("canceled search = %v, %v", first, firstErr)
		}
		return
	}
	if firstErr != nil || !slices.Equal(first, second) {
		t.Fatalf("searches returned %v and %v across %d upstream requests", first, second, requests.Load())
	}
	first[0].Title = "changed"
	if second[0].Title != "Daily" {
		t.Fatalf("equivalent searches shared a mutable result: %v", second)
	}
}

func TestFeedDirectoryCancelsUnobservedSearch(t *testing.T) {
	searcher := newTestFeedDirectorySearcher(t, nil, nil, "", "", time.Now)
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	caller, cancel := context.WithCancel(ctx)
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := searcher.searchCached(caller, "same", func(shared context.Context) ([]graph.FeedSearchResult, error) {
			close(started)
			select {
			case <-shared.Done():
				return nil, shared.Err()
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
		done <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("shared work did not start")
	}
	searcher.mu.Lock()
	call := searcher.inFlight["same"]
	searcher.mu.Unlock()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("caller error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("caller did not return")
	}
	select {
	case <-call.done:
		if !errors.Is(call.err, context.Canceled) {
			t.Fatalf("shared search error = %v", call.err)
		}
	case <-ctx.Done():
		t.Fatal("shared search did not stop when the last caller left")
	}
}

func TestFeedDirectoryRateLimitsConcurrentBurst(t *testing.T) {
	now := time.Unix(100, 0)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, directoryFixture)
	}))
	defer server.Close()
	searcher := newTestFeedDirectorySearcher(t, scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "/search?term={query}", func() time.Time { return now })
	if _, err := searcher.SearchFeeds(t.Context(), "cached"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var limited atomic.Int32
	var group sync.WaitGroup
	for i := range 100 {
		group.Go(func() {
			_, err := searcher.SearchFeeds(ctx, fmt.Sprintf("search %d", i))
			if errors.Is(err, graph.ErrFeedSearchThrottled) {
				limited.Add(1)
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
	group.Wait()
	if requests.Load() != 15 || limited.Load() != 86 {
		t.Fatalf("burst made %d requests and throttled %d searches", requests.Load(), limited.Load())
	}
	if _, err := searcher.SearchFeeds(ctx, "CACHED"); err != nil || requests.Load() != 15 {
		t.Fatalf("cached search at capacity: %v, %d requests", err, requests.Load())
	}
	searcher = newFeedDirectorySearcher(searcher.engine, nil, server.URL, "/search?term={query}", searcher.budgetFile, func() time.Time { return now })
	if _, err := searcher.SearchFeeds(ctx, "restart"); !errors.Is(err, graph.ErrFeedSearchThrottled) || requests.Load() != 15 {
		t.Fatalf("immediately after restart: %v, %d requests", err, requests.Load())
	}

	now = now.Add(time.Minute - time.Nanosecond)
	if _, err := searcher.SearchFeeds(ctx, "still throttled"); !errors.Is(err, graph.ErrFeedSearchThrottled) {
		t.Fatalf("before refill: %v", err)
	}
	now = now.Add(time.Nanosecond)
	if _, err := searcher.SearchFeeds(ctx, "after refill"); err != nil || requests.Load() != 16 {
		t.Fatalf("after refill: %v, %d requests", err, requests.Load())
	}
}

func TestFeedDirectoryPersistsAdmissionBeforeRequest(t *testing.T) {
	now := time.Unix(100, 0)
	budgetFile := filepath.Join(t.TempDir(), "budget.json")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := requests.Add(1) - 1
		var tokens []time.Time
		found, err := jsonstate.Load(budgetFile, &tokens)
		if err != nil || !found || len(tokens) != int(index)+1 || !tokens[index].Equal(now.Add(time.Minute)) {
			t.Errorf("upstream received a request before its deadline was recorded: %v, %v, %v", found, tokens, err)
		}
		_, _ = io.WriteString(w, directoryFixture)
	}))
	defer server.Close()
	searcher := newFeedDirectorySearcher(scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "/search?term={query}", budgetFile, func() time.Time { return now })
	if _, err := searcher.SearchFeeds(t.Context(), "baseline"); err != nil || requests.Load() != 1 {
		t.Fatalf("durable reservation = %v, %d upstream requests", err, requests.Load())
	}
	// A directory at the state path makes publication fail after staging.
	blockedPath := filepath.Join(filepath.Dir(budgetFile), "blocked")
	if err := os.Mkdir(blockedPath, 0o700); err != nil {
		t.Fatal(err)
	}
	searcher.budgetFile = blockedPath
	var linkErr *os.LinkError
	if _, err := searcher.SearchFeeds(t.Context(), "unpersisted"); !errors.As(err, &linkErr) || linkErr.Op != "rename" || requests.Load() != 1 {
		t.Fatalf("failed reservation write = %v, %d upstream requests", err, requests.Load())
	}
	searcher.budgetFile = budgetFile
	if _, err := searcher.SearchFeeds(t.Context(), "persisted"); err != nil || requests.Load() != 2 {
		t.Fatalf("reservation after failed write = %v, %d upstream requests", err, requests.Load())
	}
}

func TestFeedDirectoryRetriesBudgetLoad(t *testing.T) {
	now := time.Unix(100, 0)
	budgetFile := filepath.Join(t.TempDir(), "budget.json")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, directoryFixture)
	}))
	defer server.Close()
	// A directory at the state path makes the read fail, as an unreadable
	// file would after startup.
	if err := os.Mkdir(budgetFile, 0o700); err != nil {
		t.Fatal(err)
	}
	searcher := newFeedDirectorySearcher(scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "/search?term={query}", budgetFile, func() time.Time { return now })
	if _, err := searcher.SearchFeeds(t.Context(), "unreadable"); err == nil || errors.Is(err, graph.ErrFeedSearchThrottled) || requests.Load() != 0 {
		t.Fatalf("unreadable budget = %v, %d requests", err, requests.Load())
	}

	// Recovery keeps unexpired reservations and drops expired ones.
	saved := []time.Time{now.Add(-time.Second)}
	for range feedRequestsPerMin - 1 {
		saved = append(saved, now.Add(30*time.Second))
	}
	if err := os.Remove(budgetFile); err != nil {
		t.Fatal(err)
	}
	if err := jsonstate.Save(budgetFile, saved); err != nil {
		t.Fatal(err)
	}
	if _, err := searcher.SearchFeeds(t.Context(), "recovered"); err != nil || requests.Load() != 1 {
		t.Fatalf("after recovery = %v, %d requests", err, requests.Load())
	}
	if _, err := searcher.SearchFeeds(t.Context(), "full"); !errors.Is(err, graph.ErrFeedSearchThrottled) || requests.Load() != 1 {
		t.Fatalf("restored reservations = %v, %d requests", err, requests.Load())
	}

	if err := os.WriteFile(budgetFile, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	searcher = newFeedDirectorySearcher(searcher.engine, nil, server.URL, "/search?term={query}", budgetFile, func() time.Time { return now })
	if _, err := searcher.SearchFeeds(t.Context(), "corrupt"); err != nil || requests.Load() != 2 {
		t.Fatalf("corrupt budget = %v, %d requests", err, requests.Load())
	}
}

type directoryTestSecrets map[string]string

func (s directoryTestSecrets) Secret(ref string) (string, error) { return s[ref], nil }

type directoryTestFetcher struct {
	*scrape.Engine
	extendedURL string
}

func (f directoryTestFetcher) FetchDocumentLimited(ctx context.Context, rawURL string, headers map[string]string, maxBytes int64, admit func() error) ([]byte, *url.URL, error) {
	if strings.HasPrefix(rawURL, "https://api.podcastindex.org/") {
		rawURL = f.extendedURL
	}
	return f.Engine.FetchDocumentLimited(ctx, rawURL, headers, maxBytes, admit)
}

func TestFeedDirectoryExtendedCredentialsAndUserAgent(t *testing.T) {
	var primary, extended, polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.UserAgent(), "Beamfall/v9.8.7 (+https://beamfall.com/bot)"; got != want {
			t.Errorf("User-Agent = %q, want %q", got, want)
		}
		switch r.URL.Path {
		case "/search":
			primary.Add(1)
			_, _ = io.WriteString(w, directoryFixture)
		case "/extended":
			extended.Add(1)
			if r.Header.Get("X-Auth-Key") != "operator-key" || r.Header.Get("Authorization") == "" {
				t.Error("expected a signed directory request")
			}
			_, _ = io.WriteString(w, `{"status":true,"feeds":[{"title":"Extra","url":"https://example.com/extra.xml"}]}`)
		case "/feed.xml":
			polls.Add(1)
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Errorf("unexpected request to %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	now := func() time.Time { return time.Unix(100, 0) }
	engine := scrape.New(scrape.WithInsecureLoopback(), scrape.WithReleaseVersion("v9.8.7"))
	fetcher := directoryTestFetcher{Engine: engine, extendedURL: server.URL + "/extended"}
	withoutKey := newTestFeedDirectorySearcher(t, fetcher, directoryTestSecrets{"podcast-index-secret": "operator-secret"}, server.URL, "/search?term={query}", now)
	results, err := withoutKey.SearchFeeds(t.Context(), "one")
	if err != nil || len(results) != 1 || primary.Load() != 1 || extended.Load() != 0 {
		t.Fatalf("search without key = %v, %v, primary=%d extended=%d", results, err, primary.Load(), extended.Load())
	}
	withKey := newTestFeedDirectorySearcher(t, fetcher, directoryTestSecrets{"podcast-index-key": "operator-key", "podcast-index-secret": "operator-secret"}, server.URL, "/search?term={query}", now)
	for i := range 7 {
		results, err := withKey.SearchFeeds(t.Context(), fmt.Sprintf("signed %d", i))
		if err != nil || len(results) != 2 || results[1].Title != "Extra" {
			t.Fatalf("signed search = %v, %v", results, err)
		}
	}
	results, err = withKey.SearchFeeds(t.Context(), "last primary request")
	if err != nil || len(results) != 1 || primary.Load() != 9 || extended.Load() != 7 {
		t.Fatalf("shared budget = %v, %v, primary=%d extended=%d", results, err, primary.Load(), extended.Load())
	}
	poll, err := engine.FetchConditional(t.Context(), server.URL+"/feed.xml", "etag", "")
	if err != nil || !poll.NotModified || polls.Load() != 1 {
		t.Fatalf("feed poll = %v, %v, %d requests", poll, err, polls.Load())
	}
}

func TestFeedDirectoryRejectsMalformedURLLocally(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, directoryFixture)
	}))
	defer server.Close()
	engine := scrape.New(scrape.WithInsecureLoopback())
	fetcher := directoryTestFetcher{Engine: engine, extendedURL: server.URL + "/extended"}
	secrets := directoryTestSecrets{"podcast-index-key": "operator-key", "podcast-index-secret": "operator-secret"}
	searcher := newTestFeedDirectorySearcher(t, fetcher, secrets, server.URL, "/search?term={query}", time.Now)
	for _, query := range []string{"https:/example.org/feed?token=x", " HTTP:example.org/feed ", "https://:443/feed", "https://exa mple.org/feed", "https://example.org/feed?q=%"} {
		if _, err := searcher.SearchFeeds(t.Context(), query); !errors.Is(err, graph.ErrFeedSearchInvalidURL) {
			t.Errorf("malformed URL %q: got %v, want invalid URL", query, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("malformed URLs made %d provider requests", requests.Load())
	}
}

func TestFeedDirectoryMalformedQueryReturnsLocalizedGraphError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, directoryFixture)
	}))
	defer server.Close()
	searcher := newTestFeedDirectorySearcher(t, scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "/search?term={query}", time.Now)
	st := storetest.Open(t)
	handler := graph.NewHandler(nil, auth.NewService(st), st, nil, graph.Options{FeedSearcher: searcher})
	body := fmt.Sprintf(`{"query":"query($query: String!) { searchFeeds(query: $query) { feedURL } }","variables":{"query":%q}}`, server.URL+"/feed?q=%")
	req := httptest.NewRequestWithContext(l10n.WithLocale(t.Context(), "fr"), http.MethodPost, "/graphql", strings.NewReader(body))
	req.Header.Set("X-Beamfall-Session", "session-default")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	var response struct {
		Errors []struct {
			Message    string
			Extensions struct{ Code graph.ErrorCode }
		}
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if res.Code != http.StatusOK || len(response.Errors) != 1 || response.Errors[0].Extensions.Code != graph.ErrorCodeInvalid ||
		response.Errors[0].Message != "Saisissez une URL de site web valide." || requests.Load() != 0 {
		t.Fatalf("Graph status %d, body %s, upstream requests %d", res.Code, res.Body, requests.Load())
	}
}

func TestFeedDirectoryFiltersUnsafeHitsBeforeCaching(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/search":
			_, _ = io.WriteString(w, `{"results":[
			  {"collectionName":"Plain HTTP","feedUrl":"http://example.com/feed.xml"},
			  {"collectionName":"Userinfo","feedUrl":"https://user:pass@example.com/feed.xml"},
			  {"collectionName":"Token","feedUrl":"https://example.com/feed.xml?token=x"},
			  {"collectionName":"API token","feedUrl":"https://example.com/feed.xml?api_token=x"},
			  {"collectionName":"Relative","feedUrl":"/feed.xml"},
			  {"collectionName":"Invalid","feedUrl":"https://bad host/feed.xml"},
			  {"collectionName":"Daily","feedUrl":"https://example.com/feed.xml?lang=en"}
			]}`)
		case "/extended":
			_, _ = io.WriteString(w, `{"feeds":[
			  {"title":"API key","url":"https://example.com/extra.xml?%41PI-Key=x"},
			  {"title":"Extra","url":"https://example.com/extra.xml"}
			]}`)
		}
	}))
	defer server.Close()
	fetcher := directoryTestFetcher{Engine: scrape.New(scrape.WithInsecureLoopback()), extendedURL: server.URL + "/extended"}
	secrets := directoryTestSecrets{"podcast-index-key": "operator-key", "podcast-index-secret": "operator-secret"}
	searcher := newTestFeedDirectorySearcher(t, fetcher, secrets, server.URL, "/search?term={query}", time.Now)
	want := []graph.FeedSearchResult{
		{Title: "Daily", FeedURL: "https://example.com/feed.xml?lang=en"},
		{Title: "Extra", FeedURL: "https://example.com/extra.xml"},
	}
	for _, query := range []string{"daily news", " DAILY  NEWS "} {
		if results, err := searcher.SearchFeeds(t.Context(), query); err != nil || !slices.Equal(results, want) || requests.Load() != 2 {
			t.Fatalf("search %q = %v, %v, %d provider requests", query, results, err, requests.Load())
		}
	}
}

func TestFeedDirectoryWebsiteDiscovery(t *testing.T) {
	var crossOriginRequests atomic.Int32
	otherOrigin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		crossOriginRequests.Add(1)
	}))
	defer otherOrigin.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/Start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/news/Index", http.StatusFound)
	})
	mux.HandleFunc("/news/Index", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<!doctype html>
		  <link rel="alternate" type="application/atom+xml" href="/ignore.atom">
		  <link rel="alternate" type="application/rss+xml" href="http://example.com/unsupported.xml">
		  <link rel="alternate" type="application/rss+xml" href="/private.xml?token=x">
		  <LINK REL="alternate" TYPE="application/rss+xml" HREF="Feed.xml?x=Case&amp;y=Two">`)
	})
	mux.HandleFunc("/with-base", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<!doctype html>
		  <template><base href="/template/">
		    <link rel="alternate" type="application/rss+xml" href="unused.xml">
		  </template>
		  <link rel="alternate" type="application/rss+xml" href="daily.xml">
		  <base href="/feeds/">
		  <base href="/ignored/">`)
	})
	mux.HandleFunc("/relative-base", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/news/relative-base", http.StatusFound)
	})
	mux.HandleFunc("/news/relative-base", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<!doctype html>
		  <base href="feeds/">
		  <link rel="alternate" type="application/rss+xml" href="daily.xml">`)
	})
	mux.HandleFunc("/cross-origin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherOrigin.URL, http.StatusFound)
	})
	mux.HandleFunc("/oversized", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat(" ", feedDocumentMaxBytes+1))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	searcher := newTestFeedDirectorySearcher(t, scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "", time.Now)
	results, err := searcher.SearchFeeds(t.Context(), server.URL+"/Start#fragment")
	if err != nil || len(results) != 1 || results[0].FeedURL != server.URL+"/news/Feed.xml?x=Case&y=Two" {
		t.Fatalf("website discovery = %v, %v", results, err)
	}
	for path, want := range map[string]string{"/with-base": "/feeds/daily.xml", "/relative-base": "/news/feeds/daily.xml"} {
		results, err := searcher.SearchFeeds(t.Context(), server.URL+path)
		if err != nil || len(results) != 1 || results[0].FeedURL != server.URL+want {
			t.Errorf("website discovery with base at %s = %v, %v", path, results, err)
		}
	}
	if _, err := searcher.SearchFeeds(t.Context(), server.URL+"/oversized"); err == nil || !strings.Contains(err.Error(), "byte cap") {
		t.Errorf("oversized document = %v", err)
	}
	if _, err := searcher.SearchFeeds(t.Context(), server.URL+"/cross-origin"); err == nil || crossOriginRequests.Load() != 0 {
		t.Fatalf("cross-origin redirect = %v, %d requests to target", err, crossOriginRequests.Load())
	}
	strictSearcher := newTestFeedDirectorySearcher(t, scrape.New(), nil, server.URL, "", time.Now)
	if _, err := strictSearcher.SearchFeeds(t.Context(), server.URL+"/Start"); err == nil {
		t.Fatal("production fetcher accepted a loopback website")
	}
}

type directoryLookupTestFetcher struct {
	*scrape.Engine
	endpoint string
}

func (f directoryLookupTestFetcher) FetchDocumentLimited(ctx context.Context, rawURL string, headers map[string]string, maxBytes int64, admit func() error) ([]byte, *url.URL, error) {
	req, err := url.Parse(rawURL)
	if err != nil || req.Host != "itunes.apple.com" {
		return nil, nil, fmt.Errorf("unexpected upstream URL %q", rawURL)
	}
	return f.Engine.FetchDocumentLimited(ctx, f.endpoint+req.RequestURI(), headers, maxBytes, admit)
}

func TestFeedDirectoryAppleLookup(t *testing.T) {
	var lookups atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookups.Add(1)
		query := r.URL.Query()
		if r.URL.Path != "/lookup" || query.Get("country") != "ca" || query.Has("i") {
			t.Errorf("unexpected lookup %s", r.URL)
		}
		switch query.Get("id") {
		case "1234567":
			_, _ = io.WriteString(w, `{"results":[
			  {"collectionName":"Unsafe","feedUrl":"http://example.com/unsafe.xml"},
			  {"collectionName":"Daily","feedUrl":"https://example.com/Feed.xml","artworkUrl600":"https://example.com/art.jpg"}
			]}`)
		case "7654321":
			_, _ = io.WriteString(w, strings.Repeat(" ", feedDocumentMaxBytes+1))
		default:
			t.Errorf("unexpected show ID %q", query.Get("id"))
		}
	}))
	defer server.Close()
	var hooks app.Hooks
	hooks.MigrationSourceFactories = make(map[app.MigrationSourceKey]app.MigrationSourceFactory)
	addPodcastHooks(&hooks)
	engine := scrape.New(scrape.WithInsecureLoopback())
	searcher, ok := hooks.FeedSearcher.New(engine, nil, filepath.Join(t.TempDir(), "budget.json")).(*feedDirectorySearcher)
	if !ok {
		t.Fatal("compiled feed hook did not build a budgeted directory searcher")
	}
	searcher.engine = directoryLookupTestFetcher{Engine: engine, endpoint: server.URL}
	const show = "https://podcasts.apple.com/ca/podcast/daily/id"
	results, err := searcher.SearchFeeds(t.Context(), show+"1234567?i=890")
	want := []graph.FeedSearchResult{{Title: "Daily", FeedURL: "https://example.com/Feed.xml", ArtworkURL: "https://example.com/art.jpg"}}
	if err != nil || !slices.Equal(results, want) || lookups.Load() != 1 {
		t.Fatalf("Apple lookup = %v, %v, %d requests", results, err, lookups.Load())
	}
	if results, err := searcher.SearchFeeds(t.Context(), show+"invalid"); err != nil || len(results) != 0 || lookups.Load() != 1 {
		t.Fatalf("invalid show URL = %v, %v, %d requests", results, err, lookups.Load())
	}
	if _, err := searcher.SearchFeeds(t.Context(), show+"7654321"); err == nil || !strings.Contains(err.Error(), "byte cap") || lookups.Load() != 2 {
		t.Fatalf("oversized lookup = %v, %d requests", err, lookups.Load())
	}
	for range feedRequestsPerMin - 2 {
		if err := searcher.admitRequest(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := searcher.SearchFeeds(t.Context(), show+"11111"); !errors.Is(err, graph.ErrFeedSearchThrottled) || lookups.Load() != 2 {
		t.Fatalf("lookup at budget capacity = %v, %d requests", err, lookups.Load())
	}
}

func TestFeedDirectoryCountsRedirectAndDoesNotRetry429(t *testing.T) {
	now := func() time.Time { return time.Unix(100, 0) }
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/search":
			if r.URL.Query().Get("term") == "redirect" {
				http.Redirect(w, r, "/target", http.StatusFound)
				return
			}
			_, _ = io.WriteString(w, directoryFixture)
		case "/target":
			t.Error("redirect target reached without a token")
		case "/busy":
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
	defer server.Close()

	searcher := newTestFeedDirectorySearcher(t, scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "/search?term={query}", now)
	for i := range 14 {
		if _, err := searcher.SearchFeeds(t.Context(), fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := searcher.SearchFeeds(t.Context(), "redirect"); !errors.Is(err, graph.ErrFeedSearchThrottled) || requests.Load() != 15 {
		t.Fatalf("redirect without token = %v, %d requests", err, requests.Load())
	}

	fresh := newTestFeedDirectorySearcher(t, scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "/busy", now)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := fresh.SearchFeeds(ctx, "busy"); !errors.Is(err, graph.ErrFeedSearchThrottled) || requests.Load() != 16 {
		t.Fatalf("upstream 429 = %v, %d requests", err, requests.Load())
	}
}

type recordingDirectoryFetcher struct {
	*scrape.Engine
	seenURLs []string
}

func (f *recordingDirectoryFetcher) FetchDocumentLimited(ctx context.Context, rawURL string, headers map[string]string, maxBytes int64, admit func() error) ([]byte, *url.URL, error) {
	f.seenURLs = append(f.seenURLs, rawURL)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, err
	}
	return []byte(`<!doctype html><link rel="alternate" type="application/rss+xml" href="/feed.xml">`), parsed, nil
}

func TestFeedDirectoryPreservesUnicodeIDNAHostname(t *testing.T) {
	fetcher := &recordingDirectoryFetcher{Engine: scrape.New()}
	searcher := newTestFeedDirectorySearcher(t, fetcher, nil, "https://directory.example.com", "", time.Now)

	idnResults, err := searcher.SearchFeeds(t.Context(), "https://İxample.com/")
	if err != nil || len(idnResults) != 1 || idnResults[0].FeedURL != "https://xn--ixample-6he.com/feed.xml" {
		t.Fatalf("IDN search = %+v, err = %v; want punycode host xn--ixample-6he.com", idnResults, err)
	}
	asciiResults, err := searcher.SearchFeeds(t.Context(), "https://IXAMPLE.com/")
	if err != nil || len(asciiResults) != 1 || asciiResults[0].FeedURL != "https://ixample.com/feed.xml" {
		t.Fatalf("ASCII search = %+v, err = %v; want ixample.com", asciiResults, err)
	}
	if len(fetcher.seenURLs) != 2 || fetcher.seenURLs[0] != "https://xn--ixample-6he.com/" || fetcher.seenURLs[1] != "https://ixample.com/" {
		t.Fatalf("fetched URLs = %v, want distinct punycode vs ascii hosts", fetcher.seenURLs)
	}
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/scrape"
)

const directoryFixture = `{"results":[{"collectionName":"Daily","feedUrl":"https://example.com/feed.xml"}]}`

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
	searcher := newFeedDirectorySearcher(scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "/search?term={query}", func() time.Time { return now })

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
	searcher := newFeedDirectorySearcher(nil, nil, "", "", func() time.Time { return now })
	for i := range feedSearchCacheSize + 1 {
		searcher.cacheResult(fmt.Sprint(i), nil)
		now = now.Add(time.Second)
	}
	if got := len(searcher.cache); got != feedSearchCacheSize {
		t.Fatalf("cache length = %d, want %d", got, feedSearchCacheSize)
	}
	if _, ok := searcher.cachedResult("0"); ok {
		t.Fatal("oldest result survived capacity eviction")
	}
	if _, ok := searcher.cachedResult(fmt.Sprint(feedSearchCacheSize)); !ok {
		t.Fatal("newest empty result was not cached")
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
	searcher := newFeedDirectorySearcher(scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "/search?term={query}", func() time.Time { return now })
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

	now = now.Add(time.Minute - time.Nanosecond)
	if _, err := searcher.SearchFeeds(ctx, "still throttled"); !errors.Is(err, graph.ErrFeedSearchThrottled) {
		t.Fatalf("before refill: %v", err)
	}
	now = now.Add(time.Nanosecond)
	if _, err := searcher.SearchFeeds(ctx, "after refill"); err != nil || requests.Load() != 16 {
		t.Fatalf("after refill: %v, %d requests", err, requests.Load())
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
	withoutKey := newFeedDirectorySearcher(fetcher, directoryTestSecrets{"podcast-index-secret": "operator-secret"}, server.URL, "/search?term={query}", now)
	results, err := withoutKey.SearchFeeds(t.Context(), "one")
	if err != nil || len(results) != 1 || primary.Load() != 1 || extended.Load() != 0 {
		t.Fatalf("search without key = %v, %v, primary=%d extended=%d", results, err, primary.Load(), extended.Load())
	}
	withKey := newFeedDirectorySearcher(fetcher, directoryTestSecrets{"podcast-index-key": "operator-key", "podcast-index-secret": "operator-secret"}, server.URL, "/search?term={query}", now)
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

func TestFeedDirectoryAppleLookup(t *testing.T) {
	var lookups atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookups.Add(1)
		if r.URL.Path != "/lookup" || r.URL.Query().Get("id") != "1234567" || r.URL.Query().Get("country") != "ca" {
			t.Errorf("unexpected lookup %s", r.URL)
		}
		_, _ = io.WriteString(w, `{"results":[
		  {"collectionName":"Invalid","feedUrl":"http://example.com/feed.xml"},
		  {"collectionName":"Daily","feedUrl":"https://example.com/Feed.xml","artworkUrl600":"https://example.com/art.jpg"}
		]}`)
	}))
	defer server.Close()
	searcher := newFeedDirectorySearcher(scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "/search?term={query}", time.Now)
	results, err := searcher.SearchFeeds(t.Context(), "https://podcasts.apple.com/ca/podcast/daily/id1234567?i=890")
	if err != nil || len(results) != 1 || results[0].Title != "Daily" || results[0].FeedURL != "https://example.com/Feed.xml" || lookups.Load() != 1 {
		t.Fatalf("Apple lookup = %v, %v, %d requests", results, err, lookups.Load())
	}
	if results, err := searcher.SearchFeeds(t.Context(), "https://podcasts.apple.com/ca/podcast/daily/idinvalid"); err != nil || len(results) != 0 || lookups.Load() != 1 {
		t.Fatalf("invalid show URL = %v, %v, %d requests", results, err, lookups.Load())
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
		  <LINK REL="alternate" TYPE="application/rss+xml" HREF="Feed.xml?x=Case&amp;y=Two">`)
	})
	mux.HandleFunc("/cross-origin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherOrigin.URL, http.StatusFound)
	})
	for _, path := range []string{"/oversized", "/lookup"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat(" ", feedDocumentMaxBytes+1))
		})
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	searcher := newFeedDirectorySearcher(scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "", time.Now)
	results, err := searcher.SearchFeeds(t.Context(), server.URL+"/Start#fragment")
	if err != nil || len(results) != 1 || results[0].FeedURL != server.URL+"/news/Feed.xml?x=Case&y=Two" {
		t.Fatalf("website discovery = %v, %v", results, err)
	}
	for _, query := range []string{server.URL + "/oversized", "https://podcasts.apple.com/ca/podcast/daily/id1234567"} {
		if _, err := searcher.SearchFeeds(t.Context(), query); err == nil || !strings.Contains(err.Error(), "byte cap") {
			t.Errorf("oversized document = %v", err)
		}
	}
	if _, err := searcher.SearchFeeds(t.Context(), server.URL+"/cross-origin"); err == nil || crossOriginRequests.Load() != 0 {
		t.Fatalf("cross-origin redirect = %v, %d requests to target", err, crossOriginRequests.Load())
	}
	strictSearcher := newFeedDirectorySearcher(scrape.New(), nil, server.URL, "", time.Now)
	if _, err := strictSearcher.SearchFeeds(t.Context(), server.URL+"/Start"); err == nil {
		t.Fatal("production fetcher accepted a loopback website")
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

	searcher := newFeedDirectorySearcher(scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "/search?term={query}", now)
	for i := range 14 {
		if _, err := searcher.SearchFeeds(t.Context(), fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := searcher.SearchFeeds(t.Context(), "redirect"); !errors.Is(err, graph.ErrFeedSearchThrottled) || requests.Load() != 15 {
		t.Fatalf("redirect without token = %v, %d requests", err, requests.Load())
	}

	fresh := newFeedDirectorySearcher(scrape.New(scrape.WithInsecureLoopback()), nil, server.URL, "/busy", now)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := fresh.SearchFeeds(ctx, "busy"); !errors.Is(err, graph.ErrFeedSearchThrottled) || requests.Load() != 16 {
		t.Fatalf("upstream 429 = %v, %d requests", err, requests.Load())
	}
}

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/scrape"
)

type mapSecrets map[string]string

func (m mapSecrets) Secret(name string) (string, error) {
	if v, ok := m[name]; ok {
		return v, nil
	}
	return "", errors.New("missing")
}

type trendingFixture struct {
	hits     atomic.Int32
	lastReq  atomic.Pointer[http.Request]
	searcher feedDirectorySearcher
}

func newTrendingFixture(t *testing.T, secrets mapSecrets, appleChart bool) *trendingFixture {
	t.Helper()
	f := &trendingFixture{}
	mux := http.NewServeMux()
	mux.HandleFunc("/pi", func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.lastReq.Store(r)
		_, _ = w.Write([]byte(`{"feeds":[{"title":"PI Show","url":"https://feeds.example.com/pi.xml","image":"https://img.example.com/pi.jpg","description":"d"}]}`))
	})
	mux.HandleFunc("/chart/api/v2/ca/podcasts/top/25/podcasts.json", func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.lastReq.Store(r)
		_, _ = w.Write([]byte(`{"feed":{"results":[{"id":"11","name":"A"},{"id":"22","name":"B"},{"id":"x","name":"bad"}]}}`))
	})
	mux.HandleFunc("/lookup", func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.lastReq.Store(r)
		_, _ = w.Write([]byte(`{"results":[{"collectionId":22,"collectionName":"B","feedUrl":"https://feeds.example.com/b.xml","artworkUrl600":"https://img.example.com/b.jpg"},{"collectionId":11,"collectionName":"A","feedUrl":"https://feeds.example.com/a.xml"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	state := newTrendingState(appleChart)
	state.indexURL = srv.URL + "/pi"
	state.appleChartBase = srv.URL + "/chart"
	state.appleLookupBase = srv.URL
	f.searcher = feedDirectorySearcher{engine: scrape.New(scrape.WithInsecureLoopback()), secrets: secrets, trending: state}
	return f
}

func TestTrendingFeedsDisabledMakesNoRequests(t *testing.T) {
	f := newTrendingFixture(t, mapSecrets{}, false)
	got, err := f.searcher.TrendingFeeds(context.Background(), "ca", "fr")
	if err != nil || len(got) != 0 || f.hits.Load() != 0 {
		t.Fatalf("got %v err %v hits %d, want empty and no requests", got, err, f.hits.Load())
	}
}

func TestTrendingFeedsPodcastIndexSendsOnlyLocaleAndSignedAuth(t *testing.T) {
	f := newTrendingFixture(t, mapSecrets{"podcast-index-key": "k", "podcast-index-secret": "s"}, false)
	got, err := f.searcher.TrendingFeeds(context.Background(), "ca", "fr")
	if err != nil || len(got) != 1 || got[0].Source != graph.TrendingSourceIndex || got[0].ArtworkURL == "" {
		t.Fatalf("got %+v err %v", got, err)
	}
	req := f.lastReq.Load()
	if req.URL.Query().Get("lang") != "fr" || req.Header.Get("X-Auth-Key") != "k" || req.Header.Get("Authorization") == "" {
		t.Fatalf("request %s headers %v", req.URL, req.Header)
	}
	if strings.Contains(req.URL.RawQuery, "ca") && strings.Contains(req.URL.RawQuery, "region") {
		t.Fatalf("unexpected region in %s", req.URL.RawQuery)
	}
	if req.Header.Get("Cookie") != "" || req.Header.Get("Referer") != "" {
		t.Fatalf("identifying headers sent: %v", req.Header)
	}
}

func TestTrendingFeedsAppleChartKeepsOrderAndSkipsBadIDs(t *testing.T) {
	f := newTrendingFixture(t, mapSecrets{}, true)
	got, err := f.searcher.TrendingFeeds(context.Background(), "ca", "en")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if got[0].Title != "A" || got[1].FeedURL != "https://feeds.example.com/b.xml" || got[0].Source != graph.TrendingSourceChart {
		t.Fatalf("order/fields wrong: %+v", got)
	}
	if id := f.lastReq.Load().URL.Query().Get("id"); id != "11,22" {
		t.Fatalf("lookup id = %q, want only numeric ids", id)
	}
}

func TestTrendingFeedsCachesAndDegradesPerSource(t *testing.T) {
	f := newTrendingFixture(t, mapSecrets{"podcast-index-key": "k", "podcast-index-secret": "s"}, true)
	now := time.Unix(1_000_000, 0)
	f.searcher.trending.now = func() time.Time { return now }
	// Break the chart so only Podcast Index results arrive.
	f.searcher.trending.appleChartBase = "http://127.0.0.1:1/none"
	got, _ := f.searcher.TrendingFeeds(context.Background(), "ca", "en")
	if len(got) != 1 || got[0].Source != graph.TrendingSourceIndex {
		t.Fatalf("got %+v, want PI only when chart fails", got)
	}
	before := f.hits.Load()
	_, _ = f.searcher.TrendingFeeds(context.Background(), "ca", "en")
	if f.hits.Load() != before {
		t.Fatal("second call within TTL hit upstream")
	}
	now = now.Add(2 * time.Hour)
	_, _ = f.searcher.TrendingFeeds(context.Background(), "ca", "en")
	if f.hits.Load() == before {
		t.Fatal("expired entry was not refetched")
	}
}

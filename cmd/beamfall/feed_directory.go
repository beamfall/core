package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/scrape"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	feedSearchTTL        = 10 * time.Minute
	feedSearchCacheSize  = 64
	feedRequestsPerMin   = 15
	feedDocumentMaxBytes = 1 << 20
)

type feedDirectoryFetcher interface {
	CheckDocumentURL(*url.URL) error
	FetchDocumentLimited(context.Context, string, map[string]string, int64, func() error) ([]byte, *url.URL, error)
}

type feedDirectorySearcher struct {
	engine   feedDirectoryFetcher
	secrets  app.SecretSource
	endpoint string
	template string
	now      func() time.Time

	mu        sync.Mutex
	cache     map[string]cachedFeedSearch
	tokens    [feedRequestsPerMin]time.Time
	nextToken int
}

type cachedFeedSearch struct {
	results []graph.FeedSearchResult
	expires time.Time
}

func newFeedDirectorySearcher(engine feedDirectoryFetcher, secrets app.SecretSource, endpoint, template string, now func() time.Time) *feedDirectorySearcher {
	return &feedDirectorySearcher{
		engine:   engine,
		secrets:  secrets,
		endpoint: strings.TrimRight(endpoint, "/"),
		template: template,
		now:      now,
		cache:    make(map[string]cachedFeedSearch),
	}
}

func (s *feedDirectorySearcher) SearchFeeds(ctx context.Context, query string) ([]graph.FeedSearchResult, error) {
	query = strings.TrimSpace(query)
	link, err := url.Parse(query)
	if err != nil || !link.IsAbs() || link.Host == "" {
		text := strings.Join(strings.Fields(query), " ")
		key := strings.ToLower(text)
		if strings.HasPrefix(key, "https://") || strings.HasPrefix(key, "http://") {
			return nil, errors.New("invalid feed discovery URL")
		}
		return s.searchCached(key, func() ([]graph.FeedSearchResult, error) {
			return s.searchText(ctx, text)
		})
	}

	if err := s.engine.CheckDocumentURL(link); err != nil {
		return nil, err
	}
	link.Host = strings.ToLower(link.Host)
	link.Fragment = ""
	link.RawFragment = ""
	return s.searchCached(link.String(), func() ([]graph.FeedSearchResult, error) {
		if link.Hostname() == "podcasts.apple.com" {
			return s.lookupAppleFeed(ctx, link)
		}
		return s.discoverWebsiteFeed(ctx, link)
	})
}

func (s *feedDirectorySearcher) searchCached(key string, search func() ([]graph.FeedSearchResult, error)) ([]graph.FeedSearchResult, error) {
	if cached, ok := s.cachedResult(key); ok {
		return cached, nil
	}
	results, err := search()
	if err == nil {
		s.cacheResult(key, results)
	}
	return results, err
}

func (s *feedDirectorySearcher) cachedResult(key string) ([]graph.FeedSearchResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cache[key]
	if !ok {
		return nil, false
	}
	if !s.now().Before(entry.expires) {
		delete(s.cache, key)
		return nil, false
	}
	return slices.Clone(entry.results), true
}

func (s *feedDirectorySearcher) cacheResult(key string, results []graph.FeedSearchResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.cache[key]; !exists && len(s.cache) >= feedSearchCacheSize {
		var oldestKey string
		var oldest time.Time
		for candidate, entry := range s.cache {
			if oldest.IsZero() || entry.expires.Before(oldest) {
				oldestKey, oldest = candidate, entry.expires
			}
		}
		delete(s.cache, oldestKey)
	}
	s.cache[key] = cachedFeedSearch{results: slices.Clone(results), expires: s.now().Add(feedSearchTTL)}
}

func (s *feedDirectorySearcher) admitRequest() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Each token refills one minute after use. The oldest token is first in
	// line, enforcing the budget across every rolling minute, even at startup.
	now := s.now()
	if now.Before(s.tokens[s.nextToken]) {
		return graph.ErrFeedSearchThrottled
	}
	s.tokens[s.nextToken] = now.Add(time.Minute)
	s.nextToken = (s.nextToken + 1) % len(s.tokens)
	return nil
}

// FetchDocument keeps the signed extended directory on the same budget,
// response cap and fetcher as the primary directory.
func (s *feedDirectorySearcher) FetchDocument(ctx context.Context, rawURL string, headers map[string]string) ([]byte, error) {
	data, _, err := s.fetchDocument(ctx, rawURL, headers)
	return data, err
}

func (s *feedDirectorySearcher) fetchDocument(ctx context.Context, rawURL string, headers map[string]string) ([]byte, *url.URL, error) {
	data, page, err := s.engine.FetchDocumentLimited(ctx, rawURL, headers, feedDocumentMaxBytes, s.admitRequest)
	if errors.Is(err, scrape.ErrDocumentThrottled) {
		return nil, nil, graph.ErrFeedSearchThrottled
	}
	return data, page, err
}

func (s *feedDirectorySearcher) searchText(ctx context.Context, query string) ([]graph.FeedSearchResult, error) {
	if s.endpoint == "" {
		return nil, errors.New("feed directory scraper is not registered")
	}
	rawURL := s.endpoint + strings.ReplaceAll(s.template, "{query}", url.QueryEscape(query))
	results, err := s.readITunesResults(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	if extended, err := s.searchExtendedDirectory(ctx, query); err == nil {
		results = append(results, extended...)
	}
	return results, nil
}

func (s *feedDirectorySearcher) lookupAppleFeed(ctx context.Context, link *url.URL) ([]graph.FeedSearchResult, error) {
	id, ok := strings.CutPrefix(path.Base(link.Path), "id")
	if !ok || id == "" || strings.Trim(id, "0123456789") != "" {
		return nil, nil
	}
	if s.endpoint == "" {
		return nil, errors.New("feed directory scraper is not registered")
	}
	query := url.Values{"id": {id}}
	if country, _, _ := strings.Cut(strings.TrimPrefix(link.Path, "/"), "/"); len(country) == 2 {
		query.Set("country", country)
	}
	results, err := s.readITunesResults(ctx, s.endpoint+"/lookup?"+query.Encode())
	if err != nil {
		return nil, err
	}
	for _, result := range results {
		if feed, err := url.Parse(result.FeedURL); err == nil && s.engine.CheckDocumentURL(feed) == nil {
			return []graph.FeedSearchResult{result}, nil
		}
	}
	return nil, nil
}

func (s *feedDirectorySearcher) readITunesResults(ctx context.Context, rawURL string) ([]graph.FeedSearchResult, error) {
	data, err := s.FetchDocument(ctx, rawURL, nil)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Results []struct {
			Title       string `json:"collectionName"`
			FeedURL     string `json:"feedUrl"`
			ArtworkURL  string `json:"artworkUrl600"`
			Description string `json:"description"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	results := make([]graph.FeedSearchResult, 0, len(parsed.Results))
	for _, item := range parsed.Results {
		results = append(results, graph.FeedSearchResult{Title: item.Title, FeedURL: item.FeedURL, ArtworkURL: item.ArtworkURL, Description: item.Description})
	}
	return results, nil
}

func (s *feedDirectorySearcher) discoverWebsiteFeed(ctx context.Context, link *url.URL) ([]graph.FeedSearchResult, error) {
	data, page, err := s.fetchDocument(ctx, link.String(), nil)
	if err != nil {
		return nil, err
	}
	tokens := html.NewTokenizer(bytes.NewReader(data))
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			return nil, nil
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokens.Token()
			if token.DataAtom != atom.Link {
				continue
			}
			href := rssAlternateLink(token.Attr)
			if href == "" {
				continue
			}
			feed, err := page.Parse(href)
			if err == nil && s.engine.CheckDocumentURL(feed) == nil {
				return []graph.FeedSearchResult{{Title: page.Hostname(), FeedURL: feed.String()}}, nil
			}
		}
	}
}

func rssAlternateLink(attrs []html.Attribute) string {
	var alternate, rss bool
	var href string
	for _, attr := range attrs {
		switch attr.Key {
		case "rel":
			alternate = slices.Contains(strings.Fields(strings.ToLower(attr.Val)), "alternate")
		case "type":
			rss = strings.EqualFold(strings.TrimSpace(attr.Val), "application/rss+xml")
		case "href":
			href = strings.TrimSpace(attr.Val)
		}
	}
	if alternate && rss {
		return href
	}
	return ""
}

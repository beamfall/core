package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/jsonstate"
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
	// lookup lets the owning plugin resolve a directory page link through
	// this searcher's budgeted fetcher. matched=false falls back to discovery.
	lookup func(context.Context, *url.URL) (results []graph.FeedSearchResult, matched bool, err error)

	mu       sync.Mutex
	cache    map[string]cachedFeedSearch
	inFlight map[string]*feedSearchCall

	budgetMu     sync.Mutex
	budgetFile   string
	budgetLoaded bool
	reserved     []time.Time // refill deadlines of the tokens in use
}

type cachedFeedSearch struct {
	results []graph.FeedSearchResult
	expires time.Time
}

type feedSearchCall struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	results []graph.FeedSearchResult
	err     error
}

func newFeedDirectorySearcher(engine feedDirectoryFetcher, secrets app.SecretSource, endpoint, template, budgetFile string, now func() time.Time) *feedDirectorySearcher {
	return &feedDirectorySearcher{
		engine:     engine,
		secrets:    secrets,
		endpoint:   strings.TrimRight(endpoint, "/"),
		template:   template,
		now:        now,
		cache:      make(map[string]cachedFeedSearch),
		inFlight:   make(map[string]*feedSearchCall),
		budgetFile: budgetFile,
	}
}

func (s *feedDirectorySearcher) SearchFeeds(ctx context.Context, query string) ([]graph.FeedSearchResult, error) {
	query = strings.TrimSpace(query)
	link, err := url.Parse(query)
	if err != nil || !link.IsAbs() || link.Hostname() == "" {
		text := strings.Join(strings.Fields(query), " ")
		key := strings.ToLower(text)
		if strings.HasPrefix(key, "https:") || strings.HasPrefix(key, "http:") {
			return nil, graph.ErrFeedSearchInvalidURL
		}
		return s.searchCached(ctx, key, func(ctx context.Context) ([]graph.FeedSearchResult, error) {
			return s.searchText(ctx, text)
		})
	}

	if err := s.engine.CheckDocumentURL(link); err != nil {
		return nil, graph.ErrFeedSearchInvalidURL
	}
	link.Host = strings.ToLower(link.Host)
	link.Fragment = ""
	link.RawFragment = ""
	return s.searchCached(ctx, link.String(), func(ctx context.Context) ([]graph.FeedSearchResult, error) {
		if s.lookup != nil {
			results, matched, err := s.lookup(ctx, link)
			if matched {
				if err != nil {
					return nil, err
				}
				results = s.validFeedResults(results)
				return results[:min(1, len(results))], nil
			}
		}
		return s.discoverWebsiteFeed(ctx, link)
	})
}

func (s *feedDirectorySearcher) searchCached(ctx context.Context, key string, search func(context.Context) ([]graph.FeedSearchResult, error)) ([]graph.FeedSearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if entry, ok := s.cache[key]; ok && s.now().Before(entry.expires) {
		s.mu.Unlock()
		return slices.Clone(entry.results), nil
	}
	call := s.inFlight[key]
	if call == nil {
		delete(s.cache, key)
		// The callers own the lifetime collectively. Each waits with its own
		// deadline, and the last to leave cancels the upstream work.
		shared, cancel := context.WithCancel(context.WithoutCancel(ctx))
		call = &feedSearchCall{done: make(chan struct{}), cancel: cancel}
		s.inFlight[key] = call
		go s.runSearch(shared, key, call, search)
	}
	call.waiters++
	s.mu.Unlock()

	select {
	case <-call.done:
		return slices.Clone(call.results), call.err
	case <-ctx.Done():
		s.mu.Lock()
		call.waiters--
		if call.waiters == 0 && s.inFlight[key] == call {
			delete(s.inFlight, key)
			call.cancel()
		}
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (s *feedDirectorySearcher) runSearch(ctx context.Context, key string, call *feedSearchCall, search func(context.Context) ([]graph.FeedSearchResult, error)) {
	defer call.cancel()
	results, err := search(ctx)
	s.mu.Lock()
	if s.inFlight[key] == call {
		if err == nil {
			s.cacheResultLocked(key, results)
		}
		delete(s.inFlight, key)
	}
	// Closing done publishes the result to waiters. Every caller gets a copy.
	call.results, call.err = results, err
	close(call.done)
	s.mu.Unlock()
}

func (s *feedDirectorySearcher) cacheResultLocked(key string, results []graph.FeedSearchResult) {
	if len(s.cache) >= feedSearchCacheSize {
		var oldestKey string
		var oldest time.Time
		for candidate, entry := range s.cache {
			if oldest.IsZero() || entry.expires.Before(oldest) {
				oldestKey, oldest = candidate, entry.expires
			}
		}
		delete(s.cache, oldestKey)
	}
	s.cache[key] = cachedFeedSearch{results: results, expires: s.now().Add(feedSearchTTL)}
}

func (s *feedDirectorySearcher) admitRequest() error {
	s.budgetMu.Lock()
	defer s.budgetMu.Unlock()
	if !s.budgetLoaded {
		// Load lazily so a read failure is retried by the next request
		// instead of disabling search until restart. A file that does not
		// decode is replaced: it can refund at most one minute of requests.
		var saved []time.Time
		found, err := jsonstate.Load(s.budgetFile, &saved)
		if err != nil && !found {
			return fmt.Errorf("load feed request budget: %w", err)
		}
		if err != nil {
			saved = nil
		}
		s.reserved, s.budgetLoaded = saved, true
	}
	// Each token refills one minute after use. Persist the new deadline
	// before admission so a restart cannot refund a request already sent.
	// A deadline beyond one minute comes from a clock that moved back.
	now := s.now()
	active := slices.DeleteFunc(slices.Clone(s.reserved), func(deadline time.Time) bool {
		return !now.Before(deadline) || deadline.After(now.Add(time.Minute))
	})
	if len(active) >= feedRequestsPerMin {
		return graph.ErrFeedSearchThrottled
	}
	active = append(active, now.Add(time.Minute))
	if err := jsonstate.Save(s.budgetFile, active); err != nil {
		return fmt.Errorf("persist feed request budget: %w", err)
	}
	s.reserved = active
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
	return s.validFeedResults(results), nil
}

func (s *feedDirectorySearcher) validFeedResults(results []graph.FeedSearchResult) []graph.FeedSearchResult {
	return slices.DeleteFunc(results, func(result graph.FeedSearchResult) bool {
		feed, err := url.Parse(result.FeedURL)
		return err != nil || s.engine.CheckDocumentURL(feed) != nil
	})
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
	document, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	base := documentBaseURL(document, page)
	for node := range document.Descendants() {
		if !isDocumentElement(node, atom.Link) {
			continue
		}
		href := rssAlternateLink(node.Attr)
		if href == "" {
			continue
		}
		feed, err := base.Parse(href)
		if err == nil && s.engine.CheckDocumentURL(feed) == nil {
			return []graph.FeedSearchResult{{Title: page.Hostname(), FeedURL: feed.String()}}, nil
		}
	}
	return nil, nil
}

func documentBaseURL(document *html.Node, page *url.URL) *url.URL {
	for node := range document.Descendants() {
		if !isDocumentElement(node, atom.Base) {
			continue
		}
		for _, attr := range node.Attr {
			if attr.Key != "href" {
				continue
			}
			if base, err := page.Parse(strings.TrimSpace(attr.Val)); err == nil && base.Scheme != "data" && base.Scheme != "javascript" {
				return base
			}
			return page
		}
	}
	return page
}

func isDocumentElement(node *html.Node, tag atom.Atom) bool {
	if node.Type != html.ElementNode || node.DataAtom != tag || node.Namespace != "" {
		return false
	}
	for ancestor := range node.Ancestors() {
		if ancestor.DataAtom == atom.Template && ancestor.Namespace == "" {
			return false
		}
	}
	return true
}

func rssAlternateLink(attrs []html.Attribute) string {
	var alternate, rss bool
	var href string
	for _, attr := range attrs {
		switch attr.Key {
		case "rel":
			alternate = slices.Contains(strings.Fields(strings.ToLower(attr.Val)), "alternate")
		case "type":
			mediaType, _, err := mime.ParseMediaType(attr.Val)
			rss = (err == nil && mediaType == "application/rss+xml") || strings.EqualFold(strings.TrimSpace(attr.Val), "application/rss+xml")
		case "href":
			href = strings.TrimSpace(attr.Val)
		}
	}
	if alternate && rss {
		return href
	}
	return ""
}

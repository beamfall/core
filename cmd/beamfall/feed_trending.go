package main

import (
	"context"
	"crypto/sha1" //nolint:gosec // the directory API mandates a SHA-1 request signature; not used for secrecy
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/scrape"
)

// Trending shows (CORE-3554). Two public directories, each off until the
// operator opts in: the keyed index's trending list needs the operator key and secret
// searchFeeds already reads; Apple's top-shows chart needs
// the apple-chart operator env var. A request carries the server's fixed
// User-Agent plus a two-letter region and language, nothing about an install,
// Profile or library.
const (
	trendingLimit      = 25
	trendingTTL        = time.Hour
	trendingFailureTTL = 5 * time.Minute
	indexTrendURL      = "https://api.podcastindex.org/api/1.0/podcasts/trending"
	appleChartBase     = "https://rss.marketingtools.apple.com"
	appleLookupBase    = "https://itunes.apple.com"
)

type trendingEntry struct {
	results []graph.FeedTrendingResult
	expires time.Time
}

// trendingState is the per-server cache and endpoint set. The bases are fields
// so tests point them at loopback fixtures.
type trendingState struct {
	indexURL        string
	appleChartBase  string
	appleLookupBase string
	appleChart      bool
	now             func() time.Time

	mu    sync.Mutex
	cache map[string]trendingEntry
}

func newTrendingState(appleChart bool) *trendingState {
	return &trendingState{
		indexURL:        indexTrendURL,
		appleChartBase:  appleChartBase,
		appleLookupBase: appleLookupBase,
		appleChart:      appleChart,
		now:             time.Now,
		cache:           map[string]trendingEntry{},
	}
}

// TrendingFeeds returns the keyed index trending list (when keyed) followed by the
// Apple chart (when opted in). One failing source never hides the other, and
// with neither enabled no request is made.
func (s feedDirectorySearcher) TrendingFeeds(ctx context.Context, region, language string) ([]graph.FeedTrendingResult, error) {
	state := s.trending
	if state == nil {
		return []graph.FeedTrendingResult{}, nil
	}
	out := []graph.FeedTrendingResult{}
	if key, secret, ok := s.indexCredentials(); ok {
		out = append(out, state.cached("pi:"+language, func() ([]graph.FeedTrendingResult, error) {
			return state.fetchIndex(ctx, s.engine, key, secret, language)
		})...)
	}
	if state.appleChart {
		out = append(out, state.cached("apple:"+region, func() ([]graph.FeedTrendingResult, error) {
			return state.fetchAppleChart(ctx, s.engine, region)
		})...)
	}
	return out, nil
}

func (s feedDirectorySearcher) indexCredentials() (key, secret string, ok bool) {
	key, keyErr := s.secrets.Secret("podcast-index-key")
	secret, secretErr := s.secrets.Secret("podcast-index-secret")
	if keyErr != nil || secretErr != nil || strings.TrimSpace(key) == "" || strings.TrimSpace(secret) == "" {
		return "", "", false
	}
	return strings.TrimSpace(key), strings.TrimSpace(secret), true
}

// cached serves a fresh entry, else fetches under the lock so concurrent
// readers share one upstream call. A failure is cached as empty for a short
// period so an outage is not retried per request.
func (st *trendingState) cached(key string, fetch func() ([]graph.FeedTrendingResult, error)) []graph.FeedTrendingResult {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	if entry, ok := st.cache[key]; ok && now.Before(entry.expires) {
		return entry.results
	}
	results, err := fetch()
	if err != nil {
		st.cache[key] = trendingEntry{expires: now.Add(trendingFailureTTL)}
		return nil
	}
	st.cache[key] = trendingEntry{results: results, expires: now.Add(trendingTTL)}
	return results
}

func (st *trendingState) fetchIndex(ctx context.Context, engine *scrape.Engine, key, secret, language string) ([]graph.FeedTrendingResult, error) {
	unix := strconv.FormatInt(st.now().UTC().Unix(), 10)
	sum := sha1.Sum([]byte(key + secret + unix)) //nolint:gosec // signature scheme mandated by the directory API
	headers := map[string]string{
		"X-Auth-Key":    key,
		"X-Auth-Date":   unix,
		"Authorization": hex.EncodeToString(sum[:]),
	}
	data, err := engine.FetchDocument(ctx, fmt.Sprintf("%s?max=%d&lang=%s", st.indexURL, trendingLimit, url.QueryEscape(language)), headers)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Feeds []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Image       string `json:"image"`
			Artwork     string `json:"artwork"`
			Description string `json:"description"`
		} `json:"feeds"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	out := make([]graph.FeedTrendingResult, 0, len(parsed.Feeds))
	for _, feed := range parsed.Feeds {
		art := feed.Artwork
		if art == "" {
			art = feed.Image
		}
		out = append(out, graph.FeedTrendingResult{Title: feed.Title, FeedURL: feed.URL, ArtworkURL: art, Description: feed.Description, Source: graph.TrendingSourceIndex})
	}
	return out, nil
}

// fetchAppleChart reads the regional top-shows chart, then resolves the chart's
// ids to feed addresses with one iTunes lookup, keeping the chart's order.
func (st *trendingState) fetchAppleChart(ctx context.Context, engine *scrape.Engine, region string) ([]graph.FeedTrendingResult, error) {
	chartURL := fmt.Sprintf("%s/api/v2/%s/podcasts/top/%d/podcasts.json", st.appleChartBase, url.PathEscape(region), trendingLimit)
	data, err := engine.FetchDocument(ctx, chartURL, nil)
	if err != nil {
		return nil, err
	}
	var chart struct {
		Feed struct {
			Results []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"results"`
		} `json:"feed"`
	}
	if err := json.Unmarshal(data, &chart); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(chart.Feed.Results))
	for _, item := range chart.Feed.Results {
		if _, err := strconv.ParseUint(item.ID, 10, 64); err == nil {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		return []graph.FeedTrendingResult{}, nil
	}
	lookupURL := fmt.Sprintf("%s/lookup?id=%s&entity=podcast&country=%s", st.appleLookupBase, strings.Join(ids, ","), url.QueryEscape(region))
	data, err = engine.FetchDocument(ctx, lookupURL, nil)
	if err != nil {
		return nil, err
	}
	var lookup struct {
		Results []struct {
			ID         json.Number `json:"collectionId"`
			Name       string      `json:"collectionName"`
			FeedURL    string      `json:"feedUrl"`
			ArtworkURL string      `json:"artworkUrl600"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &lookup); err != nil {
		return nil, err
	}
	byID := make(map[string]int, len(lookup.Results))
	for i, item := range lookup.Results {
		byID[item.ID.String()] = i
	}
	out := make([]graph.FeedTrendingResult, 0, len(ids))
	for _, id := range ids {
		i, ok := byID[id]
		if !ok || lookup.Results[i].FeedURL == "" {
			continue // an entry without a public feed cannot be followed
		}
		item := lookup.Results[i]
		out = append(out, graph.FeedTrendingResult{Title: item.Name, FeedURL: item.FeedURL, ArtworkURL: item.ArtworkURL, Source: graph.TrendingSourceChart})
	}
	return out, nil
}

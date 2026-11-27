package graph

import (
	"context"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Trending sources (CORE-3554): the closed provenance vocabulary clients label
// results with.
const (
	TrendingSourceIndex = "directory-index"
	TrendingSourceChart = "directory-chart"
)

// FeedTrendingResult is one trending entry: a public feed address a person can
// follow, with the directory it came from.
type FeedTrendingResult struct {
	Title       string `json:"title"`
	FeedURL     string `json:"feedURL"`
	ArtworkURL  string `json:"artworkURL"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

// FeedTrender is implemented by a FeedSearcher that can also list currently
// popular feeds. region and language are validated two-letter codes.
type FeedTrender interface {
	TrendingFeeds(ctx context.Context, region, language string) ([]FeedTrendingResult, error)
}

// trendingFeeds lists popular feeds from the directories the operator opted
// into; with none enabled the searcher returns an empty list and makes no call.
func (h *Handler) trendingFeeds(ctx context.Context, scope auth.Scope, req request) ([]FeedTrendingResult, error) {
	if scope.DeviceRole != store.DeviceRoleManager && scope.Profile != store.ProfileAdult {
		return nil, clientError("adult profile or manager role required")
	}
	trender, ok := h.feedSearcher.(FeedTrender)
	if !ok {
		return []FeedTrendingResult{}, nil
	}
	results, err := trender.TrendingFeeds(ctx, TrendingCode(stringVar(req, "region"), "us"), TrendingCode(stringVar(req, "language"), "en"))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	deduped := make([]FeedTrendingResult, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.FeedURL) == "" || seen[result.FeedURL] {
			continue
		}
		seen[result.FeedURL] = true
		deduped = append(deduped, result)
	}
	return deduped, nil
}

// TrendingCode lowercases a two-letter ASCII region or language code and
// returns fallback for anything else, so no free-form client text reaches a
// directory request.
func TrendingCode(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 2 || value[0] < 'a' || value[0] > 'z' || value[1] < 'a' || value[1] > 'z' {
		return fallback
	}
	return value
}

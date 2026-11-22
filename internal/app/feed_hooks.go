package app

import (
	"context"
	"log/slog"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/ingest"
	"github.com/beamfall/core/internal/store"
)

// feedHooks adapts the ingest Poller to the graph's feed probe and
// poll-now hooks. Feed URLs are never logged.
type feedHooks struct {
	poller *ingest.Poller
	logger *slog.Logger
}

func (h feedHooks) ProbeFeed(ctx context.Context, feedURL string) (graph.FeedProbe, error) {
	title, entries, bytes, err := h.poller.ProbeFeed(ctx, feedURL)
	if err != nil {
		return graph.FeedProbe{}, err
	}
	return graph.FeedProbe{Title: title, EntryCount: entries, CatalogBytes: bytes}, nil
}

func (h feedHooks) PollSubscriptionNow(ctx context.Context, id store.SubscriptionID) {
	if err := h.poller.PollSubscriptionNow(ctx, id); err != nil && h.logger != nil {
		h.logger.WarnContext(ctx, "subscription refresh could not start", "subscription", string(id))
	}
}

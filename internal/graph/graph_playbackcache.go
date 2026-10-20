package graph

import (
	"context"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Precomputed playback rendition cache Console controls. These are
// operator-only Console operations: explicit generation (§2 — never automatic
// popularity), on-demand budget eviction, and the cache-health snapshot (§"cache
// health visibility"). Dispatch for these fields is Admin-Session gated in
// graph.go's execute(), like the Library Health group; the resolvers live here
// to keep the chokepoint edit localized.
//
// The cache objects are server-derived, disposable, rebuildable, budget-governed
// derivatives — NOT Library Versions (§3) — so they carry no Reveal filter of
// their own; the Admin Session is the gate. The PlaybackCacheController seam is
// wired from app.go with the configured cache root and disk budget, so the graph
// package never holds those policy values itself.

// PlaybackCacheController is the app-side seam the Console cache controls drive.
// Nil on the Handler means the cache is unconfigured: the read
// reports zeroes-and-disabled and the writes refuse rather than silently drop.
type PlaybackCacheController interface {
	// PrecomputeMedia explicitly enqueues precompute generation for one Media's
	// honest ladder, returning how many rendition jobs were
	// enqueued.
	PrecomputeMedia(ctx context.Context, mediaID store.MediaID) (int, error)
	// EvictOverBudget runs an immediate disk-budget eviction pass, returning how
	// many cache objects were evicted.
	EvictOverBudget(ctx context.Context) (int, error)
	// BudgetBytes reports the configured disk budget the eviction governs.
	BudgetBytes() int64
}

// PlaybackCacheInfo is the cache-health snapshot on the wire (ADR-0057
// §"cache health visibility"): per-status cache-object counts, the disk the
// ready objects occupy, and the configured disk budget that governs eviction.
type PlaybackCacheInfo struct {
	Configured  bool  `json:"configured"`
	Ready       int   `json:"ready"`
	Generating  int   `json:"generating"`
	Failed      int   `json:"failed"`
	Evicted     int   `json:"evicted"`
	ReadyBytes  int64 `json:"readyBytes"`
	BudgetBytes int64 `json:"budgetBytes"`
}

// PlaybackCacheOpResult reports the outcome of a cache mutation: how many cache
// objects the operation affected (renditions enqueued, or objects evicted).
type PlaybackCacheOpResult struct {
	Affected int `json:"affected"`
}

// playbackRenditionCache returns the cache-health snapshot for the Console.
// When no controller is wired the cache is unconfigured: the counts still
// reflect any rows present, but Configured is false and BudgetBytes is zero so
// the Console renders the "cache disabled" state honestly.
func (h *Handler) playbackRenditionCache(ctx context.Context) (PlaybackCacheInfo, error) {
	if h.store == nil {
		return PlaybackCacheInfo{}, clientError("store unavailable")
	}
	stats, err := h.store.PlaybackRenditionCacheHealth(ctx)
	if err != nil {
		return PlaybackCacheInfo{}, err
	}
	info := PlaybackCacheInfo{
		Ready:      stats.Ready,
		Generating: stats.Generating,
		Failed:     stats.Failed,
		Evicted:    stats.Evicted,
		ReadyBytes: stats.ReadyBytes,
	}
	if h.playbackCache != nil {
		info.Configured = true
		info.BudgetBytes = h.playbackCache.BudgetBytes()
	}
	return info, nil
}

// precomputePlaybackRenditions is the explicit operator generation trigger.
// It refuses when the cache is unconfigured and validates the
// Media is visible before enqueuing; the count of enqueued rendition jobs is
// returned for operator visibility, and one library.roots activity entry is
// logged (the generation is a Library-scoped operator action).
func (h *Handler) precomputePlaybackRenditions(ctx context.Context, scope auth.Scope, req request) (PlaybackCacheOpResult, error) {
	if h.playbackCache == nil {
		return PlaybackCacheOpResult{}, clientError("playback rendition cache is not configured")
	}
	mediaID := trimmedVariable(req, "mediaID")
	if mediaID == "" {
		return PlaybackCacheOpResult{}, clientError("mediaID is required")
	}
	media, err := h.store.Media(ctx, store.MediaID(mediaID), scope.LibraryFilter())
	if err != nil {
		// A Media the scope cannot see is indistinguishable from an unknown ID
		// (Reveal contract) — never an existence oracle.
		return PlaybackCacheOpResult{}, clientError("unknown media")
	}
	enqueued, err := h.playbackCache.PrecomputeMedia(ctx, media.ID)
	if err != nil {
		return PlaybackCacheOpResult{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityLibraryRoots, "media", string(media.ID), media.LibraryID); err != nil {
		return PlaybackCacheOpResult{}, err
	}
	return PlaybackCacheOpResult{Affected: enqueued}, nil
}

// evictPlaybackRenditionCache runs an immediate disk-budget eviction pass
// (ADR-0057 §4 budget governance). Refuses when the cache is unconfigured;
// returns the count of cache objects evicted.
func (h *Handler) evictPlaybackRenditionCache(ctx context.Context, scope auth.Scope) (PlaybackCacheOpResult, error) {
	if h.playbackCache == nil {
		return PlaybackCacheOpResult{}, clientError("playback rendition cache is not configured")
	}
	evicted, err := h.playbackCache.EvictOverBudget(ctx)
	if err != nil {
		return PlaybackCacheOpResult{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityLibraryRoots, "playbackcache", "evict", ""); err != nil {
		return PlaybackCacheOpResult{}, err
	}
	return PlaybackCacheOpResult{Affected: evicted}, nil
}

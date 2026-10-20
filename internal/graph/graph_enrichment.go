package graph

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Enrichment + Semantic Search resolvers (ADR-0041, master issues #62/#106/#107).
// Two operator-facing concerns share this file:
//
//   - Console Enrichment panel (#107): per-Library index status + ETA, the
//     auto-tag review queue (accept/reject with a diff preview), and the
//     per-Library AI-indexing opt-out. Every field here is Admin-Session gated
//     in graph.go's execute() like the Library Health group, and resolvers
//     read/write through the reveal-filtered store.
//
//   - Semantic search (#62) + explainability (#106): the `searchResult`
//     resolver carries a SearchMode and per-item scores, and the
//     `semanticCapability` resolver reports the 4-state availability the viewer
//     toggle renders.
//
// AI-safety invariants enforced here:
//   - Semantic candidate scoping happens in store.SemanticSearch BEFORE
//     scoring (the SQL JOIN gates which rows are even read). This file never
//     score-then-filters.
//   - Auto-tags reach canonical metadata ONLY through acceptPendingAITag →
//     store.PromotePendingAITag. Reject discards. Plugins write only to
//     pending_ai_tag (store.ProposePendingAITag); they cannot promote.

// SearchMode is the ADR-0041 retrieval discriminant. keyword always
// works (FTS); semantic uses the vector index and is available only when an
// Enrichment Plugin is active and the index is populated for the queried
// Libraries; auto prefers semantic and silently falls back to keyword. recommend
// is the engagement-seeded, content-based retrieval mode: it ignores
// the query string and instead returns the nearest content-neighbours of the
// Profile's own engagement seeds. It is per-Profile opt-in (default off) and,
// like semantic, requires an active Enrichment Plugin with a populated index.
type SearchMode string

const (
	SearchModeKeyword   SearchMode = "keyword"
	SearchModeSemantic  SearchMode = "semantic"
	SearchModeAuto      SearchMode = "auto"
	SearchModeRecommend SearchMode = "recommend"
)

// recommendOptInKey is the per-Profile opt-in flag for the ADR-0053 recommend
// mode, stored in the existing generic per-Profile preference store
// (profile_client_prefs, ADR-0014). Default off: absent or any non-"on" value
// means the Profile has not opted in, and a recommend query yields nothing. No
// new opt-in storage / migration is introduced — recommend reuses the existing
// per-Profile preference seam (ADR-0053 §2.4 per-Profile opt-in).
const recommendOptInKey = "recommend.opt-in"

var errQueryEmbeddingUnavailable = errors.New("query embedding unavailable")

// parseSearchMode reads the optional `mode` variable, defaulting to keyword.
// An unknown value is a client error rather than a silent downgrade, so a
// typo'd mode is visible instead of masquerading as keyword.
func parseSearchMode(req request) (SearchMode, error) {
	raw := strings.TrimSpace(stringVar(req, "mode"))
	if raw == "" {
		return SearchModeKeyword, nil
	}
	switch SearchMode(raw) {
	case SearchModeKeyword, SearchModeSemantic, SearchModeAuto, SearchModeRecommend:
		return SearchMode(raw), nil
	default:
		return "", clientError("mode must be one of keyword, semantic, auto, recommend")
	}
}

// SearchResultItem is one explainable result (ADR-0041 #106): the canonical
// SourceItem fields plus how it was found (Mode) and, for semantic hits, the
// cosine Score in [0,1]. Keyword hits carry mode=keyword and score=0.
type SearchResultItem struct {
	SourceItem
	Mode  SearchMode `json:"mode"`
	Score float32    `json:"score"`
}

// SearchResultResponse is the explainable search envelope (ADR-0041 #106). Mode
// is the mode actually used (auto resolves to keyword or semantic). Items is the
// reveal-filtered result list. RestrictedResultsPresent is the leak-safe boolean
// — true when the caller has hidden restricted Libraries — so a client can
// display "some results may be hidden" WITHOUT any title, id, or count of
// hidden content.
type SearchResultResponse struct {
	Mode                     SearchMode         `json:"mode"`
	Items                    []SearchResultItem `json:"items"`
	RestrictedResultsPresent bool               `json:"restrictedResultsPresent"`
}

// searchResult is the explainable, mode-aware search resolver (#62/#106). It is
// a sibling of the bare `search` array field (kept unchanged for the existing
// reveal-surface contract): the viewer's mode toggle calls this one.
//
// keyword → store.SearchMedia (FTS). semantic → store.SemanticSearch over the
// Enrichment Plugin's query vector. auto → semantic when available, else
// keyword. Explicit semantic returns an empty semantic result when the current
// Library/Reveal scope has no populated index, so keyword results never escape
// under a semantic label.
func (h *Handler) searchResult(ctx context.Context, scope auth.Scope, req request) (SearchResultResponse, error) {
	if h.store == nil {
		return SearchResultResponse{Mode: SearchModeKeyword, Items: []SearchResultItem{}}, nil
	}
	mode, err := parseSearchMode(req)
	if err != nil {
		return SearchResultResponse{}, err
	}
	text := strings.TrimSpace(stringVar(req, "query"))
	filter := scope.LibraryFilter()

	restricted, err := h.store.HasHiddenRestrictedLibraries(ctx, filter)
	if err != nil {
		return SearchResultResponse{}, err
	}

	// recommend: engagement-seeded, content-based retrieval. It does
	// not consume the query string. Per-Profile opt-in (default off) and, like
	// semantic, requires an active populated Enrichment index. When opted-out,
	// unavailable, or empty (no engagement / no embeddings yet), it returns an
	// empty recommend result rather than degrading to keyword — recommend is its
	// own honest mode (ADR-0053 §3, no blending into keyword/semantic).
	if mode == SearchModeRecommend {
		var items []SearchResultItem
		if h.semanticReady() && h.recommendOptedIn(ctx, scope.ProfileID) {
			items, err = h.recommendItems(ctx, scope.ProfileID, filter)
			if err != nil {
				return SearchResultResponse{}, err
			}
		}
		if items == nil {
			items = []SearchResultItem{}
		}
		return SearchResultResponse{
			Mode:                     SearchModeRecommend,
			Items:                    items,
			RestrictedResultsPresent: restricted,
		}, nil
	}

	if mode == SearchModeSemantic {
		if text == "" {
			return SearchResultResponse{
				Mode:                     SearchModeSemantic,
				Items:                    []SearchResultItem{},
				RestrictedResultsPresent: restricted,
			}, nil
		}
		available, err := h.semanticAvailable(ctx, filter)
		if err != nil {
			return SearchResultResponse{}, err
		}
		if !available {
			return SearchResultResponse{
				Mode:                     SearchModeSemantic,
				Items:                    []SearchResultItem{},
				RestrictedResultsPresent: restricted,
			}, nil
		}
		items, err := h.semanticItems(ctx, scope.ProfileID, filter, text)
		if err != nil {
			if !errors.Is(err, errQueryEmbeddingUnavailable) {
				return SearchResultResponse{}, err
			}
		} else {
			return SearchResultResponse{
				Mode:                     SearchModeSemantic,
				Items:                    items,
				RestrictedResultsPresent: restricted,
			}, nil
		}
	}

	if mode == SearchModeAuto && text != "" {
		available, err := h.semanticAvailable(ctx, filter)
		if err != nil {
			return SearchResultResponse{}, err
		}
		if available {
			items, semErr := h.semanticItems(ctx, scope.ProfileID, filter, text)
			if semErr != nil {
				// auto degrades to keyword on a semantic error.
			} else {
				return SearchResultResponse{
					Mode:                     SearchModeSemantic,
					Items:                    items,
					RestrictedResultsPresent: restricted,
				}, nil
			}
		}
	}

	// keyword path (also the fallback for auto).
	results, err := h.store.SearchMedia(ctx, filter, text, 50)
	if err != nil {
		return SearchResultResponse{}, err
	}
	entities, err := h.store.SearchEntities(ctx, filter, text, 50)
	if err != nil {
		return SearchResultResponse{}, err
	}
	results = append(results, entities...)
	sort.SliceStable(results, func(i, j int) bool {
		left, right := store.SearchRank(text, results[i].Title), store.SearchRank(text, results[j].Title)
		if left < 0 {
			left = 1 << 30
		}
		if right < 0 {
			right = 1 << 30
		}
		if left != right {
			return left < right
		}
		if results[i].Kind != results[j].Kind {
			return results[i].Kind < results[j].Kind
		}
		return results[i].ID < results[j].ID
	})
	if len(results) > 50 {
		results = results[:50]
	}
	// Keyword search is a deliberately-surfaced surface: muted items appear on
	// purpose and carry the Muted badge.
	if err := h.store.AnnotateMuted(ctx, scope.ProfileID, filter, results); err != nil {
		return SearchResultResponse{}, err
	}
	items := make([]SearchResultItem, len(results))
	for i, item := range results {
		items[i] = SearchResultItem{SourceItem: h.sourceItem(item, scope.Revealed), Mode: SearchModeKeyword}
	}
	return SearchResultResponse{
		Mode:                     SearchModeKeyword,
		Items:                    items,
		RestrictedResultsPresent: restricted,
	}, nil
}

// semanticReady reports whether the semantic path can serve a query: an
// Enrichment Plugin query embedder is wired AND reports its model loaded.
func (h *Handler) semanticReady() bool {
	return h.queryEmbedder != nil && h.queryEmbedder.Available()
}

// semanticAvailable gates semantic retrieval on both an active query embedder
// and at least one indexed item visible under the caller's Library/Reveal scope.
func (h *Handler) semanticAvailable(ctx context.Context, filter store.LibraryFilter) (bool, error) {
	if !h.semanticReady() || h.queryEmbedder.Kind() == "" {
		return false, nil
	}
	statuses, err := h.store.LibraryIndexStatuses(ctx, filter, h.queryEmbedder.Kind())
	if err != nil {
		return false, err
	}
	for _, st := range statuses {
		if st.AIIndexing && st.Indexed > 0 {
			return true, nil
		}
	}
	return false, nil
}

// semanticItems runs the reveal-scoped vector search and lowers the results.
// The store's SemanticSearch establishes the visible candidate set in SQL
// before scoring, so restricted Media never enters the ranking.
// libraryRestrictedMemo returns a lookup that caches the per-Library
// restricted flag for one resolver call: result sets are capped at 50 hits
// that typically share a handful of Libraries, so the uncached per-hit
// round-trip is pure repeat work.
func (h *Handler) libraryRestrictedMemo() func(ctx context.Context, id store.LibraryID) (bool, error) {
	cache := map[store.LibraryID]bool{}
	return func(ctx context.Context, id store.LibraryID) (bool, error) {
		if v, ok := cache[id]; ok {
			return v, nil
		}
		v, err := h.store.LibraryRestricted(ctx, id)
		if err != nil {
			return false, err
		}
		cache[id] = v
		return v, nil
	}
}

func (h *Handler) semanticItems(ctx context.Context, profileID store.ProfileID, filter store.LibraryFilter, text string) ([]SearchResultItem, error) {
	vector, err := h.queryEmbedder.EmbedQuery(ctx, text)
	if err != nil {
		return nil, errQueryEmbeddingUnavailable
	}
	results, err := h.store.SemanticSearch(ctx, filter, h.queryEmbedder.Kind(), vector, 50)
	if err != nil {
		return nil, err
	}
	// Build the store SourceItem for every surviving hit first, carrying each
	// hit's score alongside, so the Muted badge can be annotated over the whole
	// slice in one reach query rather than per hit.
	sources := make([]store.SourceItem, 0, len(results))
	scores := make([]float32, 0, len(results))
	libraryRestricted := h.libraryRestrictedMemo()
	for _, r := range results {
		// Re-read each hit through the reveal-filtered Media read so the wire
		// item is a full SourceItem and any item that slipped past (it cannot,
		// but defence in depth) is dropped by the filtered read.
		media, err := h.store.Media(ctx, r.MediaID, filter)
		if err != nil {
			continue
		}
		source := store.SourceItem{
			ID:      media.ID,
			Kind:    "media",
			Title:   media.Title,
			Summary: media.Summary,
		}
		// Library-level restricted flag for the per-poster tag.
		// Only consulted on a Revealed filter — an unrevealed filter never
		// reaches a restricted-Library Media (the reveal-filtered Media read
		// above drops it), and h.sourceItem suppresses the wire field anyway.
		if filter.Revealed {
			restricted, err := libraryRestricted(ctx, media.LibraryID)
			if err != nil {
				return nil, err
			}
			source.Restricted = restricted
		}
		sources = append(sources, source)
		scores = append(scores, r.Score)
	}
	// Semantic search is a deliberately-surfaced surface (§2.2 "searchable on
	// purpose", §2.5 "search (FTS / semantic)"): muted hits carry the badge (§2.7).
	if err := h.store.AnnotateMuted(ctx, profileID, filter, sources); err != nil {
		return nil, err
	}
	items := make([]SearchResultItem, len(sources))
	for i := range sources {
		items[i] = SearchResultItem{
			SourceItem: h.sourceItem(sources[i], filter.Revealed),
			Mode:       SearchModeSemantic,
			Score:      scores[i],
		}
	}
	return items, nil
}

// recommendOptedIn reports whether the Profile has opted in to the ADR-0053
// recommend mode. The flag lives in the existing generic per-Profile preference
// store under recommendOptInKey; default off — an absent flag, an empty Profile,
// or any value other than "on" means not opted in. A read error fails closed
// (treated as opted-out) so a transient store error never silently surfaces a
// Profile's engagement-derived recommendations against their opt-in.
func (h *Handler) recommendOptedIn(ctx context.Context, profileID store.ProfileID) bool {
	if profileID == "" {
		return false
	}
	prefs, err := h.store.ClientPrefs(ctx, profileID)
	if err != nil {
		return false
	}
	for _, p := range prefs {
		if p.Key == recommendOptInKey {
			return p.Value == "on"
		}
	}
	return false
}

// recommendItems runs the reveal-scoped, engagement-seeded recommendation read
// and lowers the results. store.RecommendFromEngagement establishes the visible
// candidate set in SQL before scoring (identical reveal contract to semantic)
// and already excludes the Profile's seeds and finished items, so this resolver
// only re-reads each hit through the reveal-filtered Media read for defence in
// depth and to build the full SourceItem wire shape.
func (h *Handler) recommendItems(ctx context.Context, profileID store.ProfileID, filter store.LibraryFilter) ([]SearchResultItem, error) {
	results, err := h.store.RecommendFromEngagement(ctx, profileID, filter, h.queryEmbedder.Kind(), 50)
	if err != nil {
		return nil, err
	}
	items := make([]SearchResultItem, 0, len(results))
	libraryRestricted := h.libraryRestrictedMemo()
	for _, r := range results {
		media, err := h.store.Media(ctx, r.MediaID, filter)
		if err != nil {
			continue
		}
		source := store.SourceItem{
			ID:      media.ID,
			Kind:    "media",
			Title:   media.Title,
			Summary: media.Summary,
		}
		// Library-level restricted flag — Revealed-only, see
		// semanticItems for the rationale.
		if filter.Revealed {
			restricted, err := libraryRestricted(ctx, media.LibraryID)
			if err != nil {
				return nil, err
			}
			source.Restricted = restricted
		}
		items = append(items, SearchResultItem{
			SourceItem: h.sourceItem(source, filter.Revealed),
			Mode:       SearchModeRecommend,
			Score:      r.Score,
		})
	}
	return items, nil
}

// SemanticCapabilityInfo is one Library's semantic-search availability (#62 +
// #106 4-state UX). PluginActive is the global Enrichment-Plugin presence;
// State is the per-Library index state (off/empty/partial/complete). The viewer
// folds these into: plugin-absent (PluginActive false), index-0% (empty),
// partial, complete.
type SemanticCapabilityInfo struct {
	LibraryID    string `json:"libraryID"`
	Title        string `json:"title"`
	PluginActive bool   `json:"pluginActive"`
	State        string `json:"state"`
	Indexed      int    `json:"indexed"`
	Total        int    `json:"total"`
}

// semanticCapability reports per-Library semantic availability so a viewer can
// pick the right of the four states without ever attempting an unavailable
// query. Reveal-filtered: only visible Libraries appear, so a hidden restricted
// Library's index state is never an oracle. This is NOT operator-gated — every
// session needs it to render its own search toggle.
func (h *Handler) semanticCapability(ctx context.Context, scope auth.Scope) ([]SemanticCapabilityInfo, error) {
	if h.store == nil {
		return []SemanticCapabilityInfo{}, nil
	}
	pluginActive := h.queryEmbedder != nil
	kind := store.EmbeddingKind("")
	if pluginActive {
		kind = h.queryEmbedder.Kind()
	}
	// With no plugin there is no kind to scope by; report each visible Library
	// as plugin-absent without touching the embedding table.
	if !pluginActive || kind == "" {
		libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
		if err != nil {
			return nil, err
		}
		out := make([]SemanticCapabilityInfo, len(libraries))
		for i, library := range libraries {
			out[i] = SemanticCapabilityInfo{
				LibraryID:    string(library.ID),
				Title:        library.Title,
				PluginActive: false,
				State:        store.IndexStateEmpty,
			}
		}
		return out, nil
	}
	statuses, err := h.store.LibraryIndexStatuses(ctx, scope.LibraryFilter(), kind)
	if err != nil {
		return nil, err
	}
	out := make([]SemanticCapabilityInfo, len(statuses))
	for i, st := range statuses {
		out[i] = SemanticCapabilityInfo{
			LibraryID:    string(st.LibraryID),
			Title:        st.Title,
			PluginActive: true,
			State:        st.State,
			Indexed:      st.Indexed,
			Total:        st.Total,
		}
	}
	return out, nil
}

// --- Console Enrichment panel (#107) ----------------------------------------

// LibraryIndexStatusInfo is one Library's indexing progress for the Console
// Enrichment panel: indexed vs total Media for the active embedding kind, the
// per-Library opt-out flag, and the derived state label. ETA is the operator
// signal the panel renders from indexed/total over time; Core reports the live
// counts and leaves the rate estimate to the client (no server-side timer).
type LibraryIndexStatusInfo struct {
	LibraryID  string `json:"libraryID"`
	Title      string `json:"title"`
	Indexed    int    `json:"indexed"`
	Total      int    `json:"total"`
	Failures   int    `json:"failures"`
	AIIndexing bool   `json:"aiIndexing"`
	State      string `json:"state"`
}

// libraryIndexStatus returns per-Library index progress for the Enrichment
// panel. When no Enrichment Plugin is active there is no embedding kind, so the
// panel still lists every visible Library with its opt-out flag and a zero
// index (state off when opted out, else empty). Operator-gated in graph.go.
func (h *Handler) libraryIndexStatus(ctx context.Context, scope auth.Scope) ([]LibraryIndexStatusInfo, error) {
	if h.store == nil {
		return []LibraryIndexStatusInfo{}, nil
	}
	kind := store.EmbeddingKind("")
	if h.queryEmbedder != nil {
		kind = h.queryEmbedder.Kind()
	}
	if kind == "" {
		// No active embedding kind: report opt-out + totals without an indexed
		// count (always 0). Libraries() is reveal-filtered.
		libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
		if err != nil {
			return nil, err
		}
		out := make([]LibraryIndexStatusInfo, len(libraries))
		for i, library := range libraries {
			state := store.IndexStateEmpty
			if !library.AIIndexing {
				state = store.IndexStateOff
			}
			out[i] = LibraryIndexStatusInfo{
				LibraryID:  string(library.ID),
				Title:      library.Title,
				AIIndexing: library.AIIndexing,
				State:      state,
			}
		}
		return out, nil
	}
	statuses, err := h.store.LibraryIndexStatuses(ctx, scope.LibraryFilter(), kind)
	if err != nil {
		return nil, err
	}
	out := make([]LibraryIndexStatusInfo, len(statuses))
	for i, st := range statuses {
		out[i] = LibraryIndexStatusInfo{
			LibraryID:  string(st.LibraryID),
			Title:      st.Title,
			Indexed:    st.Indexed,
			Total:      st.Total,
			Failures:   st.Failures,
			AIIndexing: st.AIIndexing,
			State:      st.State,
		}
	}
	return out, nil
}

// PendingAITagInfo is one auto-tag proposal in the Console review queue (#107).
// MediaTitle gives the diff-preview its left side ("Media X"); TagKind/Value
// are the proposed addition ("+ genre: noir"). ProposedBy names the plugin so
// the operator knows the source. No canonical value is included — the proposal
// is staged, not applied, until accept.
type PendingAITagInfo struct {
	ID         string `json:"id"`
	MediaID    string `json:"mediaID"`
	MediaTitle string `json:"mediaTitle"`
	TagKind    string `json:"tagKind"`
	Value      string `json:"value"`
	ProposedBy string `json:"proposedBy"`
	ProposedAt string `json:"proposedAt"`
}

// pendingAITagQueue returns the reveal-scoped operator review queue (#107).
// Operator-gated in graph.go.
func (h *Handler) pendingAITagQueue(ctx context.Context, scope auth.Scope, req request) ([]PendingAITagInfo, error) {
	if h.store == nil {
		return []PendingAITagInfo{}, nil
	}
	items, err := h.store.PendingAITagQueue(ctx, scope.LibraryFilter(), boundedIntVar(req, "limit", 100, 200))
	if err != nil {
		return nil, err
	}
	out := make([]PendingAITagInfo, len(items))
	for i, item := range items {
		out[i] = PendingAITagInfo{
			ID:         item.Tag.ID,
			MediaID:    string(item.Tag.MediaID),
			MediaTitle: item.MediaTitle,
			TagKind:    item.Tag.TagKind,
			Value:      item.Tag.Value,
			ProposedBy: item.Tag.ProposedBy,
			ProposedAt: item.Tag.ProposedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
	}
	return out, nil
}

// acceptPendingAITag promotes one staged auto-tag to canonical metadata — the
// ADR-0041 §2.5 approval gate. It routes through store.PromotePendingAITag,
// which is the ONLY promotion path (reveal-gated, AI-provenance-stamped, single
// transaction). Operator-gated in graph.go. Returns the updated review queue so
// the panel re-renders without a second round-trip.
func (h *Handler) acceptPendingAITag(ctx context.Context, scope auth.Scope, req request) ([]PendingAITagInfo, error) {
	tagID := trimmedVariable(req, "tagID")
	if tagID == "" {
		return nil, clientError("tagID is required")
	}
	if err := h.store.PromotePendingAITag(ctx, tagID, scope.LibraryFilter()); err != nil {
		return nil, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityAITagAccept, "media", tagID, ""); err != nil {
		return nil, err
	}
	return h.pendingAITagQueue(ctx, scope, req)
}

// rejectPendingAITag discards one staged auto-tag — it sets status='rejected'
// and the value never touches canonical metadata. Operator-gated. Returns the
// updated review queue.
func (h *Handler) rejectPendingAITag(ctx context.Context, scope auth.Scope, req request) ([]PendingAITagInfo, error) {
	tagID := trimmedVariable(req, "tagID")
	if tagID == "" {
		return nil, clientError("tagID is required")
	}
	if err := h.store.SetPendingAITagStatus(ctx, tagID, store.PendingAITagRejected); err != nil {
		return nil, err
	}
	return h.pendingAITagQueue(ctx, scope, req)
}

// setLibraryAIIndexing flips one Library's AI-indexing opt-out (#107). The
// Enrichment Plugin reads this through Core's scoped status; off means the
// Library is excluded from indexing and semantic search is never available for
// it. Operator-gated. Returns the refreshed index-status list.
func (h *Handler) setLibraryAIIndexing(ctx context.Context, scope auth.Scope, req request) ([]LibraryIndexStatusInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return nil, clientError("libraryID is required")
	}
	if _, ok := req.Variables["enabled"]; !ok {
		return nil, clientError("enabled is required")
	}
	enabled := boolVar(req, "enabled")
	// Reject a Library the caller cannot see before mutating it, so the opt-out
	// is never an existence oracle for hidden content.
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, clientError("unknown library")
	}
	if err := h.store.SetLibraryAIIndexing(ctx, store.LibraryID(libraryID), enabled); err != nil {
		return nil, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityLibraryRoots, "library", libraryID, store.LibraryID(libraryID)); err != nil {
		return nil, err
	}
	return h.libraryIndexStatus(ctx, scope)
}

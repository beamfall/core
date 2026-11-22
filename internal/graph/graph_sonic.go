package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Sonic similarity (CORE-3073, CORE-3074; OpenSpec core-1974-sonic-similarity).
// A Library opts in to local sonic analysis with a per-Library toggle, default
// OFF and separate from AI indexing (owner decision D3). The analysis job writes
// feature vectors under store.SonicEmbeddingKind; similarMedia reads the Media
// nearest to one item. Nothing here names a domain: the row is "Media that
// sound alike", whatever plugin owns the Library.

const (
	similarDefaultLimit = 20
	similarMaxLimit     = 50
)

// SonicAnalysisInfo is one Library's sonic-analysis toggle. Available reports
// whether this server has an analyser wired; with none, turning the toggle on
// is refused because nothing would ever run.
type SonicAnalysisInfo struct {
	LibraryID string `json:"libraryID"`
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
}

func (h *Handler) sonicAvailable() bool {
	return h.sonicAnalysisAvailable != nil && h.sonicAnalysisAvailable()
}

// sonicAnalysisEnabledRead returns the toggle for every Library visible to the
// scope. Admin-Session gated by the caller in execute(); visibility-filtered so
// a hidden Library never appears.
func (h *Handler) sonicAnalysisEnabledRead(ctx context.Context, scope auth.Scope) ([]SonicAnalysisInfo, error) {
	libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	available := h.sonicAvailable()
	infos := make([]SonicAnalysisInfo, 0, len(libraries))
	for _, library := range libraries {
		enabled, err := h.store.SonicAnalysisEnabled(ctx, library.ID)
		if err != nil {
			return nil, err
		}
		infos = append(infos, SonicAnalysisInfo{LibraryID: string(library.ID), Enabled: enabled, Available: available})
	}
	return infos, nil
}

// setSonicAnalysisEnabled flips one Library's toggle. Admin-Session gated by
// the caller; a Library the caller cannot see is unknown, so the mutation is
// never an existence oracle. Turning it on arms the idle-only analysis sweep.
// Turning it off is always allowed and keeps vectors already written.
func (h *Handler) setSonicAnalysisEnabled(ctx context.Context, scope auth.Scope, req request) (SonicAnalysisInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return SonicAnalysisInfo{}, clientError("libraryID is required")
	}
	if _, ok := req.Variables["enabled"]; !ok {
		return SonicAnalysisInfo{}, clientError("enabled is required")
	}
	enabled := boolVar(req, "enabled")
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return SonicAnalysisInfo{}, err
	}
	if !visible {
		return SonicAnalysisInfo{}, clientError("unknown library")
	}
	available := h.sonicAvailable()
	if enabled && !available {
		return SonicAnalysisInfo{}, clientError("sonic analysis is not available on this server")
	}
	if err := h.store.SetSonicAnalysisEnabled(ctx, store.LibraryID(libraryID), enabled, time.Now().UTC()); err != nil {
		return SonicAnalysisInfo{}, err
	}
	if enabled && h.sonicAnalysisSweep != nil {
		h.sonicAnalysisSweep(ctx)
	}
	return SonicAnalysisInfo{LibraryID: libraryID, Enabled: enabled, Available: available}, nil
}

// similarMedia answers similarMedia(mediaID, limit): the visible Media whose
// sonic vectors are nearest the item's, best first. An item without a vector,
// or a server that never analysed anything, returns an empty list, which
// clients read as "hide the row". Passive-rail Mute suppression applies, as on
// any discovery row.
func (h *Handler) similarMedia(ctx context.Context, scope auth.Scope, req request) ([]SourceItem, error) {
	mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
	if mediaID == "" {
		return nil, clientError("mediaID is required")
	}
	filter := scope.LibraryFilter()
	if _, err := h.store.Media(ctx, mediaID, filter); err != nil {
		return nil, clientError("unknown media")
	}
	limit := boundedIntVar(req, "limit", similarDefaultLimit, similarMaxLimit)
	results, err := h.store.SimilarMedia(ctx, filter, store.SonicEmbeddingKind, []store.MediaID{mediaID}, limit)
	if err != nil {
		return nil, err
	}
	items, err := h.similarSourceItems(ctx, filter, results)
	if err != nil {
		return nil, err
	}
	if items, err = h.store.SuppressMuted(ctx, scope.ProfileID, filter, items); err != nil {
		return nil, err
	}
	// Survivors may be broad-muted but protected by a positive signal (§2.7).
	if err := h.store.AnnotateMuted(ctx, scope.ProfileID, filter, items); err != nil {
		return nil, err
	}
	if err := h.annotateCards(ctx, scope, items); err != nil {
		return nil, err
	}
	resp := make([]SourceItem, len(items))
	for i, item := range items {
		resp[i] = h.sourceItem(item, scope.Revealed)
	}
	return resp, nil
}

// similarSourceItems resolves similarity hits to SourceItems through the
// caller's filter, in hit order, dropping any the filter does not admit.
func (h *Handler) similarSourceItems(ctx context.Context, filter store.LibraryFilter, results []store.SemanticResult) ([]store.SourceItem, error) {
	ids := make([]store.MediaID, len(results))
	for i, r := range results {
		ids[i] = r.MediaID
	}
	media, err := h.store.MediaByIDs(ctx, ids, filter)
	if err != nil {
		return nil, err
	}
	libraryRestricted := h.libraryRestrictedMemo()
	items := make([]store.SourceItem, 0, len(ids))
	for _, id := range ids {
		m, ok := media[id]
		if !ok {
			continue
		}
		item := store.SourceItem{
			ID:         m.ID,
			Kind:       "media",
			Title:      m.Title,
			LibraryID:  m.LibraryID,
			Summary:    m.Summary,
			MediaClass: m.Class,
			Year:       m.Year,
		}
		if filter.Revealed {
			restricted, err := libraryRestricted(ctx, m.LibraryID)
			if err != nil {
				return nil, err
			}
			item.Restricted = restricted
		}
		items = append(items, item)
	}
	return items, nil
}

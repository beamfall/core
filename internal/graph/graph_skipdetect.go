package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Skip-segment detection per-Library toggle (ENR-7, ADR-0048 Decision 3/6): the
// operator exposure for the existing store.SkipDetectionEnabled /
// store.SetSkipDetectionEnabled toggle (internal/store/skipdetect.go). Both
// store methods already existed with zero non-test callers — the built
// detector (internal/skipdetect) is gated on this default-OFF per-Library flag
// but had no GraphQL surface for an operator to ever flip it on. This file
// adds ONLY the exposure (a query + a mutation); no new detection logic.
//
// Operator-scoped like the other Console per-Library toggles
// (setLibraryAIIndexing, setScrobbleEligibility): Admin-Session gated in
// execute(), visible Libraries only (scope.LibraryFilter()) so a hidden
// Library never leaks through the toggle list or becomes a mutation oracle.

// SkipDetectionInfo is one Library's resolved skip-detection toggle state.
type SkipDetectionInfo struct {
	LibraryID string `json:"libraryID"`
	Enabled   bool   `json:"enabled"`
}

// skipDetectionEnabledRead returns the skip-detection toggle for every Library
// visible to the scope. Admin-Session gated by the caller in execute().
// Visibility-filtered through scope.LibraryFilter() so the toggle list can
// never leak a hidden/Sealed Library's existence.
func (h *Handler) skipDetectionEnabledRead(ctx context.Context, scope auth.Scope) ([]SkipDetectionInfo, error) {
	libraries, err := h.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	infos := make([]SkipDetectionInfo, 0, len(libraries))
	for _, library := range libraries {
		enabled, err := h.store.SkipDetectionEnabled(ctx, library.ID)
		if err != nil {
			return nil, err
		}
		infos = append(infos, SkipDetectionInfo{
			LibraryID: string(library.ID),
			Enabled:   enabled,
		})
	}
	return infos, nil
}

// setSkipDetectionEnabled flips one Library's skip-detection toggle (ADR-0048
// Decision 6 default-off opt-in). Operator surface, Admin-Session gated by the
// caller. Rejects a Library the caller cannot see before mutating it, so the
// toggle can never be used as an existence oracle for a hidden Library.
func (h *Handler) setSkipDetectionEnabled(ctx context.Context, scope auth.Scope, req request) (SkipDetectionInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return SkipDetectionInfo{}, clientError("libraryID is required")
	}
	if _, ok := req.Variables["enabled"]; !ok {
		return SkipDetectionInfo{}, clientError("enabled is required")
	}
	enabled := boolVar(req, "enabled")
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return SkipDetectionInfo{}, err
	}
	if !visible {
		return SkipDetectionInfo{}, clientError("unknown library")
	}
	if err := h.store.SetSkipDetectionEnabled(ctx, store.LibraryID(libraryID), enabled, time.Now().UTC()); err != nil {
		return SkipDetectionInfo{}, err
	}
	return SkipDetectionInfo{LibraryID: libraryID, Enabled: enabled}, nil
}

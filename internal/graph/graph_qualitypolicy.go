package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// QualityPolicyInfo is the Console wire shape for a Library's generic
// resolution-preference policy.
type QualityPolicyInfo struct {
	LibraryID string   `json:"libraryID"`
	Order     []string `json:"order"`
	Cutoff    string   `json:"cutoff"`
}

func (h *Handler) qualityPolicy(ctx context.Context, scope auth.Scope, req request) (QualityPolicyInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return QualityPolicyInfo{}, clientError("libraryID is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return QualityPolicyInfo{}, err
	}
	if !visible {
		return QualityPolicyInfo{}, clientError("unknown library")
	}
	policy, err := h.store.QualityPolicyForLibrary(ctx, store.LibraryID(libraryID))
	if err != nil {
		return QualityPolicyInfo{}, err
	}
	return QualityPolicyInfo{
		LibraryID: libraryID,
		Order:     policy.Order,
		Cutoff:    policy.Cutoff,
	}, nil
}

func (h *Handler) setQualityPolicy(ctx context.Context, scope auth.Scope, req request) (QualityPolicyInfo, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return QualityPolicyInfo{}, clientError("libraryID is required")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return QualityPolicyInfo{}, err
	}
	if !visible {
		return QualityPolicyInfo{}, clientError("unknown library")
	}
	v := newVars(req)
	order, ok := v.StringList("order", "quality tiers")
	cutoff, hasCutoff := v.String("cutoff")
	if err := v.Err(); err != nil {
		return QualityPolicyInfo{}, err
	}
	if !ok {
		return QualityPolicyInfo{}, clientError("order is required")
	}
	if !hasCutoff {
		return QualityPolicyInfo{}, clientError("cutoff is required")
	}
	policy := store.QualityPolicy{Order: order, Cutoff: cutoff}
	if err := h.store.SetQualityPolicyForLibrary(ctx, store.LibraryID(libraryID), policy, time.Now().UTC()); err != nil {
		return QualityPolicyInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "quality-policy", libraryID, store.LibraryID(libraryID)); err != nil {
		return QualityPolicyInfo{}, err
	}
	return h.qualityPolicy(ctx, scope, req)
}

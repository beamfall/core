package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Retention rules: Console-authored rules that drive the
// leaving-soon Smart Collection → scheduled delete-to-Trash pipeline.
// All rule mutations are Admin-Session-gated Console operations.
// setRetentionCandidateHold is the manager-tier operator hold on one
// candidate; viewer rescue is separate — the per-Profile Favorite-class keep
// bit written through setFavorite (store.SetMediaKeep), which needs no
// manager role and which the sweep honors for any Profile.

// RetentionRuleInfo is the wire shape for one retention rule.
type RetentionRuleInfo struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	SmartCollectionID   string `json:"smartCollectionID"`
	SourceExpr          string `json:"sourceExpr"`
	TargetLibraryID     string `json:"targetLibraryID"`
	GraceDays           int    `json:"graceDays"`
	RevealedAtAuthoring bool   `json:"revealedAtAuthoring"`
	Enabled             bool   `json:"enabled"`
	CreatedAt           string `json:"createdAt,omitempty"`
	UpdatedAt           string `json:"updatedAt,omitempty"`
}

func retentionRuleInfo(r store.RetentionRule) RetentionRuleInfo {
	return RetentionRuleInfo{
		ID:                  r.ID,
		Name:                r.Name,
		SmartCollectionID:   string(r.SmartCollectionID),
		SourceExpr:          r.SourceExpr,
		TargetLibraryID:     string(r.TargetLibraryID),
		GraceDays:           r.GraceDays,
		RevealedAtAuthoring: r.RevealedAtAuthoring,
		Enabled:             r.Enabled,
		CreatedAt:           formatOptionalNonPtrTime(r.CreatedAt),
		UpdatedAt:           formatOptionalNonPtrTime(r.UpdatedAt),
	}
}

// formatOptionalNonPtrTime returns the RFC3339 string for a non-zero time.Time.
func formatOptionalNonPtrTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// retentionRules returns all retention rules. Admin-Session-gated.
func (h *Handler) retentionRules(ctx context.Context, _ auth.Scope, _ request) ([]RetentionRuleInfo, error) {
	rules, err := h.store.RetentionRules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RetentionRuleInfo, len(rules))
	for i, r := range rules {
		out[i] = retentionRuleInfo(r)
	}
	return out, nil
}

// createRetentionRule builds a rule and mints its leaving-soon Smart Collection.
// Admin-Session-gated. Autonomous retention is disabled when Trash is off
// (invariant #6); the store's validateRetentionRule enforces this.
func (h *Handler) createRetentionRule(ctx context.Context, _ auth.Scope, req request) (RetentionRuleInfo, error) {
	v := newVars(req)
	name, _ := v.String("name")
	sourceExpr, _ := v.String("sourceExpr")
	targetLibraryID, _ := v.String("targetLibraryID")
	graceDays, hasGrace := v.Number("graceDays")
	revealedAtAuthoring, _ := v.Bool("revealedAtAuthoring")
	enabled, _ := v.Bool("enabled")
	if err := v.Err(); err != nil {
		return RetentionRuleInfo{}, err
	}
	if name == "" {
		return RetentionRuleInfo{}, clientError("name is required")
	}
	if sourceExpr == "" {
		return RetentionRuleInfo{}, clientError("sourceExpr is required")
	}
	if targetLibraryID == "" {
		return RetentionRuleInfo{}, clientError("targetLibraryID is required")
	}
	grace := 7
	if hasGrace {
		grace = int(graceDays)
	}
	rule, err := h.store.CreateRetentionRule(ctx, store.RetentionRule{
		Name:                name,
		SourceExpr:          sourceExpr,
		TargetLibraryID:     store.LibraryID(targetLibraryID),
		GraceDays:           grace,
		RevealedAtAuthoring: revealedAtAuthoring,
		Enabled:             enabled,
	}, time.Now().UTC())
	if err != nil {
		return RetentionRuleInfo{}, err
	}
	return retentionRuleInfo(rule), nil
}

// updateRetentionRule applies a partial patch to an existing rule.
// Admin-Session-gated. Absent fields leave the stored value unchanged.
func (h *Handler) updateRetentionRule(ctx context.Context, _ auth.Scope, req request) (RetentionRuleInfo, error) {
	v := newVars(req)
	id, _ := v.String("id")
	if err := v.Err(); err != nil {
		return RetentionRuleInfo{}, err
	}
	if id == "" {
		return RetentionRuleInfo{}, clientError("id is required")
	}
	patch := store.RetentionRulePatch{}
	if name, ok := v.String("name"); ok {
		patch.Name = &name
	}
	if expr, ok := v.String("sourceExpr"); ok {
		patch.SourceExpr = &expr
	}
	if lid, ok := v.String("targetLibraryID"); ok {
		libID := store.LibraryID(lid)
		patch.TargetLibraryID = &libID
	}
	if n, ok := v.Number("graceDays"); ok {
		days := int(n)
		patch.GraceDays = &days
	}
	if r, ok := v.Bool("revealedAtAuthoring"); ok {
		patch.RevealedAtAuthoring = &r
	}
	if en, ok := v.Bool("enabled"); ok {
		patch.Enabled = &en
	}
	rule, err := h.store.UpdateRetentionRule(ctx, id, patch, time.Now().UTC())
	if err != nil {
		return RetentionRuleInfo{}, err
	}
	return retentionRuleInfo(rule), nil
}

// deleteRetentionRule removes a rule and its leaving-soon Smart Collection.
// Admin-Session-gated.
func (h *Handler) deleteRetentionRule(ctx context.Context, scope auth.Scope, req request) (RetentionRuleInfo, error) {
	id := trimmedVariable(req, "id")
	if id == "" {
		return RetentionRuleInfo{}, clientError("id is required")
	}
	rule, err := h.store.DeleteRetentionRule(ctx, id, time.Now().UTC())
	if err != nil {
		return RetentionRuleInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityRetentionRuleDelete, "retention_rule", rule.ID, rule.TargetLibraryID); err != nil {
		return RetentionRuleInfo{}, err
	}
	return retentionRuleInfo(rule), nil
}

// setRetentionCandidateHold sets or clears the manager-tier operator hold on
// one retention candidate. The rule and the Media item resolve through the
// caller's Library filter and the item must be a candidate of the rule, so
// unknown, hidden, cross-Library, and non-candidate IDs refuse without
// creating retention state. Viewer rescue does not come here:
// it is the per-Profile Favorite-class keep bit, written through setFavorite.
func (h *Handler) setRetentionCandidateHold(ctx context.Context, scope auth.Scope, req request) (map[string]any, error) {
	v := newVars(req)
	ruleID, _ := v.String("ruleID")
	mediaID, _ := v.String("mediaID")
	hold, _ := v.Bool("hold")
	if err := v.Err(); err != nil {
		return nil, err
	}
	if ruleID == "" {
		return nil, clientError("ruleID is required")
	}
	if mediaID == "" {
		return nil, clientError("mediaID is required")
	}
	if err := h.store.SetRetentionCandidateHold(ctx, ruleID, store.MediaID(mediaID), hold, scope.LibraryFilter(), time.Now().UTC()); err != nil {
		return nil, err
	}
	return map[string]any{"ruleID": ruleID, "mediaID": mediaID, "hold": hold}, nil
}

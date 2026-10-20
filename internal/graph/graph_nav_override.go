package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// NavOverrideInfo is the wire shape for the current navigation override (WEB-5J
// / ADR-0061 §Layout editor). It carries the full override blob plus a preview
// of the effective navigation node IDs after the override is applied.
type NavOverrideInfo struct {
	// HiddenNodeIDs lists the nav node IDs currently suppressed.
	HiddenNodeIDs []string `json:"hiddenNodeIDs"`
	// NodeOrder is the operator-defined node order (empty = default order).
	NodeOrder []string `json:"nodeOrder"`
	// NodeLabelOverrides maps node ID to a locale map ({"en": "My Label"}).
	NodeLabelOverrides map[string]map[string]string `json:"nodeLabelOverrides"`
	// ThemeAccentColor is the operator override for the manifest accent color.
	// Empty string means "use the platform default".
	ThemeAccentColor string `json:"themeAccentColor"`
	// LayoutSectionOverrides maps layout ID to its section override descriptor.
	LayoutSectionOverrides map[string]SectionOverrideInfo `json:"layoutSectionOverrides"`
}

// SectionOverrideInfo describes visibility and order overrides for one Layout.
type SectionOverrideInfo struct {
	HiddenSectionIDs []string `json:"hiddenSectionIDs"`
	SectionOrder     []string `json:"sectionOrder"`
}

// NavOverrideHistoryInfo is one audit-log entry returned by navOverrideHistory.
type NavOverrideHistoryInfo struct {
	ID        int64           `json:"id"`
	Scope     string          `json:"scope"`
	Data      NavOverrideInfo `json:"data"`
	CreatedAt string          `json:"createdAt"`
}

func overrideInfoFromStore(d store.NavOverrideData) NavOverrideInfo {
	sections := map[string]SectionOverrideInfo{}
	for k, v := range d.LayoutSectionOverrides {
		sections[k] = SectionOverrideInfo{
			HiddenSectionIDs: v.HiddenSectionIDs,
			SectionOrder:     v.SectionOrder,
		}
	}
	labels := d.NodeLabelOverrides
	if labels == nil {
		labels = map[string]map[string]string{}
	}
	hidden := d.HiddenNodeIDs
	if hidden == nil {
		hidden = []string{}
	}
	order := d.NodeOrder
	if order == nil {
		order = []string{}
	}
	return NavOverrideInfo{
		HiddenNodeIDs:          hidden,
		NodeOrder:              order,
		NodeLabelOverrides:     labels,
		ThemeAccentColor:       d.ThemeAccentColor,
		LayoutSectionOverrides: sections,
	}
}

// navOverride returns the current server-level navigation override.
func (h *Handler) navOverride(ctx context.Context, _ auth.Scope, _ request) (NavOverrideInfo, error) {
	data, err := h.store.NavOverride(ctx, "server", nil)
	if err != nil {
		return NavOverrideInfo{}, err
	}
	return overrideInfoFromStore(data), nil
}

// navOverrideHistory returns recent server-level override history (newest first).
func (h *Handler) navOverrideHistory(ctx context.Context, _ auth.Scope, req request) ([]NavOverrideHistoryInfo, error) {
	limit := boundedIntVar(req, "limit", 20, 100)
	entries, err := h.store.NavOverrideHistory(ctx, "server", nil, limit)
	if err != nil {
		return nil, err
	}
	out := make([]NavOverrideHistoryInfo, len(entries))
	for i, e := range entries {
		out[i] = NavOverrideHistoryInfo{
			ID:        e.ID,
			Scope:     e.Scope,
			Data:      overrideInfoFromStore(e.Data),
			CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339),
		}
	}
	return out, nil
}

// setNavOverride applies a partial override patch and returns the new state.
// Fields not present in the JSON body are left unchanged (read–modify–write).
func (h *Handler) setNavOverride(ctx context.Context, _ auth.Scope, req request) (NavOverrideInfo, error) {
	current, err := h.store.NavOverride(ctx, "server", nil)
	if err != nil {
		return NavOverrideInfo{}, err
	}
	if v, ok := req.Variables["hiddenNodeIDs"]; ok {
		current.HiddenNodeIDs = toStringSlice(v)
	}
	if v, ok := req.Variables["nodeOrder"]; ok {
		current.NodeOrder = toStringSlice(v)
	}
	if v, ok := req.Variables["themeAccentColor"]; ok {
		current.ThemeAccentColor, _ = v.(string)
	}
	if v, ok := req.Variables["nodeLabelOverrides"]; ok {
		current.NodeLabelOverrides = toStringMapStringMap(v)
	}
	if v, ok := req.Variables["layoutSectionOverrides"]; ok {
		current.LayoutSectionOverrides = toSectionOverrides(v)
	}
	if err := h.store.SetNavOverride(ctx, "server", nil, current, time.Now().UTC()); err != nil {
		return NavOverrideInfo{}, err
	}
	return overrideInfoFromStore(current), nil
}

// resetNavOverride clears all overrides, returning the now-empty state.
func (h *Handler) resetNavOverride(ctx context.Context, _ auth.Scope, _ request) (NavOverrideInfo, error) {
	if err := h.store.ResetNavOverride(ctx, "server", nil, time.Now().UTC()); err != nil {
		return NavOverrideInfo{}, err
	}
	return overrideInfoFromStore(store.NavOverrideData{}), nil
}

// restoreNavOverride replaces the current override with a specific history entry.
func (h *Handler) restoreNavOverride(ctx context.Context, _ auth.Scope, req request) (NavOverrideInfo, error) {
	idVal, ok := req.Variables["historyID"]
	if !ok {
		return NavOverrideInfo{}, clientError("historyID is required")
	}
	// JSON is decoded without UseNumber so numeric variables arrive as float64.
	n, ok := idVal.(float64)
	if !ok {
		return NavOverrideInfo{}, clientError("historyID must be an integer")
	}
	historyID := int64(n)
	data, err := h.store.RestoreNavOverride(ctx, "server", nil, historyID, time.Now().UTC())
	if err != nil {
		return NavOverrideInfo{}, err
	}
	return overrideInfoFromStore(data), nil
}

// toStringSlice converts a []interface{} variable to []string.
func toStringSlice(v interface{}) []string {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, a := range arr {
		if s, ok := a.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// toStringMapStringMap converts a map[string]interface{} variable to
// map[string]map[string]string (locale maps keyed by node ID).
func toStringMapStringMap(v interface{}) map[string]map[string]string {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]map[string]string, len(m))
	for k, vv := range m {
		inner, ok := vv.(map[string]interface{})
		if !ok {
			continue
		}
		locale := make(map[string]string, len(inner))
		for lk, lv := range inner {
			if s, ok := lv.(string); ok {
				locale[lk] = s
			}
		}
		out[k] = locale
	}
	return out
}

// toSectionOverrides converts a map[string]interface{} variable to the store type.
func toSectionOverrides(v interface{}) map[string]store.SectionOverrideData {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]store.SectionOverrideData, len(m))
	for k, vv := range m {
		inner, ok := vv.(map[string]interface{})
		if !ok {
			continue
		}
		var s store.SectionOverrideData
		if h, ok := inner["hiddenSectionIDs"]; ok {
			s.HiddenSectionIDs = toStringSlice(h)
		}
		if o, ok := inner["sectionOrder"]; ok {
			s.SectionOrder = toStringSlice(o)
		}
		out[k] = s
	}
	return out
}

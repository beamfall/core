package graph

import (
	"context"
	"strconv"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
)

// HomeLayoutInfo is the session Profile's Home layout (CORE-1972): every Home
// Section the Profile receives, hidden ones included, in effective order.
type HomeLayoutInfo struct {
	// Customized reports whether the Profile saved a layout; false means the
	// default order.
	Customized bool                    `json:"customized"`
	Sections   []HomeLayoutSectionInfo `json:"sections"`
}

// HomeLayoutSectionInfo is one Home Section: its generic manifest id, title
// (locale map, like the manifest Section title), kind and whether the Profile
// hid it.
type HomeLayoutSectionInfo struct {
	ID     string              `json:"id"`
	Title  plugin.LocaleString `json:"title"`
	Kind   string              `json:"kind"`
	Hidden bool                `json:"hidden"`
}

func homeLayoutInfo(layout manifest.HomeLayout) HomeLayoutInfo {
	sections := make([]HomeLayoutSectionInfo, len(layout.Entries))
	for i, entry := range layout.Entries {
		sections[i] = HomeLayoutSectionInfo{
			ID:     entry.Section.ID,
			Title:  entry.Section.Title,
			Kind:   entry.Section.Kind,
			Hidden: entry.Hidden,
		}
	}
	return HomeLayoutInfo{Customized: layout.Customized, Sections: sections}
}

// homeLayout reads the session Profile's Home layout.
func (h *Handler) homeLayout(ctx context.Context, scope auth.Scope) (HomeLayoutInfo, error) {
	layout, err := h.manifest.HomeLayout(ctx, scope)
	if err != nil {
		return HomeLayoutInfo{}, err
	}
	return homeLayoutInfo(layout), nil
}

// setHomeLayout saves the session Profile's Home order and hidden set. It is
// the Profile's own display preference, so any session may set it; it only
// orders and hides Sections the Profile already receives.
func (h *Handler) setHomeLayout(ctx context.Context, scope auth.Scope, req request) (HomeLayoutInfo, error) {
	order, err := homeLayoutIDs(req, "sectionOrder")
	if err != nil {
		return HomeLayoutInfo{}, err
	}
	hidden, err := homeLayoutIDs(req, "hiddenSectionIDs")
	if err != nil {
		return HomeLayoutInfo{}, err
	}
	layout, err := h.manifest.SetHomeLayout(ctx, scope, order, hidden)
	if err != nil {
		return HomeLayoutInfo{}, err
	}
	return homeLayoutInfo(layout), nil
}

// resetHomeLayout clears the session Profile's Home layout.
func (h *Handler) resetHomeLayout(ctx context.Context, scope auth.Scope) (HomeLayoutInfo, error) {
	layout, err := h.manifest.ResetHomeLayout(ctx, scope)
	if err != nil {
		return HomeLayoutInfo{}, err
	}
	return homeLayoutInfo(layout), nil
}

// homeLayoutIDs reads one list-of-ids argument; absent means empty.
func homeLayoutIDs(req request, name string) ([]string, error) {
	raw, ok := req.Variables[name]
	if !ok || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil, clientError(name + " must be a list of section ids")
	}
	if len(items) > manifest.MaxHomeLayoutIDs {
		return nil, clientError(name + " exceeds maximum of " + strconv.Itoa(manifest.MaxHomeLayoutIDs) + " sections")
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		id, ok := item.(string)
		if !ok {
			return nil, clientError(name + " must be a list of section ids")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
)

// libraryIconBasis returns what a Library's default icon comes from: the icon
// its bound plugin declares and the media classes it declares, or zero values
// for a Library with no plugin or an unregistered one.
func (h *Handler) libraryIconBasis(library store.Library) (string, []string) {
	if library.PluginID == nil || h.plugins == nil {
		return "", nil
	}
	bundle, ok := h.plugins.Bundle(plugin.ID(*library.PluginID))
	if !ok {
		return "", nil
	}
	return bundle.LibraryDefaults.Icon, bundle.MediaClasses
}

// pluginImageRule returns the scan image rule for a registered plugin's
// declared Media classes, so the scan preview and estimate count images
// exactly as the scan imports them (CORE-0043, CORE-2180, CORE-2361). No
// plugin, or an unregistered one, keeps the default rule.
func (h *Handler) pluginImageRule(pluginID string) scan.ImageRule {
	if pluginID == "" || h.plugins == nil {
		return scan.ImagesBesideMedia
	}
	bundle, ok := h.plugins.Bundle(plugin.ID(pluginID))
	if !ok {
		return scan.ImagesBesideMedia
	}
	classes := make([]store.MediaClass, 0, len(bundle.MediaClasses))
	for _, class := range bundle.MediaClasses {
		classes = append(classes, store.MediaClass(class))
	}
	return scan.ImageRuleFor(classes)
}

// setLibraryIcon stores the owner's icon choice for a Library (CORE-1200).
// icon is a name from the fixed Library icon vocabulary; an empty icon clears
// the choice so the Library follows its media-class default. It is reached
// through the admin-gated console-config write and audited against the Library.
func (h *Handler) setLibraryIcon(ctx context.Context, scope auth.Scope, libraryID, icon string) error {
	if libraryID == "" {
		return clientError("libraryID is required")
	}
	if icon != "" && !store.IsLibraryIcon(icon) {
		return clientError("unknown library icon")
	}
	visible, err := h.visibleLibrary(ctx, scope, store.LibraryID(libraryID))
	if err != nil {
		return err
	}
	if !visible {
		return clientError("unknown library")
	}
	if err := h.store.SetLibraryIcon(ctx, store.LibraryID(libraryID), icon, time.Now().UTC()); err != nil {
		return err
	}
	return h.logActivity(ctx, scope, store.ActivityLibraryIcon, "library", libraryID, store.LibraryID(libraryID))
}

package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// libraryMediaClasses returns the media classes the Library's bound plugin
// declares, or nil for a Library with no plugin or an unregistered one. It is
// the only input to a Library's default icon.
func (h *Handler) libraryMediaClasses(library store.Library) []string {
	if library.PluginID == nil || h.plugins == nil {
		return nil
	}
	bundle, ok := h.plugins.Bundle(plugin.ID(*library.PluginID))
	if !ok {
		return nil
	}
	return bundle.MediaClasses
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

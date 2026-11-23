package graph

import (
	"context"

	"github.com/beamfall/core/internal/store"
)

// annotatePlaceholderIcon sets PlaceholderIcon on each Media item whose
// Library plugin declares a default icon (CORE-3336), so a client draws it in
// place of missing artwork. It runs after AnnotateMediaClass, which supplies
// each item's Library. Core passes the declaration through and never derives
// it from a class or a plugin name. Entities, plugin-less Libraries and
// non-declaring plugins leave it empty.
func (h *Handler) annotatePlaceholderIcon(ctx context.Context, items []store.SourceItem) {
	icons := map[store.LibraryID]string{}
	for i := range items {
		item := &items[i]
		if item.Kind == "entity" || item.LibraryID == "" {
			continue
		}
		icon, seen := icons[item.LibraryID]
		if !seen {
			icon = h.libraryPlaceholderIcon(ctx, item.LibraryID)
			icons[item.LibraryID] = icon
		}
		item.PlaceholderIcon = icon
	}
}

// libraryPlaceholderIcon returns the Library icon a Library's plugin declares,
// or "" for a plugin-less Library, a plugin that declares none, or a name
// outside the vocabulary.
func (h *Handler) libraryPlaceholderIcon(ctx context.Context, libraryID store.LibraryID) string {
	if libraryID == "" {
		return ""
	}
	bundle, found := h.libraryBundle(ctx, libraryID)
	if !found || !store.IsLibraryIcon(bundle.LibraryDefaults.Icon) {
		return ""
	}
	return bundle.LibraryDefaults.Icon
}

package graph

import (
	"context"

	"github.com/beamfall/core/internal/store"
)

// annotatePlaysAlone sets PlaysAlone on each Media item whose Library plugin
// declares that its Media play alone (CORE-3147). It runs after
// AnnotateMediaClass, which supplies each item's Library. Core passes the
// declaration through and never derives it from a class or a plugin name.
// Entities, plugin-less Libraries and non-declaring plugins leave it false.
func (h *Handler) annotatePlaysAlone(ctx context.Context, items []store.SourceItem) {
	alone := map[store.LibraryID]bool{}
	for i := range items {
		item := &items[i]
		if item.Kind == "entity" || item.LibraryID == "" {
			continue
		}
		playsAlone, seen := alone[item.LibraryID]
		if !seen {
			playsAlone = h.libraryPlaysAlone(ctx, item.LibraryID)
			alone[item.LibraryID] = playsAlone
		}
		item.PlaysAlone = playsAlone
	}
}

// libraryPlaysAlone reports whether a Library's plugin declares that its Media
// play alone. A plugin-less Library does not.
func (h *Handler) libraryPlaysAlone(ctx context.Context, libraryID store.LibraryID) bool {
	if libraryID == "" {
		return false
	}
	bundle, found := h.libraryBundle(ctx, libraryID)
	return found && bundle.Engagement.PlaysAlone
}

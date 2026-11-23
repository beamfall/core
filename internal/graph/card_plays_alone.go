package graph

import (
	"context"

	"github.com/beamfall/core/internal/store"
)

// selectFlags are a Library plugin's per-Media Select and queue declarations.
type selectFlags struct {
	playsAlone  bool
	selectPlays bool
}

// annotatePlaysAlone sets PlaysAlone and SelectPlays on each Media item whose
// Library plugin declares that its Media play alone (CORE-3147) or play on
// Select (CORE-3307). It runs after AnnotateMediaClass, which supplies each
// item's Library. Core passes the declarations through and never derives them
// from a class or a plugin name. Entities, plugin-less Libraries and
// non-declaring plugins leave both false.
func (h *Handler) annotatePlaysAlone(ctx context.Context, items []store.SourceItem) {
	byLibrary := map[store.LibraryID]selectFlags{}
	for i := range items {
		item := &items[i]
		if item.Kind == "entity" || item.LibraryID == "" {
			continue
		}
		flags, seen := byLibrary[item.LibraryID]
		if !seen {
			flags = h.librarySelectFlags(ctx, item.LibraryID)
			byLibrary[item.LibraryID] = flags
		}
		item.PlaysAlone = flags.playsAlone
		item.SelectPlays = flags.selectPlays
	}
}

// librarySelectFlags reads a Library plugin's PlaysAlone and SelectPlays
// declarations. A plugin-less Library declares neither.
func (h *Handler) librarySelectFlags(ctx context.Context, libraryID store.LibraryID) selectFlags {
	if libraryID == "" {
		return selectFlags{}
	}
	bundle, found := h.libraryBundle(ctx, libraryID)
	if !found {
		return selectFlags{}
	}
	return selectFlags{playsAlone: bundle.Engagement.PlaysAlone, selectPlays: bundle.Engagement.SelectPlays}
}

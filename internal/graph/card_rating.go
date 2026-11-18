package graph

import (
	"context"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// annotateScoreLabels sets each scored Media item's AudienceScoreLabel to the
// label its Library plugin declares for the core-conventional score field, so
// a client can name the score's source (CORE-2415). It runs after
// AnnotateMediaClass, which supplies each item's Library and score. Core
// passes the label through and never reads its words. An unscored item, a
// plugin-less Library, or a plugin that declares no label leaves it nil.
func (h *Handler) annotateScoreLabels(ctx context.Context, items []store.SourceItem) {
	labels := map[store.LibraryID]plugin.LocaleString{}
	for i := range items {
		item := &items[i]
		if item.Kind == "entity" || item.AudienceScore == nil || item.LibraryID == "" {
			continue
		}
		label, seen := labels[item.LibraryID]
		if !seen {
			if bundle, found := h.libraryBundle(ctx, item.LibraryID); found {
				label = mediaFieldLabel(bundle, store.AudienceScoreField)
			}
			labels[item.LibraryID] = label
		}
		if len(label) > 0 {
			item.AudienceScoreLabel = map[string]string(label)
		}
	}
}

// mediaFieldLabel is the label a bundle declares for one of its Media fields.
func mediaFieldLabel(bundle plugin.Bundle, name string) plugin.LocaleString {
	for _, field := range bundle.MediaFields {
		if field.Name == name {
			return field.Label
		}
	}
	return nil
}

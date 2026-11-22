package main

import (
	"context"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/store"
	books "github.com/beamfall/plugin-audiobook"
)

// bookPackResolver adapts a books plugin pack resolver to the
// offline pack service. A book is one Media; the pack service carries every
// Part of its selected Version and its chapter Markers (CORE-3016).
type bookPackResolver struct {
	store packStore
	inner books.PackResolver
}

func (r bookPackResolver) Resolve(ctx context.Context, ref string, filter store.LibraryFilter) (offlinepack.ResolvedTarget, error) {
	resolved, err := r.inner.Resolve(ctx, ref, packMediaReader{store: r.store, filter: filter})
	if err != nil {
		return offlinepack.ResolvedTarget{}, err
	}
	mediaIDs := make([]store.MediaID, len(resolved.MediaIDs))
	for i, id := range resolved.MediaIDs {
		mediaIDs[i] = store.MediaID(id)
	}
	return offlinepack.ResolvedTarget{MediaIDs: mediaIDs, Label: resolved.Label}, nil
}

func addBookPackResolvers(hooks *app.Hooks) {
	for kind, resolver := range books.PackResolvers() {
		hooks.OfflinePackResolvers = append(hooks.OfflinePackResolvers, app.OfflinePackResolverHook{
			PluginID: books.PluginID,
			Kind:     kind,
			New: func(st *store.Store) offlinepack.TargetResolver {
				return bookPackResolver{store: st, inner: resolver}
			},
		})
	}
}

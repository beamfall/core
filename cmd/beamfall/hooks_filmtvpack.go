package main

import (
	"context"
	"errors"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/store"
	filmtv "github.com/beamfall/plugin-filmtv"
)

// filmtvPackPage is the page size for reading related container Entities.
const filmtvPackPage = 200

// filmtvPackStore is the store slice the Film & TV pack reader uses.
type filmtvPackStore interface {
	MediaForEntity(context.Context, store.EntityID, store.LibraryFilter) ([]store.SourceItem, error)
	Entity(context.Context, store.EntityID, store.LibraryFilter) (store.Entity, error)
	EntitiesRelatingTo(ctx context.Context, id store.EntityID, fromKind, relation string, offset, limit int) ([]store.EntityID, error)
	PlaybackProgressFor(context.Context, store.ProfileID, store.MediaID) (store.PlaybackProgress, bool, error)
}

// filmtvPackReader implements filmtv.PackMediaReader over the store, bound to
// one caller's Library filter and Profile.
type filmtvPackReader struct {
	store   filmtvPackStore
	filter  store.LibraryFilter
	profile store.ProfileID
}

func (r filmtvPackReader) GroupMembers(ctx context.Context, ref string) ([]string, error) {
	items, err := r.store.MediaForEntity(ctx, store.EntityID(ref), r.filter)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, string(item.ID))
	}
	return ids, nil
}

// EntitiesOf reads the parent through the filter first, so a hidden parent is
// an error. Related Entities share the parent's Library, so each one is then
// read through the same filter for its fields.
func (r filmtvPackReader) EntitiesOf(ctx context.Context, parentRef, kind, relation string) ([]filmtv.PackEntity, error) {
	parent := store.EntityID(parentRef)
	if _, err := r.store.Entity(ctx, parent, r.filter); err != nil {
		return nil, err
	}
	var out []filmtv.PackEntity
	for offset := 0; ; offset += filmtvPackPage {
		ids, err := r.store.EntitiesRelatingTo(ctx, parent, kind, relation, offset, filmtvPackPage)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			entity, err := r.store.Entity(ctx, id, r.filter)
			if err != nil {
				return nil, err
			}
			out = append(out, packEntityOf(entity))
		}
		if len(ids) < filmtvPackPage {
			return out, nil
		}
	}
}

func packEntityOf(entity store.Entity) filmtv.PackEntity {
	out := filmtv.PackEntity{ID: string(entity.ID), Numbers: map[string]float64{}, Flags: map[string]bool{}}
	for _, field := range entity.Fields {
		switch field.Type {
		case store.EntityFieldNumber:
			out.Numbers[field.Name] = field.Number
		case store.EntityFieldFlag:
			out.Flags[field.Name] = field.Flag
		}
	}
	return out
}

func (r filmtvPackReader) Watched(ctx context.Context, ids []string) (map[string]bool, error) {
	if r.profile == "" {
		return nil, errors.New("offline pack watch state needs a Profile")
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		progress, ok, err := r.store.PlaybackProgressFor(ctx, r.profile, store.MediaID(id))
		if err != nil {
			return nil, err
		}
		if ok && progress.Watched {
			out[id] = true
		}
	}
	return out, nil
}

// filmtvPackResolver adapts a Film & TV pack resolver to the offline pack
// service. It resolves per Profile so watch-state kinds see the caller's
// progress.
type filmtvPackResolver struct {
	store filmtvPackStore
	inner filmtv.PackResolver
}

func (r filmtvPackResolver) Resolve(ctx context.Context, ref string, filter store.LibraryFilter) (offlinepack.ResolvedTarget, error) {
	return r.ResolveForProfile(ctx, ref, "", filter)
}

func (r filmtvPackResolver) ResolveForProfile(ctx context.Context, ref string, profile store.ProfileID, filter store.LibraryFilter) (offlinepack.ResolvedTarget, error) {
	resolved, err := r.inner.Resolve(ctx, ref, filmtvPackReader{store: r.store, filter: filter, profile: profile})
	if err != nil {
		return offlinepack.ResolvedTarget{}, err
	}
	mediaIDs := make([]store.MediaID, len(resolved.MediaIDs))
	for i, id := range resolved.MediaIDs {
		mediaIDs[i] = store.MediaID(id)
	}
	return offlinepack.ResolvedTarget{MediaIDs: mediaIDs, Label: resolved.Label}, nil
}

func addFilmTVPackResolvers(hooks *app.Hooks) {
	for kind, resolver := range filmtv.PackResolvers() {
		hooks.OfflinePackResolvers = append(hooks.OfflinePackResolvers, app.OfflinePackResolverHook{
			PluginID: filmtv.PluginID,
			Kind:     kind,
			New: func(st *store.Store) offlinepack.TargetResolver {
				return filmtvPackResolver{store: st, inner: resolver}
			},
		})
	}
}

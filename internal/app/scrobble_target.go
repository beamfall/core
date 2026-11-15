package app

import (
	"context"
	"time"

	sdk "github.com/beamfall/plugin-sdk"
	scrobble "github.com/beamfall/plugin-sdk/scrobble"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// scrobbleTargetLookupTimeout bounds the store reads the target resolver makes
// on the playback path; a slow read leaves the hint empty (the dispatcher then
// skips the event) instead of stalling playback.
const scrobbleTargetLookupTimeout = 2 * time.Second

// scrobbleTargetResolver selects a watched Media's scrobble TargetHint from
// the ScrobbleTargetSpec its Library's activated plugin declares. Core reads
// the spec as opaque plugin data: a Default hint applies to every Media, or a
// declared text Media field's exact value selects a mapped hint. Every miss
// (unbound Library, plugin without a spec, absent or unmapped field value,
// read error) returns "" so the dispatcher fails closed and skips the event.
type scrobbleTargetResolver struct {
	store *store.Store
	specs map[string]sdk.ScrobbleTargetSpec
}

// newScrobbleTargetResolver snapshots each activated bundle's declared
// ScrobbleTargetSpec. plugins may be nil (no activated plugins, so every hint
// resolves empty).
func newScrobbleTargetResolver(st *store.Store, plugins *plugin.Registry) *scrobbleTargetResolver {
	specs := map[string]sdk.ScrobbleTargetSpec{}
	for _, id := range plugins.IDs() {
		bundle, ok := plugins.Bundle(id)
		if !ok || bundle.ScrobbleTarget == nil {
			continue
		}
		specs[string(id)] = *bundle.ScrobbleTarget
	}
	return &scrobbleTargetResolver{store: st, specs: specs}
}

// resolve returns the Media's known TargetHint, or "" on any miss.
func (r *scrobbleTargetResolver) resolve(ctx context.Context, libraryID store.LibraryID, mediaID store.MediaID) scrobble.TargetHint {
	if r == nil || r.store == nil || libraryID == "" {
		return ""
	}
	pluginID, err := r.store.LibraryPluginID(ctx, libraryID)
	if err != nil || pluginID == "" {
		return ""
	}
	spec, ok := r.specs[pluginID]
	if !ok {
		return ""
	}
	if spec.Default != "" {
		return knownTargetHint(spec.Default)
	}
	if spec.MediaField == "" || mediaID == "" {
		return ""
	}
	fields, err := r.store.MediaFields(ctx, mediaID, metadataLibraryFilter)
	if err != nil {
		return ""
	}
	for _, field := range fields {
		if field.Name != spec.MediaField || field.Type != store.EntityFieldText {
			continue
		}
		for _, mapping := range spec.Mappings {
			if mapping.Value == field.Text {
				return knownTargetHint(mapping.Hint)
			}
		}
		return ""
	}
	return ""
}

// knownTargetHint passes a hint through only when it belongs to the bounded
// SDK vocabulary; anything else resolves empty (fail closed).
func knownTargetHint(hint scrobble.TargetHint) scrobble.TargetHint {
	if hint.Known() {
		return hint
	}
	return ""
}

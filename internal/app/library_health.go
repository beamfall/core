package app

import (
	"context"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/store"
)

// healthProfileSource is the identify seam that derives a plugin's Library
// health profile from its bundle; *identify.Service satisfies it.
type healthProfileSource interface {
	HealthProfile(pluginID string) store.LibraryHealthProfile
}

// libraryHealthProfiler makes Library health follow each Library's plugin
// (CORE-2340, CORE-2383). A plugin that does not auto-identify but hydrates
// from local metadata counts a Media its hydration marked complete as
// identified.
func libraryHealthProfiler(source healthProfileSource, mappers map[string]HydrationMapper) store.LibraryHealthProfiler {
	return func(pluginID string) store.LibraryHealthProfile {
		profile := source.HealthProfile(pluginID)
		if !profile.Plugin || profile.AutoIdentify {
			return profile
		}
		if mapper := mappers[pluginID]; mapper != nil {
			profile.LocalIdentityField = mapper.MarkerField()
		}
		return profile
	}
}

// libraryHealthArtworkProbe accepts stored artwork that is not the server's
// generated frame grab: embedded, sidecar and fetched art all count.
func libraryHealthArtworkProbe(service *artwork.Service) store.LibraryHealthArtworkProbe {
	return func(id store.MediaID, slot string) bool {
		return service.HasArtwork(id, slot) && !service.IsGeneratedArtwork(id, slot)
	}
}

// installLibraryHealthProfile wires the profile and artwork probe and drops
// counts cached under earlier rules, so reads stay live until the next scan
// refreshes the cache.
func installLibraryHealthProfile(ctx context.Context, st *store.Store, source healthProfileSource, mappers map[string]HydrationMapper, artworkService *artwork.Service) error {
	st.SetLibraryHealthProfiler(libraryHealthProfiler(source, mappers))
	if artworkService != nil {
		st.SetLibraryHealthArtworkProbe(libraryHealthArtworkProbe(artworkService))
	}
	return st.ClearLibraryHealthCache(ctx)
}

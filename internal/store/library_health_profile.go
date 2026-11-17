package store

import "context"

// UnmatchedManualScraperID is the scraper ID an unmatched_queue row carries when
// the identification pipeline queued a Media for manual review without
// attempting a scrape. Library health does not count such a row as a scrape
// failure.
const UnmatchedManualScraperID = "manual"

// LibraryHealthProfile says which health dimensions apply to a Library and how
// its Media are judged, derived from the owning plugin's bundle declarations
// (never from domain words). The zero value is a Library no plugin owns.
type LibraryHealthProfile struct {
	// Plugin is true when a loaded plugin owns the Library.
	Plugin bool
	// Searchable is true when the plugin has a scraper an owner can search
	// (Fix match can help). It makes unmatched apply; without it no-scraper does.
	Searchable bool
	// AutoIdentify is true when scans run an automatic scrape for the
	// Library's Media (rescan and rescrape can help). It makes scrape-failure
	// apply.
	AutoIdentify bool
	// LocalIdentityField names the integer flag media field the plugin's local
	// hydration sets to 1 when it identified a Media from local metadata. Empty
	// when the plugin has none or auto-identifies.
	LocalIdentityField string
	// ClassField names the media text field that carries a Media's archetype
	// beyond its stored media class. Empty uses the media class alone.
	ClassField string
	// ArtworkSlots lists, per Media archetype, the artwork slots the plugin's
	// scrapers supply for it, including the slot those slots fall back to.
	ArtworkSlots map[string][]string
	// DefaultArtworkSlots apply to an archetype ArtworkSlots does not name.
	// Missing artwork applies only when the plugin supplies some slot.
	DefaultArtworkSlots []string
}

// LibraryHealthProfiler resolves a plugin ID to its health profile.
type LibraryHealthProfiler func(pluginID string) LibraryHealthProfile

// LibraryHealthArtworkProbe reports whether a Media holds stored, non-generated
// artwork in a slot (embedded, sidecar or fetched art that never became an
// applied candidate).
type LibraryHealthArtworkProbe func(id MediaID, slot string) bool

// SetLibraryHealthProfiler installs the app's plugin-derived health profile
// resolver. Without one, a Library with a plugin keeps every dimension with the
// poster as its only artwork slot, and a Library without one is plugin-less.
func (s *Store) SetLibraryHealthProfiler(fn LibraryHealthProfiler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthProfiler = fn
}

// SetLibraryHealthArtworkProbe installs the app's stored-artwork check used by
// the missing-artwork dimension.
func (s *Store) SetLibraryHealthArtworkProbe(fn LibraryHealthArtworkProbe) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthArtworkProbe = fn
}

// healthProfileLocked resolves a Library's health profile. Caller holds mu.
func (s *Store) healthProfileLocked(library Library) LibraryHealthProfile {
	if library.PluginID == nil || *library.PluginID == "" {
		return LibraryHealthProfile{}
	}
	if s.healthProfiler != nil {
		return s.healthProfiler(*library.PluginID)
	}
	return LibraryHealthProfile{
		Plugin:              true,
		Searchable:          true,
		AutoIdentify:        true,
		DefaultArtworkSlots: []string{"poster"},
	}
}

// healthDimensionApplies reports whether a dimension means anything for a
// Library with this profile.
func healthDimensionApplies(p LibraryHealthProfile, dim HealthDimension) bool {
	switch dim {
	case HealthUnmatched:
		return p.Searchable
	case HealthScrapeFailure:
		return p.Searchable && p.AutoIdentify
	case HealthNoScraper:
		return !p.Searchable
	case HealthMissingArtwork:
		if !p.Plugin {
			return false
		}
		if len(p.DefaultArtworkSlots) > 0 {
			return true
		}
		for _, slots := range p.ArtworkSlots {
			if len(slots) > 0 {
				return true
			}
		}
		return false
	}
	return validHealthDimension(dim)
}

// artworkSlotsFor returns the slots a Media of an archetype can be shown with.
func (p LibraryHealthProfile) artworkSlotsFor(archetype string) []string {
	if slots, ok := p.ArtworkSlots[archetype]; ok && len(slots) > 0 {
		return slots
	}
	return p.DefaultArtworkSlots
}

// ClearLibraryHealthCache drops every cached health count, so reads fall back
// to live counts until RefreshLibraryHealthCache runs again.
func (s *Store) ClearLibraryHealthCache(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `DELETE FROM library_health_cache`)
	return err
}

package identify

import (
	"slices"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// HealthProfile derives a plugin's Library health profile from its loaded
// bundle (CORE-2340, CORE-2383): whether an owner can search its scrapers,
// whether scans auto-identify its Media, and which artwork slots its scrapers
// supply for each Media archetype (the class a scraper implies). A plugin whose
// bundle is not loaded is reported as no plugin. The local-identity field is
// left to the caller, which owns the plugin's hydration marker.
func (s *Service) HealthProfile(pluginID string) store.LibraryHealthProfile {
	if s == nil || s.bundles == nil || pluginID == "" {
		return store.LibraryHealthProfile{}
	}
	bundle, ok := s.bundles.Bundle(plugin.ID(pluginID))
	if !ok {
		return store.LibraryHealthProfile{}
	}
	profile := store.LibraryHealthProfile{Plugin: true}
	fieldTypes := bundle.MediaFieldTypes()
	byClass := map[string]map[string]bool{}
	all := map[string]bool{}
	for _, scraper := range bundle.Scrapers {
		if scraper.SearchTemplate != "" && scraper.CandidateIDPath != "" &&
			(!scraper.OptionalAuth || s.credentialAvailable(scraper.AuthRef)) {
			profile.Searchable = true
		}
		slots := scraperArtworkSlots(scraper)
		for slot := range slots {
			all[slot] = true
		}
		class, classKey := impliedClassKey(scraper)
		if class == "" {
			continue
		}
		if profile.ClassField == "" {
			if route, err := plugin.ClassifyMediaFieldKey(classKey, fieldTypes); err == nil {
				profile.ClassField = route.MediaField
			}
		}
		if byClass[class] == nil {
			byClass[class] = map[string]bool{}
		}
		for slot := range slots {
			byClass[class][slot] = true
		}
	}
	_, scored := s.scorers[pluginID]
	profile.AutoIdentify = profile.Searchable && scored && bundle.IdentificationPolicy != plugin.IdentificationPolicyQueue
	profile.DefaultArtworkSlots = orderedSlots(all)
	if len(byClass) > 0 {
		profile.ArtworkSlots = make(map[string][]string, len(byClass))
		for class, slots := range byClass {
			if ordered := orderedSlots(slots); len(ordered) > 0 {
				profile.ArtworkSlots[class] = ordered
			}
		}
	}
	return profile
}

// scraperArtworkSlots lists the Core artwork slots a scraper can fill, plus the
// slot each falls back to when served.
func scraperArtworkSlots(scraper plugin.ScraperSpec) map[string]bool {
	slots := map[string]bool{}
	add := func(slot string) {
		if !artwork.ValidSlot(slot) {
			return
		}
		slots[slot] = true
		if fb, ok := artwork.SlotFallback(slot); ok {
			slots[fb] = true
		}
	}
	for slot := range scraper.ArtworkMap {
		add(slot)
	}
	for slot := range scraper.SearchArtworkMap {
		add(slot)
	}
	for _, slot := range scraper.CandidateArtworkSlots {
		add(slot)
	}
	return slots
}

// impliedClassKey returns the class a scraper implies and the field key that
// declares it.
func impliedClassKey(scraper plugin.ScraperSpec) (string, string) {
	for _, key := range []string{"media.class", "class"} {
		if v, ok := scraper.ImpliedMediaFields[key]; ok {
			return v, key
		}
	}
	return "", ""
}

// orderedSlots returns a slot set in the fixed Core slot order.
func orderedSlots(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for _, slot := range artwork.KnownSlots {
		if set[slot] {
			out = append(out, slot)
		}
	}
	return slices.Clip(out)
}

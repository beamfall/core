package manifest

import (
	"context"
	"fmt"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// HomeLayoutID is the wire ID of the core Home Layout.
const HomeLayoutID = "layout-home"

// continueSectionID is the Home Section the default order leads with.
const continueSectionID = "continue"

// MaxHomeLayoutIDs bounds each list a Profile may save (CORE-1972).
const MaxHomeLayoutIDs = 500

// navScopeProfile is the nav_override scope holding per-Profile preferences
// (migration 0077); scope_id is the Profile ID.
const navScopeProfile = "profile"

// HomeLayoutEntry is one Home Section a Profile receives, with whether the
// Profile hid it.
type HomeLayoutEntry struct {
	Section Section
	Hidden  bool
}

// HomeLayout is a Profile's view of its Home Sections: every Section it
// receives (hidden ones included) in effective order.
type HomeLayout struct {
	Customized bool
	Entries    []HomeLayoutEntry
}

// errHomeLayoutNoProfile refuses a preference write from a scope with no
// Profile (an admin API token or an admin-scoped key).
var errHomeLayoutNoProfile = store.ValidationError("home layout needs a Profile session")

// HomeLayout returns the session Profile's Home Sections in effective order.
func (s *Service) HomeLayout(ctx context.Context, scope auth.Scope) (HomeLayout, error) {
	if scope.ProfileID == "" {
		return HomeLayout{}, errHomeLayoutNoProfile
	}
	base, err := s.profileHomeBase(ctx, scope)
	if err != nil {
		return HomeLayout{}, err
	}
	preference, err := s.homePreference(ctx, scope)
	if err != nil {
		return HomeLayout{}, err
	}
	hidden := idSet(preference.HiddenSectionIDs)
	ordered := orderSections(base.Sections, preference)
	entries := make([]HomeLayoutEntry, len(ordered))
	for i, section := range ordered {
		entries[i] = HomeLayoutEntry{Section: section, Hidden: hidden[section.ID]}
	}
	return HomeLayout{Customized: customized(preference), Entries: entries}, nil
}

// SetHomeLayout saves the session Profile's Home order and hidden set. Ids the
// Profile does not currently receive and duplicates are dropped, so a saved
// preference never names a Section outside the Profile's Libraries or one the
// owner hid server-wide.
func (s *Service) SetHomeLayout(ctx context.Context, scope auth.Scope, order, hidden []string) (HomeLayout, error) {
	if scope.ProfileID == "" {
		return HomeLayout{}, errHomeLayoutNoProfile
	}
	if len(order) > MaxHomeLayoutIDs || len(hidden) > MaxHomeLayoutIDs {
		return HomeLayout{}, store.ValidationError(fmt.Sprintf("home layout lists at most %d sections", MaxHomeLayoutIDs))
	}
	base, err := s.profileHomeBase(ctx, scope)
	if err != nil {
		return HomeLayout{}, err
	}
	known := make(map[string]bool, len(base.Sections))
	for _, section := range base.Sections {
		known[section.ID] = true
	}
	previous, err := s.homePreference(ctx, scope)
	if err != nil {
		return HomeLayout{}, err
	}
	// Ids saved earlier that this scope cannot see now (a Smart Collection in
	// a restricted Library, saved while revealed) keep their saved setting;
	// they were validated when saved and are filtered again on every read.
	preference := store.SectionOverrideData{
		SectionOrder:     withUnseen(knownIDs(order, known), previous.SectionOrder, known),
		HiddenSectionIDs: withUnseen(knownIDs(hidden, known), previous.HiddenSectionIDs, known),
	}
	if err := s.saveHomePreference(ctx, scope, preference); err != nil {
		return HomeLayout{}, err
	}
	return s.HomeLayout(ctx, scope)
}

// ResetHomeLayout clears the session Profile's Home preference.
func (s *Service) ResetHomeLayout(ctx context.Context, scope auth.Scope) (HomeLayout, error) {
	if scope.ProfileID == "" {
		return HomeLayout{}, errHomeLayoutNoProfile
	}
	if err := s.saveHomePreference(ctx, scope, store.SectionOverrideData{}); err != nil {
		return HomeLayout{}, err
	}
	return s.HomeLayout(ctx, scope)
}

// composedHome is the core Home Layout plus one shelf per Smart Collection in
// the given Libraries, before any override.
func (s *Service) composedHome(ctx context.Context, libraries []store.Library) (Layout, error) {
	home := homeLayout()
	smartSections, err := s.smartCollectionHomeSections(ctx, libraries)
	if err != nil {
		return Layout{}, err
	}
	home.Sections = append(home.Sections, smartSections...)
	return home, nil
}

// profileHomeBase is the Home Layout the Profile receives before its own
// preference: its Libraries' Sections with the server-level override applied.
func (s *Service) profileHomeBase(ctx context.Context, scope auth.Scope) (Layout, error) {
	libraries, err := s.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return Layout{}, err
	}
	home, err := s.composedHome(ctx, libraries)
	if err != nil {
		return Layout{}, err
	}
	override, err := s.store.NavOverride(ctx, "server", nil)
	if err != nil {
		return Layout{}, err
	}
	return applyLayoutOverride([]Layout{home}, override)[0], nil
}

// homePreference reads the session Profile's saved Home order and hidden set;
// zero when the scope has no Profile or nothing is saved.
func (s *Service) homePreference(ctx context.Context, scope auth.Scope) (store.SectionOverrideData, error) {
	if scope.ProfileID == "" {
		return store.SectionOverrideData{}, nil
	}
	profileID := string(scope.ProfileID)
	data, err := s.store.NavOverride(ctx, navScopeProfile, &profileID)
	if err != nil {
		return store.SectionOverrideData{}, err
	}
	return data.LayoutSectionOverrides[HomeLayoutID], nil
}

func (s *Service) saveHomePreference(ctx context.Context, scope auth.Scope, preference store.SectionOverrideData) error {
	profileID := string(scope.ProfileID)
	data, err := s.store.NavOverride(ctx, navScopeProfile, &profileID)
	if err != nil {
		return err
	}
	if customized(preference) {
		if data.LayoutSectionOverrides == nil {
			data.LayoutSectionOverrides = map[string]store.SectionOverrideData{}
		}
		data.LayoutSectionOverrides[HomeLayoutID] = preference
	} else {
		delete(data.LayoutSectionOverrides, HomeLayoutID)
	}
	return s.store.SetNavOverride(ctx, navScopeProfile, &profileID, data, s.clock().UTC().Truncate(time.Second))
}

// applyHomePreference orders and filters the Home Layout by the Profile's
// preference. With nothing saved, Continue Watching leads (CORE-2825), so
// every client puts resumable items first whatever the server override says.
func applyHomePreference(home Layout, preference store.SectionOverrideData) Layout {
	if !customized(preference) {
		home.Sections = continueFirst(home.Sections)
		return home
	}
	hidden := idSet(preference.HiddenSectionIDs)
	ordered := orderSections(home.Sections, preference)
	visible := make([]Section, 0, len(ordered))
	for _, section := range ordered {
		if !hidden[section.ID] {
			visible = append(visible, section)
		}
	}
	home.Sections = visible
	return home
}

// orderSections returns the Sections in the preference's order: listed ids
// first, then every unlisted Section in the default order, which leads with
// Continue Watching (a Library or Smart
// Collection added after the preference was saved lands at the end). Listed
// ids naming no Section are ignored. With no saved order the default order
// leads with Continue Watching.
func orderSections(sections []Section, preference store.SectionOverrideData) []Section {
	sections = continueFirst(sections)
	if len(preference.SectionOrder) == 0 {
		return sections
	}
	byID := make(map[string]Section, len(sections))
	for _, section := range sections {
		byID[section.ID] = section
	}
	ordered := make([]Section, 0, len(sections))
	seen := make(map[string]bool, len(sections))
	for _, id := range preference.SectionOrder {
		if section, ok := byID[id]; ok && !seen[id] {
			ordered = append(ordered, section)
			seen[id] = true
		}
	}
	for _, section := range sections {
		if !seen[section.ID] {
			ordered = append(ordered, section)
		}
	}
	return ordered
}

// continueFirst moves the Continue Watching Section to the front.
func continueFirst(sections []Section) []Section {
	for i, section := range sections {
		if section.ID != continueSectionID {
			continue
		}
		if i == 0 {
			return sections
		}
		out := make([]Section, 0, len(sections))
		out = append(out, section)
		out = append(out, sections[:i]...)
		return append(out, sections[i+1:]...)
	}
	return sections
}

func customized(preference store.SectionOverrideData) bool {
	return len(preference.SectionOrder) > 0 || len(preference.HiddenSectionIDs) > 0
}

func idSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// knownIDs keeps the ids in known, first occurrence only, in order.
func knownIDs(ids []string, known map[string]bool) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if known[id] && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}

// withUnseen appends the previously saved ids the current scope does not
// receive, so saving while relocked keeps settings made while revealed. The
// result stays within MaxHomeLayoutIDs.
func withUnseen(ids, previous []string, known map[string]bool) []string {
	seen := idSet(ids)
	for _, id := range previous {
		if len(ids) >= MaxHomeLayoutIDs {
			break
		}
		if !known[id] && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids
}

// LibraryHomeLayoutID is the Layout a Library without a plugin Layout opens
// once its Profile has customized Home: the Home Layout before that
// preference, so ordering or hiding Home shelves never changes a Library page
// (CORE-1972). Without a preference those Libraries keep sharing layout-home.
const LibraryHomeLayoutID = "layout-library"

// splitLibraryHomeLayout points Library nodes that share the Home Layout at a
// copy of it taken before the Profile's preference applies, in the default
// order that leads with Continue Watching.
func splitLibraryHomeLayout(layouts []Layout, navigation []NavigationNode) ([]Layout, []NavigationNode) {
	var home *Layout
	for i := range layouts {
		if layouts[i].ID == HomeLayoutID {
			home = &layouts[i]
		}
	}
	if home == nil {
		return layouts, navigation
	}
	shared := false
	for i := range navigation {
		if navigation[i].LibraryID != "" && navigation[i].LayoutID == HomeLayoutID {
			navigation[i].LayoutID = LibraryHomeLayoutID
			shared = true
		}
	}
	if !shared {
		return layouts, navigation
	}
	library := *home
	library.ID = LibraryHomeLayoutID
	library.Sections = continueFirst(append([]Section(nil), home.Sections...))
	layouts = append(layouts, library)
	sortLayouts(layouts)
	return layouts, navigation
}

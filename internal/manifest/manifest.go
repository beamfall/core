// Package manifest composes the generic Navigation Manifest served to clients.
package manifest

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// Document is the client-facing Navigation Manifest and Layout payload.
type Document struct {
	ProfileID string `json:"profileID"`
	// Revealed mirrors the session's reveal state at evaluation time so clients
	// can apply the hidden-state-only at-rest rule from a property
	// of the document itself, not a racy separate session read.
	Revealed    bool             `json:"revealed"`
	GeneratedAt time.Time        `json:"generatedAt"`
	Libraries   []LibrarySummary `json:"libraries"`
	Navigation  []NavigationNode `json:"navigation"`
	Layouts     []Layout         `json:"layouts"`
	Theme       Theme            `json:"theme"`
}

// LibrarySummary is the top-level Library row clients may render.
type LibrarySummary struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	PluginID     *string `json:"pluginID"`
	PreviewClips bool    `json:"previewClips"`
	// Accent is the bound plugin's Library accent color ("#RRGGBB",
	// ADR-0003 §7 Theme Tokens): clients normalize it into tint/focus
	// variants (artwork-fallback tint, filter selection). Omitted when the
	// Library has no plugin or the plugin declares none, so existing
	// payloads keep their shape.
	Accent string `json:"accent,omitempty"`
}

// NavigationNode is an allowlisted navigation entry. Title is a locale map
// on the wire: English-only entries serialize as the
// plain-string shorthand; entries carrying translations serialize as the
// full map, and clients resolve against the device locale with English as
// the universal fallback.
type NavigationNode struct {
	ID       string              `json:"id"`
	Title    plugin.LocaleString `json:"title"`
	View     string              `json:"view"`
	LayoutID string              `json:"layoutID"`
	// LibraryID scopes a library entry's Sources to that Library; empty on
	// aggregate entries (Home).
	LibraryID string `json:"libraryID,omitempty"`
}

// Layout is an ordered set of Section primitives.
type Layout struct {
	ID       string    `json:"id"`
	Sections []Section `json:"sections"`
	// Role distinguishes the navigation-referenced browse Layout from
	// detail Layouts clients route to when an entity is opened; omitted
	// for the historical browse form so existing payloads keep their shape.
	Role string `json:"role,omitempty"`
	// EntityKind binds a detail Layout to one plugin-declared Entity Kind;
	// omitted on browse Layouts.
	EntityKind string `json:"entityKind,omitempty"`
}

// Section is a compiled client rendering primitive parameterized by data.
// Title carries the same locale-map wire shape as NavigationNode.Title.
type Section struct {
	ID          string              `json:"id"`
	Kind        string              `json:"kind"`
	Title       plugin.LocaleString `json:"title"`
	ArtworkSlot string              `json:"artworkSlot"`
	Source      SourceSpec          `json:"source"`
	// Filters carries a filter-bar Section's declared filter options:
	// selecting one narrows the adjacent Section's Source by equality on
	// the named Media field (clients merge it into that Source's
	// fieldFilters echo). Omitted on every other kind — and on filter-bars
	// declaring none — so existing payloads keep their shape.
	Filters []FilterOption `json:"filters,omitempty"`
	// PlaybackAffordances carries a playback-affordances Section's declared
	// controls (a subset of the fixed vocabulary: "resume", "direct-play",
	// "airplay"). Clients render the listed controls and derive each one's
	// enabled/disabled state from playbackInfo. Omitted on every other kind
	// so existing payloads keep their shape.
	PlaybackAffordances []string `json:"playbackAffordances,omitempty"`
}

// FilterOption is the wire form of one filter-bar affordance: a declared
// text Media field, the value selecting it applies, and the chip's display
// label (locale map; absent label falls back to the value).
type FilterOption struct {
	Field string              `json:"field"`
	Value string              `json:"value"`
	Label plugin.LocaleString `json:"label,omitempty"`
}

// SourceSpec is the bounded data-source vocabulary consumed by a Section.
type SourceSpec struct {
	Kind string `json:"kind"`
	// EntityKind scopes an "entity-items" Source to one plugin-declared
	// Entity Kind; omitted for every other kind.
	EntityKind string `json:"entityKind,omitempty"`
	// FieldFilters narrows a catalog feed by equality/range on plugin-
	// declared Media fields. The wire shape is intentionally minimal and
	// backward-compatible: plain strings stay text equality, numbers and
	// booleans stay typed equality, and range objects use gte/lte.
	// Clients echo it back on sourceItems, like Kind and EntityKind;
	// omitted when the Section declares none, so filterless payloads keep
	// their historical shape.
	FieldFilters map[string]any `json:"fieldFilters,omitempty"`
	// EntityFilter narrows a catalog feed to the Media related to one named
	// entity of a plugin-declared Entity Kind (the declarative per-category
	// shelf). Clients echo it back on sourceItems like FieldFilters; omitted
	// when the Section declares none.
	EntityFilter *EntityFilter `json:"entityFilter,omitempty"`
	// Seed pins a "random" Source's shuffle order: the same seed
	// yields the same order. Clients echo it back on sourceItems like the other
	// Source parameters, and may substitute a fresh seed to reshuffle. Omitted
	// for non-random Sources and for the empty default seed.
	Seed string `json:"seed,omitempty"`
	// Value selects the closed vocabulary entry for a valued Source: a predicate
	// band or directory query shape. Clients echo it back on sourceItems like
	// the other Source parameters; omitted for kinds that take no value.
	Value string `json:"value,omitempty"`
}

// EntityFilter is the wire form of a Source's entity-relation filter: one
// entity named by declared Kind plus stored name.
type EntityFilter struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Theme contains the small style token set controlled by the platform contract.
type Theme struct {
	AccentColor string `json:"accentColor"`
}

// Service serves a manifest view for the current Profile scope.
type Service struct {
	store   *store.Store
	clock   func() time.Time
	plugins *plugin.Registry
}

// NewService creates a manifest service. A nil registry means no plugins.
func NewService(st *store.Store, clock func() time.Time, plugins *plugin.Registry) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{store: st, clock: clock, plugins: plugins}
}

// Document returns the current generic manifest.
func (s *Service) Document(ctx context.Context, scope auth.Scope) (Document, error) {
	libraries, err := s.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return Document{}, err
	}
	librarySummaries := make([]LibrarySummary, len(libraries))
	navigation := []NavigationNode{
		{
			ID:       "home",
			Title:    plugin.LocaleString{"en": "Home", "fr": "Accueil"},
			View:     "layout",
			LayoutID: "layout-home",
		},
		// The Ambient entry: one fixed-vocabulary top-level
		// posture, named by shape, not domain. Its "ambient" View is a compiled
		// client vocabulary value (invariant #3); clients that don't support
		// the Ambient Display posture silently ignore it. It carries no
		// LayoutID — the ambient session assembles its Ambient Queue live from
		// a Source, it does not browse a Layout.
		{
			ID:    "ambient",
			Title: plugin.LocaleString{"en": "Ambient", "fr": "Ambiance"},
			View:  "ambient",
		},
	}
	pluginLayouts := map[string]Layout{}
	for i, library := range libraries {
		summary := LibrarySummary{
			ID:           string(library.ID),
			Title:        library.Title,
			PluginID:     library.PluginID,
			PreviewClips: library.PreviewClips,
		}
		if library.PluginID != nil && s.plugins != nil {
			if bundle, ok := s.plugins.Bundle(plugin.ID(*library.PluginID)); ok {
				summary.Accent = bundle.Accent
				for _, layout := range bundleLayouts(bundle) {
					pluginLayouts[layout.ID] = layout
				}
			}
		}
		librarySummaries[i] = summary
		layoutID := "layout-home"
		if library.PluginID != nil && s.plugins != nil {
			if _, ok := s.plugins.Bundle(plugin.ID(*library.PluginID)); ok {
				layoutID = "layout-" + *library.PluginID
			}
		}
		navigation = append(navigation, NavigationNode{
			ID: "library-" + string(library.ID),
			// A Library title is user data, not a contract display string —
			// it travels as the single-locale shorthand.
			Title:     plugin.L(library.Title),
			View:      "layout",
			LayoutID:  layoutID,
			LibraryID: string(library.ID),
		})
	}
	home := homeLayout()
	smartSections, err := s.smartCollectionHomeSections(ctx, libraries)
	if err != nil {
		return Document{}, err
	}
	home.Sections = append(home.Sections, smartSections...)
	layouts := []Layout{home}
	for _, layout := range pluginLayouts {
		layouts = append(layouts, layout)
	}
	sortLayouts(layouts)

	// Apply server-level Navigation + Layout overrides (WEB-5J / ADR-0061 §Layout editor).
	// A missing or empty override is the zero value — no-op for all steps below.
	override, err := s.store.NavOverride(ctx, "server", nil)
	if err != nil {
		return Document{}, err
	}
	navigation = applyNavOverride(navigation, override)
	layouts = applyLayoutOverride(layouts, override)

	accentColor := "#2F7DFF"
	if override.ThemeAccentColor != "" {
		accentColor = override.ThemeAccentColor
	}

	return Document{
		ProfileID:   string(scope.ProfileID),
		Revealed:    scope.Revealed,
		GeneratedAt: s.clock().UTC(),
		Libraries:   librarySummaries,
		Navigation:  navigation,
		Layouts:     layouts,
		Theme: Theme{
			AccentColor: accentColor,
		},
	}, nil
}

// applyNavOverride filters, reorders, and relabels navigation nodes according
// to the server-level override. Hidden nodes are removed first; then any
// explicit order is applied (unlisted IDs are appended in their current order);
// finally per-node label overrides replace the default locale map.
func applyNavOverride(nodes []NavigationNode, o store.NavOverrideData) []NavigationNode {
	if len(o.HiddenNodeIDs) == 0 && len(o.NodeOrder) == 0 && len(o.NodeLabelOverrides) == 0 {
		return nodes
	}
	// Build hidden set for O(1) lookup.
	hidden := make(map[string]bool, len(o.HiddenNodeIDs))
	for _, id := range o.HiddenNodeIDs {
		hidden[id] = true
	}
	// Filter hidden nodes.
	visible := make([]NavigationNode, 0, len(nodes))
	for _, n := range nodes {
		if !hidden[n.ID] {
			visible = append(visible, n)
		}
	}
	// Apply explicit node order: listed IDs first (in order), then remainder.
	if len(o.NodeOrder) > 0 {
		byID := make(map[string]NavigationNode, len(visible))
		for _, n := range visible {
			byID[n.ID] = n
		}
		ordered := make([]NavigationNode, 0, len(visible))
		seen := make(map[string]bool, len(o.NodeOrder))
		for _, id := range o.NodeOrder {
			if n, ok := byID[id]; ok {
				ordered = append(ordered, n)
				seen[id] = true
			}
		}
		for _, n := range visible {
			if !seen[n.ID] {
				ordered = append(ordered, n)
			}
		}
		visible = ordered
	}
	// Apply per-node label overrides.
	if len(o.NodeLabelOverrides) > 0 {
		for i, n := range visible {
			if localeMap, ok := o.NodeLabelOverrides[n.ID]; ok && len(localeMap) > 0 {
				lm := make(plugin.LocaleString, len(localeMap))
				for lang, label := range localeMap {
					lm[lang] = label
				}
				visible[i].Title = lm
			}
		}
	}
	return visible
}

// applyLayoutOverride applies per-layout section visibility and order overrides.
func applyLayoutOverride(layouts []Layout, o store.NavOverrideData) []Layout {
	if len(o.LayoutSectionOverrides) == 0 {
		return layouts
	}
	out := make([]Layout, len(layouts))
	for i, layout := range layouts {
		sec, ok := o.LayoutSectionOverrides[layout.ID]
		if !ok || (len(sec.HiddenSectionIDs) == 0 && len(sec.SectionOrder) == 0) {
			out[i] = layout
			continue
		}
		hidden := make(map[string]bool, len(sec.HiddenSectionIDs))
		for _, id := range sec.HiddenSectionIDs {
			hidden[id] = true
		}
		visible := make([]Section, 0, len(layout.Sections))
		for _, s := range layout.Sections {
			if !hidden[s.ID] {
				visible = append(visible, s)
			}
		}
		if len(sec.SectionOrder) > 0 {
			byID := make(map[string]Section, len(visible))
			for _, s := range visible {
				byID[s.ID] = s
			}
			ordered := make([]Section, 0, len(visible))
			seen := make(map[string]bool, len(sec.SectionOrder))
			for _, id := range sec.SectionOrder {
				if s, ok := byID[id]; ok {
					ordered = append(ordered, s)
					seen[id] = true
				}
			}
			for _, s := range visible {
				if !seen[s.ID] {
					ordered = append(ordered, s)
				}
			}
			visible = ordered
		}
		layout.Sections = visible
		out[i] = layout
	}
	return out
}

// homeLayout is the generic core layout for Libraries with no plugin bound.
func homeLayout() Layout {
	return Layout{
		ID: "layout-home",
		Sections: []Section{
			{
				ID:          "continue",
				Kind:        "shelf",
				Title:       plugin.LocaleString{"en": "Continue Watching", "fr": "Reprendre la lecture"},
				ArtworkSlot: "poster",
				Source:      SourceSpec{Kind: "continue-watching"},
			},
			{
				ID:          "recent",
				Kind:        "shelf",
				Title:       plugin.LocaleString{"en": "Recently Added", "fr": "Ajouts récents"},
				ArtworkSlot: "poster",
				Source:      SourceSpec{Kind: "recently-added"},
			},
			{
				ID:          "your-most-played",
				Kind:        "shelf",
				Title:       plugin.LocaleString{"en": "Your Most-Played", "fr": "Vos plus lus"},
				ArtworkSlot: "poster",
				Source:      SourceSpec{Kind: "most-played"},
			},
		},
	}
}

func (s *Service) smartCollectionHomeSections(ctx context.Context, libraries []store.Library) ([]Section, error) {
	var sections []Section
	for _, library := range libraries {
		collections, err := s.store.SmartCollections(ctx, library.ID)
		if err != nil {
			return nil, err
		}
		for _, collection := range collections {
			sections = append(sections, Section{
				ID:          "smart-" + string(collection.ID),
				Kind:        "shelf",
				Title:       plugin.L(collection.Name),
				ArtworkSlot: "poster",
				Source:      SourceSpec{Kind: "smart-collection", Value: string(collection.ID)},
			})
		}
	}
	return sections, nil
}

// bundleLayouts converts a bundle's declared Layouts into wire Layouts.
// The browse Layout keeps the historical "layout-<plugin>" wire ID library
// navigation entries reference; additional Layouts are emitted under the
// stable "layout-<plugin>-<layoutID>" form — unreferenced by navigation,
// addressable for detail views, and silently ignorable by clients that
// don't use them. Validation already happened at activation; conversion is
// mechanical.
func bundleLayouts(bundle plugin.Bundle) []Layout {
	layouts := make([]Layout, 0, len(bundle.Layouts))
	for _, spec := range bundle.Layouts {
		id := "layout-" + string(bundle.ID)
		layout := Layout{ID: id}
		if spec.Role != "browse" {
			layout.ID += "-" + spec.ID
			layout.Role = spec.Role
			layout.EntityKind = spec.EntityKind
		}
		for _, section := range spec.Sections {
			layout.Sections = append(layout.Sections, Section{
				ID:          section.ID,
				Kind:        section.Kind,
				Title:       section.Title,
				ArtworkSlot: section.ArtworkSlot,
				Source: SourceSpec{
					Kind:         section.Source.Kind,
					EntityKind:   section.Source.EntityKind,
					FieldFilters: section.Source.FieldFilters,
					EntityFilter: wireEntityFilter(section.Source.EntityFilter),
					Seed:         section.Source.Seed,
					Value:        section.Source.Value,
				},
				Filters:             wireFilterOptions(section.FilterOptions),
				PlaybackAffordances: section.PlaybackAffordances,
			})
		}
		layouts = append(layouts, layout)
	}
	return layouts
}

// wireFilterOptions converts a filter-bar's declared options to wire form;
// an empty declaration stays nil so optionless Sections keep their
// historical shape.
func wireFilterOptions(options []plugin.FilterOption) []FilterOption {
	if len(options) == 0 {
		return nil
	}
	wire := make([]FilterOption, len(options))
	for i, option := range options {
		wire[i] = FilterOption{Field: option.Field, Value: option.Value, Label: option.Label}
	}
	return wire
}

// wireEntityFilter converts a declared entity filter to its wire form; nil
// stays nil so filterless Sources keep their historical shape.
func wireEntityFilter(filter *plugin.EntityFilter) *EntityFilter {
	if filter == nil {
		return nil
	}
	return &EntityFilter{Kind: filter.Kind, Name: filter.Name}
}

// sortLayouts keeps layout order deterministic: layout-home first, then by ID.
func sortLayouts(layouts []Layout) {
	for i := 1; i < len(layouts); i++ {
		for j := i; j > 0 && layoutLess(layouts[j], layouts[j-1]); j-- {
			layouts[j], layouts[j-1] = layouts[j-1], layouts[j]
		}
	}
}

func layoutLess(a, b Layout) bool {
	if a.ID == "layout-home" {
		return true
	}
	if b.ID == "layout-home" {
		return false
	}
	return a.ID < b.ID
}

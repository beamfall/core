package manifest

import (
	"context"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// EffectiveHomeConfig is the resolved Home configuration for a Profile (or template).
type EffectiveHomeConfig struct {
	Customized       bool             `json:"customized"`
	HasOwnerTemplate bool             `json:"hasOwnerTemplate"`
	Config           store.HomeConfig `json:"config"`
}

// EffectiveHomeConfig resolves the effective HomeConfig for scope:
// 1. Profile's saved home_config (if any)
// 2. Profile's legacy nav_override layout-home preference (if any)
// 3. Server default template (if any)
// 4. Built-in default + accessible Smart Collections
func (s *Service) EffectiveHomeConfig(ctx context.Context, scope auth.Scope) (EffectiveHomeConfig, error) {
	if scope.ProfileID != "" {
		if cfg, ok, err := s.store.HomeConfig(ctx, store.HomeConfigScopeProfile, string(scope.ProfileID)); err != nil {
			return EffectiveHomeConfig{}, err
		} else if ok {
			_, hasTmpl, err := s.store.HomeConfig(ctx, store.HomeConfigScopeServerTemplate, "")
			if err != nil {
				return EffectiveHomeConfig{}, err
			}
			return EffectiveHomeConfig{
				Customized:       true,
				HasOwnerTemplate: hasTmpl,
				Config:           cfg,
			}, nil
		}
		pref, err := s.homePreference(ctx, scope)
		if err != nil {
			return EffectiveHomeConfig{}, err
		}
		if customized(pref) {
			baseCfg, err := s.defaultHomeConfigWithSmart(ctx, scope)
			if err != nil {
				return EffectiveHomeConfig{}, err
			}
			_, hasTmpl, err := s.store.HomeConfig(ctx, store.HomeConfigScopeServerTemplate, "")
			if err != nil {
				return EffectiveHomeConfig{}, err
			}
			return EffectiveHomeConfig{
				Customized:       true,
				HasOwnerTemplate: hasTmpl,
				Config:           projectLegacyPrefToHomeConfig(baseCfg, pref),
			}, nil
		}
	}

	if tmpl, ok, err := s.store.HomeConfig(ctx, store.HomeConfigScopeServerTemplate, ""); err != nil {
		return EffectiveHomeConfig{}, err
	} else if ok {
		return EffectiveHomeConfig{
			Customized:       false,
			HasOwnerTemplate: true,
			Config:           tmpl,
		}, nil
	}

	baseCfg, err := s.defaultHomeConfigWithSmart(ctx, scope)
	if err != nil {
		return EffectiveHomeConfig{}, err
	}
	return EffectiveHomeConfig{
		Customized:       false,
		HasOwnerTemplate: false,
		Config:           baseCfg,
	}, nil
}

// SaveHomeConfig saves a Profile's HomeConfig, preserving any previously saved
// shelves that are exclusively scoped to Libraries the caller cannot currently
// see (e.g. a restricted Library while relocked).
func (s *Service) SaveHomeConfig(ctx context.Context, scope auth.Scope, cfg store.HomeConfig) (EffectiveHomeConfig, error) {
	if scope.ProfileID == "" {
		return EffectiveHomeConfig{}, errHomeLayoutNoProfile
	}
	if err := store.ValidateAndNormalizeHomeConfig(&cfg); err != nil {
		return EffectiveHomeConfig{}, err
	}
	accessible, err := s.accessibleLibrarySet(ctx, scope)
	if err != nil {
		return EffectiveHomeConfig{}, err
	}
	// Preserve shelves from previous config that are exclusively scoped to
	// currently unseen Libraries and not already present in the incoming config.
	if prev, ok, err := s.store.HomeConfig(ctx, store.HomeConfigScopeProfile, string(scope.ProfileID)); err == nil && ok {
		incomingIDs := make(map[string]bool, len(cfg.Shelves))
		for _, sh := range cfg.Shelves {
			incomingIDs[sh.ID] = true
		}
		for _, sh := range prev.Shelves {
			if !incomingIDs[sh.ID] && !shelfAccessible(sh, accessible) && len(cfg.Shelves) < store.MaxHomeShelves {
				cfg.Shelves = append(cfg.Shelves, sh)
			}
		}
	}
	if err := s.store.SetHomeConfig(ctx, store.HomeConfigScopeProfile, string(scope.ProfileID), cfg, s.clock().UTC().Truncate(time.Second)); err != nil {
		return EffectiveHomeConfig{}, err
	}
	return s.EffectiveHomeConfig(ctx, scope)
}

// ResetHomeConfig deletes a Profile's saved HomeConfig and legacy Home preference.
func (s *Service) ResetHomeConfig(ctx context.Context, scope auth.Scope) (EffectiveHomeConfig, error) {
	if scope.ProfileID == "" {
		return EffectiveHomeConfig{}, errHomeLayoutNoProfile
	}
	if err := s.store.DeleteHomeConfig(ctx, store.HomeConfigScopeProfile, string(scope.ProfileID)); err != nil {
		return EffectiveHomeConfig{}, err
	}
	_ = s.saveHomePreference(ctx, scope, store.SectionOverrideData{})
	return s.EffectiveHomeConfig(ctx, scope)
}

// SaveDefaultHomeTemplate saves the server-wide default HomeConfig template.
func (s *Service) SaveDefaultHomeTemplate(ctx context.Context, cfg store.HomeConfig) (store.HomeConfig, error) {
	if err := store.ValidateAndNormalizeHomeConfig(&cfg); err != nil {
		return store.HomeConfig{}, err
	}
	if err := s.store.SetHomeConfig(ctx, store.HomeConfigScopeServerTemplate, "", cfg, s.clock().UTC().Truncate(time.Second)); err != nil {
		return store.HomeConfig{}, err
	}
	saved, _, err := s.store.HomeConfig(ctx, store.HomeConfigScopeServerTemplate, "")
	return saved, err
}

// ResetDefaultHomeTemplate deletes the server-wide default HomeConfig template.
func (s *Service) ResetDefaultHomeTemplate(ctx context.Context) error {
	return s.store.DeleteHomeConfig(ctx, store.HomeConfigScopeServerTemplate, "")
}

// FilterHomeConfigForScope returns a copy of cfg with shelves restricted to
// Libraries accessible in scope.
func (s *Service) FilterHomeConfigForScope(ctx context.Context, scope auth.Scope, cfg store.HomeConfig) (store.HomeConfig, error) {
	accessible, err := s.accessibleLibrarySet(ctx, scope)
	if err != nil {
		return store.HomeConfig{}, err
	}
	out := cfg
	out.Shelves = make([]store.HomeShelf, 0, len(cfg.Shelves))
	for _, sh := range cfg.Shelves {
		if shelfAccessible(sh, accessible) {
			out.Shelves = append(out.Shelves, sh)
		}
	}
	return out, nil
}

func (s *Service) accessibleLibrarySet(ctx context.Context, scope auth.Scope) (map[store.LibraryID]bool, error) {
	libs, err := s.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	set := make(map[store.LibraryID]bool, len(libs))
	for _, lib := range libs {
		set[lib.ID] = true
	}
	return set, nil
}

func shelfAccessible(sh store.HomeShelf, accessible map[store.LibraryID]bool) bool {
	if len(sh.Source.LibraryIDs) == 0 {
		return true
	}
	for _, id := range sh.Source.LibraryIDs {
		if accessible[id] {
			return true
		}
	}
	return false
}

func (s *Service) defaultHomeConfigWithSmart(ctx context.Context, scope auth.Scope) (store.HomeConfig, error) {
	cfg := store.DefaultHomeConfig()
	libs, err := s.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return store.HomeConfig{}, err
	}
	for _, lib := range libs {
		cols, err := s.store.SmartCollections(ctx, lib.ID)
		if err != nil {
			return store.HomeConfig{}, err
		}
		for _, col := range cols {
			cfg.Shelves = append(cfg.Shelves, store.HomeShelf{
				ID:            "smart-" + string(col.ID),
				Title:         col.Name,
				HideWhenEmpty: true,
				Display:       store.HomeShelfDisplay{Style: "shelf", ArtworkSlot: "auto", Limit: 20},
				Source: store.HomeShelfSource{
					Type:         "collection",
					CollectionID: string(col.ID),
					LibraryIDs:   []store.LibraryID{lib.ID},
				},
			})
		}
	}
	return cfg, nil
}

func projectLegacyPrefToHomeConfig(base store.HomeConfig, pref store.SectionOverrideData) store.HomeConfig {
	hidden := idSet(pref.HiddenSectionIDs)
	byID := make(map[string]store.HomeShelf, len(base.Shelves))
	for _, sh := range base.Shelves {
		byID[sh.ID] = sh
	}
	ordered := make([]store.HomeShelf, 0, len(base.Shelves))
	seen := make(map[string]bool, len(base.Shelves))
	for _, id := range pref.SectionOrder {
		if sh, ok := byID[id]; ok && !seen[id] {
			sh.Hidden = hidden[id]
			ordered = append(ordered, sh)
			seen[id] = true
		}
	}
	for _, sh := range base.Shelves {
		if !seen[sh.ID] {
			sh.Hidden = hidden[sh.ID]
			ordered = append(ordered, sh)
		}
	}
	base.Shelves = ordered
	return base
}

// compileHomeConfigLayout projects a resolved HomeConfig into the wire Layout
// for layout-home, filtering out hidden shelves and inaccessible Library scopes.
func (s *Service) compileHomeConfigLayout(ctx context.Context, scope auth.Scope, cfg store.HomeConfig) (Layout, error) {
	filtered, err := s.FilterHomeConfigForScope(ctx, scope, cfg)
	if err != nil {
		return Layout{}, err
	}
	layout := Layout{
		ID:               HomeLayoutID,
		SuppressAutoHero: !filtered.Hero.Enabled,
	}
	if filtered.Hero.Enabled && filtered.Hero.Mode == "shelf" && filtered.Hero.Source != nil {
		layout.Sections = append(layout.Sections, Section{
			ID:          "home-hero",
			Kind:        "hero",
			Title:       plugin.L("Featured"),
			ArtworkSlot: "landscape",
			Source:      SourceSpec{Kind: "home-shelf", Value: "hero"},
		})
	}
	for _, sh := range filtered.Shelves {
		if sh.Hidden {
			continue
		}
		slot := sh.Display.ArtworkSlot
		if slot == "" || slot == "auto" {
			slot = "poster"
		}
		style := sh.Display.Style
		if style == "" {
			style = "shelf"
		}
		title := strings.TrimSpace(sh.Title)
		if title == "" {
			title = "Shelf"
		}
		src := SourceSpec{Kind: "home-shelf", Value: sh.ID}
		if sh.Source.Type == "builtin" && len(sh.Source.LibraryIDs) == 0 && sh.Source.Rules == nil && style == "shelf" && sh.Display.ArtworkSlot == "auto" {
			switch sh.Source.BuiltinKind {
			case "next-up":
				src = SourceSpec{Kind: "next-up", Value: "video"}
			case "continue-listening":
				src = SourceSpec{Kind: "next-up", Value: "audio"}
			default:
				src = SourceSpec{Kind: sh.Source.BuiltinKind}
			}
		}
		layout.Sections = append(layout.Sections, Section{
			ID:          sh.ID,
			Kind:        style,
			Title:       plugin.L(title),
			ArtworkSlot: slot,
			Source:      src,
		})
	}
	return layout, nil
}

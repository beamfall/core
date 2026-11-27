package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// HomeConfigInfo is the GraphQL payload for homeConfig and saveHomeConfig.
type HomeConfigInfo struct {
	Customized       bool                 `json:"customized"`
	HasOwnerTemplate bool                 `json:"hasOwnerTemplate"`
	CanEditTemplate  bool                 `json:"canEditTemplate"`
	Hero             store.HomeHeroConfig `json:"hero"`
	Shelves          []store.HomeShelf    `json:"shelves"`
}

// HomeFilterSchemaInfo describes the dynamic filter vocabulary and pinnable
// targets available for the caller's accessible Libraries.
type HomeFilterSchemaInfo struct {
	Libraries []HomeSchemaLibrary `json:"libraries"`
	Fields    []HomeFilterField   `json:"fields"`
	Pinnables HomeSchemaPinnables `json:"pinnables"`
}

type HomeSchemaLibrary struct {
	ID           string                  `json:"id"`
	Title        string                  `json:"title"`
	PluginID     *string                 `json:"pluginID,omitempty"`
	Icon         string                  `json:"icon,omitempty"`
	Accent       string                  `json:"accent,omitempty"`
	MediaClasses []string                `json:"mediaClasses,omitempty"`
	Presentation *PluginPresentationInfo `json:"presentation,omitempty"`
	Capabilities []string                `json:"capabilities,omitempty"`
}

type HomeFilterField struct {
	Key         string              `json:"key"`
	Label       plugin.LocaleString `json:"label"`
	ValueType   string              `json:"valueType"`
	Group       string              `json:"group"`
	Operators   []string            `json:"operators"`
	Suggestions []string            `json:"suggestions,omitempty"`
}

type HomeSchemaPinnables struct {
	Collections      []HomePinnableCollection  `json:"collections"`
	SmartCollections []HomePinnableSmart       `json:"smartCollections"`
	SavedFilters     []HomePinnableSavedFilter `json:"savedFilters"`
}

type HomePinnableCollection struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	LibraryID string `json:"libraryID"`
}

type HomePinnableSmart struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	LibraryID string `json:"libraryID"`
}

type HomePinnableSavedFilter struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	LibraryID string `json:"libraryID"`
}

// HomeShelfPreviewInfo is the live preview response for previewHomeShelf.
type HomeShelfPreviewInfo struct {
	TotalCount int          `json:"totalCount"`
	Items      []SourceItem `json:"items"`
}

func canManageHomeTemplate(scope auth.Scope) bool {
	return scope.DeviceRole == store.DeviceRoleManager || scope.APITokenID != ""
}

func (h *Handler) homeConfig(ctx context.Context, scope auth.Scope, req request) (HomeConfigInfo, error) {
	if h.manifest == nil || h.store == nil {
		return HomeConfigInfo{}, nil
	}
	wantTemplate := boolVar(req, "template")
	if wantTemplate {
		if !canManageHomeTemplate(scope) {
			return HomeConfigInfo{}, clientError("manager access required to edit default Home template")
		}
		tmpl, ok, err := h.store.HomeConfig(ctx, store.HomeConfigScopeServerTemplate, "")
		if err != nil {
			return HomeConfigInfo{}, err
		}
		if !ok {
			tmpl = store.DefaultHomeConfig()
		}
		return HomeConfigInfo{
			Customized:       ok,
			HasOwnerTemplate: ok,
			CanEditTemplate:  true,
			Hero:             tmpl.Hero,
			Shelves:          tmpl.Shelves,
		}, nil
	}
	eff, err := h.manifest.EffectiveHomeConfig(ctx, scope)
	if err != nil {
		return HomeConfigInfo{}, err
	}
	filtered, err := h.manifest.FilterHomeConfigForScope(ctx, scope, eff.Config)
	if err != nil {
		return HomeConfigInfo{}, err
	}
	return HomeConfigInfo{
		Customized:       eff.Customized,
		HasOwnerTemplate: eff.HasOwnerTemplate,
		CanEditTemplate:  canManageHomeTemplate(scope),
		Hero:             filtered.Hero,
		Shelves:          filtered.Shelves,
	}, nil
}

func (h *Handler) saveHomeConfig(ctx context.Context, scope auth.Scope, req request) (HomeConfigInfo, error) {
	var cfg store.HomeConfig
	if err := decodeGraphInput(req.Variables["config"], &cfg); err != nil {
		return HomeConfigInfo{}, clientError("invalid config: " + err.Error())
	}
	eff, err := h.manifest.SaveHomeConfig(ctx, scope, cfg)
	if err != nil {
		return HomeConfigInfo{}, err
	}
	filtered, err := h.manifest.FilterHomeConfigForScope(ctx, scope, eff.Config)
	if err != nil {
		return HomeConfigInfo{}, err
	}
	return HomeConfigInfo{
		Customized:       eff.Customized,
		HasOwnerTemplate: eff.HasOwnerTemplate,
		CanEditTemplate:  canManageHomeTemplate(scope),
		Hero:             filtered.Hero,
		Shelves:          filtered.Shelves,
	}, nil
}

func (h *Handler) resetHomeConfig(ctx context.Context, scope auth.Scope) (HomeConfigInfo, error) {
	eff, err := h.manifest.ResetHomeConfig(ctx, scope)
	if err != nil {
		return HomeConfigInfo{}, err
	}
	filtered, err := h.manifest.FilterHomeConfigForScope(ctx, scope, eff.Config)
	if err != nil {
		return HomeConfigInfo{}, err
	}
	return HomeConfigInfo{
		Customized:       eff.Customized,
		HasOwnerTemplate: eff.HasOwnerTemplate,
		CanEditTemplate:  canManageHomeTemplate(scope),
		Hero:             filtered.Hero,
		Shelves:          filtered.Shelves,
	}, nil
}

func (h *Handler) saveDefaultHomeTemplate(ctx context.Context, scope auth.Scope, req request) (HomeConfigInfo, error) {
	if !canManageHomeTemplate(scope) {
		return HomeConfigInfo{}, clientError("manager access required")
	}
	var cfg store.HomeConfig
	if err := decodeGraphInput(req.Variables["config"], &cfg); err != nil {
		return HomeConfigInfo{}, clientError("invalid config: " + err.Error())
	}
	saved, err := h.manifest.SaveDefaultHomeTemplate(ctx, cfg)
	if err != nil {
		return HomeConfigInfo{}, err
	}
	return HomeConfigInfo{
		Customized:       true,
		HasOwnerTemplate: true,
		CanEditTemplate:  true,
		Hero:             saved.Hero,
		Shelves:          saved.Shelves,
	}, nil
}

func (h *Handler) resetDefaultHomeTemplate(ctx context.Context, scope auth.Scope) (HomeConfigInfo, error) {
	if !canManageHomeTemplate(scope) {
		return HomeConfigInfo{}, clientError("manager access required")
	}
	if err := h.manifest.ResetDefaultHomeTemplate(ctx); err != nil {
		return HomeConfigInfo{}, err
	}
	def := store.DefaultHomeConfig()
	return HomeConfigInfo{
		Customized:       false,
		HasOwnerTemplate: false,
		CanEditTemplate:  true,
		Hero:             def.Hero,
		Shelves:          def.Shelves,
	}, nil
}

func (h *Handler) previewHomeShelf(ctx context.Context, scope auth.Scope, req request) (HomeShelfPreviewInfo, error) {
	if h.store == nil {
		return HomeShelfPreviewInfo{Items: []SourceItem{}}, nil
	}
	var shelf store.HomeShelf
	if err := decodeGraphInput(req.Variables["shelf"], &shelf); err != nil {
		return HomeShelfPreviewInfo{}, clientError("invalid shelf: " + err.Error())
	}
	if err := store.ValidateAndNormalizeHomeShelf(&shelf); err != nil {
		return HomeShelfPreviewInfo{}, err
	}
	limit := boundedIntVar(req, "limit", 12, 24)
	items, total, err := h.evaluateHomeShelf(ctx, scope, scope.LibraryFilter(), shelf, limit, 0)
	if err != nil {
		return HomeShelfPreviewInfo{}, err
	}
	if err := h.store.AnnotateMuted(ctx, scope.ProfileID, scope.LibraryFilter(), items); err != nil {
		return HomeShelfPreviewInfo{}, err
	}
	if err := h.annotateCards(ctx, scope, items); err != nil {
		return HomeShelfPreviewInfo{}, err
	}
	resp := make([]SourceItem, len(items))
	for i, it := range items {
		resp[i] = h.sourceItem(it, scope.Revealed)
	}
	return HomeShelfPreviewInfo{TotalCount: total, Items: resp}, nil
}

func (h *Handler) homeFilterSchema(ctx context.Context, scope auth.Scope, req request) (HomeFilterSchemaInfo, error) {
	out := HomeFilterSchemaInfo{
		Libraries: []HomeSchemaLibrary{},
		Fields:    builtinHomeFilterFields(),
		Pinnables: HomeSchemaPinnables{
			Collections:      []HomePinnableCollection{},
			SmartCollections: []HomePinnableSmart{},
			SavedFilters:     []HomePinnableSavedFilter{},
		},
	}
	if h.store == nil {
		return out, nil
	}
	libs, err := h.store.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		return out, err
	}
	selectedSet := map[store.LibraryID]bool{}
	if rawIDs, ok := req.Variables["libraryIDs"].([]any); ok {
		for _, raw := range rawIDs {
			if id, ok := raw.(string); ok && strings.TrimSpace(id) != "" {
				selectedSet[store.LibraryID(strings.TrimSpace(id))] = true
			}
		}
	}

	seenField := map[string]bool{}
	for _, f := range out.Fields {
		seenField[f.Key] = true
	}

	for _, lib := range libs {
		mediaClasses, presentation, capabilities, accent := h.libraryBundleMetadata(lib.PluginID)
		icon := lib.Icon
		if icon == "" {
			icon, _ = h.libraryIconBasis(lib)
		}
		out.Libraries = append(out.Libraries, HomeSchemaLibrary{
			ID:           string(lib.ID),
			Title:        lib.Title,
			PluginID:     lib.PluginID,
			Icon:         icon,
			Accent:       accent,
			MediaClasses: mediaClasses,
			Presentation: presentation,
			Capabilities: capabilities,
		})

		// Collections, Smart Collections, Saved Filters per accessible Library.
		if cols, err := h.store.Collections(ctx, lib.ID, false); err == nil {
			for _, c := range cols {
				out.Pinnables.Collections = append(out.Pinnables.Collections, HomePinnableCollection{
					ID:        string(c.ID),
					Name:      c.Name,
					LibraryID: string(lib.ID),
				})
			}
		}
		if smarts, err := h.store.SmartCollections(ctx, lib.ID); err == nil {
			for _, sc := range smarts {
				out.Pinnables.SmartCollections = append(out.Pinnables.SmartCollections, HomePinnableSmart{
					ID:        string(sc.ID),
					Name:      sc.Name,
					LibraryID: string(lib.ID),
				})
			}
		}
		if scope.ProfileID != "" {
			if filters, err := h.store.SavedFilters(ctx, scope.ProfileID, lib.ID); err == nil {
				for _, sf := range filters {
					out.Pinnables.SavedFilters = append(out.Pinnables.SavedFilters, HomePinnableSavedFilter{
						ID:        string(sf.ID),
						Name:      sf.Name,
						LibraryID: string(lib.ID),
					})
				}
			}
		}

		// Filter vocabulary: scope to selectedSet if provided, else all accessible.
		if len(selectedSet) > 0 && !selectedSet[lib.ID] {
			continue
		}
		if lib.PluginID != nil && h.plugins != nil {
			if bundle, ok := h.plugins.Bundle(plugin.ID(*lib.PluginID)); ok {
				for _, mf := range bundle.MediaFields {
					key := "field:" + mf.Name
					if seenField[key] {
						continue
					}
					seenField[key] = true
					vt, ops := mapPluginFieldType(mf.Type)
					out.Fields = append(out.Fields, HomeFilterField{
						Key:       key,
						Label:     mf.Label,
						ValueType: vt,
						Group:     "metadata",
						Operators: ops,
					})
				}
				for _, ek := range bundle.EntityKinds {
					key := "entity:" + ek.Name
					if seenField[key] {
						continue
					}
					seenField[key] = true
					label := ek.DisplayName
					if len(label) == 0 {
						label = plugin.L(ek.Name)
					}
					suggestions := h.entityFieldSuggestions(ctx, scope, selectedSet, ek.Name)
					out.Fields = append(out.Fields, HomeFilterField{
						Key:         key,
						Label:       label,
						ValueType:   "entity",
						Group:       "entities",
						Operators:   []string{"is", "isNot", "in", "notIn", "contains", "notContains"},
						Suggestions: suggestions,
					})
				}
			}
		}
	}
	return out, nil
}

func (h *Handler) entityFieldSuggestions(ctx context.Context, scope auth.Scope, selectedSet map[store.LibraryID]bool, entityKind string) []string {
	if h.store == nil {
		return nil
	}
	filter := scope.LibraryFilter()
	if len(selectedSet) > 0 {
		ids := make([]store.LibraryID, 0, len(selectedSet))
		for id := range selectedSet {
			ids = append(ids, id)
		}
		var ok bool
		filter, ok = intersectLibraryFilter(filter, ids)
		if !ok {
			return nil
		}
	}
	items, err := h.store.SourceItems(ctx, filter, store.SourceQuery{
		Kind:       "entity-items",
		EntityKind: entityKind,
		Limit:      30,
		ProfileID:  scope.ProfileID,
	})
	if err != nil || len(items) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, it := range items {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			continue
		}
		lower := strings.ToLower(title)
		if !seen[lower] {
			seen[lower] = true
			out = append(out, title)
		}
	}
	return out
}

func builtinHomeFilterFields() []HomeFilterField {
	textOps := []string{"is", "isNot", "contains", "notContains", "beginsWith", "in", "notIn"}
	numOps := []string{"is", "isNot", "gt", "gte", "lt", "lte", "between"}
	dateOps := []string{"inLast", "notInLast", "before", "after"}
	boolOps := []string{"isTrue", "isFalse"}
	return []HomeFilterField{
		{Key: "title", Label: plugin.L("Title"), ValueType: "text", Group: "general", Operators: textOps},
		{Key: "year", Label: plugin.L("Year"), ValueType: "number", Group: "general", Operators: numOps},
		{Key: "duration", Label: plugin.L("Duration (seconds)"), ValueType: "number", Group: "general", Operators: numOps},
		{Key: "addedAt", Label: plugin.L("Date Added"), ValueType: "date", Group: "general", Operators: dateOps},
		{Key: "watched", Label: plugin.L("Watched"), ValueType: "boolean", Group: "activity", Operators: boolOps},
		{Key: "inProgress", Label: plugin.L("In Progress"), ValueType: "boolean", Group: "activity", Operators: boolOps},
		{Key: "favorited", Label: plugin.L("Favorited"), ValueType: "boolean", Group: "activity", Operators: boolOps},
		{Key: "inWatchlist", Label: plugin.L("In Watchlist"), ValueType: "boolean", Group: "activity", Operators: boolOps},
		{Key: "playCount", Label: plugin.L("Play Count"), ValueType: "number", Group: "activity", Operators: numOps},
		{Key: "rating", Label: plugin.L("My Rating (1-10)"), ValueType: "number", Group: "activity", Operators: numOps},
		{Key: "lastPlayed", Label: plugin.L("Last Played"), ValueType: "date", Group: "activity", Operators: dateOps},
	}
}

func mapPluginFieldType(t string) (string, []string) {
	switch t {
	case "number", "int", "real":
		return "number", []string{"is", "isNot", "gt", "gte", "lt", "lte", "between"}
	case "date":
		return "date", []string{"inLast", "notInLast", "before", "after"}
	case "boolean", "bool":
		return "boolean", []string{"isTrue", "isFalse"}
	default:
		return "text", []string{"is", "isNot", "contains", "notContains", "beginsWith", "in", "notIn"}
	}
}

// sourceItemsHomeShelf hydrates a manifest Section with source.kind == "home-shelf".
func (h *Handler) sourceItemsHomeShelf(ctx context.Context, scope auth.Scope, baseFilter store.LibraryFilter, shelfID string, limit, offset int) ([]store.SourceItem, error) {
	if h.manifest == nil || h.store == nil {
		return []store.SourceItem{}, nil
	}
	eff, err := h.manifest.EffectiveHomeConfig(ctx, scope)
	if err != nil {
		return nil, err
	}
	if shelfID == "hero" {
		if !eff.Config.Hero.Enabled || eff.Config.Hero.Mode != "shelf" || eff.Config.Hero.Source == nil {
			return []store.SourceItem{}, nil
		}
		heroShelf := store.HomeShelf{
			ID:      "hero",
			Display: store.HomeShelfDisplay{Style: "shelf", Limit: 1},
			Source:  *eff.Config.Hero.Source,
		}
		items, _, err := h.evaluateHomeShelf(ctx, scope, baseFilter, heroShelf, 1, 0)
		return items, err
	}
	for _, sh := range eff.Config.Shelves {
		if sh.ID == shelfID {
			if sh.Display.Limit > 0 {
				if offset == 0 || limit <= 0 || limit > sh.Display.Limit {
					limit = min(sh.Display.Limit, store.MaxHomeShelfLimit)
				}
			}
			items, _, err := h.evaluateHomeShelf(ctx, scope, baseFilter, sh, limit, offset)
			return items, err
		}
	}
	return []store.SourceItem{}, nil
}

func (h *Handler) evaluateHomeShelf(ctx context.Context, scope auth.Scope, filter store.LibraryFilter, shelf store.HomeShelf, limit, offset int) ([]store.SourceItem, int, error) {
	if limit <= 0 {
		limit = shelf.Display.Limit
		if limit <= 0 {
			limit = 20
		}
	}
	if offset < 0 {
		offset = 0
	}
	if !homeShelfShouldRollUp(shelf) {
		return h.evaluateHomeShelfRaw(ctx, scope, filter, shelf, limit, offset)
	}
	fetchLimit := min(max((offset+limit)*5, 100), 500)
	raw, rawTotal, err := h.evaluateHomeShelfRaw(ctx, scope, filter, shelf, fetchLimit, 0)
	if err != nil {
		return nil, 0, err
	}
	var contextID store.EntityID
	if shelf.Source.Type == "entity" && shelf.Source.EntityID != "" {
		contextID = store.EntityID(shelf.Source.EntityID)
	}
	rolled, err := h.rollUpContainers(ctx, filter, contextID, raw)
	if err != nil {
		return nil, 0, err
	}
	total := len(rolled)
	if rawTotal > len(raw) {
		total = rawTotal
	}
	if offset >= len(rolled) {
		return []store.SourceItem{}, total, nil
	}
	end := min(offset+limit, len(rolled))
	return rolled[offset:end], total, nil
}

func homeShelfShouldRollUp(shelf store.HomeShelf) bool {
	if shelf.Source.RollUpContainers != nil {
		return *shelf.Source.RollUpContainers
	}
	switch shelf.Source.Type {
	case "rules", "library", "saved-filter", "entity":
		return true
	case "builtin":
		switch shelf.Source.BuiltinKind {
		case "recently-added", "recently-played", "most-played", "top-picks", "random":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func (h *Handler) evaluateHomeShelfRaw(ctx context.Context, scope auth.Scope, filter store.LibraryFilter, shelf store.HomeShelf, limit, offset int) ([]store.SourceItem, int, error) {
	scopedFilter, ok := intersectLibraryFilter(filter, shelf.Source.LibraryIDs)
	if !ok {
		return []store.SourceItem{}, 0, nil
	}

	switch shelf.Source.Type {
	case "rules":
		rules := store.SmartRuleGroup{Match: "all"}
		if shelf.Source.Rules != nil {
			rules = *shelf.Source.Rules
		}
		var sort store.HomeShelfSort
		if shelf.Source.Sort != nil {
			sort = *shelf.Source.Sort
		}
		seed := homeShelfRandomSeed(shelf.ID, sort.RandomSeedMode, time.Now().UTC())
		return h.store.EvaluateSmartRules(ctx, scopedFilter, shelf.Source.LibraryIDs, scope.ProfileID, rules, sort, seed, limit, offset)

	case "builtin":
		kind := shelf.Source.BuiltinKind
		val := ""
		switch kind {
		case "next-up":
			val = "video"
		case "continue-listening":
			kind = "next-up"
			val = "audio"
		}
		q := store.SourceQuery{
			Kind:      kind,
			Value:     val,
			Limit:     limit,
			Offset:    offset,
			ProfileID: scope.ProfileID,
			Home:      true,
		}
		if len(shelf.Source.LibraryIDs) == 1 {
			q.LibraryID = shelf.Source.LibraryIDs[0]
		}
		if shelf.Source.BuiltinKind == "random" {
			mode := "daily"
			if shelf.Source.Sort != nil && shelf.Source.Sort.RandomSeedMode != "" {
				mode = shelf.Source.Sort.RandomSeedMode
			}
			q.Seed = homeShelfRandomSeed(shelf.ID, mode, time.Now().UTC())
		}
		items, err := h.store.SourceItems(ctx, scopedFilter, q)
		if err != nil {
			return nil, 0, err
		}
		if shelf.Source.Rules != nil && len(shelf.Source.Rules.Conditions) > 0 {
			items, err = h.store.FilterMediaByRules(ctx, scopedFilter, scope.ProfileID, items, *shelf.Source.Rules)
			if err != nil {
				return nil, 0, err
			}
		}
		return items, len(items), nil

	case "collection", "playlist":
		id := shelf.Source.CollectionID
		if id == "" {
			id = shelf.Source.PlaylistID
		}
		kind := "collection"
		if _, err := h.store.SmartCollectionByID(ctx, store.SmartCollectionID(id)); err == nil {
			kind = "smart-collection"
		}
		items, err := h.store.SourceItems(ctx, scopedFilter, store.SourceQuery{
			Kind:      kind,
			Value:     id,
			Limit:     limit,
			Offset:    offset,
			ProfileID: scope.ProfileID,
		})
		if err != nil {
			return nil, 0, err
		}
		return items, len(items), nil

	case "saved-filter":
		sf, err := h.store.SavedFilterByID(ctx, store.SavedFilterID(shelf.Source.SavedFilterID))
		if err != nil {
			return []store.SourceItem{}, 0, nil
		}
		if sf.ProfileID != scope.ProfileID {
			return []store.SourceItem{}, 0, nil
		}
		// SavedFilter.FilterJSON can carry either a SmartRuleGroup or fieldFilters map.
		var group store.SmartRuleGroup
		if err := json.Unmarshal([]byte(sf.FilterJSON), &group); err == nil && len(group.Conditions) > 0 {
			return h.store.EvaluateSmartRules(ctx, scopedFilter, []store.LibraryID{sf.LibraryID}, scope.ProfileID, group, store.HomeShelfSort{By: "added", Desc: true}, "", limit, offset)
		}
		var fieldFilters map[string]any
		_ = json.Unmarshal([]byte(sf.FilterJSON), &fieldFilters)
		items, err := h.store.SourceItems(ctx, scopedFilter, store.SourceQuery{
			Kind:         "all-items",
			LibraryID:    sf.LibraryID,
			FieldFilters: fieldFilters,
			Limit:        limit,
			Offset:       offset,
			ProfileID:    scope.ProfileID,
		})
		if err != nil {
			return nil, 0, err
		}
		return items, len(items), nil

	case "library":
		if shelf.Source.Sort != nil {
			seed := homeShelfRandomSeed(shelf.ID, shelf.Source.Sort.RandomSeedMode, time.Now().UTC())
			return h.store.EvaluateSmartRules(ctx, scopedFilter, shelf.Source.LibraryIDs, scope.ProfileID, store.SmartRuleGroup{Match: "all"}, *shelf.Source.Sort, seed, limit, offset)
		}
		q := store.SourceQuery{
			Kind:      "recently-added",
			Limit:     limit,
			Offset:    offset,
			ProfileID: scope.ProfileID,
		}
		if len(shelf.Source.LibraryIDs) == 1 {
			q.LibraryID = shelf.Source.LibraryIDs[0]
		}
		items, err := h.store.SourceItems(ctx, scopedFilter, q)
		if err != nil {
			return nil, 0, err
		}
		return items, len(items), nil

	case "entity":
		var ef *store.EntityFilter
		if shelf.Source.EntityName != "" {
			ef = &store.EntityFilter{Kind: shelf.Source.EntityKind, Name: shelf.Source.EntityName}
		}
		q := store.SourceQuery{
			Kind:         "all-items",
			EntityKind:   shelf.Source.EntityKind,
			EntityID:     store.EntityID(shelf.Source.EntityID),
			EntityFilter: ef,
			Limit:        limit,
			Offset:       offset,
			ProfileID:    scope.ProfileID,
		}
		if shelf.Source.EntityID != "" && ef == nil {
			q.Kind = "entity-media"
		}
		if len(shelf.Source.LibraryIDs) == 1 {
			q.LibraryID = shelf.Source.LibraryIDs[0]
		}
		items, err := h.store.SourceItems(ctx, scopedFilter, q)
		if err != nil {
			return nil, 0, err
		}
		return items, len(items), nil
	}

	return []store.SourceItem{}, 0, nil
}

func intersectLibraryFilter(base store.LibraryFilter, requested []store.LibraryID) (store.LibraryFilter, bool) {
	if len(requested) == 0 {
		return base, true
	}
	if len(base.AllowedIDs) == 0 {
		if !base.AllLibraries {
			return store.LibraryFilter{}, false
		}
		out := base
		out.AllLibraries = false
		out.AllowedIDs = slices.Clone(requested)
		return out, true
	}
	allowedSet := make(map[store.LibraryID]bool, len(base.AllowedIDs))
	for _, id := range base.AllowedIDs {
		allowedSet[id] = true
	}
	var merged []store.LibraryID
	for _, id := range requested {
		if allowedSet[id] {
			merged = append(merged, id)
		}
	}
	if len(merged) == 0 {
		return store.LibraryFilter{}, false
	}
	out := base
	out.AllowedIDs = merged
	return out, true
}

func homeShelfRandomSeed(shelfID, mode string, now time.Time) string {
	switch mode {
	case "hourly":
		return fmt.Sprintf("%s:%s", shelfID, now.Format("2006-01-02-15"))
	case "daily":
		return fmt.Sprintf("%s:%s", shelfID, now.Format("2006-01-02"))
	default:
		return fmt.Sprintf("%s:%d", shelfID, now.Unix())
	}
}

func decodeGraphInput(raw any, dst any) error {
	if raw == nil {
		return fmt.Errorf("input is required")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, dst)
}

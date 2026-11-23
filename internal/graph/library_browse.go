package graph

import (
	"context"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// libraryGroupingArchetype marks the Entity Kinds a Library's plugin declares
// for grouping its Media. Core offers one browse filter per such kind and
// never names one itself.
const libraryGroupingArchetype = "category"

// maxLibraryGroupFilters bounds how many groupings one browse may filter by;
// each one is a further intersection over the Library's candidates.
const maxLibraryGroupFilters = 8

// libraryFieldSortPrefix opens a sort that names a plugin-declared Sortable
// text Media field ("field:artist"), so the sort argument stays one string
// (CORE-3362).
const libraryFieldSortPrefix = "field:"

// maxLibraryBrowseQueryRunes bounds the title text one browse may narrow by
// (CORE-2568); a longer one is refused rather than cut.
const maxLibraryBrowseQueryRunes = 200

// LibraryItemsGrouping is one plugin-declared way to group the browsed
// Library: the Entity Kind, the plugin's display name for it, the values the
// calling Profile can actually reach, in title order, and parallel to them how
// many distinct visible Media reach each value (CORE-2505).
type LibraryItemsGrouping struct {
	Kind         string                `json:"kind"`
	Title        plugin.LocaleString   `json:"title,omitempty"`
	Values       []string              `json:"values"`
	Counts       []int                 `json:"counts"`
	BrowseCounts []int                 `json:"browseCounts"`
	BrowseLabels []plugin.LocaleString `json:"browseLabels"`
}

// LibraryItemsFacets lists the filter values that actually occur in the
// browsed Library for the calling Profile, so a client offers only filters
// that can match something.
type LibraryItemsFacets struct {
	Groupings []LibraryItemsGrouping `json:"groupings"`
	// Fields lists the values of each requested facetFields entry, in
	// request order (CORE-3369).
	Fields    []LibraryItemsField `json:"fields"`
	Decades   []int               `json:"decades"`
	Qualities []string            `json:"qualities"`
}

// LibraryItemsField is one Filterable text Media field's values in the
// browsed Library, most items first, with the item count of each.
type LibraryItemsField struct {
	Field  string   `json:"field"`
	Values []string `json:"values"`
	Counts []int    `json:"counts"`
}

// LibraryItemsLetter is where one title initial starts in a title-sorted
// browse and how many items share it: requesting Offset returns the first item
// with that initial, so a client can jump to a letter without loading the
// pages before it.
type LibraryItemsLetter struct {
	Letter string `json:"letter"`
	Offset int    `json:"offset"`
	Count  int    `json:"count"`
}

// LibraryItemsResponse is one page of a Library browse. Total counts every
// item matching the filters (not just this page), so a client can display the
// count and size a virtualized list before the remaining pages arrive.
type LibraryItemsResponse struct {
	Total  int                `json:"total"`
	Items  []SourceItem       `json:"items"`
	Facets LibraryItemsFacets `json:"facets"`
	// Letters lists, for a title sort, the offset where each title initial
	// starts and its item count ("#" for titles that do not open with a
	// letter); other sorts answer an empty list.
	Letters []LibraryItemsLetter `json:"letters"`
}

func emptyLibraryItems() LibraryItemsResponse {
	return LibraryItemsResponse{
		Items:   []SourceItem{},
		Facets:  LibraryItemsFacets{Groupings: []LibraryItemsGrouping{}, Fields: []LibraryItemsField{}, Decades: []int{}, Qualities: []string{}},
		Letters: []LibraryItemsLetter{},
	}
}

// libraryItems answers the full, sortable, filterable listing of one Library.
// Unlike sourceItems it reports the total and sorts and filters server-side,
// because a client cannot sort or count a Library it has only partly loaded.
func (h *Handler) libraryItems(ctx context.Context, scope auth.Scope, req request) (LibraryItemsResponse, error) {
	if h.store == nil {
		return emptyLibraryItems(), nil
	}
	query, err := libraryItemsQuery(scope, req)
	if err != nil {
		return LibraryItemsResponse{}, err
	}
	query.FacetMembers = boolVar(req, "categoryCounts")
	if err := libraryItemsFieldQuery(req, &query); err != nil {
		return LibraryItemsResponse{}, err
	}
	if query.Sort == store.LibraryBrowseSortField || len(query.FieldFilters) > 0 || len(query.FacetFields) > 0 {
		// Which fields sort, filter and facet describes the Library's plugin,
		// so a Library the caller cannot see answers as empty, the same as an
		// unknown one.
		visible, err := h.store.LibraryVisible(ctx, query.LibraryID, scope.LibraryFilter())
		if err != nil {
			return LibraryItemsResponse{}, err
		}
		if !visible {
			return emptyLibraryItems(), nil
		}
	}
	bundle, _ := h.libraryBundle(ctx, query.LibraryID)
	if err := libraryBrowseMediaFields(bundle, &query); err != nil {
		return LibraryItemsResponse{}, err
	}
	kinds := h.libraryGroupingKinds(ctx, query.LibraryID)
	for _, kind := range kinds {
		query.FacetEntityKinds = append(query.FacetEntityKinds, kind.Name)
	}
	result, err := h.store.LibraryBrowse(ctx, scope.LibraryFilter(), query)
	if err != nil {
		return LibraryItemsResponse{}, err
	}
	if err := h.annotateCards(ctx, scope, result.Items); err != nil {
		return LibraryItemsResponse{}, err
	}
	resp := emptyLibraryItems()
	resp.Total = result.Total
	resp.Items = make([]SourceItem, len(result.Items))
	for i, item := range result.Items {
		resp.Items[i] = h.sourceItem(item, scope.Revealed)
	}
	var units map[store.MediaID]store.SourceItem
	var labels map[string]plugin.LocaleString
	if query.FacetMembers {
		units, err = h.categoryBrowseUnits(ctx, result.Facets.Entities)
		if err != nil {
			return LibraryItemsResponse{}, err
		}
		labels = libraryBrowsePlanFor(bundle).labels
	}
	// A grouping with no reachable value is left out: it could filter
	// nothing, and naming it would describe a Library the caller cannot see.
	for i, facet := range result.Facets.Entities {
		if len(facet.Titles) == 0 {
			continue
		}
		var counts []int
		var names []plugin.LocaleString
		if query.FacetMembers {
			counts, names = categoryBrowseCounts(facet, units, labels)
		}
		resp.Facets.Groupings = append(resp.Facets.Groupings, LibraryItemsGrouping{
			Kind:         facet.Kind,
			Title:        kinds[i].DisplayName,
			Values:       facet.Titles,
			Counts:       facet.Counts,
			BrowseCounts: counts,
			BrowseLabels: names,
		})
	}
	for _, facet := range result.Facets.Fields {
		resp.Facets.Fields = append(resp.Facets.Fields, LibraryItemsField{Field: facet.Field, Values: facet.Values, Counts: facet.Counts})
	}
	if result.Facets.Decades != nil {
		resp.Facets.Decades = result.Facets.Decades
	}
	if result.Facets.Qualities != nil {
		resp.Facets.Qualities = result.Facets.Qualities
	}
	for _, letter := range result.Letters {
		resp.Letters = append(resp.Letters, LibraryItemsLetter{Letter: letter.Letter, Offset: letter.Offset, Count: letter.Count})
	}
	return resp, nil
}

// libraryGroupingKinds lists the grouping Entity Kinds the Library's plugin
// declares, in declaration order. A plugin-less Library has none.
func (h *Handler) libraryGroupingKinds(ctx context.Context, libraryID store.LibraryID) []plugin.EntityKind {
	pluginID, err := h.store.LibraryPluginID(ctx, libraryID)
	if err != nil || pluginID == "" {
		return nil
	}
	bundle, ok := h.plugins.Bundle(plugin.ID(pluginID))
	if !ok {
		return nil
	}
	var kinds []plugin.EntityKind
	for _, kind := range bundle.EntityKinds {
		if kind.Archetype == libraryGroupingArchetype {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// libraryItemsQuery validates the request variables. An unknown sort, watch
// state or quality is a client error, never a silent fallback: quietly
// ignoring a filter would return a wider set than the one the client labels.
func libraryItemsQuery(scope auth.Scope, req request) (store.LibraryBrowseQuery, error) {
	libraryID := trimmedVariable(req, "libraryID")
	if libraryID == "" {
		return store.LibraryBrowseQuery{}, clientError("libraryID is required")
	}
	sortKey := trimmedVariable(req, "sort")
	sortField := ""
	if name, ok := strings.CutPrefix(sortKey, libraryFieldSortPrefix); ok {
		if sortField = strings.TrimSpace(name); sortField == "" {
			return store.LibraryBrowseQuery{}, clientError("sort is not a supported library sort")
		}
		sortKey = store.LibraryBrowseSortField
	} else if sortKey == store.LibraryBrowseSortField || !store.ValidLibraryBrowseSort(sortKey) {
		return store.LibraryBrowseQuery{}, clientError("sort is not a supported library sort")
	}
	direction := trimmedVariable(req, "direction")
	if direction != "" && direction != "asc" && direction != "desc" {
		return store.LibraryBrowseQuery{}, clientError("direction must be asc or desc")
	}
	watch := trimmedVariable(req, "watch")
	if !store.ValidLibraryBrowseWatch(watch) {
		return store.LibraryBrowseQuery{}, clientError("watch is not a supported watch state")
	}
	quality := trimmedVariable(req, "quality")
	if !store.ValidLibraryBrowseQuality(quality) {
		return store.LibraryBrowseQuery{}, clientError("quality is not a supported quality tier")
	}
	yearFrom, yearTo := intVar(req, "yearFrom"), intVar(req, "yearTo")
	if yearFrom < 0 || yearTo < 0 || (yearTo > 0 && yearFrom > yearTo) {
		return store.LibraryBrowseQuery{}, clientError("yearFrom and yearTo must form a valid range")
	}
	minRating := intVar(req, "minRating")
	if minRating < 0 || minRating > store.ItemRatingMax {
		return store.LibraryBrowseQuery{}, clientError("minRating must be between 0 and 10")
	}
	text := trimmedVariable(req, "query")
	if utf8.RuneCountInString(text) > maxLibraryBrowseQueryRunes {
		return store.LibraryBrowseQuery{}, clientError("query must be at most 200 characters")
	}
	// groupFilters maps an Entity Kind to the one value selected for it. The
	// kinds are applied in name order so the same request always reads the
	// same way.
	// Read raw: a blank or non-text entry is refused, not dropped, because
	// dropping it would answer a wider set than the client asked for.
	groupFilters := anyMapVar(req, "groupFilters")
	entityFilters := make([]store.EntityFilter, 0, len(groupFilters))
	if len(groupFilters) > maxLibraryGroupFilters {
		return store.LibraryBrowseQuery{}, clientError("too many groupFilters")
	}
	for kind, value := range groupFilters {
		name, _ := value.(string)
		kind, name = strings.TrimSpace(kind), strings.TrimSpace(name)
		if kind == "" || name == "" {
			return store.LibraryBrowseQuery{}, clientError("groupFilters requires a kind and a value")
		}
		entityFilters = append(entityFilters, store.EntityFilter{Kind: kind, Name: name})
	}
	slices.SortFunc(entityFilters, func(a, b store.EntityFilter) int { return strings.Compare(a.Kind, b.Kind) })
	return store.LibraryBrowseQuery{
		LibraryID:     store.LibraryID(libraryID),
		ProfileID:     scope.ProfileID,
		Sort:          sortKey,
		SortField:     sortField,
		Descending:    direction == "desc",
		Seed:          stringVar(req, "seed"),
		Watch:         watch,
		EntityFilters: entityFilters,
		YearFrom:      yearFrom,
		YearTo:        yearTo,
		Quality:       quality,
		MinRating:     minRating,
		Text:          text,
		Limit:         boundedIntVar(req, "limit", 50, 200),
		Offset:        nonNegativeIntVar(req, "offset"),
	}, nil
}

// libraryItemsFieldQuery reads libraryItems' text Media field filters and
// facets (CORE-3369); libraryBrowseMediaFields then checks them against the
// Library plugin's Filterable text fields.
func libraryItemsFieldQuery(req request, query *store.LibraryBrowseQuery) error {
	// fieldFilters maps a Filterable text Media field to the one value
	// selected for it, matched exactly as facets report it (CORE-3369). A
	// malformed argument is refused: dropping it would widen the results.
	var fieldFilters map[string]string
	if raw, present := req.Variables["fieldFilters"]; present && raw != nil {
		filters, ok := raw.(map[string]any)
		if !ok {
			return clientError("fieldFilters must be an object of field values")
		}
		if len(filters) > maxLibraryGroupFilters {
			return clientError("too many fieldFilters")
		}
		for field, value := range filters {
			text, _ := value.(string)
			if field == "" || field != strings.TrimSpace(field) || text == "" || text != strings.TrimSpace(text) {
				return clientError("fieldFilters requires a field and a value")
			}
			if fieldFilters == nil {
				fieldFilters = map[string]string{}
			}
			fieldFilters[field] = text
		}
	}
	var facetFields []string
	if raw, present := req.Variables["facetFields"]; present && raw != nil {
		fields, ok := raw.([]any)
		if !ok {
			return clientError("facetFields must be a list of field names")
		}
		if len(fields) > maxLibraryGroupFilters {
			return clientError("too many facetFields")
		}
		for _, value := range fields {
			field, _ := value.(string)
			if field == "" || field != strings.TrimSpace(field) {
				return clientError("facetFields requires field names")
			}
			if !slices.Contains(facetFields, field) {
				facetFields = append(facetFields, field)
			}
		}
	}
	query.FieldFilters = fieldFilters
	query.FacetFields = facetFields
	return nil
}

// Resolve each visible member once across all category values. The store's
// facet membership already excludes hidden, trashed and muted Media.
func (h *Handler) categoryBrowseUnits(ctx context.Context, facets []store.LibraryBrowseEntityFacet) (map[store.MediaID]store.SourceItem, error) {
	positions := map[store.MediaID]bool{}
	var items []store.SourceItem
	for _, facet := range facets {
		for _, members := range facet.Members {
			for _, id := range members {
				if !positions[id] {
					positions[id] = true
					items = append(items, store.SourceItem{ID: id})
				}
			}
		}
	}
	if err := h.annotateContainers(ctx, items); err != nil {
		return nil, err
	}
	units := make(map[store.MediaID]store.SourceItem, len(items))
	for _, item := range items {
		units[item.ID] = item
	}
	return units, nil
}

func categoryBrowseCounts(facet store.LibraryBrowseEntityFacet, units map[store.MediaID]store.SourceItem, labels map[string]plugin.LocaleString) ([]int, []plugin.LocaleString) {
	counts := slices.Clone(facet.Counts)
	names := make([]plugin.LocaleString, len(counts))
	for i, members := range facet.Members {
		kind := ""
		containers := map[store.EntityID]bool{}
		homogeneous := len(members) > 0
		for _, id := range members {
			unit := units[id]
			if unit.ContainerID == "" || (kind != "" && kind != unit.ContainerKind) {
				homogeneous = false
				break
			}
			kind = unit.ContainerKind
			containers[unit.ContainerID] = true
		}
		if homogeneous && len(labels[kind]) > 0 {
			counts[i] = len(containers)
			names[i] = labels[kind]
		}
	}
	return counts, names
}

// libraryBrowseMediaFields checks a field sort against the Library plugin's
// declarations and lets the text filter also match the plugin's Searchable
// text Media fields. A field the plugin does not declare Sortable text is a
// client error, never a silent title sort; likewise a field filter or facet
// on a field it does not declare Filterable text.
func libraryBrowseMediaFields(bundle plugin.Bundle, query *store.LibraryBrowseQuery) error {
	sortable := false
	filterable := map[string]bool{}
	for _, field := range bundle.MediaFields {
		if field.Type != "text" {
			continue
		}
		// A Sensitive field's values never leave the server through a facet
		// or answer a filter probe.
		filterable[field.Name] = field.Filterable && !field.Sensitive
		if field.Searchable {
			query.TextFields = append(query.TextFields, field.Name)
		}
		if field.Name == query.SortField && field.Sortable {
			sortable = true
		}
	}
	if query.Sort == store.LibraryBrowseSortField && !sortable {
		return clientError("sort is not a supported library sort")
	}
	for field := range query.FieldFilters {
		if !filterable[field] {
			return clientError("fieldFilters is not a supported library field filter")
		}
	}
	for _, field := range query.FacetFields {
		if !filterable[field] {
			return clientError("facetFields is not a supported library field filter")
		}
	}
	return nil
}

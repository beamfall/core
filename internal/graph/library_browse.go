package graph

import (
	"context"
	"slices"
	"strings"

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

// LibraryItemsGrouping is one plugin-declared way to group the browsed
// Library: the Entity Kind, the plugin's display name for it, the values the
// calling Profile can actually reach, in title order, and parallel to them how
// many distinct visible Media reach each value (CORE-2505).
type LibraryItemsGrouping struct {
	Kind   string              `json:"kind"`
	Title  plugin.LocaleString `json:"title,omitempty"`
	Values []string            `json:"values"`
	Counts []int               `json:"counts"`
}

// LibraryItemsFacets lists the filter values that actually occur in the
// browsed Library for the calling Profile, so a client offers only filters
// that can match something.
type LibraryItemsFacets struct {
	Groupings []LibraryItemsGrouping `json:"groupings"`
	Decades   []int                  `json:"decades"`
	Qualities []string               `json:"qualities"`
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
		Facets:  LibraryItemsFacets{Groupings: []LibraryItemsGrouping{}, Decades: []int{}, Qualities: []string{}},
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
	// A grouping with no reachable value is left out: it could filter
	// nothing, and naming it would describe a Library the caller cannot see.
	for i, facet := range result.Facets.Entities {
		if len(facet.Titles) == 0 {
			continue
		}
		resp.Facets.Groupings = append(resp.Facets.Groupings, LibraryItemsGrouping{
			Kind:   facet.Kind,
			Title:  kinds[i].DisplayName,
			Values: facet.Titles,
			Counts: facet.Counts,
		})
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
	if !store.ValidLibraryBrowseSort(sortKey) {
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
		Descending:    direction == "desc",
		Seed:          stringVar(req, "seed"),
		Watch:         watch,
		EntityFilters: entityFilters,
		YearFrom:      yearFrom,
		YearTo:        yearTo,
		Quality:       quality,
		MinRating:     minRating,
		Limit:         boundedIntVar(req, "limit", 50, 200),
		Offset:        nonNegativeIntVar(req, "offset"),
	}, nil
}

package graph

import (
	"context"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// LibraryItemsFacets lists the filter values that actually occur in the
// browsed Library for the calling Profile, so a client offers only filters
// that can match something.
type LibraryItemsFacets struct {
	Entities  []string `json:"entities"`
	Decades   []int    `json:"decades"`
	Qualities []string `json:"qualities"`
}

// LibraryItemsResponse is one page of a Library browse. Total counts every
// item matching the filters (not just this page), so a client can display the
// count and size a virtualized list before the remaining pages arrive.
type LibraryItemsResponse struct {
	Total  int                `json:"total"`
	Items  []SourceItem       `json:"items"`
	Facets LibraryItemsFacets `json:"facets"`
}

func emptyLibraryItems() LibraryItemsResponse {
	return LibraryItemsResponse{
		Items:  []SourceItem{},
		Facets: LibraryItemsFacets{Entities: []string{}, Decades: []int{}, Qualities: []string{}},
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
	result, err := h.store.LibraryBrowse(ctx, scope.LibraryFilter(), query)
	if err != nil {
		return LibraryItemsResponse{}, err
	}
	if err := h.store.AnnotateMuted(ctx, scope.ProfileID, scope.LibraryFilter(), result.Items); err != nil {
		return LibraryItemsResponse{}, err
	}
	resp := emptyLibraryItems()
	resp.Total = result.Total
	resp.Items = make([]SourceItem, len(result.Items))
	for i, item := range result.Items {
		resp.Items[i] = h.sourceItem(item, scope.Revealed)
	}
	if result.Facets.Entities != nil {
		resp.Facets.Entities = result.Facets.Entities
	}
	if result.Facets.Decades != nil {
		resp.Facets.Decades = result.Facets.Decades
	}
	if result.Facets.Qualities != nil {
		resp.Facets.Qualities = result.Facets.Qualities
	}
	return resp, nil
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
	var entityFilter *store.EntityFilter
	if raw := stringMapVar(req, "entityFilter"); raw != nil {
		kind, name := strings.TrimSpace(raw["kind"]), strings.TrimSpace(raw["name"])
		if kind == "" || name == "" {
			return store.LibraryBrowseQuery{}, clientError("entityFilter requires kind and name")
		}
		entityFilter = &store.EntityFilter{Kind: kind, Name: name}
	}
	return store.LibraryBrowseQuery{
		LibraryID:       store.LibraryID(libraryID),
		ProfileID:       scope.ProfileID,
		Sort:            sortKey,
		Descending:      direction == "desc",
		Seed:            stringVar(req, "seed"),
		Watch:           watch,
		EntityFilter:    entityFilter,
		YearFrom:        yearFrom,
		YearTo:          yearTo,
		Quality:         quality,
		FacetEntityKind: trimmedVariable(req, "facetEntityKind"),
		Limit:           boundedIntVar(req, "limit", 50, 200),
		Offset:          nonNegativeIntVar(req, "offset"),
	}, nil
}

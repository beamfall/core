package graph

import (
	"context"
	"slices"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// LibraryEntityKind is one Entity kind a Library can be browsed by: the
// plugin's browse Section title for it as Label, the kind's display name, its
// archetype, and how many of the Library's Entities have the kind.
type LibraryEntityKind struct {
	Kind            string              `json:"kind"`
	Label           plugin.LocaleString `json:"label,omitempty"`
	KindDisplayName plugin.LocaleString `json:"kindDisplayName,omitempty"`
	Archetype       string              `json:"archetype,omitempty"`
	Total           int                 `json:"total"`
}

// LibraryEntityParent names one Entity a listed Entity belongs under.
type LibraryEntityParent struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// LibraryEntityItem is one listed Entity card. ChildCount counts its linked
// Entities of the kinds that declare a relation to its kind; MediaCount counts
// the distinct visible Media linked to it or to one of those children.
type LibraryEntityItem struct {
	SourceItem
	ChildCount int                   `json:"childCount"`
	MediaCount int                   `json:"mediaCount"`
	Parents    []LibraryEntityParent `json:"parents"`
}

// LibraryEntitiesResponse is one page of a Library's Entities of one kind
// (CORE-2362). Kinds lists every kind the caller can browse the Library by;
// AllLabel is the plugin's title for its unfiltered Media grid; Kind is the
// kind listed and Total its count after the browse filters (CORE-2440).
type LibraryEntitiesResponse struct {
	Kinds    []LibraryEntityKind `json:"kinds"`
	AllLabel plugin.LocaleString `json:"allLabel,omitempty"`
	Kind     string              `json:"kind"`
	Total    int                 `json:"total"`
	Items    []LibraryEntityItem `json:"items"`
	// Letters lists, for a title sort, the offset where each title initial
	// starts in the filtered set and its count, as libraryItems does; other
	// sorts answer an empty list.
	Letters []LibraryItemsLetter `json:"letters"`
}

func emptyLibraryEntities() LibraryEntitiesResponse {
	return LibraryEntitiesResponse{Kinds: []LibraryEntityKind{}, Items: []LibraryEntityItem{}, Letters: []LibraryItemsLetter{}}
}

// libraryBrowsePlan is what a Library's plugin declares for browsing it by
// Entity: the kinds, parent kinds first, each with its Section title, and the
// title of the unfiltered Media grid.
type libraryBrowsePlan struct {
	kinds    []plugin.EntityKind
	labels   map[string]plugin.LocaleString
	allLabel plugin.LocaleString
	// declared holds every kind the plugin declares, browsable or not, so
	// child kinds can be found among them.
	declared []plugin.EntityKind
}

// libraryBrowsePlanFor reads the browse plan from a plugin's declarations. A
// browse kind is one an unfiltered "entity-items" Section of a browse Layout
// lists; a narrowed Section shows a slice of a kind, not the kind.
func libraryBrowsePlanFor(bundle plugin.Bundle) libraryBrowsePlan {
	plan := libraryBrowsePlan{labels: map[string]plugin.LocaleString{}, declared: bundle.EntityKinds}
	var listed []string
	for _, layout := range bundle.Layouts {
		if layout.Role != "browse" {
			continue
		}
		for _, section := range layout.Sections {
			source := section.Source
			if len(source.FieldFilters) > 0 || source.EntityFilter != nil {
				continue
			}
			switch {
			case source.Kind == "entity-items" && source.EntityKind != "":
				if _, seen := plan.labels[source.EntityKind]; !seen {
					plan.labels[source.EntityKind] = section.Title
					listed = append(listed, source.EntityKind)
				}
			case source.Kind == "all-items" && section.Kind != "filter-bar" && len(plan.allLabel) == 0:
				plan.allLabel = section.Title
			}
		}
	}
	for _, name := range listed {
		if kind, ok := declaredKind(bundle.EntityKinds, name); ok {
			plan.kinds = append(plan.kinds, kind)
		}
	}
	plan.kinds = parentKindsFirst(plan.kinds)
	return plan
}

func declaredKind(kinds []plugin.EntityKind, name string) (plugin.EntityKind, bool) {
	for _, kind := range kinds {
		if kind.Name == name {
			return kind, true
		}
	}
	return plugin.EntityKind{}, false
}

// relatesTo reports whether kind declares a relation to the kind named target.
func relatesTo(kind plugin.EntityKind, target string) bool {
	if kind.Name == target {
		return false
	}
	return slices.ContainsFunc(kind.Relations, func(relation plugin.RelationSpec) bool {
		return relation.ToKind == target
	})
}

// parentKindsFirst orders kinds so a kind comes before every kind declaring a
// relation to it, keeping declaration order otherwise. A cycle falls back to
// declaration order for the kinds in it.
func parentKindsFirst(kinds []plugin.EntityKind) []plugin.EntityKind {
	remaining := slices.Clone(kinds)
	ordered := make([]plugin.EntityKind, 0, len(kinds))
	for len(remaining) > 0 {
		next := 0
		for i, kind := range remaining {
			if !slices.ContainsFunc(remaining, func(other plugin.EntityKind) bool { return relatesTo(kind, other.Name) }) {
				next = i
				break
			}
		}
		ordered = append(ordered, remaining[next])
		remaining = slices.Delete(remaining, next, next+1)
	}
	return ordered
}

// childKinds names every declared kind that declares a relation to kind.
func (plan libraryBrowsePlan) childKinds(kind string) []string {
	var names []string
	for _, other := range plan.declared {
		if relatesTo(other, kind) {
			names = append(names, other.Name)
		}
	}
	return names
}

// parentKinds names the browse kinds kind declares a relation to.
func (plan libraryBrowsePlan) parentKinds(kind plugin.EntityKind) []string {
	var names []string
	for _, other := range plan.kinds {
		if relatesTo(kind, other.Name) {
			names = append(names, other.Name)
		}
	}
	return names
}

// libraryBundle returns the plugin bundle a Library is bound to; a
// plugin-less Library has none.
func (h *Handler) libraryBundle(ctx context.Context, libraryID store.LibraryID) (plugin.Bundle, bool) {
	pluginID, err := h.store.LibraryPluginID(ctx, libraryID)
	if err != nil || pluginID == "" {
		return plugin.Bundle{}, false
	}
	return h.plugins.Bundle(plugin.ID(pluginID))
}

// libraryEntities answers one page of a Library's Entities of one browse kind
// for the calling session. The kinds, their order and their labels come from
// the Library plugin's declarations; core names no kind. The sort and filter
// arguments are libraryItems's, validated the same way.
func (h *Handler) libraryEntities(ctx context.Context, scope auth.Scope, req request) (LibraryEntitiesResponse, error) {
	browse, err := libraryItemsQuery(scope, req)
	if err != nil {
		return LibraryEntitiesResponse{}, err
	}
	libraryID := browse.LibraryID
	limit, offset := intVar(req, "limit"), intVar(req, "offset")
	if limit < 0 || offset < 0 {
		return LibraryEntitiesResponse{}, clientError("limit and offset must not be negative")
	}
	resp := emptyLibraryEntities()
	if h.store == nil {
		return resp, nil
	}
	bundle, ok := h.libraryBundle(ctx, libraryID)
	if !ok {
		return resp, nil
	}
	plan := libraryBrowsePlanFor(bundle)
	if len(plan.kinds) == 0 {
		return resp, nil
	}
	names := make([]string, len(plan.kinds))
	for i, kind := range plan.kinds {
		names[i] = kind.Name
	}
	filter := scope.LibraryFilter()
	sources := h.entityBrowseSources[bundle.ID]
	// The first read only totals the kinds, so the default kind is the first
	// one the caller can actually browse.
	totals, err := h.store.LibraryEntities(ctx, filter, store.LibraryEntitiesQuery{
		LibraryID: libraryID, Kinds: names, BrowseSources: sources,
	})
	if err != nil {
		return LibraryEntitiesResponse{}, err
	}
	for _, kind := range plan.kinds {
		if total := totals.Totals[kind.Name]; total > 0 {
			resp.Kinds = append(resp.Kinds, LibraryEntityKind{
				Kind:            kind.Name,
				Label:           plan.labels[kind.Name],
				KindDisplayName: kind.DisplayName,
				Archetype:       kind.Archetype,
				Total:           total,
			})
		}
	}
	if len(resp.Kinds) == 0 {
		return resp, nil
	}
	resp.AllLabel = plan.allLabel
	resp.Kind = trimmedVariable(req, "kind")
	if resp.Kind == "" {
		resp.Kind = resp.Kinds[0].Kind
	}
	listed, ok := declaredKind(plan.kinds, resp.Kind)
	if !ok || totals.Totals[resp.Kind] == 0 {
		return resp, nil
	}
	// Kinds stays empty: the kind was totalled above, so the page read skips
	// a second count (CORE-2894).
	result, err := h.store.LibraryEntities(ctx, filter, store.LibraryEntitiesQuery{
		LibraryID:     libraryID,
		Kind:          resp.Kind,
		ChildKinds:    plan.childKinds(resp.Kind),
		ParentKinds:   plan.parentKinds(listed),
		BrowseSources: sources,
		Browse:        browse,
		Limit:         limit,
		Offset:        offset,
	})
	if err != nil {
		return LibraryEntitiesResponse{}, err
	}
	resp.Total = result.Total
	for _, letter := range result.Letters {
		resp.Letters = append(resp.Letters, LibraryItemsLetter{Letter: letter.Letter, Offset: letter.Offset, Count: letter.Count})
	}
	cards := make([]store.SourceItem, len(result.Items))
	for i, item := range result.Items {
		cards[i] = item.Item
	}
	if err := h.annotateCards(ctx, scope, cards); err != nil {
		return LibraryEntitiesResponse{}, err
	}
	resp.Items = make([]LibraryEntityItem, len(result.Items))
	for i, item := range result.Items {
		parents := make([]LibraryEntityParent, len(item.Parents))
		for j, parent := range item.Parents {
			parents[j] = LibraryEntityParent{ID: string(parent.ID), Title: parent.Title}
		}
		resp.Items[i] = LibraryEntityItem{
			SourceItem: h.sourceItem(cards[i], scope.Revealed),
			ChildCount: item.ChildCount,
			MediaCount: item.MediaCount,
			Parents:    parents,
		}
	}
	return resp, nil
}

// libraryEntitiesArguments declares the libraryEntities read's arguments:
// libraryItems's browse arguments, with the same enums, plus the listed kind.
func libraryEntitiesArguments() []NestedArgument {
	args := append(libraryItemsArguments(), NestedArgument{Name: "kind", Kind: "string", Optional: true})
	slices.SortFunc(args, func(a, b NestedArgument) int { return strings.Compare(a.Name, b.Name) })
	return args
}

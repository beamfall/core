package graph

import (
	"context"
	"sort"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/store"
)

// annotateContainers sets ContainerID and ContainerTitle on each media-kind
// item from the outermost container Entity holding it, following the Library
// plugin's declared container relations the way the container chain does
// (CORE-2524). A card can then name what an item belongs to without a detail
// read. Core reads relation and kind names from the plugin, never its words.
// The Entities walked share the visible Media's Library, so naming them
// reveals nothing the caller cannot already reach.
func (h *Handler) annotateContainers(ctx context.Context, items []store.SourceItem) error {
	var ids []store.MediaID
	for _, item := range items {
		if item.Kind != "entity" {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 || h.store == nil {
		return nil
	}
	holders, err := h.store.MediaHolderLinksBatch(ctx, ids)
	if err != nil {
		return err
	}
	relations := map[store.LibraryID]identify.ContainerRelationIndex{}
	relationsFor := func(libraryID store.LibraryID) identify.ContainerRelationIndex {
		index, ok := relations[libraryID]
		if !ok {
			bundle, _ := h.libraryBundle(ctx, libraryID)
			index = identify.ContainerRelations(bundle)
			relations[libraryID] = index
		}
		return index
	}
	// current is each item's innermost-so-far container, by item position.
	current := map[int]store.TitledEntityLink{}
	for i, item := range items {
		if item.Kind == "entity" {
			continue
		}
		for _, link := range holders[item.ID] {
			if relationsFor(link.LibraryID).Holds(link.Kind, link.Relation) {
				current[i] = link
				break
			}
		}
	}
	seen := map[int]map[store.EntityID]bool{}
	frontier := map[int]bool{}
	for i, link := range current {
		seen[i] = map[store.EntityID]bool{link.EntityID: true}
		frontier[i] = true
	}
	for depth := 1; depth < identify.MaxContainerDepth && len(frontier) > 0; depth++ {
		// Siblings share their containers, so each Entity is read once.
		var next []store.EntityID
		queued := map[store.EntityID]bool{}
		for i := range frontier {
			if id := current[i].EntityID; !queued[id] {
				queued[id] = true
				next = append(next, id)
			}
		}
		targets, err := h.store.EntityTargetLinksBatch(ctx, next)
		if err != nil {
			return err
		}
		for i := range frontier {
			from := current[i]
			moved := false
			for _, link := range targets[from.EntityID] {
				if !seen[i][link.EntityID] && relationsFor(from.LibraryID).Nests(from.Kind, link.Relation, link.Kind) {
					seen[i][link.EntityID] = true
					current[i] = link
					moved = true
					break
				}
			}
			if !moved {
				delete(frontier, i)
			}
		}
	}
	for i, link := range current {
		items[i].ContainerID = link.EntityID
		items[i].ContainerTitle = link.Title
	}
	return nil
}

// sourceItemContainer renders an item's container for the wire, with the
// container Entity's poster when it has one.
func (h *Handler) sourceItemContainer(item store.SourceItem) *SourceItemContainer {
	if item.ContainerID == "" {
		return nil
	}
	container := &SourceItemContainer{ID: string(item.ContainerID), Title: item.ContainerTitle}
	if poster, ok := h.entityArtworkSlots(item.ContainerID)[artwork.SlotPoster]; ok {
		container.Artwork = &SourceItemArtwork{Poster: poster}
		if color, ok := h.artwork.EntityDominantColor(item.ContainerID); ok {
			container.Artwork.DominantColor = color
		}
	}
	return container
}

// Keyword search asks for every hit the store scores, uncut, and cuts to its result
// limit only after container ordering, so a container with more matching
// items than the cut keeps its first items rather than the alphabetically
// first ones (CORE-2525).
const (
	keywordResultLimit    = 50
	keywordCandidateLimit = store.SearchCandidateLimit
)

// orderKeywordResults orders keyword hits by relevance, scoring a Media item
// by its own title or its container's, whichever matches better, so items
// found through their container rank with it (CORE-2525). Within a relevance
// tie hits group by container: the container Entity first, then its items by
// their stored ordinals, so siblings read in order and a client can collapse
// them under the container. A group ranks as its best-matching row, so it
// never splits across ranks. It keeps at most limit hits: a group that does
// not fit keeps its container and its best-matching items, still read in
// ordinal order, so an exact title match is never cut for a sibling that only
// matched through the container (CORE-2547).
func (h *Handler) orderKeywordResults(ctx context.Context, text string, results []store.SourceItem, limit int) ([]store.SourceItem, error) {
	if err := h.annotateContainers(ctx, results); err != nil {
		return nil, err
	}
	var ids []store.MediaID
	for _, item := range results {
		if item.Kind != "entity" {
			ids = append(ids, item.ID)
		}
	}
	ordinals := map[store.MediaID]map[string]store.MediaField{}
	if len(ids) > 0 {
		values, err := h.store.MediaCardFieldValues(ctx, ids, cardOrdinalFields)
		if err != nil {
			return nil, err
		}
		ordinals = values
	}
	type hitKey struct {
		rank       int
		own        int
		groupTitle string
		groupID    string
		member     bool
		sequence   float64
		index      float64
	}
	rank := func(title string) int {
		r := store.SearchRank(text, title)
		if r < 0 {
			return 1 << 30
		}
		return r
	}
	keys := make([]hitKey, len(results))
	for i, item := range results {
		key := hitKey{rank: rank(item.Title), groupTitle: item.Title, groupID: string(item.ID)}
		key.own = key.rank
		if item.ContainerID != "" {
			key.rank = min(key.rank, rank(item.ContainerTitle))
			key.groupTitle, key.groupID, key.member = item.ContainerTitle, string(item.ContainerID), true
		}
		if fields, ok := ordinals[item.ID]; ok && item.Kind != "entity" {
			key.sequence, key.index = fields["sequence"].Number, fields["index"].Number
		}
		keys[i] = key
	}
	// A group ranks as its best row, so an item that outranks its container
	// lifts the whole group and the container still leads it; CORE-2471's
	// regrouping then has nothing to move.
	groupRank := map[string]int{}
	for _, key := range keys {
		if best, ok := groupRank[key.groupID]; !ok || key.rank < best {
			groupRank[key.groupID] = key.rank
		}
	}
	for i := range keys {
		keys[i].rank = groupRank[keys[i].groupID]
	}
	indexes := make([]int, len(results))
	for i := range indexes {
		indexes[i] = i
	}
	// byOwn orders a group's members by their own match before their ordinals;
	// it picks which hits survive the cut, and the survivors are then read in
	// ordinal order.
	order := func(byOwn bool) {
		sort.SliceStable(indexes, func(a, b int) bool {
			left, right := keys[indexes[a]], keys[indexes[b]]
			l, r := results[indexes[a]], results[indexes[b]]
			switch {
			case left.rank != right.rank:
				return left.rank < right.rank
			case left.groupTitle != right.groupTitle:
				return left.groupTitle < right.groupTitle
			case left.groupID != right.groupID:
				return left.groupID < right.groupID
			case left.member != right.member:
				return !left.member
			case byOwn && left.own != right.own:
				return left.own < right.own
			case left.sequence != right.sequence:
				return left.sequence < right.sequence
			case left.index != right.index:
				return left.index < right.index
			case l.Kind != r.Kind:
				return l.Kind < r.Kind
			}
			return l.ID < r.ID
		})
	}
	if len(indexes) > limit {
		order(true)
		indexes = indexes[:limit]
	}
	order(false)
	sorted := make([]store.SourceItem, len(indexes))
	for i, at := range indexes {
		sorted[i] = results[at]
	}
	return sorted, nil
}

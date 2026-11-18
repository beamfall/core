package graph

import (
	"context"
	"sort"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Related Media (CORE-2444): the Media that share a Media item's category
// credits, ranked by how many of its credits they share. Every credit is read
// through the entity-media Source with the acting Profile, the path browse and
// Home rails take, so Mute suppression, the Library and reveal filter and
// locked marking apply exactly as they do there. Only archetypes are named
// here, never a plugin kind.
const (
	relatedCategoryCredits = 4
	relatedPersonCredits   = 3
	// relatedPerCredit bounds each credit's read; a larger credit contributes
	// its first Media in the Source's order.
	relatedPerCredit    = 100
	relatedDefaultLimit = 20
	relatedMaxLimit     = 50
)

// relatedSignals picks the credits the ranking reads, de-duplicated, in the
// server's credit order: the first category-archetype credits and the first
// person-archetype credits.
func relatedSignals(credits []MediaCreditInfo) (categories, people []string) {
	seen := map[string]bool{}
	for _, credit := range credits {
		if credit.EntityID == "" || seen[credit.EntityID] {
			continue
		}
		seen[credit.EntityID] = true
		switch credit.Archetype {
		case "category":
			if len(categories) < relatedCategoryCredits {
				categories = append(categories, credit.EntityID)
			}
		case "person":
			if len(people) < relatedPersonCredits {
				people = append(people, credit.EntityID)
			}
		}
	}
	return categories, people
}

// rankRelated orders the Media read for each signal: those sharing at least
// one category, most shared signals first, ties in first-read order, the item
// itself left out, at most limit.
func rankRelated(mediaID store.MediaID, byCategory, byPerson [][]store.SourceItem, limit int) []store.SourceItem {
	type entry struct {
		item     store.SourceItem
		score    int
		order    int
		category bool
	}
	scored := map[store.MediaID]*entry{}
	var entries []*entry
	add := func(items []store.SourceItem, category bool) {
		counted := map[store.MediaID]bool{}
		for _, item := range items {
			if item.ID == "" || item.ID == mediaID || counted[item.ID] {
				continue
			}
			counted[item.ID] = true
			if found, ok := scored[item.ID]; ok {
				found.score++
				found.category = found.category || category
				continue
			}
			e := &entry{item: item, score: 1, order: len(entries), category: category}
			scored[item.ID] = e
			entries = append(entries, e)
		}
	}
	for _, items := range byCategory {
		add(items, true)
	}
	for _, items := range byPerson {
		add(items, false)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].score != entries[j].score {
			return entries[i].score > entries[j].score
		}
		return entries[i].order < entries[j].order
	})
	out := []store.SourceItem{}
	for _, e := range entries {
		if !e.category {
			continue
		}
		out = append(out, e.item)
		if len(out) == limit {
			break
		}
	}
	return out
}

// relatedMedia answers relatedMedia(mediaID, limit).
func (h *Handler) relatedMedia(ctx context.Context, scope auth.Scope, req request) ([]SourceItem, error) {
	mediaID := store.MediaID(trimmedVariable(req, "mediaID"))
	if mediaID == "" {
		return nil, clientError("mediaID is required")
	}
	filter := scope.LibraryFilter()
	if _, err := h.store.Media(ctx, mediaID, filter); err != nil {
		return nil, clientError("unknown media")
	}
	limit := boundedIntVar(req, "limit", relatedDefaultLimit, relatedMaxLimit)
	categories, people := relatedSignals(h.mediaCredits(ctx, scope, mediaID))
	if len(categories) == 0 {
		return []SourceItem{}, nil
	}
	read := func(ids []string) ([][]store.SourceItem, error) {
		lists := make([][]store.SourceItem, 0, len(ids))
		for _, id := range ids {
			items, err := h.store.SourceItems(ctx, filter, store.SourceQuery{
				Kind:      "entity-media",
				EntityID:  store.EntityID(id),
				Limit:     relatedPerCredit,
				ProfileID: scope.ProfileID,
			})
			if err != nil {
				return nil, err
			}
			lists = append(lists, items)
		}
		return lists, nil
	}
	byCategory, err := read(categories)
	if err != nil {
		return nil, err
	}
	byPerson, err := read(people)
	if err != nil {
		return nil, err
	}
	items := rankRelated(mediaID, byCategory, byPerson, limit)
	// Survivors may still be broad-muted but favourited, which §2.7 badges.
	if err := h.store.AnnotateMuted(ctx, scope.ProfileID, filter, items); err != nil {
		return nil, err
	}
	if err := h.annotateCards(ctx, scope, items); err != nil {
		return nil, err
	}
	resp := make([]SourceItem, len(items))
	for i, item := range items {
		resp[i] = h.sourceItem(item, scope.Revealed)
	}
	return resp, nil
}

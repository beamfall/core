package graph

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Related Media (CORE-2444): the Media that share a Media item's category
// credits, ranked by how many of its credits they share. Only Media sharing at
// least relatedMinShared credits count, so one broad genre never fills the row
// in title order; Extras are read out and one title appears once (CORE-2506).
// Every credit is read through the entity-media Source with the acting
// Profile, the path browse and Home rails take, so Mute suppression, the
// Library and reveal filter and locked marking apply exactly as they do there.
// Only archetypes are named here, never a plugin kind.
const (
	relatedCategoryCredits = 4
	relatedPersonCredits   = 3
	// relatedPerCredit bounds each credit's read; a larger credit contributes
	// its first Media in the Source's order.
	relatedPerCredit    = 100
	relatedDefaultLimit = 20
	relatedMaxLimit     = 50
	// relatedMinShared is the fewest ranking credits a Media must share with
	// the item to be related; one shared genre alone is filler.
	relatedMinShared = 2
)

// relatedTitleKey identifies one title for the Related row: Media with the
// same title (case and surrounding space ignored) and year are separate files
// of one work (copies, cuts, a stray trailer) and are listed once.
func relatedTitleKey(title string, year int) string {
	return strings.ToLower(strings.TrimSpace(title)) + "\x00" + strconv.Itoa(year)
}

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
// one category and relatedMinShared signals, most shared signals first, ties
// in first-read order, at most limit. The item itself, Media with its title
// key and every Media after the first with one title key are left out.
func rankRelated(self store.SourceItem, byCategory, byPerson [][]store.SourceItem, limit int) []store.SourceItem {
	mediaID := self.ID
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
	titles := map[string]bool{relatedTitleKey(self.Title, self.Year): true}
	for _, e := range entries {
		if !e.category || e.score < relatedMinShared {
			continue
		}
		key := relatedTitleKey(e.item.Title, e.item.Year)
		if titles[key] {
			continue
		}
		titles[key] = true
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
	media, err := h.store.Media(ctx, mediaID, filter)
	if err != nil {
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
				Kind:                  "entity-media",
				ExcludeEditionGroupOf: mediaID,
				EntityID:              store.EntityID(id),
				Limit:                 relatedPerCredit,
				ProfileID:             scope.ProfileID,
				// Extras (a trailer, a featurette) are never related titles.
				ExcludeExtras: true,
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
	items := rankRelated(store.SourceItem{ID: media.ID, Title: media.Title, Year: media.Year}, byCategory, byPerson, limit)
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

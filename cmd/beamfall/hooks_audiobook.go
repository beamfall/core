package main

import (
	"github.com/beamfall/core/internal/app"
	books "github.com/beamfall/plugin-audiobook"
)

// booksHydrationMapper turns the books plugin's tag-and-folder plan
// into Core's hydration plan: Person credits, Series membership and Genre
// links on the book's Media (ADR-0055 amendment A). Every plan is marked
// complete. Like every hydration mapper, it runs again only when a file
// changes on disk, and a fresh write adds missing relations without replacing
// fields. Titles do not fold: folding would let two credits for one person
// land on one credited-on row, the later role overwriting the earlier.
type booksHydrationMapper struct{}

func (booksHydrationMapper) MarkerField() string { return books.HydratedField }

func (booksHydrationMapper) ExternalIDSchemes() []string { return nil }

// HydratedMediaFields is empty: the plan writes relations and the completion
// flag, never a Media field, so scan-tier Media values stay the parser's.
func (booksHydrationMapper) HydratedMediaFields() []string { return []string{} }

func (booksHydrationMapper) MapHydration(tags map[string]string, path string) app.HydrationPlan {
	plan := app.HydrationPlan{MarkComplete: true}
	for _, relation := range books.MapHydration(tags, path).Relations {
		plan.Relations = append(plan.Relations, app.HydrationRelation{
			From:       app.HydrationEntity{Kind: relation.From.Kind, Title: relation.From.Title},
			Name:       relation.Name,
			Attributes: relation.Attributes,
			SortOrder:  relation.SortOrder,
		})
	}
	return plan
}

package main

import (
	"github.com/beamfall/core/internal/app"
	books "github.com/beamfall/plugin-audiobook"
)

// audiobookHydrationMapper turns the audiobook plugin's tag-and-folder plan
// into Core's hydration plan: Person credits, Series membership and Genre
// links on the book's Media. Every file is marked complete, so hydration runs
// once per Media and a user's later edits are never overwritten by a rescan
// (ADR-0055 amendment A).
type audiobookHydrationMapper struct{}

func (audiobookHydrationMapper) MarkerField() string { return books.HydratedField }

func (audiobookHydrationMapper) ExternalIDSchemes() []string { return nil }

// HydratedMediaFields is empty: the plan writes relations and the completion
// flag, never a Media field, so scan-tier Media values stay the parser's.
func (audiobookHydrationMapper) HydratedMediaFields() []string { return []string{} }

// TitleFoldKinds lets "J. R. R. Tolkien" and "J.R.R. Tolkien" resolve to one
// Person, and likewise for Series and Genres.
func (audiobookHydrationMapper) TitleFoldKinds() []string {
	return []string{books.KindPerson, books.KindSeries, books.KindGenre}
}

func (audiobookHydrationMapper) MapHydration(tags map[string]string, path string) app.HydrationPlan {
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

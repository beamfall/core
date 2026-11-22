package main

import (
	"github.com/beamfall/core/internal/app"
	tagFolderPlugin "github.com/beamfall/plugin-audiobook"
)

// relationHydrationMapper turns the plugin's tag-and-folder plan
// into Core's hydration plan: Person credits, Series membership and Genre
// links on the Media (ADR-0055 amendment A). Every plan is marked
// complete. Like every hydration mapper, it runs again only when a file
// changes on disk, and a fresh write adds missing relations without replacing
// fields. Titles do not fold: folding would let two credits for one person
// land on one credited-on row, the later role overwriting the earlier.
type relationHydrationMapper struct{}

func (relationHydrationMapper) MarkerField() string { return tagFolderPlugin.HydratedField }

func (relationHydrationMapper) ExternalIDSchemes() []string { return nil }

// HydratedMediaFields is empty: the plan writes relations and the completion
// flag, never a Media field, so scan-tier Media values stay the parser's.
func (relationHydrationMapper) HydratedMediaFields() []string { return []string{} }

func (relationHydrationMapper) MapHydration(tags map[string]string, path string) app.HydrationPlan {
	plan := app.HydrationPlan{MarkComplete: true}
	for _, relation := range tagFolderPlugin.MapHydration(tags, path).Relations {
		plan.Relations = append(plan.Relations, app.HydrationRelation{
			From:       app.HydrationEntity{Kind: relation.From.Kind, Title: relation.From.Title},
			Name:       relation.Name,
			Attributes: relation.Attributes,
			SortOrder:  relation.SortOrder,
		})
	}
	return plan
}

package main

import (
	"github.com/beamfall/core/internal/app"
	books "github.com/beamfall/plugin-audiobook"
)

// audiobookAuthorBrowseRule lists a Person in the plugin's browse (the home
// Authors grid) only when some credit names them author: a narrator-only
// Person stays reachable from their books but is not an author (CORE-3014).
func audiobookAuthorBrowseRule() app.EntityBrowseRule {
	return app.EntityBrowseRule{PluginID: books.PluginID, Kind: books.KindPerson, Sources: []app.EntityBrowseSource{
		{Relation: books.RelationCreditedOn, Attribute: books.AttributeRole, Value: books.RoleAuthor},
	}}
}

// audiobookHydrationMapper turns the plugin's tag-and-folder plan
// into Core's hydration plan: Person credits, Series membership and Genre
// links on the book's Media (ADR-0055 amendment A). Every plan is marked
// complete. Like every hydration mapper, it runs again only when a file
// changes on disk, and a fresh write adds missing relations without replacing
// fields. Titles do not fold: folding would let two credits for one person
// land on one credited-on row, the later role overwriting the earlier.
type audiobookHydrationMapper struct{}

func (audiobookHydrationMapper) MarkerField() string { return books.HydratedField }

func (audiobookHydrationMapper) ExternalIDSchemes() []string { return nil }

// HydratedMediaFields is empty: the plan writes relations and the completion
// flag, never a Media field, so scan-tier Media values stay the parser's.
func (audiobookHydrationMapper) HydratedMediaFields() []string { return []string{} }

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

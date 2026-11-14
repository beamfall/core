package main

import (
	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/store"
	filmtv "github.com/beamfall/plugin-filmtv"
)

// filmtvHydrationMapper groups a local file into the Film & TV plugin's
// container Entities from the path alone, so files with no provider match still
// land in containers. Every file is marked complete, so a rescan does not map it
// again.
//
// The two container kinds are read from the plugin's bundle: groupKind is the
// kind that declares filmtv.SeasonOfRelation and parentKind is that relation's
// target. Core never names them (invariant 1).
type filmtvHydrationMapper struct {
	groupKind  string
	parentKind string
}

func newFilmTVHydrationMapper() filmtvHydrationMapper {
	for _, kind := range filmtv.Bundle().EntityKinds {
		for _, relation := range kind.Relations {
			if relation.Name == filmtv.SeasonOfRelation {
				return filmtvHydrationMapper{groupKind: kind.Name, parentKind: relation.ToKind}
			}
		}
	}
	return filmtvHydrationMapper{}
}

func (filmtvHydrationMapper) MarkerField() string { return filmtv.HydratedField }

func (filmtvHydrationMapper) ExternalIDSchemes() []string { return nil }

// EntityScopes scopes a group Entity by its parent: two parents can each hold
// a group with the same title.
func (m filmtvHydrationMapper) EntityScopes() []app.HydrationEntityScope {
	if m.groupKind == "" {
		return nil
	}
	return []app.HydrationEntityScope{{Kind: m.groupKind, Relation: filmtv.SeasonOfRelation}}
}

func (m filmtvHydrationMapper) MapHydration(_ map[string]string, path string) app.HydrationPlan {
	plan := app.HydrationPlan{MarkComplete: true}
	local := filmtv.LocalHydration(path)
	// LocalHydration leaves the titles empty for files it does not group.
	if m.groupKind == "" || local.SeriesTitle == "" || local.SeasonTitle == "" {
		return plan
	}
	group := app.HydrationEntity{
		Kind:  m.groupKind,
		Title: local.SeasonTitle,
		Scope: local.SeriesTitle,
		Fields: []store.EntityField{
			{Name: "number", Type: store.EntityFieldNumber, Number: float64(local.SeasonNumber)},
		},
	}
	if local.SeasonNumber == 0 {
		group.Fields = append(group.Fields, store.EntityField{Name: "specials", Type: store.EntityFieldFlag, Flag: true})
	}
	mediaOrder := local.EpisodeIndex
	groupOrder := local.SeasonNumber
	plan.Relations = append(plan.Relations,
		app.HydrationRelation{
			From:        group,
			Name:        filmtv.SeasonEpisodeRelation,
			Cardinality: "one",
			SortOrder:   &mediaOrder,
		},
		app.HydrationRelation{
			From:             group,
			Name:             filmtv.SeasonOfRelation,
			RequiresRelation: filmtv.SeasonEpisodeRelation,
			ToEntity:         &app.HydrationEntity{Kind: m.parentKind, Title: local.SeriesTitle},
			SortOrder:        &groupOrder,
		},
	)
	return plan
}

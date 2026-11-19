package main

import (
	"path/filepath"
	"regexp"
	"strings"

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

// HydratedMediaFields is empty: the plan carries Entities, relations and the
// completion flag but no Media field, so older scan-tier Media values are the
// filename parser's to reconcile (CORE-2554).
func (filmtvHydrationMapper) HydratedMediaFields() []string { return []string{} }

// EntityScopes scopes a group Entity by its parent: two parents can each hold
// a group with the same title.
func (m filmtvHydrationMapper) EntityScopes() []app.HydrationEntityScope {
	if m.groupKind == "" {
		return nil
	}
	return []app.HydrationEntityScope{{Kind: m.groupKind, Relation: filmtv.SeasonOfRelation}}
}

// TitleFoldKinds lets a rehydrated file merge the containers an older scan
// split by file-name spelling into the ones parentTitle now names
// (CORE-2459).
func (m filmtvHydrationMapper) TitleFoldKinds() []string {
	if m.groupKind == "" {
		return nil
	}
	return []string{m.groupKind, m.parentKind}
}

func (m filmtvHydrationMapper) MapHydration(_ map[string]string, path string) app.HydrationPlan {
	plan := app.HydrationPlan{MarkComplete: true}
	local := filmtv.LocalHydration(path)
	// LocalHydration leaves the titles empty for files it does not group.
	if m.groupKind == "" || local.SeriesTitle == "" || local.SeasonTitle == "" {
		return plan
	}
	parent := parentTitle(path, local.SeriesTitle)
	group := app.HydrationEntity{
		Kind:  m.groupKind,
		Title: local.SeasonTitle,
		Scope: parent,
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
			ToEntity:         &app.HydrationEntity{Kind: m.parentKind, Title: parent},
			SortOrder:        &groupOrder,
		},
	)
	return plan
}

// parentFolderDepth is how many enclosing folders parentTitle searches: the
// file's own folder, the one above it (a numbered sub-folder) and one more
// (a disc folder inside that).
const parentFolderDepth = 3

// trailingFolderTag matches the year, id or edition tags a folder name can end
// with: "(2020)", "[tvdb-123]", "{imdb-tt1}".
var trailingFolderTag = regexp.MustCompile(`\s*(\(\d{4}\)|\[[^\]]*\]|\{[^}]*\})\s*$`)

// parentTitle is the parent container's title for a file whose name parses to
// parsed. The parsed title is the file name's spelling, which can differ from
// file to file in case and punctuation ("gangs.of.london.s01e01.mkv" beside
// "Gangs of London - S02E01.mkv"), and the container's identity is its title,
// so the nearest enclosing folder whose name folds to the parsed title
// (store.FoldEntityTitle, after trailing tags) names the container instead:
// every file in that folder then lands in one container. A file in no such
// folder keeps the parsed title.
func parentTitle(path, parsed string) string {
	want := store.FoldEntityTitle(parsed)
	if want == "" {
		return parsed
	}
	dir := filepath.Dir(path)
	for range parentFolderDepth {
		name := filepath.Base(dir)
		if name == "." || name == string(filepath.Separator) || name == "" {
			break
		}
		for {
			trimmed := strings.TrimSpace(trailingFolderTag.ReplaceAllString(name, ""))
			if store.FoldEntityTitle(trimmed) == want {
				return trimmed
			}
			if trimmed == name || trimmed == "" {
				break
			}
			name = trimmed
		}
		if store.FoldEntityTitle(filepath.Base(dir)) == want {
			return strings.TrimSpace(filepath.Base(dir))
		}
		dir = filepath.Dir(dir)
	}
	return parsed
}

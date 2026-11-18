//go:build !beamfall_appstore

package main

import (
	"testing"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/store"
	filmtv "github.com/beamfall/plugin-filmtv"
)

func TestFilmTVHooksRegisterHydration(t *testing.T) {
	mapper := firstPartyHooks().HydrationMappers[filmtv.PluginID]
	if mapper == nil {
		t.Fatal("Film/TV hydration hook missing")
	}
	if mapper.MarkerField() != filmtv.HydratedField {
		t.Fatalf("marker field = %q, want %q", mapper.MarkerField(), filmtv.HydratedField)
	}
	scoped, ok := mapper.(app.HydrationScopeMapper)
	if !ok {
		t.Fatal("Film/TV hydration mapper declares no entity scopes")
	}
	scopes := scoped.EntityScopes()
	if len(scopes) != 1 || scopes[0] != (app.HydrationEntityScope{Kind: "season", Relation: filmtv.SeasonOfRelation}) {
		t.Fatalf("entity scopes = %+v", scopes)
	}
}

// An episode with no provider match lands in a season of its show: the plan
// carries a season scoped by the series, holding the Media in episode order,
// and a season-of relation to the show, every name and type declared by the
// real bundle.
func TestFilmTVHydrationGroupsEpisodeIntoSeasonAndShow(t *testing.T) {
	plan := firstPartyHooks().HydrationMappers[filmtv.PluginID].MapHydration(nil, "/tv/Show/Season 1/Show - S01E02 - Pilot.mkv")
	if !plan.MarkComplete {
		t.Fatal("episode plan not marked complete")
	}
	assertFilmTVPlanDeclared(t, plan)
	relations := map[string]app.HydrationRelation{}
	for _, relation := range plan.Relations {
		relations[relation.Name] = relation
	}
	if len(relations) != 2 {
		t.Fatalf("relations = %+v, want season-episode and season-of", plan.Relations)
	}
	episode := relations[filmtv.SeasonEpisodeRelation]
	if episode.From.Kind != "season" || episode.From.Title != "Season 1" || episode.From.Scope != "Show" {
		t.Fatalf("season-episode from = %+v", episode.From)
	}
	if episode.ToEntity != nil || episode.SortOrder == nil || *episode.SortOrder != 2 {
		t.Fatalf("season-episode = %+v, want the Media at order 2", episode)
	}
	if !hasNumberField(episode.From.Fields, "number", 1) {
		t.Fatalf("season fields = %+v, want number 1", episode.From.Fields)
	}
	of := relations[filmtv.SeasonOfRelation]
	if of.ToEntity == nil || of.ToEntity.Kind != "show" || of.ToEntity.Title != "Show" || of.ToEntity.Scope != "" {
		t.Fatalf("season-of target = %+v", of.ToEntity)
	}
	if of.RequiresRelation != filmtv.SeasonEpisodeRelation || of.From.Scope != "Show" {
		t.Fatalf("season-of = %+v", of)
	}

	// Another show's Season 1 is another season.
	other := firstPartyHooks().HydrationMappers[filmtv.PluginID].MapHydration(nil, "/tv/Other/Season 1/Other - S01E01.mkv")
	if other.Relations[0].From.Scope != "Other" {
		t.Fatalf("other show's season scope = %q", other.Relations[0].From.Scope)
	}
}

func TestFilmTVHydrationFlagsSpecials(t *testing.T) {
	plan := firstPartyHooks().HydrationMappers[filmtv.PluginID].MapHydration(nil, "/tv/Show/Specials/Show - S00E01 - Extra.mkv")
	assertFilmTVPlanDeclared(t, plan)
	if len(plan.Relations) == 0 {
		t.Fatal("special episode not grouped")
	}
	season := plan.Relations[0].From
	if season.Title != "Specials" || !hasNumberField(season.Fields, "number", 0) {
		t.Fatalf("specials season = %+v", season)
	}
	flagged := false
	for _, field := range season.Fields {
		flagged = flagged || (field.Name == "specials" && field.Type == store.EntityFieldFlag && field.Flag)
	}
	if !flagged {
		t.Fatalf("specials season fields = %+v, want the specials flag", season.Fields)
	}
}

func TestFilmTVHydrationMarksMovieComplete(t *testing.T) {
	plan := firstPartyHooks().HydrationMappers[filmtv.PluginID].MapHydration(nil, "/films/Heat (1995)/Heat (1995).mkv")
	if !plan.MarkComplete || len(plan.Relations) != 0 || len(plan.MediaFields) != 0 {
		t.Fatalf("movie plan = %+v, want marked complete with nothing written", plan)
	}
}

func hasNumberField(fields []store.EntityField, name string, number float64) bool {
	for _, field := range fields {
		if field.Name == name && field.Type == store.EntityFieldNumber && field.Number == number {
			return true
		}
	}
	return false
}

// assertFilmTVPlanDeclared checks a plan against the Film & TV bundle the way
// Core's hydration adapter does before it writes: marker, relation names,
// cardinalities, targets, entity kinds, field types and the scope relation.
func assertFilmTVPlanDeclared(t *testing.T, plan app.HydrationPlan) {
	t.Helper()
	bundle := filmtv.Bundle()
	markerDeclared := false
	for _, field := range bundle.MediaFields {
		markerDeclared = markerDeclared || (field.Name == filmtv.HydratedField && field.Type == "flag")
	}
	if !markerDeclared {
		t.Fatalf("marker %q is not a declared flag Media field", filmtv.HydratedField)
	}
	fieldTypes := map[string]map[string]string{}
	relations := map[string]struct{ to, cardinality string }{}
	for _, kind := range bundle.EntityKinds {
		fieldTypes[kind.Name] = map[string]string{}
		for _, field := range kind.Fields {
			fieldTypes[kind.Name][field.Name] = field.Type
		}
		for _, relation := range kind.Relations {
			relations[kind.Name+" "+relation.Name] = struct{ to, cardinality string }{relation.ToKind, relation.Cardinality}
		}
	}
	checkEntity := func(entity app.HydrationEntity) {
		declared, ok := fieldTypes[entity.Kind]
		if !ok {
			t.Fatalf("entity kind %q is not declared", entity.Kind)
		}
		for _, field := range entity.Fields {
			if declared[field.Name] != string(field.Type) {
				t.Fatalf("field %s.%s has type %q, declared %q", entity.Kind, field.Name, field.Type, declared[field.Name])
			}
		}
		if entity.Scope != "" {
			scope, ok := relations[entity.Kind+" "+filmtv.SeasonOfRelation]
			if entity.Kind != "season" || !ok || scope.to != "show" {
				t.Fatalf("scoped entity %+v has no declared scope relation", entity)
			}
		}
	}
	for _, relation := range plan.Relations {
		declared, ok := relations[relation.From.Kind+" "+relation.Name]
		if !ok {
			t.Fatalf("relation %q from %q is not declared", relation.Name, relation.From.Kind)
		}
		if relation.Cardinality != declared.cardinality {
			t.Fatalf("relation %q cardinality %q, declared %q", relation.Name, relation.Cardinality, declared.cardinality)
		}
		target := "media"
		if relation.ToEntity != nil {
			target = relation.ToEntity.Kind
			checkEntity(*relation.ToEntity)
		}
		if target != declared.to {
			t.Fatalf("relation %q targets %q, declared %q", relation.Name, target, declared.to)
		}
		if relation.RequiresRelation != "" {
			if _, ok := relations[relation.From.Kind+" "+relation.RequiresRelation]; !ok {
				t.Fatalf("prerequisite %q from %q is not declared", relation.RequiresRelation, relation.From.Kind)
			}
		}
		checkEntity(relation.From)
	}
}

// CORE-2459: one show folder whose episode file names spell the series title
// in different case and punctuation is one show. The Tower's
// "Gangs of London" folder held lower-case dotted names in "Season 1" and
// differently spelled names in "Season 02" and "Season 3", and scanned as
// three shows. The folder now names the show and scopes its seasons.
func TestFilmTVHydrationNamesShowFromItsFolder(t *testing.T) {
	mapper := firstPartyHooks().HydrationMappers[filmtv.PluginID]
	paths := []string{
		"/mnt/user/tv/Gangs of London/Season 1/gangs.of.london.s01e01.part.1.2160p.web.h265-ggwp.mkv",
		"/mnt/user/tv/Gangs of London/Season 1/gangs.of.london.s01e02.part.2.2160p.web.h265-ggwp.mkv",
		"/mnt/user/tv/Gangs of London/Season 02/Gangs.Of.London.S02E01.1080p.WEB.H264-GLHF.mkv",
		"/mnt/user/tv/Gangs of London/Season 3/GANGS OF LONDON - S03E01 - Episode 1.mkv",
		"/mnt/user/tv/Gangs of London (2020)/Season 3/gangs_of_london_s03e02.mkv",
	}
	for _, path := range paths {
		plan := mapper.MapHydration(nil, path)
		assertFilmTVPlanDeclared(t, plan)
		var show *app.HydrationEntity
		for _, relation := range plan.Relations {
			if relation.Name == filmtv.SeasonOfRelation {
				show = relation.ToEntity
			}
			if relation.From.Scope != "Gangs of London" {
				t.Errorf("%s: season scope = %q, want the folder's title", path, relation.From.Scope)
			}
		}
		if show == nil || show.Title != "Gangs of London" {
			t.Errorf("%s: show = %+v, want \"Gangs of London\"", path, show)
		}
	}

	// A folder that does not name the series keeps the file name's title.
	plan := mapper.MapHydration(nil, "/tv/Unsorted/ted.lasso.s01e01.mkv")
	want := filmtv.LocalHydration("/tv/Unsorted/ted.lasso.s01e01.mkv").SeriesTitle
	found := false
	for _, relation := range plan.Relations {
		if relation.Name == filmtv.SeasonOfRelation {
			found = true
			if relation.ToEntity == nil || relation.ToEntity.Title != want || want == "Unsorted" {
				t.Errorf("unsorted show = %+v, want the parsed title %q", relation.ToEntity, want)
			}
		}
	}
	if !found {
		t.Fatalf("unsorted plan = %+v, want a season-of relation", plan.Relations)
	}
}

//go:build !beamfall_appstore

package main

import (
	"testing"

	"github.com/beamfall/core/internal/app"
	books "github.com/beamfall/plugin-audiobook"
)

func TestAudiobookHooksRegisterHydration(t *testing.T) {
	mapper := firstPartyHooks().HydrationMappers[books.PluginID]
	if mapper == nil {
		t.Fatal("audiobook hydration hook missing")
	}
	if mapper.MarkerField() != books.HydratedField {
		t.Fatalf("marker field = %q, want %q", mapper.MarkerField(), books.HydratedField)
	}
	if fields := mapper.(app.HydrationMediaFieldsMapper).HydratedMediaFields(); len(fields) != 0 {
		t.Fatalf("hydrated Media fields = %v, want none", fields)
	}
}

// A tagged book maps to author and narrator credits, a series with its string
// sequence and a genre, every kind, relation and attribute declared by the
// real bundle, and is marked complete so it hydrates once.
func TestAudiobookHydrationMapsCreditsSeriesAndGenre(t *testing.T) {
	tags := map[string]string{
		"album_artist": "Ursula K. Le Guin",
		"narrator":     "Rob Inglis",
		"series":       "Earthsea",
		"series-part":  "4.5",
		"genre":        "Fantasy",
	}
	plan := firstPartyHooks().HydrationMappers[books.PluginID].MapHydration(tags, "/books/Le Guin/Tales from Earthsea/01.m4b")
	if !plan.MarkComplete {
		t.Fatal("plan not marked complete")
	}
	declared := map[string]map[string]bool{}
	for _, kind := range books.Bundle().EntityKinds {
		for _, relation := range kind.Relations {
			attrs := map[string]bool{}
			for _, attr := range relation.Attributes {
				attrs[attr] = true
			}
			declared[kind.Name+" "+relation.Name] = attrs
		}
	}
	got := map[string]app.HydrationRelation{}
	for _, relation := range plan.Relations {
		attrs, ok := declared[relation.From.Kind+" "+relation.Name]
		if !ok {
			t.Fatalf("relation %q from %q is not declared", relation.Name, relation.From.Kind)
		}
		for attr := range relation.Attributes {
			if !attrs[attr] {
				t.Fatalf("attribute %q on %q is not declared", attr, relation.Name)
			}
		}
		if relation.ToEntity != nil {
			t.Fatalf("relation %q targets an Entity, want the Media", relation.Name)
		}
		got[relation.From.Title] = relation
	}
	if r := got["Ursula K. Le Guin"]; r.Attributes[books.AttributeRole] != books.RoleAuthor {
		t.Fatalf("author credit = %+v", r)
	}
	if r := got["Rob Inglis"]; r.Attributes[books.AttributeRole] != books.RoleNarrator {
		t.Fatalf("narrator credit = %+v", r)
	}
	if r := got["Earthsea"]; r.Name != books.RelationContainsBook || r.Attributes[books.AttributeSequence] != "4.5" {
		t.Fatalf("series = %+v", r)
	}
	if r := got["Fantasy"]; r.Name != books.RelationGenreOf {
		t.Fatalf("genre = %+v", r)
	}

	// An untagged file in a plain folder still completes, with nothing written.
	empty := firstPartyHooks().HydrationMappers[books.PluginID].MapHydration(nil, "/books/Untitled/01.mp3")
	if !empty.MarkComplete || len(empty.Relations) != 0 {
		t.Fatalf("untagged plan = %+v", empty)
	}
}

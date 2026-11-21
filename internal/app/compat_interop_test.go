package app

import (
	"reflect"
	"testing"

	"github.com/beamfall/core/internal/compat"
	"github.com/beamfall/core/internal/plugin"
	sdk "github.com/beamfall/plugin-sdk"
)

func TestCompatInteropFlattensBundleDeclaration(t *testing.T) {
	bundle := testBundle("interop-fixture")
	bundle.MediaFields = append(bundle.MediaFields, plugin.FieldSpec{Name: "class", Type: "text"})
	bundle.EntityKinds = []plugin.EntityKind{
		{Name: "outer", Archetype: "container", Relations: []plugin.RelationSpec{
			{Name: "outer-media", ToKind: plugin.MediaKindName, Cardinality: "many"},
		}},
		{Name: "inner", Archetype: "container", Relations: []plugin.RelationSpec{
			{Name: "member", ToKind: plugin.MediaKindName, Cardinality: "one"},
			{Name: "member-of", ToKind: "outer"},
		}},
	}
	bundle.Scrapers = []plugin.ScraperSpec{
		{ID: "by-id", DisplayName: plugin.L("By ID"), Kind: "http-json", Endpoint: "https://example.invalid",
			LookupTemplate:     "/item/{id}",
			LookupByIDTemplate: map[string]string{"tvdb": "/item/{id}", "imdb": "/other/{id}"}},
		{ID: "unmatched", DisplayName: plugin.L("Unmatched"), Kind: "http-json", Endpoint: "https://example.invalid",
			LookupTemplate:     "/record/{id}",
			LookupByIDTemplate: map[string]string{"imdb": "/find/{id}"}},
	}
	bundle.FamilyScopes = []sdk.FamilyScopeSpec{{Value: "scope-a", DisplayName: plugin.L("Scope A"), Default: true}}
	bundle.Interop = &sdk.InteropSpec{
		MediaField: "class",
		Media:      []sdk.InteropMapping{{Value: "single", Type: "Movie"}},
		Kinds:      []sdk.InteropMapping{{Value: "outer", Type: "Series"}, {Value: "inner", Type: "Season"}},
		Scopes:     []sdk.InteropMapping{{Value: "scope-a", Type: "tvshows"}},
	}
	plain := testBundle("no-interop")
	reg, err := plugin.NewRegistry([]plugin.Bundle{bundle, plain})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	got := compatInterop(reg)
	want := map[string]compat.Interop{"interop-fixture": {
		MediaField:      "class",
		MediaTypes:      map[string]string{"single": "Movie"},
		KindTypes:       map[string]string{"outer": "Series", "inner": "Season"},
		ScopeTypes:      map[string]string{"scope-a": "tvshows"},
		MemberRelations: map[string]bool{compat.MemberRelationKey("inner", "member"): true},
		ParentRelations: map[string]bool{compat.ParentRelationKey("inner", "member-of", "outer"): true},
		ProviderSchemes: map[string]string{"by-id": "tvdb"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("compatInterop = %#v, want %#v", got, want)
	}
	if got := compatInterop(nil); len(got) != 0 {
		t.Fatalf("compatInterop(nil) = %#v, want empty", got)
	}
}

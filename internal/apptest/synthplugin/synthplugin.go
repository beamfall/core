// Package synthplugin provides a synthetic, domain-agnostic plugin Bundle for
// the in-process app test harness. It carries ZERO domain vocabulary — only
// neutral, shape-named test terms ("Item", "Collection", "Sample") — so the
// vocabulary firewall (internal/conformance vocab gate) stays satisfied even
// though this file is scanned as ordinary (non-test) source.
//
// The Bundle is the MINIMAL shape that passes plugin.Validate: a slug ID, a
// DisplayName, a MediaLabel, declared MediaClasses, and exactly one "browse"
// Layout. It deliberately imports ONLY the plugin SDK — never a real domain
// plugin module and never internal/app — so the harness's default plugin is a
// neutral fixture, not a product surface.
package synthplugin

import (
	"github.com/beamfall/core/internal/apptest/plugintest"
	sdk "github.com/beamfall/plugin-sdk"
)

// Bundle returns a minimal, valid, domain-agnostic plugin Bundle for tests.
func Bundle() sdk.Bundle {
	return plugintest.Compatible(sdk.Bundle{
		ID:           "synth",
		Version:      "0.1.0",
		DisplayName:  sdk.LocaleString{"en": "Synthetic Test Plugin"},
		MediaLabel:   sdk.LocaleString{"en": "Item"},
		MediaClasses: []string{"video", "audio", "image"},
		MediaFields: []sdk.FieldSpec{
			{Name: "class", Type: "text",
				Label: sdk.LocaleString{"en": "Class"}},
			{Name: "label", Type: "text", Searchable: true,
				Label: sdk.LocaleString{"en": "Label"}},
		},
		EntityKinds: []sdk.EntityKind{
			{
				Name:        "collection",
				DisplayName: sdk.LocaleString{"en": "Collection"},
				Archetype:   "container",
				Relations: []sdk.RelationSpec{
					{Name: "contains", ToKind: sdk.MediaKindName},
				},
			},
		},
		Layouts: []sdk.LayoutSpec{{ID: "home", Role: "browse", Sections: []sdk.SectionSpec{
			{ID: "recently-added", Kind: "shelf",
				Title:       sdk.LocaleString{"en": "Recently Added"},
				ArtworkSlot: "thumb", Source: sdk.SourceSpec{Kind: "recently-added"}},
			{ID: "collections", Kind: "grid",
				Title:       sdk.LocaleString{"en": "Collections"},
				ArtworkSlot: "poster", Source: sdk.SourceSpec{Kind: "entity-items", EntityKind: "collection"}},
			{ID: "all-items", Kind: "grid",
				Title:       sdk.LocaleString{"en": "All Items"},
				ArtworkSlot: "thumb", Source: sdk.SourceSpec{Kind: "all-items"}},
		}}},
	})
}

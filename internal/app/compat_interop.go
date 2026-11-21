package app

import (
	"github.com/beamfall/core/internal/compat"
	"github.com/beamfall/core/internal/plugin"
)

// compatInterop flattens each executable bundle's interop declaration into the
// compatibility catalog's view of it (ADR-0171 §2.8). Member relations are the
// ones a mapped container Kind holds one Media by; parent relations the ones
// it climbs to another mapped container Kind by. A scraper's provider scheme
// is the by-ID lookup key whose template is its own lookup template, so its
// stored candidate IDs are that scheme's IDs. Core names no token (invariant #1).
func compatInterop(reg *plugin.Registry) map[string]compat.Interop {
	out := map[string]compat.Interop{}
	if reg == nil {
		return out
	}
	for _, id := range reg.IDs() {
		bundle, ok := reg.Bundle(id)
		if !ok || bundle.Interop == nil {
			continue
		}
		spec := bundle.Interop
		interop := compat.Interop{
			MediaField: spec.MediaField,
			MediaTypes: map[string]string{}, KindTypes: map[string]string{}, ScopeTypes: map[string]string{},
			MemberRelations: map[string]bool{}, ParentRelations: map[string]bool{}, ProviderSchemes: map[string]string{},
		}
		for _, mapping := range spec.Media {
			interop.MediaTypes[mapping.Value] = mapping.Type
		}
		for _, mapping := range spec.Kinds {
			interop.KindTypes[mapping.Value] = mapping.Type
		}
		for _, mapping := range spec.Scopes {
			interop.ScopeTypes[mapping.Value] = mapping.Type
		}
		for _, kind := range bundle.EntityKinds {
			if interop.KindTypes[kind.Name] == "" {
				continue
			}
			for _, relation := range kind.Relations {
				switch {
				case relation.ToKind == plugin.MediaKindName && relation.Cardinality == "one":
					interop.MemberRelations[compat.MemberRelationKey(kind.Name, relation.Name)] = true
				case relation.ToKind != kind.Name && interop.KindTypes[relation.ToKind] != "":
					interop.ParentRelations[compat.ParentRelationKey(kind.Name, relation.Name, relation.ToKind)] = true
				}
			}
		}
		for _, scraper := range bundle.Scrapers {
			if scraper.LookupTemplate == "" {
				continue
			}
			for scheme, template := range scraper.LookupByIDTemplate {
				if template == scraper.LookupTemplate {
					interop.ProviderSchemes[scraper.ID] = scheme
				}
			}
		}
		out[string(id)] = interop
	}
	return out
}

package app

import (
	"log/slog"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/plugin"
)

// EntityPosterRule is a plugin wiring's declaration that one of its Entity
// kinds, when it has no poster of its own, inherits one from its sources.
type EntityPosterRule = artwork.EntityPosterRule

// EntityPosterSource is one ordered source of an EntityPosterRule.
type EntityPosterSource = artwork.EntityPosterSource

// activeEntityPosterRules keeps the rules of activated plugins whose kind
// and source relations the plugin's bundle really declares, in declared
// order. A rule naming something undeclared is a wiring fault: it is dropped
// whole and logged rather than left to match nothing silently.
func activeEntityPosterRules(registry *plugin.Registry, rules []EntityPosterRule, logger *slog.Logger) []EntityPosterRule {
	if registry == nil {
		return nil
	}
	var active []EntityPosterRule
	for _, rule := range rules {
		bundle, ok := registry.Bundle(plugin.ID(rule.PluginID))
		if !ok {
			continue
		}
		if reason := entityPosterRuleFault(bundle, rule); reason != "" {
			if logger != nil {
				logger.Warn("entity poster rule dropped", "plugin", rule.PluginID, "kind", rule.Kind, "reason", reason)
			}
			continue
		}
		active = append(active, rule)
	}
	return active
}

const (
	// categoryArchetype marks the Entity kinds a plugin declares for
	// grouping its Media. One with no artwork of its own reads as a blank
	// card, so it derives a mosaic from what it groups unless the wiring
	// declares a rule for it.
	categoryArchetype = "category"
	// containerArchetype marks the Entity kinds that hold Media; their
	// posters are the most distinct covers a category can offer.
	containerArchetype = "container"
)

// withCategoryPosterRules appends, after the declared rules, a mosaic rule
// for every category-archetype kind of an activated plugin that has none:
// first the container Entities that point at the category, then the Media it
// relates to, both through the relations the bundle declares.
func withCategoryPosterRules(registry *plugin.Registry, rules []EntityPosterRule) []EntityPosterRule {
	ruled := map[string]bool{}
	for _, rule := range rules {
		ruled[rule.PluginID+"\x00"+rule.Kind] = true
	}
	for _, id := range registry.IDs() {
		bundle, ok := registry.Bundle(id)
		if !ok {
			continue
		}
		for _, kind := range bundle.EntityKinds {
			if kind.Archetype != categoryArchetype || ruled[string(id)+"\x00"+kind.Name] {
				continue
			}
			rule := EntityPosterRule{PluginID: string(id), Kind: kind.Name, Mosaic: true}
			for _, from := range bundle.EntityKinds {
				if from.Archetype != containerArchetype {
					continue
				}
				for _, rel := range from.Relations {
					if rel.ToKind == kind.Name {
						rule.Sources = append(rule.Sources, EntityPosterSource{Relation: rel.Name, FromKind: from.Name})
					}
				}
			}
			for _, rel := range kind.Relations {
				if rel.ToKind == plugin.MediaKindName {
					rule.Sources = append(rule.Sources, EntityPosterSource{Relation: rel.Name})
				}
			}
			if len(rule.Sources) > 0 {
				rules = append(rules, rule)
			}
		}
	}
	return rules
}

func entityPosterRuleFault(bundle plugin.Bundle, rule EntityPosterRule) string {
	if _, declared := declaredEntityFieldTypes(bundle)[rule.Kind]; !declared {
		return "undeclared entity kind"
	}
	if len(rule.Sources) == 0 {
		return "no sources"
	}
	specs := declaredRelationSpecs(bundle)
	for _, source := range rule.Sources {
		fromKind, toKind := rule.Kind, plugin.MediaKindName
		if source.FromKind != "" {
			fromKind, toKind = source.FromKind, rule.Kind
		}
		spec, declared := specs[fromKind+"\x00"+source.Relation]
		if !declared || spec.ToKind != toKind {
			return "relation " + source.Relation + " from " + fromKind + " to " + toKind + " is not declared"
		}
	}
	return ""
}

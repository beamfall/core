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

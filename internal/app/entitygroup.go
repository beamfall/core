package app

import (
	"log/slog"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/plugin"
)

// EntityGroupRule is a plugin wiring's declaration that one of its Entity
// kinds splits into groups by a text field when listed among an Entity's
// related Entities (CORE-2769). Groups lists the field's known values in
// display order with their headings; core never interprets the values.
type EntityGroupRule struct {
	PluginID string
	Kind     string
	Field    string
	Groups   []EntityGroupValue
}

// EntityGroupValue is one known value of an EntityGroupRule's field.
type EntityGroupValue = graph.EntityGroupValue

// activeEntityGroupRules keeps the rules of activated plugins whose kind
// declares the rule's field as text, keyed by plugin then kind. A rule naming
// something undeclared is a wiring fault: it is dropped and logged.
func activeEntityGroupRules(registry *plugin.Registry, rules []EntityGroupRule, logger *slog.Logger) map[plugin.ID]map[string]graph.EntityGroupRule {
	if registry == nil {
		return nil
	}
	active := make(map[plugin.ID]map[string]graph.EntityGroupRule)
	for _, rule := range rules {
		id := plugin.ID(rule.PluginID)
		bundle, ok := registry.Bundle(id)
		if !ok {
			continue
		}
		fields, declared := declaredEntityFieldTypes(bundle)[rule.Kind]
		if !declared || fields[rule.Field] != "text" {
			if logger != nil {
				logger.Warn("entity group rule dropped", "plugin", id, "kind", rule.Kind, "field", rule.Field)
			}
			continue
		}
		if active[id] == nil {
			active[id] = make(map[string]graph.EntityGroupRule)
		}
		active[id][rule.Kind] = graph.EntityGroupRule{Field: rule.Field, Groups: rule.Groups}
	}
	return active
}

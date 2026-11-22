package app

import (
	"log/slog"
	"slices"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// EntityBrowseRule restricts one Entity kind in a plugin's Library browse.
type EntityBrowseRule struct {
	PluginID string
	Kind     string
	Sources  []EntityBrowseSource
}

// EntityBrowseSource is one alternative way an Entity qualifies for browse.
type EntityBrowseSource = store.EntityBrowseSource

func activeEntityBrowseRules(registry *plugin.Registry, rules []EntityBrowseRule, logger *slog.Logger) map[plugin.ID]map[string][]store.EntityBrowseSource {
	active := make(map[plugin.ID]map[string][]store.EntityBrowseSource)
	for _, rule := range rules {
		id := plugin.ID(rule.PluginID)
		bundle, ok := registry.Bundle(id)
		if !ok {
			continue
		}
		if !validEntityBrowseRule(bundle, rule) {
			if logger != nil {
				logger.Warn("invalid entity browse rule dropped", "plugin", id, "kind", rule.Kind)
			}
			continue
		}
		if active[id] == nil {
			active[id] = make(map[string][]store.EntityBrowseSource)
		}
		active[id][rule.Kind] = append(active[id][rule.Kind], rule.Sources...)
	}
	return active
}

func validEntityBrowseRule(bundle plugin.Bundle, rule EntityBrowseRule) bool {
	if _, declared := declaredEntityFieldTypes(bundle)[rule.Kind]; !declared || len(rule.Sources) == 0 {
		return false
	}
	relations := declaredRelationSpecs(bundle)
	for _, source := range rule.Sources {
		fromKind, toKind := source.FromKind, rule.Kind
		if fromKind == "" {
			fromKind, toKind = rule.Kind, plugin.MediaKindName
		}
		spec := relations[fromKind+"\x00"+source.Relation]
		if spec.ToKind != toKind {
			return false
		}
		if (source.Attribute == "") != (source.Value == "") ||
			(source.Attribute != "" && !slices.Contains(spec.Attributes, source.Attribute)) {
			return false
		}
		if source.ExcludeMediaRelation != "" {
			if source.FromKind != "" {
				return false
			}
			declared := false
			for _, kind := range bundle.EntityKinds {
				if relations[kind.Name+"\x00"+source.ExcludeMediaRelation].ToKind == plugin.MediaKindName {
					declared = true
					break
				}
			}
			if !declared {
				return false
			}
		}
	}
	return true
}

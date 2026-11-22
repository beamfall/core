package graph

import (
	"cmp"
	"context"
	"slices"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// EntityGroupRule splits one Entity kind in entity(id)'s related groups by
// the value of one of its text fields (CORE-2769): a plugin's wiring names
// the field and the values it knows, in display order, with their headings.
// Options.EntityGroupRules holds them keyed by plugin then kind.
// Core never interprets the values; a value the rule does not list still
// gets its own group, after the listed ones, headed by the value itself.
type EntityGroupRule struct {
	Field  string
	Groups []EntityGroupValue
}

// EntityGroupValue is one known value of an EntityGroupRule's field and the
// heading over its group.
type EntityGroupValue struct {
	Value string
	Label plugin.LocaleString
}

func (r EntityGroupRule) label(value string) plugin.LocaleString {
	for _, group := range r.Groups {
		if group.Value == value {
			return group.Label
		}
	}
	return nil
}

func (r EntityGroupRule) rank(value string) int {
	if value == "" {
		return -1
	}
	for i, group := range r.Groups {
		if group.Value == value {
			return i
		}
	}
	return len(r.Groups)
}

// entityGroupRulesFor returns the group rules of the plugin owning the
// Library, keyed by kind; nil when it has none.
func (h *Handler) entityGroupRulesFor(ctx context.Context, libraryID store.LibraryID) map[string]EntityGroupRule {
	if len(h.entityGroupRules) == 0 {
		return nil
	}
	pluginID, err := h.store.LibraryPluginID(ctx, libraryID)
	if err != nil || pluginID == "" {
		return nil
	}
	return h.entityGroupRules[plugin.ID(pluginID)]
}

// sortNewestFirst orders a ruled kind's cards by year, newest first; cards
// without a year keep their order after the dated ones.
func sortNewestFirst(items []SourceItem) {
	slices.SortStableFunc(items, func(a, b SourceItem) int {
		if (a.Year == 0) != (b.Year == 0) {
			if a.Year == 0 {
				return 1
			}
			return -1
		}
		return cmp.Compare(b.Year, a.Year)
	})
}

// relatedEntityPart is one store group of a kind, split by a rule's field
// value, before mergeRuledParts folds a kind's parts into one wire group.
type relatedEntityPart struct {
	value string
	info  RelatedEntityGroupInfo
}

// mergeRuledParts folds each ruled kind's parts (contiguous, as the store
// returns a kind's parts together) into one group whose Sections follow rule
// order — Entities without a value first, then the listed values, then
// unlisted ones by value — so clients that ignore Sections still see one
// group per kind. Unruled groups pass through unchanged.
func mergeRuledParts(parts []relatedEntityPart, rules map[string]EntityGroupRule) []RelatedEntityGroupInfo {
	var out []RelatedEntityGroupInfo
	for start := 0; start < len(parts); {
		end := start + 1
		for end < len(parts) && parts[end].info.Kind == parts[start].info.Kind {
			end++
		}
		rule, ruled := rules[parts[start].info.Kind]
		if !ruled {
			for _, part := range parts[start:end] {
				out = append(out, part.info)
			}
			start = end
			continue
		}
		run := parts[start:end]
		slices.SortStableFunc(run, func(a, b relatedEntityPart) int {
			return cmp.Or(cmp.Compare(rule.rank(a.value), rule.rank(b.value)), cmp.Compare(a.value, b.value))
		})
		merged := run[0].info
		merged.Items = nil
		for _, part := range run {
			merged.Items = append(merged.Items, part.info.Items...)
			merged.Sections = append(merged.Sections, RelatedEntitySectionInfo{
				Value: part.value, DisplayName: rule.label(part.value), Count: len(part.info.Items),
			})
		}
		out = append(out, merged)
		start = end
	}
	return out
}

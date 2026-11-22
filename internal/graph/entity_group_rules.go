package graph

import (
	"cmp"
	"slices"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// EntityGroupRule splits one Entity kind in entity(id)'s related groups by
// a text value derived from its linked Media (CORE-2769): a plugin's wiring names
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

// apply orders one Library's cards by its rule and records each section's
// span. An unruled group keeps the store's relation order.
func (r EntityGroupRule) apply(group *RelatedEntityGroupInfo, values map[store.MediaID]string) {
	if r.Field == "" {
		return
	}
	byValue := map[string][]SourceItem{}
	for _, item := range group.Items {
		value := values[store.MediaID(item.ID)]
		byValue[value] = append(byValue[value], item)
	}
	keys := make([]string, 0, len(byValue))
	for value := range byValue {
		keys = append(keys, value)
	}
	slices.SortFunc(keys, func(a, b string) int {
		return cmp.Or(cmp.Compare(r.rank(a), r.rank(b)), cmp.Compare(a, b))
	})
	group.Items = group.Items[:0]
	for _, value := range keys {
		items := byValue[value]
		sortNewestFirst(items)
		group.Items = append(group.Items, items...)
		group.Sections = append(group.Sections, RelatedEntitySectionInfo{
			Value: value, DisplayName: r.label(value), Count: len(items),
		})
	}
}

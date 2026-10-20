package graph

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/beamfall/core/internal/store"
)

// entityFiltersArgument describes the recursive input metadata for the generic
// Entity-field filter list shared by Source-backed reads.
func entityFiltersArgument() NestedArgument {
	return NestedArgument{Name: "filters", Kind: "list", Optional: true, Elem: &NestedArgument{
		Kind: "object",
		Fields: []NestedArgument{
			{Name: "field", Kind: "string"},
			{Name: "type", Kind: "string", Enum: []string{
				string(store.EntityFieldDate),
				string(store.EntityFieldFlag),
				string(store.EntityFieldNumber),
				string(store.EntityFieldText),
				string(store.EntityFieldURL),
			}},
			{Name: "value", Kind: "scalar"},
		},
	}}
}

// sourceItemsWithEntityFilters is the Graph-to-store boundary for Entity field
// filters. Other Source kinds ignore the shared optional argument.
func (h *Handler) sourceItemsWithEntityFilters(ctx context.Context, filter store.LibraryFilter, req request, query store.SourceQuery) ([]store.SourceItem, error) {
	if query.Kind != "entity-items" {
		return h.store.SourceItems(ctx, filter, query)
	}
	filters, err := parseEntityFieldFilters(req.Variables["filters"])
	if err != nil {
		return nil, err
	}
	query.EntityFieldFilters = filters
	return h.store.SourceItems(ctx, filter, query)
}

func parseEntityFieldFilters(raw any) ([]store.EntityField, error) {
	if raw == nil {
		return nil, nil
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, clientError("filters must be a list of objects")
	}
	if len(entries) == 0 {
		return nil, nil
	}
	filters := make([]store.EntityField, len(entries))
	for i, rawEntry := range entries {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			return nil, entityFilterClientError(i, " must be an object")
		}
		filter, err := parseEntityFieldFilter(i, entry)
		if err != nil {
			return nil, err
		}
		filters[i] = filter
	}
	return filters, nil
}

func parseEntityFieldFilter(index int, entry map[string]any) (store.EntityField, error) {
	if len(entry) != 3 {
		return store.EntityField{}, entityFilterClientError(index, " must contain only field, type, and value")
	}
	field, ok := entry["field"].(string)
	field = strings.TrimSpace(field)
	if !ok || field == "" {
		return store.EntityField{}, entityFilterClientError(index, ".field must be a non-empty string")
	}
	typeName, ok := entry["type"].(string)
	fieldType := store.EntityFieldType(strings.TrimSpace(typeName))
	if !ok || !supportedEntityFieldFilterType(fieldType) {
		return store.EntityField{}, entityFilterClientError(index, ".type must be one of text, url, number, flag, date")
	}
	value, ok := entry["value"]
	if !ok {
		return store.EntityField{}, entityFilterClientError(index, ".value is required")
	}
	return entityFieldFilterValue(index, field, fieldType, value)
}

func supportedEntityFieldFilterType(fieldType store.EntityFieldType) bool {
	switch fieldType {
	case store.EntityFieldText, store.EntityFieldURL, store.EntityFieldNumber, store.EntityFieldFlag, store.EntityFieldDate:
		return true
	default:
		return false
	}
}

func entityFieldFilterValue(index int, field string, fieldType store.EntityFieldType, value any) (store.EntityField, error) {
	filter := store.EntityField{Name: field, Type: fieldType}
	switch fieldType {
	case store.EntityFieldText, store.EntityFieldURL:
		text, ok := value.(string)
		if !ok {
			return store.EntityField{}, entityFilterValueClientError(index, "a string", fieldType)
		}
		filter.Text = text
	case store.EntityFieldNumber:
		number, ok := value.(float64)
		if !ok {
			return store.EntityField{}, entityFilterValueClientError(index, "a number", fieldType)
		}
		filter.Number = number
	case store.EntityFieldFlag:
		flag, ok := value.(bool)
		if !ok {
			return store.EntityField{}, entityFilterValueClientError(index, "a boolean", fieldType)
		}
		filter.Flag = flag
	case store.EntityFieldDate:
		text, ok := value.(string)
		if !ok {
			return store.EntityField{}, entityFilterValueClientError(index, "an RFC3339 string", fieldType)
		}
		date, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return store.EntityField{}, entityFilterValueClientError(index, "an RFC3339 string", fieldType)
		}
		filter.Date = date
	}
	return filter, nil
}

func entityFilterClientError(index int, detail string) error {
	return clientError(fmt.Sprintf("filters[%d]%s", index, detail))
}

func entityFilterValueClientError(index int, expected string, fieldType store.EntityFieldType) error {
	return entityFilterClientError(index, fmt.Sprintf(".value must be %s for type %s", expected, fieldType))
}

package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestPrepareOperationRejectsFragmentCycleBelowReachableFragmentBudget(t *testing.T) {
	query := `query { ...A } fragment A on Query { ...B } fragment B on Query { ...A }`

	_, err := prepareOperation(request{Query: query})
	if err == nil || err.Error() != `fragment cycle involving "A"` {
		t.Fatalf("prepareOperation error = %v, want fragment cycle error", err)
	}
}

func TestParseEntityFieldFiltersSupportsEveryScalarType(t *testing.T) {
	date := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)
	raw := []any{
		map[string]any{"field": "label", "type": "text", "value": "alpha"},
		map[string]any{"field": "link", "type": "url", "value": "https://example.test/item"},
		map[string]any{"field": "score", "type": "number", "value": 8.5},
		map[string]any{"field": "active", "type": "flag", "value": true},
		map[string]any{"field": "released", "type": "date", "value": date.Format(time.RFC3339)},
	}
	want := []store.EntityField{
		{Name: "label", Type: store.EntityFieldText, Text: "alpha"},
		{Name: "link", Type: store.EntityFieldURL, Text: "https://example.test/item"},
		{Name: "score", Type: store.EntityFieldNumber, Number: 8.5},
		{Name: "active", Type: store.EntityFieldFlag, Flag: true},
		{Name: "released", Type: store.EntityFieldDate, Date: date},
	}

	got, err := parseEntityFieldFilters(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseEntityFieldFilters = %#v, want %#v", got, want)
	}
}

func TestParseEntityFieldFiltersRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want string
	}{
		{name: "not a list", raw: map[string]any{}, want: "filters must be a list of objects"},
		{name: "entry not an object", raw: []any{"bad"}, want: "filters[0] must be an object"},
		{name: "extra member", raw: []any{map[string]any{"field": "color", "type": "text", "value": "red", "extra": true}}, want: "filters[0] must contain only field, type, and value"},
		{name: "blank field", raw: []any{map[string]any{"field": " ", "type": "text", "value": "red"}}, want: "filters[0].field must be a non-empty string"},
		{name: "unknown type", raw: []any{map[string]any{"field": "color", "type": "object", "value": "red"}}, want: "filters[0].type must be one of text, url, number, flag, date"},
		{name: "text value", raw: []any{map[string]any{"field": "color", "type": "text", "value": true}}, want: "filters[0].value must be a string for type text"},
		{name: "number value", raw: []any{map[string]any{"field": "score", "type": "number", "value": "8.5"}}, want: "filters[0].value must be a number for type number"},
		{name: "flag value", raw: []any{map[string]any{"field": "active", "type": "flag", "value": 1.0}}, want: "filters[0].value must be a boolean for type flag"},
		{name: "date value", raw: []any{map[string]any{"field": "released", "type": "date", "value": "not-a-date"}}, want: "filters[0].value must be an RFC3339 string for type date"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseEntityFieldFilters(tt.raw)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("parseEntityFieldFilters error = %v, want %q", err, tt.want)
			}
			if got != nil {
				t.Fatalf("parseEntityFieldFilters result = %#v, want nil on error", got)
			}
		})
	}
}

func TestSourceItemsEntityFiltersMatchCursorRead(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	if err := st.RegisterEntityFieldIndexes(ctx, []store.EntityFieldIndex{
		{Kind: "gadget", Field: "color", Type: store.EntityFieldText, Filterable: true},
		{Kind: "gadget", Field: "material", Type: store.EntityFieldText, Filterable: true},
	}); err != nil {
		t.Fatal(err)
	}
	entities := []store.Entity{
		entityFilterTestEntity("gadget-alpha", "Alpha", "red", "steel"),
		entityFilterTestEntity("gadget-bravo", "Bravo", "red", "wood"),
		entityFilterTestEntity("gadget-charlie", "Charlie", "red", "steel"),
		entityFilterTestEntity("gadget-delta", "Delta", "blue", "steel"),
	}
	for _, entity := range entities {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	filters := []store.EntityField{
		{Name: "color", Type: store.EntityFieldText, Text: "red"},
		{Name: "material", Type: store.EntityFieldText, Text: "steel"},
	}
	wantPage, err := st.EntitiesByKindCursorFiltered(
		ctx,
		"library-local",
		"gadget",
		store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}},
		store.EntityKindOptions{Filters: filters},
		"",
		50,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, len(wantPage.Entities))
	for i, entity := range wantPage.Entities {
		want[i] = string(entity.ID)
	}

	body, err := json.Marshal(map[string]any{
		"query": `query EntityFilters($kind: String!, $entityKind: String!, $filters: [EntityFieldFilter!]) { sourceItems(kind: $kind, entityKind: $entityKind, filters: $filters) { id } }`,
		"variables": map[string]any{
			"kind":       "entity-items",
			"entityKind": "gadget",
			"filters": []map[string]any{
				{"field": "color", "type": "text", "value": "red"},
				{"field": "material", "type": "text", "value": "steel"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	newTestHandlerWithStore(st).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Data struct {
			SourceItems []SourceItem `json:"sourceItems"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if got := itemIDs(resp.Data.SourceItems); !reflect.DeepEqual(got, want) {
		t.Fatalf("Graph entity filters = %v, cursor read = %v", got, want)
	}
}

func TestSourceItemsEntityFiltersRejectMalformedValue(t *testing.T) {
	body := []byte(`{
		"query":"query($kind: String!, $entityKind: String!, $filters: [EntityFieldFilter!]) { sourceItems(kind: $kind, entityKind: $entityKind, filters: $filters) { id } }",
		"variables":{
			"kind":"entity-items",
			"entityKind":"gadget",
			"filters":[{"field":"score","type":"number","value":"not-a-number"}]
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	newTestHandlerWithStore(storetest.Open(t)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	want := "filters[0].value must be a number for type number"
	if len(resp.Errors) != 1 || resp.Errors[0].Message != want {
		t.Fatalf("errors = %#v, want %q", resp.Errors, want)
	}
}

func entityFilterTestEntity(id store.EntityID, title, color, material string) store.Entity {
	return store.Entity{
		ID:        id,
		LibraryID: "library-local",
		Kind:      "gadget",
		Title:     title,
		Fields: []store.EntityField{
			{Name: "color", Type: store.EntityFieldText, Text: color},
			{Name: "material", Type: store.EntityFieldText, Text: material},
		},
	}
}

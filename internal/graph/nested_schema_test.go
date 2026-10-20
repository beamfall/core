package graph

import (
	"reflect"
	"slices"
	"testing"
)

// childNames returns the sorted child field names of a nested schema entry,
// or nil if the entry has no children.
func childNames(children []NestedField) []string {
	names := make([]string, 0, len(children))
	for _, c := range children {
		names = append(names, c.Name)
	}
	return names
}

func argumentNames(args []NestedArgument) []string {
	names := make([]string, 0, len(args))
	for _, a := range args {
		names = append(names, a.Name)
	}
	return names
}

func topLevel(t *testing.T, field string) NestedSchemaField {
	t.Helper()
	for _, e := range NestedSchema() {
		if e.Field == field {
			return e
		}
	}
	t.Fatalf("NestedSchema() missing top-level field %q", field)
	return NestedSchemaField{}
}

// TestNestedSchemaCoversEveryDispatchedField asserts the nested schema is
// complete: every field the dispatcher routes (serverInfo + every scopedField)
// has exactly one entry, so the artifact can never silently omit a field
// execute() actually serves. The init() completeness guard already panics on a
// missing resultTypes entry at package load; this pins the end-to-end coverage.
func TestNestedSchemaCoversEveryDispatchedField(t *testing.T) {
	schema := NestedSchema()
	got := map[string]int{}
	for _, e := range schema {
		got[e.Field]++
	}
	want := map[string]bool{"serverinfo": true}
	for name := range scopedFields {
		want[name] = true
	}
	for name := range want {
		if got[name] != 1 {
			t.Errorf("field %q appears %d times in NestedSchema, want exactly 1", name, got[name])
		}
	}
	for _, e := range schema {
		if !want[e.Field] {
			t.Errorf("NestedSchema has unexpected non-dispatched field %q", e.Field)
		}
	}
}

// TestNestedSchemaDeterministic asserts two generations are byte-identical and
// every children slice is sorted by name — the property the golden gate depends
// on (an unstable order would flap CI).
func TestNestedSchemaDeterministic(t *testing.T) {
	a, b := NestedSchema(), NestedSchema()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("NestedSchema() is not deterministic across calls")
	}
	if !slices.IsSortedFunc(a, func(x, y NestedSchemaField) int {
		return cmpStr(x.Field, y.Field)
	}) {
		t.Error("top-level fields are not sorted by name")
	}
	for _, e := range a {
		if !slices.IsSortedFunc(e.Arguments, func(x, y NestedArgument) int {
			return cmpStr(x.Name, y.Name)
		}) {
			t.Errorf("%s arguments not sorted: %v", e.Field, argumentNames(e.Arguments))
		}
	}
	var walk func(fields []NestedField)
	walk = func(fields []NestedField) {
		if !slices.IsSortedFunc(fields, func(x, y NestedField) int { return cmpStr(x.Name, y.Name) }) {
			t.Errorf("nested children not sorted: %v", childNames(fields))
		}
		for _, f := range fields {
			walk(f.Children)
		}
	}
	for _, e := range a {
		walk(e.Children)
	}
}

func cmpStr(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func TestNestedSchemaSourceItemsArguments(t *testing.T) {
	si := topLevel(t, "sourceitems")
	wantNames := []string{"entityFilter", "entityID", "entityKind", "fieldFilters", "filters", "kind", "libraryID", "limit", "mode", "offset", "query", "seed", "value"}
	if got := argumentNames(si.Arguments); !slices.Equal(got, wantNames) {
		t.Fatalf("sourceItems arguments = %v, want %v", got, wantNames)
	}
	ambient := topLevel(t, "ambientqueue")
	if !reflect.DeepEqual(ambient.Arguments, si.Arguments) {
		t.Fatalf("ambientQueue arguments = %#v, want shared Source arguments %#v", ambient.Arguments, si.Arguments)
	}
	media := topLevel(t, "media")
	if want := []NestedArgument{{Name: "id", Kind: "string"}}; !reflect.DeepEqual(media.Arguments, want) {
		t.Fatalf("media arguments = %#v, want %#v", media.Arguments, want)
	}
	searchResult := topLevel(t, "searchresult")
	want := []NestedArgument{{Name: "mode", Kind: "string", Optional: true, Enum: []string{"auto", "keyword", "recommend", "semantic"}}, {Name: "query", Kind: "string", Optional: true}}
	if !reflect.DeepEqual(searchResult.Arguments, want) {
		t.Fatalf("searchResult arguments = %#v, want %#v", searchResult.Arguments, want)
	}
}

// TestNestedSchemaSourceItemShape pins the sourceItems wire shape — the H17/H2
// client-drift case. The reflected SourceItem has id/kind/title/summary/
// artworkSlots/artwork plus restricted (FIDELITY-IOS-REVEAL-QA: the Library-level
// restricted flag the iOS per-poster tag consumes; emitted ONLY to a Revealed
// scope, ADR-0004 §4), locked (ACCESS-1, ADR-0113 §2.1: the per-item
// enforced-filter lock affordance, set on a visible-but-blocked item under a
// locked certification ceiling), and muted (ADR-0054 §2.7: the per-item
// Per-Profile Mute badge on deliberately-surfaced / Favorite-overridden items)
// and NOTHING else: the android-ui kit's selection
// of `libraryID` and `mediaClass` under sourceItems are nonexistent nested fields
// (the contract this schema exists to surface). If a future change adds those
// fields, this test must be updated deliberately alongside the golden.
func TestNestedSchemaSourceItemShape(t *testing.T) {
	si := topLevel(t, "sourceitems")
	if si.Kind != "object" {
		t.Fatalf("sourceItems kind = %q, want object", si.Kind)
	}
	want := []string{"artwork", "artworkSlots", "id", "kind", "locked", "muted", "restricted", "summary", "title"}
	if got := childNames(si.Children); !slices.Equal(got, want) {
		t.Fatalf("sourceItems fields = %v, want %v", got, want)
	}
	for _, c := range si.Children {
		switch c.Name {
		case "libraryID", "mediaClass":
			t.Errorf("sourceItems unexpectedly exposes %q — client-drift would be masked", c.Name)
		case "artwork":
			if c.Kind != "object" || !slices.Equal(childNames(c.Children), []string{"poster"}) {
				t.Errorf("sourceItems.artwork = %v (%s), want object{poster}", childNames(c.Children), c.Kind)
			}
		case "artworkSlots":
			if c.Kind != "map" || c.Elem == nil || c.Elem.Kind != "scalar" {
				t.Errorf("sourceItems.artworkSlots = %+v, want map with scalar value", c)
			}
		}
	}
}

func TestNestedSchemaSourceItemOptionalFields(t *testing.T) {
	si := topLevel(t, "sourceitems")
	want := map[string]bool{
		"artwork":      true,
		"artworkSlots": true,
		"id":           false,
		"kind":         false,
		"locked":       true,
		"muted":        true,
		"restricted":   true,
		"summary":      true,
		"title":        false,
	}
	for _, field := range si.Children {
		optional, ok := want[field.Name]
		if !ok {
			continue
		}
		if field.Optional != optional {
			t.Errorf("sourceItems.%s optional = %v, want %v", field.Name, field.Optional, optional)
		}
		if field.Name == "artwork" && !field.Nullable {
			t.Errorf("sourceItems.artwork nullable = false, want true for pointer result field")
		}
		if field.Name == "artwork" {
			if got := field.Children[0].Optional; got {
				t.Errorf("sourceItems.artwork.poster optional = %v, want false", got)
			}
		}
	}
}

// TestNestedSchemaFlattensEmbedded asserts embedded result structs are promoted
// inline the way encoding/json serializes them: RelatedEntityInfo embeds
// EntityInfo, so entitiesForMedia must expose EntityInfo's fields plus its own
// attributes — not a nested "entityInfo" object.
func TestNestedSchemaFlattensEmbedded(t *testing.T) {
	e := topLevel(t, "entitiesformedia")
	want := []string{"attributes", "fields", "id", "kind", "kindDisplayName", "title"}
	if got := childNames(e.Children); !slices.Equal(got, want) {
		t.Fatalf("entitiesForMedia fields = %v, want %v (embedded EntityInfo flattened)", got, want)
	}
}

// TestNestedSchemaScalarClassification pins the JSON-serialization-aware rules:
// time.Time is scalar, while named map types retain their dynamic value shape.
func TestNestedSchemaScalarClassification(t *testing.T) {
	m := topLevel(t, "navigationmanifest")
	for _, c := range m.Children {
		if c.Name == "generatedAt" && c.Kind != "scalar" {
			t.Errorf("navigationManifest.generatedAt kind = %q, want scalar (time.Time)", c.Kind)
		}
	}
	p := topLevel(t, "plugins")
	for _, c := range p.Children {
		if c.Name == "mediaClassLabels" && (c.Kind != "map" || c.Elem == nil || c.Elem.Kind != "map" || c.Elem.Elem == nil || c.Elem.Elem.Kind != "scalar") {
			t.Errorf("plugins.mediaClassLabels = %+v, want nested maps with scalar leaf", c)
		}
	}
}

func nestedChild(t *testing.T, fields []NestedField, name string) NestedField {
	t.Helper()
	for _, field := range fields {
		if field.Name == name {
			return field
		}
	}
	t.Fatalf("nested fields missing %q", name)
	return NestedField{}
}

func nestedArgument(t *testing.T, arguments []NestedArgument, name string) NestedArgument {
	t.Helper()
	for _, argument := range arguments {
		if argument.Name == name {
			return argument
		}
	}
	t.Fatalf("nested arguments missing %q", name)
	return NestedArgument{}
}

func TestNestedSchemaHasCorrectedRecursiveMetadata(t *testing.T) {
	schema := topLevel(t, "sourceitems")
	if artworkSlots := nestedChild(t, schema.Children, "artworkSlots"); artworkSlots.Kind != "map" || artworkSlots.Elem == nil || artworkSlots.Elem.Kind != "scalar" {
		t.Fatalf("artworkSlots = %+v, want map with scalar value", artworkSlots)
	}

	fieldFilters := nestedArgument(t, schema.Arguments, "fieldFilters")
	if fieldFilters.Kind != "map" || fieldFilters.Elem == nil || fieldFilters.Elem.Kind != "union" || len(fieldFilters.Elem.AnyOf) != 2 {
		t.Fatalf("candidate fieldFilters = %+v, want map of scalar-or-range union", fieldFilters)
	}
	filters := nestedArgument(t, schema.Arguments, "filters")
	if filters.Elem == nil || filters.Elem.Kind != "object" || len(filters.Elem.Fields) != 3 {
		t.Fatalf("candidate filters = %+v, want list of field/type/value objects", filters)
	}
	mode := nestedArgument(t, schema.Arguments, "mode")
	if !slices.Equal(mode.Enum, []string{"auto", "keyword", "recommend", "semantic"}) {
		t.Fatalf("candidate mode enum = %v", mode.Enum)
	}
	entityFilter := nestedArgument(t, schema.Arguments, "entityFilter")
	if entityFilter.Kind != "object" || !slices.Equal(argumentNames(entityFilter.Fields), []string{"kind", "name"}) {
		t.Fatalf("entityFilter = %+v, want object with kind/name fields", entityFilter)
	}
	if query := nestedArgument(t, schema.Arguments, "query"); query.Kind != "string" || !query.Optional {
		t.Fatalf("query = %+v, want optional string", query)
	}

	children := nestedChild(t, topLevel(t, "foldertree").Children, "children")
	if children.Elem == nil || children.Elem.Kind != "object" || !children.Elem.Recursive {
		t.Fatalf("candidate foldertree.children = %+v, want recursive object element", children)
	}
}

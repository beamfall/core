package graphquery

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func fieldNames(query string) []string {
	fields := TopLevelFields(query)
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestTopLevelFields(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		query string
		want  []string
	}{
		"simple query": {
			query: `query { serverInfo { name version } }`,
			want:  []string{"serverinfo"},
		},
		"shorthand": {
			query: `{ serverInfo }`,
			want:  []string{"serverinfo"},
		},
		"multiple fields": {
			query: `query { serverInfo { name } currentSession { id } }`,
			want:  []string{"currentsession", "serverinfo"},
		},
		"operation header with variables": {
			query: `query SourceItems($kind: String!, $limit: Int = 50) { sourceItems(kind: $kind, limit: $limit) { id } }`,
			want:  []string{"sourceitems"},
		},
		"mutation": {
			query: `mutation ScanLibrary($libraryID: String!, $root: String!) { scanLibrary(libraryID: $libraryID, root: $root) { id } }`,
			want:  []string{"scanlibrary"},
		},
		"nested selections stay nested": {
			query: `query { jobs { id progress { current total } } }`,
			want:  []string{"jobs"},
		},
		"field names inside argument strings are not fields": {
			query: `query Search($q: String) { search(query: "jobs relock trashMedia") { id } }`,
			want:  []string{"search"},
		},
		"identifySearch is not search": {
			query: `mutation { identifySearch(pluginID: "demo", scraperID: "lookup", query: "x") { id } }`,
			want:  []string{"identifysearch"},
		},
		"mediaForEntity is not entity": {
			query: `query { mediaForEntity(entityID: $entityID) { id } }`,
			want:  []string{"mediaforentity"},
		},
		"alias records the field name": {
			query: `query { items: sourceItems(kind: "recently-added") { id } }`,
			want:  []string{"sourceitems"},
		},
		"inline values with braces in strings": {
			query: `mutation { setClientPref(key: "web.style", value: "{\"size\":\"large\"}") { key } }`,
			want:  []string{"setclientpref"},
		},
		"comments are skipped": {
			query: "query {\n  # jobs would need manager role\n  serverInfo { name }\n}",
			want:  []string{"serverinfo"},
		},
		"directives do not become fields": {
			query: `query { search(query: "x") @include(if: true) { id } relock }`,
			want:  []string{"relock", "search"},
		},
		"fragment spreads are traversed": {
			query: `query { ...HomeFields ... on Query { serverInfo { name } } } fragment HomeFields on Query { currentSession { id } }`,
			want:  []string{"currentsession", "serverinfo"},
		},
		"fragment definition fields are not dispatched": {
			query: `query { serverInfo } fragment F on Media { apiKeys }`,
			want:  []string{"serverinfo"},
		},
		"fragment definition with no operation dispatches nothing": {
			query: `fragment F on Media { libraryHealth }`,
			want:  []string{},
		},
		"fragment definition before the operation": {
			query: `fragment F on Media { apiKeys } query { serverInfo }`,
			want:  []string{"serverinfo"},
		},
		"operation field plus a fragment definition with nested selections": {
			query: `query { media { ...Frag } } fragment Frag on Media { id streams { url } }`,
			want:  []string{"media"},
		},
		"empty document": {
			query: ``,
			want:  []string{},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := fieldNames(tc.query)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("TopLevelFields(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

func TestTopLevelFieldsSkipsArgumentText(t *testing.T) {
	t.Parallel()
	query := `query Search($query: String!) { search(query: "jobs trashList relock") { id } }`
	fields := TopLevelFields(query)
	if !fields["search"] {
		t.Fatalf("search field not extracted from %q", query)
	}
	for _, name := range []string{"jobs", "trashlist", "relock"} {
		if fields[name] {
			t.Fatalf("argument text leaked %q into the field set", name)
		}
	}
}

func TestTopLevelFieldsSkipsFragmentDefinitions(t *testing.T) {
	t.Parallel()
	for _, query := range []string{
		`query { serverInfo } fragment F on Media { apiKeys libraryHealth }`,
		`fragment F on Media { apiKeys }`,
		`fragment A on Media { apiKeys } fragment B on Library { libraryHealth }`,
	} {
		fields := TopLevelFields(query)
		for _, leaked := range []string{"apikeys", "libraryhealth"} {
			if fields[leaked] {
				t.Fatalf("fragment-definition field %q dispatched from %q", leaked, query)
			}
		}
	}
}

func TestSelectedOperationFields(t *testing.T) {
	t.Parallel()
	doc := `
		query Public { serverInfo { name } }
		mutation Private { deleteLibrary(libraryID: "library-local") { id } }
		fragment AdminFields on Query { apiKeys { id } }
	`
	fields, err := SelectedOperationFields(doc, "Public")
	if err != nil {
		t.Fatalf("SelectedOperationFields Public: %v", err)
	}
	if got, want := namesFromFields(fields), []string{"serverinfo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Public fields = %v, want %v", got, want)
	}
	if _, err := SelectedOperationFields(doc, "Missing"); err == nil || err.Error() != `unknown operationName "Missing"` {
		t.Fatalf("missing operation error = %v", err)
	}
	if _, err := SelectedOperationFields(doc, ""); err == nil || err.Error() != "operationName is required for multiple operations" {
		t.Fatalf("multi-operation error = %v", err)
	}
}

func TestSelectedOperationFieldsTraversesFragments(t *testing.T) {
	t.Parallel()
	doc := `
		query Public {
			...PublicFields
		}
		fragment PublicFields on Query { ... on Query { serverInfo { name } } }
		fragment AdminFields on Query { apiKeys { id } }
		query Private { currentSession { id } }
	`
	fields, err := SelectedOperationFields(doc, "Public")
	if err != nil {
		t.Fatalf("SelectedOperationFields Public: %v", err)
	}
	if got, want := namesFromFields(fields), []string{"serverinfo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Public fields = %v, want %v", got, want)
	}
}

func TestPrepareOperationValidatesOnlySelectedOperationFragments(t *testing.T) {
	t.Parallel()
	doc := `
		query Public { serverInfo { name } }
		query Private($show: Boolean!) { ...PrivateFields }
		fragment PrivateFields on Query {
			currentSession @include(if: $show) { id }
		}
	`
	op, err := PrepareOperation(doc, "Public", nil)
	if err != nil {
		t.Fatalf("PrepareOperation Public: %v", err)
	}
	if got, want := len(op.Occurrences), 1; got != want || op.Occurrences[0].Canonical != "serverinfo" {
		t.Fatalf("Public occurrences = %#v, want serverInfo only", op.Occurrences)
	}
	if _, err := PrepareOperation(doc, "Private", map[string]any{"show": true}); err != nil {
		t.Fatalf("PrepareOperation Private: %v", err)
	}
}

func TestSelectedOperationFieldsAllowsSingleOperationWithFragmentDefinition(t *testing.T) {
	t.Parallel()
	doc := `query Public { serverInfo { name } } fragment AdminFields on Query { apiKeys { id } }`
	fields, err := SelectedOperationFields(doc, "")
	if err != nil {
		t.Fatalf("SelectedOperationFields single operation: %v", err)
	}
	if got, want := namesFromFields(fields), []string{"serverinfo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Public fields = %v, want %v", got, want)
	}
}

func namesFromFields(fields map[string]bool) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestPrepareOperationPreservesOccurrencesAliasesAndInputs(t *testing.T) {
	doc := `
		query Q($kind: String = "default", $show: Boolean = true) {
			first: sourceItems(kind: $kind, limit: 2) @include(if: $show)
			...Fields
			last: serverInfo
		}
		fragment Fields on Query { second: sourceItems(kind: "inline") }
	`
	op, err := PrepareOperation(doc, "Q", map[string]any{"kind": "provided"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(op.Occurrences), 3; got != want {
		t.Fatalf("occurrences = %d, want %d", got, want)
	}
	if got, want := []string{op.Occurrences[0].ResponseKey, op.Occurrences[1].ResponseKey, op.Occurrences[2].ResponseKey}, []string{"first", "second", "last"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("response key order = %v, want %v", got, want)
	}
	if got, want := []string{op.Occurrences[0].Name, op.Occurrences[1].Name, op.Occurrences[2].Name}, []string{"sourceItems", "sourceItems", "serverInfo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("wire field names = %v, want %v", got, want)
	}
	if got := op.Occurrences[0].Input["kind"]; got != "provided" {
		t.Fatalf("first kind = %#v, want provided", got)
	}
	if got := op.Occurrences[0].Input["limit"]; got != float64(2) {
		t.Fatalf("first limit = %#v, want JSON number 2", got)
	}
	if got := op.Occurrences[1].Input["kind"]; got != "inline" {
		t.Fatalf("second kind = %#v, want inline", got)
	}
	op.Occurrences[0].Input["kind"] = "mutated"
	if got := op.Occurrences[1].Input["kind"]; got != "inline" {
		t.Fatalf("occurrence inputs alias each other: second kind = %#v", got)
	}
}

func TestPrepareOperationDirectiveDefaultsAndExplicitNull(t *testing.T) {
	query := `query Q($show: Boolean = false) { serverInfo @include(if: $show) }`
	op, err := PrepareOperation(query, "Q", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(op.Occurrences) != 1 || op.Occurrences[0].Included {
		t.Fatalf("default-false occurrence = %#v, want excluded", op.Occurrences)
	}
	if _, err := PrepareOperation(query, "Q", map[string]any{"show": nil}); err == nil || !strings.Contains(err.Error(), "must be a boolean") {
		t.Fatalf("explicit null directive error = %v", err)
	}
}

func TestPrepareOperationRejectsInvalidExecutableDocuments(t *testing.T) {
	tests := map[string]string{
		"duplicate arguments":  `query { sourceItems(kind: "a", kind: "b") }`,
		"undefined variable":   `query { sourceItems(kind: $missing) }`,
		"unknown directive":    `query { serverInfo @future(if: true) }`,
		"wrong type condition": `query { ... on Mutation { serverInfo } }`,
	}
	for name, query := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := PrepareOperation(query, "", nil); err == nil {
				t.Fatalf("PrepareOperation(%q) succeeded", query)
			}
		})
	}
}

// aliasedFields builds count top-level occurrences of one field under
// distinct aliases, so repetition is exercised without response-key merging.
func aliasedFields(count int) string {
	var b strings.Builder
	for i := range count {
		fmt.Fprintf(&b, "a%d: serverInfo ", i)
	}
	return b.String()
}

// spreadFragments builds count one-leaf fragments plus a document that
// spreads every one of them inside a nested selection set.
func spreadFragments(count int) string {
	var spreads, definitions strings.Builder
	for i := range count {
		fmt.Fprintf(&spreads, "...F%d ", i)
		fmt.Fprintf(&definitions, "fragment F%d on Query { leaf%d } ", i, i)
	}
	return "query { serverInfo { " + spreads.String() + "} } " + definitions.String()
}

func TestPrepareOperationEnforcesWorkBudgets(t *testing.T) {
	t.Run("expanded occurrences", func(t *testing.T) {
		query := "query { " + strings.Repeat("serverInfo ", maxExpandedOccurrences+1) + "}"
		if _, err := PrepareOperation(query, "", nil); err == nil || !strings.Contains(err.Error(), "expanded field work") {
			t.Fatalf("expanded-work error = %v", err)
		}
	})
	t.Run("alias repetition", func(t *testing.T) {
		query := "query { " + aliasedFields(maxExpandedOccurrences+1) + "}"
		if _, err := PrepareOperation(query, "", nil); err == nil || !strings.Contains(err.Error(), "expanded field work") {
			t.Fatalf("alias-repetition error = %v", err)
		}
	})
	t.Run("selection depth", func(t *testing.T) {
		selection := "serverInfo"
		for range maxSelectionDepth + 1 {
			selection = "child { " + selection + " }"
		}
		if _, err := PrepareOperation("query { "+selection+" }", "", nil); err == nil || !strings.Contains(err.Error(), "selection depth") {
			t.Fatalf("depth error = %v", err)
		}
	})
	t.Run("inline fragment depth", func(t *testing.T) {
		selection := "serverInfo"
		for range maxSelectionDepth + 1 {
			selection = "... on Query { " + selection + " }"
		}
		if _, err := PrepareOperation("query { "+selection+" }", "", nil); err == nil || !strings.Contains(err.Error(), "selection depth") {
			t.Fatalf("inline-fragment depth error = %v", err)
		}
	})
	t.Run("selection nodes", func(t *testing.T) {
		query := "query { serverInfo { " + strings.Repeat("leaf ", maxDocumentSelections) + "} }"
		if _, err := PrepareOperation(query, "", nil); err == nil || !strings.Contains(err.Error(), "selection work") {
			t.Fatalf("selection-node error = %v", err)
		}
	})
	t.Run("fragment amplification", func(t *testing.T) {
		// A small document whose repeated spreads of one wide fragment expand
		// past the selection-node budget: 33 spreads of a 128-leaf fragment
		// visit 33*(1+128) nodes although the document defines only 129.
		spreads := strings.Repeat("...Wide ", 33)
		fragment := "fragment Wide on Query { " + strings.Repeat("leaf ", 128) + "}"
		query := "query { serverInfo { " + spreads + "} } " + fragment
		if _, err := PrepareOperation(query, "", nil); err == nil || !strings.Contains(err.Error(), "selection work") {
			t.Fatalf("fragment-amplification error = %v", err)
		}
	})
	t.Run("reachable fragments", func(t *testing.T) {
		if _, err := PrepareOperation(spreadFragments(maxReachableFragments+1), "", nil); err == nil || !strings.Contains(err.Error(), "reachable fragments") {
			t.Fatalf("reachable-fragment error = %v", err)
		}
	})
}

// TestPrepareOperationAcceptsBoundaryDocuments locks the budgets as exact
// boundaries: a document that sits precisely at each documented maximum is
// accepted.
func TestPrepareOperationAcceptsBoundaryDocuments(t *testing.T) {
	t.Run("exact expanded occurrences", func(t *testing.T) {
		op, err := PrepareOperation("query { "+aliasedFields(maxExpandedOccurrences)+"}", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(op.Occurrences) != maxExpandedOccurrences {
			t.Fatalf("occurrences = %d, want %d", len(op.Occurrences), maxExpandedOccurrences)
		}
	})
	t.Run("exact selection depth", func(t *testing.T) {
		selection := "serverInfo"
		for range maxSelectionDepth - 1 {
			selection = "child { " + selection + " }"
		}
		if _, err := PrepareOperation("query { "+selection+" }", "", nil); err != nil {
			t.Fatalf("exact-depth document rejected: %v", err)
		}
	})
	t.Run("exact selection nodes", func(t *testing.T) {
		query := "query { serverInfo { " + strings.Repeat("leaf ", maxDocumentSelections-1) + "} }"
		if _, err := PrepareOperation(query, "", nil); err != nil {
			t.Fatalf("exact-node document rejected: %v", err)
		}
	})
	t.Run("exact reachable fragments", func(t *testing.T) {
		if _, err := PrepareOperation(spreadFragments(maxReachableFragments), "", nil); err != nil {
			t.Fatalf("exact-fragment document rejected: %v", err)
		}
	})
}

func TestSelectedOperationFieldsRejectsInvalidDocuments(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"malformed":           `query { serverInfo `,
		"duplicate operation": `query A { serverInfo } query A { serverInfo }`,
		"duplicate fragment":  `query { ...F } fragment F on Query { serverInfo } fragment F on Query { serverInfo }`,
		"anonymous not lone":  `{ serverInfo } query B { serverInfo }`,
		"undefined spread":    `query { ...Missing }`,
		"fragment cycle":      `query { ...A } fragment A on Query { ...B } fragment B on Query { ...A }`,
		"no operation":        `fragment F on Query { serverInfo }`,
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := SelectedOperationFields(query, ""); err == nil {
				t.Fatalf("SelectedOperationFields(%q) succeeded", query)
			}
		})
	}
}

package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func fieldNames(query string) []string {
	fields := topLevelFields(query)
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestTopLevelFields(t *testing.T) {
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
			got := fieldNames(tc.query)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("topLevelFields(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

// TestSearchArgumentDoesNotTriggerManagerGate is the regression for the
// substring-dispatch hazard: a viewer search whose text merely mentions a
// manager-only operation name must run as a search, not trip the manager
// gate (the old strings.Contains routing matched "jobs" anywhere in the
// query text).
func TestSearchArgumentDoesNotTriggerManagerGate(t *testing.T) {
	query := `query Search($query: String!) { search(query: "jobs trashList relock") { id } }`
	fields := topLevelFields(query)
	if !fields["search"] {
		t.Fatalf("search field not extracted from %q", query)
	}
	for _, name := range []string{"jobs", "trashlist", "relock"} {
		if fields[name] {
			t.Fatalf("argument text leaked %q into the field set", name)
		}
	}
}

func TestPrepareOperationMergesOnlyEquivalentResponseKeys(t *testing.T) {
	plan, err := prepareOperation(request{Query: `query { same: serverInfo @skip(if: true) same: serverInfo }`})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.occurrences) != 1 || plan.occurrences[0].responseKey != "same" {
		t.Fatalf("merged plan = %#v, want one included same occurrence", plan.occurrences)
	}
	if _, err := prepareOperation(request{Query: `query { same: serverInfo same: currentSession }`}); err == nil || !strings.Contains(err.Error(), "conflicting selections") {
		t.Fatalf("conflicting response-key error = %v", err)
	}
	if _, err := prepareOperation(request{Query: `query { markNotificationRead }`}); err == nil || !strings.Contains(err.Error(), "not readable") {
		t.Fatalf("operation-kind error = %v", err)
	}
}

func TestPrepareOperationRejectsWrongFieldCase(t *testing.T) {
	if _, err := prepareOperation(request{Query: `query { SERVERINFO { name } }`}); err == nil || !strings.Contains(err.Error(), "SERVERINFO") {
		t.Fatalf("uppercase field error = %v, want exact-case rejection", err)
	}
}

func TestGraphQLAliasesFragmentsDirectivesAndEmptyData(t *testing.T) {
	h := newTestHandler(t)
	data, errs := post(t, h, `{"query":"query Q($show:Boolean=true){ first:serverInfo @include(if:$show) ...F } fragment F on Query { second:serverInfo }","operationName":"Q"}`, nil)
	if len(errs) != 0 || data["first"] == nil || data["second"] == nil {
		t.Fatalf("alias/fragment response data=%v errors=%#v", data, errs)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewBufferString(`{"query":"query { currentSession @skip(if:true) }"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := strings.TrimSpace(rec.Body.String()); got != `{"data":{}}` {
		t.Fatalf("all-excluded response = %s, want exact empty data", got)
	}

	_, errs = post(t, h, `{"query":"query { futureField @skip(if:true) }"}`, nil)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "unknown top-level field") {
		t.Fatalf("skipped unknown-field errors = %#v", errs)
	}
}

func TestMutationErrorOmitsDataAndReadAliasOwnsErrorPath(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewBufferString(`{"query":"mutation { marked: markNotificationRead }"}`))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if bytes.Contains(rec.Body.Bytes(), []byte(`"data"`)) {
		t.Fatalf("failed mutation included data: %s", rec.Body.String())
	}
	var envelope struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Errors) != 1 {
		t.Fatalf("mutation errors = %#v", envelope.Errors)
	}
	_, errs := post(t, h, `{"query":"query { missing: media(id:\"does-not-exist\") { id } }"}`, authorizeViewer)
	if len(errs) != 1 || len(errs[0].Path) != 1 || errs[0].Path[0] != "missing" {
		t.Fatalf("read alias error path = %#v", errs)
	}
}

// TestFragmentDefinitionDoesNotDispatch guards the second dispatch hazard: a
// top-level `fragment … { … }` definition opens a solid brace exactly like an
// operation body, so its selections must not be recorded as operation fields.
// Merely *defining* a fragment that names a manager-only field (here apiKeys,
// libraryHealth) must dispatch nothing from that fragment — otherwise a crafted
// document runs resolvers, and trips their authz branch, that the operation
// never selected.
func TestFragmentDefinitionDoesNotDispatch(t *testing.T) {
	for _, query := range []string{
		`query { serverInfo } fragment F on Media { apiKeys libraryHealth }`,
		`fragment F on Media { apiKeys }`,
		`fragment A on Media { apiKeys } fragment B on Library { libraryHealth }`,
	} {
		fields := topLevelFields(query)
		for _, leaked := range []string{"apikeys", "libraryhealth"} {
			if fields[leaked] {
				t.Fatalf("fragment-definition field %q dispatched from %q", leaked, query)
			}
		}
	}
}

func TestSelectedOperationFields(t *testing.T) {
	doc := `
		query Public { serverInfo { name } }
		mutation Private { deleteLibrary(libraryID: "library-local") { id } }
		fragment AdminFields on Query { apiKeys { id } }
	`
	fields, err := selectedOperationFields(doc, "Public")
	if err != nil {
		t.Fatalf("selectedOperationFields Public: %v", err)
	}
	if got, want := fieldNamesFromSet(fields), []string{"serverinfo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Public fields = %v, want %v", got, want)
	}
	if _, err := selectedOperationFields(doc, "Missing"); err == nil || err.Error() != `unknown operationName "Missing"` {
		t.Fatalf("missing operation error = %v", err)
	}
	if _, err := selectedOperationFields(doc, ""); err == nil || err.Error() != "operationName is required for multiple operations" {
		t.Fatalf("multi-operation error = %v", err)
	}
}

func TestSelectedOperationFieldsPreservesFragmentParsing(t *testing.T) {
	doc := `
		query Public {
			... on Query { serverInfo { name } }
		}
		fragment AdminFields on Query { apiKeys { id } }
		query Private { currentSession { id } }
	`
	fields, err := selectedOperationFields(doc, "Public")
	if err != nil {
		t.Fatalf("selectedOperationFields Public: %v", err)
	}
	if got, want := fieldNamesFromSet(fields), []string{"serverinfo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Public fields = %v, want %v", got, want)
	}
}

func TestSelectedOperationFieldsAllowsSingleOperationWithFragmentDefinition(t *testing.T) {
	doc := `query Public { serverInfo { name } } fragment AdminFields on Query { apiKeys { id } }`
	fields, err := selectedOperationFields(doc, "")
	if err != nil {
		t.Fatalf("selectedOperationFields single operation: %v", err)
	}
	if got, want := fieldNamesFromSet(fields), []string{"serverinfo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Public fields = %v, want %v", got, want)
	}
}

func fieldNamesFromSet(fields map[string]bool) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestServeHTTPFragmentFieldsParticipateInAuth(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"query { ...Private } fragment Private on Query { currentSession { id } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestServeHTTPUnselectedOperationDoesNotParticipateInAuth(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"operationName":"Public","query":"query Public { serverInfo { name } } query Private { ...PrivateFields } fragment PrivateFields on Query { currentSession { id } }"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if _, ok := resp.Data["serverInfo"]; !ok {
		t.Fatalf("selected serverInfo missing from data: %#v", resp.Data)
	}
	if _, ok := resp.Data["currentSession"]; ok {
		t.Fatalf("unselected currentSession leaked into data: %#v", resp.Data)
	}
}

func TestServeHTTPRejectsInvalidDocumentsBeforeAuth(t *testing.T) {
	cases := map[string]request{
		"malformed":           {Query: `query { currentSession `},
		"duplicate operation": {Query: `query A { currentSession { id } } query A { serverInfo }`},
		"duplicate fragment":  {Query: `query { ...F } fragment F on Query { currentSession { id } } fragment F on Query { serverInfo }`},
		"anonymous not lone":  {Query: `{ currentSession { id } } query B { serverInfo }`},
		"undefined spread":    {Query: `query { currentSession { id } ...Missing }`},
		"fragment cycle":      {Query: `query { currentSession { id } ...A } fragment A on Query { ...B } fragment B on Query { ...A }`},
		"unknown operation": {
			Query:         `query Private { currentSession { id } }`,
			OperationName: "Missing",
		},
		"missing operation name": {
			Query: `query Private { currentSession { id } } query Public { serverInfo }`,
		},
		"fragment only": {
			Query: `fragment Private on Query { currentSession { id } }`,
		},
	}
	for name, graphRequest := range cases {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(graphRequest)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
			rec := httptest.NewRecorder()

			(&Handler{}).ServeHTTP(rec, req)

			var resp response
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(resp.Errors) != 1 {
				t.Fatalf("errors = %#v, want one pre-auth validation error", resp.Errors)
			}
		})
	}
}

func TestVarsTypedReads(t *testing.T) {
	req := request{Variables: map[string]any{
		"flag":   true,
		"count":  float64(3),
		"name":   "x",
		"list":   []any{"a", "b"},
		"nilVar": nil,
		"badNum": "three",
	}}
	v := newVars(req)
	if flag, ok := v.Bool("flag"); !ok || !flag {
		t.Fatalf("Bool(flag) = %v, %v", flag, ok)
	}
	if count, ok := v.Number("count"); !ok || count != 3 {
		t.Fatalf("Number(count) = %v, %v", count, ok)
	}
	if name, ok := v.String("name"); !ok || name != "x" {
		t.Fatalf("String(name) = %q, %v", name, ok)
	}
	if list, ok := v.StringList("list", "strings"); !ok || !reflect.DeepEqual(list, []string{"a", "b"}) {
		t.Fatalf("StringList(list) = %v, %v", list, ok)
	}
	if _, ok := v.Bool("absent"); ok {
		t.Fatal("Bool(absent) reported present")
	}
	if _, ok := v.String("nilVar"); ok {
		t.Fatal("String(nilVar) reported present")
	}
	if err := v.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := v.Number("badNum"); ok {
		t.Fatal("Number(badNum) reported ok")
	}
	if err := v.Err(); err == nil || err.Error() != "badNum must be a number" {
		t.Fatalf("Err() = %v, want badNum typing error", err)
	}
}

func TestGraphIntBounds(t *testing.T) {
	req := request{Variables: map[string]any{
		"zero":   float64(0),
		"large":  float64(9999),
		"normal": float64(25),
		"neg":    float64(-7),
	}}
	if got := boundedIntVar(req, "missing", 50, 200); got != 50 {
		t.Fatalf("missing bound = %d, want fallback 50", got)
	}
	if got := boundedIntVar(req, "zero", 50, 200); got != 50 {
		t.Fatalf("zero bound = %d, want fallback 50", got)
	}
	if got := boundedIntVar(req, "large", 50, 200); got != 200 {
		t.Fatalf("large bound = %d, want max 200", got)
	}
	if got := boundedIntVar(req, "normal", 50, 200); got != 25 {
		t.Fatalf("normal bound = %d, want 25", got)
	}
	if got := nonNegativeIntVar(req, "neg"); got != 0 {
		t.Fatalf("negative offset = %d, want 0", got)
	}
	if got := boundedNonNegativeInt(0, 3); got != 0 {
		t.Fatalf("zero depth = %d, want explicit 0 preserved", got)
	}
	if got := boundedNonNegativeInt(999, 3); got != 3 {
		t.Fatalf("large depth = %d, want max 3", got)
	}
}

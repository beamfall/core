package scrape

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	sdk "github.com/beamfall/plugin-sdk"
	"github.com/beamfall/plugin-sdk/sdktest"

	"github.com/beamfall/core/internal/plugin"
)

func TestListPathSDKParity(t *testing.T) {
	// Same path cases as sdk v0.4.10's TestScraperTraceListPaths.
	cases := []struct {
		name, path, doc string
		idPath, label   string
		want            []sdktest.TraceCandidate
	}{
		{
			name: "bare search results", path: " $ ",
			doc:    `[{"score":0.9,"show":{"id":42,"name":"Solstice"}},{"score":0.5,"show":{"id":95,"name":"Equinox"}}]`,
			idPath: "show.id", label: "show.name",
			want: []sdktest.TraceCandidate{
				{ID: "42", Fields: map[string]string{"title": "Solstice"}},
				{ID: "95", Fields: map[string]string{"title": "Equinox"}},
			},
		},
		{
			name: "TVmaze bare season listing", path: "$",
			doc:    `[{"id":1234,"number":1,"name":"Season 1","premiereDate":"2024-04-02","image":{"medium":"https://example.test/season.jpg"}}]`,
			idPath: "id", label: "name",
			want: []sdktest.TraceCandidate{{ID: "1234", Fields: map[string]string{"title": "Season 1"}}},
		},
		{
			name: "TVDB official seasons", path: "data.seasons[?type.type=official]",
			doc: `{"data":{"seasons":[
				{"id":10,"number":0,"name":"Specials","type":{"type":"official"}},
				{"id":11,"number":1,"name":"Season 1","type":{"type":"official"}},
				{"id":21,"number":1,"name":"DVD Season 1","type":{"type":"dvd"}}
			]}}`,
			idPath: "id", label: "name",
			want: []sdktest.TraceCandidate{
				{ID: "10", Fields: map[string]string{"title": "Specials"}},
				{ID: "11", Fields: map[string]string{"title": "Season 1"}},
			},
		},
		{
			name: "root filter", path: "$[?type=official]",
			doc:    `[{"id":10,"name":"Official","type":"official"},{"id":11,"name":"DVD","type":"dvd"}]`,
			idPath: "id", label: "name",
			want: []sdktest.TraceCandidate{{ID: "10", Fields: map[string]string{"title": "Official"}}},
		},
		{
			name: "root filter and scalar comparisons", path: "$[?type=official,number=1,active=true,weight=1.5]",
			doc: `[
				{"id":10,"name":"Official","type":"official","number":1,"active":true,"weight":1.5},
				{"id":11,"name":"DVD","type":"dvd","number":1,"active":true,"weight":1.5},
				{"id":12,"name":"Inactive","type":"official","number":1,"active":false,"weight":1.5},
				{"id":13,"name":"Unknown","type":"official","number":null,"active":true,"weight":1.5},
				null,
				{"id":14,"name":"Also official","type":"official","number":"1","active":"true","weight":1.5}
			]`,
			idPath: "id", label: "name",
			want: []sdktest.TraceCandidate{
				{ID: "10", Fields: map[string]string{"title": "Official"}},
				{ID: "14", Fields: map[string]string{"title": "Also official"}},
			},
		},
		{
			name: "zero matches", path: "rows[?type.type=null]",
			doc:    `{"rows":[{"type":{"type":null}},{"type":{}},{"type":{"type":[]}},{"type":{"type":{}}}]}`,
			idPath: "id", label: "name", want: []sdktest.TraceCandidate{},
		},
		{
			name: "literal dotted key takes precedence", path: "rows[?type.type=official]",
			doc: `{"rows":[
				{"id":10,"name":"Literal","type.type":"official","type":{"type":"dvd"}},
				{"id":11,"name":"Shadowed","type.type":null,"type":{"type":"official"}},
				{"id":12,"name":"Nested","type":{"type":"official"}}
			]}`,
			idPath: "id", label: "name",
			want: []sdktest.TraceCandidate{
				{ID: "10", Fields: map[string]string{"title": "Literal"}},
				{ID: "12", Fields: map[string]string{"title": "Nested"}},
			},
		},
		{
			name: "single predicate keeps first match", path: "groups[tag.kind=edition,version=1.0].items",
			doc: `{"groups":[
				{"tag.kind":"skip","tag":{"kind":"edition"},"version":"1.0","items":[]},
				{"tag":{"kind":"edition"},"version":"1.0","items":[{"id":10,"name":"First"}]},
				{"tag.kind":"edition","version":"1.0","items":[{"id":11,"name":"Second"}]}
			]}`,
			idPath: "id", label: "name",
			want: []sdktest.TraceCandidate{{ID: "10", Fields: map[string]string{"title": "First"}}},
		},
		{
			name: "single predicate with bracketed value", path: "groups[tag=edition[1]].items",
			doc:    `{"groups":[{"tag":"edition[1]","items":[{"id":10,"name":"Bracketed"}]}]}`,
			idPath: "id", label: "name",
			want: []sdktest.TraceCandidate{{ID: "10", Fields: map[string]string{"title": "Bracketed"}}},
		},
		{
			name: "single predicate with multiple conditions", path: "groups[tag=edition,version=1.0].items",
			doc:    `{"groups":[{"tag":"edition","version":"1.0","items":[{"id":10,"name":"Edition"}]}]}`,
			idPath: "id", label: "name",
			want: []sdktest.TraceCandidate{{ID: "10", Fields: map[string]string{"title": "Edition"}}},
		},
		{
			name: "single predicate with empty value", path: "groups[tag=].items",
			doc:    `{"groups":[{"items":[]},{"tag":"","items":[{"id":10,"name":"Empty tag"}]}]}`,
			idPath: "id", label: "name",
			want: []sdktest.TraceCandidate{{ID: "10", Fields: map[string]string{"title": "Empty tag"}}},
		},
		{
			name: "named array", path: "items", doc: `{"items":[{"id":10,"name":"Named"}]}`,
			idPath: "id", label: "name",
			want: []sdktest.TraceCandidate{{ID: "10", Fields: map[string]string{"title": "Named"}}},
		},
		{
			name: "indexed array", path: "data.0.items", doc: `{"data":[{"items":[{"id":10,"name":"Indexed"}]}]}`,
			idPath: "id", label: "name",
			want: []sdktest.TraceCandidate{{ID: "10", Fields: map[string]string{"title": "Indexed"}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.doc))
			}))
			defer server.Close()
			spec := plugin.ScraperSpec{
				ID: "parity", Kind: "http-json", Endpoint: server.URL,
				SearchTemplate: "/search?q={query}", CandidatesPath: tc.path, CandidateIDPath: tc.idPath,
				SearchFieldMap: map[string]string{"title": tc.label},
				Children: []sdk.ChildListing{{
					Level: "item", ListTemplate: "/list/{id}", ItemsPath: tc.path, Key: tc.idPath, Label: tc.label, Sequence: "number",
				}},
			}
			var doc any
			if err := json.Unmarshal([]byte(tc.doc), &doc); err != nil {
				t.Fatal(err)
			}
			input := sdktest.TraceInput{Query: "example"}
			switch doc := doc.(type) {
			case map[string]any:
				input.SearchDoc = doc
			case []any:
				input.SearchArray = doc
			}
			trace := sdktest.CompileScraperTrace(spec, input)
			engine := New(WithInsecureLoopback())
			candidates, err := engine.Search(t.Context(), spec, input.Query)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := engine.ListChildren(t.Context(), spec, 1, "42", nil, Variables{})
			if err != nil {
				t.Fatal(err)
			}
			if len(candidates) != len(tc.want) || len(rows) != len(tc.want) || len(trace.Candidates) != len(tc.want) {
				t.Fatalf("candidates = %v, children = %v, SDK = %v, want %v", candidates, rows, trace.Candidates, tc.want)
			}
			for i, want := range tc.want {
				label := want.Fields["title"]
				if candidates[i].ID != want.ID || candidates[i].Fields["title"] != label ||
					rows[i].Key != want.ID || rows[i].Label != label ||
					trace.Candidates[i].ID != want.ID || trace.Candidates[i].Fields["title"] != label {
					t.Fatalf("candidate = %+v, child = %+v, trace = %+v, want %+v", candidates[i], rows[i], trace.Candidates[i], want)
				}
			}
			if tc.path == "data.seasons[?type.type=official]" && (rows[0].Sequence == nil || *rows[0].Sequence != "0") {
				t.Fatalf("official zero ordinal missing: %+v", rows)
			}
		})
	}
}

func TestListPathRejectsMisplacedFilter(t *testing.T) {
	for _, path := range []string{"items[?x=y].0", "$.items", "items.$", "$[x=y]", "items[?x=y"} {
		if _, ok := resolveJSONNode(map[string]any{"items": []any{}}, path); ok {
			t.Errorf("invalid list path %q resolved", path)
		}
	}
}

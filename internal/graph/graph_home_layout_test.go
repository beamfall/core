package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store/storetest"
)

type homeLayoutReply struct {
	Data   map[string]HomeLayoutInfo `json:"data"`
	Errors []graphError              `json:"errors"`
}

func postViewerHomeLayout(t *testing.T, handler http.Handler, query string, variables map[string]any) homeLayoutReply {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var reply homeLayoutReply
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return reply
}

const homeLayoutSelection = `{ customized sections { id kind hidden title } }`

func TestHomeLayoutGraphRoundTrip(t *testing.T) {
	handler := newTestHandlerWithStore(storetest.Open(t))

	read := postViewerHomeLayout(t, handler, `query { homeLayout `+homeLayoutSelection+` }`, nil)
	if len(read.Errors) != 0 || read.Data["homeLayout"].Customized || len(read.Data["homeLayout"].Sections) == 0 {
		t.Fatalf("homeLayout = %#v errors %#v", read.Data, read.Errors)
	}
	if first := read.Data["homeLayout"].Sections[0].ID; first != "continue" {
		t.Fatalf("default first section = %q, want continue", first)
	}

	set := postViewerHomeLayout(t, handler,
		`mutation Set($order: [String!], $hidden: [String!]) { setHomeLayout(sectionOrder: $order, hiddenSectionIDs: $hidden) `+homeLayoutSelection+` }`,
		map[string]any{"order": []string{"recent", "continue"}, "hidden": []string{"watchlist"}})
	got := set.Data["setHomeLayout"]
	if len(set.Errors) != 0 || !got.Customized || got.Sections[0].ID != "recent" {
		t.Fatalf("setHomeLayout = %#v errors %#v", got, set.Errors)
	}
	for _, section := range got.Sections {
		if section.Hidden != (section.ID == "watchlist") {
			t.Fatalf("section %s hidden = %v", section.ID, section.Hidden)
		}
	}

	reset := postViewerHomeLayout(t, handler, `mutation { resetHomeLayout `+homeLayoutSelection+` }`, nil)
	if len(reset.Errors) != 0 || reset.Data["resetHomeLayout"].Customized {
		t.Fatalf("resetHomeLayout = %#v errors %#v", reset.Data, reset.Errors)
	}
}

func TestSetHomeLayoutRefusesBadInput(t *testing.T) {
	handler := newTestHandlerWithStore(storetest.Open(t))
	tooMany := make([]string, 501)
	for i := range tooMany {
		tooMany[i] = "x"
	}
	for name, variables := range map[string]map[string]any{
		"oversized":  {"order": tooMany},
		"not-list":   {"order": "recent"},
		"non-string": {"order": []any{1}},
	} {
		reply := postViewerHomeLayout(t, handler,
			`mutation Set($order: [String!]) { setHomeLayout(sectionOrder: $order) { customized } }`, variables)
		if len(reply.Errors) == 0 || strings.Contains(reply.Errors[0].Message, "internal") {
			t.Fatalf("%s: errors = %#v, want a client error", name, reply.Errors)
		}
	}
}

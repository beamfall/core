package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/store/storetest"
)

func postHomeStudioJSON(t *testing.T, handler http.Handler, query string, variables map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var reply map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return reply
}

func TestHomeStudioGraphRoundTrip(t *testing.T) {
	handler := newTestHandlerWithStore(storetest.Open(t))

	// 1. Default homeConfig
	read := postHomeStudioJSON(t, handler, `query { homeConfig { customized hasOwnerTemplate canEditTemplate hero { enabled mode } shelves { id title hidden } } }`, nil)
	if errs, _ := read["errors"].([]any); len(errs) != 0 {
		t.Fatalf("homeConfig errors: %#v", errs)
	}
	data := read["data"].(map[string]any)["homeConfig"].(map[string]any)
	if data["customized"] != false {
		t.Fatalf("expected customized=false, got %v", data["customized"])
	}

	// 2. homeFilterSchema
	schema := postHomeStudioJSON(t, handler, `query { homeFilterSchema { libraries { id title } fields { key valueType group operators } pinnables { collections { id name } } } }`, nil)
	if errs, _ := schema["errors"].([]any); len(errs) != 0 {
		t.Fatalf("homeFilterSchema errors: %#v", errs)
	}

	// 3. saveHomeConfig with a custom rules shelf
	cfg := map[string]any{
		"version": 1,
		"hero":    map[string]any{"enabled": false, "mode": "auto"},
		"shelves": []map[string]any{
			{
				"id":            "hs_custom_1",
				"title":         "Unwatched Picks",
				"hidden":        false,
				"hideWhenEmpty": true,
				"display":       map[string]any{"style": "grid", "artworkSlot": "poster", "limit": 12},
				"source": map[string]any{
					"type": "rules",
					"rules": map[string]any{
						"match": "all",
						"conditions": []map[string]any{
							{"field": "watched", "op": "isFalse"},
						},
					},
					"sort": map[string]any{"by": "added", "desc": true},
				},
			},
		},
	}
	saved := postHomeStudioJSON(t, handler, `mutation Save($config: HomeConfigInput!) { saveHomeConfig(config: $config) { customized hero { enabled } shelves { id title } } }`, map[string]any{"config": cfg})
	if errs, _ := saved["errors"].([]any); len(errs) != 0 {
		t.Fatalf("saveHomeConfig errors: %#v", errs)
	}
	savedCfg := saved["data"].(map[string]any)["saveHomeConfig"].(map[string]any)
	if savedCfg["customized"] != true {
		t.Fatalf("expected customized=true, got %v", savedCfg["customized"])
	}

	// 4. previewHomeShelf
	prev := postHomeStudioJSON(t, handler, `query Prev($shelf: HomeShelfInput!) { previewHomeShelf(shelf: $shelf, limit: 5) { totalCount items { id title } } }`, map[string]any{
		"shelf": cfg["shelves"].([]map[string]any)[0],
	})
	if errs, _ := prev["errors"].([]any); len(errs) != 0 {
		t.Fatalf("previewHomeShelf errors: %#v", errs)
	}

	// 5. sourceItems(kind: "home-shelf", value: "hs_custom_1")
	src := postHomeStudioJSON(t, handler, `query { sourceItems(kind: "home-shelf", value: "hs_custom_1", limit: 10) { id title } }`, nil)
	if errs, _ := src["errors"].([]any); len(errs) != 0 {
		t.Fatalf("sourceItems home-shelf errors: %#v", errs)
	}

	// 6. resetHomeConfig
	rst := postHomeStudioJSON(t, handler, `mutation { resetHomeConfig { customized } }`, nil)
	if errs, _ := rst["errors"].([]any); len(errs) != 0 {
		t.Fatalf("resetHomeConfig errors: %#v", errs)
	}
	if rst["data"].(map[string]any)["resetHomeConfig"].(map[string]any)["customized"] != false {
		t.Fatalf("expected customized=false after reset")
	}

	// 7. saveDefaultHomeTemplate + resetDefaultHomeTemplate (manager)
	tmplBody, _ := json.Marshal(map[string]any{
		"query":     `mutation SaveTmpl($config: HomeConfigInput!) { saveDefaultHomeTemplate(config: $config) { hasOwnerTemplate } }`,
		"variables": map[string]any{"config": cfg},
	})
	tmplReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(tmplBody))
	authorizeManager(tmplReq)
	tmplRec := httptest.NewRecorder()
	handler.ServeHTTP(tmplRec, tmplReq)
	if tmplRec.Code != http.StatusOK {
		t.Fatalf("saveDefaultHomeTemplate status = %d: %s", tmplRec.Code, tmplRec.Body.String())
	}

	rstTmplBody, _ := json.Marshal(map[string]any{
		"query": `mutation { resetDefaultHomeTemplate { hasOwnerTemplate } }`,
	})
	rstTmplReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(rstTmplBody))
	authorizeManager(rstTmplReq)
	rstTmplRec := httptest.NewRecorder()
	handler.ServeHTTP(rstTmplRec, rstTmplReq)
	if rstTmplRec.Code != http.StatusOK {
		t.Fatalf("resetDefaultHomeTemplate status = %d: %s", rstTmplRec.Code, rstTmplRec.Body.String())
	}
}

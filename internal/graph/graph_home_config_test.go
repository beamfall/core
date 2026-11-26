package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
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

func TestHomeShelfEvaluatesRollUpContainersForAudioMultiVsSingle(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	bundle := plugin.Bundle{
		ID:          "audiokit",
		Version:     "0.1.0",
		DisplayName: plugin.L("Audiokit"),
		MediaLabel:  plugin.L("Cut"),
		EntityKinds: []plugin.EntityKind{
			{Name: "holder", Archetype: "container", OrderedChildren: true, Relations: []plugin.RelationSpec{
				{Name: "holds-cut", ToKind: plugin.MediaKindName, Cardinality: "one"},
			}},
		},
		Layouts: []plugin.LayoutSpec{{
			ID:   "home",
			Role: "browse",
			Sections: []plugin.SectionSpec{
				{ID: "holders", Kind: "grid", Title: plugin.L("Holders"), Source: plugin.SourceSpec{Kind: "entity-items", EntityKind: "holder"}},
			},
		}},
	}
	registry, err := newTestPluginRegistry([]plugin.Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		auth.NewService(st),
		st,
		registry,
	)
	library, err := st.CreateLibrary(ctx, "Audio", false, "audiokit", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	importCut := func(name string) store.MediaID {
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/hs/" + name + ".flac", Title: name,
			Container: "flac", Codec: "flac", Class: store.MediaClassAudio, ContentFingerprint: "fp-hs-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		return item.ID
	}
	c1 := importCut("Cut 1")
	c2 := importCut("Cut 2")
	single := importCut("Solo Cut")

	for _, e := range []store.Entity{
		{ID: "hs-multi", LibraryID: library.ID, Kind: "holder", Title: "Full Release"},
		{ID: "hs-single", LibraryID: library.ID, Kind: "holder", Title: "Solo Release"},
	} {
		if err := st.UpsertEntity(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []store.EntityRelation{
		{FromEntityID: "hs-multi", Name: "holds-cut", ToMediaID: c1},
		{FromEntityID: "hs-multi", Name: "holds-cut", ToMediaID: c2},
		{FromEntityID: "hs-single", Name: "holds-cut", ToMediaID: single},
	} {
		if err := st.UpsertEntityRelation(ctx, rel); err != nil {
			t.Fatal(err)
		}
	}

	preview := func(rollUp any) []map[string]any {
		src := map[string]any{
			"type":       "library",
			"libraryIDs": []string{string(library.ID)},
		}
		if rollUp != nil {
			src["rollUpContainers"] = rollUp
		}
		reply := postHomeStudioJSON(t, handler, `query Prev($shelf: HomeShelfInput!) { previewHomeShelf(shelf: $shelf, limit: 10) { totalCount items { id kind title } } }`, map[string]any{
			"shelf": map[string]any{
				"id":      "hs-test",
				"title":   "Audio Recent",
				"display": map[string]any{"style": "shelf", "artworkSlot": "poster", "limit": 10},
				"source":  src,
			},
		})
		if errs, _ := reply["errors"].([]any); len(errs) != 0 {
			t.Fatalf("previewHomeShelf errors: %#v", errs)
		}
		rawItems := reply["data"].(map[string]any)["previewHomeShelf"].(map[string]any)["items"].([]any)
		out := make([]map[string]any, 0, len(rawItems))
		for _, item := range rawItems {
			out = append(out, item.(map[string]any))
		}
		return out
	}

	// Default (rollUpContainers omitted): multi-track container collapses into 1 entity,
	// while 1-track single stays media.
	defaultItems := preview(nil)
	if len(defaultItems) != 2 ||
		defaultItems[0]["id"] != string(single) || defaultItems[0]["kind"] != "media" ||
		defaultItems[1]["id"] != "hs-multi" || defaultItems[1]["kind"] != "entity" {
		t.Fatalf("default home-shelf rollUp = %#v, want [%s(media), hs-multi(entity)]", defaultItems, single)
	}

	// Explicit rollUpContainers=false keeps all 3 individual media tracks unrolled.
	rawItems := preview(false)
	if len(rawItems) != 3 {
		t.Fatalf("rollUpContainers=false len = %d, want 3 media items: %#v", len(rawItems), rawItems)
	}
}

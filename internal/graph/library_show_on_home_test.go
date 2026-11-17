package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// postManagerGraphQL runs one GraphQL body as the manager and decodes the reply.
func postManagerGraphQL(t *testing.T, handler http.Handler, body string, out any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
}

const setShowOnHomeQuery = `mutation SetLibraryShowOnHome($libraryID: String!, $enabled: Boolean!) { setLibraryShowOnHome(libraryID: $libraryID, enabled: $enabled) { id showOnHome } }`

type showOnHomeReply struct {
	Data struct {
		SetLibraryShowOnHome LibraryInfo `json:"setLibraryShowOnHome"`
	} `json:"data"`
	Errors []graphError `json:"errors"`
}

func setShowOnHomeBody(libraryID string, enabled bool) string {
	body, _ := json.Marshal(map[string]any{
		"query":     setShowOnHomeQuery,
		"variables": map[string]any{"libraryID": libraryID, "enabled": enabled},
	})
	return string(body)
}

func TestSetLibraryShowOnHomeMutationIsAdminGatedAuditedAndEchoesState(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	var denied showOnHomeReply
	postManagerGraphQL(t, handler, setShowOnHomeBody("library-local", false), &denied)
	if len(denied.Errors) == 0 || denied.Errors[0].Message != "admin session required" {
		t.Fatalf("non-admin setLibraryShowOnHome errors = %#v", denied.Errors)
	}

	openManagerAdminSession(t, st)
	var changed showOnHomeReply
	postManagerGraphQL(t, handler, setShowOnHomeBody("library-local", false), &changed)
	if len(changed.Errors) != 0 {
		t.Fatalf("setLibraryShowOnHome errors = %#v", changed.Errors)
	}
	if got := changed.Data.SetLibraryShowOnHome; got.ID != "library-local" || got.ShowOnHome {
		t.Fatalf("setLibraryShowOnHome = %#v, want library-local with showOnHome=false", got)
	}

	var listed struct {
		Data struct {
			Libraries []LibraryInfo `json:"libraries"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	postManagerGraphQL(t, handler, `{"query":"query { libraries { id showOnHome } }"}`, &listed)
	if len(listed.Errors) != 0 {
		t.Fatalf("libraries errors = %#v", listed.Errors)
	}
	for _, library := range listed.Data.Libraries {
		if want := library.ID != "library-local"; library.ShowOnHome != want {
			t.Fatalf("libraries %s showOnHome = %v, want %v", library.ID, library.ShowOnHome, want)
		}
	}

	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].Action != store.ActivityLibraryShowOnHome ||
		entries[0].TargetType != "library" || entries[0].TargetID != "library-local" ||
		entries[0].LibraryID != "library-local" {
		t.Fatalf("activity entries = %+v, want audited show-on-home change", entries)
	}

	var unknown showOnHomeReply
	postManagerGraphQL(t, handler, setShowOnHomeBody("library-missing", true), &unknown)
	if len(unknown.Errors) == 0 {
		t.Fatal("setLibraryShowOnHome accepted an unknown Library")
	}

	var restored showOnHomeReply
	postManagerGraphQL(t, handler, setShowOnHomeBody("library-local", true), &restored)
	if len(restored.Errors) != 0 || !restored.Data.SetLibraryShowOnHome.ShowOnHome {
		t.Fatalf("re-show setLibraryShowOnHome = %#v", restored)
	}
}

func TestSourceItemsHomeArgumentSkipsLibrariesHiddenFromHome(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	sourceIDs := func(args string) []string {
		t.Helper()
		var reply struct {
			Data struct {
				SourceItems []SourceItem `json:"sourceItems"`
			} `json:"data"`
			Errors []graphError `json:"errors"`
		}
		body, _ := json.Marshal(map[string]any{"query": `query { sourceItems(` + args + `) { id } }`})
		postManagerGraphQL(t, handler, string(body), &reply)
		if len(reply.Errors) != 0 {
			t.Fatalf("sourceItems(%s) errors = %#v", args, reply.Errors)
		}
		ids := make([]string, 0, len(reply.Data.SourceItems))
		for _, item := range reply.Data.SourceItems {
			ids = append(ids, item.ID)
		}
		return ids
	}

	before := sourceIDs(`kind: "recently-added", home: true`)
	if !slices.Contains(before, "media-alpha") {
		t.Fatalf("Home recently-added = %v, want media-alpha before hiding", before)
	}
	if err := st.SetLibraryShowOnHome(context.Background(), "library-local", false); err != nil {
		t.Fatal(err)
	}
	if got := sourceIDs(`kind: "recently-added", home: true`); slices.Contains(got, "media-alpha") {
		t.Fatalf("Home recently-added = %v, want media-alpha left out", got)
	}
	// Without home, and inside the hidden Library, its Media still appear.
	if got := sourceIDs(`kind: "recently-added"`); !slices.Contains(got, "media-alpha") {
		t.Fatalf("aggregate recently-added = %v, want media-alpha", got)
	}
	if got := sourceIDs(`kind: "recently-added", libraryID: "library-local", home: true`); !slices.Contains(got, "media-alpha") {
		t.Fatalf("library-local recently-added = %v, want media-alpha", got)
	}
}

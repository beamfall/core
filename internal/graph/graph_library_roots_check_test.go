package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// CORE-1598: a Library folder the server cannot use is refused with a plain
// message naming the path, and the refusal creates or changes nothing.
func TestLibraryMutationsRefuseMissingRootAndCreateNothing(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	missing := filepath.Join(t.TempDir(), "missing")
	want := "The folder " + missing + " doesn't exist on the server."
	quoted := strings.ReplaceAll(missing, `\`, `\\`)

	post := func(body string) []graphError {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorizeManager(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var resp struct {
			Errors []graphError `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Errors
	}
	libraryCount := func() int {
		t.Helper()
		libraries, err := st.Libraries(t.Context(), store.LibraryFilter{Revealed: true, AllLibraries: true})
		if err != nil {
			t.Fatal(err)
		}
		return len(libraries)
	}

	before := libraryCount()
	errs := post(`{"query":"mutation($title: String!, $roots: [String!]) { createLibrary(title: $title, roots: $roots) { id } }","variables":{"title":"Films","roots":["` + quoted + `"]}}`)
	if len(errs) != 1 || errs[0].Message != want {
		t.Fatalf("createLibrary errors = %#v, want %q", errs, want)
	}
	if after := libraryCount(); after != before {
		t.Fatalf("refused createLibrary changed Library count %d -> %d", before, after)
	}

	errs = post(`{"query":"mutation($libraryID: String!, $roots: [String!]!) { setLibraryRoots(libraryID: $libraryID, roots: $roots) { id } }","variables":{"libraryID":"library-local","roots":["` + quoted + `"]}}`)
	if len(errs) != 1 || errs[0].Message != want {
		t.Fatalf("setLibraryRoots errors = %#v, want %q", errs, want)
	}
	roots, err := st.LibraryRoots(t.Context(), "library-local")
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range roots {
		if root == missing {
			t.Fatalf("refused setLibraryRoots stored %q", missing)
		}
	}
}

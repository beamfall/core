package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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

	valid := t.TempDir()
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{valid}); err != nil {
		t.Fatal(err)
	}
	rootsBefore, err := st.LibraryRoots(t.Context(), "library-local")
	if err != nil {
		t.Fatal(err)
	}
	errs = post(setLibraryRootsBody("library-local", valid, missing))
	if len(errs) != 1 || errs[0].Message != want {
		t.Fatalf("setLibraryRoots errors = %#v, want %q", errs, want)
	}
	rootsAfter, err := st.LibraryRoots(t.Context(), "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(rootsBefore) != 1 || !slices.Equal(rootsAfter, rootsBefore) {
		t.Fatalf("refused setLibraryRoots changed roots %q -> %q", rootsBefore, rootsAfter)
	}
}

// CORE-1606: a Library whose saved folders have all gone missing can still be
// repaired by removing them; only a folder the update adds is checked.
func TestSetLibraryRootsKeepsSavedMissingRootsAndChecksAddedOnes(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	gone := t.TempDir()
	missingA := filepath.Join(gone, "a")
	missingB := filepath.Join(gone, "b")
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{missingA, missingB}); err != nil {
		t.Fatal(err)
	}
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
	roots := func() []string {
		t.Helper()
		roots, err := st.LibraryRoots(t.Context(), "library-local")
		if err != nil {
			t.Fatal(err)
		}
		return roots
	}

	added := filepath.Join(gone, "c")
	errs := post(setLibraryRootsBody("library-local", missingA, added))
	want := "The folder " + added + " doesn't exist on the server."
	if len(errs) != 1 || errs[0].Message != want {
		t.Fatalf("adding a missing folder: errors = %#v, want %q", errs, want)
	}
	if got := roots(); !slices.Equal(got, []string{missingA, missingB}) {
		t.Fatalf("refused update changed roots to %q", got)
	}

	if errs := post(setLibraryRootsBody("library-local", missingA)); len(errs) != 0 {
		t.Fatalf("removing one missing folder: errors = %#v, want none", errs)
	}
	if got := roots(); !slices.Equal(got, []string{missingA}) {
		t.Fatalf("roots after removal = %q, want %q", got, []string{missingA})
	}
}

func setLibraryRootsBody(libraryID string, roots ...string) string {
	variables, err := json.Marshal(map[string]any{"libraryID": libraryID, "roots": roots})
	if err != nil {
		panic(err)
	}
	return `{"query":"mutation($libraryID: String!, $roots: [String!]!) { setLibraryRoots(libraryID: $libraryID, roots: $roots) { id } }","variables":` + string(variables) + `}`
}

// CORE-1202: createLibrary and setLibraryRoots refuse folders that equal,
// contain or sit inside another library's folder, matching PathPicker's
// eligibility rule.
func TestLibraryMutationsRefuseOverlappingRootAndKeepState(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	base := t.TempDir()
	tvRoot := filepath.Join(base, "tv")
	tvChild := filepath.Join(tvRoot, "nested")
	filmsRoot := filepath.Join(base, "films")
	filmsChild := filepath.Join(filmsRoot, "extra")
	if err := os.MkdirAll(tvChild, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filmsChild, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLibraryWithRoots(t.Context(), "TV", false, "", "profile-default", []string{tvRoot}, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(t.Context(), "library-local", []string{filmsRoot}); err != nil {
		t.Fatal(err)
	}

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
	for _, overlap := range []string{tvRoot, base, tvChild} {
		variables, err := json.Marshal(map[string]any{"title": "Overlap", "roots": []string{overlap}})
		if err != nil {
			t.Fatal(err)
		}
		errs := post(`{"query":"mutation($title: String!, $roots: [String!]) { createLibrary(title: $title, roots: $roots) { id } }","variables":` + string(variables) + `}`)
		want := "The folder " + overlap + " is already part of a library."
		if len(errs) != 1 || errs[0].Message != want {
			t.Fatalf("createLibrary(%q) errors = %#v, want %q", overlap, errs, want)
		}
	}
	if after := libraryCount(); after != before {
		t.Fatalf("refused createLibrary changed Library count %d -> %d", before, after)
	}

	for _, overlap := range []string{tvRoot, base, tvChild, filmsChild} {
		errs := post(setLibraryRootsBody("library-local", filmsRoot, overlap))
		want := "The folder " + overlap + " is already part of a library."
		if len(errs) != 1 || errs[0].Message != want {
			t.Fatalf("setLibraryRoots(%q) errors = %#v, want %q", overlap, errs, want)
		}
	}
	rootsAfter, err := st.LibraryRoots(t.Context(), "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rootsAfter, []string{filmsRoot}) {
		t.Fatalf("refused setLibraryRoots changed roots to %q", rootsAfter)
	}
}

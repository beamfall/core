package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/store/storetest"
)

// postLibraryTrash sends one GraphQL request as the manager and decodes the
// data and error messages (CORE-1201).
func postLibraryTrash(t *testing.T, handler http.Handler, body string) (map[string]json.RawMessage, []string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	messages := make([]string, 0, len(resp.Errors))
	for _, e := range resp.Errors {
		messages = append(messages, e.Message)
	}
	return resp.Data, messages
}

// The deleted-Library fields are served by the dispatcher, behind the Admin
// Session gate like deleteLibrary, with the shapes the Console asks for.
func TestLibraryTrashFieldsAreDispatchedAndAdminGated(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	const (
		list    = `{"query":"query TrashedLibraries { trashedLibraries { id title trashedAt mediaCount purgeable purgeAfter } }"}`
		del     = `{"query":"mutation DeleteLibrary($libraryID: String!) { deleteLibrary(libraryID: $libraryID) }","variables":{"libraryID":"library-local"}}`
		restore = `{"query":"mutation RestoreLibrary($libraryID: String!) { restoreLibrary(libraryID: $libraryID) }","variables":{"libraryID":"library-local"}}`
		purge   = `{"query":"mutation PurgeLibrary($libraryID: String!, $confirm: Boolean!) { purgeLibrary(libraryID: $libraryID, confirm: $confirm) { libraryID title mediaRemoved entitiesRemoved } }","variables":{"libraryID":"library-local","confirm":true}}`
	)

	for name, body := range map[string]string{"trashedLibraries": list, "restoreLibrary": restore, "purgeLibrary": purge} {
		if _, errs := postLibraryTrash(t, handler, body); len(errs) == 0 || errs[0] != "admin session required" {
			t.Fatalf("%s without an Admin Session: errors = %q, want admin session required", name, errs)
		}
	}

	openManagerAdminSession(t, st)
	if _, errs := postLibraryTrash(t, handler, del); len(errs) != 0 {
		t.Fatalf("deleteLibrary errors = %q", errs)
	}
	data, errs := postLibraryTrash(t, handler, list)
	if len(errs) != 0 {
		t.Fatalf("trashedLibraries errors = %q", errs)
	}
	var listed []TrashedLibraryInfo
	if err := json.Unmarshal(data["trashedLibraries"], &listed); err != nil || len(listed) != 1 || listed[0].ID != "library-local" {
		t.Fatalf("trashedLibraries = %s (%v), want library-local", data["trashedLibraries"], err)
	}

	data, errs = postLibraryTrash(t, handler, restore)
	if len(errs) != 0 || string(data["restoreLibrary"]) != "true" {
		t.Fatalf("restoreLibrary = %s, errors %q; want true", data["restoreLibrary"], errs)
	}

	if _, errs := postLibraryTrash(t, handler, del); len(errs) != 0 {
		t.Fatalf("second deleteLibrary errors = %q", errs)
	}
	data, errs = postLibraryTrash(t, handler, purge)
	if len(errs) != 0 {
		t.Fatalf("purgeLibrary errors = %q", errs)
	}
	var purged LibraryPurgeResult
	if err := json.Unmarshal(data["purgeLibrary"], &purged); err != nil || purged.LibraryID != "library-local" {
		t.Fatalf("purgeLibrary = %s (%v), want library-local", data["purgeLibrary"], err)
	}
}

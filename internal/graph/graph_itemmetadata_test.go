package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// TestItemMetadataEditorLockSurvivesRefresh proves the CORE-1715 contract
// end to end: an editor write locks the fields it touches, and a per-item
// refresh re-runs the provider for the same candidate without overwriting a
// locked field while unlocked fields take the provider's values again.
func TestItemMetadataEditorLockSurvivesRefresh(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	handler.identify.SetProvenance(st)
	ctx := context.Background()

	post := func(query string, vars map[string]any, authorize func(*http.Request)) *httptest.ResponseRecorder {
		v, err := json.Marshal(vars)
		if err != nil {
			t.Fatal(err)
		}
		q, err := json.Marshal(query)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":`+string(q)+`,"variables":`+string(v)+`}`)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	const readQuery = `query { mediaMetadata { mediaID title sortTitle summary year refreshable locks { field locked } } }`
	const editQuery = `mutation { editMediaMetadata { mediaID title sortTitle summary year refreshable locks { field locked } } }`
	const refreshQuery = `mutation { refreshMediaMetadata { mediaID title sortTitle summary year refreshable locks { field locked } } }`
	const applyQuery = `mutation IdentifyApply($mediaID: String!, $pluginID: String!, $scraperID: String!, $candidateID: String!) { identifyApply(mediaID: $mediaID, pluginID: $pluginID, scraperID: $scraperID, candidateID: $candidateID) { id } }`
	alpha := map[string]any{"mediaID": "media-alpha"}

	// Viewers never reach the editor.
	for _, q := range []string{readQuery, editQuery, refreshQuery} {
		if rec := post(q, alpha, authorizeViewer); !bytes.Contains(rec.Body.Bytes(), []byte("manager role required")) {
			t.Fatalf("viewer %q = %s", q, rec.Body.String())
		}
	}

	// Before any identification there is nothing to refresh.
	info := decodeMediaMetadata(t, post(readQuery, alpha, authorizeManager), "mediaMetadata")
	if info.Refreshable {
		t.Fatalf("unidentified media refreshable = true")
	}
	if rec := post(refreshQuery, alpha, authorizeManager); !bytes.Contains(rec.Body.Bytes(), []byte("no identification to refresh")) {
		t.Fatalf("refresh without identification = %s", rec.Body.String())
	}

	// Fix Match records the candidate the editor's refresh re-applies.
	if rec := post(applyQuery, map[string]any{"mediaID": "media-alpha", "pluginID": "demo", "scraperID": "lookup", "candidateID": "42"}, authorizeManager); bytes.Contains(rec.Body.Bytes(), []byte(`"errors"`)) {
		t.Fatalf("identifyApply = %s", rec.Body.String())
	}

	// The admin edits the title and sort title: both lock, nothing else does.
	info = decodeMediaMetadata(t, post(editQuery, map[string]any{"mediaID": "media-alpha", "title": "Admin Title", "sortTitle": "Title, Admin"}, authorizeManager), "editMediaMetadata")
	if info.Title != "Admin Title" || info.SortTitle != "Title, Admin" || !info.Refreshable {
		t.Fatalf("after edit = %#v", info)
	}
	locks := fieldLockMap(info.Locks)
	if !locks["title"] || !locks["sortTitle"] || locks["summary"] || locks["year"] {
		t.Fatalf("locks after edit = %#v", locks)
	}

	// Give the summary a non-provider value, then unlock it, so the refresh
	// has an unlocked field to restore.
	if rec := post(editQuery, map[string]any{"mediaID": "media-alpha", "summary": "Drifted."}, authorizeManager); bytes.Contains(rec.Body.Bytes(), []byte(`"errors"`)) {
		t.Fatalf("summary edit = %s", rec.Body.String())
	}
	if rec := post(`mutation { setMediaFieldLock { field locked } }`, map[string]any{"mediaID": "media-alpha", "field": "summary", "locked": false}, authorizeManager); bytes.Contains(rec.Body.Bytes(), []byte(`"errors"`)) {
		t.Fatalf("unlock summary = %s", rec.Body.String())
	}

	info = decodeMediaMetadata(t, post(refreshQuery, alpha, authorizeManager), "refreshMediaMetadata")
	if info.Title != "Admin Title" || info.SortTitle != "Title, Admin" {
		t.Fatalf("refresh overwrote a locked field: %#v", info)
	}
	if info.Summary != "A classic." || info.Year != 1977 {
		t.Fatalf("refresh did not update unlocked fields: %#v", info)
	}
	locks = fieldLockMap(info.Locks)
	if !locks["title"] || locks["summary"] {
		t.Fatalf("locks after refresh = %#v", locks)
	}

	// Unlocking the title hands it back to the provider on the next refresh.
	if _, err := st.SetMediaFieldLock(ctx, "media-alpha", "title", false, store.LibraryFilter{Revealed: true, AllLibraries: true}); err != nil {
		t.Fatal(err)
	}
	info = decodeMediaMetadata(t, post(refreshQuery, alpha, authorizeManager), "refreshMediaMetadata")
	if info.Title != "Blue Harvest" {
		t.Fatalf("unlocked title after refresh = %q, want provider value", info.Title)
	}

	// Edit validation.
	for _, vars := range []map[string]any{
		{"mediaID": "media-alpha"},
		{"mediaID": "media-alpha", "title": "  "},
		{"mediaID": "media-alpha", "year": 1999.5},
		{"mediaID": "media-alpha", "year": 10000},
	} {
		if rec := post(editQuery, vars, authorizeManager); !bytes.Contains(rec.Body.Bytes(), []byte(`"errors"`)) {
			t.Fatalf("edit %#v accepted: %s", vars, rec.Body.String())
		}
	}

	// A Media in a Library the manager has not revealed is not found.
	hidden := map[string]any{"mediaID": "media-archive"}
	for _, q := range []string{readQuery, refreshQuery} {
		if rec := post(q, hidden, authorizeManager); !bytes.Contains(rec.Body.Bytes(), []byte("unknown media")) {
			t.Fatalf("hidden %q = %s", q, rec.Body.String())
		}
	}
	if rec := post(editQuery, map[string]any{"mediaID": "media-archive", "title": "Leak"}, authorizeManager); !bytes.Contains(rec.Body.Bytes(), []byte("unknown media")) {
		t.Fatalf("hidden edit = %s", rec.Body.String())
	}
}

func decodeMediaMetadata(t *testing.T, rec *httptest.ResponseRecorder, field string) MediaMetadataInfo {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data   map[string]MediaMetadataInfo `json:"data"`
		Errors []struct{ Message string }   `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	return resp.Data[field]
}

func fieldLockMap(locks []FieldLockInfo) map[string]bool {
	out := make(map[string]bool, len(locks))
	for _, l := range locks {
		out[l.Field] = l.Locked
	}
	return out
}

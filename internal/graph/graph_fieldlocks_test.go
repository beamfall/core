package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestMediaFieldLocksReadAndSetManagerGated(t *testing.T) {
	// feature:field-locks
	st := storetest.Open(t)
	ctx := context.Background()
	handler := newTestHandlerWithStore(st)

	// A freshly imported file in a manager-visible Library: nothing locked yet.
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/lockable.mp4",
		Title:              "lockable",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-lockable",
	})
	if err != nil {
		t.Fatal(err)
	}
	// A file in the restricted Library — invisible to an unrevealed manager.
	hidden, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-archive",
		Path:               "/media/hidden.mp4",
		Title:              "hidden",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-hidden",
	})
	if err != nil {
		t.Fatal(err)
	}

	post := func(body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	readBody := `{"query":"query { mediaFieldLocks { field locked } }","variables":{"mediaID":"` + string(media.ID) + `"}}`
	setBody := func(id, field string, locked bool) string {
		v, _ := json.Marshal(map[string]any{"mediaID": id, "field": field, "locked": locked})
		return `{"query":"mutation { setMediaFieldLock { field locked } }","variables":` + string(v) + `}`
	}

	// Viewer role is refused.
	if rec := post(readBody, authorizeViewer); !bytes.Contains(rec.Body.Bytes(), []byte("manager role required")) {
		t.Fatalf("viewer mediaFieldLocks = %s", rec.Body.String())
	}

	// Initial read: the three lockable fields, all unlocked.
	locks := decodeFieldLocks(t, post(readBody, authorizeManager), "mediaFieldLocks")
	wantFields := map[string]bool{"title": false, "summary": false, "year": false}
	if len(locks) != len(wantFields) {
		t.Fatalf("locks = %#v, want %d fields", locks, len(wantFields))
	}
	for _, l := range locks {
		if want, ok := wantFields[l.Field]; !ok || l.Locked != want {
			t.Fatalf("initial lock %q = %v, want %v", l.Field, l.Locked, want)
		}
	}

	// Lock the title; only the title flips.
	locks = decodeFieldLocks(t, post(setBody(string(media.ID), "title", true), authorizeManager), "setMediaFieldLock")
	for _, l := range locks {
		want := l.Field == "title"
		if l.Locked != want {
			t.Fatalf("after lock-title %q = %v, want %v", l.Field, l.Locked, want)
		}
	}

	// An unknown field is a client error.
	if rec := post(setBody(string(media.ID), "rating", true), authorizeManager); !bytes.Contains(rec.Body.Bytes(), []byte("unknown lockable field")) {
		t.Fatalf("unknown-field set = %s", rec.Body.String())
	}

	// The restricted Media is "not found" to an unrevealed manager — never an
	// existence oracle for its lock state.
	hiddenRead := `{"query":"query { mediaFieldLocks { field locked } }","variables":{"mediaID":"` + string(hidden.ID) + `"}}`
	if rec := post(hiddenRead, authorizeManager); !bytes.Contains(rec.Body.Bytes(), []byte("media not found")) {
		t.Fatalf("hidden mediaFieldLocks = %s", rec.Body.String())
	}
}

func decodeFieldLocks(t *testing.T, rec *httptest.ResponseRecorder, field string) []FieldLockInfo {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data   map[string][]FieldLockInfo `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	return resp.Data[field]
}

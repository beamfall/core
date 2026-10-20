package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeWatcherProvider is a test double for the WatcherStatusProvider seam: it
// answers from a fixed per-Library state map, defaulting unknown Libraries to
// the honest unavailable state.
type fakeWatcherProvider struct {
	states map[store.LibraryID]struct {
		active    bool
		available bool
		reason    string
	}
}

func (f fakeWatcherProvider) Status(libraryID store.LibraryID) (bool, bool, string) {
	s, ok := f.states[libraryID]
	if !ok {
		return false, false, ""
	}
	return s.active, s.available, s.reason
}

func TestWatcherStatusAdminGatedAndLibraryFiltered(t *testing.T) {
	// feature:watcher
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	body := []byte(`{"query":"query { watcherStatus { libraryID title state reason } }"}`)
	post := func(authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
		authorize(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// A session without an open Admin Session is refused — watcher status is an
	// operator-Console read.
	if rec := post(authorizeManager); !bytes.Contains(rec.Body.Bytes(), []byte("admin session required")) {
		t.Fatalf("non-admin watcherStatus = %s", rec.Body.String())
	}
	openManagerAdminSession(t, st)

	// No provider wired: every visible Library reports the honest unavailable
	// state (the Watcher is never faked running — Schedules are the backstop).
	rec := post(authorizeManager)
	statuses := decodeWatcherStatus(t, rec)
	if len(statuses) == 0 {
		t.Fatalf("expected at least one visible library, got none")
	}
	for _, s := range statuses {
		if s.State != "unavailable" {
			t.Fatalf("no-provider state for %s = %q, want unavailable", s.LibraryID, s.State)
		}
		// The restricted seed Library must not appear to an unrevealed manager.
		if s.LibraryID == "library-archive" {
			t.Fatalf("hidden library-archive leaked into watcher status")
		}
	}

	// Wire a provider: library-local is actively watching, a degraded Library
	// surfaces a token-free reason.
	handler.SetWatcherStatus(fakeWatcherProvider{states: map[store.LibraryID]struct {
		active    bool
		available bool
		reason    string
	}{
		"library-local": {active: true},
	}})
	rec = post(authorizeManager)
	statuses = decodeWatcherStatus(t, rec)
	var sawWatching bool
	for _, s := range statuses {
		if s.LibraryID == "library-local" {
			sawWatching = s.State == "watching"
		}
	}
	if !sawWatching {
		t.Fatalf("library-local state = %#v, want watching", statuses)
	}
}

func decodeWatcherStatus(t *testing.T, rec *httptest.ResponseRecorder) []WatcherStatusInfo {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			WatcherStatus []WatcherStatusInfo `json:"watcherStatus"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	return resp.Data.WatcherStatus
}

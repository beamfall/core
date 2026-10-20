package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func postGraph(t *testing.T, handler *Handler, body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorize(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	return rec
}

func allActivity(t *testing.T, st *store.Store) []store.ActivityEntry {
	t.Helper()
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestGraphMutationsWriteActivityEntries(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	// Failed then successful reveal, then relock.
	postGraph(t, handler, `{"query":"mutation Reveal($pin: String!) { reveal(pin: $pin) { revealed } }","variables":{"pin":"9999"}}`, authorizeViewer)
	postGraph(t, handler, `{"query":"mutation Reveal($pin: String!) { reveal(pin: $pin) { revealed } }","variables":{"pin":"1234"}}`, authorizeViewer)
	postGraph(t, handler, `{"query":"mutation { relock { revealed } }"}`, authorizeViewer)
	// Profile switch.
	postGraph(t, handler, `{"query":"mutation Switch($profileID: String!) { switchProfile(profileID: $profileID) { id } }","variables":{"profileID":"profile-managed"}}`, authorizeViewer)
	// Trash + restore (manager).
	postGraph(t, handler, `{"query":"mutation TrashMedia($mediaID: String!) { trashMedia(mediaID: $mediaID) { id } }","variables":{"mediaID":"media-alpha"}}`, authorizeManager)
	postGraph(t, handler, `{"query":"mutation RestoreMedia($mediaID: String!) { restoreMedia(mediaID: $mediaID) { id } }","variables":{"mediaID":"media-alpha"}}`, authorizeManager)

	entries := allActivity(t, st)
	// Newest first.
	wantActions := []string{
		store.ActivityMediaRestore,
		store.ActivityMediaTrash,
		store.ActivityProfileSwitch,
		store.ActivityRelock,
		store.ActivityReveal,
		store.ActivityRevealDenied,
	}
	if len(entries) != len(wantActions) {
		t.Fatalf("entries = %d (%+v), want %d", len(entries), entries, len(wantActions))
	}
	for i, want := range wantActions {
		if entries[i].Action != want {
			t.Fatalf("entries[%d].Action = %q, want %q", i, entries[i].Action, want)
		}
	}
	restore, trash, switched := entries[0], entries[1], entries[2]
	if trash.TargetID != "media-alpha" || trash.LibraryID != "library-local" || trash.SessionID != "session-manager" {
		t.Fatalf("trash entry = %+v", trash)
	}
	if restore.TargetID != "media-alpha" || restore.LibraryID != "library-local" {
		t.Fatalf("restore entry = %+v", restore)
	}
	if switched.TargetType != "profile" || switched.TargetID != "profile-managed" || switched.SessionID != "session-default" {
		t.Fatalf("switch entry = %+v", switched)
	}
	// Reveal-action entries never name restricted content — no target at all.
	for _, entry := range entries[3:] {
		if entry.TargetID != "" || entry.LibraryID != "" {
			t.Fatalf("reveal-path entry carries a target: %+v", entry)
		}
		if entry.SessionID != "session-default" || entry.ProfileID != "profile-default" {
			t.Fatalf("reveal-path entry actor = %+v", entry)
		}
	}
	// The PIN must never appear anywhere in the log.
	for _, entry := range entries {
		for _, field := range []string{entry.Action, entry.TargetType, entry.TargetID} {
			if strings.Contains(field, "1234") || strings.Contains(field, "9999") {
				t.Fatalf("PIN leaked into activity entry: %+v", entry)
			}
		}
	}
}

type activityLogResponse struct {
	Data struct {
		ActivityLog []ActivityEntryInfo `json:"activityLog"`
	} `json:"data"`
	Errors []struct{ Message string } `json:"errors"`
}

func decodeActivityLog(t *testing.T, rec *httptest.ResponseRecorder) activityLogResponse {
	t.Helper()
	var resp activityLogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("activityLog errors = %#v", resp.Errors)
	}
	return resp
}

func TestActivityLogQueryRevealFilterAndScopes(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	// A revealed manager session (the seed has none).
	if err := st.EnrollDevice(t.Context(),
		store.Device{ID: "device-mgr2", Name: "Manager 2", Role: store.DeviceRoleManager, SecretHash: store.HashDeviceSecret("mgr2-secret")},
		store.Session{ID: "session-mgr-revealed", DeviceID: "device-mgr2", ProfileID: "profile-default", Revealed: true},
	); err != nil {
		t.Fatal(err)
	}
	authorizeRevealedManager := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-mgr-revealed")
		req.Header.Set("X-Beamfall-Device", "device-mgr2")
		req.Header.Set("X-Beamfall-Device-Secret", "mgr2-secret")
	}

	// A restricted-library curation event and a harmless one.
	postGraph(t, handler, `{"query":"mutation TrashMedia($mediaID: String!) { trashMedia(mediaID: $mediaID) { id } }","variables":{"mediaID":"media-archive"}}`, authorizeRevealedManager)
	postGraph(t, handler, `{"query":"mutation { relock { revealed } }"}`, authorizeViewer)

	logQuery := `{"query":"query { activityLog { id action targetID libraryID profileID } }"}`

	// Revealed manager sees both entries.
	revealed := decodeActivityLog(t, postGraph(t, handler, logQuery, authorizeRevealedManager))
	if len(revealed.Data.ActivityLog) != 2 {
		t.Fatalf("revealed activityLog = %+v, want 2 entries", revealed.Data.ActivityLog)
	}
	if revealed.Data.ActivityLog[1].Action != store.ActivityMediaTrash ||
		revealed.Data.ActivityLog[1].TargetID != "media-archive" {
		t.Fatalf("revealed trash entry = %+v", revealed.Data.ActivityLog[1])
	}

	// Unrevealed manager: the restricted entry is missing with no trace —
	// the page is exactly what a log without that entry would return.
	rec := postGraph(t, handler, logQuery, authorizeManager)
	hidden := decodeActivityLog(t, rec)
	if len(hidden.Data.ActivityLog) != 1 || hidden.Data.ActivityLog[0].Action != store.ActivityRelock {
		t.Fatalf("unrevealed activityLog = %+v, want only the relock entry", hidden.Data.ActivityLog)
	}
	if strings.Contains(rec.Body.String(), "media-archive") || strings.Contains(rec.Body.String(), "library-archive") {
		t.Fatalf("unrevealed response names restricted content: %s", rec.Body.String())
	}

	// Non-manager self scope: the managed-profile session sees only its own
	// Profile's entries — here, none.
	authorizeManaged := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-managed")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}
	own := decodeActivityLog(t, postGraph(t, handler, logQuery, authorizeManaged))
	if len(own.Data.ActivityLog) != 0 {
		t.Fatalf("managed-profile activityLog = %+v, want none", own.Data.ActivityLog)
	}
	// After acting, it sees exactly its own entry.
	postGraph(t, handler, `{"query":"mutation { relock { revealed } }"}`, authorizeManaged)
	own = decodeActivityLog(t, postGraph(t, handler, logQuery, authorizeManaged))
	if len(own.Data.ActivityLog) != 1 || own.Data.ActivityLog[0].ProfileID != "profile-managed" {
		t.Fatalf("managed-profile activityLog = %+v, want its own relock", own.Data.ActivityLog)
	}
}

func TestActivityLogQueryPaging(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	for range 3 {
		postGraph(t, handler, `{"query":"mutation { relock { revealed } }"}`, authorizeViewer)
	}
	// Paging arguments come from the variables map.
	body := `{"query":"query ActivityLog($limit: Int, $offset: Int) { activityLog(limit: $limit, offset: $offset) { id } }","variables":{"limit":2,"offset":1}}`
	page := decodeActivityLog(t, postGraph(t, handler, body, authorizeManager))
	if len(page.Data.ActivityLog) != 2 {
		t.Fatalf("page = %+v, want 2 entries", page.Data.ActivityLog)
	}
	all := decodeActivityLog(t, postGraph(t, handler, `{"query":"query { activityLog { id } }"}`, authorizeManager))
	if all.Data.ActivityLog[1].ID != page.Data.ActivityLog[0].ID {
		t.Fatalf("offset page does not align: all=%+v page=%+v", all.Data.ActivityLog, page.Data.ActivityLog)
	}
}

func TestActivityLogQueryExposesScopedAPIKeyAttribution(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	if err := st.AppendActivity(context.Background(), store.ActivityEntry{
		OccurredAt: time.Unix(100, 0).UTC(),
		Action:     store.ActivityTokenCreate,
		TargetType: "token",
		TargetID:   "token-created",
		TokenID:    "token-actor",
	}); err != nil {
		t.Fatalf("AppendActivity token: %v", err)
	}
	if err := st.AppendActivity(context.Background(), store.ActivityEntry{
		OccurredAt: time.Unix(101, 0).UTC(),
		Action:     store.ActivitySessionControl,
		ProfileID:  "profile-default",
		TargetType: "session",
		TargetID:   "session-default",
		APIKeyID:   "key-actor",
	}); err != nil {
		t.Fatalf("AppendActivity key: %v", err)
	}

	got := decodeActivityLog(t, postGraph(t, handler, `{"query":"query { activityLog { action tokenID apiKeyID } }"}`, authorizeManager))
	if len(got.Data.ActivityLog) != 2 {
		t.Fatalf("activityLog = %+v, want two entries", got.Data.ActivityLog)
	}
	if got.Data.ActivityLog[0].TokenID != "" || got.Data.ActivityLog[0].APIKeyID != "key-actor" {
		t.Fatalf("scoped-key attribution = token %q key %q, want empty/key-actor", got.Data.ActivityLog[0].TokenID, got.Data.ActivityLog[0].APIKeyID)
	}
	if got.Data.ActivityLog[1].TokenID != "token-actor" || got.Data.ActivityLog[1].APIKeyID != "" {
		t.Fatalf("token attribution = token %q key %q, want token-actor/empty", got.Data.ActivityLog[1].TokenID, got.Data.ActivityLog[1].APIKeyID)
	}

	authorizeProfile := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-default")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}
	self := decodeActivityLog(t, postGraph(t, handler, `{"query":"query { activityLog { action tokenID apiKeyID profileID } }"}`, authorizeProfile))
	if len(self.Data.ActivityLog) != 1 {
		t.Fatalf("self activityLog = %+v, want one entry", self.Data.ActivityLog)
	}
	if self.Data.ActivityLog[0].TokenID != "" || self.Data.ActivityLog[0].APIKeyID != "" || self.Data.ActivityLog[0].ProfileID != "profile-default" {
		t.Fatalf("self activityLog entry = %+v, want profile row with credential ids suppressed", self.Data.ActivityLog[0])
	}
}

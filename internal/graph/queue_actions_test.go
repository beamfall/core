package graph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// These wire-level tests exercise the queueOps resolvers (graph.go queueOps:
// queueRemove / queueMove / queueClear) over the real seeded SQLite store and
// HTTP handler. They mirror TestQueueMutationsAreSessionProfileOwnedAndReveal-
// Filtered (graph_test.go) but strengthen the destructive paths: each asserts
// the EFFECT by reading the queue Source back, that the action is
// session-profile-OWNED (the managed Profile's queue is independent), and that
// it is REVEAL-FILTERED (a hidden Media is invisible to an unrevealed session
// and cannot be mutated through it).
//
// Seed facts these tests rely on (internal/store/sqlite.go developmentSeed):
//   - media-alpha   lives in library-local   (visible to every session)
//   - media-archive lives in library-archive (IsRestricted; visible only to a
//     revealed adult session)
//   - authorizeViewer/authorizeRevealed both map to profile-default and so
//     SHARE one queue; authorizeManaged maps to profile-managed (its own queue).
// Helper names are file-local and qa-prefixed to avoid collisions with the
// other _test.go files in this package.

// qaAuthorizeManaged drives the managed (profile-managed) Profile session.
func qaAuthorizeManaged(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-managed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

// qaItems posts one GraphQL body with the given authorizer and decodes the
// named field as a []SourceItem, failing on any GraphQL error.
func qaItems(t *testing.T, handler *Handler, body string, authorize func(*http.Request), field string) []SourceItem {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorize(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want 200; body=%s", field, rec.Code, rec.Body.String())
	}
	var resp struct {
		Data   map[string][]SourceItem `json:"data"`
		Errors []graphError            `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s decode: %v; body=%s", field, err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("%s errors = %#v", field, resp.Errors)
	}
	return resp.Data[field]
}

// qaErrors posts one GraphQL body and returns only the errors (used for the
// no-existence-oracle move assertions).
func qaErrors(t *testing.T, handler *Handler, body string, authorize func(*http.Request)) []graphError {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorize(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	return resp.Errors
}

// qaReadQueue reads the queue Source back for the given session.
func qaReadQueue(t *testing.T, handler *Handler, authorize func(*http.Request)) []SourceItem {
	t.Helper()
	return qaItems(t, handler,
		`{"query":"query SourceItems($kind: String!) { sourceItems(kind: $kind) { id } }","variables":{"kind":"queue"}}`,
		authorize, "sourceItems")
}

func qaIDs(items []SourceItem) []string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return ids
}

func qaEqualIDs(items []SourceItem, want ...string) bool {
	if len(items) != len(want) {
		return false
	}
	for i := range want {
		if items[i].ID != want[i] {
			return false
		}
	}
	return true
}

const (
	qaAddAlpha    = `{"query":"mutation { queueAdd(mediaID: \"media-alpha\") { id } }","variables":{"mediaID":"media-alpha"}}`
	qaAddArchive  = `{"query":"mutation { queueAdd(mediaID: \"media-archive\") { id } }","variables":{"mediaID":"media-archive"}}`
	qaRemoveAlpha = `{"query":"mutation { queueRemove(mediaID: \"media-alpha\") { id } }","variables":{"mediaID":"media-alpha"}}`
	qaClear       = `{"query":"mutation { queueClear { id } }"}`
)

// TestQueueRemoveRemovesRightItemAndIsProfileOwnedAndRevealFiltered exercises
// queueRemove at the wire level: it removes exactly the named item and leaves
// the rest in order, is idempotent on re-send, does not touch another
// Profile's queue, and cannot reach a Media the session may not reveal.
func TestQueueRemoveRemovesRightItemAndIsProfileOwnedAndRevealFiltered(t *testing.T) {
	// feature:play-queue
	handler := newTestHandler(t)

	// Build a two-item queue on the adult Profile under a revealed session
	// (the only session that can see the restricted media-archive). The queue
	// is owned by profile-default, which authorizeViewer also reads.
	if got := qaItems(t, handler, qaAddAlpha, authorizeRevealed, "queueAdd"); len(got) != 1 || got[0].ID != "media-alpha" {
		t.Fatalf("seed queueAdd alpha = %v, want [media-alpha]", qaIDs(got))
	}
	if got := qaItems(t, handler, qaAddArchive, authorizeRevealed, "queueAdd"); !qaEqualIDs(got, "media-alpha", "media-archive") {
		t.Fatalf("seed queueAdd archive = %v, want [media-alpha media-archive]", qaIDs(got))
	}

	// EFFECT: removing media-alpha drops exactly that item; media-archive
	// survives and the queue order is intact. The resolver returns the new
	// queue, and a fresh read-back agrees.
	got := qaItems(t, handler, qaRemoveAlpha, authorizeRevealed, "queueRemove")
	if !qaEqualIDs(got, "media-archive") {
		t.Fatalf("queueRemove result = %v, want [media-archive]", qaIDs(got))
	}
	if back := qaReadQueue(t, handler, authorizeRevealed); !qaEqualIDs(back, "media-archive") {
		t.Fatalf("queue after remove = %v, want [media-archive]", qaIDs(back))
	}

	// DESTRUCTIVE idempotence: removing media-alpha again is a no-op, no error,
	// and the queue is unchanged.
	if got := qaItems(t, handler, qaRemoveAlpha, authorizeRevealed, "queueRemove"); !qaEqualIDs(got, "media-archive") {
		t.Fatalf("idempotent re-remove = %v, want [media-archive]", qaIDs(got))
	}

	// PROFILE-OWNED: the managed Profile owns a separate queue. Adding then
	// removing from it never touches the adult Profile's surviving entry.
	if got := qaItems(t, handler, qaAddAlpha, qaAuthorizeManaged, "queueAdd"); !qaEqualIDs(got, "media-alpha") {
		t.Fatalf("managed queueAdd = %v, want [media-alpha]", qaIDs(got))
	}
	if got := qaItems(t, handler, qaRemoveAlpha, qaAuthorizeManaged, "queueRemove"); len(got) != 0 {
		t.Fatalf("managed queueRemove = %v, want empty", qaIDs(got))
	}
	if back := qaReadQueue(t, handler, authorizeRevealed); !qaEqualIDs(back, "media-archive") {
		t.Fatalf("adult queue after managed ops = %v, want [media-archive] untouched", qaIDs(back))
	}

	// REVEAL-FILTERED: an unrevealed viewer (same Profile) sees neither the
	// restricted media-archive nor can it remove it — hidden == missing, so
	// the remove is a silent no-op and the entry survives for reveal.
	if back := qaReadQueue(t, handler, authorizeViewer); len(back) != 0 {
		t.Fatalf("unrevealed queue = %v, want empty (media-archive filtered)", qaIDs(back))
	}
	removeArchive := `{"query":"mutation { queueRemove(mediaID: \"media-archive\") { id } }","variables":{"mediaID":"media-archive"}}`
	if got := qaItems(t, handler, removeArchive, authorizeViewer, "queueRemove"); len(got) != 0 {
		t.Fatalf("unrevealed queueRemove of hidden id = %v, want empty no-op", qaIDs(got))
	}
	if back := qaReadQueue(t, handler, authorizeRevealed); !qaEqualIDs(back, "media-archive") {
		t.Fatalf("revealed queue after unrevealed remove attempt = %v, want [media-archive] survived", qaIDs(back))
	}
}

// TestQueueMoveReordersToRequestedIndexAndIsProfileOwnedAndRevealFiltered
// exercises queueMove at the wire level: it reorders to the requested index,
// does not touch another Profile's queue, and a hidden Media cannot be moved
// through an unrevealed session (no existence oracle).
func TestQueueMoveReordersToRequestedIndexAndIsProfileOwnedAndRevealFiltered(t *testing.T) {
	// feature:play-queue
	handler := newTestHandler(t)

	// Two visible items on the adult Profile via the revealed session:
	// [media-alpha, media-archive].
	qaItems(t, handler, qaAddAlpha, authorizeRevealed, "queueAdd")
	if got := qaItems(t, handler, qaAddArchive, authorizeRevealed, "queueAdd"); !qaEqualIDs(got, "media-alpha", "media-archive") {
		t.Fatalf("seed queue = %v, want [media-alpha media-archive]", qaIDs(got))
	}

	// EFFECT: move media-archive to index 0 -> [media-archive, media-alpha].
	moveArchiveTo0 := `{"query":"mutation { queueMove(mediaID: \"media-archive\", position: 0) { id } }","variables":{"mediaID":"media-archive","position":0}}`
	got := qaItems(t, handler, moveArchiveTo0, authorizeRevealed, "queueMove")
	if !qaEqualIDs(got, "media-archive", "media-alpha") {
		t.Fatalf("queueMove to 0 result = %v, want [media-archive media-alpha]", qaIDs(got))
	}
	if back := qaReadQueue(t, handler, authorizeRevealed); !qaEqualIDs(back, "media-archive", "media-alpha") {
		t.Fatalf("queue after move = %v, want [media-archive media-alpha]", qaIDs(back))
	}

	// Move it back to index 1 -> original order, proving the index is honored.
	moveArchiveTo1 := `{"query":"mutation { queueMove(mediaID: \"media-archive\", position: 1) { id } }","variables":{"mediaID":"media-archive","position":1}}`
	if got := qaItems(t, handler, moveArchiveTo1, authorizeRevealed, "queueMove"); !qaEqualIDs(got, "media-alpha", "media-archive") {
		t.Fatalf("queueMove to 1 result = %v, want [media-alpha media-archive]", qaIDs(got))
	}

	// PROFILE-OWNED: the managed Profile's queue is independent. A move there
	// (single item to 0) does not reorder the adult Profile's queue.
	qaItems(t, handler, qaAddAlpha, qaAuthorizeManaged, "queueAdd")
	moveAlphaTo0 := `{"query":"mutation { queueMove(mediaID: \"media-alpha\", position: 0) { id } }","variables":{"mediaID":"media-alpha","position":0}}`
	if got := qaItems(t, handler, moveAlphaTo0, qaAuthorizeManaged, "queueMove"); !qaEqualIDs(got, "media-alpha") {
		t.Fatalf("managed queueMove = %v, want [media-alpha]", qaIDs(got))
	}
	if back := qaReadQueue(t, handler, authorizeRevealed); !qaEqualIDs(back, "media-alpha", "media-archive") {
		t.Fatalf("adult queue after managed move = %v, want [media-alpha media-archive] untouched", qaIDs(back))
	}

	// REVEAL-FILTERED + no existence oracle: moving the hidden media-archive
	// through an unrevealed viewer fails byte-identically to moving a Media
	// that was never queued, and the queue is untouched.
	moveHidden := `{"query":"mutation { queueMove(mediaID: \"media-archive\", position: 0) { id } }","variables":{"mediaID":"media-archive","position":0}}`
	moveAbsent := `{"query":"mutation { queueMove(mediaID: \"media-never-queued\", position: 0) { id } }","variables":{"mediaID":"media-never-queued","position":0}}`
	errHidden := qaErrors(t, handler, moveHidden, authorizeViewer)
	errAbsent := qaErrors(t, handler, moveAbsent, authorizeViewer)
	if len(errHidden) == 0 || len(errAbsent) == 0 || errHidden[0].Message != errAbsent[0].Message {
		t.Fatalf("hidden move err = %#v, absent move err = %#v - must be indistinguishable", errHidden, errAbsent)
	}
	if back := qaReadQueue(t, handler, authorizeRevealed); !qaEqualIDs(back, "media-alpha", "media-archive") {
		t.Fatalf("revealed queue after hidden move attempt = %v, want [media-alpha media-archive] survived", qaIDs(back))
	}
}

// TestQueueClearEmptiesQueueAndIsProfileOwnedAndRevealFiltered exercises
// queueClear at the wire level, strengthened beyond the canonical light check:
// it asserts the queue is actually empty via read-back, that clearing one
// Profile's queue leaves another's intact, and that an unrevealed clear leaves
// the session's hidden entries (which reappear on reveal).
func TestQueueClearEmptiesQueueAndIsProfileOwnedAndRevealFiltered(t *testing.T) {
	// feature:play-queue
	handler := newTestHandler(t)

	// Seed the adult Profile (revealed) with two items and the managed Profile
	// with one. The two queues are independent owners.
	qaItems(t, handler, qaAddAlpha, authorizeRevealed, "queueAdd")
	if got := qaItems(t, handler, qaAddArchive, authorizeRevealed, "queueAdd"); !qaEqualIDs(got, "media-alpha", "media-archive") {
		t.Fatalf("seed adult queue = %v, want [media-alpha media-archive]", qaIDs(got))
	}
	if got := qaItems(t, handler, qaAddAlpha, qaAuthorizeManaged, "queueAdd"); !qaEqualIDs(got, "media-alpha") {
		t.Fatalf("seed managed queue = %v, want [media-alpha]", qaIDs(got))
	}

	// PROFILE-OWNED: clearing the managed Profile empties only its queue; the
	// adult Profile's two-item queue is untouched.
	if got := qaItems(t, handler, qaClear, qaAuthorizeManaged, "queueClear"); len(got) != 0 {
		t.Fatalf("managed queueClear result = %v, want empty", qaIDs(got))
	}
	if back := qaReadQueue(t, handler, qaAuthorizeManaged); len(back) != 0 {
		t.Fatalf("managed queue after clear = %v, want empty", qaIDs(back))
	}
	if back := qaReadQueue(t, handler, authorizeRevealed); !qaEqualIDs(back, "media-alpha", "media-archive") {
		t.Fatalf("adult queue after managed clear = %v, want [media-alpha media-archive] untouched", qaIDs(back))
	}

	// REVEAL-FILTERED: an unrevealed viewer (same adult Profile) clears only
	// what it can see. media-alpha goes; the hidden media-archive survives and
	// reappears on reveal.
	if got := qaItems(t, handler, qaClear, authorizeViewer, "queueClear"); len(got) != 0 {
		t.Fatalf("unrevealed queueClear result = %v, want empty visible set", qaIDs(got))
	}
	if back := qaReadQueue(t, handler, authorizeViewer); len(back) != 0 {
		t.Fatalf("unrevealed queue after clear = %v, want empty", qaIDs(back))
	}
	if back := qaReadQueue(t, handler, authorizeRevealed); !qaEqualIDs(back, "media-archive") {
		t.Fatalf("revealed queue after unrevealed clear = %v, want [media-archive] survived", qaIDs(back))
	}

	// EFFECT (full clear): a revealed clear empties the adult Profile queue
	// completely, confirmed by read-back across both visibility levels.
	if got := qaItems(t, handler, qaClear, authorizeRevealed, "queueClear"); len(got) != 0 {
		t.Fatalf("revealed queueClear result = %v, want empty", qaIDs(got))
	}
	if back := qaReadQueue(t, handler, authorizeRevealed); len(back) != 0 {
		t.Fatalf("revealed queue after full clear = %v, want empty", qaIDs(back))
	}

	// DESTRUCTIVE idempotence: re-clearing an empty queue is a no-op, no error.
	if got := qaItems(t, handler, qaClear, authorizeRevealed, "queueClear"); len(got) != 0 {
		t.Fatalf("idempotent re-clear = %v, want empty", qaIDs(got))
	}
}

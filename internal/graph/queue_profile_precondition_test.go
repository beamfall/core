package graph

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// CORE-2177: Play Queue writes carry an optional expectedProfileID. Seed facts:
// session-default (authorizeViewer) is bound to profile-default, and switching
// it to profile-managed needs no PIN.

const (
	pqDefault = "profile-default"
	pqManaged = "profile-managed"
	pqAdd     = `mutation Add($mediaID: String!, $expectedProfileID: String) { queueAdd(mediaID: $mediaID, expectedProfileID: $expectedProfileID) { id } }`
	pqSwitch  = `{"query":"mutation S($profileID: String!) { switchProfile(profileID: $profileID) { profileID } }","variables":{"profileID":"profile-managed"}}`
)

// pqQueue reads a Profile's whole queue straight from the store, so a
// check never depends on which Profile the session is bound to.
func pqQueue(t *testing.T, st *store.Store, profileID store.ProfileID) []string {
	t.Helper()
	items, err := st.SourceItems(t.Context(), store.LibraryFilter{Revealed: true, AllLibraries: true}, store.SourceQuery{Kind: "queue", ProfileID: profileID})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = string(item.ID)
	}
	return ids
}

func pqSwitchViewerToManaged(t *testing.T, h *Handler) {
	t.Helper()
	if _, errs := post(t, h, pqSwitch, authorizeViewer); len(errs) != 0 {
		t.Fatalf("switchProfile errors = %#v", errs)
	}
}

func pqWantMismatch(t *testing.T, label string, errs []graphError) {
	t.Helper()
	if len(errs) != 1 || errs[0].Extensions.Code != ErrorCodeConflict || errs[0].Extensions.Reason != ErrorReasonProfileMismatch {
		t.Fatalf("%s errors = %#v, want one CONFLICT / PROFILE_MISMATCH", label, errs)
	}
}

func TestQueueWriteWithStaleExpectedProfileIsRefused(t *testing.T) {
	// feature:play-queue
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	pqSwitchViewerToManaged(t, h)

	resp := postGraphJSON(t, h, authorizeViewer, pqAdd, map[string]any{"mediaID": "media-alpha", "expectedProfileID": pqDefault})
	pqWantMismatch(t, "queueAdd", resp.Errors)
	if got := pqQueue(t, st, pqManaged); len(got) != 0 {
		t.Fatalf("new Profile's queue = %v, want untouched", got)
	}
	if got := pqQueue(t, st, pqDefault); len(got) != 0 {
		t.Fatalf("old Profile's queue = %v, want untouched", got)
	}
}

func TestEveryQueueMutationHonoursExpectedProfile(t *testing.T) {
	// feature:play-queue
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	pqSwitchViewerToManaged(t, h)
	if err := st.QueueAdd(t.Context(), pqManaged, "media-alpha", nil, store.LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"queueRemove": `mutation M($expectedProfileID: String) { queueRemove(mediaID: "media-alpha", expectedProfileID: $expectedProfileID) { id } }`,
		"queueMove":   `mutation M($expectedProfileID: String) { queueMove(mediaID: "media-alpha", position: 0, expectedProfileID: $expectedProfileID) { id } }`,
		"queueClear":  `mutation M($expectedProfileID: String) { queueClear(expectedProfileID: $expectedProfileID) { id } }`,
	}
	for field, query := range cases {
		stale := postGraphJSON(t, h, authorizeViewer, query, map[string]any{"expectedProfileID": pqDefault})
		pqWantMismatch(t, field+" stale", stale.Errors)
		if got := pqQueue(t, st, pqManaged); !slices.Equal(got, []string{"media-alpha"}) {
			t.Fatalf("%s stale changed the queue to %v", field, got)
		}
	}
	// The matching precondition applies, last clearing the queue.
	for _, field := range []string{"queueMove", "queueRemove", "queueClear"} {
		resp := postGraphJSON(t, h, authorizeViewer, cases[field], map[string]any{"expectedProfileID": pqManaged})
		if len(resp.Errors) != 0 {
			t.Fatalf("%s matching errors = %#v", field, resp.Errors)
		}
	}
	if got := pqQueue(t, st, pqManaged); len(got) != 0 {
		t.Fatalf("queue after matching remove/clear = %v, want empty", got)
	}
}

func TestStationStartHonoursExpectedProfile(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedMusicFixture(t, st)
	pqSwitchViewerToManaged(t, h)

	resp := postGraphJSON(t, h, authorizeViewer,
		`mutation Start($seedKind: String!, $seedID: String!, $expectedProfileID: String) { stationStart(seedKind: $seedKind, seedID: $seedID, expectedProfileID: $expectedProfileID) { id } }`,
		map[string]any{"seedKind": "artist", "seedID": string(fx.artistID), "expectedProfileID": pqDefault})
	pqWantMismatch(t, "stationStart", resp.Errors)
	if got := pqQueue(t, st, pqManaged); len(got) != 0 {
		t.Fatalf("stationStart stale wrote %v", got)
	}
}

func TestQueueWriteWithMatchingExpectedProfileApplies(t *testing.T) {
	// feature:play-queue
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)

	resp := postGraphJSON(t, h, authorizeViewer, pqAdd, map[string]any{"mediaID": "media-alpha", "expectedProfileID": pqDefault})
	if len(resp.Errors) != 0 {
		t.Fatalf("queueAdd errors = %#v", resp.Errors)
	}
	if got := pqQueue(t, st, pqDefault); !slices.Equal(got, []string{"media-alpha"}) {
		t.Fatalf("queue = %v, want [media-alpha]", got)
	}
}

func TestQueueWriteWithoutExpectedProfileKeepsOldBehaviour(t *testing.T) {
	// feature:play-queue
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	pqSwitchViewerToManaged(t, h)

	resp := postGraphJSON(t, h, authorizeViewer, pqAdd, map[string]any{"mediaID": "media-alpha"})
	if len(resp.Errors) != 0 {
		t.Fatalf("queueAdd errors = %#v", resp.Errors)
	}
	if got := pqQueue(t, st, pqManaged); !slices.Equal(got, []string{"media-alpha"}) {
		t.Fatalf("session's current Profile queue = %v, want [media-alpha]", got)
	}
}

// A request whose scope was resolved before the switch but whose write
// applies after it is refused: the check reads the binding at apply time, not
// the scope resolved when the request arrived.
func TestQueueWriteResolvedBeforeSwitchIsRefusedAfterIt(t *testing.T) {
	// feature:play-queue
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	scope := auth.Scope{SessionID: "session-default", ProfileID: pqDefault}
	if err := st.SetSessionProfile(t.Context(), "session-default", pqManaged); err != nil {
		t.Fatal(err)
	}
	req := request{Variables: map[string]any{"expectedProfileID": pqDefault}}
	ran := false
	err := h.withExpectedProfile(t.Context(), scope, req, func() error { ran = true; return nil })
	if !errors.Is(err, errProfileMismatch) || ran {
		t.Fatalf("withExpectedProfile err = %v, ran = %v; want mismatch and no write", err, ran)
	}
}

// pqPost is a goroutine-safe GraphQL post: it reports failures instead of
// stopping the test from a goroutine.
func pqPost(h *Handler, body []byte, authorize func(*http.Request)) ([]graphError, error) {
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorize(req)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return nil, errors.New(rec.Body.String())
	}
	var resp struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return nil, err
	}
	return resp.Errors, nil
}

func TestConcurrentSwitchNeverMovesPreconditionedWriteToNewProfile(t *testing.T) {
	// feature:play-queue
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	add, err := json.Marshal(map[string]any{"query": pqAdd, "variables": map[string]any{"mediaID": "media-alpha", "expectedProfileID": pqDefault}})
	if err != nil {
		t.Fatal(err)
	}
	all := store.LibraryFilter{Revealed: true, AllLibraries: true}
	applied, refused := 0, 0
	for i := range 40 {
		if err := st.SetSessionProfile(t.Context(), "session-default", pqDefault); err != nil {
			t.Fatal(err)
		}
		for _, p := range []store.ProfileID{pqDefault, pqManaged} {
			if err := st.QueueClear(t.Context(), p, all); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		var addErrs, switchErrs []graphError
		var addFail, switchFail error
		wg.Go(func() { addErrs, addFail = pqPost(h, add, authorizeViewer) })
		wg.Go(func() { switchErrs, switchFail = pqPost(h, []byte(pqSwitch), authorizeViewer) })
		wg.Wait()
		if addFail != nil || switchFail != nil || len(switchErrs) != 0 {
			t.Fatalf("round %d: add %v, switch %v %#v", i, addFail, switchFail, switchErrs)
		}
		if got := pqQueue(t, st, pqManaged); len(got) != 0 {
			t.Fatalf("round %d: write for %s landed in %s's queue: %v", i, pqDefault, pqManaged, got)
		}
		inDefault := slices.Equal(pqQueue(t, st, pqDefault), []string{"media-alpha"})
		switch {
		case len(addErrs) == 0 && inDefault:
			applied++
		case len(addErrs) == 1 && !inDefault:
			pqWantMismatch(t, "concurrent queueAdd", addErrs)
			refused++
		default:
			t.Fatalf("round %d: add errors %#v with item in %s = %v", i, addErrs, pqDefault, inDefault)
		}
	}
	t.Logf("applied %d, refused %d", applied, refused)
}

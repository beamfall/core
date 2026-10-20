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

// TestSetAccessScheduleEmitsInvalidation covers the PANEL-17 emission wiring: a
// manager changing a managed Profile's access schedule emits a content-free
// client.invalidate outbox event addressed to that Profile, so its still-connected
// OS surfaces purge.
func TestSetAccessScheduleEmitsInvalidation(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	body := []byte(`{"query":"mutation { setAccessSchedule { profileID } }","variables":{"profileID":"profile-managed","windows":[{"weekday":1,"startMinute":540,"endMinute":1020}]}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}

	events, err := st.ReadEventOutbox(context.Background(), 0, 1000)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	for _, e := range events {
		if e.Kind == store.InvalidationKind && e.AggregateType == store.OutboxAggregateInvalidation && e.AggregateID == "profile-managed" {
			return
		}
	}
	t.Fatal("setAccessSchedule emitted no client.invalidate event for the Profile")
}

// TestSetAccessScheduleRequiresAdminSession covers the Admin-Session gate on the
// access-schedules config resolver (ADR-0004 §4 / ADR-0007 §4 Profiles & access
// policies — a Console operation): a session without an open Admin Session can
// neither read nor set a managed Profile's allowed time windows.
// feature:access-schedules
func TestSetAccessScheduleRequiresAdminSession(t *testing.T) {
	handler := newTestHandler(t)
	body := []byte(`{"query":"mutation { setAccessSchedule }","variables":{"profileID":"profile-managed"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Message != "admin session required" {
		t.Fatalf("errors = %#v, want admin session required", resp.Errors)
	}
}

// TestAccessScheduleConfigResolverRoundTrips covers the manager-gated read/set
// config resolver: a manager writes a managed Profile's allowed time windows,
// the mutation echoes the normalized server-truth, and a subsequent read
// returns the same set. feature:access-schedules
func TestAccessScheduleConfigResolverRoundTrips(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	// Set two windows (one duplicated to prove dedup) for the managed Profile.
	setBody := []byte(`{"query":"mutation { setAccessSchedule { profileID windows { weekday startMinute endMinute } } }","variables":{"profileID":"profile-managed","windows":[{"weekday":1,"startMinute":540,"endMinute":1020},{"weekday":1,"startMinute":540,"endMinute":1020},{"weekday":6,"startMinute":0,"endMinute":1440}]}}`)
	setReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(setBody))
	authorizeManager(setReq)
	setRec := httptest.NewRecorder()

	handler.ServeHTTP(setRec, setReq)

	if setRec.Code != http.StatusOK {
		t.Fatalf("set status = %d, want %d", setRec.Code, http.StatusOK)
	}
	var setResp struct {
		Data   map[string]AccessScheduleInfo `json:"data"`
		Errors []graphError                  `json:"errors"`
	}
	if err := json.Unmarshal(setRec.Body.Bytes(), &setResp); err != nil {
		t.Fatalf("decode set response: %v", err)
	}
	if len(setResp.Errors) != 0 {
		t.Fatalf("set errors = %#v", setResp.Errors)
	}
	got := setResp.Data["setAccessSchedule"]
	if got.ProfileID != "profile-managed" {
		t.Fatalf("ProfileID = %q, want profile-managed", got.ProfileID)
	}
	// Dedup + stable order: [weekday 1] then [weekday 6].
	if len(got.Windows) != 2 {
		t.Fatalf("windows = %#v, want 2 (deduped)", got.Windows)
	}
	if got.Windows[0] != (AccessWindowInfo{Weekday: 1, StartMinute: 540, EndMinute: 1020}) {
		t.Fatalf("window[0] = %#v", got.Windows[0])
	}
	if got.Windows[1] != (AccessWindowInfo{Weekday: 6, StartMinute: 0, EndMinute: 1440}) {
		t.Fatalf("window[1] = %#v", got.Windows[1])
	}

	// Read back through the query resolver and assert the same set.
	readBody := []byte(`{"query":"query { accessSchedule { profileID windows { weekday startMinute endMinute } } }","variables":{"profileID":"profile-managed"}}`)
	readReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(readBody))
	authorizeManager(readReq)
	readRec := httptest.NewRecorder()

	handler.ServeHTTP(readRec, readReq)

	var readResp struct {
		Data map[string]AccessScheduleInfo `json:"data"`
	}
	if err := json.Unmarshal(readRec.Body.Bytes(), &readResp); err != nil {
		t.Fatalf("decode read response: %v", err)
	}
	read := readResp.Data["accessSchedule"]
	if len(read.Windows) != 2 || read.Windows[0].EndMinute != 1020 || read.Windows[1].Weekday != 6 {
		t.Fatalf("read windows = %#v, want the stored set", read.Windows)
	}
}

// TestSetAccessScheduleRejectsAdultProfile covers the config resolver refusing
// a non-managed target: schedules apply to managed Profiles only, so setting
// one on an adult Profile is a stable client error (no enforcement path exists
// for adult Profiles). feature:access-schedules
func TestSetAccessScheduleRejectsAdultProfile(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	body := []byte(`{"query":"mutation { setAccessSchedule { profileID } }","variables":{"profileID":"profile-default","windows":[{"weekday":0,"startMinute":0,"endMinute":60}]}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Message != "access schedules apply to managed profiles only" {
		t.Fatalf("errors = %#v, want managed-profiles-only rejection", resp.Errors)
	}
}

// TestSetAccessScheduleRejectsMalformedWindow covers the parse-time guard: a
// window missing a numeric field is a stable client error, never silently
// dropped — dropping it would quietly change the enforced schedule.
// feature:access-schedules
func TestSetAccessScheduleRejectsMalformedWindow(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	body := []byte(`{"query":"mutation { setAccessSchedule { profileID } }","variables":{"profileID":"profile-managed","windows":[{"weekday":1,"startMinute":540}]}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Message != "each access window requires a numeric endMinute" {
		t.Fatalf("errors = %#v, want malformed-window rejection", resp.Errors)
	}
}

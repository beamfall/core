package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestSetLibraryRestrictionEmitsInvalidation covers the PANEL-17 broadcast wiring:
// toggling a Library's restriction is a visibility-ceiling change that affects
// EVERY Profile granted the Library, so the mutation fans a content-free
// client.invalidate out one-per-granted-Profile. The seeded
// library-local is granted to both profile-default and profile-managed, so a
// single toggle must address exactly those two Profiles — proving the fan-out
// reaches Profiles other than the acting one (profile-default).
func TestSetLibraryRestrictionEmitsInvalidation(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	// session-revealed is an adult, revealed session; stamping the admin window
	// gives the Admin+Revealed scope the mutation requires (device role is part
	// of neither gate).
	openAdminSession(t, st, "session-revealed")

	body := []byte(`{"query":"mutation { setLibraryRestricted { id } }","variables":{"libraryID":"library-local","restricted":true}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeRevealed(req)
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
	var targets []string
	for _, e := range events {
		if e.Kind != store.InvalidationKind || e.AggregateType != store.OutboxAggregateInvalidation {
			continue
		}
		var payload store.InvalidationPayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("decode invalidation payload: %v", err)
		}
		if payload.Reason != store.InvalidationReasonLibraryRestrictionChanged {
			t.Fatalf("reason = %q, want %q", payload.Reason, store.InvalidationReasonLibraryRestrictionChanged)
		}
		if string(payload.ProfileID) != e.AggregateID {
			t.Fatalf("payload Profile %q != aggregate id %q", payload.ProfileID, e.AggregateID)
		}
		targets = append(targets, e.AggregateID)
	}
	sort.Strings(targets)
	want := []string{"profile-default", "profile-managed"}
	if len(targets) != len(want) || targets[0] != want[0] || targets[1] != want[1] {
		t.Fatalf("invalidation targets = %v, want exactly %v (both granted library-local)", targets, want)
	}
}

// TestSetLibraryRestrictionRequiresReveal covers the reveal gate: toggling the
// restriction flag moves the reveal boundary, so an admin session that is NOT
// revealed is refused. session-manager is admin-eligible but unrevealed.
func TestSetLibraryRestrictionRequiresReveal(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	body := []byte(`{"query":"mutation { setLibraryRestricted { id } }","variables":{"libraryID":"library-local","restricted":true}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Message != "reveal required to change library restriction" {
		t.Fatalf("errors = %#v, want reveal-required rejection", resp.Errors)
	}

	// No write, no invalidation on the rejected path.
	events, err := st.ReadEventOutbox(context.Background(), 0, 1000)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	for _, e := range events {
		if e.Kind == store.InvalidationKind {
			t.Fatalf("unexpected invalidation emitted on rejected toggle: %#v", e)
		}
	}
}

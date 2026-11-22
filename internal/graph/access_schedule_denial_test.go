package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// closeManagedSchedule gives profile-managed an access schedule whose only
// window is a whole day three days from today, so the real-time auth clock is
// outside it for the test's duration.
func closeManagedSchedule(t *testing.T, st *store.Store) {
	t.Helper()
	weekday := (int(time.Now().Weekday()) + 3) % 7
	if err := st.SetProfileAccessWindows(context.Background(), "profile-managed", []store.ProfileAccessWindow{
		{Weekday: weekday, StartMinute: 0, EndMinute: 1440},
	}); err != nil {
		t.Fatal(err)
	}
}

func postGraphStatus(t *testing.T, handler http.Handler, body string, authorize func(*http.Request)) (int, response) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorize(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, resp
}

// TestAccessScheduleEndedIsDistinctGraphDenial is CORE-2950: a managed
// Profile's session outside its access schedule is refused with HTTP 403,
// FORBIDDEN and reason ACCESS_SCHEDULE_ENDED, not the 401 UNAUTHENTICATED
// "invalid session" every other credential failure keeps.
// feature:access-schedules
func TestAccessScheduleEndedIsDistinctGraphDenial(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	const query = `{"query":"query { profileSessions { sessionID } }"}`

	closeManagedSchedule(t, st)
	status, resp := postGraphStatus(t, handler, query, authorizeManaged)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
	if len(resp.Errors) != 1 {
		t.Fatalf("errors = %#v, want one", resp.Errors)
	}
	got := resp.Errors[0]
	if got.Extensions.Code != ErrorCodeForbidden || got.Extensions.Reason != ErrorReasonAccessScheduleEnded || got.Message != "access schedule ended" {
		t.Fatalf("error = %#v, want FORBIDDEN / ACCESS_SCHEDULE_ENDED / access schedule ended", got)
	}

	// An adult Profile's session is never time-gated.
	if status, resp := postGraphStatus(t, handler, query, authorizeViewer); status != http.StatusOK || len(resp.Errors) != 0 {
		t.Fatalf("adult session: status = %d errors = %#v, want 200 and none", status, resp.Errors)
	}

	// Any other credential failure is unchanged.
	status, resp = postGraphStatus(t, handler, query, func(req *http.Request) {
		authorizeManaged(req)
		req.Header.Set("X-Beamfall-Device-Secret", "wrong-secret")
	})
	if status != http.StatusUnauthorized || len(resp.Errors) != 1 ||
		resp.Errors[0].Extensions.Code != ErrorCodeUnauthenticated || resp.Errors[0].Extensions.Reason != "" ||
		resp.Errors[0].Message != "invalid session" {
		t.Fatalf("wrong device secret: status = %d errors = %#v, want 401 UNAUTHENTICATED invalid session", status, resp.Errors)
	}

	// Clearing the schedule restores access.
	if err := st.SetProfileAccessWindows(context.Background(), "profile-managed", nil); err != nil {
		t.Fatal(err)
	}
	if status, resp := postGraphStatus(t, handler, query, authorizeManaged); status != http.StatusOK || len(resp.Errors) != 0 {
		t.Fatalf("schedule cleared: status = %d errors = %#v, want 200 and none", status, resp.Errors)
	}
}

// TestScopeRefusalMapsOnlyTheScheduleRefusal pins the mapping both scope
// resolutions (the request gate and the after-mutation re-resolution) use, and
// that the GraphQL reason is the value REST transports carry in their header.
func TestScopeRefusalMapsOnlyTheScheduleRefusal(t *testing.T) {
	if ErrorReasonAccessScheduleEnded != auth.DenialReasonAccessScheduleEnded {
		t.Fatalf("graph reason %q != auth denial reason %q", ErrorReasonAccessScheduleEnded, auth.DenialReasonAccessScheduleEnded)
	}
	for _, err := range []error{auth.ErrAccessOutsideSchedule, fmt.Errorf("resolve: %w", auth.ErrAccessOutsideSchedule)} {
		status, got := scopeRefusal(err)
		if status != http.StatusForbidden || got.Extensions.Code != ErrorCodeForbidden || got.Extensions.Reason != ErrorReasonAccessScheduleEnded {
			t.Fatalf("scopeRefusal(%v) = %d %#v, want 403 FORBIDDEN ACCESS_SCHEDULE_ENDED", err, status, got)
		}
	}
	for _, err := range []error{errors.New("session expired"), store.ErrProfileDisabled} {
		status, got := scopeRefusal(err)
		if status != http.StatusUnauthorized || got.Extensions.Code != ErrorCodeUnauthenticated || got.Extensions.Reason != "" || got.Message != "invalid session" {
			t.Fatalf("scopeRefusal(%v) = %d %#v, want 401 UNAUTHENTICATED invalid session", err, status, got)
		}
	}
}

// TestScopedKeyKeepsGenericRefusalOutsideSchedule: a Profile-bound scoped key
// whose Profile is outside its schedule keeps the generic 401, because this
// surface refuses scoped keys whatever the schedule; the reason is for session
// credentials only.
func TestScopedKeyKeepsGenericRefusalOutsideSchedule(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	_, key, err := st.CreateScopedAPIKey(t.Context(), "schedule-probe", []store.APIScope{store.ScopeReadCatalog}, "profile-managed", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	closeManagedSchedule(t, st)
	status, resp := postGraphStatus(t, handler, `{"query":"query { profiles { id } }"}`, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+key)
	})
	if status != http.StatusUnauthorized || len(resp.Errors) != 1 ||
		resp.Errors[0].Extensions.Code != ErrorCodeUnauthenticated || resp.Errors[0].Extensions.Reason != "" {
		t.Fatalf("scoped key outside schedule: status = %d errors = %#v, want 401 UNAUTHENTICATED with no reason", status, resp.Errors)
	}
}

// TestAccessScheduleEndedAfterMutationKeepsForbiddenStatus: when an earlier
// field of a multi-field mutation moves the session onto a Profile outside its
// schedule, the re-resolution refusal answers 403 with the reason, as the
// request gate does.
func TestAccessScheduleEndedAfterMutationKeepsForbiddenStatus(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	closeManagedSchedule(t, st)
	body := `{"query":"mutation M($profileID: String!){ switchProfile(profileID:$profileID){ profileID } relock { revealed } }","variables":{"profileID":"profile-managed"}}`
	status, resp := postGraphStatus(t, handler, body, authorizeRevealed)
	if status != http.StatusForbidden || len(resp.Errors) != 1 ||
		resp.Errors[0].Extensions.Code != ErrorCodeForbidden || resp.Errors[0].Extensions.Reason != ErrorReasonAccessScheduleEnded {
		t.Fatalf("switch then relock outside schedule: status = %d errors = %#v, want 403 FORBIDDEN ACCESS_SCHEDULE_ENDED", status, resp.Errors)
	}
}

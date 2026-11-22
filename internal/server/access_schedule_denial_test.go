package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// TestPlaybackRoutesReportAccessScheduleEnded is CORE-2950: playback-info and
// preview-info refuse a managed Profile outside its access schedule with 403,
// the denial-reason header and a reason body, while any other credential
// failure keeps the 401 "invalid session".
// feature:access-schedules
func TestPlaybackRoutesReportAccessScheduleEnded(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	// The test clock is Thursday 1970-01-01; the only window is a Sunday.
	if err := st.SetProfileAccessWindows(context.Background(), "profile-managed", []store.ProfileAccessWindow{
		{Weekday: 0, StartMinute: 0, EndMinute: 1440},
	}); err != nil {
		t.Fatal(err)
	}
	authorizeManaged := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-managed")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}
	for _, path := range []string{"/api/playback-info", "/api/preview-info"} {
		req := newJSONRequest(http.MethodPost, path, strings.NewReader(`{"mediaID":"media-alpha"}`))
		authorizeManaged(req)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403: %s", path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get(auth.DenialReasonHeader); got != auth.DenialReasonAccessScheduleEnded {
			t.Fatalf("%s %s = %q, want %q", path, auth.DenialReasonHeader, got, auth.DenialReasonAccessScheduleEnded)
		}
		if body := rec.Body.String(); !strings.Contains(body, `"reason":"ACCESS_SCHEDULE_ENDED"`) {
			t.Fatalf("%s body = %q, want the reason", path, body)
		}

		req = newJSONRequest(http.MethodPost, path, strings.NewReader(`{"mediaID":"media-alpha"}`))
		authorizeManaged(req)
		req.Header.Set("X-Beamfall-Device-Secret", "wrong-secret")
		rec = httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get(auth.DenialReasonHeader) != "" {
			t.Fatalf("%s wrong secret: status = %d header = %q, want 401 and no reason", path, rec.Code, rec.Header().Get(auth.DenialReasonHeader))
		}
	}
}

// TestPlaybackRoutesKeepGenericRefusalForScopedKeyOutsideSchedule is
// CORE-2950: the schedule reason belongs to session credentials. A
// Profile-bound scoped key whose managed Profile is outside its schedule gets
// the generic 401 with no reason on playback-info and preview-info.
// feature:access-schedules
func TestPlaybackRoutesKeepGenericRefusalForScopedKeyOutsideSchedule(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	if err := st.SetProfileAccessWindows(context.Background(), "profile-managed", []store.ProfileAccessWindow{
		{Weekday: 0, StartMinute: 0, EndMinute: 1440},
	}); err != nil {
		t.Fatal(err)
	}
	key := mintKey(t, st, "managed-playback", "profile-managed", store.ScopeReadCatalog)
	probe := httptest.NewRequest(http.MethodPost, "/api/playback-info", nil)
	probe.Header.Set("Authorization", "Bearer "+key)
	if _, err := srv.auth.ScopeFromRequest(context.Background(), probe); !auth.AccessScheduleEnded(err) {
		t.Fatalf("scoped key scope err = %v, want the schedule refusal", err)
	}
	for _, path := range []string{"/api/playback-info", "/api/preview-info"} {
		rec := v1Post(t, srv, path, key, `{"mediaID":"media-alpha"}`)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get(auth.DenialReasonHeader) != "" || strings.Contains(rec.Body.String(), auth.DenialReasonAccessScheduleEnded) {
			t.Fatalf("%s scoped key: status = %d header = %q body = %q, want 401 and no reason", path, rec.Code, rec.Header().Get(auth.DenialReasonHeader), rec.Body.String())
		}
	}
}

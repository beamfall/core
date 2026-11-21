package mediahttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback/sessiontest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// TestRefuseAccessScheduleEndedOnlyAnswersTheScheduleRefusal is CORE-2950:
// every media transport answers a Stream Token re-verify that fails only on
// the access schedule with 403 and the denial-reason header, and leaves every
// other grant failure to its existing status.
// feature:access-schedules
func TestRefuseAccessScheduleEndedOnlyAnswersTheScheduleRefusal(t *testing.T) {
	for _, err := range []error{auth.ErrAccessOutsideSchedule, fmt.Errorf("grant: %w", auth.ErrAccessOutsideSchedule)} {
		rec := httptest.NewRecorder()
		if !refuseAccessScheduleEnded(rec, err) {
			t.Fatalf("refuseAccessScheduleEnded(%v) = false, want true", err)
		}
		if rec.Code != http.StatusForbidden || rec.Header().Get(auth.DenialReasonHeader) != auth.DenialReasonAccessScheduleEnded {
			t.Fatalf("refusal for %v: status = %d header = %q, want 403 %s", err, rec.Code, rec.Header().Get(auth.DenialReasonHeader), auth.DenialReasonAccessScheduleEnded)
		}
	}
	rec := httptest.NewRecorder()
	if refuseAccessScheduleEnded(rec, errors.New("stream token expired")) {
		t.Fatal("refuseAccessScheduleEnded(other) = true, want false")
	}
	if rec.Body.Len() != 0 || rec.Header().Get(auth.DenialReasonHeader) != "" {
		t.Fatalf("other error wrote a response: header = %q body = %q", rec.Header().Get(auth.DenialReasonHeader), rec.Body.String())
	}
}

// TestStaleProfileTokenGetsGenericRefusalOutsideSchedule: a Stream Token
// signed for one Profile must not learn another Profile's schedule state after
// the session switches. The old token gets the generic 401 with no reason; a
// token signed for the session's current Profile still gets the reason.
func TestStaleProfileTokenGetsGenericRefusalOutsideSchedule(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	src := filepath.Join(t.TempDir(), "movie.bin")
	if err := os.WriteFile(src, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(ctx, "media-alpha", src); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(st)
	scope, err := authService.Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	issue := func(profileID store.ProfileID) string {
		token, err := tokens.Issue(ctx, streamtoken.Grant{
			Target:    streamtoken.TargetMediaDirect,
			MediaID:   "media-alpha",
			DeviceID:  scope.DeviceID,
			SessionID: scope.SessionID,
			ProfileID: profileID,
			ExpiresAt: now.Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	staleToken := issue(scope.ProfileID)

	weekday := (int(time.Now().Weekday()) + 3) % 7
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", []store.ProfileAccessWindow{{Weekday: weekday, StartMinute: 0, EndMinute: 1440}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionProfile(ctx, scope.SessionID, "profile-managed"); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), sessiontest.New(t, nil, t.TempDir(), nil))
	direct := func(token string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.Direct(rec, httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil))
		return rec
	}

	if rec := direct(staleToken); rec.Code != http.StatusUnauthorized || rec.Header().Get(auth.DenialReasonHeader) != "" {
		t.Fatalf("stale Profile token: status = %d header = %q, want 401 and no reason", rec.Code, rec.Header().Get(auth.DenialReasonHeader))
	}
	if rec := direct(issue("profile-managed")); rec.Code != http.StatusForbidden || rec.Header().Get(auth.DenialReasonHeader) != auth.DenialReasonAccessScheduleEnded {
		t.Fatalf("current Profile token: status = %d header = %q, want 403 %s", rec.Code, rec.Header().Get(auth.DenialReasonHeader), auth.DenialReasonAccessScheduleEnded)
	}
}

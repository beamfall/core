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
	tokens.SetStreamGenerationResolver(st)
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
	managedToken := issue("profile-managed")
	if rec := direct(managedToken); rec.Code != http.StatusForbidden || rec.Header().Get(auth.DenialReasonHeader) != auth.DenialReasonAccessScheduleEnded {
		t.Fatalf("current Profile token: status = %d header = %q, want 403 %s", rec.Code, rec.Header().Get(auth.DenialReasonHeader), auth.DenialReasonAccessScheduleEnded)
	}
	for _, profileID := range []store.ProfileID{scope.ProfileID, "profile-managed"} {
		if err := st.SetSessionProfile(ctx, scope.SessionID, profileID); err != nil {
			t.Fatal(err)
		}
	}
	checkCancelled := func() string {
		t.Helper()
		rec := direct(managedToken)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get(auth.DenialReasonHeader) != "" {
			t.Fatalf("cancelled token after switching back: status = %d header = %q, want 401 and no reason", rec.Code, rec.Header().Get(auth.DenialReasonHeader))
		}
		return rec.Body.String()
	}
	outside := checkCancelled()
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", nil); err != nil {
		t.Fatal(err)
	}
	if inside := checkCancelled(); inside != outside {
		t.Fatalf("cancelled token response differs by schedule: inside = %q, outside = %q", inside, outside)
	}
}

type switchableDecisionResolver struct{ decisionID string }

func (r *switchableDecisionResolver) Resolve(_ context.Context, _ string, _ streamtoken.DecisionWant) (string, bool, error) {
	return r.decisionID, true, nil
}

// TestInvalidatedCompatDecisionTokenGetsGenericRefusalOutsideSchedule: a
// matching Profile and device are not enough for the schedule reason. Once
// the token's compatibility decision is invalidated it gets the route's
// ordinary 401, exactly as it would inside the schedule.
func TestInvalidatedCompatDecisionTokenGetsGenericRefusalOutsideSchedule(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	src := filepath.Join(t.TempDir(), "movie.bin")
	if err := os.WriteFile(src, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(ctx, "media-alpha", src); err != nil {
		t.Fatal(err)
	}
	session, err := st.Session(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	weekday := (int(time.Now().Weekday()) + 3) % 7
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", []store.ProfileAccessWindow{{Weekday: weekday, StartMinute: 0, EndMinute: 1440}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionProfile(ctx, session.ID, "profile-managed"); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	resolver := &switchableDecisionResolver{decisionID: "decision-1"}
	tokens.SetCompatDecisionResolver(resolver)
	token, err := tokens.Issue(ctx, streamtoken.Grant{
		Target:    streamtoken.TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  session.DeviceID,
		SessionID: session.ID,
		ProfileID: "profile-managed",
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(st, auth.NewService(st), tokens, artwork.NewService(st, nil, t.TempDir()), sessiontest.New(t, nil, t.TempDir(), nil))
	direct := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.Direct(rec, httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil))
		return rec
	}

	if rec := direct(); rec.Code != http.StatusForbidden || rec.Header().Get(auth.DenialReasonHeader) != auth.DenialReasonAccessScheduleEnded {
		t.Fatalf("valid decision: status = %d header = %q, want 403 %s", rec.Code, rec.Header().Get(auth.DenialReasonHeader), auth.DenialReasonAccessScheduleEnded)
	}
	resolver.decisionID = "decision-2"
	if rec := direct(); rec.Code != http.StatusUnauthorized || rec.Header().Get(auth.DenialReasonHeader) != "" {
		t.Fatalf("invalidated decision: status = %d header = %q, want 401 and no reason", rec.Code, rec.Header().Get(auth.DenialReasonHeader))
	}
}

// TestRestrictedTokenGetsGenericRefusalOutsideSchedule: a restricted Stream
// Token fails Reveal on its own once its Profile is managed, so a matching
// session, Profile and device do not earn it the schedule reason. It gets the
// route's ordinary 401 with no reason header.
func TestRestrictedTokenGetsGenericRefusalOutsideSchedule(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	src := filepath.Join(t.TempDir(), "movie.bin")
	if err := os.WriteFile(src, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(ctx, "media-alpha", src); err != nil {
		t.Fatal(err)
	}
	session, err := st.Session(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	weekday := (int(time.Now().Weekday()) + 3) % 7
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", []store.ProfileAccessWindow{{Weekday: weekday, StartMinute: 0, EndMinute: 1440}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionProfile(ctx, session.ID, "profile-managed"); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	token, err := tokens.Issue(ctx, streamtoken.Grant{
		Target:     streamtoken.TargetMediaDirect,
		MediaID:    "media-alpha",
		DeviceID:   session.DeviceID,
		SessionID:  session.ID,
		ProfileID:  "profile-managed",
		Restricted: true,
		ExpiresAt:  now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(st, auth.NewService(st), tokens, artwork.NewService(st, nil, t.TempDir()), sessiontest.New(t, nil, t.TempDir(), nil))
	rec := httptest.NewRecorder()
	handler.Direct(rec, httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil))
	if rec.Code != http.StatusUnauthorized || rec.Header().Get(auth.DenialReasonHeader) != "" {
		t.Fatalf("restricted token: status = %d header = %q, want 401 and no reason", rec.Code, rec.Header().Get(auth.DenialReasonHeader))
	}
}

// TestStaleSubtitleTokenStatusHidesScheduleState: a subtitle token signed for
// another Profile gets the same generic refusal whether the session's current
// Profile is inside or outside its access schedule, so the status code never
// tells it that Profile's schedule state.
func TestStaleSubtitleTokenStatusHidesScheduleState(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	authService := auth.NewService(st)
	scope, err := authService.Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	staleToken, err := tokens.Issue(ctx, streamtoken.Grant{
		Target:    streamtoken.TargetMediaSubtitle,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionProfile(ctx, scope.SessionID, "profile-managed"); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), sessiontest.New(t, nil, t.TempDir(), nil))
	subtitle := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.Subtitles(rec, httptest.NewRequest(http.MethodGet, "/api/media/subtitle/"+staleToken+"/2.vtt", nil))
		return rec
	}

	inside := subtitle()
	weekday := (int(time.Now().Weekday()) + 3) % 7
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", []store.ProfileAccessWindow{{Weekday: weekday, StartMinute: 0, EndMinute: 1440}}); err != nil {
		t.Fatal(err)
	}
	outside := subtitle()
	for name, rec := range map[string]*httptest.ResponseRecorder{"inside schedule": inside, "outside schedule": outside} {
		if rec.Code != http.StatusUnauthorized || rec.Header().Get(auth.DenialReasonHeader) != "" {
			t.Fatalf("stale subtitle token %s: status = %d header = %q, want 401 and no reason", name, rec.Code, rec.Header().Get(auth.DenialReasonHeader))
		}
	}
}

// TestStaleSubtitleTokenStatusIgnoresScheduleClosingAfterVerify: a stale
// subtitle token refused on its bindings inside the schedule stays 401 even
// when the schedule closes before the status is chosen. The status comes from
// the verification's own result, never from a second session lookup.
func TestStaleSubtitleTokenStatusIgnoresScheduleClosingAfterVerify(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	authService := auth.NewService(st)
	scope, err := authService.Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	staleToken, err := tokens.Issue(ctx, streamtoken.Grant{
		Target:    streamtoken.TargetMediaSubtitle,
		MediaID:   "media-alpha",
		DeviceID:  scope.DeviceID,
		SessionID: scope.SessionID,
		ProfileID: scope.ProfileID,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionProfile(ctx, scope.SessionID, "profile-managed"); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), sessiontest.New(t, nil, t.TempDir(), nil))

	_, _, verifyErr := handler.verifyMediaGrant(ctx, staleToken, streamtoken.TargetMediaSubtitle)
	if verifyErr == nil || auth.AccessScheduleEnded(verifyErr) {
		t.Fatalf("stale token inside schedule: verify err = %v, want a binding refusal", verifyErr)
	}
	weekday := (int(time.Now().Weekday()) + 3) % 7
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", []store.ProfileAccessWindow{{Weekday: weekday, StartMinute: 0, EndMinute: 1440}}); err != nil {
		t.Fatal(err)
	}
	if _, err := authService.Scope(ctx, scope.SessionID); !auth.AccessScheduleEnded(err) {
		t.Fatalf("schedule did not close: Scope err = %v", err)
	}
	if status := endedSubtitleStatus(verifyErr); status != http.StatusUnauthorized {
		t.Fatalf("status after schedule closed = %d, want 401", status)
	}
}

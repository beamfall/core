package graph

// Wire tests for the ADR-0039 in-app notification inbox (#32): the
// `notifications(unreadOnly)` query and the `markNotificationRead(id)` mutation
// over the real GraphQL HTTP handler. These prove the resolver gates the read to
// the calling session's own Profile, reveal-filters the result, and routes the
// mutation through the reveal-and-recipient-gated store path.

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

// notificationsResp is a file-local decode envelope.
type notificationsResp struct {
	Data struct {
		Notifications        []NotificationInfo     `json:"notifications"`
		MarkNotificationRead NotificationReadResult `json:"markNotificationRead"`
	} `json:"data"`
	Errors []struct{ Message string } `json:"errors"`
}

func notifPost(t *testing.T, handler *Handler, body string, authorize func(*http.Request)) notificationsResp {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorize(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp notificationsResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	return resp
}

// TestNotificationsQueryRevealFiltered proves the GraphQL inbox read is
// reveal-filtered: an unrevealed session never sees a notification that
// references restricted content (library-archive), but a revealed session does;
// a content-free notice is visible to both.
func TestNotificationsQueryRevealFiltered(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()

	if err := st.CreateNotification(ctx, store.Notification{
		ID:                 "gn-open",
		RecipientProfileID: "profile-default",
		Kind:               store.NotificationSystem,
		Title:              "System notice",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateNotification(ctx, store.Notification{
		ID:                 "gn-restricted",
		RecipientProfileID: "profile-default",
		Kind:               store.NotificationExtensionRequest,
		Title:              "Restricted event",
		LibraryID:          "library-archive",
	}); err != nil {
		t.Fatal(err)
	}

	query := `{"query":"query { notifications { id read kind } }"}`

	// Unrevealed viewer (session-default → profile-default, not revealed): the
	// restricted notification must be absent; the open notice present.
	resp := notifPost(t, handler, query, authorizeViewer)
	if len(resp.Errors) != 0 {
		t.Fatalf("unrevealed notifications errors: %#v", resp.Errors)
	}
	ids := notifIDs(resp.Data.Notifications)
	if contains(ids, "gn-restricted") {
		t.Fatalf("unrevealed session saw restricted notification; ids=%v", ids)
	}
	if !contains(ids, "gn-open") {
		t.Fatalf("unrevealed session missing content-free notice; ids=%v", ids)
	}

	// Revealed viewer (session-revealed → profile-default, revealed): both show.
	resp = notifPost(t, handler, query, authorizeRevealed)
	if len(resp.Errors) != 0 {
		t.Fatalf("revealed notifications errors: %#v", resp.Errors)
	}
	ids = notifIDs(resp.Data.Notifications)
	if !contains(ids, "gn-restricted") || !contains(ids, "gn-open") {
		t.Fatalf("revealed session should see both; ids=%v", ids)
	}
}

// TestNotificationsUnreadOnly proves the unreadOnly argument filters to unread.
func TestNotificationsUnreadOnly(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()
	for _, id := range []string{"gn-a", "gn-b"} {
		if err := st.CreateNotification(ctx, store.Notification{
			ID:                 id,
			RecipientProfileID: "profile-default",
			Kind:               store.NotificationSystem,
			Title:              "n " + id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Mark one read through the mutation.
	resp := notifPost(t, handler,
		`{"query":"mutation M($id: ID!){ markNotificationRead(id:$id){ id found } }","variables":{"id":"gn-a"}}`,
		authorizeViewer)
	if len(resp.Errors) != 0 || !resp.Data.MarkNotificationRead.Found {
		t.Fatalf("markNotificationRead gn-a = %#v errors=%#v, want found", resp.Data.MarkNotificationRead, resp.Errors)
	}

	// unreadOnly: only gn-b remains. The hand-rolled GraphQL handler reads
	// arguments from `variables` (inline literals are not parsed), so unreadOnly
	// rides as a variable.
	resp = notifPost(t, handler,
		`{"query":"query N($unreadOnly: Boolean){ notifications(unreadOnly: $unreadOnly) { id read } }","variables":{"unreadOnly":true}}`,
		authorizeViewer)
	ids := notifIDs(resp.Data.Notifications)
	if len(ids) != 1 || ids[0] != "gn-b" {
		t.Fatalf("unreadOnly returned %v, want only gn-b", ids)
	}
}

// TestMarkNotificationReadRecipientGated proves a session can never mark another
// Profile's notification read: profile-managed (session-managed) marking a
// notification addressed to profile-default reports found=false and does not
// mutate it.
func TestMarkNotificationReadRecipientGated(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()
	if err := st.CreateNotification(ctx, store.Notification{
		ID:                 "gn-default-only",
		RecipientProfileID: "profile-default",
		Kind:               store.NotificationSystem,
		Title:              "Private to default",
	}); err != nil {
		t.Fatal(err)
	}

	authorizeManaged := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-managed")
		req.Header.Set("X-Beamfall-Device", "device-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	}

	// profile-managed cannot mark profile-default's notification — found=false.
	resp := notifPost(t, handler,
		`{"query":"mutation M($id: ID!){ markNotificationRead(id:$id){ id found } }","variables":{"id":"gn-default-only"}}`,
		authorizeManaged)
	if len(resp.Errors) != 0 {
		t.Fatalf("managed mark errors: %#v", resp.Errors)
	}
	if resp.Data.MarkNotificationRead.Found {
		t.Fatal("profile-managed marked profile-default's notification read (recipient gate bypass)")
	}

	// profile-managed also can't see it in their own inbox.
	resp = notifPost(t, handler, `{"query":"query { notifications { id } }"}`, authorizeManaged)
	if contains(notifIDs(resp.Data.Notifications), "gn-default-only") {
		t.Fatal("profile-managed read profile-default's notification")
	}

	// Owner still sees it unread (it was not mutated), then can mark it.
	resp = notifPost(t, handler,
		`{"query":"query N($unreadOnly: Boolean){ notifications(unreadOnly: $unreadOnly) { id read } }","variables":{"unreadOnly":true}}`,
		authorizeViewer)
	if !contains(notifIDs(resp.Data.Notifications), "gn-default-only") {
		t.Fatal("owner's notification was marked read by a non-recipient")
	}
	resp = notifPost(t, handler,
		`{"query":"mutation M($id: ID!){ markNotificationRead(id:$id){ id found } }","variables":{"id":"gn-default-only"}}`,
		authorizeViewer)
	if !resp.Data.MarkNotificationRead.Found {
		t.Fatal("owner could not mark their own notification read")
	}
}

func notifIDs(items []NotificationInfo) []string {
	ids := make([]string, len(items))
	for i, n := range items {
		ids[i] = n.ID
	}
	return ids
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// fakePushRegistrar records the (deviceID, profileID, platform, token) the
// resolver passed, so the wire test can assert the device + Profile came from
// the session scope and not the request.
type fakePushRegistrar struct {
	enabled                              bool
	deviceID, profileID, platform, token string
	called                               bool
}

func (f *fakePushRegistrar) Register(_ context.Context, deviceID, profileID, platform, token string) error {
	f.called = true
	f.deviceID, f.profileID, f.platform, f.token = deviceID, profileID, platform, token
	return nil
}

func (f *fakePushRegistrar) Enabled() bool { return f.enabled }

// pushResp is a file-local decode envelope for the push mutation.
type pushResp struct {
	Data struct {
		RegisterDevicePushToken PushRegistrationResult `json:"registerDevicePushToken"`
	} `json:"data"`
	Errors []struct{ Message string } `json:"errors"`
}

func pushPost(t *testing.T, handler *Handler, body string, authorize func(*http.Request)) pushResp {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorize(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp pushResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	return resp
}

// TestRegisterDevicePushTokenUsesSessionScope proves PUSH-1 acceptance (a): the
// registration mutation persists the token through the registrar with the
// device + Profile taken from the authenticated SESSION, never the request — a
// session can only register a token for its own enrolled device.
func TestRegisterDevicePushTokenUsesSessionScope(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	reg := &fakePushRegistrar{enabled: true}
	handler.SetPushRegistrar(reg)

	resp := pushPost(t, handler,
		`{"query":"mutation R($platform: String!, $token: String!){ registerDevicePushToken(platform:$platform, token:$token){ registered enabled } }","variables":{"platform":"apns","token":"opaque-apns-token"}}`,
		authorizeViewer)
	if len(resp.Errors) != 0 {
		t.Fatalf("register errors: %#v", resp.Errors)
	}
	if !resp.Data.RegisterDevicePushToken.Registered || !resp.Data.RegisterDevicePushToken.Enabled {
		t.Fatalf("expected registered+enabled, got %+v", resp.Data.RegisterDevicePushToken)
	}
	if !reg.called {
		t.Fatal("registrar was not called")
	}
	// session-default → device-tv / profile-default in the seed.
	if reg.deviceID != "device-tv" || reg.profileID != "profile-default" {
		t.Fatalf("device/profile not from session scope: device=%q profile=%q", reg.deviceID, reg.profileID)
	}
	if reg.platform != "apns" || reg.token != "opaque-apns-token" {
		t.Fatalf("platform/token mismatch: %q %q", reg.platform, reg.token)
	}
}

// TestRegisterDevicePushTokenDisabled proves acceptance (d): when the registrar
// is gated off (or unwired) the mutation reports enabled=false WITHOUT an error,
// so the client cleanly falls back to the polled inbox.
func TestRegisterDevicePushTokenDisabled(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	reg := &fakePushRegistrar{enabled: false}
	handler.SetPushRegistrar(reg)

	resp := pushPost(t, handler,
		`{"query":"mutation R($platform: String!, $token: String!){ registerDevicePushToken(platform:$platform, token:$token){ registered enabled } }","variables":{"platform":"apns","token":"tok"}}`,
		authorizeViewer)
	if len(resp.Errors) != 0 {
		t.Fatalf("disabled register errors: %#v", resp.Errors)
	}
	if resp.Data.RegisterDevicePushToken.Registered || resp.Data.RegisterDevicePushToken.Enabled {
		t.Fatalf("disabled registrar should report not-registered/not-enabled, got %+v", resp.Data.RegisterDevicePushToken)
	}
	if reg.called {
		t.Fatal("disabled registrar must not be called")
	}
}

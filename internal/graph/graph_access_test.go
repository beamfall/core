package graph

// Graph-layer auth tests for the ADR-0113 access flow.
//
// These tests cover the cross-profile rejection paths that were previously
// untested at the graph dispatch layer:
//   - requestAccess: only a managed Profile may raise a request — an adult is
//     rejected (adults use the Reveal path, not the access-request gate).
//   - resolveAccessRequest: only an adult Profile may resolve — a managed
//     Profile (even the original requester) is refused.
//   - accessGrantsForProfile: a managed Profile querying ANOTHER managed
//     Profile's grants is told "not found" — the surface is not a
//     cross-Profile existence oracle.
//   - revokeAccessGrant: only an adult Profile may revoke.
//
// Tests are non-vacuous: each assertion targets the exact error message
// produced by the corresponding profile-kind guard in graph_access.go, so
// the test would fail if the guard were removed or weakened.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/push"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// accessPost submits a JSON body and returns the decoded graph response.
// Named distinctly to avoid collision with graphPost in graph_bulkedit_test.go.
func accessPost(t *testing.T, h *Handler, body string, auth func(*http.Request)) response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	auth(req)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// firstAccessError returns the first graph error message, or "" if none.
func firstAccessError(resp response) string {
	if len(resp.Errors) == 0 {
		return ""
	}
	return resp.Errors[0].Message
}

// TestRequestAccessRejectsAdultProfile verifies that an adult Profile
// attempting requestAccess is rejected with the "only a managed profile"
// error. Adults have the Reveal path and never need an access request.
// Without this guard an adult could create spurious
// pending requests that appear in the adult inbox and pollute audit logs.
// The test fails if the managed-kind check in requestAccess is removed.
func TestRequestAccessRejectsAdultProfile(t *testing.T) {
	handler := newTestHandler(t)
	resp := accessPost(t, handler,
		`{"query":"mutation { requestAccess { id } }","variables":{"mediaID":"media-alpha"}}`,
		authorizeViewer, // session-default → profile-default (adult)
	)
	got := firstAccessError(resp)
	if got != "only a managed profile can request access to a locked item" {
		t.Fatalf("error = %q, want adult-rejection message", got)
	}
}

// TestResolveAccessRequestAdultSucceeds verifies the positive resolve path:
// an adult Profile can grant a pending access request.
// Without a working positive resolve path, the family-supervision flow
// cannot approve managed-Profile access even if the guard is correct.
func TestResolveAccessRequestAdultSucceeds(t *testing.T) {
	st := storetest.Open(t)
	// Create a pending request as the managed Profile.
	created, err := st.CreateAccessRequest(t.Context(), store.AccessRequestInput{
		ProfileID: "profile-managed",
		MediaID:   "media-alpha",
	})
	if err != nil {
		t.Fatalf("CreateAccessRequest: %v", err)
	}
	handler := newTestHandlerWithStore(st)
	body, _ := json.Marshal(map[string]any{
		"query":     `mutation { resolveAccessRequest { id status verdict } }`,
		"variables": map[string]any{"id": created.ID, "verdict": "granted"},
	})
	resp := accessPost(t, handler, string(body),
		authorizeViewer, // session-default → profile-default (adult) — valid resolver
	)
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v, want none for valid adult resolveAccessRequest", resp.Errors)
	}
}

// TestResolveAccessRequestRejectsManagedProfile verifies that a managed
// Profile attempting to resolve an access request (approve or deny) is
// refused with the "only an adult profile" error (ADR-0113 §2.2 —
// "first-responder-wins, any adult Profile is a valid responder; a managed
// Profile may not self-approve"). Without this check the family-supervision
// invariant breaks: a child could self-grant access.
func TestResolveAccessRequestRejectsManagedProfile(t *testing.T) {
	st := storetest.Open(t)
	// First create a pending access request from the managed Profile.
	created, err := st.CreateAccessRequest(t.Context(), store.AccessRequestInput{
		ProfileID: "profile-managed",
		MediaID:   "media-alpha",
	})
	if err != nil {
		t.Fatalf("CreateAccessRequest: %v", err)
	}
	handler := newTestHandlerWithStore(st)
	body, _ := json.Marshal(map[string]any{
		"query":     `mutation { resolveAccessRequest { id status verdict } }`,
		"variables": map[string]any{"id": created.ID, "verdict": "granted"},
	})
	resp := accessPost(t, handler, string(body),
		authorizeManaged, // session-managed → profile-managed (managed)
	)
	got := firstAccessError(resp)
	if got != "only an adult profile can resolve an access request" {
		t.Fatalf("error = %q, want adult-only-resolve message", got)
	}
}

// TestAccessGrantsForProfileRejectsCrossProfileManagedCaller verifies that a
// managed Profile cannot enumerate another managed Profile's access grants
// (ADR-0113 §2.3 cross-profile oracle prevention). The response is
// "access grants not found" — the surface deliberately makes cross-Profile
// reads look like a missing resource, not an authorization error, to avoid
// confirming whether another Profile has grants at all.
// The test fails if the caller-identity check in accessGrantsForProfile is removed.
func TestAccessGrantsForProfileRejectsCrossProfileManagedCaller(t *testing.T) {
	st := storetest.Open(t)

	// Create a second managed profile that the first managed caller will attempt to probe.
	secondManaged, err := st.CreateProfile(t.Context(), "Other Managed", store.ProfileManaged, store.LibraryFilter{AllLibraries: false})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	handler := newTestHandlerWithStore(st)
	body, _ := json.Marshal(map[string]any{
		"query":     `query { accessGrantsForProfile { profileID mediaID } }`,
		"variables": map[string]any{"profileID": string(secondManaged.ID)},
	})
	resp := accessPost(t, handler, string(body),
		authorizeManaged, // profile-managed trying to read secondManaged's grants
	)
	got := firstAccessError(resp)
	if got != "access grants not found" {
		t.Fatalf("error = %q, want cross-profile-oracle-prevention message", got)
	}
}

// TestAccessGrantsForProfileAllowsManagedCallerOwnProfile verifies the
// positive path: a managed Profile CAN read its own access grants (the
// "which items do I have access to?" surface). Without this a managed user
// can never see their own parental grants.
func TestAccessGrantsForProfileAllowsManagedCallerOwnProfile(t *testing.T) {
	handler := newTestHandler(t)
	body, _ := json.Marshal(map[string]any{
		"query":     `query { accessGrantsForProfile { profileID mediaID } }`,
		"variables": map[string]any{"profileID": "profile-managed"},
	})
	resp := accessPost(t, handler, string(body),
		authorizeManaged, // profile-managed reading its own grants → allowed
	)
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v, want no errors for own-profile grant read", resp.Errors)
	}
}

// TestRevokeAccessGrantRejectsManagedProfile verifies that a managed Profile
// cannot revoke an access grant. Only adults may revoke.
// Removing the adult-kind guard in revokeAccessGrant would let a managed
// Profile revoke its own grants, bypassing family-supervision control.
func TestRevokeAccessGrantRejectsManagedProfile(t *testing.T) {
	handler := newTestHandler(t)
	body, _ := json.Marshal(map[string]any{
		"query":     `mutation { revokeAccessGrant { profileID mediaID } }`,
		"variables": map[string]any{"profileID": "profile-managed", "mediaID": "media-alpha"},
	})
	resp := accessPost(t, handler, string(body),
		authorizeManaged, // session-managed → profile-managed (managed)
	)
	got := firstAccessError(resp)
	if got != "only an adult profile can revoke an access grant" {
		t.Fatalf("error = %q, want adult-only-revoke message", got)
	}
}

// TestRequestAccessManagedProfileSucceeds verifies the happy path: a managed
// Profile can raise an access request for a media item. This is the
// "Ask a parent" entry-point. Without a working positive path,
// the managed Profile has no route to access locked content.
func TestRequestAccessManagedProfileSucceeds(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	req := httptest.NewRequest(http.MethodPost, "/api/graphql",
		bytes.NewReader([]byte(`{"query":"mutation { requestAccess { id profileID mediaID status } }","variables":{"mediaID":"media-alpha"}}`)))
	authorizeManaged(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want %d", rec.Code, http.StatusOK)
	}
	var out struct {
		Data   map[string]AccessRequestInfo `json:"data"`
		Errors []graphError                 `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("errors = %#v, want none for valid managed requestAccess", out.Errors)
	}
	info := out.Data["requestAccess"]
	if info.ProfileID != "profile-managed" {
		t.Fatalf("ProfileID = %q, want profile-managed", info.ProfileID)
	}
	if info.Status != "pending" {
		t.Fatalf("Status = %q, want pending", info.Status)
	}
}

func TestAccessRequestStorePusherMinimalPayload(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.UpsertDevicePushToken(ctx, store.DevicePushToken{
		DeviceID: "device-mac", ProfileID: "profile-default", Platform: "apns", Token: "adult-token",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDevicePushToken(ctx, store.DevicePushToken{
		DeviceID: "device-tv", ProfileID: "profile-managed", Platform: "fcm", Token: "managed-token",
	}); err != nil {
		t.Fatal(err)
	}

	apns := &recordingExtBackend{}
	fcm := &recordingExtBackend{}
	dispatcher := push.NewDispatcher(map[push.PushPlatform]push.Backend{
		push.PlatformAPNs: apns,
		push.PlatformFCM:  fcm,
	})
	pusher := NewAccessRequestStorePusher(st, dispatcher)

	if err := pusher.Push(ctx, []PushNotificationTarget{{ProfileID: "profile-default", NotificationID: "note-access"}}); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(apns.payloads) != 1 {
		t.Fatalf("adult APNs got %d payloads, want 1", len(apns.payloads))
	}
	if got := apns.payloads[0]; got.Type != "access_request" || got.EventID != "note-access" {
		t.Fatalf("payload = %+v, want type=access_request eventId=note-access", got)
	}
	if len(fcm.payloads) != 0 {
		t.Fatalf("managed device was pushed %d times; target map must stay per-recipient", len(fcm.payloads))
	}
}

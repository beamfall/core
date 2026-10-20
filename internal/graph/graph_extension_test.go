package graph

// Wire + adapter tests for the PUSH-2 parental extension-request flow (ADR-0039
// §2.1–2.4): the requestExtension / respondExtension / extensionRequest /
// setExtensionPreauthorization mutations over the real GraphQL handler, the
// adult-device push fan-out adapter, and the authorization rules (managed
// requests, adult responds, no managed self-approve, household supervisor model).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/push"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// extResp decodes every extension mutation/query this file exercises.
type extResp struct {
	Data struct {
		RequestExtension             ExtensionRequestInfo               `json:"requestExtension"`
		RespondExtension             ExtensionRequestInfo               `json:"respondExtension"`
		ExtensionRequest             ExtensionRequestInfo               `json:"extensionRequest"`
		SetExtensionPreauthorization SetExtensionPreauthorizationResult `json:"setExtensionPreauthorization"`
	} `json:"data"`
	Errors []struct{ Message string } `json:"errors"`
}

func extPost(t *testing.T, handler *Handler, body string, authorize func(*http.Request)) extResp {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorize(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp extResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	return resp
}

// authorizeManaged routes as session-managed → profile-managed (managed kind).
func authorizeManagedExt(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-managed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

// fakeExtensionPusher records Push targets so a wire test can assert the
// resolver fired the adult-device fan-out with the right opaque notification id.
type fakeExtensionPusher struct{ pushed []PushNotificationTarget }

func (f *fakeExtensionPusher) Push(_ context.Context, targets []PushNotificationTarget) error {
	f.pushed = append(f.pushed, targets...)
	return nil
}

// TestRequestExtensionPushesToAdults is acceptance (a): a managed-Profile request
// raises a pending ExtensionRequest, drops a notification into every adult
// Profile's inbox, and invokes the push fan-out with the request id.
func TestRequestExtensionPushesToAdults(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	pusher := &fakeExtensionPusher{}
	handler.SetExtensionPusher(pusher)

	resp := extPost(t, handler,
		`{"query":"mutation R($triggerSource: String){ requestExtension(triggerSource:$triggerSource){ id status triggerSource } }","variables":{"triggerSource":"schedule"}}`,
		authorizeManagedExt)
	if len(resp.Errors) != 0 {
		t.Fatalf("requestExtension errors: %#v", resp.Errors)
	}
	got := resp.Data.RequestExtension
	if got.Status != "pending" || got.ID == "" || got.TriggerSource != "schedule" {
		t.Fatalf("request = %+v, want pending/schedule with id", got)
	}
	// The push fan-out fired with the adult notification id, not request content.
	if len(pusher.pushed) != 1 {
		t.Fatalf("pusher.pushed = %v, want one notification target", pusher.pushed)
	}
	wantNotificationID := "extreq-profile-default-" + got.ID
	if pusher.pushed[0].ProfileID != "profile-default" || pusher.pushed[0].NotificationID != wantNotificationID {
		t.Fatalf("pusher target = %+v, want profile-default/%s", pusher.pushed[0], wantNotificationID)
	}
	// The adult Profile (profile-default) got an inbox notification referencing
	// the request (the durable record alongside the push).
	notes, err := st.ListNotifications(context.Background(), "profile-default", store.LibraryFilter{AllLibraries: true}, false, 50)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, n := range notes {
		noteID, noteNonce := parseExtensionApprovalToken(n.Ref)
		if n.Kind == store.NotificationExtensionRequest && noteID == got.ID && noteNonce != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("adult inbox missing extension notification for %s; have %d notes", got.ID, len(notes))
	}
}

// TestRequestExtensionAdultRejected proves an adult Profile cannot request an
// extension — it has no schedule gate to extend (and no self-approval path).
func TestRequestExtensionAdultRejected(t *testing.T) {
	handler := newTestHandler(t)
	resp := extPost(t, handler,
		`{"query":"mutation { requestExtension { id status } }"}`,
		authorizeViewer) // session-default → profile-default (adult)
	if len(resp.Errors) == 0 {
		t.Fatal("adult requestExtension was allowed; want a client error")
	}
}

// TestRespondExtensionManagedSelfApproveRejected is acceptance (e): a managed
// Profile cannot respond to (approve/deny) an extension request — only an adult
// may. This blocks the managed requester from self-approving.
func TestRespondExtensionManagedSelfApproveRejected(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()

	req, err := st.CreateExtensionRequest(ctx, store.ExtensionRequestInput{
		ProfileID: "profile-managed", RequestingSessionID: "session-managed", TriggerSource: "schedule",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The managed Profile tries to approve its OWN request — refused.
	resp := extPost(t, handler,
		`{"query":"mutation A($id: ID!, $approve: Boolean){ respondExtension(id:$id, approve:$approve){ id status } }","variables":{"id":"`+req.ID+`","approve":true}}`,
		authorizeManagedExt)
	if len(resp.Errors) == 0 {
		t.Fatal("managed self-approve was allowed; want a client error")
	}
	// The request is still pending — no self-approval slipped through.
	got, err := st.ExtensionRequestByID(ctx, req.ID, req.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.ExtensionPending {
		t.Fatalf("request status = %q after rejected self-approve, want pending", got.Status)
	}
}

// TestRespondExtensionAdultApproves proves an adult can approve and the grant is
// recorded (the household model: any adult is an authorized responder).
func TestRespondExtensionAdultApproves(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()
	req, err := st.CreateExtensionRequest(ctx, store.ExtensionRequestInput{
		ProfileID: "profile-managed", RequestingSessionID: "session-managed", RemainingMinutes: 18,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Adult approves with the finish-item default (uses the captured 18 minutes).
	resp := extPost(t, handler,
		`{"query":"mutation A($id: ID!, $approve: Boolean, $grant: String){ respondExtension(id:$id, approve:$approve, grant:$grant){ id status grantMinutes responder } }","variables":{"id":"`+extensionApprovalToken(req)+`","approve":true,"grant":"finish-item"}}`,
		authorizeViewer) // profile-default (adult)
	if len(resp.Errors) != 0 {
		t.Fatalf("adult approve errors: %#v", resp.Errors)
	}
	got := resp.Data.RespondExtension
	if got.Status != "approved" || got.GrantMinutes != 18 || got.Responder != "profile-default" {
		t.Fatalf("approve result = %+v, want approved/18/profile-default", got)
	}
}

func TestRespondExtensionRejectsForgedAndReplayedToken(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()
	req, err := st.CreateExtensionRequest(ctx, store.ExtensionRequestInput{
		ProfileID: "profile-managed", RequestingSessionID: "session-managed", RemainingMinutes: 18,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := extPost(t, handler,
		`{"query":"mutation A($id: ID!, $approve: Boolean){ respondExtension(id:$id, approve:$approve){ id status } }","variables":{"id":"`+req.ID+`:wrong","approve":true}}`,
		authorizeViewer)
	if len(resp.Errors) == 0 {
		t.Fatal("forged approval token was accepted")
	}
	got, err := st.ExtensionRequestByID(ctx, req.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.ExtensionPending {
		t.Fatalf("forged token changed status to %q, want pending", got.Status)
	}
	resp = extPost(t, handler,
		`{"query":"mutation A($id: ID!, $approve: Boolean){ respondExtension(id:$id, approve:$approve){ id status } }","variables":{"id":"`+extensionApprovalToken(req)+`","approve":true}}`,
		authorizeViewer)
	if len(resp.Errors) != 0 || resp.Data.RespondExtension.Status != "approved" {
		t.Fatalf("valid approval = %+v errs=%#v, want approved", resp.Data.RespondExtension, resp.Errors)
	}
	resp = extPost(t, handler,
		`{"query":"mutation A($id: ID!, $approve: Boolean){ respondExtension(id:$id, approve:$approve){ id status } }","variables":{"id":"`+extensionApprovalToken(req)+`","approve":true}}`,
		authorizeViewer)
	if len(resp.Errors) == 0 {
		t.Fatal("replayed approval token was accepted")
	}
}

func TestRespondExtensionRejectsRevokedDeviceScope(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()
	req, err := st.CreateExtensionRequest(ctx, store.ExtensionRequestInput{
		ProfileID: "profile-managed", RequestingSessionID: "session-managed", RemainingMinutes: 18,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeDevice(ctx, "device-tv"); err != nil {
		t.Fatal(err)
	}
	_, err = handler.respondExtension(ctx, auth.Scope{
		SessionID: "session-default",
		DeviceID:  "device-tv",
		ProfileID: "profile-default",
		Profile:   store.ProfileAdult,
	}, request{Variables: map[string]any{
		"id":      extensionApprovalToken(req),
		"approve": true,
	}})
	if err == nil {
		t.Fatal("revoked device scope approved extension")
	}
	got, err := st.ExtensionRequestByID(ctx, req.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.ExtensionPending {
		t.Fatalf("revoked device changed status to %q, want pending", got.Status)
	}
}

// TestPreauthorizationAutoApproves is acceptance (f): with pre-authorization on,
// the FIRST request in a session is auto-approved (no adult response, no push),
// and a SECOND request in the same session falls back to pending (adult needed).
func TestPreauthorizationAutoApproves(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	pusher := &fakeExtensionPusher{}
	handler.SetExtensionPusher(pusher)
	ctx := context.Background()

	if err := st.SetExtensionPreauthorization(ctx, "profile-managed", true, time.Time{}); err != nil {
		t.Fatal(err)
	}

	// First request: auto-approved for finish-item, no push fan-out.
	resp := extPost(t, handler,
		`{"query":"mutation R($remainingMinutes: Int){ requestExtension(remainingMinutes:$remainingMinutes){ id status grantMinutes } }","variables":{"remainingMinutes":20}}`,
		authorizeManagedExt)
	if len(resp.Errors) != 0 {
		t.Fatalf("first preauth request errors: %#v", resp.Errors)
	}
	first := resp.Data.RequestExtension
	if first.Status != "approved" || first.GrantMinutes != 20 {
		t.Fatalf("first request = %+v, want approved/20 (auto)", first)
	}
	if len(pusher.pushed) != 0 {
		t.Fatalf("auto-approved request still pushed to adults: %v", pusher.pushed)
	}

	// Second request in the SAME session: pre-auth is one-per-session, so this
	// one is pending and is pushed for an adult to answer.
	resp = extPost(t, handler,
		`{"query":"mutation R($remainingMinutes: Int){ requestExtension(remainingMinutes:$remainingMinutes){ id status } }","variables":{"remainingMinutes":20}}`,
		authorizeManagedExt)
	if len(resp.Errors) != 0 {
		t.Fatalf("second preauth request errors: %#v", resp.Errors)
	}
	if resp.Data.RequestExtension.Status != "pending" {
		t.Fatalf("second request status = %q, want pending (pre-auth is once-per-session)", resp.Data.RequestExtension.Status)
	}
	if len(pusher.pushed) != 1 {
		t.Fatalf("second (non-auto) request pushed %d times, want 1", len(pusher.pushed))
	}
}

// recordingExtBackend records the payloads a dispatcher backend received so the
// adapter test can assert the wire payload is the minimal {type, eventId}.
type recordingExtBackend struct{ payloads []push.Payload }

func (b *recordingExtBackend) Send(_ context.Context, _ string, p push.Payload) error {
	b.payloads = append(b.payloads, p)
	return nil
}

// TestExtensionStorePusherMinimalPayload is the second half of acceptance (a):
// the composition-root fan-out adapter dispatches ONLY {type:"extension_request",
// eventId:notificationID} to every adult Profile's devices — no content (invariant
// #5), and never to a managed Profile's devices.
func TestExtensionStorePusherMinimalPayload(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()

	// An adult device token (profile-default) and a managed device token
	// (profile-managed). The fan-out must hit only the adult.
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
	pusher := NewExtensionStorePusher(st, dispatcher)

	if err := pusher.Push(ctx, []PushNotificationTarget{{ProfileID: "profile-default", NotificationID: "note-adult"}}); err != nil {
		t.Fatalf("Push: %v", err)
	}
	// The adult's APNs device got exactly the minimal payload.
	if len(apns.payloads) != 1 {
		t.Fatalf("adult APNs got %d payloads, want 1", len(apns.payloads))
	}
	p := apns.payloads[0]
	if p.Type != "extension_request" || p.EventID != "note-adult" {
		t.Fatalf("payload = %+v, want type=extension_request eventId=note-adult", p)
	}
	// The minimal payload marshals to ONLY those two fields (invariant #5).
	raw, _ := json.Marshal(p)
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	if len(fields) != 2 {
		t.Fatalf("payload wire fields = %v, want exactly {type, eventId}", fields)
	}
	// The managed Profile's FCM device was NOT pushed (supervisors are adults).
	if len(fcm.payloads) != 0 {
		t.Fatalf("managed device was pushed %d times; the fan-out must target only adults", len(fcm.payloads))
	}
}

// TestExtensionStorePusherDormantNoop proves a dormant dispatcher (no operator
// credentials configured) makes the fan-out a clean no-op — the request, inbox,
// and SSE outcome carry the flow without push.
func TestExtensionStorePusherDormantNoop(t *testing.T) {
	st := storetest.Open(t)
	pusher := NewExtensionStorePusher(st, push.NewDispatcher(nil))
	if err := pusher.Push(context.Background(), []PushNotificationTarget{{ProfileID: "profile-default", NotificationID: "note-1"}}); err != nil {
		t.Fatalf("dormant Push should be a no-op, got %v", err)
	}
}

// TestSetExtensionPreauthorizationRequiresAdminSession proves the §2.4 toggle is
// admin-gated and managed-only: a manager session WITHOUT an open Admin Session
// is refused, and an open Admin Session can set it for a managed Profile but not
// an adult one.
func TestSetExtensionPreauthorizationRequiresAdminSession(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	// No admin session: refused.
	resp := extPost(t, handler,
		`{"query":"mutation S($profileID: ID!, $enabled: Boolean){ setExtensionPreauthorization(profileID:$profileID, enabled:$enabled){ profileID enabled } }","variables":{"profileID":"profile-managed","enabled":true}}`,
		authorizeManager)
	if len(resp.Errors) == 0 || resp.Errors[0].Message != "admin session required" {
		t.Fatalf("non-admin setExtensionPreauthorization = %#v, want admin session required", resp.Errors)
	}

	openManagerAdminSession(t, st)
	// Managed target: allowed.
	resp = extPost(t, handler,
		`{"query":"mutation S($profileID: ID!, $enabled: Boolean){ setExtensionPreauthorization(profileID:$profileID, enabled:$enabled){ profileID enabled } }","variables":{"profileID":"profile-managed","enabled":true}}`,
		authorizeManager)
	if len(resp.Errors) != 0 || !resp.Data.SetExtensionPreauthorization.Enabled {
		t.Fatalf("admin setExtensionPreauthorization(managed) = %#v errs=%#v, want enabled", resp.Data.SetExtensionPreauthorization, resp.Errors)
	}
	// Adult target: refused (no schedule to auto-extend).
	resp = extPost(t, handler,
		`{"query":"mutation S($profileID: ID!, $enabled: Boolean){ setExtensionPreauthorization(profileID:$profileID, enabled:$enabled){ profileID enabled } }","variables":{"profileID":"profile-default","enabled":true}}`,
		authorizeManager)
	if len(resp.Errors) == 0 {
		t.Fatal("setExtensionPreauthorization on an adult Profile was allowed; want a client error")
	}
}

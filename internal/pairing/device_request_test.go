package pairing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const testDevicePin = "sha256-device-request-pin"

// deviceRequestHarness is a pairing service with a movable clock and
// sequenced codes, secrets and ids.
type deviceRequestHarness struct {
	t       *testing.T
	st      *store.Store
	service *Service
	now     time.Time
	seq     int
}

func newDeviceRequestHarness(t *testing.T) *deviceRequestHarness {
	t.Helper()
	h := &deviceRequestHarness{t: t, st: storetest.Open(t), now: time.Unix(0, 0).UTC()}
	h.service = NewService(h.st, func() time.Time { return h.now })
	h.service.generateCode = func() (string, error) { h.seq++; return fmt.Sprintf("CODE%04d", h.seq), nil }
	h.service.generateSecret = func() (string, error) { h.seq++; return fmt.Sprintf("secret-%04d", h.seq), nil }
	h.service.generateID = func(prefix string) (string, error) { h.seq++; return fmt.Sprintf("%s-%04d", prefix, h.seq), nil }
	h.service.SetSPKIProvider(fakeSPKI{pin: testDevicePin})
	return h
}

func (h *deviceRequestHarness) admin() auth.Scope {
	h.t.Helper()
	return managerAdminScope(h.t, h.st, h.now)
}

func (h *deviceRequestHarness) request(source string) DeviceRequestResponse {
	h.t.Helper()
	resp, err := h.service.RequestDevicePairing(context.Background(), DeviceRequest{DeviceName: "Living Room TV", RemoteAddr: source, IsLAN: true})
	if err != nil {
		h.t.Fatalf("RequestDevicePairing: %v", err)
	}
	if resp.Code == "" || resp.RequestSecret == "" || resp.ExpiresAt == "" || resp.PollIntervalSeconds <= 0 {
		h.t.Fatalf("request response = %+v", resp)
	}
	return resp
}

func (h *deviceRequestHarness) poll(secret, source string) (DevicePollResponse, error) {
	return h.service.PollDevicePairing(context.Background(), DevicePollRequest{RequestSecret: secret, RemoteAddr: source, IsLAN: true})
}

func (h *deviceRequestHarness) approve(scope auth.Scope, code string) (DeviceRequestDecisionResponse, error) {
	return h.service.ApproveDeviceRequest(context.Background(), scope, DeviceRequestDecision{Code: code})
}

func (h *deviceRequestHarness) deviceCount() int {
	h.t.Helper()
	devices, err := h.st.ListDevices(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return len(devices)
}

func TestDeviceRequestEnrollsOnlyAfterAdminApproval(t *testing.T) {
	h := newDeviceRequestHarness(t)
	ctx := context.Background()
	before := h.deviceCount()
	req := h.request("192.168.1.20:5000")

	polled, err := h.poll(req.RequestSecret, "192.168.1.20:5001")
	if err != nil || polled.Status != DeviceRequestPending || polled.Enrollment != nil || polled.ExpiresAt != req.ExpiresAt {
		t.Fatalf("poll before approval = %+v, %v; want pending without enrollment", polled, err)
	}
	if h.deviceCount() != before {
		t.Fatal("a pending request created a device")
	}

	authService := auth.NewServiceWithClock(h.st, func() time.Time { return h.now })
	nonAdmin, err := authService.Scope(ctx, "session-manager")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.approve(nonAdmin, req.Code); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin approve = %v, want ErrForbidden", err)
	}
	if _, err := h.service.ListDeviceRequests(ctx, nonAdmin); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin list = %v, want ErrForbidden", err)
	}
	if _, err := h.service.DenyDeviceRequest(ctx, nonAdmin, DeviceRequestDecision{Code: req.Code}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin deny = %v, want ErrForbidden", err)
	}

	admin := h.admin()
	list, err := h.service.ListDeviceRequests(ctx, admin)
	if err != nil || len(list.Requests) != 1 || list.Requests[0].Code != req.Code || list.Requests[0].DeviceName != "Living Room TV" {
		t.Fatalf("pending list = %+v, %v", list, err)
	}
	// Knowing the display code is not enough to collect anything.
	if _, err := h.poll(req.Code, "192.168.1.99:1"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("poll with the display code = %v, want ErrInvalidCode", err)
	}

	decision, err := h.approve(admin, strings.ToLower(req.Code))
	if err != nil || decision.Status != DeviceRequestApproved || decision.ProfileID != string(admin.ProfileID) || decision.DeviceRole != string(store.DeviceRoleViewer) {
		t.Fatalf("approve = %+v, %v", decision, err)
	}
	if list, _ := h.service.ListDeviceRequests(ctx, admin); len(list.Requests) != 0 {
		t.Fatalf("approved request still pending: %+v", list)
	}

	polled, err = h.poll(req.RequestSecret, "192.168.1.20:5002")
	if err != nil || polled.Status != DeviceRequestApproved || polled.Enrollment == nil {
		t.Fatalf("poll after approval = %+v, %v", polled, err)
	}
	got := *polled.Enrollment
	if got.DeviceSecret == "" || got.SessionID == "" || got.ProfileID != string(admin.ProfileID) || got.DeviceRole != string(store.DeviceRoleViewer) || got.ServerKeyPin != testDevicePin {
		t.Fatalf("enrollment = %+v", got)
	}
	device, err := h.st.Device(ctx, store.DeviceID(got.DeviceID))
	if err != nil || device.Name != "Living Room TV" || device.Role != store.DeviceRoleViewer {
		t.Fatalf("stored device = %+v, %v", device, err)
	}
	if _, err := auth.NewServiceWithClock(h.st, func() time.Time { return h.now }).Scope(ctx, store.SessionID(got.SessionID)); err != nil {
		t.Fatalf("delivered session does not authenticate: %v", err)
	}

	// Single use: the secret and the code are spent.
	if _, err := h.poll(req.RequestSecret, "192.168.1.20:5003"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("reused secret = %v, want ErrInvalidCode", err)
	}
	if _, err := h.approve(admin, req.Code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("approving a spent code = %v, want ErrInvalidCode", err)
	}
	if h.deviceCount() != before+1 {
		t.Fatalf("devices = %d, want %d", h.deviceCount(), before+1)
	}
	if !hasActivity(t, h.st, store.ActivityPairingCode, string(admin.ProfileID)) || !hasActivity(t, h.st, store.ActivityEnroll, got.DeviceID) {
		t.Fatal("approval or enrollment was not recorded in activity")
	}
}

func TestDeviceRequestCodesNeverRedeemThroughEnroll(t *testing.T) {
	h := newDeviceRequestHarness(t)
	req := h.request("192.168.1.20:5000")
	if _, err := h.approve(h.admin(), req.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.Enroll(context.Background(), EnrollRequest{Code: req.Code, DeviceName: "Thief", IsLAN: true}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("/api/enroll with a device-request code = %v, want ErrInvalidCode", err)
	}
}

func TestDeviceRequestExpires(t *testing.T) {
	h := newDeviceRequestHarness(t)
	admin := h.admin()
	req := h.request("192.168.1.20:5000")
	h.now = h.now.Add(deviceRequestTTL)
	if _, err := h.approve(admin, req.Code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("approving an expired code = %v, want ErrInvalidCode", err)
	}
	if _, err := h.poll(req.RequestSecret, "192.168.1.20:5000"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("polling an expired request = %v, want ErrInvalidCode", err)
	}
	if list, _ := h.service.ListDeviceRequests(context.Background(), admin); len(list.Requests) != 0 {
		t.Fatalf("expired request listed: %+v", list)
	}

	// An approval the device never collects in time dies too.
	req = h.request("192.168.1.20:5000")
	if _, err := h.approve(admin, req.Code); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(deviceRequestTTL)
	if _, err := h.poll(req.RequestSecret, "192.168.1.20:5000"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("collecting an expired approval = %v, want ErrInvalidCode", err)
	}
}

func TestDeviceRequestRegenerateKillsTheOldCodeAndSecret(t *testing.T) {
	h := newDeviceRequestHarness(t)
	admin := h.admin()
	ctx := context.Background()
	old := h.request("192.168.1.20:5000")
	h.now = h.now.Add(9 * time.Minute)
	fresh, err := h.service.RegenerateDevicePairing(ctx, DevicePollRequest{RequestSecret: old.RequestSecret, RemoteAddr: "192.168.1.20:5000", IsLAN: true})
	if err != nil || fresh.Code == old.Code || fresh.RequestSecret == old.RequestSecret || fresh.ExpiresAt == old.ExpiresAt {
		t.Fatalf("regenerate = %+v, %v", fresh, err)
	}
	if _, err := h.approve(admin, old.Code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("approving a regenerated-away code = %v, want ErrInvalidCode", err)
	}
	if _, err := h.poll(old.RequestSecret, "192.168.1.20:5000"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("polling a regenerated-away secret = %v, want ErrInvalidCode", err)
	}
	if _, err := h.service.RegenerateDevicePairing(ctx, DevicePollRequest{RequestSecret: old.RequestSecret, IsLAN: true}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("regenerating from a dead secret = %v, want ErrInvalidCode", err)
	}
	// The fresh code outlives the original ten minutes.
	h.now = h.now.Add(5 * time.Minute)
	if _, err := h.approve(admin, fresh.Code); err != nil {
		t.Fatalf("approving the fresh code = %v", err)
	}
	polled, err := h.poll(fresh.RequestSecret, "192.168.1.20:5000")
	if err != nil || polled.Enrollment == nil {
		t.Fatalf("collecting the fresh approval = %+v, %v", polled, err)
	}
}

func TestDeviceRequestDeny(t *testing.T) {
	h := newDeviceRequestHarness(t)
	admin := h.admin()
	before := h.deviceCount()
	req := h.request("192.168.1.20:5000")
	decision, err := h.service.DenyDeviceRequest(context.Background(), admin, DeviceRequestDecision{Code: req.Code})
	if err != nil || decision.Status != DeviceRequestDenied {
		t.Fatalf("deny = %+v, %v", decision, err)
	}
	if _, err := h.approve(admin, req.Code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("approving a denied code = %v, want ErrInvalidCode", err)
	}
	polled, err := h.poll(req.RequestSecret, "192.168.1.20:5000")
	if err != nil || polled.Status != DeviceRequestDenied || polled.Enrollment != nil {
		t.Fatalf("poll after deny = %+v, %v", polled, err)
	}
	if _, err := h.poll(req.RequestSecret, "192.168.1.20:5000"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("poll after the denial was read = %v, want ErrInvalidCode", err)
	}
	if h.deviceCount() != before {
		t.Fatal("a denied request created a device")
	}
}

func TestDeviceRequestPollAndRequestAreThrottledBySource(t *testing.T) {
	h := newDeviceRequestHarness(t)
	const attacker = "192.168.1.66:4000"
	for i := range enrollBackoff.FreeAttempts {
		if _, err := h.poll(fmt.Sprintf("guess-%d", i), attacker); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("guess %d = %v, want ErrInvalidCode", i, err)
		}
	}
	if _, err := h.poll("guess-next", attacker); !errors.Is(err, ErrThrottled) {
		t.Fatalf("poll after free attempts = %v, want ErrThrottled", err)
	}
	if _, err := h.service.RequestDevicePairing(context.Background(), DeviceRequest{DeviceName: "TV", RemoteAddr: attacker, IsLAN: true}); !errors.Is(err, ErrThrottled) {
		t.Fatalf("request from a throttled source = %v, want ErrThrottled", err)
	}
	// Another source is unaffected, and its live polls record no attempts.
	req := h.request("192.168.1.20:5000")
	for range 2 * enrollBackoff.FreeAttempts {
		if polled, err := h.poll(req.RequestSecret, "192.168.1.20:5000"); err != nil || polled.Status != DeviceRequestPending {
			t.Fatalf("live poll = %+v, %v", polled, err)
		}
	}
}

func TestDeviceRequestPendingTableIsCapped(t *testing.T) {
	h := newDeviceRequestHarness(t)
	for range maxDeviceRequestsPerSource {
		h.request("192.168.1.20:5000")
	}
	if _, err := h.service.RequestDevicePairing(context.Background(), DeviceRequest{DeviceName: "TV", RemoteAddr: "192.168.1.20:5000", IsLAN: true}); !errors.Is(err, ErrThrottled) {
		t.Fatalf("request over the per-source cap = %v, want ErrThrottled", err)
	}
	for i := maxDeviceRequestsPerSource; i < maxDeviceRequests; i++ {
		h.request(fmt.Sprintf("10.0.%d.%d:5000", i/250, i%250+1))
	}
	if _, err := h.service.RequestDevicePairing(context.Background(), DeviceRequest{DeviceName: "TV", RemoteAddr: "10.9.9.9:5000", IsLAN: true}); !errors.Is(err, ErrThrottled) {
		t.Fatalf("request over the table cap = %v, want ErrThrottled", err)
	}
	// Expiry frees the table.
	h.now = h.now.Add(deviceRequestTTL)
	h.request("10.9.9.9:5000")
}

func TestDeviceRequestViewerSwitch(t *testing.T) {
	h := newDeviceRequestHarness(t)
	admin := h.admin()
	ctx := context.Background()
	pending := h.request("192.168.1.20:5000")
	disableViewerPairingCodes(t, h.st, h.now)

	_, err := h.service.RequestDevicePairing(ctx, DeviceRequest{DeviceName: "TV", RemoteAddr: "192.168.1.21:5000", IsLAN: true})
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("viewer request with the switch on = %v, want ErrInvalidCode", err)
	}
	if list, _ := h.service.ListDeviceRequests(ctx, admin); len(list.Requests) != 1 {
		t.Fatalf("refused request left an entry: %+v", list)
	}
	if _, err := h.approve(admin, pending.Code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("approving a pending viewer request with the switch on = %v, want ErrInvalidCode", err)
	}
	if polled, err := h.poll(pending.RequestSecret, "192.168.1.20:5000"); err != nil || polled.Status != DeviceRequestPending {
		t.Fatalf("refused approval changed the request: %+v, %v", polled, err)
	}
	if len(viewerCodeRefusals(t, h.st, viewerCodeDeviceRequestRefused)) != 1 || len(viewerCodeRefusals(t, h.st, viewerCodeApproveRefused)) != 1 {
		t.Fatal("viewer refusals were not recorded")
	}

	// An approval granted before the switch flipped is refused at collection.
	enabled := false
	if _, err := h.st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{ViewerPairingCodesDisabled: &enabled}, h.now); err != nil {
		t.Fatal(err)
	}
	if _, err := h.approve(admin, pending.Code); err != nil {
		t.Fatal(err)
	}
	disableViewerPairingCodes(t, h.st, h.now)
	before := h.deviceCount()
	if _, err := h.poll(pending.RequestSecret, "192.168.1.20:5000"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("collecting a viewer approval with the switch on = %v, want ErrInvalidCode", err)
	}
	if h.deviceCount() != before {
		t.Fatal("refused collection created a device")
	}
}

func TestDeviceRequestRemoteEnrollmentGate(t *testing.T) {
	h := newDeviceRequestHarness(t)
	ctx := context.Background()
	if _, err := h.service.RequestDevicePairing(ctx, DeviceRequest{DeviceName: "TV", RemoteAddr: "203.0.113.5:5000"}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("off-LAN request with remote enrollment off = %v, want ErrInvalidCode", err)
	}
	// A LAN request collected from off the LAN obeys the same gate.
	req := h.request("192.168.1.20:5000")
	if _, err := h.approve(h.admin(), req.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.PollDevicePairing(ctx, DevicePollRequest{RequestSecret: req.RequestSecret, RemoteAddr: "203.0.113.5:5000"}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("off-LAN collection with remote enrollment off = %v, want ErrInvalidCode", err)
	}

	enabled := true
	if _, err := h.st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{RemoteEnrollmentEnabled: &enabled}, h.now); err != nil {
		t.Fatal(err)
	}
	remote, err := h.service.RequestDevicePairing(ctx, DeviceRequest{DeviceName: "TV", RemoteAddr: "203.0.113.5:5000"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.approve(h.admin(), remote.Code); err != nil {
		t.Fatal(err)
	}
	polled, err := h.service.PollDevicePairing(ctx, DevicePollRequest{RequestSecret: remote.RequestSecret, RemoteAddr: "192.168.1.20:5000", IsLAN: true})
	if err != nil || polled.Enrollment == nil {
		t.Fatalf("collect remote request = %+v, %v", polled, err)
	}
	if polled.Enrollment.ServerKeyPin != "" {
		t.Fatal("a request made off the LAN received a server key pin")
	}
}

func TestDeviceRequestRejectsUntrustedNames(t *testing.T) {
	h := newDeviceRequestHarness(t)
	for _, name := range []string{"", "   ", "TV\x00", "TV\nAdmin", "TV\u202eVT", "TV\u200b", strings.Repeat("a", maxDeviceNameRunes+1), "\xff\xfe"} {
		if _, err := h.service.RequestDevicePairing(context.Background(), DeviceRequest{DeviceName: name, RemoteAddr: "192.168.1.20:5000", IsLAN: true}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("name %q = %v, want ErrInvalidRequest", name, err)
		}
	}
	resp, err := h.service.RequestDevicePairing(context.Background(), DeviceRequest{DeviceName: "  Télé du salon " + strings.Repeat("é", 40) + "  ", RemoteAddr: "192.168.1.20:5000", IsLAN: true})
	if err != nil {
		t.Fatalf("accented name = %v", err)
	}
	list, _ := h.service.ListDeviceRequests(context.Background(), h.admin())
	if len(list.Requests) != 1 || list.Requests[0].Code != resp.Code || strings.HasPrefix(list.Requests[0].DeviceName, " ") {
		t.Fatalf("list = %+v", list)
	}
}

func hasActivity(t *testing.T, st *store.Store, action, targetID string) bool {
	t.Helper()
	for _, entry := range activityEntries(t, st) {
		if entry.Action == action && entry.TargetID == targetID {
			return true
		}
	}
	return false
}

func TestDeviceRequestNamesTheDeviceToTheAdmin(t *testing.T) {
	h := newDeviceRequestHarness(t)
	ctx := context.Background()
	lan, err := h.service.RequestDevicePairing(ctx, DeviceRequest{DeviceName: "Pixel 9", Platform: " android ", RemoteAddr: "192.168.1.20:5000", IsLAN: true})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := h.st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{RemoteEnrollmentEnabled: &enabled}, h.now); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(time.Second)
	remote, err := h.service.RequestDevicePairing(ctx, DeviceRequest{DeviceName: "Office Mac", Platform: "macos", RequestedRole: "manager", RemoteAddr: "203.0.113.5:5000"})
	if err != nil {
		t.Fatal(err)
	}

	list, err := h.service.ListDeviceRequests(ctx, h.admin())
	if err != nil || len(list.Requests) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	want := []PendingDeviceRequest{
		{Code: lan.Code, DeviceName: "Pixel 9", Platform: "android", RequestedRole: "viewer", LAN: true},
		{Code: remote.Code, DeviceName: "Office Mac", Platform: "macos", RequestedRole: "manager", LAN: false},
	}
	for i, got := range list.Requests {
		w := want[i]
		if got.Code != w.Code || got.DeviceName != w.DeviceName || got.Platform != w.Platform || got.RequestedRole != w.RequestedRole || got.LAN != w.LAN {
			t.Fatalf("pending[%d] = %+v, want %+v", i, got, w)
		}
	}
}

func TestDeviceRequestRoleOnlyThroughApproval(t *testing.T) {
	h := newDeviceRequestHarness(t)
	ctx := context.Background()
	for _, role := range []string{"admin", "owner", "Manager", "root"} {
		if _, err := h.service.RequestDevicePairing(ctx, DeviceRequest{DeviceName: "TV", RequestedRole: role, RemoteAddr: "192.168.1.20:5000", IsLAN: true}); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("requested role %q = %v, want ErrInvalidCode", role, err)
		}
	}
	// Each refused role counted against the address: the throttle now holds
	// even a valid request from it.
	if _, err := h.service.RequestDevicePairing(ctx, DeviceRequest{DeviceName: "TV", RequestedRole: "admin", RemoteAddr: "192.168.1.20:5000", IsLAN: true}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("fifth refused role = %v", err)
	}
	if _, err := h.service.RequestDevicePairing(ctx, DeviceRequest{DeviceName: "TV", RemoteAddr: "192.168.1.20:5000", IsLAN: true}); !errors.Is(err, ErrThrottled) {
		t.Fatalf("request after refused roles = %v, want ErrThrottled", err)
	}

	// A manager request is only a request: nothing is enrolled until an
	// admin approves, and the admin may grant less.
	other := "192.168.1.30:5000"
	manager, err := h.service.RequestDevicePairing(ctx, DeviceRequest{DeviceName: "Office Mac", RequestedRole: "manager", RemoteAddr: other, IsLAN: true})
	if err != nil {
		t.Fatal(err)
	}
	if polled, err := h.poll(manager.RequestSecret, other); err != nil || polled.Enrollment != nil {
		t.Fatalf("unapproved manager request = %+v, %v", polled, err)
	}
	// An approval that names no role grants Viewer, whatever the device asked
	// for: the requested role is untrusted input from an unauthenticated host.
	decision, err := h.approve(h.admin(), manager.Code)
	if err != nil || decision.DeviceRole != "viewer" {
		t.Fatalf("approve without a role = %+v, %v; want viewer", decision, err)
	}
	polled, err := h.poll(manager.RequestSecret, other)
	if err != nil || polled.Enrollment == nil || polled.Enrollment.DeviceRole != "viewer" {
		t.Fatalf("collect default approval = %+v, %v; want a viewer device", polled, err)
	}

	// Manager is granted only when the admin names it.
	explicit, err := h.service.RequestDevicePairing(ctx, DeviceRequest{DeviceName: "Office Mac", RequestedRole: "manager", RemoteAddr: other, IsLAN: true})
	if err != nil {
		t.Fatal(err)
	}
	decision, err = h.service.ApproveDeviceRequest(ctx, h.admin(), DeviceRequestDecision{Code: explicit.Code, DeviceRole: "manager"})
	if err != nil || decision.DeviceRole != "manager" {
		t.Fatalf("approve naming manager = %+v, %v", decision, err)
	}
	polled, err = h.poll(explicit.RequestSecret, other)
	if err != nil || polled.Enrollment == nil || polled.Enrollment.DeviceRole != "manager" {
		t.Fatalf("collect manager approval = %+v, %v", polled, err)
	}

	downgraded, err := h.service.RequestDevicePairing(ctx, DeviceRequest{DeviceName: "Shared iPad", RequestedRole: "manager", RemoteAddr: other, IsLAN: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.ApproveDeviceRequest(ctx, h.admin(), DeviceRequestDecision{Code: downgraded.Code, DeviceRole: "viewer"}); err != nil {
		t.Fatal(err)
	}
	polled, err = h.poll(downgraded.RequestSecret, other)
	if err != nil || polled.Enrollment == nil || polled.Enrollment.DeviceRole != "viewer" {
		t.Fatalf("collect downgraded approval = %+v, %v", polled, err)
	}

	// With Viewer codes disabled a manager request still waits for approval,
	// and an admin cannot downgrade it to Viewer.
	disableViewerPairingCodes(t, h.st, h.now)
	pending, err := h.service.RequestDevicePairing(ctx, DeviceRequest{DeviceName: "Laptop", RequestedRole: "manager", RemoteAddr: other, IsLAN: true})
	if err != nil {
		t.Fatalf("manager request with Viewer codes disabled = %v", err)
	}
	if _, err := h.service.ApproveDeviceRequest(ctx, h.admin(), DeviceRequestDecision{Code: pending.Code, DeviceRole: "viewer"}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("downgrade to Viewer with the switch on = %v, want ErrInvalidCode", err)
	}
	// The default approval is a Viewer grant, so the switch refuses it too.
	if _, err := h.approve(h.admin(), pending.Code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("default approval with the switch on = %v, want ErrInvalidCode", err)
	}
}

func TestDeviceRequestRejectsUntrustedPlatform(t *testing.T) {
	h := newDeviceRequestHarness(t)
	for _, platform := range []string{"tvos\x00", "ios\u202e", strings.Repeat("p", maxDevicePlatformRunes+1)} {
		if _, err := h.service.RequestDevicePairing(context.Background(), DeviceRequest{DeviceName: "TV", Platform: platform, RemoteAddr: "192.168.1.20:5000", IsLAN: true}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("platform %q = %v, want ErrInvalidRequest", platform, err)
		}
	}
	if _, err := h.service.RequestDevicePairing(context.Background(), DeviceRequest{DeviceName: "TV", RemoteAddr: "192.168.1.20:5000", IsLAN: true}); err != nil {
		t.Fatalf("request without a platform = %v", err)
	}
}

// A source that hits the pending caps is told to back off, but the cap is not
// a failed guess: it must not throttle the source's own live polls.
func TestDeviceRequestCapDoesNotSpendThrottleAttempts(t *testing.T) {
	h := newDeviceRequestHarness(t)
	const source = "192.168.1.20:5000"
	live := h.request(source)
	for range maxDeviceRequestsPerSource - 1 {
		h.request(source)
	}
	for i := range enrollBackoff.FreeAttempts + 2 {
		if _, err := h.service.RequestDevicePairing(context.Background(), DeviceRequest{DeviceName: "TV", RemoteAddr: source, IsLAN: true}); !errors.Is(err, ErrThrottled) {
			t.Fatalf("request %d over the cap = %v, want ErrThrottled", i, err)
		}
	}
	if polled, err := h.poll(live.RequestSecret, source); err != nil || polled.Status != DeviceRequestPending {
		t.Fatalf("live poll after hitting the cap = %+v, %v; want pending", polled, err)
	}
}

// Pairing requests have their own throttle lane: a device polling a secret
// the server forgot (a restart, an expiry) must not lock its address out of
// code enrollment.
func TestDeviceRequestFailuresDoNotThrottleCodeEnrollment(t *testing.T) {
	h := newDeviceRequestHarness(t)
	ctx := context.Background()
	const source = "192.168.1.20:5000"
	for i := range enrollBackoff.FreeAttempts + 2 {
		_, err := h.poll("stale-secret", source)
		if !errors.Is(err, ErrInvalidCode) && !errors.Is(err, ErrThrottled) {
			t.Fatalf("stale poll %d = %v", i, err)
		}
	}
	if _, err := h.poll("stale-secret", source); !errors.Is(err, ErrThrottled) {
		t.Fatalf("stale polls past the free attempts = %v, want ErrThrottled on the pairing-request lane", err)
	}
	if _, err := h.service.Enroll(ctx, EnrollRequest{Code: "N0SVCHCD", DeviceName: "TV", RemoteAddr: source, IsLAN: true}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("code enrollment after stale pairing polls = %v, want ErrInvalidCode (not throttled)", err)
	}
	// And the other way round: bad codes on /api/enroll do not stop a device
	// asking to join.
	const other = "192.168.1.30:5000"
	for range enrollBackoff.FreeAttempts + 1 {
		_, _ = h.service.Enroll(ctx, EnrollRequest{Code: "N0SVCHCD", DeviceName: "TV", RemoteAddr: other, IsLAN: true})
	}
	if _, err := h.service.Enroll(ctx, EnrollRequest{Code: "N0SVCHCD", DeviceName: "TV", RemoteAddr: other, IsLAN: true}); !errors.Is(err, ErrThrottled) {
		t.Fatalf("enroll after bad codes = %v, want ErrThrottled", err)
	}
	if _, err := h.service.RequestDevicePairing(ctx, DeviceRequest{DeviceName: "TV", RemoteAddr: other, IsLAN: true}); err != nil {
		t.Fatalf("pairing request from a source throttled on code enrollment = %v", err)
	}
}

// A store failure while enrolling an approved request is not a failed guess:
// every poll would otherwise spend an attempt and throttle the device out of
// the approval it is waiting to collect.
func TestDeviceRequestTransientEnrollFailureKeepsTheApprovalAndTheThrottle(t *testing.T) {
	h := newDeviceRequestHarness(t)
	const source = "192.168.1.20:5000"
	req := h.request(source)
	if _, err := h.approve(h.admin(), req.Code); err != nil {
		t.Fatal(err)
	}
	before := h.deviceCount()
	generateID := h.service.generateID
	// A blank device id makes the store refuse the insert.
	h.service.generateID = func(prefix string) (string, error) {
		if prefix == "device" {
			return "", nil
		}
		return generateID(prefix)
	}
	for i := range enrollBackoff.FreeAttempts + 2 {
		_, err := h.poll(req.RequestSecret, source)
		if err == nil || errors.Is(err, ErrInvalidCode) || errors.Is(err, ErrThrottled) {
			t.Fatalf("poll %d during a store failure = %v, want the store error", i, err)
		}
	}
	if h.deviceCount() != before {
		t.Fatal("a failed enrollment left a device")
	}
	h.service.generateID = generateID
	polled, err := h.poll(req.RequestSecret, source)
	if err != nil || polled.Status != DeviceRequestApproved || polled.Enrollment == nil {
		t.Fatalf("poll after the store recovered = %+v, %v; want the enrollment", polled, err)
	}
}

func TestDeviceRequestRegenerateObeysTheRemoteEnrollmentGate(t *testing.T) {
	h := newDeviceRequestHarness(t)
	ctx := context.Background()
	admin := h.admin()
	req := h.request("192.168.1.20:5000")
	if _, err := h.service.RegenerateDevicePairing(ctx, DevicePollRequest{RequestSecret: req.RequestSecret, RemoteAddr: "203.0.113.5:5000"}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("off-LAN regenerate with remote enrollment off = %v, want ErrInvalidCode", err)
	}
	if polled, err := h.poll(req.RequestSecret, "192.168.1.20:5000"); err != nil || polled.Status != DeviceRequestPending {
		t.Fatalf("refused regenerate changed the request: %+v, %v", polled, err)
	}

	enabled := true
	if _, err := h.st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{RemoteEnrollmentEnabled: &enabled}, h.now); err != nil {
		t.Fatal(err)
	}
	fresh, err := h.service.RegenerateDevicePairing(ctx, DevicePollRequest{RequestSecret: req.RequestSecret, RemoteAddr: "203.0.113.5:5000"})
	if err != nil {
		t.Fatalf("off-LAN regenerate with remote enrollment on = %v", err)
	}
	// The secret has now travelled off the LAN: the admin is told, and the
	// collection gets no server key pin.
	list, err := h.service.ListDeviceRequests(ctx, admin)
	if err != nil || len(list.Requests) != 1 || list.Requests[0].LAN {
		t.Fatalf("list after an off-LAN regenerate = %+v, %v; want lan false", list, err)
	}
	if _, err := h.approve(admin, fresh.Code); err != nil {
		t.Fatal(err)
	}
	polled, err := h.poll(fresh.RequestSecret, "192.168.1.20:5000")
	if err != nil || polled.Enrollment == nil || polled.Enrollment.ServerKeyPin != "" {
		t.Fatalf("collect after an off-LAN regenerate = %+v, %v; want no pin", polled, err)
	}
}

// removeLocked drops only the entry it was given: a slot another request now
// holds is left alone.
func TestDeviceRequestRemoveKeepsAnotherEntrysSlot(t *testing.T) {
	var table deviceRequests
	table.init()
	stale := &deviceRequestEntry{code: "CODE0001", secretHash: hashRequestSecret("one")}
	current := &deviceRequestEntry{code: "CODE0001", secretHash: hashRequestSecret("one")}
	table.byCode[current.code] = current
	table.bySecret[current.secretHash] = current
	table.removeLocked(stale)
	if table.byCode["CODE0001"] != current || table.bySecret[current.secretHash] != current {
		t.Fatal("removing a stale entry dropped the request that now holds its code")
	}
	table.removeLocked(current)
	if len(table.byCode) != 0 || len(table.bySecret) != 0 {
		t.Fatal("removing the live entry left it in the table")
	}
}

// Two polls racing for one approval: the first enrolls, the second reads as
// pending while that enrollment is in flight, and exactly one device exists.
func TestDeviceRequestConcurrentPollsEnrollOnce(t *testing.T) {
	h := newDeviceRequestHarness(t)
	const source = "192.168.1.20:5000"
	req := h.request(source)
	if _, err := h.approve(h.admin(), req.Code); err != nil {
		t.Fatal(err)
	}
	before := h.deviceCount()
	entered := make(chan struct{})
	release := make(chan struct{})
	generateID := h.service.generateID
	var once sync.Once
	h.service.generateID = func(prefix string) (string, error) {
		once.Do(func() {
			close(entered)
			<-release
		})
		return generateID(prefix)
	}
	type result struct {
		resp DevicePollResponse
		err  error
	}
	first := make(chan result, 1)
	go func() {
		resp, err := h.poll(req.RequestSecret, source)
		first <- result{resp, err}
	}()
	<-entered
	second, err := h.poll(req.RequestSecret, source)
	if err != nil || second.Status != DeviceRequestPending || second.Enrollment != nil {
		t.Fatalf("poll during a collection = %+v, %v; want pending without enrollment", second, err)
	}
	close(release)
	got := <-first
	if got.err != nil || got.resp.Status != DeviceRequestApproved || got.resp.Enrollment == nil {
		t.Fatalf("collecting poll = %+v, %v", got.resp, got.err)
	}
	if h.deviceCount() != before+1 {
		t.Fatalf("devices = %d, want %d", h.deviceCount(), before+1)
	}
	if _, err := h.poll(req.RequestSecret, source); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("poll after the collection = %v, want ErrInvalidCode", err)
	}
}

func TestDeviceRequestCancel(t *testing.T) {
	h := newDeviceRequestHarness(t)
	const source = "192.168.1.20:5000"
	ctx := context.Background()
	cancel := func(secret string) error {
		return h.service.CancelDevicePairing(ctx, DevicePollRequest{RequestSecret: secret, RemoteAddr: source, IsLAN: true})
	}
	req := h.request(source)
	if err := cancel(""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cancel without a secret = %v, want ErrInvalidRequest", err)
	}
	// The display code is not the secret, and an unknown secret is not a failed guess.
	for i := range enrollBackoff.FreeAttempts + 2 {
		if err := cancel(req.Code); err != nil {
			t.Fatalf("cancel %d with the display code = %v", i, err)
		}
	}
	if polled, err := h.poll(req.RequestSecret, source); err != nil || polled.Status != DeviceRequestPending {
		t.Fatalf("poll after cancels that named no request = %+v, %v; want pending", polled, err)
	}
	if err := cancel(req.RequestSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := h.approve(h.admin(), req.Code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("approve a cancelled code = %v, want ErrInvalidCode", err)
	}
	if _, err := h.poll(req.RequestSecret, source); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("poll a cancelled request = %v, want ErrInvalidCode", err)
	}

	// Cancelling an approved request withdraws the approval before it enrolls.
	approved := h.request(source)
	if _, err := h.approve(h.admin(), approved.Code); err != nil {
		t.Fatal(err)
	}
	before := h.deviceCount()
	if err := cancel(approved.RequestSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := h.poll(approved.RequestSecret, source); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("poll a cancelled approval = %v, want ErrInvalidCode", err)
	}
	if got := h.deviceCount(); got != before {
		t.Fatalf("devices = %d after a cancelled approval, want %d", got, before)
	}
}

func TestDeviceRequestBrowserBindingAndServerRelativeExpiry(t *testing.T) {
	h := newDeviceRequestHarness(t)
	const source = "192.168.1.20:5000"
	ctx := context.Background()
	native := h.request(source)
	if native.ExpiresInSeconds != int(deviceRequestTTL/time.Second) {
		t.Fatalf("expiresInSeconds = %d, want %d", native.ExpiresInSeconds, int(deviceRequestTTL/time.Second))
	}
	if h.service.DeviceRequestBrowserBound(native.RequestSecret) {
		t.Fatal("native request reads as browser-bound")
	}
	web, err := h.service.RequestDevicePairing(ctx, DeviceRequest{DeviceName: "Chrome", Platform: "web", RemoteAddr: source, IsLAN: true, Browser: true})
	if err != nil {
		t.Fatal(err)
	}
	if !h.service.DeviceRequestBrowserBound(web.RequestSecret) || h.service.DeviceRequestBrowserBound(web.Code) || h.service.DeviceRequestBrowserBound("") {
		t.Fatal("browser binding is not keyed by the request secret")
	}
	fresh, err := h.service.RegenerateDevicePairing(ctx, DevicePollRequest{RequestSecret: web.RequestSecret, RemoteAddr: source, IsLAN: true})
	if err != nil {
		t.Fatal(err)
	}
	if h.service.DeviceRequestBrowserBound(web.RequestSecret) || !h.service.DeviceRequestBrowserBound(fresh.RequestSecret) {
		t.Fatal("regenerate did not carry the browser binding to the fresh secret")
	}
	pending, err := h.poll(fresh.RequestSecret, source)
	if err != nil || !pending.Browser || pending.ExpiresInSeconds != fresh.ExpiresInSeconds {
		t.Fatalf("pending browser poll = %+v, %v", pending, err)
	}
	if _, err := h.approve(h.admin(), fresh.Code); err != nil {
		t.Fatal(err)
	}
	collected, err := h.poll(fresh.RequestSecret, source)
	if err != nil || collected.Enrollment == nil || !collected.Browser {
		t.Fatalf("collected browser poll = %+v, %v", collected, err)
	}
	if collected, err := func() (DevicePollResponse, error) {
		if _, err := h.approve(h.admin(), native.Code); err != nil {
			return DevicePollResponse{}, err
		}
		return h.poll(native.RequestSecret, source)
	}(); err != nil || collected.Enrollment == nil || collected.Browser {
		t.Fatalf("collected native poll = %+v, %v", collected, err)
	}
}

package pairing

import (
	"context"
	"crypto/sha256"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Device-initiated pairing (CORE-0316): a device asks for a short code, shows
// it, and polls with a request secret only it holds; an admin approves the code
// it sees on the device, and the device's next poll receives its enrollment.
// Pending requests live in memory and die on restart.
const (
	deviceRequestTTL = 10 * time.Minute
	// DeviceRequestPollInterval is how often a device should poll.
	DeviceRequestPollInterval = 3 * time.Second
	// maxDeviceRequests bounds the whole table; maxDeviceRequestsPerSource
	// bounds one source address lane, so a single host cannot fill the table.
	maxDeviceRequests          = 64
	maxDeviceRequestsPerSource = 3
	maxDeviceNameRunes         = 64
	maxDevicePlatformRunes     = 32

	// Device request polls answer with one of these states.
	DeviceRequestPending  = "pending"
	DeviceRequestApproved = "approved"
	DeviceRequestDenied   = "denied"
	// deviceRequestCollecting marks an approved request whose enrollment is
	// in flight. It never leaves the server: such a poll reads as pending.
	deviceRequestCollecting = "collecting"

	// deviceRequestAttemptAction is the pairing-request throttle lane. It is
	// separate from the /api/enroll lane so a device polling a secret the
	// server no longer knows cannot lock its address out of code enrollment,
	// and bad codes cannot stop a device asking to join.
	deviceRequestAttemptAction = "pairing.request"

	deviceRequestTarget            = "pairing_request"
	viewerCodeDeviceRequestRefused = "viewer_pairing_codes_disabled:device_request"
	viewerCodeApproveRefused       = "viewer_pairing_codes_disabled:approve"
)

// DeviceRequest is an unauthenticated device asking to pair. DeviceName and
// Platform are untrusted display text. RequestedRole is only a request: the
// device gets a role solely through an admin's approval (CORE-2063).
type DeviceRequest struct {
	DeviceName        string `json:"deviceName"`
	Platform          string `json:"platform,omitempty"`
	RequestedRole     string `json:"requestedRole,omitempty"`
	DistributionClass string `json:"distributionClass,omitempty"`
	RemoteAddr        string `json:"-"`
	IsLAN             bool   `json:"-"`
	// Browser marks a request made by the web app. The binding is fixed here,
	// at creation, and decides how the enrollment is delivered: a browser's
	// goes into HttpOnly cookies and never into page-readable JSON.
	Browser bool `json:"-"`
}

// DeviceRequestResponse gives the device the code to display and the secret to
// poll with. The code is only a display handle for the admin; the secret is
// what collects the enrollment, so it never leaves the device.
//
// ExpiresInSeconds is the lifetime left on the server's clock; a device whose
// own clock is wrong counts down from it rather than from ExpiresAt.
type DeviceRequestResponse struct {
	Code                string `json:"code"`
	RequestSecret       string `json:"requestSecret"`
	ExpiresAt           string `json:"expiresAt"`
	ExpiresInSeconds    int    `json:"expiresInSeconds"`
	PollIntervalSeconds int    `json:"pollIntervalSeconds"`
}

// DevicePollRequest names a pending request by its secret. It is also the
// regenerate and cancel body.
type DevicePollRequest struct {
	RequestSecret string `json:"requestSecret"`
	RemoteAddr    string `json:"-"`
	IsLAN         bool   `json:"-"`
}

// DevicePollResponse is the state of a request. Enrollment is set only on the
// one poll that collects an approved request. Browser reports the binding the
// request was created with.
type DevicePollResponse struct {
	Status           string          `json:"status"`
	ExpiresAt        string          `json:"expiresAt,omitempty"`
	ExpiresInSeconds int             `json:"expiresInSeconds,omitempty"`
	Enrollment       *EnrollResponse `json:"enrollment,omitempty"`
	Browser          bool            `json:"-"`
}

// PendingDeviceRequest is one row of the admin pending list. LAN is derived
// by the server from where the request arrived, never from the device.
type PendingDeviceRequest struct {
	Code              string `json:"code"`
	DeviceName        string `json:"deviceName"`
	Platform          string `json:"platform"`
	RequestedRole     string `json:"requestedRole"`
	LAN               bool   `json:"lan"`
	DistributionClass string `json:"distributionClass"`
	RequestedAt       string `json:"requestedAt"`
	ExpiresAt         string `json:"expiresAt"`
}

// PendingDeviceRequests is the admin pending list.
type PendingDeviceRequests struct {
	Requests []PendingDeviceRequest `json:"requests"`
}

// DeviceRequestDecision approves or denies the request showing Code. On
// approval ProfileID defaults to the admin's own Profile and DeviceRole to
// viewer: the role the device asked for is untrusted, so manager is granted
// only when the admin names it.
type DeviceRequestDecision struct {
	Code       string `json:"code"`
	ProfileID  string `json:"profileID,omitempty"`
	DeviceRole string `json:"deviceRole,omitempty"`
}

// DeviceRequestDecisionResponse confirms a decision.
type DeviceRequestDecisionResponse struct {
	Code       string `json:"code"`
	Status     string `json:"status"`
	ProfileID  string `json:"profileID,omitempty"`
	DeviceRole string `json:"deviceRole,omitempty"`
}

type deviceRequestEntry struct {
	code              string
	secretHash        [sha256.Size]byte
	name              string
	platform          string
	distributionClass store.DeviceDistributionClass
	role              store.DeviceRole
	isLAN             bool
	browser           bool
	source            string
	requestedAt       time.Time
	expiresAt         time.Time
	status            string
	approvedProfile   store.ProfileID
	approvedRole      store.DeviceRole
}

// deviceRequests is the in-memory pending table, indexed by display code and
// by secret hash.
type deviceRequests struct {
	mu       sync.Mutex
	byCode   map[string]*deviceRequestEntry
	bySecret map[[sha256.Size]byte]*deviceRequestEntry
}

func (t *deviceRequests) init() {
	if t.byCode == nil {
		t.byCode = map[string]*deviceRequestEntry{}
		t.bySecret = map[[sha256.Size]byte]*deviceRequestEntry{}
	}
}

// sweepLocked drops every entry that expired at or before now.
func (t *deviceRequests) sweepLocked(now time.Time) {
	for _, entry := range t.byCode {
		if !now.Before(entry.expiresAt) {
			t.removeLocked(entry)
		}
	}
}

// removeLocked drops entry. A slot that now belongs to another entry (the
// table was unlocked while entry was being enrolled) is left alone.
func (t *deviceRequests) removeLocked(entry *deviceRequestEntry) {
	if t.byCode[entry.code] == entry {
		delete(t.byCode, entry.code)
	}
	if t.bySecret[entry.secretHash] == entry {
		delete(t.bySecret, entry.secretHash)
	}
}

// secondsLeft is the whole seconds from now to expiresAt, rounded up.
func secondsLeft(expiresAt, now time.Time) int {
	return int((expiresAt.Sub(now) + time.Second - 1) / time.Second)
}

func hashRequestSecret(secret string) [sha256.Size]byte {
	return sha256.Sum256([]byte("pairing-request-secret\x00" + secret))
}

// deviceRequestSource is the throttle lane and per-source cap key. Requests
// with no source address share one lane rather than escaping the throttle.
func deviceRequestSource(remoteAddr string) string {
	return setupAttemptKey(remoteAddr)
}

// RequestDevicePairing records a pending request and returns its code and
// secret. Nothing is enrolled until an admin approves the code.
func (s *Service) RequestDevicePairing(ctx context.Context, req DeviceRequest) (DeviceRequestResponse, error) {
	name, err := untrustedLabel(req.DeviceName, maxDeviceNameRunes)
	if err != nil {
		return DeviceRequestResponse{}, err
	}
	platform := strings.TrimSpace(req.Platform)
	if platform != "" {
		if platform, err = untrustedLabel(platform, maxDevicePlatformRunes); err != nil {
			return DeviceRequestResponse{}, err
		}
	}
	distributionClass, err := store.NormalizeDeviceDistributionClass(store.DeviceDistributionClass(req.DistributionClass))
	if err != nil {
		return DeviceRequestResponse{}, ErrInvalidRequest
	}

	now := s.now()
	source := deviceRequestSource(req.RemoteAddr)
	if err := s.deviceRequestThrottle(ctx, source, now); err != nil {
		return DeviceRequestResponse{}, err
	}
	// A role outside viewer and manager (an "admin" role, say) is refused
	// like a bad code: a device cannot ask its way past the approval path.
	role, err := parseRole(req.RequestedRole)
	if err != nil {
		s.refuseDeviceRequest(ctx, source, now)
		return DeviceRequestResponse{}, ErrInvalidCode
	}
	if !req.IsLAN {
		cfg, err := s.store.ConsoleConfig(ctx)
		if err != nil {
			return DeviceRequestResponse{}, err
		}
		if !cfg.RemoteEnrollmentEnabled {
			s.refuseDeviceRequest(ctx, source, now)
			return DeviceRequestResponse{}, ErrInvalidCode
		}
	}
	// Viewer pairing-code switch: refused before any pending entry exists,
	// with the invalid-code answer, so the flow is never a way around it.
	if role == store.DeviceRoleViewer {
		if refused, err := s.refuseDisabledViewer(ctx, auth.Scope{}, "", viewerCodeDeviceRequestRefused); err != nil {
			return DeviceRequestResponse{}, err
		} else if refused {
			_ = s.recordDeviceRequestFailure(ctx, source, now)
			return DeviceRequestResponse{}, ErrInvalidCode
		}
	}

	entry := &deviceRequestEntry{
		name:              name,
		platform:          platform,
		distributionClass: distributionClass,
		role:              role,
		isLAN:             req.IsLAN,
		browser:           req.Browser,
		source:            source,
		requestedAt:       now.UTC(),
		status:            DeviceRequestPending,
	}
	// A full table or source answers ErrThrottled without spending an attempt:
	// the cap is not a failed guess, and counting it would throttle the
	// source's own live polls.
	return s.insertDeviceRequest(entry, now)
}

// RegenerateDevicePairing replaces a pending request's code and secret. The
// old code and secret stop working at once. It obeys the remote-enrollment
// gate like a new request, and a request regenerated from off the LAN stops
// counting as a LAN request.
func (s *Service) RegenerateDevicePairing(ctx context.Context, req DevicePollRequest) (DeviceRequestResponse, error) {
	now := s.now()
	source := deviceRequestSource(req.RemoteAddr)
	if err := s.deviceRequestThrottle(ctx, source, now); err != nil {
		return DeviceRequestResponse{}, err
	}
	if !req.IsLAN {
		cfg, err := s.store.ConsoleConfig(ctx)
		if err != nil {
			return DeviceRequestResponse{}, err
		}
		if !cfg.RemoteEnrollmentEnabled {
			s.refuseDeviceRequest(ctx, source, now)
			return DeviceRequestResponse{}, ErrInvalidCode
		}
	}
	secret := strings.TrimSpace(req.RequestSecret)
	freshSecret, err := s.generateSecret()
	if err != nil {
		return DeviceRequestResponse{}, err
	}
	t := &s.deviceRequests
	t.mu.Lock()
	t.init()
	t.sweepLocked(now)
	old, ok := t.bySecret[hashRequestSecret(secret)]
	if secret == "" || !ok || old.status != DeviceRequestPending {
		t.mu.Unlock()
		s.refuseDeviceRequest(ctx, source, now)
		return DeviceRequestResponse{}, ErrInvalidCode
	}
	defer t.mu.Unlock()
	// The swap happens under one lock hold, so the request keeps its table
	// slot: a concurrent new request cannot take it and strand the device
	// with a dead code.
	code, err := s.mintDeviceRequestCodeLocked()
	if err != nil {
		return DeviceRequestResponse{}, err
	}
	t.removeLocked(old)
	fresh := *old
	fresh.code = code
	fresh.secretHash = hashRequestSecret(freshSecret)
	fresh.isLAN = old.isLAN && req.IsLAN
	fresh.requestedAt = now.UTC()
	fresh.expiresAt = now.Add(deviceRequestTTL).UTC()
	t.byCode[code] = &fresh
	t.bySecret[fresh.secretHash] = &fresh
	return deviceRequestResponse(&fresh, freshSecret, now), nil
}

// CancelDevicePairing withdraws the request held by req.RequestSecret, so a
// device that gave up does not leave a code an admin could still approve. An
// unknown secret is not an error: the request is gone either way, and the
// answer tells a guesser nothing.
func (s *Service) CancelDevicePairing(ctx context.Context, req DevicePollRequest) error {
	secret := strings.TrimSpace(req.RequestSecret)
	if secret == "" {
		return ErrInvalidRequest
	}
	now := s.now()
	if err := s.deviceRequestThrottle(ctx, deviceRequestSource(req.RemoteAddr), now); err != nil {
		return err
	}
	t := &s.deviceRequests
	t.mu.Lock()
	defer t.mu.Unlock()
	t.init()
	t.sweepLocked(now)
	// A request being collected is already enrolling: that poll finishes it.
	if entry, ok := t.bySecret[hashRequestSecret(secret)]; ok && entry.status != deviceRequestCollecting {
		t.removeLocked(entry)
	}
	return nil
}

// DeviceRequestBrowserBound reports whether the live request held by secret
// was made by the web app. It changes nothing and records nothing.
func (s *Service) DeviceRequestBrowserBound(secret string) bool {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return false
	}
	now := s.now()
	t := &s.deviceRequests
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.bySecret[hashRequestSecret(secret)]
	return ok && entry.browser && now.Before(entry.expiresAt)
}

// mintDeviceRequestCodeLocked returns a code no live request holds.
func (s *Service) mintDeviceRequestCodeLocked() (string, error) {
	for range 4 {
		code, err := s.generateCode()
		if err != nil {
			return "", err
		}
		code = normalizeCode(code)
		if code == "" {
			return "", ErrInvalidRequest
		}
		if _, taken := s.deviceRequests.byCode[code]; !taken {
			return code, nil
		}
	}
	return "", errors.New("could not mint unique pairing request code")
}

func deviceRequestResponse(entry *deviceRequestEntry, secret string, now time.Time) DeviceRequestResponse {
	return DeviceRequestResponse{
		Code:                entry.code,
		RequestSecret:       secret,
		ExpiresAt:           entry.expiresAt.Format(time.RFC3339),
		ExpiresInSeconds:    secondsLeft(entry.expiresAt, now),
		PollIntervalSeconds: int(DeviceRequestPollInterval / time.Second),
	}
}

// insertDeviceRequest gives entry a fresh code, secret and expiry and stores
// it, enforcing the table and per-source caps.
func (s *Service) insertDeviceRequest(entry *deviceRequestEntry, now time.Time) (DeviceRequestResponse, error) {
	secret, err := s.generateSecret()
	if err != nil {
		return DeviceRequestResponse{}, err
	}
	entry.secretHash = hashRequestSecret(secret)
	entry.expiresAt = now.Add(deviceRequestTTL).UTC()

	t := &s.deviceRequests
	t.mu.Lock()
	defer t.mu.Unlock()
	t.init()
	t.sweepLocked(now)
	if len(t.byCode) >= maxDeviceRequests {
		return DeviceRequestResponse{}, ErrThrottled
	}
	perSource := 0
	for _, other := range t.byCode {
		if other.source == entry.source {
			perSource++
		}
	}
	if perSource >= maxDeviceRequestsPerSource {
		return DeviceRequestResponse{}, ErrThrottled
	}
	code, err := s.mintDeviceRequestCodeLocked()
	if err != nil {
		return DeviceRequestResponse{}, err
	}
	entry.code = code
	t.byCode[code] = entry
	t.bySecret[entry.secretHash] = entry
	return deviceRequestResponse(entry, secret, now), nil
}

// PollDevicePairing reports a request's state. The poll that finds it
// approved enrolls the device and consumes the request, so the enrollment is
// delivered once and only to the holder of the request secret.
func (s *Service) PollDevicePairing(ctx context.Context, req DevicePollRequest) (DevicePollResponse, error) {
	now := s.now()
	source := deviceRequestSource(req.RemoteAddr)
	if err := s.deviceRequestThrottle(ctx, source, now); err != nil {
		return DevicePollResponse{}, err
	}
	secret := strings.TrimSpace(req.RequestSecret)
	t := &s.deviceRequests
	t.mu.Lock()
	t.init()
	t.sweepLocked(now)
	entry, ok := t.bySecret[hashRequestSecret(secret)]
	if secret == "" || !ok {
		t.mu.Unlock()
		s.refuseDeviceRequest(ctx, source, now)
		return DevicePollResponse{}, ErrInvalidCode
	}
	// deviceRequestCollecting: another poll is enrolling this request right
	// now, which reads as pending.
	if entry.status == DeviceRequestPending || entry.status == deviceRequestCollecting {
		pending := DevicePollResponse{
			Status:           DeviceRequestPending,
			ExpiresAt:        entry.expiresAt.Format(time.RFC3339),
			ExpiresInSeconds: secondsLeft(entry.expiresAt, now),
			Browser:          entry.browser,
		}
		t.mu.Unlock()
		return pending, nil
	}
	if entry.status == DeviceRequestDenied {
		browser := entry.browser
		t.removeLocked(entry)
		t.mu.Unlock()
		return DevicePollResponse{Status: DeviceRequestDenied, Browser: browser}, nil
	}
	// Approved: hold the entry while enrolling so the enrollment is made
	// once, and consume it only when the outcome is final.
	entry.status = deviceRequestCollecting
	collected := *entry
	t.mu.Unlock()

	enrollment, err := s.enrollApprovedRequest(ctx, collected, req, source, now)
	t.mu.Lock()
	if err == nil || errors.Is(err, ErrInvalidCode) {
		t.removeLocked(entry)
	} else {
		// A store or context failure enrolled nothing: the approval stands
		// and the device's next poll tries again.
		entry.status = DeviceRequestApproved
	}
	t.mu.Unlock()
	if err != nil {
		return DevicePollResponse{}, err
	}
	return DevicePollResponse{Status: DeviceRequestApproved, Enrollment: &enrollment, Browser: collected.browser}, nil
}

func (s *Service) enrollApprovedRequest(ctx context.Context, entry deviceRequestEntry, req DevicePollRequest, source string, now time.Time) (EnrollResponse, error) {
	// The enrollment rules of /api/enroll apply at collection too: the
	// remote-enrollment gate, and the Viewer switch flipped after approval.
	if !req.IsLAN {
		cfg, err := s.store.ConsoleConfig(ctx)
		if err != nil {
			return EnrollResponse{}, err
		}
		if !cfg.RemoteEnrollmentEnabled {
			s.refuseDeviceRequest(ctx, source, now)
			return EnrollResponse{}, ErrInvalidCode
		}
	}
	if entry.approvedRole == store.DeviceRoleViewer {
		if refused, err := s.refuseDisabledViewer(ctx, auth.Scope{}, entry.approvedProfile, viewerCodeRedeemRefused); err != nil {
			return EnrollResponse{}, err
		} else if refused {
			_ = s.recordDeviceRequestFailure(ctx, source, now)
			return EnrollResponse{}, ErrInvalidCode
		}
	}

	deviceID, err := s.generateID("device")
	if err != nil {
		return EnrollResponse{}, err
	}
	sessionID, err := s.generateID("session")
	if err != nil {
		return EnrollResponse{}, err
	}
	deviceSecret, err := s.generateSecret()
	if err != nil {
		return EnrollResponse{}, err
	}
	sessionExpiresAt := now.Add(30 * 24 * time.Hour).UTC()
	device := store.Device{
		ID:                store.DeviceID(deviceID),
		Name:              entry.name,
		Role:              entry.approvedRole,
		DistributionClass: entry.distributionClass,
		SecretHash:        store.HashDeviceSecret(deviceSecret),
	}
	session := store.Session{
		ID:        store.SessionID(sessionID),
		DeviceID:  device.ID,
		ProfileID: entry.approvedProfile,
		ExpiresAt: sessionExpiresAt,
	}
	if err := s.store.EnrollDevice(ctx, device, session); err != nil {
		if errors.Is(err, store.ErrProfileDisabled) || strings.Contains(err.Error(), "profile not found") {
			s.refuseDeviceRequest(ctx, source, now)
			return EnrollResponse{}, ErrInvalidCode
		}
		// Any other store failure enrolled nothing and is not the device's
		// doing: the approval stands and no attempt is spent, or the polls
		// that retry it would throttle the device out of its own approval.
		return EnrollResponse{}, err
	}
	_ = s.store.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: now.UTC(),
		Action:     store.ActivityEnroll,
		SessionID:  session.ID,
		DeviceID:   device.ID,
		ProfileID:  entry.approvedProfile,
		TargetType: "device",
		TargetID:   string(device.ID),
	})
	return EnrollResponse{
		DeviceID:         string(device.ID),
		DeviceSecret:     deviceSecret,
		SessionID:        string(session.ID),
		SessionExpiresAt: sessionExpiresAt.Format(time.RFC3339),
		ProfileID:        string(entry.approvedProfile),
		DeviceRole:       string(device.Role),
		// Pin only when both the request and the collecting poll came from
		// the LAN: a relay in either leg must not seed a pin.
		ServerKeyPin: s.serverKeyPin(ctx, entry.isLAN && req.IsLAN),
	}, nil
}

// ListDeviceRequests returns the pending requests for an admin to approve.
func (s *Service) ListDeviceRequests(_ context.Context, scope auth.Scope) (PendingDeviceRequests, error) {
	if !scope.Admin {
		return PendingDeviceRequests{}, ErrForbidden
	}
	now := s.now()
	t := &s.deviceRequests
	t.mu.Lock()
	t.init()
	t.sweepLocked(now)
	pending := make([]*deviceRequestEntry, 0, len(t.byCode))
	for _, entry := range t.byCode {
		if entry.status == DeviceRequestPending {
			pending = append(pending, entry)
		}
	}
	out := make([]PendingDeviceRequest, 0, len(pending))
	sort.Slice(pending, func(i, j int) bool {
		if !pending[i].requestedAt.Equal(pending[j].requestedAt) {
			return pending[i].requestedAt.Before(pending[j].requestedAt)
		}
		return pending[i].code < pending[j].code
	})
	for _, entry := range pending {
		out = append(out, entry.pendingView())
	}
	t.mu.Unlock()
	return PendingDeviceRequests{Requests: out}, nil
}

func (e *deviceRequestEntry) pendingView() PendingDeviceRequest {
	return PendingDeviceRequest{
		Code:              e.code,
		DeviceName:        e.name,
		Platform:          e.platform,
		RequestedRole:     string(e.role),
		LAN:               e.isLAN,
		DistributionClass: string(e.distributionClass),
		RequestedAt:       e.requestedAt.Format(time.RFC3339),
		ExpiresAt:         e.expiresAt.Format(time.RFC3339),
	}
}

// ApproveDeviceRequest approves the pending request showing req.Code. Only
// an admin may approve, and approval is the only way a device-initiated
// request reaches any role.
func (s *Service) ApproveDeviceRequest(ctx context.Context, scope auth.Scope, req DeviceRequestDecision) (DeviceRequestDecisionResponse, error) {
	if !scope.Admin {
		return DeviceRequestDecisionResponse{}, ErrForbidden
	}
	code := normalizeCode(req.Code)
	if code == "" {
		return DeviceRequestDecisionResponse{}, ErrInvalidRequest
	}
	profileID := store.ProfileID(strings.TrimSpace(req.ProfileID))
	if profileID == "" {
		profileID = scope.ProfileID
	}
	profile, err := s.store.Profile(ctx, profileID)
	if err != nil || profile.Disabled {
		return DeviceRequestDecisionResponse{}, ErrInvalidRequest
	}
	// An approval that names no role grants Viewer. The requested role came
	// from an unauthenticated device, so it is shown to the admin but never
	// granted by default.
	role, err := parseRole(req.DeviceRole)
	if err != nil {
		return DeviceRequestDecisionResponse{}, err
	}

	now := s.now()
	t := &s.deviceRequests
	t.mu.Lock()
	t.init()
	t.sweepLocked(now)
	entry, ok := t.byCode[code]
	if !ok || entry.status != DeviceRequestPending {
		t.mu.Unlock()
		return DeviceRequestDecisionResponse{}, ErrInvalidCode
	}
	t.mu.Unlock()

	// The Viewer switch is read outside the table lock (it is a store read)
	// and the entry is re-checked after, so a concurrent decision wins once.
	if role == store.DeviceRoleViewer {
		if refused, err := s.refuseDisabledViewer(ctx, scope, profileID, viewerCodeApproveRefused); err != nil {
			return DeviceRequestDecisionResponse{}, err
		} else if refused {
			return DeviceRequestDecisionResponse{}, ErrInvalidCode
		}
	}

	t.mu.Lock()
	if current, ok := t.byCode[code]; !ok || current != entry || entry.status != DeviceRequestPending || !now.Before(entry.expiresAt) {
		t.mu.Unlock()
		return DeviceRequestDecisionResponse{}, ErrInvalidCode
	}
	entry.status = DeviceRequestApproved
	entry.approvedProfile = profileID
	entry.approvedRole = role
	t.mu.Unlock()

	// Like a minted code, the approval is a grant for a Profile; the code
	// itself is never logged.
	_ = s.store.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: now.UTC(),
		Action:     store.ActivityPairingCode,
		SessionID:  scope.SessionID,
		DeviceID:   scope.DeviceID,
		ProfileID:  scope.ProfileID,
		TargetType: "profile",
		TargetID:   string(profileID),
	})
	return DeviceRequestDecisionResponse{
		Code:       code,
		Status:     DeviceRequestApproved,
		ProfileID:  string(profileID),
		DeviceRole: string(role),
	}, nil
}

// DenyDeviceRequest denies the pending request showing req.Code. The device's
// next poll learns it was denied, and the request is gone after that.
func (s *Service) DenyDeviceRequest(ctx context.Context, scope auth.Scope, req DeviceRequestDecision) (DeviceRequestDecisionResponse, error) {
	if !scope.Admin {
		return DeviceRequestDecisionResponse{}, ErrForbidden
	}
	code := normalizeCode(req.Code)
	if code == "" {
		return DeviceRequestDecisionResponse{}, ErrInvalidRequest
	}
	now := s.now()
	t := &s.deviceRequests
	t.mu.Lock()
	t.init()
	t.sweepLocked(now)
	entry, ok := t.byCode[code]
	if !ok || entry.status != DeviceRequestPending {
		t.mu.Unlock()
		return DeviceRequestDecisionResponse{}, ErrInvalidCode
	}
	entry.status = DeviceRequestDenied
	t.mu.Unlock()

	_ = s.store.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: now.UTC(),
		Action:     store.ActivityEnrollDenied,
		SessionID:  scope.SessionID,
		DeviceID:   scope.DeviceID,
		ProfileID:  scope.ProfileID,
		TargetType: deviceRequestTarget,
		TargetID:   "denied",
	})
	return DeviceRequestDecisionResponse{Code: code, Status: DeviceRequestDenied}, nil
}

// deviceRequestThrottle applies the pairing-request throttle's source-address
// lane, with the enroll backoff schedule.
func (s *Service) deviceRequestThrottle(ctx context.Context, source string, now time.Time) error {
	throttled, err := s.throttledFor(ctx, deviceRequestAttemptAction, source, "", now)
	if err != nil {
		return err
	}
	if throttled {
		return ErrThrottled
	}
	return nil
}

// recordDeviceRequestFailure spends one attempt on the source lane. Request
// and poll never record a success: a success resets the lane, and a device
// polling a live request must not wipe the failures of guesses from the same
// source.
func (s *Service) recordDeviceRequestFailure(ctx context.Context, source string, now time.Time) error {
	return s.recordAttemptFor(ctx, deviceRequestAttemptAction, source, "", now, false)
}

// refuseDeviceRequest records a failed attempt and an enroll.denied entry
// with no actor and no code.
func (s *Service) refuseDeviceRequest(ctx context.Context, source string, now time.Time) {
	_ = s.recordDeviceRequestFailure(ctx, source, now)
	s.logDenied(ctx, now, store.ActivityEnrollDenied)
}

// refuseDisabledViewer reports whether Viewer pairing codes are switched off,
// and records the refusal of step when they are.
func (s *Service) refuseDisabledViewer(ctx context.Context, scope auth.Scope, profileID store.ProfileID, step string) (bool, error) {
	disabled, err := s.viewerCodesDisabled(ctx)
	if err != nil || !disabled {
		return false, err
	}
	_ = s.store.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: s.now().UTC(),
		Action:     store.ActivityEnrollDenied,
		SessionID:  scope.SessionID,
		DeviceID:   scope.DeviceID,
		ProfileID:  profileID,
		TargetType: viewerCodePolicyTarget,
		TargetID:   step,
	})
	return true, nil
}

// untrustedLabel trims a device-supplied label and refuses one that is empty,
// longer than maxRunes, not UTF-8, or carries control or format characters
// (which could spoof what the admin reads in the pending list).
func untrustedLabel(value string, maxRunes int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return "", ErrInvalidRequest
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", ErrInvalidRequest
		}
	}
	return value, nil
}

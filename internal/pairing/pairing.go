// Package pairing owns pairing-code enrollment for new devices.
package pairing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

var (
	// ErrForbidden means the current session cannot mint pairing codes.
	ErrForbidden = errors.New("pairing forbidden")
	// ErrInvalidCode means a code is unknown, expired, or already consumed.
	ErrInvalidCode = errors.New("pairing code invalid")
	// ErrInvalidRequest means the request body is missing required fields.
	ErrInvalidRequest = errors.New("pairing request invalid")
	// ErrThrottled means enrollment attempts exceeded the short-window limit.
	ErrThrottled = errors.New("pairing attempts throttled")
	// ErrRoleNotGranted means the pairing code does not grant the manager role.
	ErrRoleNotGranted = errors.New("pairing code does not grant the manager role")
	// ErrSetupTokenInvalid means the presented setup token is wrong, already
	// consumed, or setup is already complete.
	ErrSetupTokenInvalid = errors.New("setup token invalid")
)

const (
	enrollAttemptAction = "enroll"
	setupAttemptAction  = "setup"
	enrollAttemptWindow = 10 * time.Minute
	minDemoKeyLength    = 16
)

// enrollBackoff is the pairing lane's exponential-backoff curve:
// the lockout grows with consecutive failed enrollments and resets on a
// successful redeem. BaseDelay doubles per failure up to MaxDelay, so a cheap
// 10,000-call brute force is throttled, not merely counted.
//
// There is deliberately NO server-wide enrollment lane: a shared lane spent by
// unauthenticated failures let ~25 junk requests from anywhere deny every
// valid redemption for the whole server, repeatably.
// The per-code and per-source lanes
// below already throttle same-code and same-source brute force, and the
// 40-bit code space with a 10-minute expiry makes cross-source distributed
// guessing a non-threat.
var enrollBackoff = store.AuthBackoff{
	Window:       enrollAttemptWindow,
	FreeAttempts: 5,
	BaseDelay:    2 * time.Second,
	MaxDelay:     enrollAttemptWindow,
}

// CodeRequest asks the server to mint a pairing code for one device enrollment.
type CodeRequest struct {
	ProfileID  string `json:"profileID,omitempty"`
	DeviceRole string `json:"deviceRole,omitempty"`
}

// CodeResponse is the pairing code shown by a manager surface.
type CodeResponse struct {
	Code       string `json:"code"`
	ExpiresAt  string `json:"expiresAt"`
	ProfileID  string `json:"profileID"`
	DeviceRole string `json:"deviceRole"`
}

// EnrollRequest redeems a pairing code from the new device.
type EnrollRequest struct {
	Code              string `json:"code"`
	DeviceName        string `json:"deviceName"`
	DistributionClass string `json:"distributionClass,omitempty"`
	RemoteAddr        string `json:"-"`
	// IsLAN reports that this enrollment arrived from the local network (no
	// relay in the path). Only a LAN enrollment captures the server's SPKI pin
	// — a remote enrollment must never (re-)pin, because a
	// compromised relay terminating TLS is exactly the impostor the pin defends
	// against. Set by the server layer from the request's source address.
	IsLAN bool `json:"-"`
}

// EnrollResponse gives the new device its first server-side session.
type EnrollResponse struct {
	DeviceID         string `json:"deviceID"`
	DeviceSecret     string `json:"deviceSecret"`
	SessionID        string `json:"sessionID"`
	SessionExpiresAt string `json:"sessionExpiresAt"`
	ProfileID        string `json:"profileID"`
	DeviceRole       string `json:"deviceRole"`
	// ServerKeyPin is the home server's TLS leaf SPKI pin (HPKP form
	// "sha256-...") for a LAN-enrolling native client to trust-on-first-use.
	// Empty when the enrollment is remote (never re-pin over a
	// relay) or when no served certificate exists yet to pin.
	ServerKeyPin string `json:"serverKeyPin,omitempty"`
}

// SetupRequest redeems the one-time setup token and creates the first MANAGER
// device+session directly, bypassing the normal pairing-code flow. This is the
// only escape from the circular dependency on a fresh server:
// no MANAGER exists to mint a code, so the setup token is a one-shot bootstrap.
type SetupRequest struct {
	Token             string `json:"token"`
	DeviceName        string `json:"deviceName"`
	ProfileName       string `json:"profileName"`
	DistributionClass string `json:"distributionClass,omitempty"`
	// AdminPassword, when non-empty, sets the server admin password in-browser
	// during first-run setup. The setup token gates this: it is
	// proof of server/log access, so setting the password here does not weaken
	// the posture versus the CLI reset-admin-password path. Empty leaves the
	// existing flow unchanged (backward compatible). Never logged or echoed.
	AdminPassword string `json:"adminPassword"`
	RemoteAddr    string `json:"-"`
}

// SetupResponse gives the browser its first MANAGER session. The shape is
// identical to EnrollResponse so the web UI can reuse the same cookie-setting
// path in the server layer.
type SetupResponse = EnrollResponse

// SPKIProvider supplies the SPKI pin of the live TLS keypair the server is
// actually serving. It is nil on a LAN-only server with no
// direct-HTTPS identity; when set but no certificate exists yet it returns a
// non-nil error, which the enroll path treats as "no pin available".
type SPKIProvider interface {
	ServerSPKIPin(ctx context.Context) (string, error)
}

// DemoEnrollment is the opt-in App Review reusable enrollment grant. The key
// is loaded from a 0600 secret file by app.New and kept in memory only.
type DemoEnrollment struct {
	Key       string
	ProfileID store.ProfileID
}

// Service owns short-lived, one-time pairing codes.
type Service struct {
	store          *store.Store
	now            func() time.Time
	generateCode   func() (string, error)
	generateID     func(string) (string, error)
	generateSecret func() (string, error)

	// spki supplies the server's SPKI pin captured at LAN enrollment.
	// nil means no direct-HTTPS identity is configured, so no pin
	// is offered. Set once at wiring time, read-only thereafter.
	spki SPKIProvider

	// bootstrapToken is the one-time setup token generated at startup. A zero
	// value means the token has been consumed or was never set. Guarded by mu.
	mu                  sync.Mutex
	bootstrapToken      string
	bootstrapInProgress bool
	demo                DemoEnrollment
}

// NewService creates a pairing service.
func NewService(st *store.Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{
		store:          st,
		now:            now,
		generateCode:   randomCode,
		generateID:     randomID,
		generateSecret: randomSecret,
	}
}

// SetSPKIProvider wires the source of the server's SPKI pin. It
// is called once at startup when a direct-HTTPS identity exists; left unset on a
// LAN-only server, in which case enrollment offers no pin.
func (s *Service) SetSPKIProvider(p SPKIProvider) {
	s.spki = p
}

// SetDemoEnrollment installs the App Review reusable VIEWER enrollment grant.
// A blank key disables the grant. The key is normalized the same way a reviewer
// enters it, but is never persisted by this service.
func (s *Service) SetDemoEnrollment(grant DemoEnrollment) error {
	key := normalizeCode(grant.Key)
	if key == "" {
		s.mu.Lock()
		s.demo = DemoEnrollment{}
		s.mu.Unlock()
		return nil
	}
	if len(key) < minDemoKeyLength {
		return ErrInvalidRequest
	}
	if grant.ProfileID == "" {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	s.demo = DemoEnrollment{
		Key:       key,
		ProfileID: grant.ProfileID,
	}
	s.mu.Unlock()
	return nil
}

// SetBootstrapToken installs the one-time first-run setup token. It is called
// once at startup when the server detects an unconfigured state. Calling it a
// second time replaces the token (e.g. if startup re-checks).
func (s *Service) SetBootstrapToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bootstrapToken = token
}

// SetupRequired reports whether the server is still awaiting first-run setup:
// true iff the one-time bootstrap setup token is still installed (setup not yet
// completed via the web bootstrap). It becomes false once the token has been
// consumed by a successful Bootstrap. Safe to call from an unenrolled browser;
// it leaks only this single boolean and never the token value itself.
func (s *Service) SetupRequired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bootstrapToken != ""
}

// Bootstrap redeems the one-time setup token, creates the first Profile and
// first MANAGER device+session, and clears the token so it is single-use.
// It refuses if no token is installed (setup already completed or token never
// set) or if the presented token does not match in constant time.
func (s *Service) Bootstrap(ctx context.Context, req SetupRequest) (SetupResponse, error) {
	name := strings.TrimSpace(req.DeviceName)
	profileName := strings.TrimSpace(req.ProfileName)
	if profileName == "" {
		profileName = "Owner"
	}
	token := strings.TrimSpace(req.Token)
	if token == "" || name == "" {
		return SetupResponse{}, ErrInvalidRequest
	}
	distributionClass, err := store.NormalizeDeviceDistributionClass(store.DeviceDistributionClass(req.DistributionClass))
	if err != nil {
		return SetupResponse{}, ErrInvalidRequest
	}

	now := s.now()
	remoteKey := remoteAttemptKey(req.RemoteAddr)
	attemptKey := setupAttemptKey()
	if throttled, err := s.throttledFor(ctx, setupAttemptAction, attemptKey, remoteKey, now); err != nil {
		return SetupResponse{}, err
	} else if throttled {
		return SetupResponse{}, ErrThrottled
	}

	s.mu.Lock()
	stored := s.bootstrapToken
	if stored == "" || s.bootstrapInProgress {
		s.mu.Unlock()
		_ = s.recordAttemptFor(ctx, setupAttemptAction, attemptKey, remoteKey, now, false)
		return SetupResponse{}, ErrSetupTokenInvalid
	}
	// Constant-time comparison to resist timing attacks on the token.
	if subtle.ConstantTimeCompare([]byte(token), []byte(stored)) != 1 {
		s.mu.Unlock()
		_ = s.recordAttemptFor(ctx, setupAttemptAction, attemptKey, remoteKey, now, false)
		return SetupResponse{}, ErrSetupTokenInvalid
	}
	s.bootstrapInProgress = true
	s.mu.Unlock()
	bootstrapSucceeded := false
	defer func() {
		s.mu.Lock()
		if bootstrapSucceeded && s.bootstrapToken == stored {
			s.bootstrapToken = ""
		}
		s.bootstrapInProgress = false
		s.mu.Unlock()
	}()

	// Set the server admin password in-browser, gated by the now-validated
	// setup token. This is the same argon2id store write the CLI
	// reset-admin-password uses. An invalid/used token never reaches this point.
	// Empty AdminPassword leaves the existing flow unchanged (backward compat);
	// the plaintext stays inside store.SetAdminPassword and is never logged.
	if strings.TrimSpace(req.AdminPassword) != "" {
		if err := s.store.SetAdminPassword(ctx, req.AdminPassword, now); err != nil {
			return SetupResponse{}, err
		}
	}
	deviceID, err := s.generateID("device")
	if err != nil {
		return SetupResponse{}, err
	}
	sessionID, err := s.generateID("session")
	if err != nil {
		return SetupResponse{}, err
	}
	deviceSecret, err := s.generateSecret()
	if err != nil {
		return SetupResponse{}, err
	}

	// Create the first owner Profile so the session has a valid binding. The
	// store's CreateProfile caller is the bootstrap itself (no creator scope
	// exists yet), so we pass a fully-open filter — there are no Libraries yet
	// on a truly fresh server, so the granted set is empty but valid.
	profile, err := s.store.CreateProfile(ctx, profileName, store.ProfileAdult, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		return SetupResponse{}, err
	}
	// First-run designates this bootstrap Profile as the single owner (ADR-0007
	// §2/§4): the only Profile that may later open an Admin Session. Movable
	// afterwards via SetProfileOwner.
	if err := s.store.SetProfileOwner(ctx, profile.ID); err != nil {
		return SetupResponse{}, err
	}

	sessionExpiresAt := now.Add(30 * 24 * time.Hour).UTC()
	device := store.Device{
		ID:                store.DeviceID(deviceID),
		Name:              name,
		Role:              store.DeviceRoleManager,
		DistributionClass: distributionClass,
		SecretHash:        store.HashDeviceSecret(deviceSecret),
	}
	session := store.Session{
		ID:        store.SessionID(sessionID),
		DeviceID:  device.ID,
		ProfileID: profile.ID,
		ExpiresAt: sessionExpiresAt,
	}
	if err := s.store.EnrollDevice(ctx, device, session); err != nil {
		return SetupResponse{}, err
	}
	_ = s.store.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: now.UTC(),
		Action:     store.ActivityEnroll,
		SessionID:  session.ID,
		DeviceID:   device.ID,
		ProfileID:  profile.ID,
		TargetType: "device",
		TargetID:   string(device.ID),
	})
	_ = s.recordAttemptFor(ctx, setupAttemptAction, attemptKey, remoteKey, now, true)
	bootstrapSucceeded = true
	return SetupResponse{
		DeviceID:         string(device.ID),
		DeviceSecret:     deviceSecret,
		SessionID:        string(session.ID),
		SessionExpiresAt: sessionExpiresAt.Format(time.RFC3339),
		ProfileID:        string(profile.ID),
		DeviceRole:       string(store.DeviceRoleManager),
	}, nil
}

// CreateCode mints a one-time code. Pairing-code issuance is Console/admin
// authority, not merely a manager-role device capability.
func (s *Service) CreateCode(ctx context.Context, scope auth.Scope, req CodeRequest) (CodeResponse, error) {
	if !scope.Admin {
		return CodeResponse{}, ErrForbidden
	}

	profileID := store.ProfileID(strings.TrimSpace(req.ProfileID))
	if profileID == "" {
		profileID = scope.ProfileID
	}
	if _, err := s.store.Profile(ctx, profileID); err != nil {
		return CodeResponse{}, ErrInvalidRequest
	}

	role, err := parseRole(req.DeviceRole)
	if err != nil {
		return CodeResponse{}, err
	}
	expiresAt := s.now().Add(10 * time.Minute).UTC()

	for range 4 {
		code, err := s.generateCode()
		if err != nil {
			return CodeResponse{}, err
		}
		code = normalizeCode(code)
		if code == "" {
			return CodeResponse{}, ErrInvalidRequest
		}

		err = s.store.CreatePairingCode(ctx, store.PairingCode{
			Code:       code,
			ProfileID:  profileID,
			DeviceRole: role,
			ExpiresAt:  expiresAt,
		})
		if err == nil {
			// ADR-0012 §11: log that a code was minted and for which
			// Profile — never the code itself (it is a credential until
			// consumed or expired).
			_ = s.store.AppendActivity(ctx, store.ActivityEntry{
				OccurredAt: s.now().UTC(),
				Action:     store.ActivityPairingCode,
				SessionID:  scope.SessionID,
				DeviceID:   scope.DeviceID,
				ProfileID:  scope.ProfileID,
				TargetType: "profile",
				TargetID:   string(profileID),
			})
			return CodeResponse{
				Code:       code,
				ExpiresAt:  expiresAt.Format(time.RFC3339),
				ProfileID:  string(profileID),
				DeviceRole: string(role),
			}, nil
		}
		if !errors.Is(err, store.ErrPairingCodeExists) {
			return CodeResponse{}, err
		}
	}
	return CodeResponse{}, errors.New("could not mint unique pairing code")
}

// Enroll consumes a one-time code and creates a hidden session for the new device.
func (s *Service) Enroll(ctx context.Context, req EnrollRequest) (EnrollResponse, error) {
	code := normalizeCode(req.Code)
	name := strings.TrimSpace(req.DeviceName)
	if code == "" || name == "" {
		return EnrollResponse{}, ErrInvalidRequest
	}
	distributionClass, err := store.NormalizeDeviceDistributionClass(store.DeviceDistributionClass(req.DistributionClass))
	if err != nil {
		return EnrollResponse{}, ErrInvalidRequest
	}

	now := s.now()
	remoteKey := remoteAttemptKey(req.RemoteAddr)
	if grant, ok := s.demoEnrollment(code); ok {
		attemptKey := demoAttemptKey(code)
		if throttled, err := s.throttled(ctx, attemptKey, remoteKey, now); err != nil {
			return EnrollResponse{}, err
		} else if throttled {
			return EnrollResponse{}, ErrThrottled
		}
		return s.enrollDemo(ctx, req, grant.ProfileID, now, attemptKey, remoteKey)
	}
	attemptKey := codeAttemptKey(code)
	if throttled, err := s.throttled(ctx, attemptKey, remoteKey, now); err != nil {
		return EnrollResponse{}, err
	} else if throttled {
		return EnrollResponse{}, ErrThrottled
	}
	pending, err := s.store.PairingCode(ctx, code, now)
	if err != nil {
		_ = s.recordAttempt(ctx, attemptKey, remoteKey, now, false)
		s.logDenied(ctx, now, store.ActivityEnrollDenied)
		if errors.Is(err, store.ErrPairingCodeInvalid) {
			return EnrollResponse{}, ErrInvalidCode
		}
		return EnrollResponse{}, err
	}
	// Remote-enrollment gate (ADR-0012 amendment 3 / ADR-0081). Validate the
	// code first so off-LAN callers get the same invalid-code answer for bad
	// guesses whether the operator disabled remote enrollment or not.
	if !req.IsLAN {
		cfg, err := s.store.ConsoleConfig(ctx)
		if err != nil {
			return EnrollResponse{}, err
		}
		if !cfg.RemoteEnrollmentEnabled {
			_ = s.recordAttempt(ctx, attemptKey, remoteKey, now, false)
			s.logDenied(ctx, now, store.ActivityEnrollDenied)
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
		Name:              name,
		Role:              pending.DeviceRole,
		DistributionClass: distributionClass,
		SecretHash:        store.HashDeviceSecret(deviceSecret),
	}
	session := store.Session{
		ID:        store.SessionID(sessionID),
		DeviceID:  device.ID,
		ExpiresAt: sessionExpiresAt,
	}
	pending, err = s.store.RedeemPairingCode(ctx, code, now, device, session)
	if err != nil {
		_ = s.recordAttempt(ctx, attemptKey, remoteKey, now, false)
		s.logDenied(ctx, now, store.ActivityEnrollDenied)
		if errors.Is(err, store.ErrPairingCodeInvalid) || errors.Is(err, store.ErrProfileDisabled) {
			return EnrollResponse{}, ErrInvalidCode
		}
		return EnrollResponse{}, err
	}
	_ = s.recordAttempt(ctx, attemptKey, remoteKey, now, true)
	_ = s.store.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: now.UTC(),
		Action:     store.ActivityEnroll,
		SessionID:  session.ID,
		DeviceID:   device.ID,
		ProfileID:  pending.ProfileID,
		TargetType: "device",
		TargetID:   string(device.ID),
	})
	return EnrollResponse{
		DeviceID:         string(device.ID),
		DeviceSecret:     deviceSecret,
		SessionID:        string(session.ID),
		SessionExpiresAt: sessionExpiresAt.Format(time.RFC3339),
		ProfileID:        string(pending.ProfileID),
		DeviceRole:       string(device.Role),
		ServerKeyPin:     s.serverKeyPin(ctx, req.IsLAN),
	}, nil
}

func (s *Service) demoEnrollment(code string) (DemoEnrollment, bool) {
	s.mu.Lock()
	grant := s.demo
	s.mu.Unlock()
	if grant.Key == "" || subtle.ConstantTimeCompare([]byte(code), []byte(grant.Key)) != 1 {
		return DemoEnrollment{}, false
	}
	return grant, true
}

func (s *Service) enrollDemo(ctx context.Context, req EnrollRequest, profileID store.ProfileID, now time.Time, attemptKey string, remoteKey string) (EnrollResponse, error) {
	distributionClass, err := store.NormalizeDeviceDistributionClass(store.DeviceDistributionClass(req.DistributionClass))
	if err != nil {
		return EnrollResponse{}, ErrInvalidRequest
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
		Name:              strings.TrimSpace(req.DeviceName),
		Role:              store.DeviceRoleViewer,
		DistributionClass: distributionClass,
		SecretHash:        store.HashDeviceSecret(deviceSecret),
	}
	session := store.Session{
		ID:        store.SessionID(sessionID),
		DeviceID:  device.ID,
		ProfileID: profileID,
		ExpiresAt: sessionExpiresAt,
	}
	if err := s.store.EnrollDevice(ctx, device, session); err != nil {
		_ = s.recordAttempt(ctx, attemptKey, remoteKey, now, false)
		s.logDenied(ctx, now, store.ActivityEnrollDenied)
		if errors.Is(err, store.ErrProfileDisabled) || strings.Contains(err.Error(), "profile not found") {
			return EnrollResponse{}, ErrInvalidCode
		}
		return EnrollResponse{}, err
	}
	_ = s.recordAttempt(ctx, attemptKey, remoteKey, now, true)
	_ = s.store.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: now.UTC(),
		Action:     store.ActivityEnroll,
		SessionID:  session.ID,
		DeviceID:   device.ID,
		ProfileID:  profileID,
		TargetType: "device",
		TargetID:   string(device.ID),
	})
	return EnrollResponse{
		DeviceID:         string(device.ID),
		DeviceSecret:     deviceSecret,
		SessionID:        string(session.ID),
		SessionExpiresAt: sessionExpiresAt.Format(time.RFC3339),
		ProfileID:        string(profileID),
		DeviceRole:       string(store.DeviceRoleViewer),
		ServerKeyPin:     s.serverKeyPin(ctx, req.IsLAN),
	}, nil
}

// serverKeyPin returns the server's SPKI pin to hand a LAN-enrolling client,
// or "" when this is a remote enrollment, no SPKI provider is
// wired, or no served certificate exists yet. A remote enrollment must NOT
// (re-)pin: only the local-network moment establishes the trust-on-first-use
// pin, so a relay terminating TLS can never seed an impostor pin.
func (s *Service) serverKeyPin(ctx context.Context, isLAN bool) string {
	if !isLAN || s.spki == nil {
		return ""
	}
	pin, err := s.spki.ServerSPKIPin(ctx)
	if err != nil {
		return ""
	}
	return pin
}

// PromoteRequest redeems a manager-role pairing code for an enrolled device.
type PromoteRequest struct {
	Code       string `json:"code"`
	RemoteAddr string `json:"-"`
}

// PromoteResponse confirms the device's upgraded role.
type PromoteResponse struct {
	DeviceID         string `json:"deviceID"`
	DeviceRole       string `json:"deviceRole"`
	DeviceSecret     string `json:"-"`
	SessionID        string `json:"-"`
	SessionExpiresAt string `json:"-"`
}

// Promote upgrades the calling session's already-enrolled device to MANAGER
// by redeeming a manager-role pairing code — the same
// pairing-codes-only credential as enrollment, minted only from
// a manager surface, so the promotion is a deliberate per-device grant. The
// device keeps its identity but rotates both session and device secret in the
// same write as the role change. Codes share the enrollment throttle budget —
// promotion is not a second brute-force lane.
func (s *Service) Promote(ctx context.Context, scope auth.Scope, req PromoteRequest) (PromoteResponse, error) {
	code := normalizeCode(req.Code)
	if code == "" || scope.DeviceID == "" || scope.SessionID == "" || scope.ProfileID == "" {
		return PromoteResponse{}, ErrInvalidRequest
	}

	now := s.now()
	remoteKey := remoteAttemptKey(req.RemoteAddr)
	attemptKey := codeAttemptKey(code)
	if throttled, err := s.throttled(ctx, attemptKey, remoteKey, now); err != nil {
		return PromoteResponse{}, err
	} else if throttled {
		return PromoteResponse{}, ErrThrottled
	}
	fail := func(err error) (PromoteResponse, error) {
		_ = s.recordAttempt(ctx, attemptKey, remoteKey, now, false)
		_ = s.store.AppendActivity(ctx, store.ActivityEntry{
			OccurredAt: now.UTC(),
			Action:     store.ActivityPromoteDenied,
			SessionID:  scope.SessionID,
			DeviceID:   scope.DeviceID,
			ProfileID:  scope.ProfileID,
		})
		switch {
		case errors.Is(err, store.ErrPairingCodeInvalid):
			return PromoteResponse{}, ErrInvalidCode
		case errors.Is(err, store.ErrPairingCodeRoleMismatch):
			return PromoteResponse{}, ErrRoleNotGranted
		}
		return PromoteResponse{}, err
	}
	pending, err := s.store.PairingCode(ctx, code, now)
	if err != nil {
		return fail(err)
	}
	if pending.DeviceRole != store.DeviceRoleManager {
		return fail(store.ErrPairingCodeRoleMismatch)
	}
	sessionID, err := s.generateID("session")
	if err != nil {
		return PromoteResponse{}, err
	}
	deviceSecret, err := s.generateSecret()
	if err != nil {
		return PromoteResponse{}, err
	}
	sessionExpiresAt := now.Add(30 * 24 * time.Hour).UTC()
	_, err = s.store.RedeemPairingCodeForDevice(ctx, code, now, scope.DeviceID, store.DeviceRoleManager, scope.SessionID, store.Session{
		ID:        store.SessionID(sessionID),
		DeviceID:  scope.DeviceID,
		ProfileID: scope.ProfileID,
		ExpiresAt: sessionExpiresAt,
	}, store.HashDeviceSecret(deviceSecret))
	if err != nil {
		return fail(err)
	}
	_ = s.recordAttempt(ctx, attemptKey, remoteKey, now, true)
	_ = s.store.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: now.UTC(),
		Action:     store.ActivityPromote,
		SessionID:  scope.SessionID,
		DeviceID:   scope.DeviceID,
		ProfileID:  scope.ProfileID,
		TargetType: "device",
		TargetID:   string(scope.DeviceID),
	})
	return PromoteResponse{
		DeviceID:         string(scope.DeviceID),
		DeviceRole:       string(store.DeviceRoleManager),
		DeviceSecret:     deviceSecret,
		SessionID:        sessionID,
		SessionExpiresAt: sessionExpiresAt.Format(time.RFC3339),
	}, nil
}

func (s *Service) throttled(ctx context.Context, attemptKey string, remoteKey string, now time.Time) (bool, error) {
	return s.throttledFor(ctx, enrollAttemptAction, attemptKey, remoteKey, now)
}

func (s *Service) throttledFor(ctx context.Context, action, attemptKey string, remoteKey string, now time.Time) (bool, error) {
	until, err := s.store.AuthBackoffUntil(ctx, action, attemptKey, now, enrollBackoff)
	if err != nil {
		return false, err
	}
	if now.Before(until) {
		return true, nil
	}
	if remoteKey == "" {
		return false, nil
	}
	until, err = s.store.AuthBackoffUntil(ctx, action, remoteKey, now, enrollBackoff)
	if err != nil {
		return false, err
	}
	return now.Before(until), nil
}

// logDenied appends a failed-enrollment activity entry (ADR-0012 §3 audit
// events for failures). The caller is unauthenticated, so the entry carries
// no actor identifiers — and never the attempted code, which could be a
// near-miss of a live credential.
func (s *Service) logDenied(ctx context.Context, now time.Time, action string) {
	_ = s.store.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: now.UTC(),
		Action:     action,
	})
}

func (s *Service) recordAttempt(ctx context.Context, attemptKey string, remoteKey string, now time.Time, success bool) error {
	return s.recordAttemptFor(ctx, enrollAttemptAction, attemptKey, remoteKey, now, success)
}

func (s *Service) recordAttemptFor(ctx context.Context, action, attemptKey string, remoteKey string, now time.Time, success bool) error {
	if err := s.store.RecordAuthAttempt(ctx, store.AuthAttempt{
		OccurredAt: now.UTC(),
		Action:     action,
		Key:        attemptKey,
		Success:    success,
	}); err != nil {
		return err
	}
	if remoteKey == "" {
		return nil
	}
	return s.store.RecordAuthAttempt(ctx, store.AuthAttempt{
		OccurredAt: now.UTC(),
		Action:     action,
		Key:        remoteKey,
		Success:    success,
	})
}

func parseRole(value string) (store.DeviceRole, error) {
	switch store.DeviceRole(strings.TrimSpace(value)) {
	case "":
		return store.DeviceRoleViewer, nil
	case store.DeviceRoleViewer:
		return store.DeviceRoleViewer, nil
	case store.DeviceRoleManager:
		return store.DeviceRoleManager, nil
	default:
		return "", ErrInvalidRequest
	}
}

func normalizeCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, " ", "")
	code = strings.ReplaceAll(code, "-", "")
	return code
}

func codeAttemptKey(code string) string {
	sum := sha256.Sum256([]byte("pairing-code\x00" + code))
	return "code:" + hex.EncodeToString(sum[:])
}

func demoAttemptKey(code string) string {
	sum := sha256.Sum256([]byte("review-demo-enrollment\x00" + code))
	return "review-demo:" + hex.EncodeToString(sum[:])
}

func setupAttemptKey() string {
	return "bootstrap"
}

func remoteAttemptKey(remoteAddr string) string {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		remoteAddr = host
	}
	if ip := net.ParseIP(remoteAddr); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			remoteAddr = ip4.String()
		} else if masked := ip.Mask(net.CIDRMask(64, 128)); masked != nil {
			remoteAddr = masked.String() + "/64"
		}
	}
	return "remote:" + remoteAddr
}

func randomCode() (string, error) {
	var data [5]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(data[:]), nil
}

func randomID(prefix string) (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(data[:]), nil
}

func randomSecret() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data[:]), nil
}

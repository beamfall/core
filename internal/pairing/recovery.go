package pairing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Sign-in recovery for an owner who has lost every working session
// (CORE-1106): a browser renews its session from its device credential, and
// the owner can enroll a new Manager Device with the admin password.

var (
	// ErrSessionRenewRefused means the device credential cannot renew a session.
	ErrSessionRenewRefused = errors.New("session renewal refused")
	// ErrSignInFailed is the single answer to every refused admin-password
	// sign-in, so a caller cannot tell a wrong password from no password or
	// no owner.
	ErrSignInFailed = errors.New("sign-in failed")
)

const (
	renewAttemptAction       = "session.renew"
	adminSignInAttemptAction = "admin.signin"
	// adminSignInGlobalKey is the server-wide admin sign-in lane. Unlike the
	// enroll and setup lanes, the admin password is human-chosen, so
	// distributed guessing across sources must be throttled too. The cost is
	// that a remote guesser can delay the owner's own sign-in; the pairing
	// code paths stay open.
	adminSignInGlobalKey = "global"
)

// Refusal wraps a recovery refusal with the closed-vocabulary reason the
// server notes in its audit record.
type Refusal struct {
	Err    error
	Reason auth.RefusalReason
}

func (r *Refusal) Error() string { return r.Err.Error() }

// Unwrap exposes the outcome error (ErrSessionRenewRefused, ErrSignInFailed
// or ErrThrottled) to errors.Is.
func (r *Refusal) Unwrap() error { return r.Err }

func refuse(err error, reason auth.RefusalReason) error {
	return &Refusal{Err: err, Reason: reason}
}

// RenewRequest carries a browser's device credential cookies. OldSessionID is
// the session cookie's value, which may name an expired or deleted session.
type RenewRequest struct {
	DeviceID     string
	DeviceSecret string
	OldSessionID string
	RemoteAddr   string
}

// RenewResponse describes the new session. The device secret is unchanged.
type RenewResponse struct {
	DeviceID         string `json:"deviceID"`
	SessionID        string `json:"-"`
	SessionExpiresAt string `json:"sessionExpiresAt"`
	ProfileID        string `json:"profileID"`
	DeviceRole       string `json:"deviceRole"`
}

// RenewSession mints a new 30-day session for a device that proves its
// device secret, keeping the Profile of the session it replaces. The old
// session's expiry does not matter: the device secret is the long-lived
// credential, and revoking the device still ends it.
func (s *Service) RenewSession(ctx context.Context, req RenewRequest) (RenewResponse, error) {
	deviceID := store.DeviceID(strings.TrimSpace(req.DeviceID))
	if deviceID == "" || req.DeviceSecret == "" {
		// Not a guess, so not counted: a signed-out browser asks on every load.
		return RenewResponse{}, refuse(ErrSessionRenewRefused, auth.RefusalCredentialIncomplete)
	}
	now := s.now()
	key := setupAttemptKey(req.RemoteAddr)
	if throttled, err := s.throttledFor(ctx, renewAttemptAction, key, "", now); err != nil {
		return RenewResponse{}, err
	} else if throttled {
		return RenewResponse{}, refuse(ErrThrottled, auth.RefusalPairingThrottled)
	}
	fail := func(reason auth.RefusalReason) (RenewResponse, error) {
		_ = s.recordAttemptFor(ctx, renewAttemptAction, key, "", now, false)
		return RenewResponse{}, refuse(ErrSessionRenewRefused, reason)
	}
	device, err := s.store.VerifyDeviceSecret(ctx, deviceID, req.DeviceSecret)
	if err != nil {
		return fail(auth.RefusalDeviceSecretInvalid)
	}
	if device.Revoked {
		return fail(auth.RefusalDeviceRevoked)
	}
	oldID := store.SessionID(strings.TrimSpace(req.OldSessionID))
	var profileID store.ProfileID
	if oldID != "" {
		if old, err := s.store.Session(ctx, oldID); err == nil {
			if old.Kind != store.SessionKindEnrolled {
				return fail(auth.RefusalIntegrationRestricted)
			}
			if old.DeviceID == device.ID {
				profileID = old.ProfileID
			}
		}
	}
	if profileID == "" {
		latest, err := s.store.LatestDeviceSession(ctx, device.ID)
		if errors.Is(err, store.ErrNoDeviceSession) {
			return fail(auth.RefusalSessionInvalid)
		}
		if err != nil {
			return RenewResponse{}, err
		}
		profileID = latest.ProfileID
	}
	sessionID, err := s.generateID("session")
	if err != nil {
		return RenewResponse{}, err
	}
	expiresAt := now.Add(30 * 24 * time.Hour).UTC()
	err = s.store.RenewDeviceSession(ctx, oldID, store.Session{
		ID:        store.SessionID(sessionID),
		DeviceID:  device.ID,
		ProfileID: profileID,
		ExpiresAt: expiresAt,
	})
	switch {
	case errors.Is(err, store.ErrDeviceRevoked):
		return fail(auth.RefusalDeviceRevoked)
	case errors.Is(err, store.ErrProfileDisabled):
		return fail(auth.RefusalProfileUnavailable)
	case err != nil:
		return RenewResponse{}, err
	}
	_ = s.recordAttemptFor(ctx, renewAttemptAction, key, "", now, true)
	return RenewResponse{
		DeviceID:         string(device.ID),
		SessionID:        sessionID,
		SessionExpiresAt: expiresAt.Format(time.RFC3339),
		ProfileID:        string(profileID),
		DeviceRole:       string(device.Role),
	}, nil
}

// AdminSignInRequest signs a browser in with the server admin password.
type AdminSignInRequest struct {
	Password   string `json:"password"`
	DeviceName string `json:"deviceName"`
	RemoteAddr string `json:"-"`
}

// AdminSignIn enrolls a new Manager Device with a 30-day session on the owner
// Profile when the admin password verifies and the sign-in security mode is
// Home. Attempts are throttled on the admin re-auth curve per source and
// server-wide. Every refusal other than a throttle is ErrSignInFailed.
func (s *Service) AdminSignIn(ctx context.Context, req AdminSignInRequest) (EnrollResponse, error) {
	name := strings.TrimSpace(req.DeviceName)
	if name == "" || req.Password == "" {
		return EnrollResponse{}, ErrInvalidRequest
	}
	now := s.now()
	remoteKey := setupAttemptKey(req.RemoteAddr)
	if throttled, err := s.adminSignInThrottled(ctx, remoteKey, now); err != nil {
		return EnrollResponse{}, err
	} else if throttled {
		return EnrollResponse{}, refuse(ErrThrottled, auth.RefusalAdminLocked)
	}
	// deny refuses an attempt CheckAdminPassword has already counted.
	deny := func() (EnrollResponse, error) {
		s.logDenied(ctx, now, store.ActivityAdminLoginDenied)
		return EnrollResponse{}, refuse(ErrSignInFailed, auth.RefusalAdminPasswordRejected)
	}
	mode, err := s.store.SignInSecurityMode(ctx)
	if err != nil {
		return EnrollResponse{}, err
	}
	// Verify first: it spends the KDF whether or not a password is set, and in
	// Strict mode too, so neither timing nor the counters reveal the mode. The
	// KDF runs inside the process-wide bound, and the attempt is counted on
	// both lanes before it runs, so parallel guesses cannot outrun the lanes.
	err = auth.CheckAdminPassword(ctx, s.store, auth.AdminPasswordAttempt{
		Action: adminSignInAttemptAction,
		Keys:   []string{remoteKey, adminSignInGlobalKey},
		At:     now,
	}, req.Password)
	switch {
	case errors.Is(err, auth.ErrAdminLocked):
		return EnrollResponse{}, refuse(ErrThrottled, auth.RefusalAdminLocked)
	case errors.Is(err, auth.ErrAdminBusy):
		// No slot: the same answer as a wrong password, not counted.
		s.logDenied(ctx, now, store.ActivityAdminLoginDenied)
		return EnrollResponse{}, refuse(ErrSignInFailed, auth.RefusalAdminLocked)
	case errors.Is(err, store.ErrNoAdminPassword), errors.Is(err, store.ErrAdminPasswordMismatch):
		return deny()
	case err != nil:
		return EnrollResponse{}, err
	}
	// Strict mode turns this path off (CORE-1110): even the right password is
	// refused and counted exactly like a wrong one.
	if mode == store.SignInSecurityStrict {
		return deny()
	}
	owner, err := s.store.OwnerProfile(ctx)
	if errors.Is(err, store.ErrNoOwnerProfile) {
		return deny()
	}
	if err != nil {
		return EnrollResponse{}, err
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
		ID:         store.DeviceID(deviceID),
		Name:       name,
		Role:       store.DeviceRoleManager,
		SecretHash: store.HashDeviceSecret(deviceSecret),
	}
	session := store.Session{
		ID:        store.SessionID(sessionID),
		DeviceID:  device.ID,
		ProfileID: owner.ID,
		ExpiresAt: sessionExpiresAt,
	}
	if err := s.store.EnrollDevice(ctx, device, session); err != nil {
		if errors.Is(err, store.ErrProfileDisabled) {
			return deny()
		}
		return EnrollResponse{}, err
	}
	_ = s.recordAttemptFor(ctx, adminSignInAttemptAction, adminSignInGlobalKey, remoteKey, now, true)
	_ = s.store.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: now.UTC(),
		Action:     store.ActivityEnroll,
		SessionID:  session.ID,
		DeviceID:   device.ID,
		ProfileID:  owner.ID,
		TargetType: "device",
		TargetID:   string(device.ID),
	})
	return EnrollResponse{
		DeviceID:         string(device.ID),
		DeviceSecret:     deviceSecret,
		SessionID:        string(session.ID),
		SessionExpiresAt: sessionExpiresAt.Format(time.RFC3339),
		ProfileID:        string(owner.ID),
		DeviceRole:       string(device.Role),
	}, nil
}

// SignInSecurityMode reports the stored sign-in security mode, so the enroll
// page can hide the admin-password form in Strict mode.
func (s *Service) SignInSecurityMode(ctx context.Context) (store.SignInSecurityMode, error) {
	return s.store.SignInSecurityMode(ctx)
}

func (s *Service) adminSignInThrottled(ctx context.Context, remoteKey string, now time.Time) (bool, error) {
	curve := auth.AdminBackoff()
	for _, key := range []string{remoteKey, adminSignInGlobalKey} {
		until, err := s.store.AuthBackoffUntil(ctx, adminSignInAttemptAction, key, now, curve)
		if err != nil {
			return false, err
		}
		if now.Before(until) {
			return true, nil
		}
	}
	return false, nil
}

// Package push is the Core Platform push-notification primitive.
//
// ADR-0039 makes push a Core Platform primitive shared by the parental
// extension-request flow (§2.1–2.4) and Watch Party invitations (§2.6). It has
// two halves, both in this package:
//
//   - REGISTRATION (Registrar): a viewer app registers its push token at
//     enrollment; the token is persisted per enrolled-device record (replacing
//     the original no-op seam that discarded it) and refreshed on app launch.
//   - DISPATCH (Dispatcher): the server sends a push to a device's token via
//     APNs HTTP/2, FCM v1, or the browser Web Push protocol directly — no
//     third-party relay (§2.5 rejected-alternatives). Each backend is picked by
//     the token's platform.
//
// Payload minimization is load-bearing (§2.5): a dispatched payload carries ONLY
// a notification type and a server-issued opaque event ID. The client fetches
// event details over the authenticated API on receipt — no library, playback,
// or any other content ever rides the push payload. See Payload.
//
// Operator credentials (the APNs .p8 signing key, the FCM service-account JSON,
// the Web Push VAPID keys) are operator-configured and live in the 0600 secure
// store (invariant #5) — never plaintext, never logged. The whole subsystem is
// opt-in / default-off: with no credentials configured the Dispatcher is
// dormant and the Registrar still persists tokens (so a later credential
// configure activates dispatch without re-enrollment), but nothing is ever sent.
//
// The client-independent fallback that works regardless is the in-app
// notification INBOX (store.CreateNotification / ListNotifications), polled on
// foreground. Push is the real-time accelerator (§2.5: "if a human is waiting on
// a blocked screen, use push"); the inbox is the durable record.
package push

import "context"

// PushPlatform names a device's push transport. Closed vocabulary: the three
// transports ADR-0039 §2.5 names — APNs (Apple), FCM (Android), and the browser
// Web Push protocol (web clients). The Dispatcher routes a token to its backend
// by this value; an unknown platform is a clean error, never a silent drop.
type PushPlatform string

const (
	PlatformAPNs    PushPlatform = "apns"
	PlatformFCM     PushPlatform = "fcm"
	PlatformWebPush PushPlatform = "webpush"
)

// valid reports whether p is one of the three known transports.
func (p PushPlatform) valid() bool {
	switch p {
	case PlatformAPNs, PlatformFCM, PlatformWebPush:
		return true
	default:
		return false
	}
}

// Registration is one device's push-token registration request (ADR-0039 §2.5:
// "viewer apps register a push token with the server at enrollment time"). The
// token is the APNs device token, the FCM registration token, or the Web Push
// subscription endpoint+keys (encoded by the client). ProfileID and DeviceID
// scope the registration to the enrolled device so dispatch can later fan out to
// a Profile's devices.
type Registration struct {
	ProfileID string
	DeviceID  string
	Platform  PushPlatform
	Token     string
}

// PushRegistrar is the registration seam (ADR-0016 §seams: an interface defined
// at the consumer — the server/graph enrollment path). Register persists (or
// refreshes) a device's push token. It returns ErrPushDisabled when registration
// is gated off so the caller can surface "push unavailable" honestly (and fall
// back to the polled inbox) rather than silently dropping the token.
type PushRegistrar interface {
	Register(ctx context.Context, reg Registration) error
	// Enabled reports whether push-token registration is currently accepted.
	Enabled() bool
}

// ErrPushDisabled is returned by Register when push-token registration is gated
// off. It is a stable sentinel so callers can branch on it (e.g. fall back to
// the polled inbox) rather than treating it as an internal failure.
type ErrPushDisabled struct{}

func (ErrPushDisabled) Error() string {
	return "push registration disabled"
}

// ErrInvalidPlatform is returned when a Registration carries a platform outside
// the closed apns|fcm|webpush vocabulary.
type ErrInvalidPlatform struct{}

func (ErrInvalidPlatform) Error() string {
	return "unknown push platform"
}

// TokenStore is the persistence seam the Registrar writes through (interface at
// the consumer, ADR-0016). *store.Store satisfies it via UpsertDevicePushToken.
// It is a one-method seam so a test can assert persistence without a real DB.
type TokenStore interface {
	UpsertDevicePushToken(ctx context.Context, t TokenRecord) error
}

// TokenRecord is the persisted shape of a registration. It mirrors
// store.DevicePushToken's input fields but is declared here so the push package
// (which owns the seam) does not import the store; the composition root adapts
// between the two. Platform is the closed-vocabulary string.
type TokenRecord struct {
	DeviceID  string
	ProfileID string
	Platform  string
	Token     string
}

// Registrar is the persisting PushRegistrar (PUSH-1, replacing NoopRegistrar):
// when enabled it validates the platform and upserts the token through the store
// seam; when disabled it refuses with ErrPushDisabled. It touches no network and
// no secret — dispatch is a separate concern (Dispatcher). Persisting the token
// even while no dispatch credentials are configured is deliberate: an operator
// who configures credentials later can dispatch to already-registered devices
// without forcing every client to re-enroll.
type Registrar struct {
	store   TokenStore
	enabled bool
}

// NewRegistrar returns a persisting registrar gated by enabled (wired from
// config.PushRegistrationEnabled). store is required when enabled.
func NewRegistrar(store TokenStore, enabled bool) *Registrar {
	return &Registrar{store: store, enabled: enabled}
}

// Register validates and persists the registration when enabled, or refuses with
// ErrPushDisabled when not. An unknown platform is rejected with
// ErrInvalidPlatform before any write.
func (r *Registrar) Register(ctx context.Context, reg Registration) error {
	if !r.enabled || r.store == nil {
		return ErrPushDisabled{}
	}
	if !reg.Platform.valid() {
		return ErrInvalidPlatform{}
	}
	return r.store.UpsertDevicePushToken(ctx, TokenRecord{
		DeviceID:  reg.DeviceID,
		ProfileID: reg.ProfileID,
		Platform:  string(reg.Platform),
		Token:     reg.Token,
	})
}

// Enabled reports the configured gate state.
func (r *Registrar) Enabled() bool { return r.enabled }

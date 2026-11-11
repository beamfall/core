package auth

import (
	"context"
	"sync"
)

// RefusalReason names why a presented credential was refused. It is a closed
// vocabulary safe for logs and metric labels: a reason never carries any part
// of the credential, the session or the caller's identity.
type RefusalReason string

// Refusal reasons noted by credential resolution and sign-in.
const (
	RefusalCredentialIncomplete  RefusalReason = "credential_incomplete"
	RefusalCSRFMissing           RefusalReason = "csrf_missing"
	RefusalSessionInvalid        RefusalReason = "session_invalid"
	RefusalSessionExpired        RefusalReason = "session_expired"
	RefusalIntegrationRestricted RefusalReason = "integration_session_restricted"
	RefusalDeviceMismatch        RefusalReason = "device_mismatch"
	RefusalDeviceSecretInvalid   RefusalReason = "device_secret_invalid"
	RefusalDeviceRevoked         RefusalReason = "device_revoked"
	RefusalProfileUnavailable    RefusalReason = "profile_unavailable"
	RefusalOutsideSchedule       RefusalReason = "outside_schedule"
	RefusalAPITokenInvalid       RefusalReason = "api_token_invalid"
	RefusalScopedKeyInvalid      RefusalReason = "scoped_key_invalid"
	RefusalAdminLocked           RefusalReason = "admin_locked"
	RefusalAdminPasswordRejected RefusalReason = "admin_password_rejected"
	RefusalIntegrationLocked     RefusalReason = "integration_locked"
	RefusalIntegrationRejected   RefusalReason = "integration_rejected"
	RefusalPairingThrottled      RefusalReason = "pairing_throttled"
	RefusalPairingCodeInvalid    RefusalReason = "pairing_code_invalid"
	RefusalSetupTokenInvalid     RefusalReason = "setup_token_invalid"
)

// Operations whose refusals internal/auth notes.
const (
	refusalOperationCredential        = "credential"
	refusalOperationAdminSession      = "admin_session"
	refusalOperationIntegrationSignIn = "integration_sign_in"
)

// RefusalReasons lists every reason in the vocabulary, for callers that
// report a fixed label set.
var RefusalReasons = []RefusalReason{
	RefusalCredentialIncomplete, RefusalCSRFMissing, RefusalSessionInvalid, RefusalSessionExpired,
	RefusalIntegrationRestricted, RefusalDeviceMismatch, RefusalDeviceSecretInvalid, RefusalDeviceRevoked,
	RefusalProfileUnavailable, RefusalOutsideSchedule, RefusalAPITokenInvalid, RefusalScopedKeyInvalid,
	RefusalAdminLocked, RefusalAdminPasswordRejected, RefusalIntegrationLocked, RefusalIntegrationRejected,
	RefusalPairingThrottled, RefusalPairingCodeInvalid, RefusalSetupTokenInvalid,
}

// Refusal is the refusal noted for one request. Decisive marks an explicit
// sign-in attempt whose refusal is the outcome; a non-decisive refusal comes
// from scope resolution, which a route may tolerate.
type Refusal struct {
	Operation string
	Reason    RefusalReason
	Decisive  bool
}

type refusalNoteKey struct{}

type refusalNote struct {
	mu      sync.Mutex
	refusal Refusal
	set     bool
}

// WithRefusalNote returns a context in which credential refusals are noted,
// for RefusalFromContext to read after the request completes.
func WithRefusalNote(ctx context.Context) context.Context {
	return context.WithValue(ctx, refusalNoteKey{}, &refusalNote{})
}

// RefusalFromContext reports the refusal noted in ctx, if any.
func RefusalFromContext(ctx context.Context) (Refusal, bool) {
	note, _ := ctx.Value(refusalNoteKey{}).(*refusalNote)
	if note == nil {
		return Refusal{}, false
	}
	note.mu.Lock()
	defer note.mu.Unlock()
	return note.refusal, note.set
}

// NoteRefusal records a refusal in ctx when WithRefusalNote installed a note.
// The first refusal wins, except that a decisive refusal replaces a
// non-decisive one.
func NoteRefusal(ctx context.Context, operation string, reason RefusalReason, decisive bool) {
	note, _ := ctx.Value(refusalNoteKey{}).(*refusalNote)
	if note == nil {
		return
	}
	note.mu.Lock()
	defer note.mu.Unlock()
	if note.set && (note.refusal.Decisive || !decisive) {
		return
	}
	note.refusal = Refusal{Operation: operation, Reason: reason, Decisive: decisive}
	note.set = true
}

func noteCredentialRefusal(ctx context.Context, reason RefusalReason) {
	NoteRefusal(ctx, refusalOperationCredential, reason, false)
}

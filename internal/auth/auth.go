// Package auth resolves Beamfall sessions into server-side authorization scopes.
package auth

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/beamfall/core/internal/store"
)

// Browser credential cookie names and the CSRF header: the web
// app's device credential lives in HttpOnly cookies and never enters
// JS-readable space. Cookie-authenticated mutating requests must also carry
// the custom header — a cross-site page cannot set it, and SameSite=Strict
// keeps the cookies off cross-site requests in the first place.
const (
	SessionCookie      = "beamfall_session"
	DeviceCookie       = "beamfall_device"
	DeviceSecretCookie = "beamfall_device_secret"
	CSRFHeader         = "X-Beamfall-Csrf"
)

// Admin Session policy (ADR-0007 §4 — "short-lived re-auth"; ADR-0012 §3 —
// brute-force economics). The ADR pins neither the lifetime nor the lockout
// curve numerically; both are chosen here and flagged in
// docs/agent-memory/questions.md for confirmation. The lockout mirrors the
// pairing lane's exponential-backoff curve so admin re-auth is not a separate,
// weaker brute-force surface.
const (
	// adminSessionTTL is the Admin Session window. ADR-0007 §2 pins this at 15
	// minutes (confirmed 2026-06-16; the ADR previously said only "short-lived").
	adminSessionTTL = 15 * time.Minute
	// adminAttemptAction names the admin re-auth throttle lane.
	adminAttemptAction = "admin.login"
	// adminAttemptWindow forgets failures older than this.
	adminAttemptWindow              = 10 * time.Minute
	integrationSessionTTL           = 24 * time.Hour
	integrationSetupTTL             = 15 * time.Minute
	integrationFailureFloor         = 75 * time.Millisecond
	integrationAttemptAction        = "integration.login"
	integrationAttemptPseudonymInfo = "beamfall/integration-attempt-pseudonym/v1"
)

// adminBackoff is the admin re-auth lane's exponential-backoff curve (ADR-0012
// §3): the lockout grows with consecutive failed re-auths and resets on
// success. Mirrors the pairing lane's curve.
var adminBackoff = store.AuthBackoff{
	Window:       adminAttemptWindow,
	FreeAttempts: 5,
	BaseDelay:    2 * time.Second,
	MaxDelay:     adminAttemptWindow,
}

// ErrAdminLocked means admin re-auth attempts for this session exceeded the
// short-window limit; the caller must back off.
var ErrAdminLocked = errors.New("admin re-auth attempts throttled")

// ErrAdminPassword means the supplied admin password did not verify (or none
// is set). The two cases are deliberately indistinguishable to the network
// caller — no oracle for whether a password exists.
var ErrAdminPassword = errors.New("admin password rejected")

// ErrAccessOutsideSchedule means a managed Profile's session was resolved
// outside its allowed access-schedule time windows.
// Every credential path that builds a Scope refuses here, so enforcement is at
// the single scope-resolution choke point rather than per-surface.
var ErrAccessOutsideSchedule = errors.New("profile access not allowed at this time")

// ErrIntegrationAuthentication is the single network-facing failure class for
// every integration sign-in refusal, including lockout.
var ErrIntegrationAuthentication = errors.New("integration authentication rejected")

var integrationBackoff = store.AuthBackoff{
	Window: 10 * time.Minute, FreeAttempts: 5, BaseDelay: 2 * time.Second, MaxDelay: 10 * time.Minute,
}

// Scope is the authorization context used by read paths.
type Scope struct {
	// APITokenID is set when the request authenticated with an admin API
	// token instead of a session; every session field below
	// is then empty. Token scopes are ops-class: manager role,
	// hidden-world visibility, no Profile.
	APITokenID string
	// APIKeyID is set when the request authenticated with a scoped developer
	// API key (ADR-0040 §2.2, Bearer bfk_…) instead of a session or admin
	// token. APIScopes then carries the key's closed-vocabulary scope set;
	// per-route enforcement checks it via HasAPIScope. A scoped key may be
	// Profile-bound (ProfileID set, LibraryIDs/Certification resolved from
	// that Profile so the Reveal contract filters reads exactly as for a
	// session) or admin-scoped (no Profile, manager-class). Like the admin
	// token, a scoped key is never a standing Reveal — Revealed stays false,
	// so restricted content never reaches a key that cannot Reveal it.
	APIKeyID          string
	APIScopes         []store.APIScope
	SessionID         store.SessionID
	SessionKind       store.SessionKind
	IntegrationScopes []store.IntegrationScope
	DeviceID          store.DeviceID
	DeviceRole        store.DeviceRole
	DistributionClass store.DeviceDistributionClass
	ProfileID         store.ProfileID
	Profile           store.ProfileKind
	Revealed          bool
	// Admin is true when the session currently holds an Admin Session:
	// an adult Profile whose session was re-authenticated with
	// the server admin password within the short-lived window. It gates the
	// Console operation class, orthogonal to DeviceRole. A managed Profile, a
	// session whose admin window lapsed, and an API token are never admin.
	Admin bool
	// AdminUntil is the server-side expiry for the current Admin Session. It is
	// populated only for an active Admin Session so the Console can render and
	// client-expire the same short window the server enforces.
	AdminUntil time.Time
	ExpiresAt  time.Time
	LibraryIDs []store.LibraryID
	// AllLibraries marks this scope as the deliberate "all Libraries" grant —
	// the ops-class admin token and admin-scoped (non-Profile) key, whose empty
	// LibraryIDs legitimately means every Library. It flows into the
	// LibraryFilter sentinel so the store can tell this case apart from a
	// zero-grant Profile session (whose empty LibraryIDs must fail CLOSED).
	// Profile-bound scopes never set it; their visibility is exactly their
	// LibraryIDs grant.
	AllLibraries bool
	// Certification carries the Profile's resolved certification ceilings
	// into every read path's LibraryFilter.
	Certification []store.CertificationConstraint
	// GrantedMediaIDs lists the media items this managed Profile has active
	// AccessGrants for. Populated at scope resolution
	// time for managed Profiles; nil for adults and non-Profile scopes.
	GrantedMediaIDs []store.MediaID
}

// Service resolves sessions.
type Service struct {
	store *store.Store
	now   func() time.Time
	// certificationScales maps plugin ID → that plugin's ordered
	// certification scale (low→high), injected at wiring time from the
	// activated bundles. It resolves a stored ceiling value into the
	// allowed-value prefix; a ceiling against an unknown scale or value
	// resolves fail-closed (no certified Media allowed).
	certificationScales      map[string][]string
	integrationAttemptSecret []byte
}

// NewService creates an auth service.
func NewService(st *store.Store) *Service {
	return NewServiceWithClock(st, time.Now)
}

// NewServiceWithClock creates an auth service with an injectable clock for tests.
func NewServiceWithClock(st *store.Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	return &Service{store: st, now: now, integrationAttemptSecret: secret}
}

// SetIntegrationAttemptSecret binds unknown-name security-event targets to a
// one-way HKDF key derived from a durable server root with fixed domain-separation
// info. The root is never used directly, so audit tags cannot be reused as Stream
// Token material or vice versa. The attempted name itself is never persisted.
func (s *Service) SetIntegrationAttemptSecret(secret []byte) {
	if len(secret) == 0 {
		s.integrationAttemptSecret = append([]byte(nil), secret...)
		return
	}
	derived, err := hkdf.Key(sha256.New, secret, nil, integrationAttemptPseudonymInfo, 32)
	if err != nil {
		panic(err)
	}
	s.integrationAttemptSecret = derived
}

// SetCertificationScales wires the activated plugins' ordered certification
// scales (plugin ID → values, low→high). Called once at wiring time.
func (s *Service) SetCertificationScales(scales map[string][]string) {
	s.certificationScales = scales
}

// certificationConstraints resolves a Profile's stored ceilings against the
// wired scales: the allowed set is the scale prefix up to and including the
// ceiling. A ceiling whose plugin scale is unknown, or whose value is not on
// the scale, yields an empty allowed set — fail-closed, consistent with the
// unrated-blocked-by-default posture.
func (s *Service) certificationConstraints(ceilings []store.ProfileCertificationCeiling) []store.CertificationConstraint {
	if len(ceilings) == 0 {
		return nil
	}
	constraints := make([]store.CertificationConstraint, 0, len(ceilings))
	for _, ceiling := range ceilings {
		constraint := store.CertificationConstraint{
			PluginID:     ceiling.PluginID,
			AllowUnrated: ceiling.AllowUnrated,
			Locked:       ceiling.FilterVisibility == "locked",
		}
		for i, value := range s.certificationScales[ceiling.PluginID] {
			if value == ceiling.Ceiling {
				constraint.Allowed = append([]string(nil), s.certificationScales[ceiling.PluginID][:i+1]...)
				break
			}
		}
		constraints = append(constraints, constraint)
	}
	return constraints
}

// ScopeFromRequest resolves HTTP credentials into a Scope. A Bearer credential
// wins when present — a scoped developer key (ADR-0040, bfk_ prefix) routes to
// the scoped-key path, anything else to the admin API token; then
// the X-Beamfall-* headers; then the browser cookie credential.
func (s *Service) ScopeFromRequest(ctx context.Context, r *http.Request) (Scope, error) {
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		token := strings.TrimSpace(bearer)
		if strings.HasPrefix(token, store.ScopedKeyPrefix) {
			return s.ScopeFromScopedKey(ctx, token)
		}
		return s.ScopeFromAPIToken(ctx, token)
	}
	sessionID := store.SessionID(r.Header.Get("X-Beamfall-Session"))
	if sessionID == "" {
		return s.scopeFromCookies(ctx, r)
	}
	deviceID := store.DeviceID(r.Header.Get("X-Beamfall-Device"))
	deviceSecret := r.Header.Get("X-Beamfall-Device-Secret")
	return s.ScopeWithDeviceCredential(ctx, sessionID, deviceID, deviceSecret)
}

// scopeFromCookies resolves the browser cookie credential. Mutating requests
// must carry the CSRF header: cookies travel automatically, so SameSite=Strict
// plus a custom header (which forces a same-origin script) is the CSRF
// defense per ADR-0012 §5.
func (s *Service) scopeFromCookies(ctx context.Context, r *http.Request) (Scope, error) {
	session, err := r.Cookie(SessionCookie)
	if err != nil {
		return Scope{}, errors.New("session credential is required")
	}
	device, err := r.Cookie(DeviceCookie)
	if err != nil {
		return Scope{}, errors.New("device credential is required")
	}
	secret, err := r.Cookie(DeviceSecretCookie)
	if err != nil {
		return Scope{}, errors.New("device credential is required")
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		if r.Header.Get(CSRFHeader) == "" {
			return Scope{}, errors.New("missing CSRF header on cookie-authenticated request")
		}
	}
	return s.ScopeWithDeviceCredential(ctx, store.SessionID(session.Value), store.DeviceID(device.Value), secret.Value)
}

// ScopeFromAPIToken resolves an admin API token into its ops-class Scope:
// manager role for the ops surface, Revealed always false (the Reveal
// contract is per-session and PIN-gated — a standing credential is never a
// standing reveal), no Profile, no Library grants beyond the hidden world.
func (s *Service) ScopeFromAPIToken(ctx context.Context, token string) (Scope, error) {
	verified, err := s.store.VerifyAPIToken(ctx, token, s.now())
	if err != nil {
		return Scope{}, err
	}
	return Scope{
		APITokenID:   verified.ID,
		DeviceRole:   store.DeviceRoleManager,
		AllLibraries: true, // ops-class token: hidden-world, every Library
	}, nil
}

// ScopeFromScopedKey resolves a scoped developer API key (ADR-0040 §2.2,
// Bearer bfk_…) into a Scope carrying the key's closed-vocabulary scope set.
// Per-route enforcement reads APIScopes via HasAPIScope; this method only
// authenticates the credential and resolves visibility, it does not itself
// reject on scope (a route declares the scope it needs).
//
// A Profile-bound key resolves that Profile and inherits its Library grants and
// certification ceilings, so the Reveal contract filters reads for
// the key exactly as it does for a session of that Profile — a Profile-bound
// key sees only its own Profile's content. Like the admin token
// (ScopeFromAPIToken), a scoped key is never a standing Reveal: Revealed stays
// false, so restricted content never reaches a caller who cannot Reveal it. An
// admin-scoped key (no Profile binding) is manager-class with no Library
// restriction, mirroring the admin token's ops-class visibility.
func (s *Service) ScopeFromScopedKey(ctx context.Context, key string) (Scope, error) {
	verified, err := s.store.VerifyScopedAPIKey(ctx, key, s.now())
	if err != nil {
		return Scope{}, err
	}
	scope := Scope{
		APIKeyID:   verified.ID,
		APIScopes:  verified.Scopes,
		DeviceRole: store.DeviceRoleManager,
	}
	if verified.ProfileID == "" {
		// Admin-scoped key (no Profile binding): manager-class, every Library —
		// mirrors the ops-class admin token's hidden-world visibility.
		scope.AllLibraries = true
		return scope, nil
	}
	profile, err := s.store.Profile(ctx, verified.ProfileID)
	if err != nil {
		return Scope{}, err
	}
	if profile.Disabled {
		return Scope{}, store.ErrProfileDisabled
	}
	if err := s.enforceProfileAccessSchedule(ctx, profile, ""); err != nil {
		return Scope{}, err
	}
	scope.DeviceRole = ""
	scope.ProfileID = profile.ID
	scope.Profile = profile.Kind
	scope.LibraryIDs = profile.Library
	scope.Certification = s.certificationConstraints(profile.CertificationCeilings)
	if profile.Kind == store.ProfileManaged {
		scope.GrantedMediaIDs, _ = s.store.GrantedMediaIDsForProfile(ctx, profile.ID)
	}
	return scope, nil
}

// HasAPIScope reports whether a scoped-key Scope carries the given API scope.
// It is false for any non-scoped-key Scope (a session or admin token carries no
// APIScopes), so a route that gates on an API scope rejects credentials that are
// not scoped keys at all — the scope vocabulary is the developer-key surface.
func (s Scope) HasAPIScope(scope store.APIScope) bool {
	for _, have := range s.APIScopes {
		if have == scope {
			return true
		}
	}
	return false
}

// IsScopedKey reports whether the Scope was authenticated by a scoped developer
// API key. Per-route enforcement applies the API-scope check only to scoped-key
// callers; a session- or admin-token-authenticated request keeps its existing
// authorization path untouched.
func (s Scope) IsScopedKey() bool {
	return s.APIKeyID != ""
}

// Scope resolves a session identifier into a Scope.
func (s *Service) Scope(ctx context.Context, sessionID store.SessionID) (Scope, error) {
	return s.scope(ctx, sessionID, "", "")
}

// ScopeWithDeviceCredential resolves a session and verifies the device credential.
func (s *Service) ScopeWithDeviceCredential(ctx context.Context, sessionID store.SessionID, deviceID store.DeviceID, deviceSecret string) (Scope, error) {
	if deviceID == "" || deviceSecret == "" {
		return Scope{}, errors.New("device credential is required")
	}
	return s.scope(ctx, sessionID, deviceID, deviceSecret)
}

func (s *Service) scope(ctx context.Context, sessionID store.SessionID, credentialDeviceID store.DeviceID, deviceSecret string) (Scope, error) {
	session, err := s.store.Session(ctx, sessionID)
	if err != nil {
		return Scope{}, err
	}
	if !session.ExpiresAt.IsZero() && !s.now().Before(session.ExpiresAt) {
		return Scope{}, errors.New("session expired")
	}
	if session.Kind != store.SessionKindEnrolled {
		return Scope{}, errors.New("integration session is restricted to compat routes")
	}
	device, err := s.store.Device(ctx, session.DeviceID)
	if err != nil {
		return Scope{}, err
	}
	if credentialDeviceID != "" {
		if credentialDeviceID != session.DeviceID {
			return Scope{}, errors.New("device credential mismatch")
		}
		verified, err := s.store.VerifyDeviceSecret(ctx, credentialDeviceID, deviceSecret)
		if err != nil {
			return Scope{}, err
		}
		device = verified
	}
	if device.Revoked {
		return Scope{}, errors.New("device revoked")
	}
	profile, err := s.store.Profile(ctx, session.ProfileID)
	if err != nil {
		return Scope{}, err
	}
	if profile.Disabled {
		return Scope{}, store.ErrProfileDisabled
	}
	// Access-schedule enforcement: a managed Profile
	// is denied a resolved Scope outside its allowed server-local time windows.
	// Additive and fail-open by absence — a Profile with no windows has no
	// schedule and resolves normally; only a managed Profile with windows that
	// exclude the current instant is refused. Evaluated in server-local time to
	// match the Scheduler's server-timezone-at-rest posture.
	if profile.Kind == store.ProfileManaged {
		if err := s.enforceProfileAccessSchedule(ctx, profile, session.ID); err != nil {
			return Scope{}, err
		}
	}
	revealed := session.Revealed && profile.Kind == store.ProfileAdult
	// Admin authorization: an adult Profile whose admin window
	// is still in the future. PINs never grant admin and managed Profiles have
	// no admin path, so the adult-kind requirement is enforced here exactly as
	// for Reveal — the stored window alone is not sufficient.
	adminUntil := time.Time{}
	if profile.Kind == store.ProfileAdult &&
		!session.AdminUntil.IsZero() && s.now().Before(session.AdminUntil) {
		adminUntil = session.AdminUntil
	}
	// ACCESS-3: for managed Profiles, resolve the active access
	// grants so cert enforcement can bypass them. Best-effort: an error here
	// does not block login — the scope resolves without grants (fail-safe: the
	// locked ceiling still applies, which is the conservative failure mode).
	var grantedIDs []store.MediaID
	if profile.Kind == store.ProfileManaged {
		grantedIDs, _ = s.store.GrantedMediaIDsForProfile(ctx, profile.ID)
	}
	return Scope{
		SessionID:         session.ID,
		SessionKind:       session.Kind,
		DeviceID:          session.DeviceID,
		DeviceRole:        device.Role,
		DistributionClass: device.DistributionClass,
		ProfileID:         profile.ID,
		Profile:           profile.Kind,
		Revealed:          revealed,
		Admin:             !adminUntil.IsZero(),
		AdminUntil:        adminUntil,
		ExpiresAt:         session.ExpiresAt,
		LibraryIDs:        profile.Library,
		Certification:     s.certificationConstraints(profile.CertificationCeilings),
		GrantedMediaIDs:   grantedIDs,
	}, nil
}

func (s *Service) enforceProfileAccessSchedule(ctx context.Context, profile store.Profile, sessionID store.SessionID) error {
	if profile.Kind != store.ProfileManaged {
		return nil
	}
	at := s.now()
	windows, err := s.store.ProfileAccessWindows(ctx, profile.ID)
	if err != nil {
		return err
	}
	if store.AllowedAt(windows, at.Local()) {
		return nil
	}
	if sessionID == "" {
		return ErrAccessOutsideSchedule
	}
	// Extension hook: an approved, unexpired grant is
	// scoped to one session. Scoped keys have no session ID, so they fail closed
	// outside schedule and never inherit an extension grant.
	granted, err := s.store.ActiveExtensionGrant(ctx, profile.ID, sessionID, at)
	if err != nil {
		return err
	}
	if !granted {
		return ErrAccessOutsideSchedule
	}
	return nil
}

// IntegrationSession is the bearer returned only by the compat
// AuthenticateByName surface.
type IntegrationSession struct {
	Token     string
	ProfileID store.ProfileID
	ExpiresAt time.Time
	Setup     bool
}

// AuthenticateIntegrationByName authenticates a Profile entry credential, or
// the bounded "admin" setup credential, and mints a non-renewable integration
// Session. Every refusal uses one error and a common minimum timing class.
func (s *Service) AuthenticateIntegrationByName(ctx context.Context, name, credential string, scopes []store.IntegrationScope) (IntegrationSession, error) {
	started := time.Now()
	name = strings.TrimSpace(name)
	profile, profileErr := s.store.ProfileByName(ctx, name)
	setup := strings.EqualFold(name, "admin")
	if setup {
		profile, profileErr = s.store.OwnerProfile(ctx)
	}
	attemptKey := s.integrationAttemptKey(name, profile, profileErr, setup)
	lockedUntil, err := s.store.AuthBackoffUntil(ctx, integrationAttemptAction, attemptKey, s.now(), integrationBackoff)
	if err != nil {
		return IntegrationSession{}, err
	}
	if s.now().Before(lockedUntil) {
		s.auditIntegrationAttempt(ctx, profile, profileErr, attemptKey, false)
		return IntegrationSession{}, s.integrationFailure(started)
	}

	credentialErr := profileErr
	if credentialErr == nil && setup {
		credentialErr = s.store.VerifyAdminPassword(ctx, credential)
	}
	if credentialErr == nil && !setup {
		credentialErr = s.store.VerifyProfileEntryPIN(ctx, profile.ID, credential)
		if credentialErr == nil && (!profile.IntegrationSignIn || profile.Disabled || profile.Kind != store.ProfileAdult) {
			credentialErr = ErrIntegrationAuthentication
		}
	}
	if credentialErr != nil {
		_ = s.store.RecordAuthAttempt(ctx, store.AuthAttempt{OccurredAt: s.now().UTC(), Action: integrationAttemptAction, Key: attemptKey})
		s.auditIntegrationAttempt(ctx, profile, profileErr, attemptKey, false)
		return IntegrationSession{}, s.integrationFailure(started)
	}

	if _, err := encodeIntegrationScopeRequest(scopes); err != nil {
		return IntegrationSession{}, s.integrationFailure(started)
	}
	token, err := randomIntegrationToken()
	if err != nil {
		return IntegrationSession{}, err
	}
	ttl := integrationSessionTTL
	if setup {
		ttl = integrationSetupTTL
	}
	expires := s.now().Add(ttl).UTC()
	err = s.store.CreateIntegrationSession(ctx, store.Session{
		ID: store.SessionID(token), ProfileID: profile.ID, Kind: store.SessionKindIntegration,
		IntegrationScopes: scopes, ExpiresAt: expires,
	}, !setup)
	if err != nil {
		return IntegrationSession{}, s.integrationFailure(started)
	}
	_ = s.store.RecordAuthAttempt(ctx, store.AuthAttempt{OccurredAt: s.now().UTC(), Action: integrationAttemptAction, Key: attemptKey, Success: true})
	s.auditIntegrationAttempt(ctx, profile, nil, attemptKey, true)
	return IntegrationSession{Token: token, ProfileID: profile.ID, ExpiresAt: expires, Setup: setup}, nil
}

func encodeIntegrationScopeRequest(scopes []store.IntegrationScope) (string, error) {
	if len(scopes) == 0 {
		return "", errors.New("integration scopes are required")
	}
	parts := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if scope != store.IntegrationScopeRead && scope != store.IntegrationScopeRefresh {
			return "", errors.New("integration scope exceeds ceiling")
		}
		parts = append(parts, string(scope))
	}
	return strings.Join(parts, ","), nil
}

func randomIntegrationToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (s *Service) integrationAttemptKey(name string, profile store.Profile, profileErr error, setup bool) string {
	if profileErr == nil {
		if setup {
			return "admin:" + string(profile.ID)
		}
		return "profile:" + string(profile.ID)
	}
	mac := hmac.New(sha256.New, s.integrationAttemptSecret)
	_, _ = mac.Write([]byte(strings.ToLower(name)))
	return "unknown:" + hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) integrationFailure(started time.Time) error {
	if remaining := integrationFailureFloor - time.Since(started); remaining > 0 {
		time.Sleep(remaining)
	}
	return ErrIntegrationAuthentication
}

func (s *Service) auditIntegrationAttempt(ctx context.Context, profile store.Profile, profileErr error, attemptKey string, success bool) {
	action := "integration.login.denied"
	if success {
		action = "integration.login"
	}
	entry := store.ActivityEntry{OccurredAt: s.now().UTC(), Action: action}
	if profileErr == nil {
		entry.ProfileID = profile.ID
	} else {
		entry.TargetType = "security-target"
		entry.TargetID = strings.TrimPrefix(attemptKey, "unknown:")
	}
	_ = s.store.AppendActivity(ctx, entry)
}

// IntegrationScope authenticates an integration bearer for one closed compat
// scope. Enrolled sessions and integrations lacking the bit fail closed.
func (s *Service) IntegrationScope(ctx context.Context, token string, required store.IntegrationScope) (Scope, error) {
	session, err := s.store.Session(ctx, store.SessionID(token))
	if err != nil {
		return Scope{}, err
	}
	if session.Kind != store.SessionKindIntegration || !s.now().Before(session.ExpiresAt) {
		return Scope{}, errors.New("invalid integration session")
	}
	hasScope := false
	for _, scope := range session.IntegrationScopes {
		if scope == required {
			hasScope = true
		}
	}
	if !hasScope {
		return Scope{}, errors.New("integration scope denied")
	}
	profile, err := s.store.Profile(ctx, session.ProfileID)
	if err != nil || profile.Disabled || profile.Kind != store.ProfileAdult {
		return Scope{}, errors.New("integration profile unavailable")
	}
	return Scope{
		SessionID: session.ID, SessionKind: session.Kind, IntegrationScopes: session.IntegrationScopes,
		ProfileID: profile.ID, Profile: profile.Kind, Revealed: session.Revealed,
		ExpiresAt: session.ExpiresAt, LibraryIDs: profile.Library,
		Certification: s.certificationConstraints(profile.CertificationCeilings),
	}, nil
}

// RevokeIntegrationSession revokes the caller's non-renewable bearer.
func (s *Service) RevokeIntegrationSession(ctx context.Context, token string) error {
	session, err := s.store.Session(ctx, store.SessionID(token))
	if err != nil || session.Kind != store.SessionKindIntegration {
		return errors.New("invalid integration session")
	}
	if err := s.store.RevokeIntegrationSession(ctx, session.ID, session.ProfileID); err != nil {
		return err
	}
	_ = s.store.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: s.now().UTC(), Action: "integration.revoke", ProfileID: session.ProfileID,
		TargetType: "session", TargetID: string(session.ID),
	})
	return nil
}

// BeginAdminSession re-authenticates the caller's session with the server
// admin password and, on success, opens the short-lived Admin Session window.
// The session must already resolve to an adult Profile —
// PINs/managed Profiles never grant admin, and a non-adult caller cannot open
// the window even with the right password. Throttled per session
// and audited (success and failure) without ever recording the password.
//
// The returned time is the window's end; every Console-op read re-derives
// Scope.Admin from it via scope(), so expiry is immediate. A failed verify and
// a not-set password both return ErrAdminPassword — no presence oracle.
func (s *Service) BeginAdminSession(ctx context.Context, scope Scope, password string) (time.Time, error) {
	if scope.SessionID == "" {
		return time.Time{}, errors.New("session is required")
	}
	now := s.now()
	attemptKey := "session:" + string(scope.SessionID)
	lockedUntil, err := s.store.AuthBackoffUntil(ctx, adminAttemptAction, attemptKey, now, adminBackoff)
	if err != nil {
		return time.Time{}, err
	}
	if now.Before(lockedUntil) {
		return time.Time{}, ErrAdminLocked
	}
	// Admin is an adult-Profile tier: a managed Profile has no admin path, so
	// reject before spending the KDF or burning a verify attempt against it.
	if scope.Profile != store.ProfileAdult {
		s.recordAdminAttempt(ctx, attemptKey, now, false)
		s.auditAdmin(ctx, scope, now, store.ActivityAdminLoginDenied)
		return time.Time{}, ErrAdminPassword
	}
	// Admin is restricted to the designated owner Profile.
	// Lockout-safety fallback: a server with no owner designated — e.g. one
	// upgraded across migration 0069 — admits any adult Profile exactly as
	// before, so an upgrade can never brick Admin access. A non-owner adult is
	// rejected with ErrAdminPassword (the same error as a bad password), so the
	// owner identity is never an oracle.
	ownerDesignated, err := s.store.OwnerDesignated(ctx)
	if err != nil {
		return time.Time{}, err
	}
	if ownerDesignated {
		isOwner, oerr := s.store.ProfileIsOwner(ctx, scope.ProfileID)
		if oerr != nil {
			return time.Time{}, oerr
		}
		if !isOwner {
			s.recordAdminAttempt(ctx, attemptKey, now, false)
			s.auditAdmin(ctx, scope, now, store.ActivityAdminLoginDenied)
			return time.Time{}, ErrAdminPassword
		}
	}
	if verr := s.store.VerifyAdminPassword(ctx, password); verr != nil {
		s.recordAdminAttempt(ctx, attemptKey, now, false)
		s.auditAdmin(ctx, scope, now, store.ActivityAdminLoginDenied)
		return time.Time{}, ErrAdminPassword
	}
	until := now.Add(adminSessionTTL).UTC()
	if err := s.store.SetSessionAdminUntil(ctx, scope.SessionID, until); err != nil {
		return time.Time{}, err
	}
	s.recordAdminAttempt(ctx, attemptKey, now, true)
	s.auditAdmin(ctx, scope, now, store.ActivityAdminLogin)
	return until, nil
}

// EndAdminSession clears the caller's Admin Session window immediately
// (explicit lock, mirroring relock for Reveal). Always safe to call.
func (s *Service) EndAdminSession(ctx context.Context, scope Scope) error {
	if scope.SessionID == "" {
		return errors.New("session is required")
	}
	return s.store.SetSessionAdminUntil(ctx, scope.SessionID, time.Time{})
}

func (s *Service) recordAdminAttempt(ctx context.Context, key string, now time.Time, success bool) {
	_ = s.store.RecordAuthAttempt(ctx, store.AuthAttempt{
		OccurredAt: now.UTC(),
		Action:     adminAttemptAction,
		Key:        key,
		Success:    success,
	})
}

func (s *Service) auditAdmin(ctx context.Context, scope Scope, now time.Time, action string) {
	_ = s.store.AppendActivity(ctx, store.ActivityEntry{
		OccurredAt: now.UTC(),
		Action:     action,
		SessionID:  scope.SessionID,
		DeviceID:   scope.DeviceID,
		ProfileID:  scope.ProfileID,
	})
}

// LibraryFilter converts a Scope into the store read policy.
func (s Scope) LibraryFilter() store.LibraryFilter {
	return store.LibraryFilter{
		AllowedIDs:      s.LibraryIDs,
		AllLibraries:    s.AllLibraries,
		Revealed:        s.Revealed,
		Certification:   s.Certification,
		GrantedMediaIDs: s.GrantedMediaIDs,
	}
}

// PlaybackFilter returns a LibraryFilter with PlaybackGate set, which causes
// certificationFilterSQL to enforce ALL certification constraints as hard
// exclusions regardless of the Locked/visibility mode (ACCESS-1, ADR-0113
// §2.1). Use this for byte-serving paths where the server must deny locked Media.
func (s Scope) PlaybackFilter() store.LibraryFilter {
	f := s.LibraryFilter()
	f.PlaybackGate = true
	return f
}

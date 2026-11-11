package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/reveal"
	"github.com/beamfall/core/internal/store"
)

func (s *Server) mountWebPrivateHandlers() {
	s.handle(http.MethodPost, "/api/web/enroll", reveal.ContentJSON, s.enrollWebDevice)
	s.handle(http.MethodPost, "/api/web/logout", reveal.ContentJSON, s.logoutWebDevice)
	s.handle(http.MethodPost, "/api/web/promote", reveal.ContentJSON, s.promoteWebDevice)
	// Sign-in recovery (CORE-1106): renew a lapsed session from the device
	// credential cookies, or enroll a new Manager Device with the admin password.
	s.handle(http.MethodPost, "/api/web/session/renew", reveal.ContentJSON, s.renewWebSession)
	s.handle(http.MethodPost, "/api/web/admin-signin", reveal.ContentJSON, s.adminSignInWebDevice)
	// Home/Strict sign-in security switch (CORE-1110): a Manager session plus
	// the admin password re-entered in the same request.
	s.handle(http.MethodPost, "/api/web/signin-security", reveal.ContentJSON, s.setSignInSecurityMode)
	// First-run setup bootstrap (ADR-0007 §2 / feature:first-run-setup-token):
	// redeems the one-time setup token printed to the server log, creating the
	// first MANAGER device+session without requiring an existing manager. Only
	// valid while setup is incomplete; the token is consumed on first use.
	s.handle(http.MethodPost, "/api/setup", reveal.ContentJSON, s.setupFirstRun)
	// Unauthenticated first-run probe: a brand-new, unenrolled
	// browser cannot reach the session-gated setupState query, so this bare GET
	// reports only whether the one-time setup token is still installed. Like
	// /health it carries no Reveal surface (empty class) and resolves no session,
	// so it is genuinely reachable pre-enrollment; it leaks only the single
	// boolean "is this a fresh server" and never the token value. It also
	// reports the sign-in security mode (CORE-1110), which reveals only which
	// sign-in path exists, so the enroll page can hide a path that is off.
	s.handle(http.MethodGet, "/api/setup", "", s.setupRequired)
}

// webDeviceCookieTTL is the device ID and device secret cookie lifetime: the
// 400-day browser maximum. They outlive the 30-day session so a browser whose
// session lapsed can still renew it (CORE-1106). Revoking the device ends them.
const webDeviceCookieTTL = 400 * 24 * time.Hour

// setWebSignInCookies sets the three browser credential cookies. The session
// cookie keeps the session's expiry, which the server enforces regardless.
func setWebSignInCookies(w http.ResponseWriter, r *http.Request, sessionID, sessionExpiresAt, deviceID, deviceSecret string) {
	expires, err := time.Parse(time.RFC3339, sessionExpiresAt)
	if err != nil {
		expires = time.Time{}
	}
	deviceExpires := time.Now().Add(webDeviceCookieTTL)
	setWebCredentialCookie(w, r, auth.SessionCookie, sessionID, expires)
	setWebCredentialCookie(w, r, auth.DeviceCookie, deviceID, deviceExpires)
	setWebCredentialCookie(w, r, auth.DeviceSecretCookie, deviceSecret, deviceExpires)
}

func rejectPublicPlainHTTPWebCredentials(w http.ResponseWriter, r *http.Request) bool {
	if requestIsHTTPS(r) || requestIsLAN(r) {
		return false
	}
	writeError(w, r, http.StatusForbidden, "secure connection or LAN required")
	return true
}

// enrollWebDevice is the browser variant of enrollDevice: the
// session and device credential are set as Secure/HttpOnly/SameSite=Strict
// cookies, so the device secret never enters JS-readable space — the response
// body deliberately omits it. The CSRF header is required even pre-auth so a
// cross-site page cannot burn pairing codes or attach a session to a victim.
func (s *Server) enrollWebDevice(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	if r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return
	}
	if rejectPublicPlainHTTPWebCredentials(w, r) {
		return
	}
	var req pairing.EnrollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	req.RemoteAddr = r.RemoteAddr
	// The browser enroll path must report its LAN-ness like the native enroll
	// handler does, so the remote-enrollment gate evaluates correctly
	// — without this, a LAN browser would read as off-LAN (IsLAN zero-value).
	req.IsLAN = requestIsLAN(r)
	resp, err := s.pairing.Enroll(r.Context(), req)
	if err != nil {
		notePairingRefusal(r, refusalOperationEnroll, err)
		switch {
		case errors.Is(err, pairing.ErrThrottled):
			writeError(w, r, http.StatusTooManyRequests, "pairing attempts throttled")
		case errors.Is(err, pairing.ErrInvalidCode):
			writeError(w, r, http.StatusUnauthorized, "invalid pairing code")
		case errors.Is(err, pairing.ErrInvalidRequest):
			writeError(w, r, http.StatusBadRequest, "invalid enrollment request")
		default:
			writeErrorCause(w, r, http.StatusInternalServerError, "enrollment failed", err)
		}
		return
	}
	setWebSignInCookies(w, r, resp.SessionID, resp.SessionExpiresAt, resp.DeviceID, resp.DeviceSecret)
	writeJSON(w, http.StatusCreated, map[string]string{
		"deviceID":         resp.DeviceID,
		"sessionExpiresAt": resp.SessionExpiresAt,
		"profileID":        resp.ProfileID,
		"deviceRole":       resp.DeviceRole,
	})
}

// setupFirstRun redeems the one-time setup token (ADR-0007 §2 /
// feature:first-run-setup-token). It bypasses the normal pairing-code flow to
// create the first MANAGER device+session, resolving the circular dependency on
// fresh servers. CSRF header required; token is single-use and cleared on
// success. While setup is pending, a request that passes lanSetupClaim may omit
// the token (CORE-1105); other requests still need it.
func (s *Server) setupFirstRun(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	if r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return
	}
	if rejectPublicPlainHTTPWebCredentials(w, r) {
		return
	}
	var req pairing.SetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	req.RemoteAddr = r.RemoteAddr
	req.LANClaim = lanSetupClaim(r)
	resp, err := s.pairing.Bootstrap(r.Context(), req)
	if err != nil {
		notePairingRefusal(r, refusalOperationSetup, err)
		switch {
		case errors.Is(err, pairing.ErrThrottled):
			writeError(w, r, http.StatusTooManyRequests, "setup attempts throttled")
		case errors.Is(err, pairing.ErrSetupTokenInvalid):
			writeError(w, r, http.StatusUnauthorized, "setup token invalid or already used")
		case errors.Is(err, pairing.ErrInvalidRequest):
			writeError(w, r, http.StatusBadRequest, "invalid setup request")
		default:
			writeErrorCause(w, r, http.StatusInternalServerError, "setup failed", err)
		}
		return
	}
	setWebSignInCookies(w, r, resp.SessionID, resp.SessionExpiresAt, resp.DeviceID, resp.DeviceSecret)
	writeJSON(w, http.StatusCreated, map[string]string{
		"deviceID":         resp.DeviceID,
		"profileID":        resp.ProfileID,
		"deviceRole":       resp.DeviceRole,
		"sessionExpiresAt": resp.SessionExpiresAt,
	})
}

// setupRequired is the unauthenticated first-run probe: it returns
// {"required": true} while the one-time setup token is still installed and
// {"required": false} once it has been consumed by POST /api/setup. No session,
// no CSRF header — a fresh, unenrolled browser must be able to call it to decide
// whether to route to the setup wizard. tokenRequired tells the wizard whether
// this request would need the setup token (false for a LAN request while setup
// is pending). It leaks only these booleans, never the token.
func (s *Server) setupRequired(w http.ResponseWriter, r *http.Request) {
	required := s.pairing.SetupRequired()
	mode, err := s.pairing.SignInSecurityMode(r.Context())
	if err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "setup status unavailable", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"required":      required,
		"tokenRequired": required && !lanSetupClaim(r),
		"securityMode":  string(mode),
	})
}

// lanSetupClaim reports whether a first-run setup request may omit the setup
// token (CORE-1105). The source must pass requestIsLAN, and the Host the
// browser addressed must be one a public website cannot rebind to a LAN
// address: an IP literal, localhost, or an mDNS .local name. Otherwise a page
// on attacker.example could repoint its own name at a fresh server and claim
// it through a visiting LAN browser, since the matching Host passes the
// same-origin checks.
func lanSetupClaim(r *http.Request) bool {
	return requestIsLAN(r) && setupHostResistsRebinding(r.Host)
}

// rejectRebindablePlainHTTPSignIn refuses admin-password sign-in over plain
// HTTP unless the request passes lanSetupClaim: a LAN source addressing a Host
// a public website cannot rebind. HTTPS is accepted as for other web
// credentials (CORE-1106).
func rejectRebindablePlainHTTPSignIn(w http.ResponseWriter, r *http.Request) bool {
	if requestIsHTTPS(r) || lanSetupClaim(r) {
		return false
	}
	writeError(w, r, http.StatusForbidden, "secure connection or LAN required")
	return true
}

func setupHostResistsRebinding(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if zone := strings.LastIndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local")
}

// promoteWebDevice upgrades the calling browser's device to MANAGER
// by redeeming a manager-role pairing code. Promotion is a
// privilege change, so the session ID and device secret rotate on success.
func (s *Server) promoteWebDevice(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	if r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return
	}
	if rejectPublicPlainHTTPWebCredentials(w, r) {
		return
	}
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	var req pairing.PromoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	req.RemoteAddr = r.RemoteAddr
	resp, err := s.pairing.Promote(r.Context(), scope, req)
	if err != nil {
		notePairingRefusal(r, refusalOperationPromote, err)
		switch {
		case errors.Is(err, pairing.ErrThrottled):
			writeError(w, r, http.StatusTooManyRequests, "pairing attempts throttled")
		case errors.Is(err, pairing.ErrInvalidCode):
			writeError(w, r, http.StatusUnauthorized, "invalid pairing code")
		case errors.Is(err, pairing.ErrRoleNotGranted):
			writeError(w, r, http.StatusForbidden, "pairing code does not grant the manager role")
		case errors.Is(err, pairing.ErrInvalidRequest):
			writeError(w, r, http.StatusBadRequest, "invalid promotion request")
		default:
			writeErrorCause(w, r, http.StatusInternalServerError, "promotion failed", err)
		}
		return
	}
	setWebSignInCookies(w, r, resp.SessionID, resp.SessionExpiresAt, resp.DeviceID, resp.DeviceSecret)
	writeJSON(w, http.StatusOK, resp)
}

// sessionRevoker is the logout revocation seam: deleting the session's auth
// row (so its next request fails auth) and clearing its outstanding
// Stream-Token ledger rows. The composition roots hand the server its
// JobStatsProvider as the backing *store.Store, which implements this via
// Store.TerminateSession; a wiring without the capability fails logout closed
// rather than reporting a revocation that never happened.
type sessionRevoker interface {
	TerminateSession(ctx context.Context, id store.SessionID) error
}

// logoutWebDevice ends the calling session server-side, then clears the
// browser's credential cookies. Cookie expiry alone is not revocation — a
// captured credential would stay valid for the session's remaining server-side
// lifetime — so logout authenticates the caller, revokes exactly the calling
// session (auth row plus its outstanding Stream-Token ledger activity), and
// only reports success once the revocation held. Other devices' sessions are
// untouched.
func (s *Server) logoutWebDevice(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	if r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return
	}
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	if scope.SessionID == "" {
		writeError(w, r, http.StatusUnauthorized, "session credential required")
		return
	}
	revoker, ok := s.jobStats.(sessionRevoker)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "session revocation unavailable")
		return
	}
	if err := revoker.TerminateSession(r.Context(), scope.SessionID); err != nil {
		writeErrorCause(w, r, http.StatusInternalServerError, "logout failed", err)
		return
	}
	expired := time.Unix(0, 0).UTC()
	for _, name := range []string{auth.SessionCookie, auth.DeviceCookie, auth.DeviceSecretCookie} {
		setWebCredentialCookie(w, r, name, "", expired)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged-out"})
}

// renewWebSession mints a new session for the calling browser from its device
// ID and device secret cookies (CORE-1106). The session cookie may be expired
// or missing: the device secret is the long-lived credential, and revoking the
// device ends it. The device secret does not rotate.
func (s *Server) renewWebSession(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	if r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return
	}
	if rejectPublicPlainHTTPWebCredentials(w, r) {
		return
	}
	cookie := func(name string) string {
		if c, err := r.Cookie(name); err == nil {
			return c.Value
		}
		return ""
	}
	req := pairing.RenewRequest{
		DeviceID:     cookie(auth.DeviceCookie),
		DeviceSecret: cookie(auth.DeviceSecretCookie),
		OldSessionID: cookie(auth.SessionCookie),
		RemoteAddr:   r.RemoteAddr,
	}
	resp, err := s.pairing.RenewSession(r.Context(), req)
	if err != nil {
		notePairingRefusal(r, refusalOperationSessionRenew, err)
		switch {
		case errors.Is(err, pairing.ErrThrottled):
			writeError(w, r, http.StatusTooManyRequests, "renewal attempts throttled")
		case errors.Is(err, pairing.ErrSessionRenewRefused):
			writeError(w, r, http.StatusUnauthorized, "session renewal refused")
		default:
			writeErrorCause(w, r, http.StatusInternalServerError, "session renewal failed", err)
		}
		return
	}
	setWebSignInCookies(w, r, resp.SessionID, resp.SessionExpiresAt, req.DeviceID, req.DeviceSecret)
	writeJSON(w, http.StatusOK, resp)
}

// adminSignInWebDevice enrolls the calling browser as a new Manager Device on
// the owner Profile when the admin password verifies (CORE-1106). Every
// refusal other than a throttle is the same 401, so a caller learns nothing
// about whether a password or an owner exists.
func (s *Server) adminSignInWebDevice(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	if r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return
	}
	if rejectRebindablePlainHTTPSignIn(w, r) {
		return
	}
	var req pairing.AdminSignInRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	req.RemoteAddr = r.RemoteAddr
	resp, err := s.pairing.AdminSignIn(r.Context(), req)
	if err != nil {
		notePairingRefusal(r, refusalOperationAdminSignIn, err)
		switch {
		case errors.Is(err, pairing.ErrThrottled):
			writeError(w, r, http.StatusTooManyRequests, "sign-in attempts throttled")
		case errors.Is(err, pairing.ErrSignInFailed):
			writeError(w, r, http.StatusUnauthorized, "sign-in failed")
		case errors.Is(err, pairing.ErrInvalidRequest):
			writeError(w, r, http.StatusBadRequest, "invalid sign-in request")
		default:
			writeErrorCause(w, r, http.StatusInternalServerError, "sign-in failed", err)
		}
		return
	}
	setWebSignInCookies(w, r, resp.SessionID, resp.SessionExpiresAt, resp.DeviceID, resp.DeviceSecret)
	writeJSON(w, http.StatusCreated, map[string]string{
		"deviceID":         resp.DeviceID,
		"sessionExpiresAt": resp.SessionExpiresAt,
		"profileID":        resp.ProfileID,
		"deviceRole":       resp.DeviceRole,
	})
}

// signInSecurityRequest is the body of POST /api/web/signin-security.
type signInSecurityRequest struct {
	Mode          string `json:"mode"`
	AdminPassword string `json:"adminPassword"`
}

// setSignInSecurityMode switches between Home and Strict sign-in security
// (ADR-0007, CORE-1110). The caller must be a Manager session and re-enter the
// admin password; a wrong or missing password is 401 and counts against the
// session's admin re-auth lane, which answers 429 once it locks.
func (s *Server) setSignInSecurityMode(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	if r.Header.Get(auth.CSRFHeader) == "" {
		writeError(w, r, http.StatusForbidden, "missing CSRF header")
		return
	}
	if rejectPublicPlainHTTPWebCredentials(w, r) {
		return
	}
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	var req signInSecurityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mode := store.SignInSecurityMode(req.Mode)
	if _, err := s.auth.ChangeSignInSecurityMode(r.Context(), scope, mode, req.AdminPassword); err != nil {
		var invalid store.ValidationError
		switch {
		case errors.As(err, &invalid):
			writeError(w, r, http.StatusBadRequest, "securityMode must be home or strict")
		case errors.Is(err, auth.ErrNotManager):
			writeError(w, r, http.StatusForbidden, "a manager device is required")
		case errors.Is(err, auth.ErrAdminLocked):
			writeError(w, r, http.StatusTooManyRequests, "admin password attempts throttled")
		case errors.Is(err, auth.ErrAdminPassword):
			writeError(w, r, http.StatusUnauthorized, "admin password rejected")
		default:
			writeErrorCause(w, r, http.StatusInternalServerError, "could not change sign-in security", err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"securityMode": string(mode)})
}
